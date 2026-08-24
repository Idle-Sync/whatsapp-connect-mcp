package dashboard

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

type messagesPage struct {
	Messages []map[string]any `json:"messages"`
	Cursor   int64            `json:"cursor"`
}

func TestMessagesInitialLoadTailsWithCursor(t *testing.T) {
	fb := &fakeBridge{status: bridge.Status{State: "connected"}}
	fs := &fakeStore{
		msgs:      []store.MessageRow{{ChatJID: "c", ID: "m1", TS: 5, Kind: "image", Text: "cap", HasMedia: true, SenderName: "A"}},
		tailRowID: 7, nextRowID: 42,
	}
	h, cookie := newTestHandler(t, fs, fb)

	w := authedGet(t, h, cookie, "/api/messages?chat=c%40s.whatsapp.net")
	if w.Code != http.StatusOK {
		t.Fatalf("messages = %d", w.Code)
	}
	if !fb.caughtUp {
		t.Fatal("WaitForCatchUp not called before the read")
	}
	if !fs.tailCalled || fs.gotAfter != 7 {
		t.Fatalf("tail cursor: tailCalled=%v afterRowID=%d, want tail cursor 7 fed to the list", fs.tailCalled, fs.gotAfter)
	}
	if !fs.gotOwn {
		t.Fatal("dashboard must include own sends")
	}
	var page messagesPage
	if err := json.NewDecoder(w.Body).Decode(&page); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if page.Cursor != 42 {
		t.Fatalf("cursor = %d, want 42", page.Cursor)
	}
	if len(page.Messages) != 1 || page.Messages[0]["has_media"] != true || page.Messages[0]["kind"] != "image" {
		t.Fatalf("messages = %v", page.Messages)
	}
}

func TestMessagesRefreshReadsAfterCursor(t *testing.T) {
	fs := &fakeStore{
		msgs:      []store.MessageRow{{ChatJID: "c", ID: "m2", TS: 9, Kind: "text", Text: "new", SenderName: "A"}},
		nextRowID: 43,
	}
	h, cookie := newTestHandler(t, fs, nil)

	w := authedGet(t, h, cookie, "/api/messages?chat=c%40s.whatsapp.net&after=42")
	if w.Code != http.StatusOK {
		t.Fatalf("messages = %d", w.Code)
	}
	if fs.tailCalled {
		t.Fatal("refresh must not recompute the tail cursor")
	}
	if fs.gotAfter != 42 {
		t.Fatalf("afterRowID = %d, want the caller's cursor 42", fs.gotAfter)
	}
	var page messagesPage
	if err := json.NewDecoder(w.Body).Decode(&page); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if page.Cursor != 43 || len(page.Messages) != 1 {
		t.Fatalf("page = %+v, want 1 message and cursor 43", page)
	}
}

func TestMessagesEmptyRefreshKeepsCursorAndEmptyList(t *testing.T) {
	fs := &fakeStore{nextRowID: 42}
	h, cookie := newTestHandler(t, fs, nil)

	w := authedGet(t, h, cookie, "/api/messages?chat=c%40s.whatsapp.net&after=42")
	if w.Code != http.StatusOK {
		t.Fatalf("messages = %d", w.Code)
	}
	body := w.Body.String()
	var page messagesPage
	if err := json.Unmarshal([]byte(body), &page); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if page.Cursor != 42 {
		t.Fatalf("cursor = %d, want unchanged 42", page.Cursor)
	}
	if !strings.Contains(body, `"messages":[]`) {
		t.Fatalf("empty page must encode messages as [], got %s", body)
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
