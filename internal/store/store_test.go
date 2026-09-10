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
	if err := s.conn().QueryRow(`SELECT version FROM schema_version`).Scan(&version); err != nil {
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
	if _, err := s1.conn().Exec(`DROP TABLE lid_map`); err != nil {
		t.Fatalf("drop lid_map: %v", err)
	}
	if _, err := s1.conn().Exec(`UPDATE schema_version SET version = 1`); err != nil {
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
	if err := s2.conn().QueryRow(`SELECT version FROM schema_version`).Scan(&version); err != nil {
		t.Fatalf("query schema_version: %v", err)
	}
	if want := migrations[len(migrations)-1].version; version != want {
		t.Fatalf("schema_version = %d, want %d (the latest migration)", version, want)
	}

	var rowCount int
	if err := s2.conn().QueryRow(`SELECT COUNT(*) FROM schema_version`).Scan(&rowCount); err != nil {
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
	if err := s.conn().QueryRow(`PRAGMA quick_check`).Scan(&result); err != nil {
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

	if _, err := s.conn().Exec(`INSERT INTO chats (jid) VALUES ('123@s.whatsapp.net')`); err != nil {
		t.Fatalf("insert chat: %v", err)
	}

	// messages.ts is STRICT INTEGER; binding a non-numeric TEXT value must
	// be rejected by the database rather than silently coerced or stored.
	_, err = s.conn().Exec(
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

	if _, err := s.conn().Exec(`INSERT INTO chats (jid) VALUES ('123@s.whatsapp.net')`); err != nil {
		t.Fatalf("insert chat: %v", err)
	}
	if _, err := s.conn().Exec(
		`INSERT INTO messages (chat_jid, id, sender_jid, from_me, ts, kind, text)
		 VALUES ('123@s.whatsapp.net', 'msg1', '123@s.whatsapp.net', 0, 1000, 'text', 'hello searchable world')`,
	); err != nil {
		t.Fatalf("insert message: %v", err)
	}

	var count int
	if err := s.conn().QueryRow(`SELECT COUNT(*) FROM messages_fts WHERE messages_fts MATCH 'searchable'`).Scan(&count); err != nil {
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

	if _, err := s.conn().Exec(`INSERT INTO chats (jid) VALUES ('123@s.whatsapp.net')`); err != nil {
		t.Fatalf("insert chat: %v", err)
	}
	if _, err := s.conn().Exec(
		`INSERT INTO messages (chat_jid, id, sender_jid, from_me, ts, kind, text)
		 VALUES ('123@s.whatsapp.net', 'msg1', '123@s.whatsapp.net', 0, 1000, 'text', 'original body')`,
	); err != nil {
		t.Fatalf("insert message: %v", err)
	}
	if _, err := s.conn().Exec(
		`UPDATE messages SET text = 'revised content' WHERE chat_jid = '123@s.whatsapp.net' AND id = 'msg1'`,
	); err != nil {
		t.Fatalf("update message: %v", err)
	}

	var oldCount int
	if err := s.conn().QueryRow(`SELECT COUNT(*) FROM messages_fts WHERE messages_fts MATCH 'original'`).Scan(&oldCount); err != nil {
		t.Fatalf("query messages_fts for stale term: %v", err)
	}
	if oldCount != 0 {
		t.Fatalf("messages_fts match count for stale term = %d, want 0", oldCount)
	}

	var newCount int
	if err := s.conn().QueryRow(`SELECT COUNT(*) FROM messages_fts WHERE messages_fts MATCH 'revised'`).Scan(&newCount); err != nil {
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

	if _, err := s.conn().Exec(`INSERT INTO chats (jid) VALUES ('123@s.whatsapp.net')`); err != nil {
		t.Fatalf("insert chat: %v", err)
	}
	if _, err := s.conn().Exec(
		`INSERT INTO messages (chat_jid, id, sender_jid, from_me, ts, kind, text)
		 VALUES ('123@s.whatsapp.net', 'msg1', '123@s.whatsapp.net', 0, 1000, 'text', 'ephemeral content')`,
	); err != nil {
		t.Fatalf("insert message: %v", err)
	}
	if _, err := s.conn().Exec(
		`DELETE FROM messages WHERE chat_jid = '123@s.whatsapp.net' AND id = 'msg1'`,
	); err != nil {
		t.Fatalf("delete message: %v", err)
	}

	var count int
	if err := s.conn().QueryRow(`SELECT COUNT(*) FROM messages_fts WHERE messages_fts MATCH 'ephemeral'`).Scan(&count); err != nil {
		t.Fatalf("query messages_fts: %v", err)
	}
	if count != 0 {
		t.Fatalf("messages_fts match count = %d, want 0", count)
	}
}

// Attach is the one moment a store's file changes under a running
// process: an install that started unpaired has just paired, so which
// account's messages.db these belong in is only now known.
func TestAttachSwitchesFileAndKeepsBothIntact(t *testing.T) {
	dir := t.TempDir()
	pending := filepath.Join(dir, "pending.db")
	account := filepath.Join(dir, "account.db")

	s, err := Open(pending)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = s.Close() }()

	// Seed the account file with a chat the pending store has never seen.
	seed, err := Open(account)
	if err != nil {
		t.Fatalf("Open account: %v", err)
	}
	if _, err := seed.conn().Exec(
		`INSERT INTO chats (jid, name, is_group, last_message_at) VALUES (?, ?, 0, 1)`,
		"mine@s.whatsapp.net", "Mine"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := seed.Close(); err != nil {
		t.Fatalf("close seed: %v", err)
	}

	if rows, err := s.Chats("", true, 10); err != nil || len(rows) != 0 {
		t.Fatalf("pending store started non-empty: %v, %v", rows, err)
	}

	if err := s.Attach(account); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	rows, err := s.Chats("", true, 10)
	if err != nil {
		t.Fatalf("Chats after attach: %v", err)
	}
	if len(rows) != 1 || rows[0].JID != "mine@s.whatsapp.net" {
		t.Fatalf("after attach = %+v, want the account's own chat", rows)
	}

	// Attaching what is already attached is a no-op, so the caller need not
	// track whether pairing actually changed anything.
	if err := s.Attach(account); err != nil {
		t.Fatalf("re-Attach: %v", err)
	}
	if rows, err := s.Chats("", true, 10); err != nil || len(rows) != 1 {
		t.Fatalf("re-attach disturbed the store: %v, %v", rows, err)
	}

	// The file left behind is still a usable database, not a casualty.
	back, err := Open(pending)
	if err != nil {
		t.Fatalf("reopen pending: %v", err)
	}
	defer func() { _ = back.Close() }()
	if rows, err := back.Chats("", true, 10); err != nil || len(rows) != 0 {
		t.Fatalf("pending store damaged: %v, %v", rows, err)
	}
}
