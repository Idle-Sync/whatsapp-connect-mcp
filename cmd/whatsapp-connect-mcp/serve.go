package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
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
	"github.com/idle-sync/whatsapp-connect-mcp/internal/instancelock"
	"github.com/idle-sync/whatsapp-connect-mcp/internal/mcpserv"
	"github.com/idle-sync/whatsapp-connect-mcp/internal/mediapath"
	"github.com/idle-sync/whatsapp-connect-mcp/internal/schedule"
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

	// One serve per data directory, enforced before anything opens the
	// SQLite files: an MCP client reconnect can spawn a fresh process while
	// an orphaned one is still alive, and two servers double-attached to
	// the same session/message databases is a real observed failure mode.
	// The OS releases the lock on process exit, however the process dies,
	// so there is no stale-lock case.
	lock, err := instancelock.Acquire(dataDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "serve: %v\n", err)
		return 1
	}
	defer func() { _ = lock.Release() }()

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

	// Unpaired is a waiting state, not a fatal one: exiting 1 makes a
	// service manager with Restart=always hammer the binary in a crash
	// loop (issue #12). WaitForPairing re-reads the session store in place
	// — setup runs in its own process and writes the device identity into
	// session.db — so the same Bridge continues once pairing lands.
	if br.NeedsPairing() {
		fmt.Fprintln(os.Stderr, "not paired — run: whatsapp-connect-mcp setup")
		fmt.Fprintln(os.Stderr, "serve: waiting for pairing (re-checking every 15s) instead of exiting, so a service manager does not restart-loop")
		if err := br.WaitForPairing(ctx); err != nil {
			if errors.Is(err, context.Canceled) {
				return 0 // shutdown signal while waiting — a clean stop
			}
			fmt.Fprintf(os.Stderr, "serve: %v\n", err)
			return 1
		}
		fmt.Fprintln(os.Stderr, "serve: pairing detected — starting")
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

	// Trust is answered live on every check, for both forms: the
	// persistent list is re-read from config.json (so `trust --add` and
	// `--remove` apply to a running serve without a restart — issue #11),
	// and session grants (trust --session) are read from their own file,
	// wiped here before the gate exists so nothing granted for a previous
	// process carries into this one. Both stay CLI-only: no MCP tool
	// writes either file.
	sess := sessiontrust.Open(dataDir)
	if err := sess.ClearAtStartup(); err != nil {
		fmt.Fprintf(os.Stderr, "serve: %v\n", err)
		return 1
	}
	trustReader := config.NewTrustReader(dataDir)
	trusted := func(jid string) bool { return trustReader.Trusted(jid) || sess.Trusted(jid) }

	g := gate.New(br, trusted, cfg.RateBurst, cfg.RatePerSeconds, time.Now)

	// Scheduled sends. The schedule-time gate shares the trust predicate
	// with the live gate but delivers into the schedule store (that is
	// where the human's confirmation lands); the runner then fires due
	// entries through the LIVE gate's DeliverScheduled, which consumes the
	// same shared rate limiter as every other send. Schedules that came
	// due more than 15 minutes ago while serve was down are dropped, and
	// said so.
	schedStore, droppedSchedules, err := schedule.Load(dataDir, time.Now())
	if err != nil {
		fmt.Fprintf(os.Stderr, "serve: %v\n", err)
		return 1
	}
	for _, e := range droppedSchedules {
		fmt.Fprintf(os.Stderr, "serve: dropped schedule %s — its fire time passed while serve was not running\n", e.ID)
	}
	schedGate := gate.New(
		schedule.NewStoreDeliverer(schedStore, br.Validate, time.Now),
		trusted, cfg.RateBurst, cfg.RatePerSeconds, time.Now,
	)
	runner := schedule.NewRunner(schedStore, func(ctx context.Context, d gate.Delivery) error {
		_, err := g.DeliverScheduled(ctx, d)
		return err
	}, os.Stderr)
	go runner.Run(ctx)

	doc := mcpserv.DoctorEnv{
		Home: home, BinaryPath: binaryPath,
		NeedsPairing: br.NeedsPairing, LoggedIn: br.LoggedIn,
		LastEventAt: br.LastEventAt, OpenedAt: br.OpenedAt,
		IngestErrors: br.IngestErrors, LastDisconnect: br.LastDisconnect,
	}
	server := mcpserv.New(st, br, g, &mcpserv.Scheduler{Gate: schedGate, Store: schedStore}, dataDir, doc)

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
		return runHTTP(ctx, server, *httpAddr, token, os.Stderr)
	}
	return runStdio(ctx, server, os.Stderr)
}

// runStdio runs server over stdio until ctx is cancelled, the client
// disconnects (stdin closes), or the parent process goes away. The last of
// these is the watchdog: a client that crashes without closing the pipe
// would otherwise leave this process orphaned, still holding the WhatsApp
// connection. Readiness is acknowledged on errOut (never stdout, which
// carries the MCP framing) so a hand-started serve is visibly alive.
func runStdio(ctx context.Context, server *mcp.Server, errOut io.Writer) int {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func() {
		if watchdog.WatchParent(runCtx, os.Getppid(), watchdog.DefaultInterval, os.Getppid) {
			// Parent gone: cancel runCtx so server.Run unblocks and we exit
			// like any other clean shutdown.
			cancel()
		}
	}()

	_, _ = fmt.Fprintln(errOut, "serve: ready on stdio")
	if err := server.Run(runCtx, &mcp.StdioTransport{}); err != nil && !errors.Is(err, context.Canceled) {
		_, _ = fmt.Fprintf(errOut, "serve: %v\n", err)
		return 1
	}
	return 0
}

// runHTTP serves server over streamable HTTP on addr until ctx is
// cancelled, then shuts the HTTP server down gracefully. Every request must
// carry token and be addressed to a loopback Host; see internal/httpauth.
// Startup is acknowledged on errOut once the port is held — a silent start
// is indistinguishable from a hang (issue #14). errOut is never stdout in
// production, keeping stdio-transport framing clean.
func runHTTP(ctx context.Context, server *mcp.Server, addr, token string, errOut io.Writer) int {
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	httpServer := &http.Server{
		Handler:           httpauth.Middleware(token, handler),
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Bind before announcing anything: a bind failure (port already in
	// use) surfaces here, synchronously, instead of racing shutdown on
	// errCh — and "listening" is only ever printed while the port is
	// actually held. The bound address is announced rather than addr so
	// an ":0" request reports the real port.
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		_, _ = fmt.Fprintf(errOut, "serve: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprintf(errOut, "serve: listening on http://%s (streamable HTTP, bearer-token auth) — Ctrl-C to stop\n", ln.Addr())

	errCh := make(chan error, 1)
	go func() { errCh <- httpServer.Serve(ln) }()

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			_, _ = fmt.Fprintf(errOut, "serve: %v\n", err)
			return 1
		}
		return 0
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), httpShutdownTimeout)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			_, _ = fmt.Fprintf(errOut, "serve: %v\n", err)
			return 1
		}
		return 0
	}
}
