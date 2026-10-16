package importcsv

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"hello-guest/internal/store"
)

const sample = `Номер заказа,Дата создания,Статус,Email,Тип билета,Номер билета,EAN,Имя,Фамилия,Телефон,Промокод,Сумма
1001,2026-10-01 12:00,paid,ivan@example.com,Standard,TKT-001,4600001,Иван,Иванов,+79001112233,PROMO,1000
1002,2026-10-01 12:30,paid,olga@example.com,VIP,TKT-002,4600002,Ольга,Петрова,+79002223344,,2000
1003,2026-10-02 09:15,paid,ivan+2@example.com,Standard,TKT-003,4600003,Иван,Иванов,+79001112233,,1000
1004,2026-10-02 10:00,pending,,Standard,TKT-004,4600004,Пётр,Сидоров,+79003334455,,1000
1005,2026-10-03 11:00,paid,anna@example.com,VIP,TKT-005,4600005,Анна,Козлова,,PROMO,2000
`

func TestParseSample(t *testing.T) {
	guests, err := Parse(strings.NewReader(sample), "conf-2026")
	if err != nil {
		t.Fatal(err)
	}
	// ivan@example.com и ivan+2@example.com — один гость (дедуп по email)
	if len(guests) != 4 {
		t.Fatalf("want 4 guests after dedup, got %d: %+v", len(guests), guests)
	}
	var ivan *store.Guest
	for i := range guests {
		if store.NormalizeEmail(guests[i].Email) == "ivan@example.com" {
			ivan = &guests[i]
		}
	}
	if ivan == nil {
		t.Fatal("ivan not found")
	}
	if ivan.Name != "Иван Иванов" {
		t.Errorf("name = %q", ivan.Name)
	}
	if ivan.TicketType != "Standard" {
		t.Errorf("ticket_type = %q", ivan.TicketType)
	}
	if ivan.Event != "conf-2026" {
		t.Errorf("event = %q", ivan.Event)
	}
}

func TestDedupKeepsTicketType(t *testing.T) {
	in := []store.Guest{
		{ID: "email:a@b.c", Email: "a@b.c"},
		{ID: "email:a@b.c", Email: "a@b.c", TicketType: "VIP", TicketNumber: "T1"},
	}
	out := dedup(in)
	if len(out) != 1 {
		t.Fatalf("want 1, got %d", len(out))
	}
	if out[0].TicketType != "VIP" || out[0].TicketNumber != "T1" {
		t.Errorf("merged fields lost: %+v", out[0])
	}
}

func TestParseRealFile(t *testing.T) {
	f, err := os.Open("../../sample_timepad.csv")
	if err != nil {
		t.Skip("no sample file")
	}
	defer f.Close()
	guests, err := Parse(f, "e1")
	if err != nil {
		t.Fatal(err)
	}
	if len(guests) != 4 {
		t.Errorf("want 4, got %d", len(guests))
	}
}

// Реальные колонки Timepad: «Тип билета» не должен занять ticket_number,
// иначе guest_id становится «ticket:<тип билета>» и гости схлопываются.
const timepadReal = `Заказ,Код,Фамилия,Имя,E-mail,Тип билета,Статус
90654090,117589678,Шепелев,Андрей,a.shepelyov@gmail.com,Сольный билет,оплачен
90655762,117591732,Манахов,Кирилл,kirill080404@mail.ru,Командный билет,оплачен
`

func TestParseTimepadRealHeaders(t *testing.T) {
	guests, err := Parse(strings.NewReader(timepadReal), "muzloto")
	if err != nil {
		t.Fatal(err)
	}
	if len(guests) != 2 {
		t.Fatalf("want 2 guests, got %d: %+v", len(guests), guests)
	}
	for _, g := range guests {
		// «Код» Timepad (117589678:95603926) кодируется в QR билета —
		// он же становится guest_id для чек-ина по скану
		if g.TicketNumber == "" || !strings.HasPrefix(g.ID, "ticket:") {
			t.Errorf("code column should map to ticket id: %+v", g)
		}
		if g.TicketType == "" {
			t.Errorf("ticket_type empty: %+v", g)
		}
	}
}

// Полная шапка реальной выгрузки Timepad: ловушки «Статус события»,
// «Тип оплаты», «Email заказчика» не должны перетирать точные совпадения.
func TestDetectFullTimepadHeaders(t *testing.T) {
	headers := []string{
		"Событие", "Статус события", "Город", "Адрес", "Дата", "Заказ", "Код", "Штрихкод",
		"Зарегистрирован", "Оплатил", "Сумма", "Скидка (руб.)", "Скидка (%)", "Тип оплаты",
		"ID оплаты", "Тип билета", "Промокод", "Статус", "Место", "Email заказчика",
		"Рекламная кампания", "Источник трафика", "Канал регистрации", "Идентификатор контента",
		"Ключевое слово", "YM ClientID", "Персональная ссылка", "Пришёл", "Комментарий",
		"E-mail", "Название команды", "Фамилия", "Имя", "Контакт для связи с капитаном",
		"ФИО участников команды",
	}
	m := detect(headers)
	if m.OrderID != "Заказ" {
		t.Errorf("order_id = %q, want Заказ", m.OrderID)
	}
	if m.TicketType != "Тип билета" {
		t.Errorf("ticket_type = %q, want Тип билета", m.TicketType)
	}
	if m.Status != "Статус" {
		t.Errorf("status = %q, want Статус", m.Status)
	}
	if m.Email != "E-mail" {
		t.Errorf("email = %q, want E-mail", m.Email)
	}
	if m.FirstName != "Имя" || m.LastName != "Фамилия" {
		t.Errorf("name columns = %q/%q, want Имя/Фамилия", m.FirstName, m.LastName)
	}
	if m.TicketNumber != "Код" {
		t.Errorf("ticket_number = %q, want Код", m.TicketNumber)
	}
	if m.Barcode != "Штрихкод" {
		t.Errorf("barcode = %q, want Штрихкод", m.Barcode)
	}
}

