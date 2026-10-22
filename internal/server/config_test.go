package server

import (
	"crypto/tls"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode"
)

func TestLoadOrCreateConfig(t *testing.T) {
	dir := t.TempDir()
	cfg, pin, err := LoadOrCreateConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(pin) != 6 || cfg.PinPlain != pin {
		t.Fatalf("pin: %q", pin)
	}
	for _, r := range pin {
		if r < '0' || r > '9' {
			t.Fatalf("pin не из цифр: %q", pin)
		}
	}
	if !cfg.Check(pin) || cfg.Check("654321") && pin == "654321" {
		t.Fatalf("Check: %q", pin)
	}
	if len(cfg.PinSalt) != 32 || len(cfg.PinHash) != 64 {
		t.Fatalf("salt/hash: %q %q", cfg.PinSalt, cfg.PinHash)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "config.json")); err != nil || !strings.Contains(string(b), "pin_hash") {
		t.Fatalf("config.json не записан: %v", err)
	}

	// повторный вызов читает существующий конфиг и не выдаёт новый pin
	cfg2, pin2, err := LoadOrCreateConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if pin2 != "" || cfg2.PinHash != cfg.PinHash || cfg2.PinPlain != pin {
		t.Fatalf("перечитывание: pin2=%q plain=%q", pin2, cfg2.PinPlain)
	}

	// сохранение падает, когда каталога не существует и создать его нельзя
	f := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadOrCreateConfig(filepath.Join(f, "sub")); err == nil {
		t.Fatal("ожидали ошибку сохранения конфига")
	}
}

func TestEnsureCert(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile, err := EnsureCert(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tls.LoadX509KeyPair(certFile, keyFile); err != nil {
		t.Fatalf("пара не загружается: %v", err)
	}
	// повторно — из кэша, содержимое не меняется
	before, _ := os.ReadFile(certFile)
	certFile2, _, err := EnsureCert(dir)
	if err != nil || certFile2 != certFile {
		t.Fatalf("кэш: %q %v", certFile2, err)
	}
	after, _ := os.ReadFile(certFile)
	if string(before) != string(after) {
		t.Fatal("сертификат пересоздался")
	}
}

func TestReportBase(t *testing.T) {
	if got := reportBase(""); !strings.HasPrefix(got, "event-") {
		t.Fatalf("пустое имя: %q", got)
	}
	if got := reportBase("Мой конференция 2026!"); !strings.HasPrefix(got, "Мой-конференция-2026_") {
		t.Fatalf("пробел/знак: %q", got)
	}
	long := reportBase(strings.Repeat("ж", 60))
	if len([]rune(long)) != 40+1+15 { // усечение до 40 рун + "-" + метка времени
		t.Fatalf("длина long: %d (%q)", len([]rune(long)), long)
	}
	for _, r := range reportBase("a/b\\c:d") {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == '.' {
			continue
		}
		t.Fatalf("недопустимый символ %q в имени файла", r)
	}
}
