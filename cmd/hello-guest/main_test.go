package main

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestShortVersion(t *testing.T) {
	cases := map[string]string{
		"v1.5.0":        "1.5",
		"v1.5.0-dirty":  "1.5",
		"1.5.0":         "1.5",
		"v1.6":          "1.6",
		"dev":           "dev",
		"8fb9321":       "8fb9321",
		"v1.5.0-3-gabc": "1.5",
	}
	for in, want := range cases {
		if got := shortVersion(in); got != want {
			t.Errorf("shortVersion(%q) = %q, ждали %q", in, got, want)
		}
	}
}

func TestStripFlags(t *testing.T) {
	got := stripFlags([]string{"-d", "--open", "--addr", ":8080", "-open=1"}, "-d", "--d", "-open", "--open")
	want := "--addr :8080"
	if s := strings.Join(got, " "); s != want {
		t.Fatalf("stripFlags = %q, ждали %q", s, want)
	}
}

func TestRunningStatus(t *testing.T) {
	dir := t.TempDir()
	if _, ok := runningStatus(dir); ok {
		t.Fatal("без файла status.port не должно считаться работающим")
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/version", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"version":"test"}`))
	})
	go http.Serve(ln, mux)
	port := ln.Addr().(*net.TCPAddr).Port
	os.WriteFile(filepath.Join(dir, "status.port"), []byte(fmt.Sprintf("%d", port)), 0o644)
	u, ok := runningStatus(dir)
	if !ok || !strings.HasSuffix(u, "/status") {
		t.Fatalf("живой сервер: ok=%v url=%q", ok, u)
	}

	ln.Close()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, ok := runningStatus(dir); !ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("после остановки сервера файл status.port не должен давать 살아")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestParseIPs(t *testing.T) {
	got := parseIPs(" 192.168.1.10 ,10.0.0.5,, ")
	want := []string{"192.168.1.10", "10.0.0.5"}
	if len(got) != len(want) {
		t.Fatalf("parseIPs = %v, ждали %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("parseIPs = %v, ждали %v", got, want)
		}
	}
	if len(parseIPs("")) != 0 {
		t.Fatal("пустая строка должна давать пустой список")
	}
}

func TestChooseIP(t *testing.T) {
	ip := func(s string) net.IP { return net.ParseIP(s) }
	bridge := ipFact{ip("192.168.139.3"), true} // vmware-мост раньше en0 в списке
	lan := ipFact{ip("192.168.35.2"), true}
	vpn := ipFact{ip("10.8.0.2"), false} // utun: без broadcast

	cases := []struct {
		name   string
		route  net.IP
		all    []ipFact
		expect string
	}{
		{"маршрут по умолчанию на lan", ip("192.168.35.2"), []ipFact{bridge, lan, vpn}, "192.168.35.2"},
		{"маршрут через vpn — берём lan", ip("10.8.0.2"), []ipFact{lan, vpn}, "192.168.35.2"},
		{"маршрут публичный — берём частный", ip("8.8.8.8"), []ipFact{bridge, lan}, "192.168.139.3"},
		{"без маршрута — первый частный broadcast", nil, []ipFact{bridge, lan}, "192.168.139.3"},
		{"только vpn-адреса — берём частный", ip("10.8.0.2"), []ipFact{vpn}, "10.8.0.2"},
		{"ничего нет — лупбэк", nil, nil, "127.0.0.1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := chooseIP(c.route, c.all).String()
			if got != c.expect {
				t.Fatalf("chooseIP = %s, ждали %s", got, c.expect)
			}
		})
	}
}
