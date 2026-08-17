package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/idle-sync/whatsapp-connect-mcp/internal/bridge"
	"github.com/idle-sync/whatsapp-connect-mcp/internal/clients"
	"github.com/idle-sync/whatsapp-connect-mcp/internal/config"
	"github.com/idle-sync/whatsapp-connect-mcp/internal/store"
	"github.com/idle-sync/whatsapp-connect-mcp/internal/wizard"
)

// runSetup implements the "setup" subcommand: it opens the data dir, store,
// and bridge, then hands NeedsPairing/PairQR and the client detect/inject
// functions to wizard.Run, which drives the interactive flow. Ctrl+C
// (SIGINT) or SIGTERM cancels the context the wizard reads against, which
// aborts before anything is written.
func runSetup(args []string) int {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	dataDir, err := config.Dir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "setup: %v\n", err)
		return 1
	}

	st, err := store.Open(filepath.Join(dataDir, "messages.db"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "setup: %v\n", err)
		return 1
	}
	defer func() { _ = st.Close() }()

	br, err := bridge.Open(ctx, dataDir, st)
	if err != nil {
		fmt.Fprintf(os.Stderr, "setup: %v\n", err)
		return 1
	}
	// PairQR may leave the client connected if pairing succeeds; Close
	// disconnects unconditionally, so setup never leaves a live
	// connection running after it exits.
	defer func() { _ = br.Close() }()

	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "setup: resolve home directory: %v\n", err)
		return 1
	}

	binaryPath, err := resolveBinaryPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "setup: %v\n", err)
		return 1
	}

	deps := wizard.Deps{
		NeedsPairing: br.NeedsPairing,
		PairQR:       br.PairQR,
		Detect: func() []wizard.Client {
			detected := clients.Detect(home)
			out := make([]wizard.Client, len(detected))
			for i, c := range detected {
				out[i] = wizard.Client{
					Name:       c.Name,
					ConfigPath: c.ConfigPath,
					Installed:  c.Installed,
					Injected:   c.Injected,
				}
			}
			return out
		},
		Inject:     clients.Inject,
		BinaryPath: binaryPath,
	}

	err = wizard.Run(ctx, os.Stdin, os.Stdout, deps)
	if err != nil {
		if errors.Is(err, wizard.ErrAborted) {
			fmt.Fprintln(os.Stderr, err.Error())
		} else {
			fmt.Fprintf(os.Stderr, "setup: %v\n", err)
		}
		return 1
	}
	return 0
}

// resolveBinaryPath returns the absolute path to the currently running
// binary, which is what gets written into every injected client config so
// the client invokes this exact binary.
func resolveBinaryPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve binary path: %w", err)
	}
	abs, err := filepath.Abs(exe)
	if err != nil {
		return "", fmt.Errorf("resolve binary path: %w", err)
	}
	return abs, nil
}
