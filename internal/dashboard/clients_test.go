package dashboard

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/idle-sync/whatsapp-connect-mcp/internal/clients"
	"github.com/idle-sync/whatsapp-connect-mcp/internal/config"
	"github.com/idle-sync/whatsapp-connect-mcp/internal/store"
)

// newClientsHandler builds a handler whose client detection is rooted at a
// temp home, so a test never reads or writes the developer's real configs.
func newClientsHandler(t *testing.T, mod func(*Deps)) (*Handler, *http.Cookie, string) {
	t.Helper()
	home := t.TempDir()
	h, cookie := newTestHandlerWith(t, func(d *Deps) {
		d.Home = home
		d.BinaryPath = filepath.Join(home, "whatsapp-connect-mcp")
		if mod != nil {
			mod(d)
		}
	})
	return h, cookie, home
}

func getClients(t *testing.T, h *Handler, cookie *http.Cookie) []clientRow {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/api/clients", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/clients = %d, want 200", w.Code)
	}
	var rows []clientRow
	if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode clients: %v", err)
	}
	return rows
}

func findClient(t *testing.T, rows []clientRow, name string) clientRow {
	t.Helper()
	for _, r := range rows {
		if r.Name == name {
			return r
		}
	}
	t.Fatalf("client %q not in %v", name, rows)
	return clientRow{}
}

// A fresh home has every known client detected, none of them connected.
func TestClientsListDetectsKnownClients(t *testing.T) {
	h, cookie, _ := newClientsHandler(t, nil)
	rows := getClients(t, h, cookie)
	if len(rows) == 0 {
		t.Fatal("no clients detected")
	}
	for _, r := range rows {
		if r.Connected {
			t.Errorf("%s reported connected in an empty home", r.Name)
		}
		if r.ConfigPath == "" {
			t.Errorf("%s has no config path", r.Name)
		}
	}
	// Claude Code is in the known table on every platform.
	findClient(t, rows, "Claude Code")
}

// Adding writes an entry the client can use, and the list reflects it.
func TestClientAddThenRemove(t *testing.T) {
	h, cookie, home := newClientsHandler(t, func(d *Deps) {
		d.HTTPURL = "http://127.0.0.1:2178" // Token stays testToken: login runs first
	})

	if w := mutate(t, h, cookie, http.MethodPost, "/api/clients", `{"name":"Claude Code"}`); w.Code != http.StatusOK {
		t.Fatalf("add = %d (%s), want 200", w.Code, w.Body.String())
	}

	row := findClient(t, getClients(t, h, cookie), "Claude Code")
	if !row.Connected {
		t.Fatal("Claude Code not connected after add")
	}
	if row.Transport != "http" || row.Target != "http://127.0.0.1:2178" {
		t.Errorf("transport/target = %q/%q, want http/http://127.0.0.1:2178", row.Transport, row.Target)
	}
	if row.Broken {
		t.Error("freshly added entry reported broken")
	}
	if _, err := os.Stat(filepath.Join(home, ".claude.json")); err != nil {
		t.Fatalf("config not written: %v", err)
	}

	if w := mutate(t, h, cookie, http.MethodDelete, "/api/clients/Claude%20Code", ""); w.Code != http.StatusOK {
		t.Fatalf("remove = %d (%s), want 200", w.Code, w.Body.String())
	}
	if findClient(t, getClients(t, h, cookie), "Claude Code").Connected {
		t.Fatal("still connected after remove")
	}
}

// A stdio entry naming a binary that is not this one is what the doctor
// calls a broken client config; the dashboard must agree.
func TestClientBrokenEntryReported(t *testing.T) {
	h, cookie, home := newClientsHandler(t, nil)
	cfg := filepath.Join(home, ".claude.json")
	if err := clients.Inject(cfg, filepath.Join(home, "some-other-binary")); err != nil {
		t.Fatalf("inject: %v", err)
	}
	row := findClient(t, getClients(t, h, cookie), "Claude Code")
	if !row.Connected {
		t.Fatal("entry not detected")
	}
	if !row.Broken {
		t.Error("entry naming another binary not reported broken")
	}
}

// The caller names a client, never a path: an unknown name is refused
// rather than resolved into a write somewhere unexpected.
func TestClientAddRejectsUnknownName(t *testing.T) {
	h, cookie, home := newClientsHandler(t, nil)
	escape := filepath.Join(home, "..", "escaped.json")

	for _, body := range []string{`{"name":"Nope"}`, `{"name":"` + filepath.ToSlash(escape) + `"}`, `{"name":""}`, `not json`} {
		w := mutate(t, h, cookie, http.MethodPost, "/api/clients", body)
		if w.Code == http.StatusOK {
			t.Errorf("body %q accepted, want refusal", body)
		}
	}
	if _, err := os.Stat(escape); !os.IsNotExist(err) {
		t.Fatalf("a file was written outside the known-client table: %v", err)
	}
}

