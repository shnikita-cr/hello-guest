package importcsv

import (
	"bytes"
	"encoding/csv"
	"io"
	"strings"

	"hello-guest/internal/store"
)

var headerAliases = map[string][]string{
	"order_id":      {"номер заказа", "order", "orderid", "заказ"},
	"ticket_number": {"номер билета", "ticketnumber", "ticketnumber", "билет", "номер", "код"},
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
	claimed := make([]bool, len(headers))
	// сначала точные совпадения по всем полям, затем Contains только по незанятым колонкам:
	// иначе «тип билета» съедает ticket_number по подстроке «билет» раньше,
	// чем ticket_type займёт её точным совпадением
	type target struct {
		field string
		dst   *string
	}
	targets := []target{
		{"order_id", &m.OrderID},
		{"ticket_number", &m.TicketNumber},
		{"ticket_type", &m.TicketType},
		{"email", &m.Email},
		{"phone", &m.Phone},
		{"first_name", &m.FirstName},
		{"last_name", &m.LastName},
		{"name", &m.Name},
		{"status", &m.Status},
		{"promocode", &m.Promocode},
		{"sum", &m.Sum},
	}
	match := func(t target, exact bool) bool {
		if *t.dst != "" {
			return false
		}
		for i, h := range norm {
			if claimed[i] {
				continue
			}
			for _, alias := range headerAliases[t.field] {
				if exact && h == alias || !exact && strings.Contains(h, alias) {
					*t.dst = headers[i]
					claimed[i] = true
					return true
				}
			}
		}
		return false
	}
	for _, t := range targets {
		match(t, true)
	}
	for _, t := range targets {
		match(t, false)
	}
	return m
}

func normalize(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.Trim(s, "\ufeff\"'")
	return s
}

type table struct {
	headers []string
	rows    [][]string
}

// Parse принимает выгрузку Timepad в CSV или XLSX — формат определяется по магии файла.
func Parse(r io.Reader, event string) ([]store.Guest, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	var tables []table
	if isZip(data) {
		tables, err = xlsxTables(data)
	} else {
		t, cerr := csvTable(data)
		if cerr != nil {
			return nil, cerr
		}
		tables = []table{t}
	}
	if err != nil {
		return nil, err
	}
	var guests []store.Guest
	for _, t := range tables {
		guests = append(guests, guestsFrom(t, event)...)
	}
	return dedup(guests), nil
}

func csvTable(data []byte) (table, error) {
	cr := csv.NewReader(bytes.NewReader(data))
	cr.Comma = detectDelimiter(cr)
	cr.LazyQuotes = true
	cr.FieldsPerRecord = -1
	cr.TrimLeadingSpace = true

	headers, err := cr.Read()
	if err != nil {
		return table{}, err
	}
	var rows [][]string
	for {
		row, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return table{}, err
		}
		rows = append(rows, row)
	}
	return table{headers: headers, rows: rows}, nil
}

func guestsFrom(t table, event string) []store.Guest {
	headers := make([]string, len(t.headers))
	for i, h := range t.headers {
		headers[i] = normalize(h)
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
	for _, row := range t.rows {
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
	return guests
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
