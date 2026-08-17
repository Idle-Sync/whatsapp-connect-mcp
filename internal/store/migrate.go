package store

import (
	"database/sql"
	"fmt"
)

// migration is one numbered schema change applied in order at open.
type migration struct {
	version int
	sql     string
}

// migrations lists every schema migration in ascending version order.
// schemaVersion tracks how many have been applied.
var migrations = []migration{
	{version: 1, sql: migration1SQL},
}

const migration1SQL = `
CREATE TABLE chats (
  jid TEXT PRIMARY KEY, name TEXT NOT NULL DEFAULT '',
  is_group INTEGER NOT NULL DEFAULT 0, archived INTEGER NOT NULL DEFAULT 0,
  last_message_at INTEGER NOT NULL DEFAULT 0) STRICT;
CREATE TABLE messages (
  chat_jid TEXT NOT NULL, id TEXT NOT NULL, sender_jid TEXT NOT NULL,
  from_me INTEGER NOT NULL, ts INTEGER NOT NULL,
  kind TEXT NOT NULL,               -- text|image|video|audio|voice|document|sticker|reaction|other
  text TEXT NOT NULL DEFAULT '',    -- body or caption
  quoted_id TEXT NOT NULL DEFAULT '',
  media_ref BLOB,                   -- whatsmeow download keys, NULL for text
  media_filename TEXT NOT NULL DEFAULT '',
  read_at INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (chat_jid, id)) STRICT;
CREATE INDEX idx_messages_chat_ts ON messages (chat_jid, ts);
CREATE TABLE contacts (
  jid TEXT PRIMARY KEY, phone TEXT NOT NULL DEFAULT '',
  push_name TEXT NOT NULL DEFAULT '', full_name TEXT NOT NULL DEFAULT '',
  business_name TEXT NOT NULL DEFAULT '') STRICT;
CREATE TABLE calls (
  id TEXT PRIMARY KEY, peer_jid TEXT NOT NULL, ts INTEGER NOT NULL,
  direction TEXT NOT NULL,          -- incoming|outgoing
  status TEXT NOT NULL,             -- missed|answered|rejected|unknown
  is_video INTEGER NOT NULL DEFAULT 0) STRICT;
-- sqlite-specific: FTS5 virtual table; STRICT does not apply to virtual
-- tables. Postgres equivalent is a tsvector column with a GIN index.
CREATE VIRTUAL TABLE messages_fts USING fts5(
  text, content='messages', content_rowid='rowid');
CREATE TABLE schema_version (version INTEGER NOT NULL) STRICT;
-- sqlite-specific: these triggers key the FTS index off messages' rowid
-- (content_rowid='rowid' above), which is how a content-linked FTS5 table
-- locates its source row. Postgres equivalent: triggers that update the
-- tsvector column directly, keyed off the primary key (chat_jid, id)
-- rather than a rowid.
CREATE TRIGGER messages_ai AFTER INSERT ON messages BEGIN
  INSERT INTO messages_fts(rowid, text) VALUES (new.rowid, new.text);
END;
CREATE TRIGGER messages_ad AFTER DELETE ON messages BEGIN
  INSERT INTO messages_fts(messages_fts, rowid, text) VALUES ('delete', old.rowid, old.text);
END;
CREATE TRIGGER messages_au AFTER UPDATE ON messages BEGIN
  INSERT INTO messages_fts(messages_fts, rowid, text) VALUES ('delete', old.rowid, old.text);
  INSERT INTO messages_fts(rowid, text) VALUES (new.rowid, new.text);
END;
`

// migrate brings db up to the latest known schema version, applying any
// pending numbered migrations each inside its own transaction.
func migrate(db *sql.DB) error {
	version, err := schemaVersion(db)
	if err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}

	for _, m := range migrations {
		if m.version <= version {
			continue
		}
		if err := applyMigration(db, m); err != nil {
			return fmt.Errorf("apply migration %d: %w", m.version, err)
		}
	}
	return nil
}

// schemaVersion returns the database's current schema version, or 0 if it
// has not been migrated yet.
func schemaVersion(db *sql.DB) (int, error) {
	var exists bool
	err := db.QueryRow(
		`SELECT EXISTS (SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'schema_version')`,
	).Scan(&exists)
	if err != nil {
		return 0, err
	}
	if !exists {
		return 0, nil
	}

	var version int
	if err := db.QueryRow(`SELECT version FROM schema_version LIMIT 1`).Scan(&version); err != nil {
		return 0, err
	}
	return version, nil
}

// applyMigration runs m's schema statements and records the new version,
// all inside one transaction.
func applyMigration(db *sql.DB, m migration) (err error) {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	if _, err = tx.Exec(m.sql); err != nil {
		return err
	}

	if m.version == 1 {
		_, err = tx.Exec(`INSERT INTO schema_version (version) VALUES (?)`, m.version)
	} else {
		_, err = tx.Exec(`UPDATE schema_version SET version = ?`, m.version)
	}
	if err != nil {
		return err
	}

	return tx.Commit()
}
