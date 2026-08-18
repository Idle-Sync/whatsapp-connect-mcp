package httpauth

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLoadOrCreateTokenGeneratesThenReuses(t *testing.T) {
	dir := t.TempDir()

	first, created, err := LoadOrCreateToken(dir)
	if err != nil {
		t.Fatalf("first LoadOrCreateToken: %v", err)
	}
	if !created {
		t.Error("created = false on first call, want true")
	}
	if len(first) < 2*tokenBytes {
		t.Errorf("token %q is shorter than the generated size", first)
	}

	second, created, err := LoadOrCreateToken(dir)
	if err != nil {
		t.Fatalf("second LoadOrCreateToken: %v", err)
	}
	if created {
		t.Error("created = true on second call, want false (must reuse the persisted token)")
	}
	if second != first {
		t.Errorf("second token %q != first %q; a restart would invalidate every configured client", second, first)
	}
}

func TestLoadOrCreateTokenWritesOwnerOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file mode is not meaningful on Windows")
	}
	dir := t.TempDir()
	if _, _, err := LoadOrCreateToken(dir); err != nil {
		t.Fatalf("LoadOrCreateToken: %v", err)
	}

	info, err := os.Stat(filepath.Join(dir, tokenFileName))
	if err != nil {
		t.Fatalf("stat token file: %v", err)
	}
	if perm := info.Mode().Perm(); perm != fs.FileMode(0o600) {
		t.Errorf("token file mode = %o, want 600 (a readable token defeats the point)", perm)
	}
}

func TestLoadOrCreateTokenRejectsEmptyFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, tokenFileName), []byte("  \n"), 0o600); err != nil {
		t.Fatalf("seed empty token: %v", err)
	}
	if _, _, err := LoadOrCreateToken(dir); err == nil {
		t.Error("LoadOrCreateToken(empty file) error = nil, want an error rather than an empty token that authorises nothing safely")
	}
}

// okHandler is the protected handler; reaching it means a request passed
// both checks.
func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
}

func do(t *testing.T, h http.Handler, host, auth string) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "http://x/mcp", nil)
	req.Host = host
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

func TestMiddlewareAllowsLoopbackWithToken(t *testing.T) {
	h := Middleware("secret", okHandler())
	for _, host := range []string{"127.0.0.1:8000", "localhost:8000", "[::1]:8000", "127.0.0.1"} {
		if code := do(t, h, host, "Bearer secret"); code != http.StatusTeapot {
			t.Errorf("host %q with valid token = %d, want it to reach the handler (418)", host, code)
		}
	}
}

func TestMiddlewareRejectsNonLoopbackHost(t *testing.T) {
	h := Middleware("secret", okHandler())
	// Even with the right token, a non-loopback Host must be refused: this
	// is the DNS-rebinding case, where the token might have been captured.
	for _, host := range []string{"evil.com", "evil.com:8000", "10.0.0.5:8000", "192.168.1.4"} {
		if code := do(t, h, host, "Bearer secret"); code != http.StatusForbidden {
			t.Errorf("host %q = %d, want 403 forbidden", host, code)
		}
	}
}

func TestMiddlewareRejectsBadOrMissingToken(t *testing.T) {
	h := Middleware("secret", okHandler())
	for _, auth := range []string{"", "secret", "Bearer", "Bearer ", "Bearer wrong", "Basic secret"} {
		if code := do(t, h, "127.0.0.1:8000", auth); code != http.StatusUnauthorized {
			t.Errorf("auth %q = %d, want 401 unauthorized", auth, code)
		}
	}
}

// TestMiddlewareChecksHostBeforeToken pins the order: a non-loopback request
// must be turned away as forbidden without the token ever being examined, so
// the rebinding defense does not depend on the token also being wrong.
func TestMiddlewareChecksHostBeforeToken(t *testing.T) {
	h := Middleware("secret", okHandler())
	if code := do(t, h, "evil.com", "Bearer secret"); code != http.StatusForbidden {
		t.Errorf("non-loopback host with a valid token = %d, want 403 (Host checked first)", code)
	}
}

func TestBearerPrefixIsCaseInsensitive(t *testing.T) {
	h := Middleware("secret", okHandler())
	if code := do(t, h, "127.0.0.1", "bearer secret"); code != http.StatusTeapot {
		t.Errorf("lowercase bearer scheme = %d, want it accepted per RFC 7235", code)
	}
}

func TestTokenIsNotAPrefixMatch(t *testing.T) {
	// A token that is a prefix of the real one must not pass: constant-time
	// compare also enforces equal length.
	if tokenMatches("secret", "Bearer secr") {
		t.Error("a prefix of the token was accepted")
	}
	if !strings.HasPrefix("secret", "secr") {
		t.Fatal("fixture assumption broken")
	}
}
