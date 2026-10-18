package main

import (
	"bytes"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/grandcat/zeroconf"

	"hello-guest/internal/backup"
	"hello-guest/internal/server"
	"hello-guest/internal/store"
)

var version = "dev"

// daemonMode: фоновый ребёнок пишет стартовый вывод сам, в файл журнала
// с меткой времени; обычный режим — в консоль.
var daemonMode bool
var logfile io.Writer

func printf(format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	if logfile != nil {
		io.WriteString(logfile, msg)
	}
	if !daemonMode {
		os.Stdout.WriteString(msg)
	}
}

func main() {
	log.SetFlags(0) // метку времени ставит tsWriter в фоновом журнале
	addr := flag.String("addr", "", "listen address (default :80, fallback :8080)")
	data := flag.String("data", "./data", "data directory")
	name := flag.String("name", "hello-guest", "mDNS hostname (<name>.local)")
	showVer := flag.Bool("version", false, "print version and exit")
	detach := flag.Bool("d", false, "show startup info in console, then run in background")
	backupDir := flag.String("backup-dir", "../../copy-data", "backup directory (relative to data dir)")
	advertiseIP := flag.String("ip", "", "advertised IP for banner and mDNS (default: auto-detect)")
	openStatus := flag.Bool("open", false, "open the status page in the browser after start")
	flag.Parse()
	if *showVer {
		fmt.Println(version)
		return
	}
	if *detach {
		runBackground(*data, stripD(os.Args[1:]))
		return
	}

	if err := os.MkdirAll(*data, 0o755); err != nil {
		log.Fatal(err)
	}
	if os.Getenv("HG_DAEMON") == "1" {
		daemonMode = true
		if lf, err := os.OpenFile(filepath.Join(*data, "hello-guest.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
			logfile = tsWriter{lf}
			log.SetOutput(logfile)
		}
	}

	// синглтон: экземпляр уже работает — показать его экран статуса и выйти
	if u, ok := runningStatus(*data); ok {
		printf("уже запущен — открываю %s\n", u)
		openBrowser(u)
		return
	}

	cfg, pin, err := server.LoadOrCreateConfig(*data)
	if err != nil {
		log.Fatal(err)
	}
	if pin != "" {
		printf("\n  PIN: %s\n\n", pin)
	}

	st, err := store.Open(*data + "/hello-guest.ndjson")
	if err != nil {
		log.Fatal(err)
	}
	defer st.Close()

	server.Version = version
	srv := server.New(cfg, st, *data)

	// бэкапы каталога данных: каждые 5 минут zip, хранятся последние 20
	bk := backup.Runner{DataDir: *data, DestDir: *backupDir, Keep: 20, Every: 5 * time.Minute}
	if !filepath.IsAbs(bk.DestDir) {
		bk.DestDir = filepath.Join(*data, bk.DestDir)
	}
	bk.Start(nil, func(err error) { log.Printf("бэкап: %v", err) })
	srv.TriggerBackup = func() (string, error) { return bk.Manual(time.Now()) }
	srv.BackupTimes = bk.Times
	printf("бэкапы: каждые 5 минут → %s\n", bk.DestDir)
	listener, port := listen(*addr)
	os.WriteFile(filepath.Join(*data, "status.port"), []byte(fmt.Sprintf("%d", port)), 0o644)
	var ips []string
	if v := parseIPs(*advertiseIP); len(v) > 0 {
		ips = v
	} else {
		ips = availableIPs()
	}
	srv.StatusIPs = ips
	host := *name
	// в mDNS при автоопределении уходит только основной адрес,
	// явный список --ip публикуется целиком
	mdns := ips[:1]
	if *advertiseIP != "" {
		mdns = ips
	}
	printf("hello-guest запущен\n")

	// TLS на 8443: камера (сканер QR) работает только в secure context
	var tlsL net.Listener
	certFile, keyFile, err := server.EnsureCert(*data)
	if err != nil {
		printf("TLS недоступен: %v\n", err)
	} else {
		printf("  https://%s:8443\n", *name+".local")
		for _, x := range ips {
			printf("  https://%s:8443\n", x)
		}
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			printf("TLS: %v\n", err)
		} else if l, err := net.Listen("tcp", ":8443"); err != nil {
			printf("TLS: %v\n", err)
		} else {
			tlsL = l
			go func() {
				tlsSrv := &http.Server{Handler: srv.Handler()}
				if err := tlsSrv.Serve(tls.NewListener(l, &tls.Config{Certificates: []tls.Certificate{cert}})); err != nil && !errors.Is(err, net.ErrClosed) {
					printf("TLS: %v\n", err)
				}
			}()
		}
	}

	done := make(chan struct{})
	if sd, err := zeroconf.RegisterProxy(*name, "_http._tcp", "local.", port, host, mdns, nil, nil); err == nil {
		go func() { <-done; sd.Shutdown() }()
	} else {
		printf("mDNS недоступен: %v\n", err)
	}

	// стоп и перезапуск — кнопками экрана статуса (только localhost)
	srv.OnStop = func() {
		printf("останавливаюсь по запросу с экрана статуса\n")
		time.Sleep(250 * time.Millisecond) // ответ уходит клиенту
		listener.Close()
		if tlsL != nil {
			tlsL.Close()
		}
		st.Close()
		os.Exit(0)
	}
	srv.OnRestart = func() {
		printf("перезапускаю по запросу с экрана статуса\n")
		listener.Close()
		if tlsL != nil {
			tlsL.Close()
		}
		cmd := exec.Command(os.Args[0], stripFlags(os.Args[1:], "-d", "--d", "-open", "--open")...)
		f, _ := os.OpenFile(filepath.Join(*data, "hello-guest.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		cmd.Stdout = nil
		if f != nil {
			cmd.Stderr = f
		}
		cmd.Env = append(os.Environ(), "HG_DAEMON=1")
		daemonize(cmd)
		if err := cmd.Start(); err != nil {
			log.Printf("рестарт: %v", err)
		}
		if f != nil {
			f.Close()
		}
		st.Close()
		os.Exit(0)
	}
	if *openStatus {
		openBrowser(fmt.Sprintf("http://127.0.0.1:%d/status", port))
	}

	go func() {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
		<-ch
		close(done)
		listener.Close()
		os.Exit(0)
	}()

	if err := http.Serve(listener, srv.Handler()); err != nil && !errors.Is(err, net.ErrClosed) {
		log.Fatal(err)
	}
	// слушатель закрыли стоп/рестарт/сигнал — завершает сам обработчик,
	// здесь только дожить (иначе процесс умрёт до ответа клиенту)
	select {}
}

// runBackground запускает копию процесса в фоне (без -d), показывает
// её стартовый вывод в этой консоли и завершается — как docker run -d.
func runBackground(data string, args []string) {
	if err := os.MkdirAll(data, 0o755); err != nil {
		log.Fatal(err)
	}
	logPath := filepath.Join(data, "hello-guest.log")
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		log.Fatal(err)
	}
	off, _ := f.Seek(0, io.SeekEnd)
	cmd := exec.Command(os.Args[0], args...)
	// ребёнок сам пишет в файл журнала (HG_DAEMON): stdout в /dev/null,
	// stderr напрямую в файл — никаких pipe, которые умирают с родителем
	cmd.Stdout = nil
	cmd.Stderr = f
	cmd.Env = append(os.Environ(), "HG_DAEMON=1")
	daemonize(cmd)
	if err := cmd.Start(); err != nil {
		log.Fatal(err)
	}
	f.Close()
	fmt.Printf("в фоне: PID %d, лог %s\n", cmd.Process.Pid, logPath)

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var seen []byte
	deadline := time.After(15 * time.Second)
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case err := <-done:
			drainLog(logPath, &off, &seen)
			if err != nil {
				os.Exit(1)
			}
			return
		case <-deadline:
			return
		case <-tick.C:
			drainLog(logPath, &off, &seen)
			if bytes.Contains(seen, []byte("запущен")) {
				time.Sleep(700 * time.Millisecond)
				drainLog(logPath, &off, &seen)
				return
			}
		}
	}
}

