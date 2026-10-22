package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hello-guest/internal/store"
	"hello-guest/internal/timepad"
)

func newTestServer(t *testing.T) (*Server, *store.Store, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "j.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	cfg := &Config{PinHash: hashPin("123456", "salt"), PinSalt: "salt", PinPlain: "123456"}
	return New(cfg, st, dir), st, dir
}

// do — запрос от LAN-клиента с верным PIN.
func do(s *Server, method, path, body string) *httptest.ResponseRecorder {
	return doFrom(s, method, path, body, "10.9.8.7:5000", "123456")
}

func doFrom(s *Server, method, path, body, remote, pin string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = remote
	if pin != "" {
		req.Header.Set("X-Pin", pin)
	}
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	return rr
}

func decodeObj(t *testing.T, rr *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &m); err != nil {
		t.Fatalf("битый json: %s", rr.Body.String())
	}
	return m
}

func TestPinVersionAndAuth(t *testing.T) {
	s, _, _ := newTestServer(t)

	if rr := do(s, "GET", "/api/pin", ""); rr.Code != 200 || !strings.Contains(rr.Body.String(), `"ok":true`) {
		t.Fatalf("верный pin: code=%d body=%s", rr.Code, rr.Body.String())
	}
	if rr := doFrom(s, "GET", "/api/pin", "", "10.9.8.7:1", "999999"); rr.Code != http.StatusUnauthorized {
		t.Fatalf("неверный pin: %d", rr.Code)
	}

	if rr := do(s, "GET", "/api/version", ""); rr.Code != 200 || !strings.Contains(rr.Body.String(), "version") {
		t.Fatalf("version: code=%d body=%s", rr.Code, rr.Body.String())
	}

	// PIN можно передать и query-параметром
	if rr := doFrom(s, "GET", "/api/events?pin=123456", "", "10.9.8.7:1", ""); rr.Code != 200 {
		t.Fatalf("pin query: %d", rr.Code)
	}
	if rr := doFrom(s, "GET", "/api/events?pin=000000", "", "10.9.8.7:1", ""); rr.Code != http.StatusUnauthorized {
		t.Fatalf("плохой pin query: %d", rr.Code)
	}
	if rr := doFrom(s, "GET", "/api/events", "", "10.9.8.7:1", ""); rr.Code != http.StatusUnauthorized {
		t.Fatalf("без pin: %d", rr.Code)
	}
}

func TestPostEndpointsRequirePost(t *testing.T) {
	s, _, _ := newTestServer(t)
	paths := []string{"/api/checkin", "/api/member", "/api/team", "/api/undo", "/api/guest",
		"/api/import", "/api/refresh", "/api/qrmiss", "/api/event/rename", "/api/event/hide"}
	for _, p := range paths {
		if rr := do(s, "GET", p, ""); rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("GET %s: code=%d, ждали 405", p, rr.Code)
		}
	}
}

