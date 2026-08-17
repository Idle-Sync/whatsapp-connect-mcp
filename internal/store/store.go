// Package store manages the SQLite-backed message store (messages.db):
// opening the database with the pragmas the rest of the application relies
// on and running schema migrations at open.
package store

import (
	"database/sql"
	"errors"
	"fmt"

	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver
)

// Store wraps the SQLite database handle backing messages.db.
type Store struct {
	db *sql.DB
}

// Open opens (creating if necessary) the SQLite database at path with WAL
// journaling, a 10s busy timeout, and foreign keys enabled, then applies
// any pending schema migrations.
func Open(path string) (*Store, error) {
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

	return &Store{db: db}, nil
}

// Close closes the underlying database handle.
func (s *Store) Close() error {
	return s.db.Close()
}

// QuickCheck runs SQLite's file-integrity pragma and reports a non-nil
// error if the database is damaged.
//
// sqlite-specific: quick_check is a SQLite file-integrity PRAGMA. Postgres
// has no client-side equivalent; page/checksum integrity there is the
// server's job (data checksums, WAL replay).
func (s *Store) QuickCheck() error {
	var result string
	if err := s.db.QueryRow(`PRAGMA quick_check`).Scan(&result); err != nil {
		return fmt.Errorf("run integrity check: %w", err)
	}
	if result != "ok" {
		return errors.New("database integrity check failed")
	}
	return nil
}
