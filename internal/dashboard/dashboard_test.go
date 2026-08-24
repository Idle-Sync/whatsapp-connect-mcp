package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/idle-sync/whatsapp-connect-mcp/internal/bridge"
	"github.com/idle-sync/whatsapp-connect-mcp/internal/doctor"
	"github.com/idle-sync/whatsapp-connect-mcp/internal/store"
)

// fakeStore satisfies dashboard.Store; fields configure behavior, later
// task tests extend it alongside the interface.
type fakeStore struct {
	counts     store.Counts
	chats      []store.ChatRow
	msgs       []store.MessageRow
	tailRowID  int64
	nextRowID  int64
	gotLimit   int
	gotAfter   int64
	gotOwn     bool
	tailCalled bool
	backupPath string

	mediaRef      []byte
	mediaFilename string
	mediaKind     string
	mediaRefErr   error
}

func (f *fakeStore) Counts() (store.Counts, error) { return f.counts, nil }

func (f *fakeStore) Chats(_ string, _ bool, limit int) ([]store.ChatRow, error) {
	f.gotLimit = limit
	return f.chats, nil
}

func (f *fakeStore) TailRowID(_ string, includeOwn bool, n int) (int64, error) {
	f.tailCalled = true
	f.gotOwn = includeOwn
	f.gotLimit = n
	return f.tailRowID, nil
}

func (f *fakeStore) MessagesAfterRowID(_ string, afterRowID int64, includeOwn bool, limit int) ([]store.MessageRow, int64, error) {
	f.gotAfter = afterRowID
	f.gotOwn = includeOwn
	f.gotLimit = limit
	return f.msgs, f.nextRowID, nil
}

func (f *fakeStore) SearchMessages(_, _ string, limit int) ([]store.MessageRow, error) {
	f.gotLimit = limit
	return f.msgs, nil
}

func (f *fakeStore) MessageMediaRef(_, _ string) ([]byte, string, string, error) {
	if f.mediaRefErr != nil {
		return nil, "", "", f.mediaRefErr
	}
	return f.mediaRef, f.mediaFilename, f.mediaKind, nil
}

func (f *fakeStore) BackupTo(path string) error {
	f.backupPath = path
	return os.WriteFile(path, []byte("fake"), 0o600)
}

type fakeBridge struct {
	status    bridge.Status
	unpaired  bool
	caughtUp  bool
	loggedOut bool
	logoutErr error

	downloadData   []byte // bytes DownloadMedia writes to the requested path
	downloadedName string // filename of the last DownloadMedia call
}

