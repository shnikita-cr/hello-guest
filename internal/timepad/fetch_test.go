package timepad

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"hello-guest/internal/store"
)

func mkOrders(n, from int) ordersResp {
	var items []order
	for i := 0; i < n; i++ {
		items = append(items, order{
			ID:     int64(from + i),
			Status: status{Name: "paid"},
			Tickets: []ticket{{
				ID:         int64(from + i),
				Number:     fmt.Sprintf("777:%d", from+i),
				TicketType: ticketType{Name: "Standard"},
			}},
		})
	}
	return ordersResp{Items: items}
}

func TestFetchOrders(t *testing.T) {
	old := BaseURL
	t.Cleanup(func() { BaseURL = old })

	// первая страница ровно по лимиту — цикл должен уйти на вторую
	pages := map[string]ordersResp{"0": mkOrders(300, 1), "300": mkOrders(1, 301)}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":"unauthorized"}`))
			return
		}
		if p, ok := pages[r.URL.Query().Get("skip")]; ok {
			json.NewEncoder(w).Encode(p)
			return
		}
		json.NewEncoder(w).Encode(ordersResp{})
	}))
	t.Cleanup(srv.Close)
	BaseURL = srv.URL

	gs, err := New("tok").FetchOrders(7)
	if err != nil {
		t.Fatal(err)
	}
	if len(gs) != 301 {
		t.Fatalf("пагинация: got %d guests, ждали 301", len(gs))
	}
	if gs[0].Event != "7" || gs[0].ID != "ticket:777:1" || gs[0].Source != "timepad-api" {
		t.Fatalf("первая запись: %+v", gs[0])
	}
	if gs[300].ID != "ticket:777:301" {
		t.Fatalf("последняя запись: %+v", gs[300])
	}

	if _, err := New("wrong").FetchOrders(7); err == nil || !strings.Contains(err.Error(), "timepad 401") {
		t.Fatalf("401: %v", err)
	}

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("{oops"))
	}))
	t.Cleanup(bad.Close)
	BaseURL = bad.URL
	if _, err := New("tok").FetchOrders(7); err == nil {
		t.Fatal("битый json — ошибка")
	}

	BaseURL = "http://127.0.0.1:1"
	if _, err := New("tok").FetchOrders(7); err == nil {
		t.Fatal("недоступный сервер — ошибка")
	}
}

func TestGuestIDVariants(t *testing.T) {
	cases := []struct {
		g    store.Guest
		want string
	}{
		{store.Guest{TicketNumber: "777:1"}, "ticket:777:1"},
		{store.Guest{Email: "Ivan+X@Mail.RU"}, "email:ivan@mail.ru"},
		{store.Guest{Name: "Пётр"}, "name:пётр"},
		{store.Guest{}, ""},
	}
	for _, c := range cases {
		if got := guestID(c.g); got != c.want {
			t.Errorf("guestID(%+v) = %q, ждали %q", c.g, got, c.want)
		}
	}
}

func TestSnippet(t *testing.T) {
	if s := snippet([]byte("  привет \n")); s != "привет" {
		t.Fatalf("короткий: %q", s)
	}
	if s := snippet(bytes.Repeat([]byte("я"), 300)); len(s) != 200 {
		t.Fatalf("длинный обрезается до 200 байт, got %d", len(s))
	}
}
