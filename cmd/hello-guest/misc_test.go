package main

import (
	"bytes"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestPrintfToLogAndStdout(t *testing.T) {
	var buf bytes.Buffer
	logfile, daemonMode = &buf, true
	defer func() { logfile, daemonMode = nil, false }()

	printf("привет %d\n", 42) // daemon: только в журнал
	if buf.String() != "привет 42\n" {
		t.Fatalf("журнал: %q", buf.String())
	}
	daemonMode = false
	printf("и в консоль\n") // обычный режим: в журнал и в stdout
	if buf.String() != "привет 42\nи в консоль\n" {
		t.Fatalf("журнал после второго: %q", buf.String())
	}
	logfile = nil
	printf("только stdout\n") // журнала нет — пишем только в консоль
	if !strings.HasPrefix(buf.String(), "привет") {
		t.Fatalf("буфер журнала изменён: %q", buf.String())
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("сбой записи") }

func TestTsWriter(t *testing.T) {
	var b bytes.Buffer
	n, err := tsWriter{&b}.Write([]byte("x"))
	if err != nil || n != 1 {
		t.Fatalf("write: n=%d err=%v", n, err)
	}
	if !regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2} x$`).MatchString(b.String()) {
		t.Fatalf("метка времени: %q", b.String())
	}
	if _, err := (tsWriter{failingWriter{}}).Write([]byte("y")); err == nil {
		t.Fatal("ошибка записи должна всплыть")
	}
}

func TestDrainLog(t *testing.T) {
	p := filepath.Join(t.TempDir(), "l.log")
	var off int64
	var seen []byte

	drainLog(p, &off, &seen) // файла ещё нет — тихо выходим
	if len(seen) != 0 {
		t.Fatalf("нет файла: %q", seen)
	}
	if err := os.WriteFile(p, []byte("первое\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	drainLog(p, &off, &seen)
	if string(seen) != "первое\n" || off != int64(len("первое\n")) {
		t.Fatalf("первая порция: %q off=%d", seen, off)
	}
	drainLog(p, &off, &seen) // новых данных нет
	if string(seen) != "первое\n" {
		t.Fatalf("повтор не должен дублировать: %q", seen)
	}
	f, _ := os.OpenFile(p, os.O_WRONLY|os.O_APPEND, 0o644)
	f.WriteString("второе\n")
	f.Close()
	drainLog(p, &off, &seen)
	if string(seen) != "первое\nвторое\n" {
		t.Fatalf("вторая порция: %q", seen)
	}
}

func TestStripD(t *testing.T) {
	got := stripD([]string{"-d", "--d", "-data", "./d", "-addr", ":8080"})
	want := "-data ./d -addr :8080"
	if s := strings.Join(got, " "); s != want {
		t.Fatalf("stripD = %q, ждали %q", s, want)
	}
}

func TestListenAndIPs(t *testing.T) {
	l, port := listen("127.0.0.1:0")
	if port <= 0 {
		t.Fatalf("port=%d", port)
	}
	l.Close()

	ips := availableIPs()
	if len(ips) == 0 {
		t.Fatal("availableIPs пуст")
	}
	for _, s := range ips {
		if net.ParseIP(s) == nil {
			t.Fatalf("не ip: %q", s)
		}
	}
	gatherIPs() // без паники в любом окружении
}

func TestDaemonizeSetsid(t *testing.T) {
	c := exec.Command("true")
	daemonize(c)
	if c.SysProcAttr == nil {
		t.Fatal("SysProcAttr не задан")
	}
}

func TestMainVersionFlag(t *testing.T) {
	old := os.Args
	os.Args = []string{"hello-guest", "-version"}
	defer func() { os.Args = old }()
	// main определяет флаги глобально — вызывать строго один раз за процесс
	main() // печатает версию и выходит до каких-либо побочных эффектов
}

func TestListenDefault(t *testing.T) {
	l, port := listen("")
	if port != 80 && port != 8080 {
		t.Fatalf("дефолтный порт: %d", port)
	}
	l.Close()
}
