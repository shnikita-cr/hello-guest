package importcsv

import (
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
		if g.TicketNumber != "" {
			t.Errorf("ticket_number stolen from ticket_type: %+v", g)
		}
		if g.TicketType == "" {
			t.Errorf("ticket_type empty: %+v", g)
		}
		if !strings.HasPrefix(g.ID, "email:") {
			t.Errorf("id should be email-based, got %q", g.ID)
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
	if m.TicketNumber != "" {
		t.Errorf("ticket_number = %q, want empty", m.TicketNumber)
	}
}