func TestEventsStateStatsJournal(t *testing.T) {
	s, st, _ := newTestServer(t)

	rr := do(s, "POST", "/api/guest", `{"event":"ev1","name":"Иван Иванов","ticket_type":"Standard"}`)
	if rr.Code != 200 {
		t.Fatalf("guest: %d %s", rr.Code, rr.Body.String())
	}
	walkinID := decodeObj(t, rr)["id"].(string)

	if _, _, err := st.Upsert([]store.Guest{{ID: "t1", Event: "ev1", Name: "Пётр", TicketNumber: "117726839:97059462", TicketType: "Standard"}}); err != nil {
		t.Fatal(err)
	}
	if rr := do(s, "POST", "/api/checkin", `{"event":"ev1","guest_id":"t1","via":"qr"}`); rr.Code != 200 {
		t.Fatalf("checkin qr: %d %s", rr.Code, rr.Body.String())
	}
	if rr := do(s, "POST", "/api/checkin", `{"event":"ev1","guest_id":"`+walkinID+`"}`); rr.Code != 200 {
		t.Fatalf("checkin tap: %d %s", rr.Code, rr.Body.String())
	}
	if rr := do(s, "POST", "/api/qrmiss", `{"event":"ev1","code":"999000"}`); rr.Code != 200 {
		t.Fatalf("qrmiss: %d %s", rr.Code, rr.Body.String())
	}
	if rr := do(s, "POST", "/api/qrmiss", "{oops"); rr.Code != http.StatusBadRequest {
		t.Fatalf("qrmiss битый json: %d", rr.Code)
	}

	if rr := do(s, "GET", "/api/events", ""); rr.Code != 200 || !strings.Contains(rr.Body.String(), `"event":"ev1"`) {
		t.Fatalf("events: %d %s", rr.Code, rr.Body.String())
	}
	if rr := do(s, "GET", "/api/state?event=ev1", ""); rr.Code != 200 || !strings.Contains(rr.Body.String(), "Иван Иванов") {
		t.Fatalf("state: %d %s", rr.Code, rr.Body.String())
	}

	rr = do(s, "GET", "/api/stats?event=ev1", "")
	if rr.Code != 200 {
		t.Fatalf("stats: %d", rr.Code)
	}
	stj := decodeObj(t, rr)
	want := map[string]float64{"total": 2, "arrived": 2, "qr": 1, "tap": 1, "walkins": 1, "false_qr": 1}
	for k, v := range want {
		if got, _ := stj[k].(float64); got != v {
			t.Errorf("stats[%s] = %v, ждали %v (тело: %s)", k, stj[k], v, rr.Body.String())
		}
	}

	rr = do(s, "GET", "/api/admin/journal", "")
	if rr.Code != 200 || !json.Valid(rr.Body.Bytes()) {
		t.Fatalf("journal: %d %s", rr.Code, rr.Body.String())
	}
}

func TestCheckinUndoFlow(t *testing.T) {
	s, st, _ := newTestServer(t)
	if _, _, err := st.Upsert([]store.Guest{{ID: "g1", Event: "e1", Name: "Анна"}}); err != nil {
		t.Fatal(err)
	}

	if rr := do(s, "POST", "/api/checkin", "{oops"); rr.Code != http.StatusBadRequest {
		t.Fatalf("битый json: %d", rr.Code)
	}
	if rr := do(s, "POST", "/api/checkin", `{"event":"e1","guest_id":"nope"}`); rr.Code != http.StatusNotFound {
		t.Fatalf("нет гостя: %d", rr.Code)
	}

	rr := do(s, "POST", "/api/checkin", `{"event":"e1","guest_id":"g1"}`)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"already":false`) {
		t.Fatalf("первый checkin: %d %s", rr.Code, rr.Body.String())
	}
	rr = do(s, "POST", "/api/checkin", `{"event":"e1","guest_id":"g1","device":"tab-1","via":"qr"}`)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"already":true`) {
		t.Fatalf("повторный checkin: %d %s", rr.Code, rr.Body.String())
	}

	if rr := do(s, "POST", "/api/undo", "{oops"); rr.Code != http.StatusBadRequest {
		t.Fatalf("undo битый json: %d", rr.Code)
	}
	if rr := do(s, "POST", "/api/undo", `{"event":"nope","guest_id":"g1"}`); rr.Code != http.StatusNotFound {
		t.Fatalf("undo нет события: %d", rr.Code)
	}
	if rr := do(s, "POST", "/api/undo", `{"event":"e1","guest_id":"nope"}`); rr.Code != http.StatusNotFound {
		t.Fatalf("undo нет гостя: %d", rr.Code)
	}
	if rr := do(s, "POST", "/api/undo", `{"event":"e1","guest_id":"g1"}`); rr.Code != 200 {
		t.Fatalf("undo: %d %s", rr.Code, rr.Body.String())
	}
	if g := st.Search("e1", ""); len(g) != 1 || g[0].ArrivedAt != "" {
		t.Fatalf("после undo: %+v", g[0])
	}
}

