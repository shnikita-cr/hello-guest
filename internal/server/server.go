package server

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

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
	cfg   *Config
	store *store.Store
	mux   *http.ServeMux
	now   func() time.Time
}

func New(cfg *Config, st *store.Store) *Server {
	s := &Server{cfg: cfg, store: st, mux: http.NewServeMux(), now: time.Now}
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
	s.mux.HandleFunc("/jsQR.js", s.handleJSQR)
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
	}
	out := make([]row, 0, len(evs))
	for _, e := range evs {
		total, arrived := s.store.Count(e)
		out = append(out, row{e, total, arrived})
	}
	writeJSON(w, out)
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
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad json")
		return
	}
	already, err := s.store.Checkin(req.Event, req.Guest, req.Device, s.now())
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
	total, arrived := s.store.Count(ev)
	writeJSON(w, map[string]any{"event": ev, "total": total, "arrived": arrived})
}
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

var errNoBody = errors.New("empty body")
