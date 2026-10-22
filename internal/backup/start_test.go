package backup

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRunnerStart(t *testing.T) {
	data := t.TempDir()
	if err := os.WriteFile(filepath.Join(data, "hello-guest.ndjson"), []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &Runner{DataDir: data, DestDir: filepath.Join(data, "copies"), Keep: 20, Every: 20 * time.Millisecond}
	stop := make(chan struct{})
	defer close(stop)
	r.Start(stop, nil)

	deadline := time.Now().Add(2 * time.Second)
	for {
		if auto, _ := r.Times(); !auto.IsZero() {
			return // тикер создал копию
		}
		if time.Now().After(deadline) {
			t.Fatal("автоматическая копия не создалась")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestWriteZipMissingDir(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "no-such-dir", "a.zip")
	if err := writeZip(t.TempDir(), dest); err == nil {
		t.Fatal("запись в несуществующую папку должна падать")
	}
}

func TestBackupOnceDestNotDir(t *testing.T) {
	data := t.TempDir()
	if err := os.WriteFile(filepath.Join(data, "hello-guest.ndjson"), []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(data, "file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &Runner{DataDir: data, DestDir: filepath.Join(file, "copies"), Keep: 20, Every: time.Minute}
	if _, err := r.BackupOnce(time.Now()); err == nil {
		t.Fatal("каталог назначения внутри файла — ошибка")
	}
}
