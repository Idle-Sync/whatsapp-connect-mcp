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
	"time"

	"github.com/idle-sync/whatsapp-connect-mcp/internal/bridge"
	"github.com/idle-sync/whatsapp-connect-mcp/internal/config"
	"github.com/idle-sync/whatsapp-connect-mcp/internal/instancelock"
	"github.com/idle-sync/whatsapp-connect-mcp/internal/mediapath"
	"github.com/idle-sync/whatsapp-connect-mcp/internal/store"
)

// loginWait bounds how long logout waits for the connection to
// authenticate before sending the unlink. Connecting normally takes a
// couple of seconds; a session that cannot log in within this window is
// not going to.
const loginWait = 20 * time.Second

// runLogout implements the "logout" subcommand: sign this device out on
// WhatsApp's servers, so the phone stops listing it under Linked devices.
// Local messages, settings, and client configuration stay. This is the
// counterpart to `remove`, which deletes the local session WITHOUT telling
// WhatsApp; after logout the local session resets to unpaired on its own.
func runLogout(args []string) int {
	fs := flag.NewFlagSet("logout", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	dataDir, err := config.Dir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "logout: %v\n", err)
		return 1
	}

	// Logging out needs its own live connection; doing that while a serve
	// holds the session would replace the server's stream mid-flight. The
	// instance lock makes the conflict explicit instead of racing it.
	lock, err := instancelock.Acquire(dataDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "logout: a serve process is running against this data directory —")
		fmt.Fprintln(os.Stderr, "use the dashboard's Unlink button instead, or stop the server and retry")
		return 1
	}
	defer func() { _ = lock.Release() }()

	st, err := store.Open(filepath.Join(dataDir, "messages.db"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "logout: %v\n", err)
		return 1
	}
	defer func() { _ = st.Close() }()

	br, err := bridge.Open(ctx, dataDir, st, mediapath.Roots{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "logout: %v\n", err)
		return 1
	}
	defer func() { _ = br.Close() }()

	if br.NeedsPairing() {
		fmt.Fprintln(os.Stderr, "logout: this install is not paired — nothing to unlink")
		return 1
	}

	fmt.Println("This signs the server out of your WhatsApp on WhatsApp's servers: the")
	fmt.Println("phone stops listing it under Linked devices, and pairing is required to")
	fmt.Println("use it again. Stored messages and settings are kept.")
	fmt.Print(`Type "yes" to continue: `)
	if !readConfirmYes(os.Stdin) {
		fmt.Println("aborted — nothing was changed")
		return 1
	}

	if err := bridge.RefreshWAVersion(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "logout: %v (continuing with the built-in version)\n", err)
	}
	if err := br.Connect(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "logout: %v\n", err)
		return 1
	}

	// Logout is an authenticated request; wait for the login handshake to
	// finish rather than firing it into a still-connecting socket.
	deadline := time.Now().Add(loginWait)
	for !br.LoggedIn() {
		if time.Now().After(deadline) {
			fmt.Fprintln(os.Stderr, "logout: could not authenticate within 20s — check the connection and retry")
			return 1
		}
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.Canceled) {
				fmt.Println("aborted — nothing was changed")
				return 0
			}
			return 1
		case <-time.After(250 * time.Millisecond):
		}
	}

	if err := br.Logout(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "logout: %v\n", err)
		return 1
	}
	fmt.Println("logged out — WhatsApp no longer lists this device; run `whatsapp-connect-mcp setup`")
	fmt.Println("(or the dashboard's Pair tab) to pair again")
	return 0
}
