package store

import (
	"path/filepath"
	"testing"
)

func TestOpenCreatesLatestSchemaVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "messages.db")

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	defer func() { _ = s.Close() }()

	var version int
	if err := s.db.QueryRow(`SELECT version FROM schema_version`).Scan(&version); err != nil {
		t.Fatalf("query schema_version: %v", err)
	}
	if want := migrations[len(migrations)-1].version; version != want {
		t.Fatalf("schema_version = %d, want %d (the latest migration)", version, want)
	}
}

// A database created before a migration existed must be upgraded in place
// on the next Open, not just fresh databases.
func TestOpenUpgradesOlderSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "messages.db")

	s1, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	// Rewind to schema version 1 by undoing migration 2 by hand.
	if _, err := s1.db.Exec(`DROP TABLE lid_map`); err != nil {
		t.Fatalf("drop lid_map: %v", err)
	}
	if _, err := s1.db.Exec(`UPDATE schema_version SET version = 1`); err != nil {
		t.Fatalf("rewind version: %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	s2, err := Open(path)
	if err != nil {
		t.Fatalf("re-Open() error: %v", err)
	}
	defer func() { _ = s2.Close() }()
	if err := s2.UpsertLIDMapping("1@lid", "1@s.whatsapp.net"); err != nil {
		t.Fatalf("lid_map not recreated by migration: %v", err)
	}
}

func TestOpenIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "messages.db")

	s1, err := Open(path)
	if err != nil {
		t.Fatalf("first Open() error: %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("close first handle: %v", err)
	}

	s2, err := Open(path)
	if err != nil {
		t.Fatalf("second Open() error: %v", err)
	}
	defer func() { _ = s2.Close() }()

	var version int
	if err := s2.db.QueryRow(`SELECT version FROM schema_version`).Scan(&version); err != nil {
		t.Fatalf("query schema_version: %v", err)
	}
	if want := migrations[len(migrations)-1].version; version != want {
		t.Fatalf("schema_version = %d, want %d (the latest migration)", version, want)
	}

	var rowCount int
	if err := s2.db.QueryRow(`SELECT COUNT(*) FROM schema_version`).Scan(&rowCount); err != nil {
		t.Fatalf("count schema_version rows: %v", err)
	}
	if rowCount != 1 {
		t.Fatalf("schema_version row count = %d, want 1", rowCount)
	}
}

func TestOpenPassesIntegrityCheck(t *testing.T) {
	path := filepath.Join(t.TempDir(), "messages.db")

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	defer func() { _ = s.Close() }()

	// sqlite-specific: quick_check is a SQLite file-integrity PRAGMA.
	// Postgres has no equivalent client-side check; page/checksum
	// integrity there is the server's job (data checksums, WAL replay).
	var result string
	if err := s.db.QueryRow(`PRAGMA quick_check`).Scan(&result); err != nil {
		t.Fatalf("quick_check: %v", err)
	}
	if result != "ok" {
		t.Fatalf("quick_check = %q, want ok", result)
	}
}

func TestStrictTableRejectsWrongTypedValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "messages.db")

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	defer func() { _ = s.Close() }()

	if _, err := s.db.Exec(`INSERT INTO chats (jid) VALUES ('123@s.whatsapp.net')`); err != nil {
		t.Fatalf("insert chat: %v", err)
	}

	// messages.ts is STRICT INTEGER; binding a non-numeric TEXT value must
	// be rejected by the database rather than silently coerced or stored.
	_, err = s.db.Exec(
		`INSERT INTO messages (chat_jid, id, sender_jid, from_me, ts, kind, text)
		 VALUES ('123@s.whatsapp.net', 'msg1', '123@s.whatsapp.net', 0, 'not-a-timestamp', 'text', 'hi')`,
	)
	if err == nil {
		t.Fatal("insert with TEXT value for INTEGER column ts succeeded, want STRICT type error")
	}
}

