package dashboard

import (
	"context"
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

// TestSuffixRoutesRejectWrongMethod guards against the suffix-routed
// mutating endpoints executing on any method that merely carries the CSRF
// header — /api/trust/{jid}, /api/schedules/{id}, /api/backup, and
// /api/pair/start must each require their one intended method.
func TestSuffixRoutesRejectWrongMethod(t *testing.T) {
	fs := &fakeStore{}
	h, cookie := newTestHandler(t, fs, nil)

	if w := mutate(t, h, cookie, http.MethodGet, "/api/backup", ""); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET /api/backup = %d, want 405", w.Code)
	}
	if fs.backupPath != "" {
		t.Fatalf("GET /api/backup executed a backup: path=%q", fs.backupPath)
	}

	if w := mutate(t, h, cookie, http.MethodGet, "/api/trust/15551234567@s.whatsapp.net", ""); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET /api/trust/{jid} = %d, want 405", w.Code)
	}

	if w := mutate(t, h, cookie, http.MethodGet, "/api/schedules/anyid", ""); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET /api/schedules/{id} = %d, want 405", w.Code)
	}

	fbp := &pairFakeBridge{fakeBridge: fakeBridge{unpaired: true}, release: make(chan struct{})}
	defer close(fbp.release)
	hp, cookieP := newTestHandlerWithBridge(t, fbp)
	if w := mutate(t, hp, cookieP, http.MethodGet, "/api/pair/start", ""); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET /api/pair/start = %d, want 405", w.Code)
	}
	fbp.mu.Lock()
	starts := fbp.starts
	fbp.mu.Unlock()
	if starts != 0 {
		t.Fatalf("GET /api/pair/start started pairing: starts=%d", starts)
	}
}

type nullDeliverer struct{ n int }

func (d *nullDeliverer) Deliver(context.Context, gate.Delivery) (string, error) {
	d.n++
	return "MSG1", nil
}
func (d *nullDeliverer) Validate(gate.Delivery) error { return nil }

func TestDraftApproveEndpoint(t *testing.T) {
	del := &nullDeliverer{}
	g := gate.New(del, func(string) bool { return false }, 3, 5, time.Now)
	res, err := g.Submit(context.Background(), gate.Delivery{Kind: "text", To: "x@s.whatsapp.net", Text: "hi"}, "", nil)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	h, cookie := newTestHandlerWith(t, func(d *Deps) { d.Gate = g })

	w := authedGet(t, h, cookie, "/api/drafts")
	var rows []map[string]any
	_ = json.NewDecoder(w.Body).Decode(&rows)
	if len(rows) != 1 || rows[0]["token"] != res.DraftToken {
		t.Fatalf("drafts = %v", rows)
	}

	if w := mutate(t, h, cookie, http.MethodPost, "/api/drafts/"+res.DraftToken+"/approve", ""); w.Code != http.StatusOK {
		t.Fatalf("approve = %d: %s", w.Code, w.Body.String())
	}
	if del.n != 1 {
		t.Fatalf("deliveries = %d, want 1", del.n)
	}
}

func TestDraftDiscardEndpoint(t *testing.T) {
	del := &nullDeliverer{}
	g := gate.New(del, func(string) bool { return false }, 3, 5, time.Now)
	res, err := g.Submit(context.Background(), gate.Delivery{Kind: "text", To: "x@s.whatsapp.net", Text: "hi"}, "", nil)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	h, cookie := newTestHandlerWith(t, func(d *Deps) { d.Gate = g })

	if w := mutate(t, h, cookie, http.MethodDelete, "/api/drafts/"+res.DraftToken, ""); w.Code != http.StatusOK {
		t.Fatalf("discard = %d: %s", w.Code, w.Body.String())
	}
	if w := mutate(t, h, cookie, http.MethodPost, "/api/drafts/"+res.DraftToken+"/approve", ""); w.Code != http.StatusConflict {
		t.Fatalf("approve after discard = %d, want 409", w.Code)
	}
	if del.n != 0 {
		t.Fatalf("deliveries = %d, want 0", del.n)
	}
}

// TestDraftActionRejectsWrongMethod extends the suffix-route method-check
// coverage to the drafts endpoint: only POST .../approve and DELETE are
// wired, everything else must 405 without touching the gate.
func TestDraftActionRejectsWrongMethod(t *testing.T) {
	del := &nullDeliverer{}
	g := gate.New(del, func(string) bool { return false }, 3, 5, time.Now)
	res, err := g.Submit(context.Background(), gate.Delivery{Kind: "text", To: "x@s.whatsapp.net", Text: "hi"}, "", nil)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	h, cookie := newTestHandlerWith(t, func(d *Deps) { d.Gate = g })

	if w := mutate(t, h, cookie, http.MethodGet, "/api/drafts/"+res.DraftToken+"/approve", ""); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET .../approve = %d, want 405", w.Code)
	}
	if w := mutate(t, h, cookie, http.MethodPut, "/api/drafts/"+res.DraftToken, ""); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("PUT /api/drafts/{token} = %d, want 405", w.Code)
	}
	if del.n != 0 {
		t.Fatalf("deliveries = %d, want 0", del.n)
	}
}
