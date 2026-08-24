// Package dashboard serves the local web UI and its JSON API on the same
// listener as the MCP transport: /ui/ and /api/ belong to it, every other
// path stays MCP's. Auth is a one-time token login exchanged for an
// HttpOnly SameSite=Strict session cookie (see auth.go); the loopback
// Host check wraps it one level up in cmd/serve. All WhatsApp-originated
// strings it returns are data for the authenticated local human; the UI
// renders them exclusively via textContent (enforced by test).
package dashboard

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/idle-sync/whatsapp-connect-mcp/internal/bridge"
	"github.com/idle-sync/whatsapp-connect-mcp/internal/doctor"
	"github.com/idle-sync/whatsapp-connect-mcp/internal/gate"
	"github.com/idle-sync/whatsapp-connect-mcp/internal/schedule"
	"github.com/idle-sync/whatsapp-connect-mcp/internal/store"
)

//go:embed ui
var uiFS embed.FS

// Store is the read/backup surface the dashboard needs from *store.Store.
// Extended by later tasks; satisfied by *store.Store throughout.
type Store interface {
	Counts() (store.Counts, error)
	Chats(query string, includeArchived bool, limit int) ([]store.ChatRow, error)
	TailRowID(chatJID string, includeOwn bool, n int) (int64, error)
	MessagesAfterRowID(chatJID string, afterRowID int64, includeOwn bool, limit int) ([]store.MessageRow, int64, error)
	MessageMediaRef(chatJID, id string) (ref []byte, filename, kind string, err error)
	SearchMessages(query, chatJID string, limit int) ([]store.MessageRow, error)
	BackupTo(path string) error
}

// Bridge is the connection surface the dashboard needs from
// *bridge.Bridge. Extended by later tasks.
type Bridge interface {
	Status() bridge.Status
	NeedsPairing() bool
	PairQR(ctx context.Context, show func(code string)) error
	Logout(ctx context.Context) error
	WaitForCatchUp(ctx context.Context)
	DownloadMedia(ctx context.Context, ref []byte, destDir, filename string) (string, error)
}

// Deps wires the dashboard into serve's already-constructed pieces. Ctx is
// process-scoped (the signal context) and governs pairing goroutines.
type Deps struct {
	Ctx     context.Context
	Store   Store
	Bridge  Bridge
	Gate    *gate.Gate
	Sched   *schedule.Store
	DataDir string
	Token   string
	Version string
	Doctor  func(ctx context.Context) []doctor.Finding
}

// Handler is the dashboard's http.Handler. Construct with New.
type Handler struct {
	deps    Deps
	session string // per-process session cookie value; browser logs in again after a restart
	mux     *http.ServeMux
	pair    pairState
}

// New builds the dashboard handler. It panics only on entropy failure at
// construction (same stance as a failed listener: the process is unusable).
func New(deps Deps) *Handler {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		panic("dashboard: session entropy unavailable: " + err.Error())
	}
	h := &Handler{deps: deps, session: hex.EncodeToString(buf), mux: http.NewServeMux()}

	h.mux.HandleFunc("/ui/login", h.handleLogin)
	h.mux.Handle("/ui/", h.authed(h.serveStatic))
	// Unknown /api/ paths get a JSON 404 rather than net/http's plain-text
	// default; specific routes below win by pattern length.
	h.mux.HandleFunc("/api/", h.authed(func(w http.ResponseWriter, _ *http.Request) {
		h.writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such endpoint"})
	}))
	h.mux.HandleFunc("/api/status", h.authed(h.handleStatus))
	h.mux.HandleFunc("/api/doctor", h.authed(h.handleDoctor))
	h.mux.HandleFunc("/api/pair/start", h.authed(h.mutating(h.handlePairStart)))
	h.mux.HandleFunc("/api/pair/logout", h.authed(h.mutating(h.handlePairLogout)))
	h.mux.HandleFunc("/api/pair", h.authed(h.handlePairInfo))
	h.mux.HandleFunc("/api/pair/qr.png", h.authed(h.handlePairQR))
	h.mux.HandleFunc("/api/chats", h.authed(h.handleChats))
	h.mux.HandleFunc("/api/messages", h.authed(h.handleMessages))
	h.mux.HandleFunc("/api/search", h.authed(h.handleSearch))
	h.mux.HandleFunc("/api/media", h.authed(h.handleMedia))
	h.mux.HandleFunc("/api/trust", h.authed(h.handleTrust))
	h.mux.HandleFunc("/api/trust/", h.authed(h.mutating(h.handleTrustRemove)))
	h.mux.HandleFunc("/api/schedules", h.authed(h.handleSchedules))
	h.mux.HandleFunc("/api/schedules/", h.authed(h.mutating(h.handleScheduleCancel)))
	h.mux.HandleFunc("/api/backup", h.authed(h.mutating(h.handleBackup)))
	h.mux.HandleFunc("/api/drafts", h.authed(h.handleDrafts))
	h.mux.HandleFunc("/api/drafts/", h.authed(h.mutating(h.handleDraftAction)))
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) { h.mux.ServeHTTP(w, r) }

