package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/idle-sync/whatsapp-connect-mcp/internal/accounts"
	"github.com/idle-sync/whatsapp-connect-mcp/internal/config"
)

// prepareAccount resolves where the paired account's data lives, moving a
// pre-0.3.8 flat layout into place the first time it runs.
//
// Adoption only happens for a paired install. An install still showing a
// QR code does not know whose the legacy files are, and filing one
// person's history under another's number is not a mistake that can be
// undone afterwards — so the pending directory is used and the legacy
// files are left exactly where they are until pairing says who they
// belong to.
//
// What moved is printed rather than done silently: someone whose data
// directory changed shape between two runs should be able to read why.
func prepareAccount(dataDir, ownJID string, out io.Writer) (accounts.Paths, error) {
	acct, err := accounts.For(dataDir, ownJID)
	if err != nil {
		return accounts.Paths{}, err
	}
	if ownJID == "" {
		return acct, nil
	}

	moved, err := accounts.AdoptLegacy(dataDir, acct)
	if err != nil {
		return accounts.Paths{}, err
	}
	if len(moved) > 0 {
		_, _ = fmt.Fprintf(out, "serve: moved %s into this account's own directory — each paired number now keeps its messages, trust list, readable chats and schedules separately\n",
			strings.Join(moved, ", "))
	}

	split, err := config.SplitLegacy(dataDir, acct.Dir)
	if err != nil {
		return accounts.Paths{}, err
	}
	if split {
		_, _ = fmt.Fprintln(out, "serve: moved the trust list and readable-chat settings into this account's own settings file; rate limits and outbox roots stay shared")
	}
	return acct, nil
}

// attachAccount re-points everything that is account-scoped once pairing
// has just told us which account this is.
//
// This is the one moment the store's file can change under a running
// process, and it is safe precisely because of when it happens: before
// pairing there is no WhatsApp connection, so nothing has been written to
// the pending store that could be stranded by the swap.
func attachAccount(dataDir, ownJID string, st attachable, out io.Writer) error {
	if ownJID == "" {
		return nil
	}
	acct, err := prepareAccount(dataDir, ownJID, out)
	if err != nil {
		return err
	}
	return st.Attach(acct.Messages())
}

// attachable is the slice of *store.Store this needs, kept narrow so the
// pairing path can be tested without a database.
type attachable interface {
	Attach(path string) error
}

// cliAccount resolves the paired account's paths for a short-lived
// command, adopting a pre-0.3.8 layout if that has not happened yet.
//
// Adopting here as well as in serve is deliberate. serve adopts before it
// opens anything, so a running server has already done it and this finds
// nothing left to move; and a `trust --list` run after upgrading but
// before the next serve would otherwise read an account file that does not
// exist yet. Each file moves with one rename, so two processes racing can
// only mean one of them finds the file already gone.
func cliAccount(dataDir string, out io.Writer) (accounts.Paths, error) {
	ownJID, err := accounts.Current(dataDir)
	if err != nil {
		return accounts.Paths{}, err
	}
	return prepareAccount(dataDir, ownJID, out)
}
