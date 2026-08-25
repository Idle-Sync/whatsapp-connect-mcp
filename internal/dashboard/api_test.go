package dashboard

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	Cursor   struct {
		TS int64  `json:"ts"`
		ID string `json:"id"`
	} `json:"cursor"`
}

func TestMessagesInitialLoadReturnsRecentWithCursor(t *testing.T) {
	fb := &fakeBridge{status: bridge.Status{State: "connected"}}
	fs := &fakeStore{
		msgs: []store.MessageRow{{ChatJID: "c", ID: "m1", TS: 5, Kind: "image", Text: "cap", HasMedia: true, SenderName: "A"}},
	}
	h, cookie := newTestHandler(t, fs, fb)

	w := authedGet(t, h, cookie, "/api/messages?chat=c%40s.whatsapp.net")
	if w.Code != http.StatusOK {
		t.Fatalf("messages = %d", w.Code)
	}
	if !fb.caughtUp {
		t.Fatal("WaitForCatchUp not called before the read")
	}
	if !fs.recentCalled {
		t.Fatal("initial load must read the newest page by time, not a rowid tail")
	}
	var page messagesPage
	if err := json.NewDecoder(w.Body).Decode(&page); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// The cursor is the newest shown message's (ts, id), so a refresh
	// continues from there.
	if page.Cursor.TS != 5 || page.Cursor.ID != "m1" {
		t.Fatalf("cursor = %+v, want {5 m1}", page.Cursor)
	}
	if len(page.Messages) != 1 || page.Messages[0]["has_media"] != true || page.Messages[0]["kind"] != "image" {
		t.Fatalf("messages = %v", page.Messages)
	}
	if page.Messages[0]["chat"] != "c" {
		t.Fatalf("chat = %v, want the row's chat JID (media links need it)", page.Messages[0]["chat"])
	}
}

func TestMessagesRefreshReadsSinceCursor(t *testing.T) {
	fs := &fakeStore{
		msgs: []store.MessageRow{{ChatJID: "c", ID: "m2", TS: 9, Kind: "text", Text: "new", SenderName: "A"}},
	}
	h, cookie := newTestHandler(t, fs, nil)

	w := authedGet(t, h, cookie, "/api/messages?chat=c%40s.whatsapp.net&after_ts=5&after_id=m1")
	if w.Code != http.StatusOK {
		t.Fatalf("messages = %d", w.Code)
	}
	if fs.recentCalled {
		t.Fatal("a refresh must not reload the newest page")
	}
	if fs.gotSinceTS != 5 || fs.gotSinceID != "m1" {
		t.Fatalf("since cursor = (%d,%q), want (5,m1)", fs.gotSinceTS, fs.gotSinceID)
	}
	var page messagesPage
	if err := json.NewDecoder(w.Body).Decode(&page); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if page.Cursor.TS != 9 || page.Cursor.ID != "m2" || len(page.Messages) != 1 {
		t.Fatalf("page = %+v, want 1 message and cursor {9 m2}", page)
	}
}

