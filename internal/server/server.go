package server

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"hello-guest/internal/store"
	"hello-guest/internal/timepad"
)

type Config struct {
	PinHash string `json:"pin_hash"`
	PinSalt string `json:"pin_salt"`
}

func LoadOrCreateConfig(dir string) (*Config, string, error) {
	path := filepath.Join(dir, "config.json")
	if b, err := os.ReadFile(path); err == nil {
		var c Config
		if json.Unmarshal(b, &c) == nil && c.PinHash != "" {
			return &c, "", nil
		}
	}
	pin, err := randomPin()
	if err != nil {
		return nil, "", err
	}
	salt := randHex(16)
	c := &Config{PinHash: hashPin(pin, salt), PinSalt: salt}
	b, _ := json.MarshalIndent(c, "", "  ")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return nil, "", err
	}
	return c, pin, nil
}

func (c *Config) Check(pin string) bool {
	got := hashPin(pin, c.PinSalt)
	return subtle.ConstantTimeCompare([]byte(got), []byte(c.PinHash)) == 1
}

func hashPin(pin, salt string) string {
	h := sha256.Sum256([]byte(salt + ":" + pin))
	return hex.EncodeToString(h[:])
}

func randomPin() (string, error) {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	var sb strings.Builder
	for _, x := range b {
		sb.WriteByte('0' + x%10)
	}
	return sb.String(), nil
}

func randHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

type Server struct {
	cfg     *Config
	store   *store.Store
	mux     *http.ServeMux
	now     func() time.Time
	dataDir string
}

func New(cfg *Config, st *store.Store, dataDir string) *Server {
	s := &Server{cfg: cfg, store: st, mux: http.NewServeMux(), now: time.Now, dataDir: dataDir}
	s.routes()
	return s
}

func (s *Server) Handler() http.Handler { return s.mux }

func (s *Server) routes() {
	s.mux.HandleFunc("/api/pin", s.handlePin)
	s.mux.HandleFunc("/api/events", s.authed(s.handleEvents))
	s.mux.HandleFunc("/api/state", s.authed(s.handleState))
	s.mux.HandleFunc("/api/checkin", s.authed(s.handleCheckin))
	s.mux.HandleFunc("/api/undo", s.authed(s.handleUndo))
	s.mux.HandleFunc("/api/guest", s.authed(s.handleGuest))
	s.mux.HandleFunc("/api/import", s.authed(s.handleImport))
	s.mux.HandleFunc("/api/refresh", s.authed(s.handleRefresh))
	s.mux.HandleFunc("/api/stats", s.authed(s.handleStats))
	s.mux.HandleFunc("/api/qrmiss", s.authed(s.handleQRMiss))
	s.mux.HandleFunc("/api/event/rename", s.authed(s.handleEventRename))
	s.mux.HandleFunc("/api/event/hide", s.authed(s.handleEventHide))
	s.mux.HandleFunc("/api/admin/scans", s.authed(s.handleAdminScans))
	s.mux.HandleFunc("/api/admin/report", s.authed(s.handleAdminReport))
	s.mux.HandleFunc("/jsQR.js", s.handleJSQR)
	s.mux.HandleFunc("/zxing.js", s.handleZXing)
	s.mux.HandleFunc("/admin", s.handleAdmin)
	s.mux.HandleFunc("/", s.handleIndex)
}

func (s *Server) authed(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pin := r.Header.Get("X-Pin")
		if pin == "" {
			pin = r.URL.Query().Get("pin")
		}
		if !s.cfg.Check(pin) {
			writeErr(w, http.StatusUnauthorized, "bad pin")
			return
		}
		h(w, r)
	}
}

func (s *Server) handlePin(w http.ResponseWriter, r *http.Request) {
	pin := r.Header.Get("X-Pin")
	if s.cfg.Check(pin) {
		writeJSON(w, map[string]bool{"ok": true})
		return
	}
	writeErr(w, http.StatusUnauthorized, "bad pin")
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	evs := s.store.Events()
	type row struct {
		Event   string `json:"event"`
		Total   int    `json:"total"`
		Arrived int    `json:"arrived"`
		Hidden  bool   `json:"hidden"`
	}
	out := make([]row, 0, len(evs))
	for _, e := range evs {
		total, arrived := s.store.Count(e)
		out = append(out, row{e, total, arrived, s.store.EventHidden(e)})
	}
	writeJSON(w, out)
}

func (s *Server) handleEventRename(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var req struct {
		Event string `json:"event"`
		New   string `json:"new"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad json")
		return
	}
	if err := s.store.RenameEvent(req.Event, req.New); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) handleEventHide(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var req struct {
		Event  string `json:"event"`
		Hidden bool   `json:"hidden"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad json")
		return
	}
	if err := s.store.HideEvent(req.Event, req.Hidden); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	ev := r.URL.Query().Get("event")
	writeJSON(w, map[string]any{"guests": s.store.Search(ev, "")})
}

