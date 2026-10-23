package store

import (
	"path/filepath"
	"testing"
	"time"
)

func tempStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestUpsertAndSearch(t *testing.T) {
	s := tempStore(t)
	guests := []Guest{
		{ID: "ticket:1", Event: "e1", Name: "Иван Иванов", Email: "i@x.com", TicketType: "Standard"},
		{ID: "ticket:2", Event: "e1", Name: "Ольга Петрова", Email: "o@x.com", TicketType: "VIP"},
	}
	added, updated, err := s.Upsert(guests)
	if err != nil {
		t.Fatal(err)
	}
	if added != 2 || updated != 0 {
		t.Fatalf("added=%d updated=%d", added, updated)
	}
	if got := s.Search("e1", "ольг"); len(got) != 1 || got[0].ID != "ticket:2" {
		t.Fatalf("search: %+v", got)
	}
	if got := s.Search("e1", ""); len(got) != 2 {
		t.Fatalf("empty query should return all: %d", len(got))
	}
}

func TestCheckinIdempotent(t *testing.T) {
	s := tempStore(t)
	s.Upsert([]Guest{{ID: "g1", Event: "e1", Name: "A"}})
	first, err := s.Checkin("e1", "g1", "tablet-1", "tap", time.Now())
	if err != nil || first {
		t.Fatalf("first checkin already=%v err=%v", first, err)
	}
	second, err := s.Checkin("e1", "g1", "tablet-2", "tap", time.Now())
	if err != nil || !second {
		t.Fatalf("second checkin already=%v err=%v", second, err)
	}
	total, arrived := s.Count("e1")
	if total != 1 || arrived != 1 {
		t.Fatalf("total=%d arrived=%d", total, arrived)
	}
	cs := s.Checkins("e1")
	if len(cs) != 1 {
		t.Fatalf("checkin events should be 1, got %d", len(cs))
	}
}

func TestCheckinUnknownGuest(t *testing.T) {
	s := tempStore(t)
	if _, err := s.Checkin("e1", "ghost", "d", "tap", time.Now()); err != ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestReplayRestoresState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.ndjson")
	s1, _ := Open(path)
	s1.Upsert([]Guest{{ID: "g1", Event: "e1", Name: "A"}})
	s1.Checkin("e1", "g1", "d1", "qr", time.Now())
	s1.Close()

	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	total, arrived := s2.Count("e1")
	if total != 1 || arrived != 1 {
		t.Fatalf("replay total=%d arrived=%d", total, arrived)
	}
	gs := s2.Search("e1", "a")
	if len(gs) != 1 || gs[0].ArrivedAt == "" {
		t.Fatalf("replay search: %+v", gs)
	}
}

func TestReimportPreservesArrival(t *testing.T) {
	s := tempStore(t)
	s.Upsert([]Guest{{ID: "g1", Event: "e1", Name: "A", Email: "a@x.com"}})
	s.Checkin("e1", "g1", "d", "tap", time.Now())
	s.Upsert([]Guest{{ID: "g1", Event: "e1", Name: "A", Email: "a@x.com", TicketType: "VIP"}})
	_, arrived := s.Count("e1")
	if arrived != 1 {
		t.Fatalf("arrival lost after reimport: arrived=%d", arrived)
	}
	gs := s.Search("e1", "a")
	if gs[0].TicketType != "VIP" {
		t.Fatalf("update not applied: %+v", gs[0])
	}
}