func TestMessagesEmptyRefreshKeepsCursorAndEmptyList(t *testing.T) {
	fs := &fakeStore{}
	h, cookie := newTestHandler(t, fs, nil)

	w := authedGet(t, h, cookie, "/api/messages?chat=c%40s.whatsapp.net&after_ts=42&after_id=m9")
	if w.Code != http.StatusOK {
		t.Fatalf("messages = %d", w.Code)
	}
	body := w.Body.String()
	var page messagesPage
	if err := json.Unmarshal([]byte(body), &page); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if page.Cursor.TS != 42 || page.Cursor.ID != "m9" {
		t.Fatalf("cursor = %+v, want the caller's {42 m9} unchanged", page.Cursor)
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

// pngBytes is a minimal valid PNG header — enough for content sniffing to
// identify image/png.
var pngBytes = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")

func TestMediaCacheHitServesInlineRasterWithoutBridge(t *testing.T) {
	fs := &fakeStore{mediaRef: []byte("ref"), mediaKind: "image"}
	fb := &fakeBridge{}
	var dataDir string
	h, cookie := newTestHandlerWith(t, func(d *Deps) {
		d.Store, d.Bridge, dataDir = fs, fb, d.DataDir
	})

	dir := filepath.Join(dataDir, "media", "c@s.whatsapp.net")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "m1.jpg"), pngBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	w := authedGet(t, h, cookie, "/api/media?chat=c%40s.whatsapp.net&id=m1")
	if w.Code != http.StatusOK {
		t.Fatalf("media = %d: %s", w.Code, w.Body.String())
	}
	if fb.downloadedName != "" {
		t.Fatal("cache hit must not reach the bridge")
	}
	if ct := w.Header().Get("Content-Type"); ct != "image/png" {
		t.Fatalf("Content-Type = %q, want sniffed image/png", ct)
	}
	if cd := w.Header().Get("Content-Disposition"); cd != "inline" {
		t.Fatalf("Content-Disposition = %q, want inline", cd)
	}
	if w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("nosniff header missing")
	}
	if !bytes.Equal(w.Body.Bytes(), pngBytes) {
		t.Fatal("served bytes differ from the cached file")
	}
}

func TestMediaCacheMissDownloadsOnceThenServes(t *testing.T) {
	fs := &fakeStore{mediaRef: []byte("ref"), mediaKind: "image"}
	fb := &fakeBridge{downloadData: pngBytes}
	h, cookie := newTestHandlerWith(t, func(d *Deps) { d.Store, d.Bridge = fs, fb })

	w := authedGet(t, h, cookie, "/api/media?chat=c%40s.whatsapp.net&id=m1")
	if w.Code != http.StatusOK {
		t.Fatalf("media = %d: %s", w.Code, w.Body.String())
	}
	if fb.downloadedName != "m1.jpg" {
		t.Fatalf("bridge downloaded %q, want m1.jpg", fb.downloadedName)
	}
	if !bytes.Equal(w.Body.Bytes(), pngBytes) {
		t.Fatal("served bytes differ from the downloaded file")
	}

	fb.downloadedName = ""
	if w := authedGet(t, h, cookie, "/api/media?chat=c%40s.whatsapp.net&id=m1"); w.Code != http.StatusOK {
		t.Fatalf("second read = %d", w.Code)
	}
	if fb.downloadedName != "" {
		t.Fatal("second read must come from the disk cache, not a fresh download")
	}
}

