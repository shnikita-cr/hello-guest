package server

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	"hello-guest/internal/store"
	"hello-guest/internal/timepad"
)

type Config struct {
	PinHash  string `json:"pin_hash"`
	PinSalt  string `json:"pin_salt"`
	PinPlain string `json:"pin_plain,omitempty"`
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
	c := &Config{PinHash: hashPin(pin, salt), PinSalt: salt, PinPlain: pin}
	if err := c.save(dir); err != nil {
		return nil, "", err
	}
	return c, pin, nil
}

func (c *Config) save(dir string) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "config.json"), b, 0o600)
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

	// StatusIPs — адреса для экрана статуса; TriggerBackup — ручной бэкап из cmd;
	// OnStop/OnRestart — стоп и перезапуск процесса (живут в cmd).
	StatusIPs     []string
	TriggerBackup func() (string, error)
	BackupTimes   func() (auto, manual time.Time)
	OnStop        func()
	OnRestart     func()

	seenMu  sync.Mutex
	seen    map[string]seenInfo
	events  []clientEvent
	started time.Time
}

// clientEvent: устройство подключилось или вышло из сети (по опросам страниц).
type clientEvent struct {
	Kind   string `json:"kind"` // join | leave
	IP     string `json:"ip"`
	Device string `json:"device,omitempty"` // код устройства клиента (hg_dev)
	At     string `json:"at"`               // HH:MM:SS
	at     time.Time
}

// seenInfo: последний опрос клиента и его код устройства.
type seenInfo struct {
	at  time.Time
	dev string
}

// Version выставляется из cmd приложения (git describe).
var Version = "dev"

func New(cfg *Config, st *store.Store, dataDir string) *Server {
	s := &Server{cfg: cfg, store: st, mux: http.NewServeMux(), now: time.Now, dataDir: dataDir,
		seen: map[string]seenInfo{}, started: time.Now()}
	s.routes()
	return s
}

func (s *Server) Handler() http.Handler { return s.mux }

func (s *Server) routes() {
	s.mux.HandleFunc("/api/pin", s.handlePin)
	s.mux.HandleFunc("/api/version", s.handleVersion)
	s.mux.HandleFunc("/api/events", s.authed(s.handleEvents))
	s.mux.HandleFunc("/api/state", s.authed(s.handleState))
	s.mux.HandleFunc("/api/checkin", s.authed(s.handleCheckin))
	s.mux.HandleFunc("/api/member", s.authed(s.handleMember))
	s.mux.HandleFunc("/api/undo", s.authed(s.handleUndo))
	s.mux.HandleFunc("/api/guest", s.authed(s.handleGuest))
	s.mux.HandleFunc("/api/import", s.authed(s.handleImport))
	s.mux.HandleFunc("/api/refresh", s.authed(s.handleRefresh))
	s.mux.HandleFunc("/api/stats", s.authed(s.handleStats))
	s.mux.HandleFunc("/api/qrmiss", s.authed(s.handleQRMiss))
	s.mux.HandleFunc("/api/event/rename", s.authed(s.handleEventRename))
	s.mux.HandleFunc("/api/event/hide", s.authed(s.handleEventHide))
	s.mux.HandleFunc("/api/admin/journal", s.authed(s.handleAdminJournal))
	s.mux.HandleFunc("/api/admin/report", s.authed(s.handleAdminReport))
	s.mux.HandleFunc("/jsQR.js", s.handleJSQR)
	s.mux.HandleFunc("/sw.js", s.handleSW)
	s.mux.HandleFunc("/zxing.js", s.handleZXing)
	s.mux.HandleFunc("/admin", s.handleAdmin)
	s.mux.HandleFunc("/status", s.local(s.handleStatusPage))
	s.mux.HandleFunc("/reset", s.handleReset)
	s.mux.HandleFunc("/api/status", s.local(s.handleStatusAPI))
	s.mux.HandleFunc("/api/admin/backup", s.local(s.handleBackupNow))
	s.mux.HandleFunc("/api/admin/stop", s.local(s.handleStop))
	s.mux.HandleFunc("/api/admin/restart", s.local(s.handleRestart))
	s.mux.HandleFunc("/", s.handleIndex)
}

