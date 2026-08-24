package store

import (
	"errors"
	"os"
)

// BackupTo writes a consistent snapshot of the open database to path,
// safely even while another process is writing (the snapshot is one read
// transaction under WAL, and the DSN's busy_timeout covers lock waits).
// The destination must not already exist: refusing to overwrite is what
// keeps a mistyped --dest from destroying a previous backup.
//
// sqlite-specific: VACUUM INTO (SQLite >= 3.27). Postgres equivalent:
// pg_dump / pg_basebackup.
func (s *Store) BackupTo(path string) error {
	if _, err := os.Stat(path); err == nil {
		return errors.New("backup destination already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("backup destination could not be checked")
	}
	if _, err := s.db.Exec(`VACUUM INTO ?`, path); err != nil {
		return errors.New("back up database: write failed — check free disk space and that the destination directory exists")
	}
	return nil
}
