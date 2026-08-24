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

// toctouBridge proves handlePairStart decides active-join and
// NeedsPairing atomically: NeedsPairing blocks until released, and each
// call is announced on called so the test can show that no second call
// arrives while the first is still pending — which is only true if both
// checks run under the same lock.
type toctouBridge struct {
	fakeBridge
	mu         sync.Mutex
	needsCalls int
	starts     int
	called     chan struct{}
	proceed    chan struct{}
	release    chan struct{}
}

func (b *toctouBridge) NeedsPairing() bool {
	b.mu.Lock()
	b.needsCalls++
	b.mu.Unlock()
	b.called <- struct{}{}
	<-b.proceed
	return true
}

func (b *toctouBridge) PairQR(ctx context.Context, _ func(string)) error {
	b.mu.Lock()
	b.starts++
	b.mu.Unlock()
	select {
	case <-b.release:
	case <-ctx.Done():
	}
	return nil
}

func TestPairStartDecidesActiveAndNeedsPairingAtomically(t *testing.T) {
	fb := &toctouBridge{called: make(chan struct{}), proceed: make(chan struct{}), release: make(chan struct{})}
	defer close(fb.release)
	h, cookie := newTestHandlerWithBridge(t, fb)

	doneA := make(chan *httptest.ResponseRecorder, 1)
	go func() { doneA <- postPairStart(t, h, cookie) }()

	select {
	case <-fb.called:
	case <-time.After(2 * time.Second):
		t.Fatal("NeedsPairing was never called")
	}

	// The first request's decision is still in flight (blocked inside
	// NeedsPairing). A second start must not be able to reach its own
	// NeedsPairing call in the meantime — if it can, the active-join
	// check and the NeedsPairing check are not one critical section.
	doneC := make(chan *httptest.ResponseRecorder, 1)
	go func() { doneC <- postPairStart(t, h, cookie) }()

	select {
	case <-fb.called:
		t.Fatal("second start observed NeedsPairing while the first request's decision was still pending")
	case <-time.After(200 * time.Millisecond):
	}

	close(fb.proceed)

	wA := <-doneA
	wC := <-doneC
	if wA.Code != http.StatusOK || wC.Code != http.StatusOK {
		t.Fatalf("start = %d, join = %d, want both 200", wA.Code, wC.Code)
	}

	// runPairing dispatches PairQR from a goroutine spawned after the
	// response is written; wait for it to land before counting starts.
	deadline := time.Now().Add(2 * time.Second)
	for {
		fb.mu.Lock()
		starts := fb.starts
		fb.mu.Unlock()
		if starts > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	fb.mu.Lock()
	defer fb.mu.Unlock()
	if fb.needsCalls != 1 {
		t.Fatalf("NeedsPairing called %d times, want 1 (decided once, under the pairing lock)", fb.needsCalls)
	}
	if fb.starts != 1 {
		t.Fatalf("PairQR started %d times, want 1", fb.starts)
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
