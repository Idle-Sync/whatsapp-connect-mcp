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
	"github.com/idle-sync/whatsapp-connect-mcp/internal/mcpserv"
	"github.com/idle-sync/whatsapp-connect-mcp/internal/store"
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
	httpAddr := fs.String("http", "", "serve streamable HTTP on this address instead of stdio")
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

	br, err := bridge.Open(ctx, dataDir, st)
	if err != nil {
		fmt.Fprintf(os.Stderr, "serve: %v\n", err)
		return 1
	}
	defer func() { _ = br.Close() }()

	if br.NeedsPairing() {
		fmt.Fprintln(os.Stderr, "not paired — run: whatsapp-connect-mcp setup")
		return 1
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

	g := gate.New(br, cfg.IsTrusted, cfg.RateBurst, cfg.RatePerSeconds, time.Now)
	doc := mcpserv.DoctorEnv{Home: home, BinaryPath: binaryPath, LoggedIn: br.LoggedIn}
	server := mcpserv.New(st, br, g, dataDir, doc)

	if *httpAddr != "" {
		return runHTTP(ctx, server, *httpAddr)
	}
	return runStdio(ctx, server)
}

// runStdio runs server over stdio until ctx is cancelled or the client
// disconnects.
func runStdio(ctx context.Context, server *mcp.Server) int {
	if err := server.Run(ctx, &mcp.StdioTransport{}); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintf(os.Stderr, "serve: %v\n", err)
		return 1
	}
	return 0
}

// runHTTP serves server over streamable HTTP on addr until ctx is
// cancelled, then shuts the HTTP server down gracefully.
func runHTTP(ctx context.Context, server *mcp.Server, addr string) int {
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	httpServer := &http.Server{
		Addr:              addr,
		Handler:           handler,
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