// Mutating routes require the dashboard header, like every other one.
func TestClientMutationsNeedDashboardHeader(t *testing.T) {
	h, cookie, _ := newClientsHandler(t, nil)
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/clients", `{"name":"Claude Code"}`},
		{http.MethodDelete, "/api/clients/Claude%20Code", ""},
	} {
		r := httptest.NewRequest(tc.method, tc.path, http.NoBody)
		r.AddCookie(cookie) // no X-Requested-With
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusForbidden {
			t.Errorf("%s %s = %d, want 403", tc.method, tc.path, w.Code)
		}
	}
}

// Without an HTTP URL the handler falls back to a stdio entry naming this
// binary — the shape `setup` writes when there is no shared server.
func TestClientAddStdioFallback(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("path shape differs on windows")
	}
	h, cookie, home := newClientsHandler(t, nil) // HTTPURL left empty
	bin := filepath.Join(home, "whatsapp-connect-mcp")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o600); err != nil { // #nosec G306 -- a stand-in file, only stat-ed
		t.Fatalf("write binary: %v", err)
	}
	if w := mutate(t, h, cookie, http.MethodPost, "/api/clients", `{"name":"Cursor"}`); w.Code != http.StatusOK {
		t.Fatalf("add = %d (%s), want 200", w.Code, w.Body.String())
	}
	row := findClient(t, getClients(t, h, cookie), "Cursor")
	if row.Transport != "stdio" || row.Target != bin {
		t.Errorf("transport/target = %q/%q, want stdio/%s", row.Transport, row.Target, bin)
	}
	if row.Broken {
		t.Error("stdio entry naming this binary reported broken")
	}
}

