package store

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type Guest struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Email        string `json:"email,omitempty"`
	Phone        string `json:"phone,omitempty"`
	TicketType   string `json:"ticket_type,omitempty"`
	OrderID      string `json:"order_id,omitempty"`
	TicketNumber string `json:"ticket_number,omitempty"`
	Source       string `json:"source,omitempty"`
	Status       string `json:"status,omitempty"`
	ArrivedAt    string `json:"arrived_at,omitempty"`
	Event        string `json:"event"`
}

type Record struct {
	Kind    string    `json:"kind"`
	Event   string    `json:"event"`
	TS      time.Time `json:"ts"`
	Guest   *Guest    `json:"guest,omitempty"`
	Device  string    `json:"device,omitempty"`
	GuestID string    `json:"guest_id,omitempty"`
	New     string    `json:"new,omitempty"`    // rename_event: новое имя события
	Hidden  *bool     `json:"hidden,omitempty"` // event_meta: скрыть/показать событие
	Via     string    `json:"via,omitempty"`    // checkin: qr | tap
	Code    string    `json:"code,omitempty"`   // qrmiss: распознанный код билета
}

type Store struct {
	mu       sync.RWMutex
	path     string
	file     *os.File
	guests   map[string]map[string]*Guest
	checkins map[string][]Record
	qrmiss   map[string][]Record
	hidden   map[string]bool
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	s := &Store{
		path:     path,
		file:     f,
		guests:   map[string]map[string]*Guest{},
		checkins: map[string][]Record{},
		qrmiss:   map[string][]Record{},
		hidden:   map[string]bool{},
	}
	if err := s.replay(); err != nil {
		f.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) replay() error {
	sc := bufio.NewScanner(s.file)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var r Record
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			continue
		}
		s.apply(&r)
	}
	return sc.Err()
}

func (s *Store) apply(r *Record) {
	switch r.Kind {
	case "upsert":
		if r.Guest == nil {
			return
		}
		if s.guests[r.Event] == nil {
			s.guests[r.Event] = map[string]*Guest{}
		}
		g := *r.Guest
		g.Event = r.Event
		if prev, ok := s.guests[r.Event][g.ID]; ok && prev.ArrivedAt != "" && g.ArrivedAt == "" {
			g.ArrivedAt = prev.ArrivedAt
		}
		s.guests[r.Event][g.ID] = &g
	case "checkin":
		s.checkins[r.Event] = append(s.checkins[r.Event], *r)
		if s.guests[r.Event] != nil {
			if g, ok := s.guests[r.Event][r.GuestID]; ok && g.ArrivedAt == "" {
				g.ArrivedAt = r.TS.UTC().Format(time.RFC3339)
			}
		}
	case "event_meta":
		if r.Hidden != nil {
			s.hidden[r.Event] = *r.Hidden
		}
	case "qrmiss":
		s.qrmiss[r.Event] = append(s.qrmiss[r.Event], *r)
	case "rename_event":
		if r.New == "" || r.Event == r.New {
			return
		}
		if _, exists := s.guests[r.New]; exists {
			return
		}
		if gs, ok := s.guests[r.Event]; ok {
			for _, g := range gs {
				g.Event = r.New
			}
			s.guests[r.New] = gs
			delete(s.guests, r.Event)
		}
		if cs, ok := s.checkins[r.Event]; ok {
			s.checkins[r.New] = cs
			delete(s.checkins, r.Event)
		}
		if h, ok := s.hidden[r.Event]; ok {
			s.hidden[r.New] = h
			delete(s.hidden, r.Event)
		}
	}
}

func (s *Store) append(r *Record) error {
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if _, err := s.file.Write(append(b, '\n')); err != nil {
		return err
	}
	return s.file.Sync()
}

func (s *Store) Upsert(guests []Guest) (added, updated int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	for _, g := range guests {
		ev := s.guests[g.Event]
		if ev == nil {
			ev = map[string]*Guest{}
			s.guests[g.Event] = ev
		}
		if prev, ok := ev[g.ID]; ok {
			g.ArrivedAt = prev.ArrivedAt
			updated++
		} else {
			added++
		}
		gg := g
		r := Record{Kind: "upsert", Event: g.Event, TS: now, Guest: &gg}
		if err := s.append(&r); err != nil {
			return added, updated, err
		}
		s.apply(&r)
	}
	return added, updated, nil
}

var ErrNotFound = errors.New("guest not found")

func (s *Store) Checkin(event, guestID, device, via string, now time.Time) (already bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ev := s.guests[event]
	if ev == nil {
		return false, ErrNotFound
	}
	g, ok := ev[guestID]
	if !ok {
		return false, ErrNotFound
	}
	if g.ArrivedAt != "" {
		return true, nil
	}
	r := Record{Kind: "checkin", Event: event, TS: now.UTC(), GuestID: guestID, Device: device, Via: via}
	if err := s.append(&r); err != nil {
		return false, err
	}
	s.apply(&r)
	return false, nil
}

