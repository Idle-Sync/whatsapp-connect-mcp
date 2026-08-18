//go:build windows

package instancelock

import (
	"os"

	"golang.org/x/sys/windows"
)

// lockFile takes an exclusive, non-blocking lock on the first byte of f.
// LockFileEx locks are held per handle and released by the OS when the
// process exits, however it exits.
func lockFile(f *os.File) error {
	ol := new(windows.Overlapped)
	return windows.LockFileEx(windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, ol)
}

func unlockFile(f *os.File) error {
	ol := new(windows.Overlapped)
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, ol)
}
