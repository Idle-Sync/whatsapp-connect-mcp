package dashboard

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/idle-sync/whatsapp-connect-mcp/internal/config"
	"github.com/idle-sync/whatsapp-connect-mcp/internal/medianame"
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
			"ts":      time.Unix(m.TS, 0).UTC().Format(time.RFC3339),
			"ts_unix": m.TS, // numeric, for the pane's (ts, id) scroll cursors
			"sender":  m.SenderName, "kind": m.Kind, "text": m.Text,
			"id": m.ID, "chat": m.ChatJID, "has_media": m.HasMedia, "from_me": m.FromMe,
			"quoted_id": m.QuotedID, // a reaction's target message (attached to it, not shown as a bubble)
		}
	}
	return rows
}

// cursor is the chat pane's position: the (ts, id) of the newest message
// on screen, in the same total order RecentMessages/MessagesSince use. The
// client sends it back to fetch only what is newer.
type cursor struct {
	TS int64  `json:"ts"`
	ID string `json:"id"`
}

// cursorOf returns the cursor for the newest of an oldest-first page,
// leaving a fallback untouched when the page is empty (an empty refresh
// keeps the caller's cursor).
func cursorOf(msgs []store.MessageRow, fallback cursor) cursor {
	if len(msgs) == 0 {
		return fallback
	}
	last := msgs[len(msgs)-1]
	return cursor{TS: last.TS, ID: last.ID}
}

// handleMessages serves the chat pane's three faces, all ordered by
// wall-clock time (ts, id) — NOT rowid: a chat can hold a history-sync
// backfill inserted long after the moments it records, and the LID fold
// merges chats whose rows interleave in time, so "highest rowid" is not
// "newest message". Without a cursor it returns the newest page; with
// ?after_ts=&after_id= it returns only what is newer, and the response
// carries the next cursor so a refresh neither skips nor duplicates.
// With ?around=<id> — a search result opened in place — it returns the
// window surrounding that message, no cursor: the pane is then parked
// mid-history, and returning to the live tail is a fresh load.
func (h *Handler) handleMessages(w http.ResponseWriter, r *http.Request) {
	chat := r.URL.Query().Get("chat")
	if chat == "" {
		h.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "chat is required"})
		return
	}
	limit := queryLimit(r)
	h.deps.Bridge.WaitForCatchUp(r.Context())

	if around := r.URL.Query().Get("around"); around != "" {
		msgs, err := h.deps.Store.MessageContext(chat, around, limit/2, limit/2)
		if err != nil {
			if strings.Contains(err.Error(), "not found") {
				h.writeJSON(w, http.StatusNotFound, map[string]string{"error": "message not found"})
				return
			}
			h.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "store unavailable"})
			return
		}
		h.writeJSON(w, http.StatusOK, map[string]any{"messages": messageRows(msgs), "around": around})
		return
	}

	if r.URL.Query().Has("before_ts") {
		bts, _ := strconv.ParseInt(r.URL.Query().Get("before_ts"), 10, 64)
		older, err := h.deps.Store.MessagesBefore(chat, bts, r.URL.Query().Get("before_id"), limit)
		if err != nil {
			h.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "store unavailable"})
			return
		}
		// more is true when the page came back full, so the client knows
		// another scroll-up can load further back.
		h.writeJSON(w, http.StatusOK, map[string]any{"messages": messageRows(older), "more": len(older) == limit})
		return
	}

	var msgs []store.MessageRow
	var err error
	prev := cursor{}
	if r.URL.Query().Has("after_ts") {
		prev.TS, _ = strconv.ParseInt(r.URL.Query().Get("after_ts"), 10, 64)
		prev.ID = r.URL.Query().Get("after_id")
		msgs, err = h.deps.Store.MessagesSince(chat, prev.TS, prev.ID, limit)
	} else {
		msgs, err = h.deps.Store.RecentMessages(chat, limit)
	}
	if err != nil {
		h.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "store unavailable"})
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"messages": messageRows(msgs), "cursor": cursorOf(msgs, prev)})
}