func TestMemberSnapshot(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.ndjson")
	s1, _ := Open(path)
	s1.Upsert([]Guest{{ID: "g1", Event: "e1", Name: "A", Members: []string{"A", "B", "C"}}})
	err := s1.SetMembers("e1", "g1", map[string]bool{"A": true, "B": false, "C": false}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	// реимпорт без member-полей не должен затирать снапшот
	s1.Upsert([]Guest{{ID: "g1", Event: "e1", Name: "A", Members: []string{"A", "B", "C"}, TicketType: "Командный билет"}})
	s1.Close()

	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	gs := s2.Search("e1", "a")
	if len(gs) != 1 {
		t.Fatalf("search: %+v", gs)
	}
	ma := gs[0].MemberArrived
	if len(ma) != 3 || !ma["A"] || ma["B"] || ma["C"] {
		t.Fatalf("member snapshot lost/wrong: %+v", ma)
	}
	if err := s2.SetMembers("e1", "g1", nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	if gs := s2.Search("e1", "a"); gs[0].MemberArrived != nil {
		t.Fatalf("clearing snapshot failed: %+v", gs[0].MemberArrived)
	}
}

func TestSetTeam(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.ndjson")
	s1, _ := Open(path)
	s1.Upsert([]Guest{{ID: "g1", Event: "e1", Name: "A", Team: "Орбита", Members: []string{"A", "B"}}})
	s1.Checkin("e1", "g1", "d", "tap", time.Now())
	if err := s1.SetTeam("e1", "g1", "Орбита-2", []string{"A", "B", "C"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := s1.SetTeam("e1", "nope", "x", nil, time.Now()); err != ErrNotFound {
		t.Fatalf("нет гостя: %v", err)
	}
	s1.Close()

	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	gs := s2.Search("e1", "a")
	if len(gs) != 1 {
		t.Fatalf("search: %+v", gs)
	}
	g := gs[0]
	if g.Team != "Орбита-2" || len(g.Members) != 3 || g.ArrivedAt == "" {
		t.Fatalf("состав/отметка потерялись: %+v", g)
	}
}

func TestUndoCheckin(t *testing.T) {
	s := tempStore(t)
	s.Upsert([]Guest{{ID: "g1", Event: "e1", Name: "A"}})
	s.Checkin("e1", "g1", "d", "tap", time.Now())
	if err := s.UndoCheckin("e1", "g1"); err != nil {
		t.Fatal(err)
	}
	_, arrived := s.Count("e1")
	if arrived != 0 {
		t.Fatalf("undo failed: arrived=%d", arrived)
	}
}

func TestNormalizeEmail(t *testing.T) {
	cases := map[string]string{
		"Ivan+promo@Example.com ": "ivan@example.com",
		"plain@x.ru":              "plain@x.ru",
		"a+1+b@y.com":             "a@y.com",
	}
	for in, want := range cases {
		if got := NormalizeEmail(in); got != want {
			t.Errorf("NormalizeEmail(%q)=%q want %q", in, got, want)
		}
	}
}

func TestEventsList(t *testing.T) {
	s := tempStore(t)
	s.Upsert([]Guest{
		{ID: "a", Event: "e2", Name: "A"},
		{ID: "b", Event: "e1", Name: "B"},
	})
	evs := s.Events()
	if len(evs) != 2 || evs[0] != "e1" || evs[1] != "e2" {
		t.Fatalf("events: %v", evs)
	}
}

func TestRenameEvent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "d.ndjson")
	s1, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s1.Upsert([]Guest{{ID: "g1", Event: "old", Name: "A"}})
	s1.Checkin("old", "g1", "d1", "tap", time.Now())
	if err := s1.RenameEvent("old", "new"); err != nil {
		t.Fatal(err)
	}
	if total, _ := s1.Count("old"); total != 0 {
		t.Fatalf("old event must be empty, total=%d", total)
	}
	if total, _ := s1.Count("new"); total != 1 {
		t.Fatalf("new event total=%d", total)
	}
	if gs := s1.Search("new", "a"); len(gs) != 1 || gs[0].ArrivedAt == "" {
		t.Fatalf("rename lost checkin: %+v", gs)
	}
	s1.Close()

	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	evs := s2.Events()
	if len(evs) != 1 || evs[0] != "new" {
		t.Fatalf("events after replay: %v", evs)
	}
	if total, arrived := s2.Count("new"); total != 1 || arrived != 1 {
		t.Fatalf("replay total=%d arrived=%d", total, arrived)
	}
}

func TestRenameEventConflict(t *testing.T) {
	s := tempStore(t)
	s.Upsert([]Guest{{ID: "a", Event: "e1", Name: "A"}})
	s.Upsert([]Guest{{ID: "b", Event: "e2", Name: "B"}})
	if err := s.RenameEvent("e1", "e2"); err == nil {
		t.Fatal("want conflict error")
	}
	if err := s.RenameEvent("e1", "e1"); err == nil {
		t.Fatal("want bad-name error")
	}
	if err := s.RenameEvent("ghost", "x"); err != ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestHideEvent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "d.ndjson")
	s1, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s1.Upsert([]Guest{{ID: "g1", Event: "e1", Name: "A"}})
	if err := s1.HideEvent("e1", true); err != nil {
		t.Fatal(err)
	}
	if !s1.EventHidden("e1") {
		t.Fatal("hide not applied")
	}
	if total, _ := s1.Count("e1"); total != 1 {
		t.Fatalf("guests must remain, total=%d", total)
	}
	s1.Close()

	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if !s2.EventHidden("e1") {
		t.Fatal("hidden flag lost on replay")
	}
	if total, _ := s2.Count("e1"); total != 1 {
		t.Fatalf("guests lost after replay, total=%d", total)
	}
	if err := s2.HideEvent("e1", false); err != nil {
		t.Fatal(err)
	}
	if s2.EventHidden("e1") {
		t.Fatal("unhide failed")
	}
	if err := s2.HideEvent("ghost", true); err != ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestStats(t *testing.T) {
	s := tempStore(t)
	s.Upsert([]Guest{
		{ID: "a", Event: "e1", Name: "A"},
		{ID: "b", Event: "e1", Name: "B"},
		{ID: "c", Event: "e1", Name: "C", Source: "walkin"},
	})
	s.Checkin("e1", "a", "d1", "qr", time.Now())
	s.Checkin("e1", "b", "d1", "tap", time.Now())
	s.QRMiss("e1", "117726839:97059462")
	s.QRMiss("e1", "9999:8888")
	st := s.Stats("e1")
	if st.Total != 3 || st.Arrived != 2 || st.QR != 1 || st.Tap != 1 || st.Walkins != 1 || st.FalseQR != 2 {
		t.Fatalf("stats: %+v", st)
	}
	s.UndoCheckin("e1", "a")
	s.Checkin("e1", "a", "d2", "tap", time.Now())
	st = s.Stats("e1")
	if st.QR != 0 || st.Tap != 2 || st.Arrived != 2 {
		t.Fatalf("stats after recheck: %+v", st)
	}
}

func TestBarcodeFromTicket(t *testing.T) {
	cases := map[string]string{
		"117589678:95603926": "1000956039269",
		"117640725:97155249": "1000971552491",
		"117591732:49871258": "1000498712583",
		"117726839:97059462": "1000970594621",
		"117726839":          "",
		"abc:def":            "",
	}
	for ticket, want := range cases {
		if got := BarcodeFromTicket(ticket); got != want {
			t.Errorf("BarcodeFromTicket(%q)=%q, want %q", ticket, got, want)
		}
	}
	s := tempStore(t)
	s.Upsert([]Guest{{ID: "a", Event: "e1", Name: "A", TicketNumber: "117726839:97059462"}})
	got := s.Search("e1", "1000970594621")
	if len(got) != 1 || got[0].Barcode != "1000970594621" {
		t.Fatalf("search by derived barcode: %+v", got)
	}
}

func TestJournal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "j.ndjson")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	s.Upsert([]Guest{{ID: "a", Event: "e1", Name: "Иван"}})
	s.Checkin("e1", "a", "tab-1", "tap", now.Add(-5*time.Minute))
	s.Checkin("e1", "a", "tab-1", "qr", now.Add(-4*time.Minute)) // уже отмечен — повтор
	s.UndoCheckin("e1", "a")
	s.Checkin("e1", "a", "tab-2", "qr", now.Add(-2*time.Minute))
	s.QRMiss("e1", "9999:8888")
	s.Upsert([]Guest{{ID: "w1", Event: "e1", Name: "Ольга", Source: "walkin"}})

	j := s.Journal()
	counts := map[string]int{}
	for _, a := range j {
		counts[a.Kind]++
	}
	want := map[string]int{"checkin": 2, "again": 1, "undo": 1, "miss": 1, "add": 1}
	if len(j) != 6 || !mapsEqual(counts, want) {
		t.Fatalf("journal counts %v, want %v: %+v", counts, want, j)
	}
	for _, a := range j {
		if a.Kind == "again" && a.Guest != "Иван" {
			t.Fatalf("again entry: %+v", a)
		}
		if a.Kind == "miss" && a.Code != "9999:8888" {
			t.Fatalf("miss entry: %+v", a)
		}
		if a.Kind == "add" && a.Guest != "Ольга" {
			t.Fatalf("add entry: %+v", a)
		}
		if a.Kind == "checkin" && a.Via == "" {
			t.Fatalf("checkin entry without via: %+v", a)
		}
	}
	s.Close()

	// после перезапуска журнал и отметки восстанавливаются из файла
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if got := len(s2.Journal()); got != 6 {
		t.Fatalf("journal after restart: %d", got)
	}
	gs := s2.Search("e1", "Иван")
	if len(gs) != 1 || gs[0].ArrivedAt == "" {
		t.Fatalf("checkin after undo must survive restart: %+v", gs)
	}
}

func TestOriginalKeepsFirstState(t *testing.T) {
	s := tempStore(t)
	s.Upsert([]Guest{{ID: "g1", Event: "e1", Name: "Иван", TicketType: "Сольный билет", Team: "X"}})
	s.Upsert([]Guest{{ID: "g1", Event: "e1", Name: "Иван", TicketType: "Командный билет", Team: "Y"}})
	o, ok := s.Original("e1", "g1")
	if !ok || o.TicketType != "Сольный билет" || o.Team != "X" {
		t.Fatalf("original: ok=%v %+v", ok, o)
	}
	got := s.Search("e1", "")
	if len(got) != 1 || got[0].TicketType != "Командный билет" || got[0].Team != "Y" {
		t.Fatalf("current: %+v", got)
	}
}

func mapsEqual(a, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
