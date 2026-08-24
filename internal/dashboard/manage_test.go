package dashboard

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/idle-sync/whatsapp-connect-mcp/internal/config"
	"github.com/idle-sync/whatsapp-connect-mcp/internal/gate"
	"github.com/idle-sync/whatsapp-connect-mcp/internal/schedule"
)

func mutate(t *testing.T, h *Handler, cookie *http.Cookie, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.AddCookie(cookie)
	r.Header.Set("X-Requested-With", "dashboard")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// newTestHandlerSched is a one-line delegation to newTestHandlerWith for
// tests that need a real schedule.Store.
func newTestHandlerSched(t *testing.T, sched *schedule.Store) (*Handler, *http.Cookie) {
	t.Helper()
	return newTestHandlerWith(t, func(d *Deps) { d.Sched = sched })
}

func TestTrustRoundTrip(t *testing.T) {
	h, cookie := newTestHandler(t, nil, nil) // DataDir is a fresh t.TempDir()
	dataDir := h.deps.DataDir

	if w := mutate(t, h, cookie, http.MethodPost, "/api/trust", `{"jid":"15551234567@s.whatsapp.net"}`); w.Code != http.StatusOK {
		t.Fatalf("trust add = %d: %s", w.Code, w.Body.String())
	}
	cfg, err := config.Load(dataDir)
	if err != nil || !cfg.IsTrusted("15551234567@s.whatsapp.net") {
		t.Fatalf("config after add: %+v err=%v", cfg, err)
	}

	w := authedGet(t, h, cookie, "/api/trust")
	var jids []string
	_ = json.NewDecoder(w.Body).Decode(&jids)
	if len(jids) != 1 || jids[0] != "15551234567@s.whatsapp.net" {
		t.Fatalf("trust list = %v", jids)
	}

	if w := mutate(t, h, cookie, http.MethodDelete, "/api/trust/15551234567@s.whatsapp.net", ""); w.Code != http.StatusOK {
		t.Fatalf("trust remove = %d", w.Code)
	}
	cfg, _ = config.Load(dataDir)
	if cfg.IsTrusted("15551234567@s.whatsapp.net") {
		t.Fatal("still trusted after remove")
	}
}

func TestTrustAddRejectsEmpty(t *testing.T) {
	h, cookie := newTestHandler(t, nil, nil)
	if w := mutate(t, h, cookie, http.MethodPost, "/api/trust", `{"jid":"  "}`); w.Code != http.StatusBadRequest {
		t.Fatalf("empty jid = %d, want 400", w.Code)
	}
}

func TestBackupEndpointWritesSnapshot(t *testing.T) {
	fs := &fakeStore{}
	h, cookie := newTestHandler(t, fs, nil)
	w := mutate(t, h, cookie, http.MethodPost, "/api/backup", "")
	if w.Code != http.StatusOK {
		t.Fatalf("backup = %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Path string `json:"path"`
	}
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if fs.backupPath != resp.Path || !strings.Contains(resp.Path, string(os.PathSeparator)+"backups"+string(os.PathSeparator)) {
		t.Fatalf("backup path store=%q resp=%q", fs.backupPath, resp.Path)
	}
	if _, err := os.Stat(filepath.Dir(resp.Path)); err != nil {
		t.Fatalf("backups dir not created: %v", err)
	}
}

func TestSchedulesListAndCancel(t *testing.T) {
	sched, _, err := schedule.Load(t.TempDir(), time.Now())
	if err != nil {
		t.Fatalf("schedule.Load: %v", err)
	}
	e, err := sched.Add(gate.Delivery{Kind: "text", To: "x@s.whatsapp.net", Text: "hi", FireAt: time.Now().Add(time.Hour).Unix()}, "text to x")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	h, cookie := newTestHandlerSched(t, sched)

	w := authedGet(t, h, cookie, "/api/schedules")
	var rows []map[string]any
	_ = json.NewDecoder(w.Body).Decode(&rows)
	if len(rows) != 1 || rows[0]["id"] != e.ID {
		t.Fatalf("schedules = %v", rows)
	}

	if w := mutate(t, h, cookie, http.MethodDelete, "/api/schedules/"+e.ID, ""); w.Code != http.StatusOK {
		t.Fatalf("cancel = %d", w.Code)
	}
	if w := mutate(t, h, cookie, http.MethodDelete, "/api/schedules/"+e.ID, ""); w.Code != http.StatusNotFound {
		t.Fatalf("second cancel = %d, want 404", w.Code)
	}
}