func (f *fakeBridge) DownloadMedia(_ context.Context, _ []byte, destDir, filename string) (string, error) {
	f.downloadedName = filename
	if err := os.MkdirAll(destDir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(destDir, filename)
	if err := os.WriteFile(path, f.downloadData, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func (f *fakeBridge) Status() bridge.Status          { return f.status }
func (f *fakeBridge) NeedsPairing() bool             { return f.unpaired }
func (f *fakeBridge) WaitForCatchUp(context.Context) { f.caughtUp = true }

// fakeBridge's default PairQR blocks until cancelled — the base fake
// never pairs; pairFakeBridge overrides it with scripted behavior.
func (f *fakeBridge) PairQR(ctx context.Context, _ func(string)) error {
	<-ctx.Done()
	return ctx.Err()
}

// Logout records the unlink and flips the fake to unpaired, mirroring the
// real bridge's post-logout state.
func (f *fakeBridge) Logout(context.Context) error {
	f.loggedOut = true
	f.unpaired = true
	return f.logoutErr
}

const testToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// newTestHandlerWith is the general constructor: sane default Deps, then
// mod tweaks them, then New + the login flow. Every other constructor in
// this package's tests is a one-line delegation to it.
func newTestHandlerWith(t *testing.T, mod func(*Deps)) (*Handler, *http.Cookie) {
	t.Helper()
	deps := Deps{
		Ctx:     context.Background(),
		Store:   &fakeStore{},
		Bridge:  &fakeBridge{status: bridge.Status{State: "connected"}},
		DataDir: t.TempDir(), Token: testToken, Version: "test",
		Doctor: func(context.Context) []doctor.Finding { return nil },
	}
	if mod != nil {
		mod(&deps)
	}
	h := New(deps)

	r := httptest.NewRequest(http.MethodGet, "/ui/login?token="+testToken, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusFound {
		t.Fatalf("login = %d, want 302", w.Code)
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == "wcm_session" {
			if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode {
				t.Fatalf("session cookie not HttpOnly+SameSite=Strict: %+v", c)
			}
			return h, c
		}
	}
	t.Fatal("no session cookie set on login")
	return nil, nil
}

// newTestHandler keeps the common two-fake call sites short.
func newTestHandler(t *testing.T, fs *fakeStore, fb *fakeBridge) (*Handler, *http.Cookie) {
	t.Helper()
	return newTestHandlerWith(t, func(d *Deps) {
		if fs != nil {
			d.Store = fs
		}
		if fb != nil {
			d.Bridge = fb
		}
	})
}

func newTestHandlerWithBridge(t *testing.T, fb Bridge) (*Handler, *http.Cookie) {
	t.Helper()
	return newTestHandlerWith(t, func(d *Deps) { d.Bridge = fb })
}

func TestLoginRejectsBadToken(t *testing.T) {
	h, _ := newTestHandler(t, nil, nil)
	r := httptest.NewRequest(http.MethodGet, "/ui/login?token=wrong", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("bad token login = %d, want 401", w.Code)
	}
	if len(w.Result().Cookies()) != 0 {
		t.Fatal("cookie set on failed login")
	}
}

func TestAPIRequiresCookieOrBearer(t *testing.T) {
	h, cookie := newTestHandler(t, &fakeStore{counts: store.Counts{Messages: 7}}, nil)

	r := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated /api/status = %d, want 401", w.Code)
	}

	r = httptest.NewRequest(http.MethodGet, "/api/status", nil)
	r.AddCookie(cookie)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("cookie-authed /api/status = %d, want 200", w.Code)
	}
	var got struct {
		State    string `json:"state"`
		Messages int    `json:"messages"`
		Version  string `json:"version"`
	}
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.State != "connected" || got.Messages != 7 || got.Version != "test" {
		t.Fatalf("status payload = %+v", got)
	}

	r = httptest.NewRequest(http.MethodGet, "/api/status", nil)
	r.Header.Set("Authorization", "Bearer "+testToken)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("bearer-authed /api/status = %d, want 200", w.Code)
	}
}

func TestMutatingRequiresCSRFHeader(t *testing.T) {
	h, cookie := newTestHandler(t, nil, nil)
	// /api/backup is Task 5; the CSRF gate is generic, so use any POST
	// route through h.mutating — Task 2 registers a POST-only probe is NOT
	// added; instead assert via the pair endpoint once Task 3 lands. For
	// Task 2 the gate is unit-tested directly:
	called := false
	inner := h.mutating(func(http.ResponseWriter, *http.Request) { called = true })

	r := httptest.NewRequest(http.MethodPost, "/api/anything", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	inner(w, r)
	if called || w.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF header: called=%v code=%d, want blocked 403", called, w.Code)
	}

	r = httptest.NewRequest(http.MethodPost, "/api/anything", nil)
	r.AddCookie(cookie)
	r.Header.Set("X-Requested-With", "dashboard")
	w = httptest.NewRecorder()
	inner(w, r)
	if !called {
		t.Fatal("valid CSRF header still blocked")
	}
}

// TestEmbeddedJSHasNoHTMLSinks enforces the XSS invariant mechanically:
// WhatsApp-originated strings may only reach the DOM via textContent, so
// the HTML-parsing sinks must never appear in the embedded JS.
func TestEmbeddedJSHasNoHTMLSinks(t *testing.T) {
	data, err := uiFS.ReadFile("ui/app.js")
	if err != nil {
		t.Fatalf("read embedded app.js: %v", err)
	}
	for _, sink := range []string{"innerHTML", "outerHTML", "insertAdjacentHTML", "document.write"} {
		if strings.Contains(string(data), sink) {
			t.Fatalf("embedded JS contains forbidden sink %q", sink)
		}
	}
}

func TestUIRequiresSession(t *testing.T) {
	h, cookie := newTestHandler(t, nil, nil)
	r := httptest.NewRequest(http.MethodGet, "/ui/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated /ui/ = %d, want 401", w.Code)
	}
	r = httptest.NewRequest(http.MethodGet, "/ui/", nil)
	r.AddCookie(cookie)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "<title>") {
		t.Fatalf("authed /ui/ = %d, want the page", w.Code)
	}
}

// TestSignedOutPageForBrowserPaths: an unauthenticated /ui/ request gets
// the designed signed-out page (with the way back in), while /api keeps
// the terse 401 the dashboard's JS keys off.
func TestSignedOutPageForBrowserPaths(t *testing.T) {
	h, _ := newTestHandler(t, nil, nil)

	r := httptest.NewRequest(http.MethodGet, "/ui/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized || !strings.Contains(w.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("/ui/ unauth = %d %q, want 401 html", w.Code, w.Header().Get("Content-Type"))
	}
	if !strings.Contains(w.Body.String(), "whatsapp-connect-mcp dashboard") {
		t.Fatal("signed-out page must say how to log back in")
	}

	r = httptest.NewRequest(http.MethodGet, "/api/status", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized || strings.Contains(w.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("/api unauth = %d %q, want terse 401", w.Code, w.Header().Get("Content-Type"))
	}
}

func TestDesignedNotFoundForMissingStatic(t *testing.T) {
	h, cookie := newTestHandler(t, nil, nil)
	r := httptest.NewRequest(http.MethodGet, "/ui/nope.css", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "/ui/") {
		t.Fatalf("missing static = %d, want designed 404 pointing at /ui/", w.Code)
	}
}

func TestUnknownAPIPathIsJSON404(t *testing.T) {
	h, cookie := newTestHandler(t, nil, nil)
	r := httptest.NewRequest(http.MethodGet, "/api/definitely-not-a-thing", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound || !strings.Contains(w.Header().Get("Content-Type"), "json") {
		t.Fatalf("unknown api = %d %q, want JSON 404", w.Code, w.Header().Get("Content-Type"))
	}
}
