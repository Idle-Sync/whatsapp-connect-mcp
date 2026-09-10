package dashboard

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/idle-sync/whatsapp-connect-mcp/internal/clients"
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
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o700); err != nil {
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
