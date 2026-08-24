package dashboard

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"golang.org/x/time/rate"

	"github.com/idle-sync/whatsapp-connect-mcp/internal/bridge"
)

func TestAvatarServesSniffedRasterAndCaches(t *testing.T) {
	fb := &fakeBridge{avatarData: pngBytes}
	h, cookie := newTestHandlerWith(t, func(d *Deps) { d.Bridge = fb })

	w := authedGet(t, h, cookie, "/api/avatar?jid=x%40s.whatsapp.net")
	if w.Code != http.StatusOK {
		t.Fatalf("avatar = %d: %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "image/png" {
		t.Fatalf("Content-Type = %q, want sniffed image/png", ct)
	}
	if w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("nosniff header missing")
	}
	if cc := w.Header().Get("Cache-Control"); cc != "private, max-age=3600" {
		t.Fatalf("Cache-Control = %q, want private, max-age=3600", cc)
	}
	if fb.avatarCalls != 1 {
		t.Fatalf("bridge lookups = %d, want 1", fb.avatarCalls)
	}

	if w := authedGet(t, h, cookie, "/api/avatar?jid=x%40s.whatsapp.net"); w.Code != http.StatusOK {
		t.Fatalf("second avatar read = %d", w.Code)
	}
	if fb.avatarCalls != 1 {
		t.Fatalf("bridge lookups after cache hit = %d, want still 1", fb.avatarCalls)
	}
}

func TestAvatarNoPictureIs404AndNegativeCached(t *testing.T) {
	fb := &fakeBridge{avatarErr: bridge.ErrNoProfilePicture}
	h, cookie := newTestHandlerWith(t, func(d *Deps) { d.Bridge = fb })

	if w := authedGet(t, h, cookie, "/api/avatar?jid=x%40s.whatsapp.net"); w.Code != http.StatusNotFound {
		t.Fatalf("no picture = %d, want 404", w.Code)
	}
	if w := authedGet(t, h, cookie, "/api/avatar?jid=x%40s.whatsapp.net"); w.Code != http.StatusNotFound {
		t.Fatalf("second no-picture read = %d, want 404", w.Code)
	}
	if fb.avatarCalls != 1 {
		t.Fatalf("bridge lookups = %d, want 1 (the miss must be cached)", fb.avatarCalls)
	}
}

func TestAvatarNonRasterBytesAreNoAvatar(t *testing.T) {
	fb := &fakeBridge{avatarData: []byte("<svg xmlns=\"x\"><script>alert(1)</script></svg>")}
	h, cookie := newTestHandlerWith(t, func(d *Deps) { d.Bridge = fb })

	if w := authedGet(t, h, cookie, "/api/avatar?jid=x%40s.whatsapp.net"); w.Code != http.StatusNotFound {
		t.Fatalf("non-raster avatar = %d, want 404 (never rendered, never downloaded)", w.Code)
	}
}

func TestAvatarRateCapRefusesWithoutLookup(t *testing.T) {
	fb := &fakeBridge{avatarData: pngBytes}
	h, cookie := newTestHandlerWith(t, func(d *Deps) { d.Bridge = fb })
	h.avatars.limiter = rate.NewLimiter(0, 1) // one lookup, no refill

	if w := authedGet(t, h, cookie, "/api/avatar?jid=a%40s.whatsapp.net"); w.Code != http.StatusOK {
		t.Fatalf("first avatar = %d, want 200", w.Code)
	}
	if w := authedGet(t, h, cookie, "/api/avatar?jid=b%40s.whatsapp.net"); w.Code != http.StatusTooManyRequests {
		t.Fatalf("capped avatar = %d, want 429", w.Code)
	}
	if fb.avatarCalls != 1 {
		t.Fatalf("bridge lookups = %d, want 1 (the cap must refuse before the bridge)", fb.avatarCalls)
	}

	// The cached entry stays servable while the cap is exhausted.
	if w := authedGet(t, h, cookie, "/api/avatar?jid=a%40s.whatsapp.net"); w.Code != http.StatusOK {
		t.Fatalf("cached avatar under exhausted cap = %d, want 200", w.Code)
	}
}

func TestAvatarTransientFailureIs503AndRetriesAfterTTL(t *testing.T) {
	fb := &fakeBridge{avatarErr: errors.New("boom")}
	h, cookie := newTestHandlerWith(t, func(d *Deps) { d.Bridge = fb })
	now := time.Unix(1_000_000, 0)
	h.avatars.now = func() time.Time { return now }

	if w := authedGet(t, h, cookie, "/api/avatar?jid=x%40s.whatsapp.net"); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("transient failure = %d, want 503", w.Code)
	}
	if w := authedGet(t, h, cookie, "/api/avatar?jid=x%40s.whatsapp.net"); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("second read = %d, want cached 503", w.Code)
	}
	if fb.avatarCalls != 1 {
		t.Fatalf("bridge lookups = %d, want 1 (failures cached briefly)", fb.avatarCalls)
	}

	now = now.Add(2 * time.Minute)
	fb.avatarErr, fb.avatarData = nil, pngBytes
	if w := authedGet(t, h, cookie, "/api/avatar?jid=x%40s.whatsapp.net"); w.Code != http.StatusOK {
		t.Fatalf("read after failure TTL = %d, want a fresh 200", w.Code)
	}
	if fb.avatarCalls != 2 {
		t.Fatalf("bridge lookups = %d, want 2 (failure entry expired)", fb.avatarCalls)
	}
}

func TestAvatarRequiresJID(t *testing.T) {
	h, cookie := newTestHandler(t, nil, nil)
	if w := authedGet(t, h, cookie, "/api/avatar"); w.Code != http.StatusBadRequest {
		t.Fatalf("avatar without jid = %d, want 400", w.Code)
	}
}
