package timepad

import "testing"

func TestNormalizeOrder(t *testing.T) {
	o := order{
		ID:     123,
		Status: status{Name: "paid"},
		Tickets: []ticket{
			{
				ID:     1,
				Number: "TKT-1",
				Answers: []answer{
					{Question: "Ваше имя и фамилия", Answer: "Иван Иванов"},
					{Question: "Email", Answer: "ivan@example.com"},
					{Question: "Телефон", Answer: "+79001112233"},
				},
				TicketType: ticketType{Name: "Standard"},
				Codes:      codes{EAN13: "4600001"},
			},
			{
				ID:     2,
				Number: "",
				Answers: []answer{
					{Question: "Name", Answer: "Ольга"},
					{Question: "E-mail", Answer: "olga@example.com"},
				},
				TicketType: ticketType{Name: "VIP"},
				Codes:      codes{EAN13: "4600002"},
			},
		},
	}
	gs := normalizeOrder(o, "e1")
	if len(gs) != 2 {
		t.Fatalf("want 2, got %d", len(gs))
	}
	g := gs[0]
	if g.Name != "Иван Иванов" || g.Email != "ivan@example.com" || g.Phone != "+79001112233" {
		t.Errorf("contacts: %+v", g)
	}
	if g.ID != "ticket:TKT-1" || g.TicketType != "Standard" || g.Status != "paid" {
		t.Errorf("fields: %+v", g)
	}
	if g.OrderID != "123" || g.Event != "e1" {
		t.Errorf("order/event: %+v", g)
	}
	// без number — берём EAN как ticket_number
	if gs[1].ID != "ticket:4600002" {
		t.Errorf("ean fallback id: %s", gs[1].ID)
	}
	if gs[1].Name != "Ольга" || gs[1].Email != "olga@example.com" {
		t.Errorf("second contacts: %+v", gs[1])
	}
}

func TestExtractContactsSkipsQuestionLikeAnswers(t *testing.T) {
	name, email, _ := extractContacts([]answer{
		{Question: "Сколько вам лет?", Answer: "25"},
		{Question: "Как прийти?", Answer: "метро"},
		{Question: "Имя", Answer: "Пётр"},
	})
	if name != "Пётр" {
		t.Errorf("name = %q", name)
	}
	if email != "" {
		t.Errorf("email = %q", email)
	}
}