// QRMiss фиксирует сканирование ненайденного QR-кода.
func (s *Store) QRMiss(event, code string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := Record{Kind: "qrmiss", Event: event, Code: code, TS: time.Now().UTC()}
	if err := s.append(&r); err != nil {
		return err
	}
	s.apply(&r)
	return nil
}

type Stats struct {
	Total   int `json:"total"`
	Arrived int `json:"arrived"`
	QR      int `json:"qr"`
	Tap     int `json:"tap"`
	Walkins int `json:"walkins"`
	FalseQR int `json:"false_qr"`
}

// Stats считает итоги события: отмеченных по qr и из списка,
// добавленных вручную (walk-in), ложных сканов QR.
func (s *Store) Stats(event string) Stats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ev := s.guests[event]
	st := Stats{Total: len(ev), FalseQR: len(s.qrmiss[event])}
	via := map[string]string{}
	for _, r := range s.checkins[event] {
		via[r.GuestID] = r.Via
	}
	for _, g := range ev {
		if g.Source == "walkin" {
			st.Walkins++
		}
		if g.ArrivedAt != "" {
			st.Arrived++
			if via[g.ID] == "qr" {
				st.QR++
			} else {
				st.Tap++
			}
		}
	}
	return st
}

func (s *Store) UndoCheckin(event, guestID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ev := s.guests[event]
	if ev == nil {
		return ErrNotFound
	}
	g, ok := ev[guestID]
	if !ok {
		return ErrNotFound
	}
	r := Record{Kind: "undo", Event: event, TS: time.Now().UTC(), GuestID: guestID}
	if err := s.append(&r); err != nil {
		return err
	}
	g.ArrivedAt = ""
	return nil
}

func (s *Store) Events() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.guests))
	for k := range s.guests {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (s *Store) EventHidden(event string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.hidden[event]
}

// RenameEvent переименовывает событие записью в журнал; данные не трогаются.
func (s *Store) RenameEvent(event, newName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if newName == "" || newName == event {
		return errors.New("bad event name")
	}
	if _, ok := s.guests[event]; !ok {
		return ErrNotFound
	}
	if _, ok := s.guests[newName]; ok {
		return errors.New("event already exists")
	}
	r := Record{Kind: "rename_event", Event: event, New: newName, TS: time.Now().UTC()}
	if err := s.append(&r); err != nil {
		return err
	}
	s.apply(&r)
	return nil
}

// HideEvent скрывает или показывает событие записью в журнал; данные остаются.
func (s *Store) HideEvent(event string, hidden bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.guests[event]; !ok {
		return ErrNotFound
	}
	r := Record{Kind: "event_meta", Event: event, TS: time.Now().UTC(), Hidden: &hidden}
	if err := s.append(&r); err != nil {
		return err
	}
	s.apply(&r)
	return nil
}

func (s *Store) Search(event, query string) []Guest {
	s.mu.RLock()
	defer s.mu.RUnlock()
	q := strings.ToLower(strings.TrimSpace(query))
	var out []Guest
	for _, g := range s.guests[event] {
		if q == "" || matches(g, q) {
			out = append(out, *g)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ArrivedAt == "" && out[j].ArrivedAt != "" {
			return true
		}
		if out[i].ArrivedAt != "" && out[j].ArrivedAt == "" {
			return false
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func matches(g *Guest, q string) bool {
	return strings.Contains(strings.ToLower(g.Name), q) ||
		strings.Contains(strings.ToLower(g.Email), q) ||
		strings.Contains(strings.ToLower(g.Phone), q) ||
		strings.Contains(strings.ToLower(g.TicketNumber), q) ||
		strings.Contains(strings.ToLower(g.OrderID), q)
}

func (s *Store) Checkins(event string) []Record {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]Record(nil), s.checkins[event]...)
}

func (s *Store) Count(event string) (total, arrived int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	total = len(s.guests[event])
	for _, g := range s.guests[event] {
		if g.ArrivedAt != "" {
			arrived++
		}
	}
	return total, arrived
}

func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.file != nil {
		return s.file.Close()
	}
	return nil
}

func NormalizeEmail(e string) string {
	e = strings.ToLower(strings.TrimSpace(e))
	if i := strings.Index(e, "+"); i > 0 && strings.Contains(e, "@") {
		at := strings.Index(e, "@")
		if i < at {
			e = e[:i] + e[at:]
		}
	}
	return e
}

func (s *Store) String() string {
	return fmt.Sprintf("store(%s, %d events)", s.path, len(s.guests))
}
