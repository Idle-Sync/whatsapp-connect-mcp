package dashboard

import (
	"errors"
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/idle-sync/whatsapp-connect-mcp/internal/bridge"
)

// Avatar cache TTLs. Definitive results — a picture, or the confirmed
// absence of one — hold for a day: profile pictures change rarely and
// every refetch is live WhatsApp traffic. Transient failures hold for one
// minute so a flaky moment does not blank avatars for a day.
const (
	avatarTTL     = 24 * time.Hour
	avatarFailTTL = time.Minute
)

// avatarEntry is one cached lookup outcome: picture bytes, a definitive
// no-picture, or a transient failure.
type avatarEntry struct {
	data []byte
	none bool
	fail bool
	at   time.Time
}

// avatarCache remembers profile-picture lookups per JID and rate-caps the
// live ones — the dashboard's chat list would otherwise turn every page
// load into a burst of WhatsApp queries, which the ban-risk posture rules
// out. All state hangs off the Handler; nothing package-level.
type avatarCache struct {
	mu      sync.Mutex
	entries map[string]avatarEntry
	limiter *rate.Limiter
	now     func() time.Time
}

func newAvatarCache() *avatarCache {
	return &avatarCache{
		entries: make(map[string]avatarEntry),
		// Burst 10 covers a fresh chat list's first render; after that,
		// one live lookup per second.
		limiter: rate.NewLimiter(rate.Limit(1), 10),
		now:     time.Now,
	}
}

// get returns the cached entry for jid if it is still fresh.
func (c *avatarCache) get(jid string) (avatarEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[jid]
	if !ok {
		return avatarEntry{}, false
	}
	ttl := avatarTTL
	if e.fail {
		ttl = avatarFailTTL
	}
	if c.now().Sub(e.at) > ttl {
		delete(c.entries, jid)
		return avatarEntry{}, false
	}
	return e, true
}

func (c *avatarCache) put(jid string, e avatarEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e.at = c.now()
	c.entries[jid] = e
}

// handleAvatar serves one JID's profile picture through the cache. A miss
// costs one rate-capped live lookup; an exhausted cap refuses (the UI
// keeps its letter fallback and retries on a later page load) rather than
// queueing WhatsApp traffic. Avatar bytes are WhatsApp-supplied content:
// the same sniff-and-allowlist rule as /api/media applies, except that a
// non-raster result is simply treated as having no avatar — an avatar is
// never worth an attachment download.
func (h *Handler) handleAvatar(w http.ResponseWriter, r *http.Request) {
	jid := r.URL.Query().Get("jid")
	if jid == "" {
		h.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "jid is required"})
		return
	}

	e, ok := h.avatars.get(jid)
	if !ok {
		if !h.avatars.limiter.Allow() {
			h.writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "avatar lookups are rate-capped; retry later"})
			return
		}
		data, err := h.deps.Bridge.ProfilePicture(r.Context(), jid)
		switch {
		case errors.Is(err, bridge.ErrNoProfilePicture):
			e = avatarEntry{none: true}
		case err != nil:
			e = avatarEntry{fail: true}
		case !rasterInline[http.DetectContentType(data)]:
			e = avatarEntry{none: true}
		default:
			e = avatarEntry{data: data}
		}
		h.avatars.put(jid, e)
	}

	switch {
	case e.fail:
		h.writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "avatar lookup failed"})
	case e.none:
		h.writeJSON(w, http.StatusNotFound, map[string]string{"error": "no avatar"})
	default:
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Type", http.DetectContentType(e.data))
		// Cached client-side deliberately (unlike the UI's no-store):
		// avatars change rarely, and a chat-list refresh should not
		// re-request fifty of them.
		w.Header().Set("Cache-Control", "private, max-age=3600")
		_, _ = w.Write(e.data)
	}
}
