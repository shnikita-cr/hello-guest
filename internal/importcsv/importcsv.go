package importcsv

import (
	"encoding/csv"
	"io"
	"strings"

	"hello-guest/internal/store"
)

var headerAliases = map[string][]string{
	"order_id":      {"номер заказа", "order", "orderid", "заказ"},
	"ticket_number": {"номер билета", "ticketnumber", "ticketnumber", "билет", "номер"},
	"ticket_type":   {"тип билета", "tickettype", "ticket_type", "тип"},
	"email":         {"email", "e-mail", "почта", "эл. почта", "электронная почта"},
	"phone":         {"телефон", "phone", "тел", "мобильный"},
	"name":          {"имя", "name", "имя и фамилия", "full name", "гость", "visitor"},
	"first_name":    {"имя", "first name", "firstname"},
	"last_name":     {"фамилия", "last name", "lastname"},
	"status":        {"статус", "status"},
	"promocode":     {"промокод", "promocode", "promo"},
	"sum":           {"сумма", "sum", "цена", "price", "стоимость"},
}

type mapping struct {
	OrderID, TicketNumber, TicketType, Email, Phone, Name, FirstName, LastName, Status, Promocode, Sum string
}

func detect(headers []string) mapping {
	m := mapping{}
	norm := make([]string, len(headers))
	for i, h := range headers {
		norm[i] = normalize(h)
	}
	set := func(field string, idx *string) {
		for i, h := range norm {
			for _, alias := range headerAliases[field] {
				if h == alias {
					*idx = headers[i]
					return
				}
			}
		}
		for i, h := range norm {
			if *idx != "" {
				return
			}
			for _, alias := range headerAliases[field] {
				if strings.Contains(h, alias) {
					*idx = headers[i]
					return
				}
			}
		}
	}
	set("order_id", &m.OrderID)
	set("ticket_number", &m.TicketNumber)
	set("ticket_type", &m.TicketType)
	set("email", &m.Email)
	set("phone", &m.Phone)
	set("first_name", &m.FirstName)
	set("last_name", &m.LastName)
	set("name", &m.Name)
	set("status", &m.Status)
	set("promocode", &m.Promocode)
	set("sum", &m.Sum)
	return m
}

func normalize(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.Trim(s, "\ufeff\"'")
	return s
}

func Parse(r io.Reader, event string) ([]store.Guest, error) {
	cr := csv.NewReader(r)
	cr.Comma = detectDelimiter(cr)
	cr.LazyQuotes = true
	cr.FieldsPerRecord = -1
	cr.TrimLeadingSpace = true

	headers, err := cr.Read()
	if err != nil {
		return nil, err
	}
	for i := range headers {
		headers[i] = normalize(headers[i])
	}
	m := detect(headers)
	col := func(row []string, name string) string {
		for i, h := range headers {
			if h == name {
				if i < len(row) {
					return strings.TrimSpace(row[i])
				}
				return ""
			}
		}
		return ""
	}

	var guests []store.Guest
	for {
		row, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		g := store.Guest{Event: event, Source: "timepad"}
		if m.OrderID != "" {
			g.OrderID = col(row, normalize(m.OrderID))
		}
		if m.TicketNumber != "" {
			g.TicketNumber = col(row, normalize(m.TicketNumber))
		}
		if m.TicketType != "" {
			g.TicketType = col(row, normalize(m.TicketType))
		}
		if m.Email != "" {
			g.Email = col(row, normalize(m.Email))
		}
		if m.Phone != "" {
			g.Phone = col(row, normalize(m.Phone))
		}
		if m.Status != "" {
			g.Status = col(row, normalize(m.Status))
		}
		if m.FirstName != "" && m.LastName != "" {
			fn := col(row, normalize(m.FirstName))
			ln := col(row, normalize(m.LastName))
			g.Name = strings.TrimSpace(fn + " " + ln)
		} else if m.Name != "" {
			g.Name = col(row, normalize(m.Name))
		} else if m.FirstName != "" {
			g.Name = col(row, normalize(m.FirstName))
		}
		g.ID = guestID(g)
		if g.ID == "" {
			continue
		}
		guests = append(guests, g)
	}
	return dedup(guests), nil
}

func guestID(g store.Guest) string {
	if g.TicketNumber != "" {
		return "ticket:" + g.TicketNumber
	}
	if g.Email != "" {
		return "email:" + store.NormalizeEmail(g.Email)
	}
	if g.Name != "" && g.Phone != "" {
		return "np:" + strings.ToLower(g.Name) + "|" + strings.ReplaceAll(g.Phone, " ", "")
	}
	if g.Name != "" {
		return "name:" + strings.ToLower(g.Name)
	}
	return ""
}

func dedup(guests []store.Guest) []store.Guest {
	seen := map[string]int{}
	var out []store.Guest
	for _, g := range guests {
		key := ""
		if g.Email != "" {
			key = "email:" + store.NormalizeEmail(g.Email)
		} else {
			key = g.ID
		}
		if idx, ok := seen[key]; ok {
			if out[idx].TicketType == "" && g.TicketType != "" {
				out[idx].TicketType = g.TicketType
			}
			if out[idx].TicketNumber == "" && g.TicketNumber != "" {
				out[idx].TicketNumber = g.TicketNumber
				out[idx].ID = guestID(out[idx])
			}
			continue
		}
		seen[key] = len(out)
		out = append(out, g)
	}
	return out
}

func detectDelimiter(cr *csv.Reader) rune {
	return ','
}