func TestMemberAndTeam(t *testing.T) {
	s, st, _ := newTestServer(t)
	if _, _, err := st.Upsert([]store.Guest{{ID: "g1", Event: "e1", Name: "Капитан", Members: []string{"Капитан"}}}); err != nil {
		t.Fatal(err)
	}

	if rr := do(s, "POST", "/api/member", "{oops"); rr.Code != http.StatusBadRequest {
		t.Fatalf("member битый json: %d", rr.Code)
	}
	if rr := do(s, "POST", "/api/member", `{"event":"e1","guest_id":"nope","members":{"X":true}}`); rr.Code != http.StatusNotFound {
		t.Fatalf("member нет гостя: %d", rr.Code)
	}
	rr := do(s, "POST", "/api/member", `{"event":"e1","guest_id":"g1","members":{"Капитан":true}}`)
	if rr.Code != 200 {
		t.Fatalf("member: %d %s", rr.Code, rr.Body.String())
	}
	g := st.Search("e1", "")
	if len(g) != 1 || !g[0].MemberArrived["Капитан"] {
		t.Fatalf("member_arrived не сохранился: %+v", g[0])
	}

	if rr := do(s, "POST", "/api/team", "{oops"); rr.Code != http.StatusBadRequest {
		t.Fatalf("team битый json: %d", rr.Code)
	}
	if rr := do(s, "POST", "/api/team", `{"event":"e1","guest_id":"nope","team":"Волки","members":["X"]}`); rr.Code != http.StatusNotFound {
		t.Fatalf("team нет гостя: %d", rr.Code)
	}
	rr = do(s, "POST", "/api/team", `{"event":"e1","guest_id":"g1","team":"Волки","members":["Капитан","Борис"]}`)
	if rr.Code != 200 {
		t.Fatalf("team: %d %s", rr.Code, rr.Body.String())
	}
	g = st.Search("e1", "")
	if len(g) != 1 || g[0].Team != "Волки" || len(g[0].Members) != 2 {
		t.Fatalf("команда не сохранилась: %+v", g[0])
	}
}

func TestGuestValidation(t *testing.T) {
	s, st, _ := newTestServer(t)

	if rr := do(s, "POST", "/api/guest", "{oops"); rr.Code != http.StatusBadRequest {
		t.Fatalf("битый json: %d", rr.Code)
	}
	if rr := do(s, "POST", "/api/guest", `{"event":"e1"}`); rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "name") {
		t.Fatalf("без имени: %d %s", rr.Code, rr.Body.String())
	}

	rr := do(s, "POST", "/api/guest", `{"id":"w1","event":"e1","name":"Ольга"}`)
	if rr.Code != 200 || decodeObj(t, rr)["id"] != "w1" {
		t.Fatalf("со своим id: %d %s", rr.Code, rr.Body.String())
	}
	rr = do(s, "POST", "/api/guest", `{"event":"e1","name":"Дарья"}`)
	id, _ := decodeObj(t, rr)["id"].(string)
	if rr.Code != 200 || !strings.HasPrefix(id, "walkin:") {
		t.Fatalf("генерация id: %d %s", rr.Code, rr.Body.String())
	}
	g := st.Search("e1", "")
	if len(g) != 2 || g[0].Source != "walkin" {
		t.Fatalf("walkin-гости: %+v", g)
	}
}

