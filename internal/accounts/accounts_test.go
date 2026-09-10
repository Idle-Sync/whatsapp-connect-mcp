package accounts

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The directory name is the number, so re-pairing the same number lands
// back on the same history — the device suffix must not leak into it.
func TestIDIsTheNumberWithoutTheDevice(t *testing.T) {
	for in, want := range map[string]string{
		"918100466743@s.whatsapp.net":    "918100466743",
		"918100466743:12@s.whatsapp.net": "918100466743",
		"918100466743":                   "918100466743",
		"":                               "",
		"../../etc/passwd":               "",
		"nonsense@lid":                   "",
		"91810046674a@s.whatsapp.net":    "",
	} {
		if got := ID(in); got != want {
			t.Errorf("ID(%q) = %q, want %q", in, got, want)
		}
	}
}

// A path can never be built out of something that did not come from a
// phone JID, however the caller got hold of it.
func TestForRefusesToBuildPathsFromJunk(t *testing.T) {
	dir := t.TempDir()
	p, err := For(dir, "../../escape")
	if err != nil {
		t.Fatalf("For: %v", err)
	}
	if !strings.HasPrefix(filepath.Clean(p.Dir), filepath.Clean(dir)) {
		t.Fatalf("escaped the data directory: %s", p.Dir)
	}
	if filepath.Base(p.Dir) != pendingID {
		t.Errorf("junk did not fall back to the pending directory: %s", p.Dir)
	}
}

// Two accounts get two directories, which is the whole mechanism: one
// account's queries cannot reach another's rows because they are not in
// the same file.
func TestSeparateAccountsSeparateFiles(t *testing.T) {
	dir := t.TempDir()
	a, err := For(dir, "111@s.whatsapp.net")
	if err != nil {
		t.Fatalf("For: %v", err)
	}
	b, err := For(dir, "222@s.whatsapp.net")
	if err != nil {
		t.Fatalf("For: %v", err)
	}
	if a.Messages() == b.Messages() {
		t.Fatal("two accounts share one message database")
	}
	for _, pair := range [][2]string{
		{a.Config(), b.Config()},
		{a.Schedules(), b.Schedules()},
		{a.SessionTrust(), b.SessionTrust()},
		{a.Media(), b.Media()},
		{a.Backups(), b.Backups()},
	} {
		if pair[0] == pair[1] {
			t.Errorf("shared path between accounts: %s", pair[0])
		}
	}
	// Re-pairing the same number must return to the same directory.
	again, err := For(dir, "111:9@s.whatsapp.net")
	if err != nil {
		t.Fatalf("For: %v", err)
	}
	if again.Dir != a.Dir {
		t.Errorf("re-pair landed elsewhere: %s vs %s", again.Dir, a.Dir)
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Adoption moves the old flat layout in, without rewriting anything.
func TestAdoptLegacyMovesEverythingOnce(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "messages.db"), "db")
	writeFile(t, filepath.Join(dir, "messages.db-wal"), "wal")
	writeFile(t, filepath.Join(dir, "schedules.json"), "[]")
	writeFile(t, filepath.Join(dir, "session-trust"), "x@s.whatsapp.net\n")
	writeFile(t, filepath.Join(dir, "media", "chat", "a.jpg"), "img")

	p, err := For(dir, "111@s.whatsapp.net")
	if err != nil {
		t.Fatalf("For: %v", err)
	}
	moved, err := AdoptLegacy(dir, p)
	if err != nil {
		t.Fatalf("AdoptLegacy: %v", err)
	}
	if len(moved) != 5 {
		t.Fatalf("moved %v, want 5 entries", moved)
	}

	// The bytes are the same bytes: this is a rename, not a rewrite.
	got, err := os.ReadFile(p.Messages())
	if err != nil || string(got) != "db" {
		t.Fatalf("messages.db = %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(p.Media(), "chat", "a.jpg")); err != nil {
		t.Errorf("media did not come across: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "messages.db")); !os.IsNotExist(err) {
		t.Error("legacy messages.db still in place")
	}

	// Running again finds nothing left to do, so start-up can call it
	// unconditionally.
	moved, err = AdoptLegacy(dir, p)
	if err != nil || len(moved) != 0 {
		t.Fatalf("second adopt = %v, %v; want nothing", moved, err)
	}
}

// Never overwrite: a populated account directory beside a legacy file is
// not a situation to resolve by guessing which copy is wanted.
func TestAdoptLegacyRefusesToOverwrite(t *testing.T) {
	dir := t.TempDir()
	p, err := For(dir, "111@s.whatsapp.net")
	if err != nil {
		t.Fatalf("For: %v", err)
	}
	writeFile(t, filepath.Join(dir, "messages.db"), "legacy")
	writeFile(t, p.Messages(), "already here")

	if _, err := AdoptLegacy(dir, p); !errors.Is(err, ErrLegacyOccupied) {
		t.Fatalf("AdoptLegacy err = %v, want ErrLegacyOccupied", err)
	}
	got, _ := os.ReadFile(p.Messages())
	if string(got) != "already here" {
		t.Error("the existing account file was overwritten")
	}
}

// An install with no session.db has simply never paired — that is not an
// error, it is every install before its first QR scan.
func TestCurrentUnpaired(t *testing.T) {
	got, err := Current(t.TempDir())
	if err != nil || got != "" {
		t.Fatalf("Current = %q, %v; want empty and no error", got, err)
	}
}

// A session.db that is not whatsmeow's (or not initialised yet) reads as
// unpaired rather than failing start-up.
func TestCurrentIgnoresAnUninitialisedSession(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "session.db"), "")
	got, err := Current(dir)
	if err != nil || got != "" {
		t.Fatalf("Current = %q, %v; want empty and no error", got, err)
	}
}