// The scope round-trips through the dashboard, and adding a chat is what
// turns the limit on: someone naming a chat to allow is asking for a
// restriction, not filing it for later.
func TestScopeRoundTrip(t *testing.T) {
	h, cookie, _ := newClientsHandler(t, nil)

	get := func() (string, []scopeRow) {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "/api/scope", nil)
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("GET /api/scope = %d", w.Code)
		}
		var body struct {
			Mode  string     `json:"mode"`
			Chats []scopeRow `json:"chats"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode scope: %v", err)
		}
		return body.Mode, body.Chats
	}

	if mode, chats := get(); mode != "all" || len(chats) != 0 {
		t.Fatalf("fresh scope = %q/%v, want all/none", mode, chats)
	}

	if w := mutate(t, h, cookie, http.MethodPost, "/api/scope", `{"jid":"a@s.whatsapp.net"}`); w.Code != http.StatusOK {
		t.Fatalf("add = %d (%s)", w.Code, w.Body.String())
	}
	mode, chats := get()
	if mode != "allowlist" || len(chats) != 1 || chats[0].JID != "a@s.whatsapp.net" {
		t.Fatalf("after add = %q/%+v", mode, chats)
	}

	// Removing the last chat leaves the limit ON and empty — nothing
	// readable — rather than silently reopening every chat.
	if w := mutate(t, h, cookie, http.MethodDelete, "/api/scope/a%40s.whatsapp.net", ""); w.Code != http.StatusOK {
		t.Fatalf("remove = %d (%s)", w.Code, w.Body.String())
	}
	if mode, chats := get(); mode != "allowlist" || len(chats) != 0 {
		t.Fatalf("after remove = %q/%+v, want allowlist with no chats", mode, chats)
	}

	// Reopening is its own explicit act.
	if w := mutate(t, h, cookie, http.MethodPost, "/api/scope", `{"mode":"all"}`); w.Code != http.StatusOK {
		t.Fatalf("mode=all = %d (%s)", w.Code, w.Body.String())
	}
	if mode, _ := get(); mode != "all" {
		t.Fatalf("after mode=all = %q", mode)
	}
}

func TestScopeRejectsBadInput(t *testing.T) {
	h, cookie, _ := newClientsHandler(t, nil)
	for _, body := range []string{`{}`, `{"mode":"banana"}`, `{"jid":"   "}`, `nope`} {
		if w := mutate(t, h, cookie, http.MethodPost, "/api/scope", body); w.Code == http.StatusOK {
			t.Errorf("body %q accepted, want refusal", body)
		}
	}
}

// Writing the scope is a human-only act: an agent that could widen its own
// allowlist would make the setting decorative.
func TestScopeMutationsNeedDashboardHeader(t *testing.T) {
	h, cookie, _ := newClientsHandler(t, nil)
	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/api/scope"},
		{http.MethodDelete, "/api/scope/a%40s.whatsapp.net"},
	} {
		r := httptest.NewRequest(tc.method, tc.path, http.NoBody)
		r.AddCookie(cookie) // no X-Requested-With
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusForbidden {
			t.Errorf("%s %s = %d, want 403", tc.method, tc.path, w.Code)
		}
	}
}

// Trust and scope take a name or a number, not just a raw JID: nobody
// knows their friend as 15551234567@s.whatsapp.net, and a mistyped JID is
// accepted and then silently never matches.
func TestScopeAcceptsNameAndNumber(t *testing.T) {
	h, cookie, _ := newClientsHandler(t, func(d *Deps) {
		d.Store = &fakeStore{contacts: []store.ContactRow{
			{JID: "1@s.whatsapp.net", Name: "Ashmi", Phone: "15551234567"},
			{JID: "2@s.whatsapp.net", Name: "Bhaskar", Phone: "15559876543"},
		}}
	})

	if w := mutate(t, h, cookie, http.MethodPost, "/api/scope", `{"jid":"Ashmi"}`); w.Code != http.StatusOK {
		t.Fatalf("add by name = %d (%s)", w.Code, w.Body.String())
	}
	if w := mutate(t, h, cookie, http.MethodPost, "/api/scope", `{"jid":"+1 555 000 1111"}`); w.Code != http.StatusOK {
		t.Fatalf("add by number = %d (%s)", w.Code, w.Body.String())
	}

	cfg, err := config.Load(h.deps.DataDir)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	want := map[string]bool{"1@s.whatsapp.net": true, "15550001111@s.whatsapp.net": true}
	if len(cfg.ReadableChats) != 2 {
		t.Fatalf("readable = %v, want 2 entries", cfg.ReadableChats)
	}
	for _, jid := range cfg.ReadableChats {
		if !want[jid] {
			t.Errorf("unexpected entry %q", jid)
		}
	}
}

// An ambiguous name is answered with the candidates so the page can ask,
// and nothing is written until one is picked.
func TestScopeAmbiguousNameOffersCandidates(t *testing.T) {
	h, cookie, _ := newClientsHandler(t, func(d *Deps) {
		d.Store = &fakeStore{contacts: []store.ContactRow{
			{JID: "1@s.whatsapp.net", Name: "Ashmi B", Phone: "15551234567"},
			{JID: "2@s.whatsapp.net", Name: "Ashmi G", Phone: "15559876543"},
		}}
	})

	w := mutate(t, h, cookie, http.MethodPost, "/api/scope", `{"jid":"Ashmi"}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("ambiguous add = %d (%s), want 409", w.Code, w.Body.String())
	}
	var body struct {
		Candidates []struct {
			JID   string `json:"jid"`
			Name  string `json:"name"`
			Phone string `json:"phone"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Candidates) != 2 {
		t.Fatalf("candidates = %+v, want 2", body.Candidates)
	}
	if body.Candidates[0].Phone == "" {
		t.Error("candidate carries no phone number to disambiguate by")
	}

	cfg, _ := config.Load(h.deps.DataDir)
	if len(cfg.ReadableChats) != 0 || cfg.ScopeActive() {
		t.Error("an unresolved name changed the config")
	}

	// Picking one commits it.
	if w := mutate(t, h, cookie, http.MethodPost, "/api/scope",
		`{"jid":"`+body.Candidates[1].JID+`"}`); w.Code != http.StatusOK {
		t.Fatalf("commit chosen = %d (%s)", w.Code, w.Body.String())
	}
	cfg, _ = config.Load(h.deps.DataDir)
	if len(cfg.ReadableChats) != 1 || cfg.ReadableChats[0] != "2@s.whatsapp.net" {
		t.Errorf("readable = %v", cfg.ReadableChats)
	}
}

// A name nothing matches is refused with advice, not stored verbatim.
func TestScopeUnknownNameRefused(t *testing.T) {
	h, cookie, _ := newClientsHandler(t, nil)
	w := mutate(t, h, cookie, http.MethodPost, "/api/scope", `{"jid":"Nobody At All"}`)
	if w.Code != http.StatusNotFound {
		t.Fatalf("unknown name = %d (%s), want 404", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "phone number") {
		t.Errorf("refusal gives no way forward: %s", w.Body.String())
	}
}
