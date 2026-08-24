package dashboard

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/idle-sync/whatsapp-connect-mcp/internal/bridge"
	"github.com/idle-sync/whatsapp-connect-mcp/internal/store"
)

func authedGet(t *testing.T, h *Handler, cookie *http.Cookie, path string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestChatsEndpointClampsAndRenders(t *testing.T) {
	fs := &fakeStore{
		chats: []store.ChatRow{{JID: "x@s.whatsapp.net", Name: "<b>Alice</b>", LastMessageAt: 100}},
	}
	h, cookie := newTestHandler(t, fs, nil)

	w := authedGet(t, h, cookie, "/api/chats?limit=99999")
	if w.Code != http.StatusOK {
		t.Fatalf("chats = %d", w.Code)
	}
	if fs.gotLimit != store.MaxLimit {
		t.Fatalf("limit reached store as %d, want clamped %d", fs.gotLimit, store.MaxLimit)
	}
	var rows []map[string]any
	if err := json.NewDecoder(w.Body).Decode(&rows); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// JSON carries the raw name; HTML-escaping is the client's textContent
	// job, but the JSON encoding itself must not mangle or drop it.
	if rows[0]["name"] != "<b>Alice</b>" {
		t.Fatalf("name = %v", rows[0]["name"])
	}
}

func TestMessagesEndpointWaitsForCatchUp(t *testing.T) {
	fb := &fakeBridge{status: bridge.Status{State: "connected"}}
	fs := &fakeStore{msgs: []store.MessageRow{{ChatJID: "c", ID: "m1", TS: 5, Kind: "image", Text: "cap", HasMedia: true, SenderName: "A"}}}
	h, cookie := newTestHandler(t, fs, fb)

	w := authedGet(t, h, cookie, "/api/messages?chat=c%40s.whatsapp.net")
	if w.Code != http.StatusOK {
		t.Fatalf("messages = %d", w.Code)
	}
	if !fb.caughtUp {
		t.Fatal("WaitForCatchUp not called before the read")
	}
	var rows []map[string]any
	_ = json.NewDecoder(w.Body).Decode(&rows)
	if rows[0]["has_media"] != true || rows[0]["kind"] != "image" {
		t.Fatalf("row = %v", rows[0])
	}
}

func TestMessagesRequiresChat(t *testing.T) {
	h, cookie := newTestHandler(t, nil, nil)
	if w := authedGet(t, h, cookie, "/api/messages"); w.Code != http.StatusBadRequest {
		t.Fatalf("messages without chat = %d, want 400", w.Code)
	}
}

func TestSearchRequiresQuery(t *testing.T) {
	h, cookie := newTestHandler(t, nil, nil)
	if w := authedGet(t, h, cookie, "/api/search"); w.Code != http.StatusBadRequest {
		t.Fatalf("search without q = %d, want 400", w.Code)
	}
}
