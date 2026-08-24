package dashboard

import (
	"crypto/subtle"
	"net/http"

	"github.com/idle-sync/whatsapp-connect-mcp/internal/httpauth"
)

// sessionCookie is the login session's cookie name.
const sessionCookie = "wcm_session"

// handleLogin exchanges the bearer token (as a one-time ?token= query,
// produced only by the `dashboard` CLI command) for the session cookie,
// then redirects so the token leaves the URL bar. Constant-time compare;
// a failure is a fixed 401 with no detail.
func (h *Handler) handleLogin(w http.ResponseWriter, r *http.Request) {
	got := r.URL.Query().Get("token")
	if subtle.ConstantTimeCompare([]byte(got), []byte(h.deps.Token)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	http.SetCookie(w, &http.Cookie{ // #nosec G124 -- loopback-only plain HTTP by design; Secure would drop the cookie
		Name: sessionCookie, Value: h.session,
		Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode,
	})
	http.Redirect(w, r, "/ui/", http.StatusFound)
}

// authed admits a request that carries the session cookie or the bearer
// token (curl convenience — same token, same constant-time check as MCP).
func (h *Handler) authed(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie(sessionCookie); err == nil &&
			subtle.ConstantTimeCompare([]byte(c.Value), []byte(h.session)) == 1 {
			next(w, r)
			return
		}
		if httpauth.TokenMatches(h.deps.Token, r.Header.Get("Authorization")) {
			next(w, r)
			return
		}
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}
}

// mutating additionally requires the custom header cross-origin HTML
// forms cannot set — CSRF belt to SameSite=Strict's suspenders.
func (h *Handler) mutating(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Requested-With") != "dashboard" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}
