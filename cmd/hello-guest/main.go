package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/grandcat/zeroconf"

	"hello-guest/internal/server"
	"hello-guest/internal/store"
)

var version = "dev"

func main() {
	addr := flag.String("addr", "", "listen address (default :80, fallback :8080)")
	data := flag.String("data", "./data", "data directory")
	name := flag.String("name", "hello-guest", "mDNS hostname (<name>.local)")
	showVer := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *showVer {
		fmt.Println(version)
		return
	}

	if err := os.MkdirAll(*data, 0o755); err != nil {
		log.Fatal(err)
	}

	cfg, pin, err := server.LoadOrCreateConfig(*data)
	if err != nil {
		log.Fatal(err)
	}
	if pin != "" {
		fmt.Printf("\n  PIN: %s\n\n", pin)
	}

	st, err := store.Open(*data + "/hello-guest.ndjson")
	if err != nil {
		log.Fatal(err)
	}
	defer st.Close()

	srv := server.New(cfg, st)
	listener, port := listen(*addr)
	actual := fmt.Sprintf("%s:%d", hostIP(), port)
	fmt.Printf("hello-guest запущен\n  http://%s.local\n  http://%s\n", *name, actual)

	done := make(chan struct{})
	if sd, err := zeroconf.Register(*name, "_http._tcp", "local.", port, nil, nil); err == nil {
		go func() { <-done; sd.Shutdown() }()
	} else {
		fmt.Printf("mDNS недоступен: %v\n", err)
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

func hostIP() string {
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok && !ipn.IP.IsLoopback() && ipn.IP.To4() != nil {
			return ipn.IP.String()
		}
	}
	return "127.0.0.1"
}

var _ = time.Now
