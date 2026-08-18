// Package httpauth guards the optional streamable-HTTP transport.
//
// The stdio transport needs no authentication: it speaks to exactly one
// parent process over a pipe. The HTTP transport is different — anything
// that can open a socket to the bound address can drive every tool,
// including the send tools. This package supplies the two controls that
// close that: a bearer token every request must carry, and a Host-header
// check that refuses requests not addressed to a loopback name, which is
// what stops a web page in the operator's browser from reaching the server
// via DNS rebinding.
package httpauth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// tokenFileName is the file the bearer token is persisted in, under the
// data directory, with owner-only permissions.
const tokenFileName = ".http-token"

// tokenBytes is the size of the generated token before hex encoding. 32
// bytes is 256 bits, well past any brute-force concern for a value that
// never leaves the local machine.
const tokenBytes = 32

// LoadOrCreateToken returns the bearer token for the HTTP transport,
// generating and persisting one the first time it is needed. created
// reports whether this call minted a new token, so the caller can print it
// once for the operator to copy rather than logging a secret on every boot.
//
// The token file is written with mode 0600. A pre-existing file with looser
// permissions is left as the operator set it — tightening it silently could
// mask a deliberate choice — but its contents are trusted as-is.
func LoadOrCreateToken(dir string) (token string, created bool, err error) {
	path := filepath.Join(dir, tokenFileName)

	data, err := os.ReadFile(path) // #nosec G304 -- dir is the program's own data directory
	switch {
	case err == nil:
		t := strings.TrimSpace(string(data))
		if t == "" {
			return "", false, errors.New("http token file is empty; delete it to have a new one generated")
		}
		return t, false, nil
	case !errors.Is(err, os.ErrNotExist):
		return "", false, fmt.Errorf("read http token: %w", err)
	}

	buf := make([]byte, tokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", false, fmt.Errorf("generate http token: %w", err)
	}
	t := hex.EncodeToString(buf)

	if err := os.WriteFile(path, []byte(t+"\n"), 0o600); err != nil {
		return "", false, fmt.Errorf("write http token: %w", err)
	}
	return t, true, nil
}

// Middleware wraps next so that every request must both be addressed to a
// loopback Host and carry the bearer token. A request failing either is
// answered before it reaches next, so an unauthenticated caller cannot so
// much as start a tool call.
func Middleware(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The Host check comes first, and deliberately so: it is what
		// defends against a browser being tricked into sending the token to
		// a rebind of the operator's own site. It costs nothing and its
		// failure is not about credentials, so it need not be constant-time.
		if !isLoopbackHost(r.Host) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if !tokenMatches(token, r.Header.Get("Authorization")) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// isLoopbackHost reports whether host (an HTTP Host header, possibly with a
// port) names the loopback interface. "localhost" and any loopback IP
// literal qualify; a public name or address does not, which is the whole
// point — a DNS rebind resolves a public name to a loopback address, but
// the Host header the browser sends still carries that public name.
func isLoopbackHost(host string) bool {
	if host == "" {
		return false
	}
	name := host
	if h, _, err := net.SplitHostPort(host); err == nil {
		name = h
	}
	if strings.EqualFold(name, "localhost") {
		return true
	}
	ip := net.ParseIP(name)
	return ip != nil && ip.IsLoopback()
}

// tokenMatches reports whether authHeader is a Bearer credential equal to
// want. The comparison is constant-time so that a caller cannot learn the
// token a byte at a time from response timing.
func tokenMatches(want, authHeader string) bool {
	const prefix = "Bearer "
	if len(authHeader) <= len(prefix) || !strings.EqualFold(authHeader[:len(prefix)], prefix) {
		return false
	}
	got := authHeader[len(prefix):]
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}
