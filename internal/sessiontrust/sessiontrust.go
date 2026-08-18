// Package sessiontrust implements the session-scoped confirmed-sender
// grant: a recipient the human has explicitly elevated mid-session
// auto-commits (still rate-limited) for the life of the current serve
// process, then evaporates.
//
// The grant channel is a plain file in the data directory, one JID per
// line, written only by the `trust --session` CLI command — deliberately
// not by any MCP tool, so a model cannot elevate a recipient through the
// tool surface (the same property the persistent trust list has). serve
// wipes the file before wiring the gate (ClearAtStartup), which is what
// makes a grant process-scoped: nothing granted during a previous session,
// or while serve was down, survives into a new one. Unlike the persistent
// list, which is read once at startup, this file is re-read on every
// check, so a grant takes effect immediately in the running process.
package sessiontrust

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// fileName is the grant file's name inside the data directory.
const fileName = "session-trust"

// List reads session grants for a running serve process.
type List struct {
	path string
}

// Open returns a List over dataDir's grant file. It touches nothing on
// disk.
func Open(dataDir string) *List {
	return &List{path: filepath.Join(dataDir, fileName)}
}

// ClearAtStartup removes every grant. serve calls it before wiring the
// gate so grants never outlive the process they were made for. A missing
// file is success, not an error.
func (l *List) ClearAtStartup() error {
	err := os.Remove(l.path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("clear session trust: %w", err)
	}
	return nil
}

// Trusted reports whether jid holds a session grant right now. The file is
// re-read on every call — it is tiny, sends are rate-limited to at most
// one per five seconds, and a live read is what lets a mid-session grant
// apply without any signal channel into the process.
func (l *List) Trusted(jid string) bool {
	if jid == "" {
		return false
	}
	jids, err := readGrants(l.path)
	if err != nil {
		return false
	}
	for _, g := range jids {
		if g == jid {
			return true
		}
	}
	return false
}

// Add grants jid for the currently running serve session. Idempotent.
func Add(dataDir, jid string) error {
	jid = strings.TrimSpace(jid)
	if jid == "" {
		return errors.New("session trust: a JID is required")
	}

	path := filepath.Join(dataDir, fileName)
	jids, err := readGrants(path)
	if err != nil {
		return err
	}
	for _, g := range jids {
		if g == jid {
			return nil
		}
	}
	return writeGrants(path, append(jids, jid))
}

// Remove revokes jid's session grant. Revoking a grant that does not exist
// is a no-op.
func Remove(dataDir, jid string) error {
	path := filepath.Join(dataDir, fileName)
	jids, err := readGrants(path)
	if err != nil {
		return err
	}

	kept := jids[:0]
	for _, g := range jids {
		if g != strings.TrimSpace(jid) {
			kept = append(kept, g)
		}
	}
	if len(kept) == len(jids) {
		return nil
	}
	if len(kept) == 0 {
		return Open(dataDir).ClearAtStartup()
	}
	return writeGrants(path, kept)
}

// JIDs lists the current session grants.
func JIDs(dataDir string) ([]string, error) {
	return readGrants(filepath.Join(dataDir, fileName))
}

// readGrants parses the grant file: one JID per line, blank lines ignored.
// A missing file is an empty list.
func readGrants(path string) ([]string, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- path is derived from the server's own data directory
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read session trust: %w", err)
	}

	var jids []string
	for _, line := range strings.Split(string(data), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			jids = append(jids, line)
		}
	}
	return jids, nil
}

func writeGrants(path string, jids []string) error {
	if err := os.WriteFile(path, []byte(strings.Join(jids, "\n")+"\n"), 0o600); err != nil {
		return fmt.Errorf("write session trust: %w", err)
	}
	return nil
}
