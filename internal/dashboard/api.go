package dashboard

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/idle-sync/whatsapp-connect-mcp/internal/config"
	"github.com/idle-sync/whatsapp-connect-mcp/internal/store"
)

// queryLimit parses ?limit= and clamps through the store's single source
// of truth, so the dashboard can never ask for more than a tool could.
func queryLimit(r *http.Request) int {
	n, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	return store.ClampLimit(n)
}

func (h *Handler) handleChats(w http.ResponseWriter, r *http.Request) {
	chats, err := h.deps.Store.Chats(r.URL.Query().Get("query"), false, queryLimit(r))
	if err != nil {
		h.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "store unavailable"})
		return
	}
	rows := make([]map[string]any, len(chats))
	for i, c := range chats {
		rows[i] = map[string]any{
			"jid": c.JID, "name": c.Name, "is_group": c.IsGroup,
			"last_message_at": time.Unix(c.LastMessageAt, 0).UTC().Format(time.RFC3339),
		}
	}
	h.writeJSON(w, http.StatusOK, rows)
}

func messageRows(msgs []store.MessageRow) []map[string]any {
	rows := make([]map[string]any, len(msgs))
	for i, m := range msgs {
		rows[i] = map[string]any{
			"ts":     time.Unix(m.TS, 0).UTC().Format(time.RFC3339),
			"sender": m.SenderName, "kind": m.Kind, "text": m.Text,
			"id": m.ID, "has_media": m.HasMedia, "from_me": m.FromMe,
		}
	}
	return rows
}

func (h *Handler) handleMessages(w http.ResponseWriter, r *http.Request) {
	chat := r.URL.Query().Get("chat")
	if chat == "" {
		h.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "chat is required"})
		return
	}
	before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	h.deps.Bridge.WaitForCatchUp(r.Context())
	msgs, err := h.deps.Store.Messages(chat, before, 0, queryLimit(r))
	if err != nil {
		h.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "store unavailable"})
		return
	}
	h.writeJSON(w, http.StatusOK, messageRows(msgs))
}

func (h *Handler) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if q == "" {
		h.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "q is required"})
		return
	}
	h.deps.Bridge.WaitForCatchUp(r.Context())
	msgs, err := h.deps.Store.SearchMessages(q, "", queryLimit(r))
	if err != nil {
		h.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "search failed"})
		return
	}
	h.writeJSON(w, http.StatusOK, messageRows(msgs))
}

// Trust management. The written project rule is amended alongside this
// change: per-contact trust is set only by the human — via the CLI or
// this authenticated local dashboard — never by a model or MCP tool.
// Session-scoped grants stay CLI-only.
func (h *Handler) handleTrust(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cfg, err := config.Load(h.deps.DataDir)
		if err != nil {
			h.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "config unreadable"})
			return
		}
		if cfg.TrustedJIDs == nil {
			cfg.TrustedJIDs = []string{}
		}
		h.writeJSON(w, http.StatusOK, cfg.TrustedJIDs)
	case http.MethodPost:
		h.mutating(h.handleTrustAdd)(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) handleTrustAdd(w http.ResponseWriter, r *http.Request) {
	var in struct {
		JID string `json:"jid"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || strings.TrimSpace(in.JID) == "" {
		h.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "jid is required"})
		return
	}
	jid := strings.TrimSpace(in.JID)
	cfg, err := config.Load(h.deps.DataDir)
	if err != nil {
		h.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "config unreadable"})
		return
	}
	if !cfg.IsTrusted(jid) {
		cfg.TrustedJIDs = append(cfg.TrustedJIDs, jid)
		sort.Strings(cfg.TrustedJIDs)
		if err := config.Save(h.deps.DataDir, cfg); err != nil {
			h.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "config write failed"})
			return
		}
	}
	h.writeJSON(w, http.StatusOK, map[string]string{"trusted": jid})
}

func (h *Handler) handleTrustRemove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	jid := strings.TrimPrefix(r.URL.Path, "/api/trust/")
	cfg, err := config.Load(h.deps.DataDir)
	if err != nil {
		h.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "config unreadable"})
		return
	}
	kept := cfg.TrustedJIDs[:0]
	for _, t := range cfg.TrustedJIDs {
		if t != jid {
			kept = append(kept, t)
		}
	}
	cfg.TrustedJIDs = kept
	if err := config.Save(h.deps.DataDir, cfg); err != nil {
		h.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "config write failed"})
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]string{"untrusted": jid})
}

func (h *Handler) handleSchedules(w http.ResponseWriter, _ *http.Request) {
	if h.deps.Sched == nil {
		h.writeJSON(w, http.StatusOK, []any{})
		return
	}
	entries := h.deps.Sched.List()
	rows := make([]map[string]any, len(entries))
	for i, e := range entries {
		rows[i] = map[string]any{
			"id": e.ID, "fire_at": time.Unix(e.Delivery.FireAt, 0).UTC().Format(time.RFC3339),
			"preview": e.Preview,
		}
	}
	h.writeJSON(w, http.StatusOK, rows)
}

func (h *Handler) handleScheduleCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/schedules/")
	if h.deps.Sched == nil {
		http.NotFound(w, r)
		return
	}
	removed, err := h.deps.Sched.Remove(id)
	if err != nil {
		h.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "schedule store write failed"})
		return
	}
	if !removed {
		http.NotFound(w, r)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]string{"cancelled": id})
}

func (h *Handler) handleDrafts(w http.ResponseWriter, _ *http.Request) {
	if h.deps.Gate == nil {
		h.writeJSON(w, http.StatusOK, []any{})
		return
	}
	drafts := h.deps.Gate.Drafts()
	rows := make([]map[string]any, len(drafts))
	for i, d := range drafts {
		rows[i] = map[string]any{"token": d.Token, "preview": d.Preview, "expires": d.Expires.UTC().Format(time.RFC3339)}
	}
	h.writeJSON(w, http.StatusOK, rows)
}

func (h *Handler) handleDraftAction(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/drafts/")
	if h.deps.Gate == nil {
		http.NotFound(w, r)
		return
	}
	if r.Method == http.MethodDelete {
		if !h.deps.Gate.Discard(rest) {
			http.NotFound(w, r)
			return
		}
		h.writeJSON(w, http.StatusOK, map[string]string{"discarded": rest})
		return
	}
	token, ok := strings.CutSuffix(rest, "/approve")
	if !ok || r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	res, err := h.deps.Gate.Approve(r.Context(), token)
	if err != nil {
		h.writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()}) // gate errors are category-only
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"sent": res.Sent, "message_id": res.MessageID})
}

func (h *Handler) handleBackup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	path := store.DefaultBackupPath(h.deps.DataDir, time.Now())
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		h.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not create backups directory"})
		return
	}
	if err := h.deps.Store.BackupTo(path); err != nil {
		h.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()}) // BackupTo errors are path-free by contract
		return
	}
	_ = os.Chmod(path, 0o600)
	info, err := os.Stat(path)
	size := int64(0)
	if err == nil {
		size = info.Size()
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"path": path, "size": size})
}
