package main

import (
	"bytes"
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
	printf("бэкапы: каждые 5 минут → %s\n", bk.DestDir)
	listener, port := listen(*addr)
	var ips []string
	if v := parseIPs(*advertiseIP); len(v) > 0 {
		ips = v
	} else {
		ips = availableIPs()
	}
	host := *name
	// в mDNS при автоопределении уходит только основной адрес,
	// явный список --ip публикуется целиком
	mdns := ips[:1]
	if *advertiseIP != "" {
		mdns = ips
	}
	printf("hello-guest запущен\n")

	// TLS на 8443: камера (сканер QR) работает только в secure context
	certFile, keyFile, err := server.EnsureCert(*data)
	if err != nil {
		printf("TLS недоступен: %v\n", err)
	} else {
		printf("  https://%s:8443\n", *name+".local")
		for _, x := range ips {
			printf("  https://%s:8443\n", x)
		}
		go func() {
			tlsSrv := &http.Server{Addr: ":8443", Handler: srv.Handler()}
			if err := tlsSrv.ListenAndServeTLS(certFile, keyFile); err != nil {
				printf("TLS: %v\n", err)
			}
		}()
	}

	done := make(chan struct{})
	if sd, err := zeroconf.RegisterProxy(*name, "_http._tcp", "local.", port, host, mdns, nil, nil); err == nil {
		go func() { <-done; sd.Shutdown() }()
	} else {
		printf("mDNS недоступен: %v\n", err)
	}

	go func() {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
		<-ch
		close(done)
		listener.Close()
		os.Exit(0)
	}()

	if err := http.Serve(listener, srv.Handler()); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
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
	out := make([]string, 0, len(args))
	for _, a := range args {
		if a == "-d" || a == "--d" || strings.HasPrefix(a, "-d=") || strings.HasPrefix(a, "--d=") {
			continue
		}
		out = append(out, a)
	}
	return out
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