// staticTypes maps the embedded UI's file extensions to content types —
// the whole surface is three extensions, so a table beats mime lookups.
var staticTypes = map[string]string{
	".html": "text/html; charset=utf-8",
	".css":  "text/css; charset=utf-8",
	".js":   "text/javascript; charset=utf-8",
}

// staticFile is one embedded UI asset. All assets are loaded once at
// package init into staticFiles, so a request path only ever selects
// among fixed compile-time entries — it never reaches a filesystem read.
type staticFile struct {
	data        []byte
	contentType string
}

var staticFiles = loadStaticFiles()

// loadStaticFiles reads every embedded UI asset with a known extension.
// A failure here means the binary itself is broken (the embed is part of
// it), so panicking at init matches New's stance on an unusable process.
func loadStaticFiles() map[string]staticFile {
	entries, err := uiFS.ReadDir("ui")
	if err != nil {
		panic("dashboard: embedded ui missing: " + err.Error())
	}
	out := make(map[string]staticFile, len(entries))
	for _, e := range entries {
		ct, ok := staticTypes[path.Ext(e.Name())]
		if !ok {
			continue
		}
		data, err := uiFS.ReadFile("ui/" + e.Name())
		if err != nil {
			panic("dashboard: read embedded " + e.Name() + ": " + err.Error())
		}
		out[e.Name()] = staticFile{data: data, contentType: ct}
	}
	return out
}

// serveStatic serves the embedded UI files under /ui/, with the designed
// not-found page (styled, and pointing back at /ui/) instead of a bare
// 404 line for paths that don't exist.
func (h *Handler) serveStatic(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/ui/")
	if name == "" {
		name = "index.html"
	}
	f, ok := staticFiles[name]
	if !ok {
		h.servePage(w, http.StatusNotFound, "notfound.html")
		return
	}
	w.Header().Set("Content-Type", f.contentType)
	// Never cached: with no validators, browsers heuristically cache these
	// and keep serving a previous version's UI after an upgrade.
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(f.data)
}

// servePage writes one of the embedded standalone pages (the designed
// signed-out and not-found screens) with the given status. name is always
// a compile-time constant at the call sites.
func (h *Handler) servePage(w http.ResponseWriter, code int, name string) {
	f, ok := staticFiles[name]
	if !ok {
		http.Error(w, http.StatusText(code), code)
		return
	}
	w.Header().Set("Content-Type", f.contentType)
	w.WriteHeader(code)
	_, _ = w.Write(f.data)
}

// writeJSON writes v with the right header; encoding failures are a
// programming error surfaced as 500 with a fixed body.
func (h *Handler) writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (h *Handler) handleStatus(w http.ResponseWriter, _ *http.Request) {
	st := h.deps.Bridge.Status()
	counts, err := h.deps.Store.Counts()
	if err != nil {
		h.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "store unavailable"})
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{
		"state": st.State, "since": st.Since.UTC().Format(time.RFC3339),
		"last_event":      eventTime(st.LastEventAt),
		"reconnects":      st.Reconnects,
		"ingest_errors":   st.IngestErrors,
		"last_disconnect": st.LastDisconnect,
		"needs_pairing":   h.deps.Bridge.NeedsPairing(),
		"chats":           counts.Chats, "messages": counts.Messages,
		"contacts": counts.Contacts, "calls": counts.Calls,
		"version": h.deps.Version,
	})
}

func eventTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func (h *Handler) handleDoctor(w http.ResponseWriter, r *http.Request) {
	findings := h.deps.Doctor(r.Context())
	rows := make([]map[string]string, len(findings))
	for i, f := range findings {
		rows[i] = map[string]string{"check": f.Check, "status": f.Status, "detail": f.Detail, "fix": f.Fix}
	}
	h.writeJSON(w, http.StatusOK, rows)
}