func xlsxBytes(t *testing.T, sharedXML, sheetXML string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range []struct{ name, body string }{
		{"xl/sharedStrings.xml", sharedXML},
		{"xl/worksheets/sheet1.xml", sheetXML},
	} {
		w, err := zw.Create(f.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(w, f.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestParseXLSX(t *testing.T) {
	// словарь: 0-6 — шапка, 7-13 — строка 1, 14-19 — строка 2 (в B3 дырка)
	shared := []string{"Заказ", "Код", "Фамилия", "Имя", "E-mail", "Тип билета", "Статус",
		"90654090", "117589678", "Шепелев", "Андрей", "a@x.com", "Сольный билет", "оплачен",
		"90655762", "Манахов", "Кирилл", "k@x.com", "Командный билет", "оплачен"}
	var ss strings.Builder
	ss.WriteString(`<sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">`)
	for _, s := range shared {
		fmt.Fprintf(&ss, `<si><t>%s</t></si>`, s)
	}
	ss.WriteString(`</sst>`)
	sheet := `<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>` +
		`<row r="1"><c r="A1" t="s"><v>0</v></c><c r="B1" t="s"><v>1</v></c><c r="C1" t="s"><v>2</v></c><c r="D1" t="s"><v>3</v></c><c r="E1" t="s"><v>4</v></c><c r="F1" t="s"><v>5</v></c><c r="G1" t="s"><v>6</v></c></row>` +
		`<row r="2"><c r="A2" t="s"><v>7</v></c><c r="B2" t="s"><v>8</v></c><c r="C2" t="s"><v>9</v></c><c r="D2" t="s"><v>10</v></c><c r="E2" t="s"><v>11</v></c><c r="F2" t="s"><v>12</v></c><c r="G2" t="s"><v>13</v></c></row>` +
		// в строке 3 B пропущен — колонки обязаны сойтись по адресам ячеек
		`<row r="3"><c r="A3" t="s"><v>14</v></c><c r="C3" t="s"><v>15</v></c><c r="D3" t="s"><v>16</v></c><c r="E3" t="s"><v>17</v></c><c r="F3" t="s"><v>18</v></c><c r="G3" t="s"><v>19</v></c></row>` +
		`</sheetData></worksheet>`
	guests, err := Parse(bytes.NewReader(xlsxBytes(t, ss.String(), sheet)), "e1")
	if err != nil {
		t.Fatal(err)
	}
	if len(guests) != 2 {
		t.Fatalf("want 2 guests, got %d: %+v", len(guests), guests)
	}
	// строка 2 с кодом → ticket-id, строка 3 без ячейки «Код» → email-id
	if !strings.HasPrefix(guests[0].ID, "ticket:") || guests[0].TicketNumber != "117589678" {
		t.Errorf("row with code should be ticket-based: %+v", guests[0])
	}
	if !strings.HasPrefix(guests[1].ID, "email:") {
		t.Errorf("row with code gap should fall back to email: %+v", guests[1])
	}
	for _, g := range guests {
		if g.TicketType == "" || g.OrderID == "" || g.Status == "" {
			t.Errorf("fields lost after cell-gap row: %+v", g)
		}
	}
	if guests[0].Name != "Андрей Шепелев" && guests[1].Name != "Андрей Шепелев" {
		t.Errorf("name wrong: %+v", guests)
	}
}

func TestParseRealXLSXFile(t *testing.T) {
	f, err := os.Open("../../Uchastniki_24_10_2026_--_NE_SPAT_MUZLOTO_--_na_16_10_2026_11-15 copy.xlsx")
	if err != nil {
		t.Skip("no sample xlsx")
	}
	defer f.Close()
	guests, err := Parse(f, "muzloto")
	if err != nil {
		t.Fatal(err)
	}
	if len(guests) != 6 {
		t.Errorf("want 6 guests from sample, got %d", len(guests))
	}
	for _, g := range guests {
		if !strings.HasPrefix(g.ID, "ticket:") || g.TicketNumber == "" {
			t.Errorf("code column should map to ticket id: %+v", g)
		}
		if g.Name == "" || g.TicketType == "" {
			t.Errorf("missing fields: %+v", g)
		}
	}
}
