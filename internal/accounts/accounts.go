// Package accounts decides where one WhatsApp account's data lives inside
// the data directory, and moves a pre-0.3.8 single-account layout into it.
//
// Everything that is *about* an account — its messages, its downloaded
// media, its trust list, its readable-chat allowlist, its pending
// scheduled sends — belongs to that account and to no other. Before this
// package they shared one set of files, so pairing a second number showed
// the first number's chats, trusted its contacts, and would have fired its
// scheduled sends from the new number.
//
// The fix is separation by place rather than by predicate: each account
// gets a directory, and a query physically cannot reach another account's
// rows because they are in a different file. Nothing has to remember to
// filter, including code written long after this.
//
// What stays shared is what belongs to the machine rather than to any
// account: the outbox files are sent from, which directories a send may
// read, the rate limits, the HTTP bearer token, and the run lock.
package accounts

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver
)

// dirName is the parent directory every account directory sits under.
const dirName = "accounts"

// pendingID names the directory used before pairing, when no account is
// known yet. An unpaired install holds no WhatsApp connection, so nothing
// is written there; it exists so the store has somewhere valid to point
// while the QR code is on screen.
const pendingID = "_pending"

// ID is an account's directory name: the digits of its phone JID.
//
// The JID's user part is used rather than the whole JID because it is the
// part that identifies the account and the part that is safe as a
// filename, and because it survives re-pairing — the device suffix does
// not. Re-pairing the same number therefore lands back in the same
// directory, with its history intact, which is the whole point.
func ID(ownJID string) string {
	user := ownJID
	if at := strings.Index(user, "@"); at >= 0 {
		user = user[:at]
	}
	if colon := strings.Index(user, ":"); colon >= 0 { // strip any device suffix
		user = user[:colon]
	}
	// Anything that is not a digit cannot have come from a phone JID; refuse
	// it rather than building a path out of it.
	for _, r := range user {
		if r < '0' || r > '9' {
			return ""
		}
	}
	return user
}

// Paths locates one account's files.
type Paths struct {
	Dir string // the account's directory
}

// For returns the paths for the account whose own JID is ownJID, creating
// the directory. An empty ownJID — an install that has not paired yet —
// returns the pending directory.
func For(dataDir, ownJID string) (Paths, error) {
	id := ID(ownJID)
	if id == "" {
		id = pendingID
	}
	dir := filepath.Join(dataDir, dirName, id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Paths{}, fmt.Errorf("create account directory: %w", err)
	}
	return Paths{Dir: dir}, nil
}

// Messages is the account's message database.
func (p Paths) Messages() string { return filepath.Join(p.Dir, "messages.db") }

// Media is the directory downloaded message media is written under.
func (p Paths) Media() string { return filepath.Join(p.Dir, "media") }

// Backups is the directory backup snapshots are written to.
func (p Paths) Backups() string { return filepath.Join(p.Dir, "backups") }

// Config is the account's own settings: its trust list and its
// readable-chat allowlist. Machine-wide settings stay in the data
// directory's config.json.
func (p Paths) Config() string { return filepath.Join(p.Dir, "account.json") }

// Schedules is the account's pending scheduled sends. This one is why the
// separation is not merely tidiness: a schedule is a message that will
// actually be sent, and a shared file would fire one account's pending
// send from whichever number happened to be paired when its time came.
func (p Paths) Schedules() string { return filepath.Join(p.Dir, "schedules.json") }

// SessionTrust is the account's process-scoped trust grants.
func (p Paths) SessionTrust() string { return filepath.Join(p.Dir, "session-trust") }

// legacyNames maps a file or directory in the old flat layout to its name
// inside an account directory. config.json is absent deliberately: it is
// split rather than moved, since it holds both account and machine
// settings — see config.SplitLegacy.
var legacyNames = map[string]string{
	"messages.db":     "messages.db",
	"messages.db-wal": "messages.db-wal",
	"messages.db-shm": "messages.db-shm",
	"media":           "media",
	"backups":         "backups",
	"schedules.json":  "schedules.json",
	"session-trust":   "session-trust",
}

// ErrLegacyOccupied reports that both a pre-0.3.8 file and an account file
// of the same name exist, so adopting would have to overwrite one.
var ErrLegacyOccupied = errors.New("both a legacy and a per-account copy exist")

// AdoptLegacy moves a pre-0.3.8 flat layout into this account's directory.
//
// It is the whole migration: a rename per file, no rows rewritten. That is
// deliberate. The alternative — one shared database with an account column
// folded into each primary key — cannot be done in SQLite without
// rebuilding every table, which renumbers rowids; and rowid here is both
// the FTS index's link to its content and the cursor poll_new_messages
// hands out, so renumbering silently corrupts search results and makes
// agents skip messages they never received.
//
// Adoption runs only for a paired account: an install still showing a QR
// code has no idea whose these files are, and guessing would file one
// person's history under another's number. It is skipped, not failed, when
// there is nothing to move, so it is safe to call on every start.
func AdoptLegacy(dataDir string, p Paths) (moved []string, err error) {
	for legacy, target := range legacyNames {
		from := filepath.Join(dataDir, legacy)
		if _, err := os.Lstat(from); err != nil {
			continue // nothing to adopt
		}
		to := filepath.Join(p.Dir, target)
		if _, err := os.Lstat(to); err == nil {
			// Never overwrite: a populated account directory beside a legacy
			// file is not a case to resolve automatically.
			return moved, fmt.Errorf("%w: %s", ErrLegacyOccupied, legacy)
		}
		if err := os.Rename(from, to); err != nil {
			return moved, fmt.Errorf("adopt %s: %w", legacy, err)
		}
		moved = append(moved, legacy)
	}
	return moved, nil
}

// Current reports the JID of the account this install is paired to, or ""
// when it is not paired.
//
// It reads session.db directly rather than opening whatsmeow's session
// container. The container is not a read-only thing — it creates its
// schema on open — and this has to run before the bridge exists, precisely
// so the right message store can be opened for the account the bridge is
// about to connect. A missing file is "not paired", not an error: that is
// the state of every install before its first QR scan.
func Current(dataDir string) (string, error) {
	path := filepath.Join(dataDir, "session.db")
	if _, err := os.Stat(path); err != nil {
		return "", nil
	}
	// Opened read-only so this can never create or migrate the file. The
	// immutable flag is deliberately not set: serve may hold the same
	// database, and its WAL must still be consulted.
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return "", fmt.Errorf("open session store: %w", err)
	}
	defer func() { _ = db.Close() }()

	var jid sql.NullString
	err = db.QueryRow(`SELECT jid FROM whatsmeow_device LIMIT 1`).Scan(&jid)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", nil // the table exists but no device is paired
	case err != nil:
		// A session.db without the device table is one whatsmeow has not
		// initialised yet, which is again simply "not paired".
		if strings.Contains(err.Error(), "no such table") {
			return "", nil
		}
		return "", fmt.Errorf("read paired device: %w", err)
	}
	return jid.String, nil
}

// Root is the directory every account directory sits under. reset deletes
// it wholesale: a full wipe that left another number's messages behind
// would be a surprising thing to find afterwards.
func Root(dataDir string) string { return filepath.Join(dataDir, dirName) }