func TestMessageInsertIsSearchableViaFTS(t *testing.T) {
	path := filepath.Join(t.TempDir(), "messages.db")

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	defer func() { _ = s.Close() }()

	if _, err := s.db.Exec(`INSERT INTO chats (jid) VALUES ('123@s.whatsapp.net')`); err != nil {
		t.Fatalf("insert chat: %v", err)
	}
	if _, err := s.db.Exec(
		`INSERT INTO messages (chat_jid, id, sender_jid, from_me, ts, kind, text)
		 VALUES ('123@s.whatsapp.net', 'msg1', '123@s.whatsapp.net', 0, 1000, 'text', 'hello searchable world')`,
	); err != nil {
		t.Fatalf("insert message: %v", err)
	}

	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM messages_fts WHERE messages_fts MATCH 'searchable'`).Scan(&count); err != nil {
		t.Fatalf("query messages_fts: %v", err)
	}
	if count != 1 {
		t.Fatalf("messages_fts match count = %d, want 1", count)
	}
}

func TestMessageUpdateRefreshesFTS(t *testing.T) {
	path := filepath.Join(t.TempDir(), "messages.db")

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	defer func() { _ = s.Close() }()

	if _, err := s.db.Exec(`INSERT INTO chats (jid) VALUES ('123@s.whatsapp.net')`); err != nil {
		t.Fatalf("insert chat: %v", err)
	}
	if _, err := s.db.Exec(
		`INSERT INTO messages (chat_jid, id, sender_jid, from_me, ts, kind, text)
		 VALUES ('123@s.whatsapp.net', 'msg1', '123@s.whatsapp.net', 0, 1000, 'text', 'original body')`,
	); err != nil {
		t.Fatalf("insert message: %v", err)
	}
	if _, err := s.db.Exec(
		`UPDATE messages SET text = 'revised content' WHERE chat_jid = '123@s.whatsapp.net' AND id = 'msg1'`,
	); err != nil {
		t.Fatalf("update message: %v", err)
	}

	var oldCount int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM messages_fts WHERE messages_fts MATCH 'original'`).Scan(&oldCount); err != nil {
		t.Fatalf("query messages_fts for stale term: %v", err)
	}
	if oldCount != 0 {
		t.Fatalf("messages_fts match count for stale term = %d, want 0", oldCount)
	}

	var newCount int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM messages_fts WHERE messages_fts MATCH 'revised'`).Scan(&newCount); err != nil {
		t.Fatalf("query messages_fts for new term: %v", err)
	}
	if newCount != 1 {
		t.Fatalf("messages_fts match count for new term = %d, want 1", newCount)
	}
}

func TestMessageDeleteRemovesFromFTS(t *testing.T) {
	path := filepath.Join(t.TempDir(), "messages.db")

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	defer func() { _ = s.Close() }()

	if _, err := s.db.Exec(`INSERT INTO chats (jid) VALUES ('123@s.whatsapp.net')`); err != nil {
		t.Fatalf("insert chat: %v", err)
	}
	if _, err := s.db.Exec(
		`INSERT INTO messages (chat_jid, id, sender_jid, from_me, ts, kind, text)
		 VALUES ('123@s.whatsapp.net', 'msg1', '123@s.whatsapp.net', 0, 1000, 'text', 'ephemeral content')`,
	); err != nil {
		t.Fatalf("insert message: %v", err)
	}
	if _, err := s.db.Exec(
		`DELETE FROM messages WHERE chat_jid = '123@s.whatsapp.net' AND id = 'msg1'`,
	); err != nil {
		t.Fatalf("delete message: %v", err)
	}

	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM messages_fts WHERE messages_fts MATCH 'ephemeral'`).Scan(&count); err != nil {
		t.Fatalf("query messages_fts: %v", err)
	}
	if count != 0 {
		t.Fatalf("messages_fts match count = %d, want 0", count)
	}
}
