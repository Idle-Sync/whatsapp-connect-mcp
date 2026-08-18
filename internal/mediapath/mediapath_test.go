package mediapath

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// newRoots builds a Roots over dir, failing the test rather than returning
// an error, since every test here needs a working one.
func newRoots(t *testing.T, dirs ...string) Roots {
	t.Helper()
	r, err := New(dirs)
	if err != nil {
		t.Fatalf("New(%v): %v", dirs, err)
	}
	return r
}

// writeFile creates a file with some content and returns its path.
func writeFile(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	return p
}

func TestAllowsFileInsideRoot(t *testing.T) {
	dir := t.TempDir()
	f := writeFile(t, dir, "photo.jpg")

	if err := newRoots(t, dir).Allows(f); err != nil {
		t.Errorf("Allows(file in root) = %v, want nil", err)
	}
}

func TestAllowsFileInNestedSubdirectory(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "a", "b")
	if err := os.MkdirAll(sub, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	f := writeFile(t, sub, "deep.pdf")

	if err := newRoots(t, dir).Allows(f); err != nil {
		t.Errorf("Allows(nested file) = %v, want nil", err)
	}
}

func TestRejectsFileOutsideRoot(t *testing.T) {
	root := t.TempDir()
	other := t.TempDir()
	secret := writeFile(t, other, "id_rsa")

	if err := newRoots(t, root).Allows(secret); !errors.Is(err, ErrOutsideRoots) {
		t.Errorf("Allows(file outside root) = %v, want ErrOutsideRoots", err)
	}
}

// TestRejectsSiblingSharingAPrefix is the case a naive strings.HasPrefix
// check would wave through: "<root>-stolen" starts with "<root>" as a
// string but is a different directory entirely.
func TestRejectsSiblingSharingAPrefix(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "outbox")
	sibling := root + "-stolen"
	for _, d := range []string{root, sibling} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	f := writeFile(t, sibling, "loot.txt")

	if err := newRoots(t, root).Allows(f); !errors.Is(err, ErrOutsideRoots) {
		t.Errorf("Allows(%q with root %q) = %v, want ErrOutsideRoots", f, root, err)
	}
}

// TestRejectsTraversalOutOfRoot covers the plainest attack: a relative path
// that climbs out of an allowed directory.
func TestRejectsTraversalOutOfRoot(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "outbox")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	secret := writeFile(t, base, "secret.txt")
	traversal := filepath.Join(root, "..", "secret.txt")

	if err := newRoots(t, root).Allows(traversal); !errors.Is(err, ErrOutsideRoots) {
		t.Errorf("Allows(traversal) = %v, want ErrOutsideRoots", err)
	}
	// Sanity: the traversal really does name the secret file.
	if _, err := os.Stat(secret); err != nil {
		t.Fatalf("fixture missing: %v", err)
	}
}

// TestRejectsSymlinkEscapingRoot is the reason paths are resolved rather
// than merely cleaned: a link sitting inside an allowed directory that
// points outside it must be judged by its target.
func TestRejectsSymlinkEscapingRoot(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "outbox")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	secret := writeFile(t, base, "id_rsa")

	link := filepath.Join(root, "innocent.jpg")
	if err := os.Symlink(secret, link); err != nil {
		// Unprivileged Windows cannot create symlinks; the guarantee still
		// holds there, it just cannot be demonstrated.
		t.Skipf("cannot create symlink on this system: %v", err)
	}

	if err := newRoots(t, root).Allows(link); !errors.Is(err, ErrOutsideRoots) {
		t.Errorf("Allows(symlink escaping root) = %v, want ErrOutsideRoots", err)
	}
}

// TestAllowsSymlinkStayingInsideRoot is the other half: resolution must not
// reject a link that leads somewhere legitimate.
func TestAllowsSymlinkStayingInsideRoot(t *testing.T) {
	root := t.TempDir()
	target := writeFile(t, root, "real.jpg")
	link := filepath.Join(root, "alias.jpg")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("cannot create symlink on this system: %v", err)
	}

	if err := newRoots(t, root).Allows(link); err != nil {
		t.Errorf("Allows(symlink inside root) = %v, want nil", err)
	}
}

// TestRejectsMissingFileAsOutsideRoots pins that a nonexistent path is
// indistinguishable from a forbidden one. Reporting them differently would
// turn the send tools into a filesystem existence oracle.
func TestRejectsMissingFileAsOutsideRoots(t *testing.T) {
	root := t.TempDir()
	missing := filepath.Join(root, "not-here.jpg")

	if err := newRoots(t, root).Allows(missing); !errors.Is(err, ErrOutsideRoots) {
		t.Errorf("Allows(missing file) = %v, want ErrOutsideRoots", err)
	}
}

// TestZeroValueAllowsNothing pins the fail-safe direction: a Roots that was
// never configured must deny, not permit the whole filesystem.
func TestZeroValueAllowsNothing(t *testing.T) {
	dir := t.TempDir()
	f := writeFile(t, dir, "anything.txt")

	var r Roots
	if err := r.Allows(f); !errors.Is(err, ErrOutsideRoots) {
		t.Errorf("zero Roots.Allows() = %v, want ErrOutsideRoots", err)
	}
}

func TestRejectsTheRootDirectoryItself(t *testing.T) {
	dir := t.TempDir()
	if err := newRoots(t, dir).Allows(dir); !errors.Is(err, ErrOutsideRoots) {
		t.Errorf("Allows(the root itself) = %v, want ErrOutsideRoots (a directory is not a file)", err)
	}
}

func TestMultipleRootsEachAllowed(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	fa, fb := writeFile(t, a, "a.jpg"), writeFile(t, b, "b.jpg")

	r := newRoots(t, a, b)
	if err := r.Allows(fa); err != nil {
		t.Errorf("Allows(file in first root) = %v, want nil", err)
	}
	if err := r.Allows(fb); err != nil {
		t.Errorf("Allows(file in second root) = %v, want nil", err)
	}
}

func TestNewRejectsEmptyAndMissingDirectories(t *testing.T) {
	if _, err := New(nil); err == nil {
		t.Error("New(nil) error = nil, want an error rather than a Roots allowing nothing silently")
	}
	if _, err := New([]string{filepath.Join(t.TempDir(), "nope")}); err == nil {
		t.Error("New(missing dir) error = nil, want an error")
	}
}

// TestErrorCarriesNoPath holds the project-wide invariant that an error
// reaching a model names a category and nothing else.
func TestErrorCarriesNoPath(t *testing.T) {
	root := t.TempDir()
	other := t.TempDir()
	secret := writeFile(t, other, "id_rsa")

	err := newRoots(t, root).Allows(secret)
	if err == nil {
		t.Fatal("Allows(outside file) = nil, want an error")
	}
	for _, leak := range []string{secret, other, "id_rsa"} {
		if strings.Contains(err.Error(), leak) {
			t.Errorf("error %q leaks %q", err, leak)
		}
	}
}

// TestCaseInsensitiveOnWindows records how containment behaves when a
// configured root differs in case from the path being checked. Windows
// paths are case-insensitive, so a user configuring "C:\Outbox" and a model
// naming "c:\outbox\x.jpg" must resolve to the same place.
func TestCaseInsensitiveOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("case-insensitive path matching is a Windows concern")
	}
	dir := t.TempDir()
	f := writeFile(t, dir, "photo.jpg")

	if err := newRoots(t, strings.ToUpper(dir)).Allows(strings.ToLower(f)); err != nil {
		t.Errorf("Allows() with case-varied root and path = %v, want nil", err)
	}
}
