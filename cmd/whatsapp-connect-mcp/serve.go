package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/idle-sync/whatsapp-connect-mcp/internal/bridge"
	"github.com/idle-sync/whatsapp-connect-mcp/internal/config"
	"github.com/idle-sync/whatsapp-connect-mcp/internal/gate"
	"github.com/idle-sync/whatsapp-connect-mcp/internal/httpauth"
	"github.com/idle-sync/whatsapp-connect-mcp/internal/mcpserv"
	"github.com/idle-sync/whatsapp-connect-mcp/internal/mediapath"
	"github.com/idle-sync/whatsapp-connect-mcp/internal/sessiontrust"
	"github.com/idle-sync/whatsapp-connect-mcp/internal/store"
	"github.com/idle-sync/whatsapp-connect-mcp/internal/watchdog"
)

// httpShutdownTimeout bounds how long runServe waits for in-flight HTTP
// requests to finish once a shutdown signal arrives.
const httpShutdownTimeout = 5 * time.Second

// runServe implements the "serve" subcommand: it opens the config, message
// store, and WhatsApp bridge, wires the send gate and MCP server, then runs
// the server over stdio (default) or streamable HTTP (--http addr) until
// SIGINT/SIGTERM arrives or an unrecoverable error occurs.
func runServe(args []string) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	httpAddr := fs.String("http", "", "serve streamable HTTP on this address instead of stdio "+
		"(bearer-token authenticated, loopback Host only — still bind 127.0.0.1)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	dataDir, err := config.Dir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "serve: %v\n", err)
		return 1
	}

	cfg, err := config.Load(dataDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "serve: %v\n", err)
		return 1
	}

	st, err := store.Open(filepath.Join(dataDir, "messages.db"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "serve: %v\n", err)
		return 1
	}
	defer func() { _ = st.Close() }()

	// The default outbox has to exist before the roots resolve, since
	// resolution canonicalises through the filesystem. Creating it here
	// rather than in mediapath keeps that package free of side effects, and
	// only ever creates this program's own directory — a root the user
	// configured themselves is theirs to create.
	if err := os.MkdirAll(config.DefaultMediaDir(dataDir), 0o700); err != nil {
		fmt.Fprintf(os.Stderr, "serve: create outbox directory: %v\n", err)
		return 1
	}
	mediaRoots, err := mediapath.New(cfg.MediaRoots)
	if err != nil {
		fmt.Fprintf(os.Stderr, "serve: %v\n", err)
		return 1
	}

	br, err := bridge.Open(ctx, dataDir, st, mediaRoots)
	if err != nil {
		fmt.Fprintf(os.Stderr, "serve: %v\n", err)
		return 1
	}
	defer func() { _ = br.Close() }()

	if br.NeedsPairing() {
		fmt.Fprintln(os.Stderr, "not paired — run: whatsapp-connect-mcp setup")
		return 1
	}

	// Best-effort, and deliberately before Connect so the login payload
	// carries it: a stale version still connects, so a failed lookup is
	// reported and ignored rather than held against startup.
	if err := bridge.RefreshWAVersion(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "serve: %v (continuing with the built-in version)\n", err)
	}

	// ctx is long-lived (cancelled only by the shutdown signal), not
	// request-scoped: it governs the WhatsApp connection for the life of
	// the process.
	if err := br.Connect(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "serve: %v\n", err)
		return 1
	}

	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "serve: resolve home directory: %v\n", err)
		return 1
	}
	binaryPath, err := resolveBinaryPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "serve: %v\n", err)
		return 1
	}

	// Session trust (trust --session): grants are wiped here, before the
	// gate exists, so nothing granted for a previous process — or while
	// serve was down — carries into this one. The composed predicate keeps
	// the persistent list's startup-snapshot semantics while session grants
	// are read live, which is what lets the CLI elevate a recipient in a
	// running session without a restart.
	sess := sessiontrust.Open(dataDir)
	if err := sess.ClearAtStartup(); err != nil {
		fmt.Fprintf(os.Stderr, "serve: %v\n", err)
		return 1
	}
	trusted := func(jid string) bool { return cfg.IsTrusted(jid) || sess.Trusted(jid) }

	g := gate.New(br, trusted, cfg.RateBurst, cfg.RatePerSeconds, time.Now)
	doc := mcpserv.DoctorEnv{Home: home, BinaryPath: binaryPath, NeedsPairing: br.NeedsPairing, LoggedIn: br.LoggedIn}
	server := mcpserv.New(st, br, g, dataDir, doc)

	if *httpAddr != "" {
		token, created, err := httpauth.LoadOrCreateToken(dataDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "serve: %v\n", err)
			return 1
		}
		if created {
			// Printed once, only when freshly minted, so the operator can
			// copy it into their client. On later starts it lives in the
			// token file and is never logged.
			fmt.Fprintf(os.Stderr, "serve: generated HTTP bearer token: %s\n", token)
		}
		return runHTTP(ctx, server, *httpAddr, token)
	}
	return runStdio(ctx, server)
}

// runStdio runs server over stdio until ctx is cancelled, the client
// disconnects (stdin closes), or the parent process goes away. The last of
// these is the watchdog: a client that crashes without closing the pipe
// would otherwise leave this process orphaned, still holding the WhatsApp
// connection.
func runStdio(ctx context.Context, server *mcp.Server) int {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func() {
		if watchdog.WatchParent(runCtx, os.Getppid(), watchdog.DefaultInterval, os.Getppid) {
			// Parent gone: cancel runCtx so server.Run unblocks and we exit
			// like any other clean shutdown.
			cancel()
		}
	}()

	if err := server.Run(runCtx, &mcp.StdioTransport{}); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintf(os.Stderr, "serve: %v\n", err)
		return 1
	}
	return 0
}

// runHTTP serves server over streamable HTTP on addr until ctx is
// cancelled, then shuts the HTTP server down gracefully. Every request must
// carry token and be addressed to a loopback Host; see internal/httpauth.
func runHTTP(ctx context.Context, server *mcp.Server, addr, token string) int {
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	httpServer := &http.Server{
		Addr:              addr,
		Handler:           httpauth.Middleware(token, handler),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() { errCh <- httpServer.ListenAndServe() }()

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintf(os.Stderr, "serve: %v\n", err)
			return 1
		}
		return 0
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), httpShutdownTimeout)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			fmt.Fprintf(os.Stderr, "serve: %v\n", err)
			return 1
		}
		return 0
	}
}
