package main

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/idle-sync/whatsapp-connect-mcp/internal/bridge"
	"github.com/idle-sync/whatsapp-connect-mcp/internal/mediapath"
	"github.com/idle-sync/whatsapp-connect-mcp/internal/store"
)

func testMCPServer() *mcp.Server {
	return mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0"}, nil)
}

// cancelledCtx returns an already-cancelled context: runHTTP and runStdio
// treat cancellation as a clean shutdown, so startup output can be observed
// synchronously without a live client.
func cancelledCtx() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func TestRunHTTPAnnouncesListeningOnceBound(t *testing.T) {
	var out bytes.Buffer
	code := runHTTP(cancelledCtx(), testMCPServer(), http.NotFoundHandler(), "127.0.0.1:0", "token", &out)
	if code != 0 {
		t.Fatalf("runHTTP returned %d on clean shutdown, want 0; output: %q", code, out.String())
	}
	if !strings.Contains(out.String(), "listening on http://127.0.0.1:") {
		t.Errorf("startup output %q does not announce the listening address", out.String())
	}
	// Asked to bind port 0, the announcement must carry the real bound
	// port, not the wildcard it was asked for.
	if strings.Contains(out.String(), "127.0.0.1:0") {
		t.Errorf("startup output %q announces port 0 instead of the bound port", out.String())
	}
}

func TestRunHTTPReportsBindFailureWithoutAnnouncing(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("occupy a port: %v", err)
	}
	defer func() { _ = ln.Close() }()

	// The context is already cancelled: the bind error must still win,
	// not be masked by the shutdown path.
	var out bytes.Buffer
	code := runHTTP(cancelledCtx(), testMCPServer(), http.NotFoundHandler(), ln.Addr().String(), "token", &out)
	if code != 1 {
		t.Errorf("runHTTP returned %d on a bind failure, want 1; output: %q", code, out.String())
	}
	if strings.Contains(out.String(), "listening") {
		t.Errorf("startup output %q announces listening despite the failed bind", out.String())
	}
	if !strings.Contains(out.String(), "serve:") {
		t.Errorf("startup output %q does not report the bind error", out.String())
	}
}

func TestRunStdioAnnouncesReady(t *testing.T) {
	var out bytes.Buffer
	done := make(chan int, 1)
	go func() { done <- runStdio(cancelledCtx(), testMCPServer(), &out) }()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("runStdio did not return on a cancelled context")
	}
	if !strings.Contains(out.String(), "ready on stdio") {
		t.Errorf("startup output %q does not announce stdio readiness", out.String())
	}
}

// TestConnectWhenPairedCancelledWhileWaiting: an unpaired bridge plus a
// cancelled context must return context.Canceled without any network
// dial — the stdio path's clean-shutdown contract.
func TestConnectWhenPairedCancelledWhileWaiting(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "messages.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer func() { _ = st.Close() }()
	ctx, cancel := context.WithCancel(context.Background())
	br, err := bridge.Open(ctx, dir, st, mediapath.Roots{})
	if err != nil {
		t.Fatalf("bridge.Open: %v", err)
	}
	defer func() { _ = br.Close() }()

	cancel()
	var buf bytes.Buffer
	if err := connectWhenPaired(ctx, br, &buf); !errors.Is(err, context.Canceled) {
		t.Fatalf("connectWhenPaired = %v, want context.Canceled", err)
	}
}

// TestRetryConnectKeepsTryingUntilSuccess: a startup connect that fails
// (DNS not up yet at login, typically) is retried with a doubling delay
// rather than abandoned, each failure reported with the wait before the
// next attempt, and the loop ends the moment an attempt succeeds.
func TestRetryConnectKeepsTryingUntilSuccess(t *testing.T) {
	attempts := 0
	connect := func(context.Context) error {
		attempts++
		if attempts < 3 {
			return errors.New("connect: WhatsApp request failed")
		}
		return nil
	}
	var out bytes.Buffer
	err := retryConnect(context.Background(), connect, &out, time.Millisecond, 2*time.Millisecond)
	if err != nil {
		t.Fatalf("retryConnect = %v, want nil once connect succeeds", err)
	}
	if attempts != 3 {
		t.Fatalf("connect called %d times, want 3", attempts)
	}
	log := out.String()
	if strings.Count(log, "retrying in") != 2 {
		t.Fatalf("output %q should report exactly the two failed attempts", log)
	}
	if !strings.Contains(log, "retrying in 1ms") || !strings.Contains(log, "retrying in 2ms") {
		t.Fatalf("output %q should name the doubling delay before each retry", log)
	}
}

// TestRetryConnectStopsOnCancel: shutdown during the backoff sleep ends the
// loop with the context error instead of one more attempt, and a connect
// that fails because the context was cancelled mid-attempt is reported as
// cancellation, not as a failure to retry.
func TestRetryConnectStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	attempts := 0
	connect := func(context.Context) error {
		attempts++
		cancel()
		return errors.New("connect: timed out")
	}
	var out bytes.Buffer
	err := retryConnect(ctx, connect, &out, time.Hour, time.Hour)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("retryConnect = %v, want context.Canceled", err)
	}
	if attempts != 1 {
		t.Fatalf("connect called %d times after cancellation, want 1", attempts)
	}
	if out.Len() != 0 {
		t.Fatalf("a cancelled attempt must not be reported as a retry; got %q", out.String())
	}
}
