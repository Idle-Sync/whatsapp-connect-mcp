package sessiontrust

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTrustedFalseWithNoGrants(t *testing.T) {
	l := Open(t.TempDir())
	if l.Trusted("111@s.whatsapp.net") {
		t.Fatal("Trusted() = true with no grants on record")
	}
}

func TestAddGrantsAndRemoveRevokes(t *testing.T) {
	dir := t.TempDir()
	l := Open(dir)

	if err := Add(dir, "111@s.whatsapp.net"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if !l.Trusted("111@s.whatsapp.net") {
		t.Fatal("Trusted() = false after Add")
	}
	if l.Trusted("222@s.whatsapp.net") {
		t.Fatal("Trusted() = true for a JID never granted")
	}

	if err := Remove(dir, "111@s.whatsapp.net"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if l.Trusted("111@s.whatsapp.net") {
		t.Fatal("Trusted() = true after Remove")
	}
}

func TestAddIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	if err := Add(dir, "111@s.whatsapp.net"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := Add(dir, "111@s.whatsapp.net"); err != nil {
		t.Fatalf("second Add: %v", err)
	}
	jids, err := JIDs(dir)
	if err != nil {
		t.Fatalf("JIDs: %v", err)
	}
	if len(jids) != 1 || jids[0] != "111@s.whatsapp.net" {
		t.Fatalf("JIDs = %v, want exactly one entry", jids)
	}
}

func TestRemoveMissingGrantIsNoop(t *testing.T) {
	dir := t.TempDir()
	if err := Remove(dir, "111@s.whatsapp.net"); err != nil {
		t.Fatalf("Remove with no file: %v", err)
	}
}

func TestAddRejectsEmptyJID(t *testing.T) {
	if err := Add(t.TempDir(), "  "); err == nil {
		t.Fatal("Add(blank) error = nil, want error")
	}
}

func TestTrustedMatchesExactly(t *testing.T) {
	dir := t.TempDir()
	l := Open(dir)
	if err := Add(dir, "11@s.whatsapp.net"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if l.Trusted("1@s.whatsapp.net") || l.Trusted("11@s.whatsapp.net2") {
		t.Fatal("Trusted() matched a different JID by prefix/suffix")
	}
}

// ClearAtStartup is what makes a grant process-scoped: serve wipes the file
// before wiring the gate, so nothing granted during a previous session (or
// while serve was down) survives into a new one.
func TestClearAtStartupWipesGrants(t *testing.T) {
	dir := t.TempDir()
	l := Open(dir)
	if err := Add(dir, "111@s.whatsapp.net"); err != nil {
		t.Fatalf("Add: %v", err)
	}

	if err := l.ClearAtStartup(); err != nil {
		t.Fatalf("ClearAtStartup: %v", err)
	}
	if l.Trusted("111@s.whatsapp.net") {
		t.Fatal("Trusted() = true after ClearAtStartup")
	}
	if _, err := os.Stat(filepath.Join(dir, "session-trust")); !os.IsNotExist(err) {
		t.Fatalf("session-trust file still exists after ClearAtStartup (stat err = %v)", err)
	}

	// Idempotent: clearing again with no file is not an error.
	if err := l.ClearAtStartup(); err != nil {
		t.Fatalf("second ClearAtStartup: %v", err)
	}
}

// A grant added mid-session (after serve already opened its List) must be
// visible on the next Trusted call — the file is the live channel between
// the CLI and the running process, not a startup snapshot.
func TestGrantAddedAfterOpenIsSeen(t *testing.T) {
	dir := t.TempDir()
	l := Open(dir)
	_ = l.Trusted("111@s.whatsapp.net") // force any lazy state before the grant

	if err := Add(dir, "111@s.whatsapp.net"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if !l.Trusted("111@s.whatsapp.net") {
		t.Fatal("Trusted() = false for a grant added after Open")
	}
}