func TestEventRenameAndHide(t *testing.T) {
	s, st, _ := newTestServer(t)
	if _, _, err := st.Upsert([]store.Guest{{ID: "g1", Event: "old", Name: "Имя"}}); err != nil {
		t.Fatal(err)
	}

	if rr := do(s, "POST", "/api/event/rename", "{oops"); rr.Code != http.StatusBadRequest {
		t.Fatalf("rename битый json: %d", rr.Code)
	}
	if rr := do(s, "POST", "/api/event/rename", `{"event":"nope","new":"x"}`); rr.Code != http.StatusBadRequest {
		t.Fatalf("rename нет события: %d", rr.Code)
	}
	if rr := do(s, "POST", "/api/event/rename", `{"event":"old","new":"new"}`); rr.Code != 200 {
		t.Fatalf("rename: %d %s", rr.Code, rr.Body.String())
	}
	evs := st.Events()
	if len(evs) != 1 || evs[0] != "new" {
		t.Fatalf("события после rename: %v", evs)
	}

	if rr := do(s, "POST", "/api/event/hide", "{oops"); rr.Code != http.StatusBadRequest {
		t.Fatalf("hide битый json: %d", rr.Code)
	}
	if rr := do(s, "POST", "/api/event/hide", `{"event":"nope","hidden":true}`); rr.Code != http.StatusBadRequest {
		t.Fatalf("hide нет события: %d", rr.Code)
	}
	if rr := do(s, "POST", "/api/event/hide", `{"event":"new","hidden":true}`); rr.Code != 200 {
		t.Fatalf("hide: %d %s", rr.Code, rr.Body.String())
	}
	rr := do(s, "GET", "/api/events", "")
	if !strings.Contains(rr.Body.String(), `"hidden":true`) {
		t.Fatalf("hidden не виден: %s", rr.Body.String())
	}
}

func TestImportCSVAndXLSX(t *testing.T) {
	s, _, dir := newTestServer(t)

	header := "Номер заказа,Дата создания,Статус,Email,Тип билета,Номер билета,EAN,Имя,Фамилия,Телефон"
	row := "1001,2026-01-01,paid,ivan@example.com,Standard,117726839:97059462,4600000000001,Иван,Иванов,+79001112233"
	csv := header + "\n" + row + "\n"

	if rr := do(s, "POST", "/api/import", csv); rr.Code != http.StatusBadRequest {
		t.Fatalf("без события: %d", rr.Code)
	}
	if rr := do(s, "POST", "/api/import?event=imp", ""); rr.Code != http.StatusBadRequest {
		t.Fatalf("пустой файл: %d", rr.Code)
	}
	// тело больше лимита в 20 МБ отклоняется без разбора
	if rr := do(s, "POST", "/api/import?event=imp", strings.Repeat("a", 20<<20+1)); rr.Code != http.StatusBadRequest {
		t.Fatalf("слишком большое тело: %d", rr.Code)
	}

	rr := do(s, "POST", "/api/import?event=imp", csv)
	if rr.Code != 200 {
		t.Fatalf("csv: %d %s", rr.Code, rr.Body.String())
	}
	if m := decodeObj(t, rr); m["total"].(float64) != 1 || m["added"].(float64) != 1 {
		t.Fatalf("csv итог: %v", m)
	}
	rr = do(s, "POST", "/api/import?event=imp", csv)
	if m := decodeObj(t, rr); m["added"].(float64) != 0 {
		t.Fatalf("повторный импорт должен обновлять, а не добавлять: %v", m)
	}
	imports, err := os.ReadDir(filepath.Join(dir, "imports"))
	if err != nil || len(imports) == 0 || !strings.HasSuffix(imports[0].Name(), ".csv") {
		t.Fatalf("исходник не сохранён: %v %v", imports, err)
	}

	rows := [][]string{
		strings.Split(header, ","),
		strings.Split(row, ","),
	}
	x, err := xlsxBytes(rows)
	if err != nil {
		t.Fatal(err)
	}
	rr = do(s, "POST", "/api/import?event=imp-xlsx", string(x))
	if rr.Code != 200 {
		t.Fatalf("xlsx: %d %s", rr.Code, rr.Body.String())
	}
	if m := decodeObj(t, rr); m["total"].(float64) != 1 {
		t.Fatalf("xlsx итог: %v", m)
	}
	imports, _ = os.ReadDir(filepath.Join(dir, "imports"))
	hasXLSX := false
	for _, f := range imports {
		if strings.HasSuffix(f.Name(), ".xlsx") {
			hasXLSX = true
		}
	}
	if !hasXLSX {
		t.Fatalf("xlsx-исходник не сохранён: %v", imports)
	}
}