func (s *Server) handleCheckin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var req struct {
		Event  string `json:"event"`
		Guest  string `json:"guest_id"`
		Device string `json:"device"`
		Via    string `json:"via"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad json")
		return
	}
	if req.Via == "" {
		req.Via = "tap"
	}
	already, err := s.store.Checkin(req.Event, req.Guest, req.Device, req.Via, s.now())
	if err == store.ErrNotFound {
		writeErr(w, http.StatusNotFound, "guest not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true, "already": already})
}

func (s *Server) handleUndo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var req struct {
		Event string `json:"event"`
		Guest string `json:"guest_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad json")
		return
	}
	if err := s.store.UndoCheckin(req.Event, req.Guest); err != nil {
		writeErr(w, http.StatusNotFound, "guest not found")
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) handleGuest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var g store.Guest
	if err := json.NewDecoder(r.Body).Decode(&g); err != nil {
		writeErr(w, http.StatusBadRequest, "bad json")
		return
	}
	if g.Name == "" {
		writeErr(w, http.StatusBadRequest, "name required")
		return
	}
	if g.ID == "" {
		g.ID = "walkin:" + randHex(8)
	}
	g.Source = "walkin"
	if _, _, err := s.store.Upsert([]store.Guest{g}); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true, "id": g.ID})
}

func (s *Server) handleImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	ev := r.URL.Query().Get("event")
	if ev == "" {
		writeErr(w, http.StatusBadRequest, "event required")
		return
	}
	body := http.MaxBytesReader(w, r.Body, 20<<20)
	guests, err := importFrom(body, ev)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	added, updated, err := s.store.Upsert(guests)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true, "total": len(guests), "added": added, "updated": updated})
}

func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var req struct {
		Event     string `json:"event"`
		TimepadID int64  `json:"timepad_id"`
		Token     string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad json")
		return
	}
	if req.Token == "" {
		req.Token = os.Getenv("TIMEPAD_TOKEN")
	}
	if req.Token == "" {
		writeErr(w, http.StatusBadRequest, "no token")
		return
	}
	if req.Event == "" || req.TimepadID == 0 {
		writeErr(w, http.StatusBadRequest, "event and timepad_id required")
		return
	}
	guests, err := timepad.New(req.Token).FetchOrders(req.TimepadID)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	for i := range guests {
		guests[i].Event = req.Event
	}
	added, updated, err := s.store.Upsert(guests)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true, "total": len(guests), "added": added, "updated": updated})
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	ev := r.URL.Query().Get("event")
	st := s.store.Stats(ev)
	writeJSON(w, map[string]any{"event": ev, "total": st.Total, "arrived": st.Arrived,
		"qr": st.QR, "tap": st.Tap, "walkins": st.Walkins, "false_qr": st.FalseQR})
}

func (s *Server) handleQRMiss(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var req struct {
		Event string `json:"event"`
		Code  string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad json")
		return
	}
	if err := s.store.QRMiss(req.Event, req.Code); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// Журнал сканирований для /admin: успешные QR-отметки и ненайденные коды.
func (s *Server) handleAdminScans(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.store.Scans())
}

// Отчёт регистраций: CSV по событию, сохраняется в <data>/reports/ и
// отдаётся файлом тому, кто его запросил.
func (s *Server) handleAdminReport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	ev := r.URL.Query().Get("event")
	if ev == "" {
		writeErr(w, http.StatusBadRequest, "event required")
		return
	}
	guests := s.store.Search(ev, "")
	if len(guests) == 0 {
		writeErr(w, http.StatusNotFound, "event not found")
		return
	}
	via := map[string]string{}
	for _, rec := range s.store.Checkins(ev) {
		via[rec.GuestID] = rec.Via
	}
	var buf bytes.Buffer
	buf.Write([]byte("\xEF\xBB\xBF"))
	cw := csv.NewWriter(&buf)
	cw.Comma = ';'
	cw.Write([]string{"Имя", "Email", "Телефон", "Тип билета", "Номер билета", "Штрихкод", "Отмечен", "Время прихода", "Способ", "Источник"})
	for _, g := range guests {
		marked, arrived := "—", ""
		if g.ArrivedAt != "" {
			marked = "да"
			if t, err := time.Parse(time.RFC3339, g.ArrivedAt); err == nil {
				arrived = t.Local().Format("02.01.2006 15:04")
			}
		}
		method := "—"
		switch via[g.ID] {
		case "qr":
			method = "QR"
		case "tap":
			method = "список"
		}
		src := ""
		if g.Source == "walkin" {
			src = "вручную"
		}
		cw.Write([]string{g.Name, g.Email, g.Phone, g.TicketType, g.TicketNumber, g.Barcode, marked, arrived, method, src})
	}
	cw.Flush()
	name := reportBase(ev) + ".csv"
	dir := filepath.Join(s.dataDir, "reports")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("X-Report-File", url.PathEscape(filepath.ToSlash(path)))
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(name))
	w.Write(buf.Bytes())
}

// reportBase — безопасное имя файла отчёта: имя события + метка времени.
func reportBase(ev string) string {
	s := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == '.' {
			return r
		}
		if r == ' ' {
			return '-'
		}
		return '_'
	}, ev)
	if rs := []rune(s); len(rs) > 40 {
		s = string(rs[:40])
	}
	if s == "" {
		s = "event"
	}
	return s + "-" + time.Now().Format("20060102-150405")
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

var errNoBody = errors.New("empty body")
