package importcsv

import (
	"archive/zip"
	"bytes"
	"errors"
	"strings"
	"testing"

	"hello-guest/internal/store"
)

func TestGuestIDVariants(t *testing.T) {
	cases := []struct {
		g    store.Guest
		want string
	}{
		{store.Guest{TicketNumber: "777:1"}, "ticket:777:1"},
		{store.Guest{Email: "Ivan+X@Mail.RU"}, "email:ivan@mail.ru"},
		{store.Guest{Name: "Иван", Phone: "+7 900 111"}, "np:иван|+7900111"},
		{store.Guest{Name: "Иван"}, "name:иван"},
		{store.Guest{}, ""},
	}
	for _, c := range cases {
		if got := guestID(c.g); got != c.want {
			t.Errorf("guestID(%+v) = %q, ждали %q", c.g, got, c.want)
		}
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("сбой чтения") }

func TestParseReadError(t *testing.T) {
	if _, err := Parse(errReader{}, "e"); err == nil {
		t.Fatal("ошибка чтения должна всплыть")
	}
}

func TestColIdx(t *testing.T) {
	cases := map[string]int{"A": 0, "Z": 25, "AA": 26, "a": 0, "AB1": 27, "B12": 1, "": -1, "1": -1}
	for ref, want := range cases {
		if got := colIdx(ref); got != want {
			t.Errorf("colIdx(%q) = %d, ждали %d", ref, got, want)
		}
	}
}

func TestParseEmptyAndUnknownHeaders(t *testing.T) {
	if _, err := Parse(strings.NewReader(""), "e"); err == nil {
		t.Fatal("пустой файл — ошибка разбора")
	}
	gs, err := Parse(strings.NewReader("foo,bar\n1,2\n"), "e")
	if err != nil {
		t.Fatal(err)
	}
	if len(gs) != 0 {
		t.Fatalf("незнакомые колонки не дают гостей: %+v", gs)
	}
}

func TestParseBrokenSharedStrings(t *testing.T) {
	// zip без словаря строк валиден (он опционален), битый словарь — нет
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range map[string]string{
		"xl/worksheets/sheet1.xml": `<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData><row r="1"><c r="A1" t="s"><v>0</v></c></row></sheetData></worksheet>`,
		"xl/sharedStrings.xml":     `<sst>битый`,
	} {
		f, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		f.Write([]byte(body))
	}
	zw.Close()
	if _, err := Parse(&buf, "e"); err == nil {
		t.Fatal("битый словарь строк — ошибка")
	}
}