// tsWriter добавляет метку времени в начало каждой записи журнала.
type tsWriter struct{ w io.Writer }

func (t tsWriter) Write(p []byte) (int, error) {
	stamp := []byte(time.Now().Format("2006-01-02 15:04:05 "))
	if _, err := t.w.Write(stamp); err != nil {
		return 0, err
	}
	return t.w.Write(p)
}

func drainLog(path string, off *int64, seen *[]byte) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.Size() <= *off {
		return
	}
	if _, err := f.Seek(*off, io.SeekStart); err != nil {
		return
	}
	b, err := io.ReadAll(f)
	if err != nil {
		return
	}
	*off += int64(len(b))
	*seen = append(*seen, b...)
	os.Stdout.Write(b)
}

func stripD(args []string) []string {
	return stripFlags(args, "-d", "--d")
}

func listen(want string) (net.Listener, int) {
	if want != "" {
		l, err := net.Listen("tcp", want)
		if err != nil {
			log.Fatal(err)
		}
		return l, l.Addr().(*net.TCPAddr).Port
	}
	if l, err := net.Listen("tcp", ":80"); err == nil {
		return l, 80
	} else {
		fmt.Println("порт 80 занят правами — поднимаюсь на 8080")
	}
	l, err := net.Listen("tcp", ":8080")
	if err != nil {
		log.Fatal(err)
	}
	return l, 8080
}

