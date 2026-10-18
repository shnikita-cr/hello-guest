package server

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hello-guest/internal/store"
)

func TestAdminReport(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "j.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.Upsert([]store.Guest{{ID: "g1", Event: "e1", Name: "Иван Иванов", TicketNumber: "117726839:97059462"}})
	st.Checkin("e1", "g1", "tab-1", "qr", time.Now())

	cfg := &Config{PinHash: hashPin("123456", "salt"), PinSalt: "salt"}
	s := New(cfg, st, dir)

	req := httptest.NewRequest("GET", "/api/admin/report?event=e1", nil)
	req.Header.Set("X-Pin", "123456")
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.HasPrefix(body, "\xEF\xBB\xBF") {
		t.Fatal("report must start with UTF-8 BOM")
	}
	for _, want := range []string{"Иван Иванов", "QR", "1000970594621", "Имя;Email"} {
		if !strings.Contains(body, want) {
			t.Fatalf("report misses %q: %q", want, body)
		}
	}
	files, err := os.ReadDir(filepath.Join(dir, "reports"))
	if err != nil || len(files) != 1 || !strings.HasSuffix(files[0].Name(), ".csv") {
		t.Fatalf("saved reports: %v err=%v", files, err)
	}

	reqX := httptest.NewRequest("GET", "/api/admin/report?format=xlsx&event=e1", nil)
	reqX.Header.Set("X-Pin", "123456")
	rrX := httptest.NewRecorder()
	s.Handler().ServeHTTP(rrX, reqX)
	if rrX.Code != 200 {
		t.Fatalf("xlsx code=%d body=%s", rrX.Code, rrX.Body.String())
	}
	if !strings.HasPrefix(rrX.Body.String(), "PK\x03\x04") {
		t.Fatal("xlsx must be a zip archive")
	}
	zr, err := zip.NewReader(bytes.NewReader(rrX.Body.Bytes()), int64(rrX.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range zr.File {
		if f.Name == "xl/worksheets/sheet1.xml" {
			rc, _ := f.Open()
			var sb strings.Builder
			io.Copy(&sb, rc)
			rc.Close()
			if !strings.Contains(sb.String(), "Иван Иванов") {
				t.Fatalf("sheet misses guest: %q", sb.String())
			}
		}
	}
	files, err = os.ReadDir(filepath.Join(dir, "reports"))
	if err != nil || len(files) != 2 {
		t.Fatalf("after xlsx saved reports: %v err=%v", files, err)
	}

	req2 := httptest.NewRequest("GET", "/api/admin/report?event=e1", nil)
	rr2 := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr2, req2)
	if rr2.Code != 401 {
		t.Fatalf("without pin must be 401, got %d", rr2.Code)
	}
}

func TestStatusLocalOnly(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "j.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	cfg := &Config{PinHash: hashPin("123456", "salt"), PinSalt: "salt", PinPlain: "123456"}
	s := New(cfg, st, dir)

	// чужой адрес — закрыто
	req := httptest.NewRequest("GET", "/api/status", nil)
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("LAN must be 403, got %d", rr.Code)
	}

	// loopback, но через cloudflared (есть заголовки крайнего) — закрыто
	reqT := httptest.NewRequest("GET", "/api/status", nil)
	reqT.RemoteAddr = "127.0.0.1:4444"
	reqT.Header.Set("Cf-Ray", "abc")
	rrT := httptest.NewRecorder()
	s.Handler().ServeHTTP(rrT, reqT)
	if rrT.Code != http.StatusForbidden {
		t.Fatalf("tunnel must be 403, got %d", rrT.Code)
	}

	// сам ноутбук — PIN виден
	reqL := httptest.NewRequest("GET", "/api/status", nil)
	reqL.RemoteAddr = "127.0.0.1:4444"
	rrL := httptest.NewRecorder()
	s.Handler().ServeHTTP(rrL, reqL)
	if rrL.Code != 200 || !strings.Contains(rrL.Body.String(), `"pin":"123456"`) {
		t.Fatalf("local: code=%d body=%s", rrL.Code, rrL.Body.String())
	}
}