func TestMediaNonRasterNeverRendersInline(t *testing.T) {
	// The stored kind claims image, but the bytes are markup — the served
	// response must be a forced download, never an inline render.
	fs := &fakeStore{mediaRef: []byte("ref"), mediaKind: "image"}
	fb := &fakeBridge{downloadData: []byte("<svg xmlns=\"http://www.w3.org/2000/svg\"><script>alert(1)</script></svg>")}
	h, cookie := newTestHandlerWith(t, func(d *Deps) { d.Store, d.Bridge = fs, fb })

	w := authedGet(t, h, cookie, "/api/media?chat=c%40s.whatsapp.net&id=m1")
	if w.Code != http.StatusOK {
		t.Fatalf("media = %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/octet-stream" {
		t.Fatalf("Content-Type = %q, want application/octet-stream", ct)
	}
	cd := w.Header().Get("Content-Disposition")
	if !strings.HasPrefix(cd, "attachment") || !strings.Contains(cd, `filename="m1.jpg"`) {
		t.Fatalf("Content-Disposition = %q, want attachment with the derived filename", cd)
	}
	if w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("nosniff header missing")
	}
}

func TestMediaDocumentIsAttachment(t *testing.T) {
	fs := &fakeStore{mediaRef: []byte("ref"), mediaKind: "document", mediaFilename: "report.pdf"}
	fb := &fakeBridge{downloadData: []byte("%PDF-1.7 fake")}
	h, cookie := newTestHandlerWith(t, func(d *Deps) { d.Store, d.Bridge = fs, fb })

	w := authedGet(t, h, cookie, "/api/media?chat=c%40s.whatsapp.net&id=m1")
	if w.Code != http.StatusOK {
		t.Fatalf("media = %d", w.Code)
	}
	cd := w.Header().Get("Content-Disposition")
	if !strings.HasPrefix(cd, "attachment") || !strings.Contains(cd, `filename="m1.pdf"`) {
		t.Fatalf("Content-Disposition = %q, want attachment with m1.pdf", cd)
	}
}

func TestMediaMissingIs404(t *testing.T) {
	fs := &fakeStore{mediaRefErr: errors.New("message media: message not found")}
	h, cookie := newTestHandlerWith(t, func(d *Deps) { d.Store = fs })
	if w := authedGet(t, h, cookie, "/api/media?chat=c%40s.whatsapp.net&id=m1"); w.Code != http.StatusNotFound {
		t.Fatalf("missing media = %d, want 404", w.Code)
	}
}

func TestMediaRequiresChatAndID(t *testing.T) {
	h, cookie := newTestHandler(t, nil, nil)
	if w := authedGet(t, h, cookie, "/api/media?chat=c%40s.whatsapp.net"); w.Code != http.StatusBadRequest {
		t.Fatalf("media without id = %d, want 400", w.Code)
	}
	if w := authedGet(t, h, cookie, "/api/media?id=m1"); w.Code != http.StatusBadRequest {
		t.Fatalf("media without chat = %d, want 400", w.Code)
	}
}

// TestSearchCarriesChatNamesAndScope: every result names its chat (the
// list is read chat-first) and ?chat= narrows the search to one chat.
func TestSearchCarriesChatNamesAndScope(t *testing.T) {
	fs := &fakeStore{
		chats: []store.ChatRow{{JID: "g@g.us", Name: "Team", IsGroup: true}},
		msgs: []store.MessageRow{
			{ChatJID: "g@g.us", ID: "m1", TS: 5, Kind: "text", Text: "good morning", SenderName: "A"},
			{ChatJID: "x@s.whatsapp.net", ID: "m2", TS: 4, Kind: "text", Text: "good night", SenderName: "B"},
		},
	}
	h, cookie := newTestHandler(t, fs, nil)

	w := authedGet(t, h, cookie, "/api/search?q=good&chat=g%40g.us")
	if w.Code != http.StatusOK {
		t.Fatalf("search = %d", w.Code)
	}
	if fs.gotChat != "g@g.us" {
		t.Fatalf("chat scope reached the store as %q, want g@g.us", fs.gotChat)
	}
	var rows []map[string]any
	if err := json.NewDecoder(w.Body).Decode(&rows); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(rows) != 2 || rows[0]["chat_name"] != "Team" || rows[0]["chat_is_group"] != true {
		t.Fatalf("rows = %v, want the group's name and flag on its result", rows)
	}
	if rows[1]["chat_name"] != "" || rows[1]["chat_is_group"] != false {
		t.Fatalf("rows[1] = %v, want an empty name for a chat the store doesn't know (client falls back to the JID)", rows[1])
	}
}

// TestMessagesAroundReturnsContextWithoutCursor: opening a search result
// asks for the window around that message, split evenly from the limit,
// and gets no rowid cursor back — that page is parked mid-history.
func TestMessagesAroundReturnsContextWithoutCursor(t *testing.T) {
	fb := &fakeBridge{status: bridge.Status{State: "connected"}}
	fs := &fakeStore{msgs: []store.MessageRow{{ChatJID: "c", ID: "hit", TS: 5, Kind: "text", Text: "found", SenderName: "A"}}}
	h, cookie := newTestHandler(t, fs, fb)

	w := authedGet(t, h, cookie, "/api/messages?chat=c&around=hit&limit=60")
	if w.Code != http.StatusOK {
		t.Fatalf("messages around = %d", w.Code)
	}
	if !fb.caughtUp {
		t.Fatal("WaitForCatchUp not called before the read")
	}
	if fs.gotAround != "hit" || fs.gotBefore != 30 || fs.gotAfterN != 30 {
		t.Fatalf("context asked for %q ±%d/%d, want hit ±30/30", fs.gotAround, fs.gotBefore, fs.gotAfterN)
	}
	if fs.recentCalled {
		t.Fatal("a context load must not read the recent page")
	}
	body := w.Body.String()
	if strings.Contains(body, `"cursor"`) || !strings.Contains(body, `"around":"hit"`) {
		t.Fatalf("body = %s, want the around id echoed and no cursor", body)
	}
}

func TestMessagesAroundUnknownIs404(t *testing.T) {
	fs := &fakeStore{contextErr: errors.New("message context: message not found")}
	h, cookie := newTestHandler(t, fs, nil)
	if w := authedGet(t, h, cookie, "/api/messages?chat=c&around=nope"); w.Code != http.StatusNotFound {
		t.Fatalf("messages around unknown id = %d, want 404", w.Code)
	}
}

func TestSearchRequiresQuery(t *testing.T) {
	h, cookie := newTestHandler(t, nil, nil)
	if w := authedGet(t, h, cookie, "/api/search"); w.Code != http.StatusBadRequest {
		t.Fatalf("search without q = %d, want 400", w.Code)
	}
}

// fixedClock returns a clock function pinned to a base time that each call
// can advance via the returned setter — so cooldown boundaries are tested
// without sleeping.
func fixedClock(base time.Time) (func() time.Time, func(time.Duration)) {
	cur := base
	return func() time.Time { return cur }, func(d time.Duration) { cur = cur.Add(d) }
}

func TestHistoryRequestsFromOldestAnchor(t *testing.T) {
	fb := &fakeBridge{status: bridge.Status{State: "connected"}}
	fs := &fakeStore{oldest: &store.MessageRow{ID: "OLD", FromMe: true, TS: 1000}}
	h, cookie := newTestHandler(t, fs, fb)

	w := mutate(t, h, cookie, http.MethodPost, "/api/history?chat=c%40s.whatsapp.net&count=99999", "")
	if w.Code != http.StatusOK {
		t.Fatalf("history = %d, body %s", w.Code, w.Body.String())
	}
	if fb.historyCalls != 1 || fb.historyChat != "c@s.whatsapp.net" || fb.historyAnchor != "OLD" || !fb.historyFromMe || fb.historyTS != 1000 {
		t.Fatalf("request = %+v, want anchored on the oldest stored message", *fb)
	}
	if fb.historyCount != maxHistoryCount {
		t.Fatalf("count = %d, want clamped to %d", fb.historyCount, maxHistoryCount)
	}
}

func TestHistoryEmptyChatIsRejected(t *testing.T) {
	fb := &fakeBridge{status: bridge.Status{State: "connected"}}
	fs := &fakeStore{} // no oldest
	h, cookie := newTestHandler(t, fs, fb)

	w := mutate(t, h, cookie, http.MethodPost, "/api/history?chat=c%40s.whatsapp.net", "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("history on empty chat = %d, want 400", w.Code)
	}
	if fb.historyCalls != 0 {
		t.Fatal("no request may be sent for a chat with nothing to anchor on")
	}
}

func TestHistoryCooldownsThrottle(t *testing.T) {
	clock, advance := fixedClock(time.Unix(1_800_000_000, 0))
	fb := &fakeBridge{status: bridge.Status{State: "connected"}}
	fs := &fakeStore{oldest: &store.MessageRow{ID: "OLD", TS: 1000}}
	h, cookie := newTestHandlerWith(t, func(d *Deps) { d.Store, d.Bridge, d.Now = fs, fb, clock })

	first := mutate(t, h, cookie, http.MethodPost, "/api/history?chat=a%40s.whatsapp.net", "")
	if first.Code != http.StatusOK {
		t.Fatalf("first request = %d", first.Code)
	}
	// A different chat, immediately: blocked by the GLOBAL cooldown.
	if w := mutate(t, h, cookie, http.MethodPost, "/api/history?chat=b%40s.whatsapp.net", ""); w.Code != http.StatusTooManyRequests {
		t.Fatalf("second chat immediately = %d, want 429 (global cooldown)", w.Code)
	}
	// Past the global cooldown, the other chat is allowed.
	advance(historyGlobalCooldown)
	if w := mutate(t, h, cookie, http.MethodPost, "/api/history?chat=b%40s.whatsapp.net", ""); w.Code != http.StatusOK {
		t.Fatalf("second chat after global cooldown = %d, want 200", w.Code)
	}
	// The FIRST chat again, past the global cooldown but inside its
	// per-chat cooldown: still blocked.
	advance(historyGlobalCooldown)
	if w := mutate(t, h, cookie, http.MethodPost, "/api/history?chat=a%40s.whatsapp.net", ""); w.Code != http.StatusTooManyRequests {
		t.Fatalf("same chat inside per-chat cooldown = %d, want 429", w.Code)
	}
	// Past the per-chat cooldown, allowed once more.
	advance(historyPerChatCooldown)
	if w := mutate(t, h, cookie, http.MethodPost, "/api/history?chat=a%40s.whatsapp.net", ""); w.Code != http.StatusOK {
		t.Fatalf("same chat after per-chat cooldown = %d, want 200", w.Code)
	}
}

func TestHistorySendFailureDoesNotBurnQuota(t *testing.T) {
	clock, _ := fixedClock(time.Unix(1_800_000_000, 0))
	fb := &fakeBridge{status: bridge.Status{State: "connected"}, historyErr: errors.New("not connected")}
	fs := &fakeStore{oldest: &store.MessageRow{ID: "OLD", TS: 1000}}
	h, cookie := newTestHandlerWith(t, func(d *Deps) { d.Store, d.Bridge, d.Now = fs, fb, clock })

	if w := mutate(t, h, cookie, http.MethodPost, "/api/history?chat=a%40s.whatsapp.net", ""); w.Code != http.StatusBadGateway {
		t.Fatalf("failed send = %d, want 502", w.Code)
	}
	// The clock hasn't advanced, yet a retry must be allowed — the failed
	// attempt rolled its cooldown stamp back. fb now succeeds.
	fb.historyErr = nil
	if w := mutate(t, h, cookie, http.MethodPost, "/api/history?chat=a%40s.whatsapp.net", ""); w.Code != http.StatusOK {
		t.Fatalf("retry after a failed send = %d, want 200 (quota not burned)", w.Code)
	}
}

func TestMessagesBeforeReturnsOlderWithMoreFlag(t *testing.T) {
	fb := &fakeBridge{status: bridge.Status{State: "connected"}}
	fs := &fakeStore{olderMsgs: []store.MessageRow{
		{ChatJID: "c", ID: "o1", TS: 1, Kind: "text", Text: "older", SenderName: "A"},
		{ChatJID: "c", ID: "o2", TS: 2, Kind: "text", Text: "less old", SenderName: "A"},
	}}
	h, cookie := newTestHandler(t, fs, fb)

	// limit 2 and a full page back => more:true.
	w := authedGet(t, h, cookie, "/api/messages?chat=c&before_ts=5&before_id=m3&limit=2")
	if w.Code != http.StatusOK {
		t.Fatalf("before = %d", w.Code)
	}
	if fs.gotBeforeTS != 5 || fs.gotBeforeID != "m3" {
		t.Fatalf("before cursor = (%d,%q), want (5,m3)", fs.gotBeforeTS, fs.gotBeforeID)
	}
	body := w.Body.String()
	if !strings.Contains(body, `"more":true`) {
		t.Fatalf("a full page must report more:true; got %s", body)
	}
	if strings.Contains(body, `"cursor"`) {
		t.Fatal("the load-older page must not carry a forward cursor")
	}
}
