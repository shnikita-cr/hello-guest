package timepad

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"hello-guest/internal/store"
)

const baseURL = "https://api.timepad.ru/v1"

type Client struct {
	Token string
	HTTP  *http.Client
}

func New(token string) *Client {
	return &Client{Token: token, HTTP: &http.Client{Timeout: 30 * time.Second}}
}

type order struct {
	ID      int64    `json:"id"`
	Status  status   `json:"status"`
	Tickets []ticket `json:"tickets"`
}

type status struct {
	Name  string `json:"name"`
	Title string `json:"title"`
}

type ticket struct {
	ID         int64      `json:"id"`
	Number     string     `json:"number"`
	Answers    []answer   `json:"answers"`
	TicketType ticketType `json:"ticket_type"`
	Codes      codes      `json:"codes"`
}

type ticketType struct {
	Name string `json:"name"`
}

type codes struct {
	EAN13       string `json:"ean13"`
	PrintedCode string `json:"printed_code"`
}

type answer struct {
	Question string `json:"question"`
	Answer   string `json:"answer"`
}

type ordersResp struct {
	Items []order `json:"items"`
}

func (c *Client) FetchOrders(eventID int64) ([]store.Guest, error) {
	var all []store.Guest
	skip := 0
	const limit = 300
	for {
		u := fmt.Sprintf("%s/orders?event_ids=%d&limit=%d&skip=%d", baseURL, eventID, limit, skip)
		req, _ := http.NewRequest("GET", u, nil)
		req.Header.Set("Authorization", "Bearer "+c.Token)
		resp, err := c.HTTP.Do(req)
		if err != nil {
			return nil, err
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("timepad %d: %s", resp.StatusCode, snippet(body))
		}
		var page ordersResp
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, err
		}
		for _, o := range page.Items {
			all = append(all, normalizeOrder(o, strconv.FormatInt(eventID, 10))...)
		}
		if len(page.Items) < limit {
			break
		}
		skip += limit
	}
	return all, nil
}

func normalizeOrder(o order, event string) []store.Guest {
	var out []store.Guest
	for _, t := range o.Tickets {
		g := store.Guest{
			Event:        event,
			Source:       "timepad-api",
			OrderID:      strconv.FormatInt(o.ID, 10),
			TicketNumber: t.Number,
			TicketType:   t.TicketType.Name,
			Status:       o.Status.Name,
		}
		if g.TicketNumber == "" && t.Codes.EAN13 != "" {
			g.TicketNumber = t.Codes.EAN13
		}
		g.Name, g.Email, g.Phone = extractContacts(t.Answers)
		g.ID = guestID(g)
		if g.ID == "" {
			continue
		}
		out = append(out, g)
	}
	return out
}

func extractContacts(answers []answer) (name, email, phone string) {
	for _, a := range answers {
		q := strings.ToLower(a.Question)
		v := strings.TrimSpace(a.Answer)
		switch {
		case strings.Contains(q, "почт") || strings.Contains(q, "email") || strings.Contains(q, "e-mail"):
			if email == "" {
				email = v
			}
		case strings.Contains(q, "телефон") || strings.Contains(q, "phone") || strings.Contains(q, "тел"):
			if phone == "" {
				phone = v
			}
		case strings.Contains(q, "имя") || strings.Contains(q, "name") || strings.Contains(q, "гост") || strings.Contains(q, "фамил"):
			if name == "" {
				name = v
			}
		default:
			if name == "" && !strings.ContainsAny(q, "?") && len(v) > 1 && len(v) < 60 && !strings.Contains(strings.ToLower(v), "@") {
				name = v
			}
		}
	}
	return name, email, phone
}

func guestID(g store.Guest) string {
	if g.TicketNumber != "" {
		return "ticket:" + g.TicketNumber
	}
	if g.Email != "" {
		return "email:" + store.NormalizeEmail(g.Email)
	}
	if g.Name != "" {
		return "name:" + strings.ToLower(g.Name)
	}
	return ""
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}
