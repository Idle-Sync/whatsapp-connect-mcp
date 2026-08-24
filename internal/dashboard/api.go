package dashboard

import (
	"net/http"
	"strconv"
	"time"

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
