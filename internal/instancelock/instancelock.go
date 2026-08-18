// Package instancelock guarantees at most one serve process per data
// directory. Two servers double-attached to the same SQLite files (and
// both believing they own the WhatsApp session) is a real observed
// failure mode: an MCP client reconnect can spawn a fresh process while
// an orphaned one is still alive.
//
// The guarantee comes from an OS-level exclusive lock on a lock file
// (flock on Unix, LockFileEx on Windows), which the operating system
// releases automatically when the holding process exits — however it
// exits — so a crashed serve never leaves a stale lock behind and no
// PID-liveness guesswork is needed.
package instancelock

import (
	"fmt"
	"os"
	"path/filepath"
)

// fileName is the lock file's name inside the data directory. The file
// itself carries no data and is never deleted — only its lock state
// matters.
const fileName = "serve.lock"

// Lock is a held single-instance lock. Release it when the process is
// done; the OS also releases it on process exit.
type Lock struct {
	f *os.File
}

// Acquire takes the data directory's exclusive lock without blocking. It
// fails when another process already holds it — the caller should refuse
// to start and surface the error as-is.
func Acquire(dataDir string) (*Lock, error) {
	path := filepath.Join(dataDir, fileName)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600) // #nosec G304 -- path is derived from the server's own data directory
	if err != nil {
		return nil, fmt.Errorf("open instance lock: %w", err)
	}

	if err := lockFile(f); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("another serve process is already running against this data directory (%s)", dataDir)
	}
	return &Lock{f: f}, nil
}

// Release drops the lock. Safe to call once; the OS would also release it
// at process exit.
func (l *Lock) Release() error {
	if l == nil || l.f == nil {
		return nil
	}
	err := unlockFile(l.f)
	closeErr := l.f.Close()
	l.f = nil
	if err != nil {
		return fmt.Errorf("release instance lock: %w", err)
	}
	if closeErr != nil {
		return fmt.Errorf("release instance lock: %w", closeErr)
	}
	return nil
}