// runningStatus: порт занят — не я ли сам? Файл status.port и живой
// /api/version на нём означают «второй экземпляр уже работает».
func runningStatus(data string) (string, bool) {
	b, err := os.ReadFile(filepath.Join(data, "status.port"))
	if err != nil {
		return "", false
	}
	port := strings.TrimSpace(string(b))
	if port == "" {
		return "", false
	}
	url := "http://127.0.0.1:" + port
	hc := http.Client{Timeout: 1500 * time.Millisecond}
	resp, err := hc.Get(url + "/api/version")
	if err != nil || resp.StatusCode != 200 {
		return "", false
	}
	resp.Body.Close()
	return url + "/status", true
}

func openBrowser(url string) {
	var c *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		c = exec.Command("open", url)
	case "windows":
		c = exec.Command("cmd", "/c", "start", "", url)
	default:
		c = exec.Command("xdg-open", url)
	}
	c.Start()
}

func stripFlags(args []string, drop ...string) []string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		keep := true
		for _, d := range drop {
			if a == d || strings.HasPrefix(a, d+"=") {
				keep = false
				break
			}
		}
		if keep {
			out = append(out, a)
		}
	}
	return out
}

// parseIPs: несколько адресов для --ip через запятую: --ip 192.168.1.10,10.0.0.5
func parseIPs(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ipFact — IPv4-адрес интерфейса; broadcast false для point-to-point (vpn, tun).
type ipFact struct {
	ip        net.IP
	broadcast bool
}

// gatherIPs: маршрут по умолчанию + IPv4-адреса поднятых не-loopback интерфейсов.
func gatherIPs() (net.IP, []ipFact) {
	// адрес источника маршрута по умолчанию: пакет не отправляется, читается только таблица роутинга
	var route net.IP
	if c, err := net.Dial("udp4", "1.1.1.1:53"); err == nil {
		if u, ok := c.LocalAddr().(*net.UDPAddr); ok {
			route = u.IP
		}
		c.Close()
	}
	var all []ipFact
	ifaces, _ := net.Interfaces()
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok || ipn.IP.To4() == nil {
				continue
			}
			all = append(all, ipFact{ipn.IP, iface.Flags&net.FlagBroadcast != 0})
		}
	}
	return route, all
}

// availableIPs: все адреса, на которых приложение доступно; первый — предпочтительный.
func availableIPs() []string {
	route, all := gatherIPs()
	prim := chooseIP(route, all)
	out := []string{prim.String()}
	seen := map[string]bool{prim.String(): true}
	for _, f := range all {
		s := f.ip.String()
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// chooseIP: частный адрес интерфейса маршрута по умолчанию (но не vpn-tun),
// иначе первый частный адрес обычного интерфейса, иначе сам маршрутный,
// иначе лупбэк. Ручная правка — флаг --ip.
func chooseIP(route net.IP, all []ipFact) net.IP {
	for _, f := range all {
		if route != nil && f.broadcast && f.ip.Equal(route) && f.ip.IsPrivate() {
			return route
		}
	}
	for _, f := range all {
		if f.broadcast && f.ip.IsPrivate() {
			return f.ip
		}
	}
	if route != nil && !route.IsLoopback() {
		return route
	}
	for _, f := range all {
		if f.ip.IsPrivate() {
			return f.ip
		}
	}
	return net.IPv4(127, 0, 0, 1)
}
