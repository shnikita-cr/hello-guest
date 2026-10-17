package server

import (
	"archive/zip"
	"bytes"
	"io"
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
