// Package store manages the SQLite-backed message store (messages.db):
// opening the database with the pragmas the rest of the application relies
// on and running schema migrations at open.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"sync"

	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver
)

// Store wraps the SQLite database handle backing messages.db.
//
// Every account keeps its own messages.db, so which file this handle
// points at is decided by which WhatsApp account is paired. That is not
// always known when the process starts: an unpaired install has no account
// until someone scans the QR code. Attach exists for exactly that moment —
// see its comment for why swapping the handle is safe and why the
// alternative (one shared file with an account column) is not.
//
// The handle is behind conn() rather than a bare field so a swap cannot
// race a query. The field is deliberately named dbh, not db: it is short
// enough to be typed by accident, and a missed call site must fail to
// compile rather than quietly read through a stale handle.
type Store struct {
	mu   sync.RWMutex
	dbh  *sql.DB
	path string
}

// conn returns the current database handle. Callers must not retain it
// across a possible Attach.
func (s *Store) conn() *sql.DB {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.dbh
}

// openDB opens the SQLite database at path with the pragmas the rest of
// the application relies on, and applies any pending schema migrations.
func openDB(path string) (*sql.DB, error) {
	// sqlite-specific: busy_timeout, journal_mode(WAL), and foreign_keys are
	// SQLite PRAGMAs passed as DSN options. Postgres equivalent: ordinary
	// connection/session settings (statement_timeout, the WAL-equivalent
	// durability is a server default, foreign keys are always enforced).
	dsn := fmt.Sprintf(
		"file:%s?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)",
		path,
	)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	if err := migrate(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate database: %w", err)
	}
	return db, nil
}

// Open opens (creating if necessary) the message store at path.
func Open(path string) (*Store, error) {
	db, err := openDB(path)
	if err != nil {
		return nil, err
	}
	return &Store{dbh: db, path: path}, nil
}

// Attach points this store at a different messages.db, closing the one it
// held. It exists for one moment in the process's life: an install that
// started unpaired has just finished pairing, so the account — and with it
// which file its messages belong in — is only now known.
//
// That moment is narrow by construction. An unpaired install holds no
// WhatsApp connection, so nothing is arriving to be written; the handle
// being replaced is one nothing has used. The swap takes the write lock,
// so a dashboard read that happens to land mid-swap waits rather than
// tearing, and the old handle is closed only after the new one is in
// place.
//
// Attaching the file already attached is a no-op rather than an error, so
// a caller need not track whether pairing changed anything.
func (s *Store) Attach(path string) error {
	if path == "" {
		return errors.New("attach: no path")
	}
	s.mu.RLock()
	same := s.path == path
	s.mu.RUnlock()
	if same {
		return nil
	}

	db, err := openDB(path)
	if err != nil {
		return err
	}

	s.mu.Lock()
	old := s.dbh
	s.dbh, s.path = db, path
	s.mu.Unlock()

	if old != nil {
		_ = old.Close()
	}
	return nil
}

// Close closes the underlying database handle.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dbh == nil {
		return nil
	}
	err := s.dbh.Close()
	s.dbh = nil
	return err
}

// QuickCheck runs SQLite's file-integrity pragma and reports a non-nil
// error if the database is damaged.
//
// sqlite-specific: quick_check is a SQLite file-integrity PRAGMA. Postgres
// has no client-side equivalent; page/checksum integrity there is the
// server's job (data checksums, WAL replay).
func (s *Store) QuickCheck() error {
	var result string
	if err := s.conn().QueryRow(`PRAGMA quick_check`).Scan(&result); err != nil {
		return fmt.Errorf("run integrity check: %w", err)
	}
	if result != "ok" {
		return errors.New("database integrity check failed")
	}
	return nil
}
