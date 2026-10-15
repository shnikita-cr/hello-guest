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