func remoteHost(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// local: экран статуса показывает PIN, поэтому только с самого ноутбука;
// запросы через cloudflared приходят с loopback, но с заголовками крайнего — их закрываем.
func (s *Server) local(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ip := net.ParseIP(remoteHost(r))
		if ip == nil || !ip.IsLoopback() || r.Header.Get("Cf-Ray") != "" || r.Header.Get("Cf-Connecting-Ip") != "" {
			writeErr(w, http.StatusForbidden, "local only")
			return
		}
		h(w, r)
	}
}

// touch: считаем клиентов по опросу /api/pin (каждые 2.5 с со страниц),
// loopback не считаем — это сам ноутбук.
func (s *Server) touch(r *http.Request) {
	host := remoteHost(r)
	if ip := net.ParseIP(host); ip == nil || ip.IsLoopback() {
		return
	}
	dev := r.URL.Query().Get("device")
	s.seenMu.Lock()
	defer s.seenMu.Unlock()
	prev, existed := s.seen[host]
	if dev != "" {
		prev.dev = dev // не затираем код пустым опросом
	}
	prev.at = time.Now()
	s.seen[host] = prev
	if !existed {
		s.pushEventLocked(clientEvent{Kind: "join", IP: host, Device: prev.dev, At: nowHM(), at: time.Now()})
	}
}

func nowHM() string { return time.Now().Format("15:04:05") }

func (s *Server) pushEventLocked(e clientEvent) {
	s.events = append(s.events, e)
	if len(s.events) > 12 {
		s.events = s.events[len(s.events)-12:]
	}
}

func (s *Server) clients() int {
	s.seenMu.Lock()
	defer s.seenMu.Unlock()
	n := 0
	for ip, v := range s.seen {
		since := time.Since(v.at)
		if since > 10*time.Second {
			delete(s.seen, ip)
			// ушедший недавно — показываем; старые хвосты при открытии
			// страницы молча выкидываем, чтобы не флудить историей
			if since <= 5*time.Minute {
				s.pushEventLocked(clientEvent{Kind: "leave", IP: ip, Device: v.dev,
					At: v.at.Add(10 * time.Second).Format("15:04:05"), at: v.at.Add(10 * time.Second)})
			}
			continue
		}
		n++
	}
	return n
}

// clientEvents: свежие события за 15 минут, новые сверху.
func (s *Server) clientEvents() []clientEvent {
	s.seenMu.Lock()
	defer s.seenMu.Unlock()
	out := make([]clientEvent, 0, len(s.events))
	cut := time.Now().Add(-15 * time.Minute)
	for i := len(s.events) - 1; i >= 0; i-- {
		if s.events[i].at.After(cut) {
			out = append(out, s.events[i])
		}
	}
	return out
}

func (s *Server) authed(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.touch(r)
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
	s.touch(r)
	pin := r.Header.Get("X-Pin")
	if s.cfg.Check(pin) {
		writeJSON(w, map[string]bool{"ok": true})
		return
	}
	writeErr(w, http.StatusUnauthorized, "bad pin")
}

func (s *Server) handleStatusPage(w http.ResponseWriter, r *http.Request) {
	s.serveHTML(w, r, "web/status.html")
}

// handleReset: страница сброса кэша/воркера на устройстве (телефон, планшет).
func (s *Server) handleReset(w http.ResponseWriter, r *http.Request) {
	s.serveHTML(w, r, "web/reset.html")
}

