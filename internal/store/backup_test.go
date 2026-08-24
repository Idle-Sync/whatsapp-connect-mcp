package store

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestBackupToProducesConsistentCopy(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(filepath.Join(dir, "messages.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = st.Close() }()

	if err := st.UpsertChat("chat@s.whatsapp.net", "Chat", false, 100); err != nil {
		t.Fatalf("UpsertChat: %v", err)
	}
	if err := st.UpsertMessage(Message{ChatJID: "chat@s.whatsapp.net", ID: "m1", SenderJID: "s@s.whatsapp.net", TS: 100, Kind: "text", Text: "hello"}); err != nil {
		t.Fatalf("UpsertMessage: %v", err)
	}

	dest := filepath.Join(dir, "backup.db")
	if err := st.BackupTo(dest); err != nil {
		t.Fatalf("BackupTo: %v", err)
	}

	copyStore, err := Open(dest)
	if err != nil {
		t.Fatalf("Open backup: %v", err)
	}
	defer func() { _ = copyStore.Close() }()
	want, _ := st.Counts()
	got, err := copyStore.Counts()
	if err != nil || got != want {
		t.Fatalf("backup Counts = %+v (err %v), want %+v", got, err, want)
	}
	if err := copyStore.QuickCheck(); err != nil {
		t.Fatalf("backup failed integrity check: %v", err)
	}

	if err := st.BackupTo(dest); err == nil {
		t.Fatal("BackupTo over an existing file should refuse")
	}
}

// TestBackupToWhileSecondConnectionWrites proves the WAL-concurrency
// claim: a backup taken while another connection to the same file is
// writing must succeed and pass integrity.
func TestBackupToWhileSecondConnectionWrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "messages.db")
	writer, err := Open(path)
	if err != nil {
		t.Fatalf("Open writer: %v", err)
	}
	defer func() { _ = writer.Close() }()
	reader, err := Open(path)
	if err != nil {
		t.Fatalf("Open reader: %v", err)
	}
	defer func() { _ = reader.Close() }()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 50; i++ {
			_ = writer.UpsertChat("c@s.whatsapp.net", "C", false, int64(i))
		}
	}()

	dest := filepath.Join(dir, "backup.db")
	if err := reader.BackupTo(dest); err != nil {
		t.Fatalf("BackupTo during concurrent writes: %v", err)
	}
	<-done
	copyStore, err := Open(dest)
	if err != nil {
		t.Fatalf("Open backup: %v", err)
	}
	defer func() { _ = copyStore.Close() }()
	if err := copyStore.QuickCheck(); err != nil {
		t.Fatalf("backup failed integrity check: %v", err)
	}
}

// TestBackupToErrorsOmitPath verifies that BackupTo errors never embed the
// destination path, which could leak filesystem structure in error messages.
func TestBackupToErrorsOmitPath(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(filepath.Join(dir, "messages.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = st.Close() }()

	// Destination in a nonexistent directory triggers VACUUM error.
	// (The database write fails because the parent directory doesn't exist.)
	dest := filepath.Join(dir, "nonexistent", "backup.db")
	err = st.BackupTo(dest)
	if err == nil {
		t.Fatal("BackupTo into a nonexistent directory should fail")
	}
	errStr := err.Error()
	if strings.Contains(errStr, dir) || strings.Contains(errStr, dest) {
		t.Fatalf("BackupTo error must not contain path; got: %v", err)
	}
}
