package store

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStringAndOpenErrors(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(filepath.Join(dir, "j.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.Upsert([]Guest{{ID: "g", Event: "e", Name: "N"}}); err != nil {
		t.Fatal(err)
	}
	if s := st.String(); !strings.HasPrefix(s, "store(") || !strings.Contains(s, dir) || !strings.Contains(s, "1 events") {
		t.Fatalf("String: %q", s)
	}
	st.Close()

	// открыть каталог как журнал нельзя
	if _, err := Open(dir); err == nil {
		t.Fatal("открытие каталога — ошибка")
	}
	// каталог для журнала лежит внутри обычного файла
	f := filepath.Join(dir, "file")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(filepath.Join(f, "sub", "j.ndjson")); err == nil {
		t.Fatal("MkdirAll внутри файла — ошибка")
	}
	// строка длиннее лимита сканера — replay падает
	p := filepath.Join(dir, "big.ndjson")
	if err := os.WriteFile(p, bytes.Repeat([]byte("a"), 5<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(p); err == nil {
		t.Fatal("слишком длинная строка — ошибка replay")
	}
}

func TestClosedStoreReturnsErrors(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(filepath.Join(dir, "j.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.Upsert([]Guest{{ID: "g", Event: "e", Name: "N"}}); err != nil {
		t.Fatal(err)
	}
	st.Close()

	// после Close все записи падают — ошибка должна всплывать, а не теряться
	if _, _, err := st.Upsert([]Guest{{ID: "g2", Event: "e", Name: "M"}}); err == nil {
		t.Fatal("Upsert после Close — ошибка")
	}
	if err := st.SetMembers("e", "g", map[string]bool{"N": true}, time.Now()); err == nil {
		t.Fatal("SetMembers после Close — ошибка")
	}
	if err := st.SetTeam("e", "g", "Волки", []string{"N"}, time.Now()); err == nil {
		t.Fatal("SetTeam после Close — ошибка")
	}
	if err := st.QRMiss("e", "123"); err == nil {
		t.Fatal("QRMiss после Close — ошибка")
	}
	if err := st.UndoCheckin("e", "g"); err == nil {
		t.Fatal("UndoCheckin после Close — ошибка")
	}
	if _, err := st.Checkin("e", "g", "d", "tap", time.Now()); err == nil {
		t.Fatal("Checkin после Close — ошибка")
	}
}

func TestSearchQuery(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(filepath.Join(dir, "j.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, _, err := st.Upsert([]Guest{
		{ID: "g1", Event: "e", Name: "Иван Петров", Email: "ivan@x.ru", Phone: "+79001112233", TicketNumber: "10001:2"},
		{ID: "g2", Event: "e", Name: "Ольга"},
	}); err != nil {
		t.Fatal(err)
	}
	for q, want := range map[string]int{"иван": 1, "olga": 0, "@x.ru": 1, "+79001112233": 1, "10001:2": 1, "zzz": 0} {
		if got := len(st.Search("e", q)); got != want {
			t.Errorf("Search(%q) = %d, ждали %d", q, got, want)
		}
	}
}