func (s *Server) handleStatusAPI(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{
		"version": Version,
		"clients": s.clients(),
		"ips":     s.StatusIPs,
		"uptime":  int(time.Since(s.started).Seconds()),
		"backup":  s.TriggerBackup != nil,
		"backup_auto": func() string {
			if s.BackupTimes == nil {
				return ""
			}
			a, _ := s.BackupTimes()
			if a.IsZero() {
				return ""
			}
			return a.Format("02.01.2006 15:04")
		}(),
		"backup_manual": func() string {
			if s.BackupTimes == nil {
				return ""
			}
			_, m := s.BackupTimes()
			if m.IsZero() {
				return ""
			}
			return m.Format("02.01.2006 15:04")
		}(),
		"events": s.clientEvents(),
	})
}

func requirePost(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "post only")
		return false
	}
	return true
}

func (s *Server) handleBackupNow(w http.ResponseWriter, r *http.Request) {
	if !requirePost(w, r) {
		return
	}
	if s.TriggerBackup == nil {
		writeErr(w, http.StatusNotImplemented, "backup недоступен")
		return
	}
	file, err := s.TriggerBackup()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true, "file": file, "skipped": file == ""})
}

func (s *Server) handleStop(w http.ResponseWriter, r *http.Request) {
	if !requirePost(w, r) {
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
	if s.OnStop != nil {
		go s.OnStop()
	}
}

func (s *Server) handleRestart(w http.ResponseWriter, r *http.Request) {
	if !requirePost(w, r) {
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
	if s.OnRestart != nil {
		go s.OnRestart()
	}
}

func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]string{"version": Version})
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

// handleMember: снапшот отметок прихода по членам группы.
func (s *Server) handleMember(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var req struct {
		Event   string          `json:"event"`
		Guest   string          `json:"guest_id"`
		Members map[string]bool `json:"members"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad json")
		return
	}
	if err := s.store.SetMembers(req.Event, req.Guest, req.Members, s.now()); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "guest not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
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
	raw, err := io.ReadAll(body)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	// сырой файл выгрузки Timepad сохраняем в <data>/imports/
	if dir := filepath.Join(s.dataDir, "imports"); os.MkdirAll(dir, 0o755) == nil {
		ext := ".csv"
		if len(raw) >= 4 && raw[0] == 'P' && raw[1] == 'K' {
			ext = ".xlsx"
		}
		os.WriteFile(filepath.Join(dir, time.Now().Format("20060102-150405")+ext), raw, 0o644)
	}
	guests, err := importFrom(bytes.NewReader(raw), ev)
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

// Журнал действий для /admin: отметки, повторные сканы, снятия,
// добавления вручную и ненайденные коды.
func (s *Server) handleAdminJournal(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.store.Journal())
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
	rows := [][]string{{"Имя", "Email", "Телефон", "Тип билета", "Номер билета", "Штрихкод", "Отмечен", "Время прихода", "Способ", "Источник"}}
	for _, g := range guests {
		marked, arrived := "—", ""
		if g.ArrivedAt != "" {
			marked = "да"
			if t, err := time.Parse(time.RFC3339, g.ArrivedAt); err == nil {
				arrived = t.Local().Format("02.01.2006 15:04")
			}
		}
		method := "—"
		if g.ArrivedAt != "" {
			switch via[g.ID] {
			case "qr":
				method = "QR/штрихкод"
			case "tap":
				method = "вручную"
			}
		}
		src := ""
		if g.Source == "walkin" {
			src = "вручную"
		}
		rows = append(rows, []string{g.Name, g.Email, g.Phone, g.TicketType, g.TicketNumber, g.Barcode, marked, arrived, method, src})
	}
	var payload []byte
	ext, ct := ".csv", "text/csv; charset=utf-8"
	if r.URL.Query().Get("format") == "xlsx" {
		x, err := xlsxBytes(rows)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		payload, ext, ct = x, ".xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	} else {
		payload = csvBytes(rows)
	}
	name := reportBase(ev) + ext
	dir := filepath.Join(s.dataDir, "reports")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("X-Report-File", url.PathEscape(filepath.ToSlash(path)))
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(name))
	w.Write(payload)
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