func (h *Handler) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if q == "" {
		h.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "q is required"})
		return
	}
	h.deps.Bridge.WaitForCatchUp(r.Context())
	msgs, err := h.deps.Store.SearchMessages(q, r.URL.Query().Get("chat"), queryLimit(r))
	if err != nil {
		h.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "search failed"})
		return
	}
	// A result list is read chat-first — "who was this with" before "what
	// was said" — so each row carries its chat's display name, resolved
	// once per distinct chat.
	rows := messageRows(msgs)
	names := map[string]store.ChatRow{}
	for i, m := range msgs {
		c, seen := names[m.ChatJID]
		if !seen {
			c, _, _ = h.deps.Store.Chat(m.ChatJID)
			names[m.ChatJID] = c
		}
		rows[i]["chat_name"] = c.Name
		rows[i]["chat_is_group"] = c.IsGroup
	}
	h.writeJSON(w, http.StatusOK, rows)
}

// History backfill rate limits. These are deliberately conservative: the
// control is a manual button, so even the per-chat limit is far looser than
// any human clicks, and the global limit bounds a script driving the
// endpoint. Requesting history sends a peer message to the paired phone;
// abnormal request volume is a documented ban trigger (see the README's
// ban-risk section), so the server refuses to exceed these regardless of
// how fast it is asked.
const (
	historyPerChatCooldown = 15 * time.Second
	historyGlobalCooldown  = 3 * time.Second
	defaultHistoryCount    = 50
	maxHistoryCount        = 500
)

func (h *Handler) now() time.Time {
	if h.deps.Now != nil {
		return h.deps.Now()
	}
	return time.Now()
}

// handleHistory asks the paired phone for messages older than the oldest
// one already stored for a chat — the dashboard's "load older" control, and
// the way to pull a chat's history in from scratch, one bounded page at a
// time. It reuses fetch_older_messages' exact machinery: anchor on the
// oldest stored message, send a history-sync request, let the answer land
// asynchronously in the store. Two cooldowns keep a button (or a script)
// from turning into a burst of peer messages. Nothing is sent to any
// contact — this requests your own history from your own phone.
func (h *Handler) handleHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	chat := r.URL.Query().Get("chat")
	if chat == "" {
		h.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "chat is required"})
		return
	}
	count := defaultHistoryCount
	if n, err := strconv.Atoi(r.URL.Query().Get("count")); err == nil && n > 0 {
		count = n
	}
	if count > maxHistoryCount {
		count = maxHistoryCount
	}

	// The phone anchors its reply on a message it can be told about, so a
	// chat with nothing stored cannot be backfilled. Say so plainly.
	oldest, ok, err := h.deps.Store.OldestMessage(chat)
	if err != nil {
		h.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "store unavailable"})
		return
	}
	if !ok {
		h.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "nothing stored for this chat yet — open it once it has messages, then load older"})
		return
	}

	// Reserve a slot under the cooldowns before sending. Holding the lock
	// across the send would serialize every history request behind one
	// round-trip; instead we stamp the times up front and roll them back
	// if the send itself fails, so a failed attempt doesn't burn the quota.
	now := h.now()
	h.historyMu.Lock()
	if wait := historyGlobalCooldown - now.Sub(h.lastHistoryAny); wait > 0 {
		h.historyMu.Unlock()
		h.tooSoon(w, wait)
		return
	}
	if last, seen := h.lastHistory[chat]; seen {
		if wait := historyPerChatCooldown - now.Sub(last); wait > 0 {
			h.historyMu.Unlock()
			h.tooSoon(w, wait)
			return
		}
	}
	prevChat, prevAny := h.lastHistory[chat], h.lastHistoryAny
	h.lastHistory[chat] = now
	h.lastHistoryAny = now
	h.historyMu.Unlock()

	if err := h.deps.Bridge.RequestOlderMessages(r.Context(), chat, oldest.ID, oldest.FromMe, oldest.TS, count); err != nil {
		h.historyMu.Lock()
		// Roll back only if nothing newer claimed the slot meanwhile.
		if h.lastHistory[chat].Equal(now) {
			if prevChat.IsZero() {
				delete(h.lastHistory, chat)
			} else {
				h.lastHistory[chat] = prevChat
			}
		}
		if h.lastHistoryAny.Equal(now) {
			h.lastHistoryAny = prevAny
		}
		h.historyMu.Unlock()
		h.writeJSON(w, http.StatusBadGateway, map[string]string{"error": "couldn't reach WhatsApp to request history"})
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{
		"status":    "requested older messages — they arrive from your phone in a few seconds; refresh to see them",
		"anchor_ts": oldest.TS,
		"count":     count,
	})
}

