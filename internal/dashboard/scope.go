package dashboard

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/idle-sync/whatsapp-connect-mcp/internal/config"
	"github.com/idle-sync/whatsapp-connect-mcp/internal/target"
)

// scopeRow is one allowlisted chat, carrying the name so the UI can show
// who a bare JID belongs to. Known is false for a JID that is allowed but
// has no chat in the store yet — a typo, or a chat that has not arrived.
type scopeRow struct {
	JID   string `json:"jid"`
	Name  string `json:"name"`
	Known bool   `json:"known"`
}

// handleScope serves the readable-chat allowlist and, on POST, adds to it.
//
// Writing this list is deliberately reachable only from here and the CLI.
// It is the boundary that decides what an agent may read, so the agent
// must not be able to move it: a scope an MCP tool could widen would be
// undone by the first message that talked one into widening it. That is
// the same rule config.json's trust list already follows.
func (h *Handler) handleScope(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cfg, err := config.LoadFor(h.deps.DataDir, h.accountDir())
		if err != nil {
			h.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "config unreadable"})
			return
		}
		mode := config.ScopeAll
		if cfg.ScopeActive() {
			mode = config.ScopeAllowlist
		}
		h.writeJSON(w, http.StatusOK, map[string]any{
			"mode":  mode,
			"chats": h.scopeRows(cfg.ReadableChats),
		})
	case http.MethodPost:
		h.mutating(h.handleScopeAdd)(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// scopeRows decorates the configured JIDs with their chat names.
func (h *Handler) scopeRows(jids []string) []scopeRow {
	out := make([]scopeRow, 0, len(jids))
	for _, jid := range jids {
		row := scopeRow{JID: jid}
		if chat, ok, err := h.deps.Store.Chat(jid); err == nil && ok {
			row.Name, row.Known = chat.Name, true
		}
		out = append(out, row)
	}
	return out
}

// handleScopeAdd takes either a mode change or a chat to allow. Adding a
// chat also switches the mode on: someone who names a chat to allow is
// asking for a restriction, and leaving the mode at "all" would accept the
// input and change nothing.
func (h *Handler) handleScopeAdd(w http.ResponseWriter, r *http.Request) {
	var in struct {
		JID  string `json:"jid"`
		Mode string `json:"mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		h.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request body"})
		return
	}
	cfg, err := config.LoadFor(h.deps.DataDir, h.accountDir())
	if err != nil {
		h.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "config unreadable"})
		return
	}

	switch {
	case in.Mode != "":
		if in.Mode != config.ScopeAll && in.Mode != config.ScopeAllowlist {
			h.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "mode must be all or allowlist"})
			return
		}
		// Switching to "all" keeps the list. Someone turning the scope off
		// for a moment should not have to retype every chat to turn it back
		// on, and an ignored list is not a leak.
		cfg.ChatScope = in.Mode
	case strings.TrimSpace(in.JID) != "":
		jid, ok := h.resolveOrOffer(w, strings.TrimSpace(in.JID))
		if !ok {
			return
		}
		cfg.ChatScope = config.ScopeAllowlist
		if !containsString(cfg.ReadableChats, jid) {
			cfg.ReadableChats = append(cfg.ReadableChats, jid)
			sort.Strings(cfg.ReadableChats)
		}
	default:
		h.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "jid or mode is required"})
		return
	}

	if err := config.SaveFor(h.deps.DataDir, h.accountDir(), cfg); err != nil {
		h.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "config write failed"})
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]string{"mode": cfg.ChatScope, "allowed": in.JID})
}

// handleScopeRemove drops one chat from the allowlist. It does not touch
// the mode: emptying an active allowlist leaves an agent able to read
// nothing, which is the safe reading of "remove the last chat I allowed"
// and the state setup leaves behind when someone chooses to pick their
// chats later. Turning the scope off is its own explicit action.
func (h *Handler) handleScopeRemove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	jid, err := url.PathUnescape(strings.TrimPrefix(r.URL.Path, "/api/scope/"))
	if err != nil {
		h.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad jid"})
		return
	}
	cfg, err := config.LoadFor(h.deps.DataDir, h.accountDir())
	if err != nil {
		h.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "config unreadable"})
		return
	}
	kept := cfg.ReadableChats[:0]
	for _, c := range cfg.ReadableChats {
		if c != jid {
			kept = append(kept, c)
		}
	}
	cfg.ReadableChats = kept
	if err := config.SaveFor(h.deps.DataDir, h.accountDir(), cfg); err != nil {
		h.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "config write failed"})
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]string{"removed": jid})
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// resolveOrOffer turns what someone typed into the box — a name, a phone
// number, or a JID — into a JID. An ambiguous name is answered with 409
// and the candidates, so the page can ask which was meant instead of
// picking one; writing the wrong JID here exposes the wrong chat.
func (h *Handler) resolveOrOffer(w http.ResponseWriter, input string) (string, bool) {
	jid, err := target.Resolve(h.deps.Store, input)
	if err == nil {
		return jid, true
	}

	var amb *target.AmbiguousError
	if errors.As(err, &amb) {
		h.writeJSON(w, http.StatusConflict, map[string]any{
			"error":      "several contacts match that name",
			"candidates": amb.Candidates,
		})
		return "", false
	}
	if errors.Is(err, target.ErrNoMatch) {
		h.writeJSON(w, http.StatusNotFound, map[string]string{
			"error": "no contact or chat matches that name — try the phone number",
		})
		return "", false
	}
	h.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "lookup failed"})
	return "", false
}

// accountDir is where this account's own settings live. It stays empty
// when no account directory was wired, which config.LoadFor and SaveFor
// read as "keep everything in one config.json" — the pre-accounts
// behaviour an unpaired install and the tests rely on.
func (h *Handler) accountDir() string { return h.deps.AccountDir }

// accountFiles is where this account's media and backups are written.
// Unlike accountDir it falls back to the data directory, because a path is
// needed either way and the flat layout is where those files used to live.
func (h *Handler) accountFiles() string {
	if h.deps.AccountDir != "" {
		return h.deps.AccountDir
	}
	return h.deps.DataDir
}
