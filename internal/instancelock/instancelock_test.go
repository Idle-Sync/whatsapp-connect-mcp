package instancelock

import (
	"strings"
	"testing"
)

func TestAcquireIsExclusive(t *testing.T) {
	dir := t.TempDir()

	l, err := Acquire(dir)
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	defer func() { _ = l.Release() }()

	if _, err := Acquire(dir); err == nil {
		t.Fatal("second Acquire succeeded while the first lock is held")
	} else if !strings.Contains(err.Error(), "already running") {
		t.Fatalf("second Acquire error = %q, want it to say another serve is already running", err)
	}
}

func TestReleaseAllowsReacquire(t *testing.T) {
	dir := t.TempDir()

	l, err := Acquire(dir)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := l.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}

	l2, err := Acquire(dir)
	if err != nil {
		t.Fatalf("re-Acquire after Release: %v", err)
	}
	_ = l2.Release()
}

func TestAcquireDistinctDirsAreIndependent(t *testing.T) {
	l1, err := Acquire(t.TempDir())
	if err != nil {
		t.Fatalf("Acquire dir1: %v", err)
	}
	defer func() { _ = l1.Release() }()

	l2, err := Acquire(t.TempDir())
	if err != nil {
		t.Fatalf("Acquire dir2: %v", err)
	}
	_ = l2.Release()
}