// tooSoon writes the 429 a cooldown produces, naming the seconds to wait so
// the client can show a countdown instead of a bare error.
func (h *Handler) tooSoon(w http.ResponseWriter, wait time.Duration) {
	secs := int(wait/time.Second) + 1
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	h.writeJSON(w, http.StatusTooManyRequests, map[string]any{
		"error":       "history was requested very recently — wait a moment before asking again",
		"retry_after": secs,
	})
}

// rasterInline is the closed set of content types the media endpoint will
// serve inline. Membership is decided by sniffing the file's own bytes,
// never the sender-declared kind; everything outside it — notably SVG and
// HTML, which would execute script on this origin — is forced down as an
// opaque attachment.
var rasterInline = map[string]bool{
	"image/jpeg": true, "image/png": true, "image/gif": true, "image/webp": true,
}

// handleMedia serves one message's media from <dataDir>/media/<chat>/,
// the same location download_media saves to — an existing file is served
// with no WhatsApp traffic, and a miss is downloaded through the bridge
// exactly once. Attachments are attacker-supplied content served from the
// dashboard's own origin: the content type is verified server-side and
// only allowlisted raster images ever render inline.
func (h *Handler) handleMedia(w http.ResponseWriter, r *http.Request) {
	chat := r.URL.Query().Get("chat")
	id := r.URL.Query().Get("id")
	if chat == "" || id == "" {
		h.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "chat and id are required"})
		return
	}
	ref, senderFilename, kind, err := h.deps.Store.MessageMediaRef(chat, id)
	if err != nil {
		h.writeJSON(w, http.StatusNotFound, map[string]string{"error": "no media for that message"})
		return
	}
	dir, err := medianame.ChatDir(h.deps.DataDir, chat)
	if err != nil {
		h.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid chat"})
		return
	}
	name, err := medianame.SavedFilename(id, kind, senderFilename)
	if err != nil {
		h.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid message id"})
		return
	}
	path := filepath.Join(dir, name)
	if _, err := os.Stat(path); err != nil { // #nosec G703 -- dir and name are traversal-checked by medianame just above
		if _, err := h.deps.Bridge.DownloadMedia(r.Context(), ref, dir, name); err != nil {
			h.writeJSON(w, http.StatusBadGateway, map[string]string{"error": "media download failed"})
			return
		}
	}

	f, err := os.Open(path) // #nosec G304 G703 -- same medianame-confined path as the Stat above
	if err != nil {
		h.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "media unreadable"})
		return
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		h.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "media unreadable"})
		return
	}

	buf := make([]byte, 512)
	n, err := io.ReadFull(f, buf)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		h.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "media unreadable"})
		return
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		h.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "media unreadable"})
		return
	}

	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	if ct := http.DetectContentType(buf[:n]); rasterInline[ct] {
		w.Header().Set("Content-Type", ct)
		w.Header().Set("Content-Disposition", "inline")
	} else {
		w.Header().Set("Content-Type", "application/octet-stream")
		// name is a validated single path component (message-id-derived);
		// stripping quotes keeps the header unambiguous even so.
		w.Header().Set("Content-Disposition", `attachment; filename="`+strings.ReplaceAll(name, `"`, "")+`"`)
	}
	http.ServeContent(w, r, "", info.ModTime(), f)
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