func TestRefreshFromTimepad(t *testing.T) {
	s, _, _ := newTestServer(t)
	old := timepad.BaseURL
	t.Cleanup(func() { timepad.BaseURL = old })

	orders := `{"items":[{"id":1,"status":{"name":"paid","title":"Оплачен"},"tickets":[{"id":11,"number":"117726839:97059462","ticket_type":{"name":"Standard"},"codes":{},"answers":[{"question":"Имя","answer":"Пётр"},{"question":"Телефон","answer":"+79001112233"}]}]}]}`
	okSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":"unauthorized"}`))
			return
		}
		w.Write([]byte(orders))
	}))
	defer okSrv.Close()
	timepad.BaseURL = okSrv.URL

	if rr := do(s, "POST", "/api/refresh", "{oops"); rr.Code != http.StatusBadRequest {
		t.Fatalf("refresh битый json: %d", rr.Code)
	}
	if rr := do(s, "POST", "/api/refresh", `{"event":"rf"}`); rr.Code != http.StatusBadRequest {
		t.Fatalf("без токена: %d", rr.Code)
	}
	if rr := do(s, "POST", "/api/refresh", `{"event":"rf","timepad_id":0,"token":"tok"}`); rr.Code != http.StatusBadRequest {
		t.Fatalf("без id: %d", rr.Code)
	}
	if rr := do(s, "POST", "/api/refresh", `{"event":"rf","timepad_id":5,"token":"wrong"}`); rr.Code != http.StatusBadGateway {
		t.Fatalf("401 от api: code=%d body=%s", rr.Code, rr.Body.String())
	}

	rr := do(s, "POST", "/api/refresh", `{"event":"rf","timepad_id":5,"token":"tok"}`)
	if rr.Code != 200 {
		t.Fatalf("refresh: %d %s", rr.Code, rr.Body.String())
	}
	if m := decodeObj(t, rr); m["total"].(float64) != 1 || m["added"].(float64) != 1 {
		t.Fatalf("refresh итог: %v", m)
	}

	// токен берётся из переменной окружения, если не передан в теле
	t.Setenv("TIMEPAD_TOKEN", "tok")
	if rr := do(s, "POST", "/api/refresh", `{"event":"rf2","timepad_id":5}`); rr.Code != 200 {
		t.Fatalf("refresh из TIMEPAD_TOKEN: %d %s", rr.Code, rr.Body.String())
	}
	t.Setenv("TIMEPAD_TOKEN", "")
	if rr := do(s, "POST", "/api/refresh", `{"event":"rf3","timepad_id":5}`); rr.Code != http.StatusBadRequest {
		t.Fatalf("пустой env-токен: %d", rr.Code)
	}
}

func TestPagesAndAssets(t *testing.T) {
	s, _, _ := newTestServer(t)
	local := func(path string) *httptest.ResponseRecorder {
		return doFrom(s, "GET", path, "", "127.0.0.1:4444", "")
	}

	for _, p := range []string{"/", "/status", "/admin", "/reset"} {
		rr := local(p)
		if p == "/admin" {
			rr = do(s, "GET", p, "")
		}
		if rr.Code != 200 || !strings.Contains(rr.Header().Get("Content-Type"), "text/html") {
			t.Errorf("%s: code=%d ct=%s", p, rr.Code, rr.Header().Get("Content-Type"))
		}
	}
	for _, p := range []string{"/jsQR.js", "/zxing.js", "/sw.js"} {
		rr := do(s, "GET", p, "")
		if rr.Code != 200 || !strings.Contains(rr.Header().Get("Content-Type"), "javascript") {
			t.Errorf("%s: code=%d ct=%s", p, rr.Code, rr.Header().Get("Content-Type"))
		}
	}
	if rr := do(s, "GET", "/definitely-missing", ""); rr.Code != http.StatusNotFound {
		t.Errorf("нет страницы: %d", rr.Code)
	}

	// status-страница с LAN закрыта
	if rr := do(s, "GET", "/api/status", ""); rr.Code != http.StatusForbidden {
		t.Errorf("status с LAN: %d", rr.Code)
	}
	// loopback с заголовком крайнего — закрыт
	req := httptest.NewRequest("GET", "/api/status", nil)
	req.RemoteAddr = "127.0.0.1:4444"
	req.Header.Set("Cf-Connecting-Ip", "1.2.3.4")
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("tunnel Cf-Connecting-Ip: %d", rr.Code)
	}
	// RemoteAddr без порта — тоже закрыт
	if rr := doFrom(s, "GET", "/api/status", "", "noport", ""); rr.Code != http.StatusForbidden {
		t.Errorf("без порта: %d", rr.Code)
	}
}

