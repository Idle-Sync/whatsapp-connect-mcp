package store

import (
	"path/filepath"
	"testing"
)

func TestOpenCreatesSchemaVersion1(t *testing.T) {
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
	if version != 1 {
		t.Fatalf("schema_version = %d, want 1", version)
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
	if version != 1 {
		t.Fatalf("schema_version = %d, want 1", version)
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

	var result string
	if err := s.db.QueryRow(`PRAGMA quick_check`).Scan(&result); err != nil {
		t.Fatalf("quick_check: %v", err)
	}
	if result != "ok" {
		t.Fatalf("quick_check = %q, want ok", result)
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
