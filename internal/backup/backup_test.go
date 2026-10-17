package backup

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBackupSkipRotateAndContent(t *testing.T) {
	data := t.TempDir()
	dest := filepath.Join(data, "copies") // папка назначения внутри данных — проверка пропуска
	journal := filepath.Join(data, "hello-guest.ndjson")
	if err := os.WriteFile(journal, []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &Runner{DataDir: data, DestDir: dest, Keep: 20, Every: time.Minute}
	now := time.Now()

	// первый бэкап создаётся
	p, err := r.BackupOnce(now)
	if err != nil || p == "" {
		t.Fatalf("first backup: %q %v", p, err)
	}
	// содержимое не менялось — пропуск
	if p2, _ := r.BackupOnce(now); p2 != "" {
		t.Fatalf("unchanged data must be skipped, got %q", p2)
	}
	// 22 изменения подряд → всего должно остаться 20 архивов
	for i := 0; i < 22; i++ {
		if err := os.WriteFile(journal, []byte(strings.Repeat("x", i+2)), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := r.BackupOnce(now.Add(time.Duration(i+1) * time.Second)); err != nil {
			t.Fatalf("backup %d: %v", i, err)
		}
	}
	ents, err := os.ReadDir(dest)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 20 {
		t.Fatalf("rotation kept %d archives, want 20", len(ents))
	}
	// архив читается и содержит журнал
	zr, err := zip.OpenReader(filepath.Join(dest, ents[19].Name()))
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	found := false
	for _, f := range zr.File {
		if f.Name == "hello-guest.ndjson" {
			rc, _ := f.Open()
			b, _ := io.ReadAll(rc)
			rc.Close()
			if len(b) == 0 {
				t.Fatal("journal entry empty")
			}
			found = true
		}
	}
	if !found {
		t.Fatal("journal not in archive")
	}
}
