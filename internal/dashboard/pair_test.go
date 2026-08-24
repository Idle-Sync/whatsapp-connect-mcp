package dashboard

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// pairFakeBridge scripts PairQR: emits the given codes, then blocks until
// released, returning retErr.
type pairFakeBridge struct {
	fakeBridge
	mu      sync.Mutex
	starts  int
	codes   []string
	release chan struct{}
	retErr  error
}

func (f *pairFakeBridge) PairQR(ctx context.Context, show func(string)) error {
	f.mu.Lock()
	f.starts++
	f.mu.Unlock()
	for _, c := range f.codes {
		show(c)
	}
	select {
	case <-f.release:
	case <-ctx.Done():
	}
	return f.retErr
}

func postPairStart(t *testing.T, h *Handler, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/api/pair/start", nil)
	r.AddCookie(cookie)
	r.Header.Set("X-Requested-With", "dashboard")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestPairStartIsSingleFlightAndServesQR(t *testing.T) {
	fb := &pairFakeBridge{fakeBridge: fakeBridge{unpaired: true}, codes: []string{"CODE-1"}, release: make(chan struct{})}
	h, cookie := newTestHandlerWithBridge(t, fb)
	defer close(fb.release)

	if w := postPairStart(t, h, cookie); w.Code != http.StatusOK {
		t.Fatalf("pair start = %d", w.Code)
	}
	if w := postPairStart(t, h, cookie); w.Code != http.StatusOK {
		t.Fatalf("second pair start (join) = %d", w.Code)
	}
	// Wait for the code to land (PairQR runs in a goroutine).
	deadline := time.Now().Add(2 * time.Second)
	var png []byte
	for time.Now().Before(deadline) {
		r := httptest.NewRequest(http.MethodGet, "/api/pair/qr.png", nil)
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code == http.StatusOK {
			png = w.Body.Bytes()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !bytes.HasPrefix(png, []byte("\x89PNG")) {
		t.Fatalf("qr.png is not a PNG (got %d bytes)", len(png))
	}
	fb.mu.Lock()
	defer fb.mu.Unlock()
	if fb.starts != 1 {
		t.Fatalf("PairQR started %d times, want 1 (single-flight)", fb.starts)
	}
}

func TestPairStartWhenPairedIs409(t *testing.T) {
	fb := &pairFakeBridge{fakeBridge: fakeBridge{unpaired: false}}
	h, cookie := newTestHandlerWithBridge(t, fb)
	if w := postPairStart(t, h, cookie); w.Code != http.StatusConflict {
		t.Fatalf("pair start while paired = %d, want 409", w.Code)
	}
}

func TestPairQRWithoutActivePairingIs404(t *testing.T) {
	h, cookie := newTestHandler(t, nil, nil)
	r := httptest.NewRequest(http.MethodGet, "/api/pair/qr.png", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("qr.png with no pairing = %d, want 404", w.Code)
	}
}