func TestStatusBackupAndPin(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "j.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	cfg := &Config{PinHash: hashPin("123456", "salt"), PinSalt: "salt"}
	s := New(cfg, st, dir)
	post := func(path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", path, strings.NewReader(body))
		req.RemoteAddr = "127.0.0.1:4444"
		rr := httptest.NewRecorder()
		s.Handler().ServeHTTP(rr, req)
		return rr
	}

	if rr := post("/api/admin/backup", ""); rr.Code != http.StatusNotImplemented {
		t.Fatalf("no hook must be 501, got %d", rr.Code)
	}
	s.TriggerBackup = func() (string, error) { return "/tmp/a.zip", nil }
	if rr := post("/api/admin/backup", ""); rr.Code != 200 || !strings.Contains(rr.Body.String(), "/tmp/a.zip") {
		t.Fatalf("backup: code=%d body=%s", rr.Code, rr.Body.String())
	}

	if rr := post("/api/status/pin", `{"pin":"000000"}`); rr.Code != http.StatusBadRequest {
		t.Fatalf("wrong pin must be 400, got %d", rr.Code)
	}
	if rr := post("/api/status/pin", `{"pin":"123456"}`); rr.Code != 200 {
		t.Fatalf("pin save: code=%d body=%s", rr.Code, rr.Body.String())
	}
	c, _, err := LoadOrCreateConfig(dir)
	if err != nil || c.PinPlain != "123456" {
		t.Fatalf("pin_plain not saved: err=%v", err)
	}
}

func TestStatusStopRestart(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "j.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	s := New(&Config{PinHash: hashPin("1", "salt"), PinSalt: "salt"}, st, dir)
	stopped := make(chan bool, 1)
	restarted := make(chan bool, 1)
	s.OnStop = func() { stopped <- true }
	s.OnRestart = func() { restarted <- true }

	post := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", path, nil)
		req.RemoteAddr = "127.0.0.1:4444"
		rr := httptest.NewRecorder()
		s.Handler().ServeHTTP(rr, req)
		return rr
	}
	if rr := post("/api/admin/stop"); rr.Code != 200 {
		t.Fatalf("stop: %d", rr.Code)
	}
	if rr := post("/api/admin/restart"); rr.Code != 200 {
		t.Fatalf("restart: %d", rr.Code)
	}
	for _, ch := range []chan bool{stopped, restarted} {
		select {
		case <-ch:
		case <-time.After(time.Second):
			t.Fatal("OnStop/OnRestart не вызваны")
		}
	}

	get := httptest.NewRequest("GET", "/api/admin/stop", nil)
	get.RemoteAddr = "127.0.0.1:4444"
	rrG := httptest.NewRecorder()
	s.Handler().ServeHTTP(rrG, get)
	if rrG.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET stop must be 405, got %d", rrG.Code)
	}
}

func TestClientEvents(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "j.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	s := New(&Config{PinHash: hashPin("1", "salt"), PinSalt: "salt"}, st, dir)

	touch := func(ip string) {
		r := httptest.NewRequest("GET", "/api/pin", nil)
		r.RemoteAddr = ip + ":5000"
		s.touch(r)
	}

	touch("10.0.0.7")
	evs := s.clientEvents()
	if len(evs) != 1 || evs[0].Kind != "join" || evs[0].IP != "10.0.0.7" {
		t.Fatalf("после первого touch: %+v", evs)
	}
	touch("10.0.0.7")
	if evs := s.clientEvents(); len(evs) != 1 {
		t.Fatalf("повторный touch не должен добавлять событие: %+v", evs)
	}

	// протух (>10с, но в пределах 5 мин) → leave при подсчёте клиентов
	s.seenMu.Lock()
	s.seen["10.0.0.7"] = time.Now().Add(-time.Minute)
	s.seenMu.Unlock()
	if n := s.clients(); n != 0 {
		t.Fatalf("клиентов после протухания: %d", n)
	}
	evs = s.clientEvents()
	if len(evs) != 2 || evs[0].Kind != "leave" {
		t.Fatalf("после протухания: %+v", evs)
	}

	// древний хвост при открытии страницы молча выкидывается
	s.seenMu.Lock()
	s.seen["10.0.0.8"] = time.Now().Add(-10 * time.Minute)
	s.seenMu.Unlock()
	s.clients()
	if evs := s.clientEvents(); len(evs) != 2 {
		t.Fatalf("древний хвост не должен попадать в события: %+v", evs)
	}
}