func TestStatusAPIBackupTimes(t *testing.T) {
	s, _, _ := newTestServer(t)
	statusGet := func() map[string]any {
		t.Helper()
		rr := doFrom(s, "GET", "/api/status", "", "127.0.0.1:4444", "")
		if rr.Code != 200 {
			t.Fatalf("status: %d", rr.Code)
		}
		return decodeObj(t, rr)
	}

	m := statusGet()
	if m["backup"].(bool) != false || m["backup_auto"] != "" || m["backup_manual"] != "" {
		t.Fatalf("без хуков: %v", m)
	}

	auto := time.Date(2026, 10, 1, 12, 0, 0, 0, time.Local)
	manual := time.Date(2026, 10, 1, 13, 30, 0, 0, time.Local)
	s.BackupTimes = func() (time.Time, time.Time) { return auto, manual }
	m = statusGet()
	if m["backup_auto"] != "01.10.2026 12:00" || m["backup_manual"] != "01.10.2026 13:30" {
		t.Fatalf("время бэкапов: %v", m)
	}
	s.BackupTimes = func() (time.Time, time.Time) { return time.Time{}, time.Time{} }
	m = statusGet()
	if m["backup_auto"] != "" || m["backup_manual"] != "" {
		t.Fatalf("ноль времени — пустые строки: %v", m)
	}
}

func TestBackupNowError(t *testing.T) {
	s, _, _ := newTestServer(t)
	s.TriggerBackup = func() (string, error) { return "", os.ErrPermission }
	rr := doFrom(s, "POST", "/api/admin/backup", "", "127.0.0.1:4444", "")
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("ошибка бэкапа: %d %s", rr.Code, rr.Body.String())
	}
}

func TestStopRestartWithoutHandlers(t *testing.T) {
	s, _, _ := newTestServer(t)
	for _, p := range []string{"/api/admin/stop", "/api/admin/restart"} {
		if rr := doFrom(s, "POST", p, "", "127.0.0.1:4444", ""); rr.Code != 200 {
			t.Errorf("%s без хука: %d", p, rr.Code)
		}
	}
}

func TestAdminReportErrors(t *testing.T) {
	s, _, _ := newTestServer(t)
	if rr := do(s, "GET", "/api/admin/report", ""); rr.Code != http.StatusBadRequest {
		t.Fatalf("без события: %d", rr.Code)
	}
	if rr := do(s, "GET", "/api/admin/report?event=nope", ""); rr.Code != http.StatusNotFound {
		t.Fatalf("нет события: %d", rr.Code)
	}
	if rr := do(s, "POST", "/api/admin/report", ""); rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST: %d", rr.Code)
	}
}

func TestClientEventsTrim(t *testing.T) {
	s, _, _ := newTestServer(t)
	for i := 0; i < 15; i++ {
		s.pushEventLocked(clientEvent{Kind: "join", At: "10:00:00", at: time.Now()})
	}
	if len(s.events) != 12 {
		t.Fatalf("история должна обрезаться до 12, got %d", len(s.events))
	}
}

func TestTouchKeepsDevice(t *testing.T) {
	s, _, _ := newTestServer(t)
	r1 := httptest.NewRequest("GET", "/api/pin?device=dev-1", nil)
	r1.RemoteAddr = "10.1.1.1:1"
	s.touch(r1)
	r2 := httptest.NewRequest("GET", "/api/pin", nil)
	r2.RemoteAddr = "10.1.1.1:1"
	s.touch(r2)
	s.seenMu.Lock()
	dev := s.seen["10.1.1.1"].dev
	s.seenMu.Unlock()
	if dev != "dev-1" {
		t.Fatalf("пустой опрос затёр код устройства: %q", dev)
	}
}
