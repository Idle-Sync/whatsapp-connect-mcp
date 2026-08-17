// Package store manages the SQLite-backed message store (messages.db):
// opening the database with the pragmas the rest of the application relies
// on and running schema migrations at open.
package store

import (
	"database/sql"
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
