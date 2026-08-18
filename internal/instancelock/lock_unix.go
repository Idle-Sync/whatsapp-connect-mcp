//go:build !windows

package instancelock

import (
	"os"

	"golang.org/x/sys/unix"
)

// lockFile takes an exclusive, non-blocking flock on f. flock locks are
// held per open file description and released by the OS when the process
// exits, however it exits.
func lockFile(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
}

func unlockFile(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_UN)
}
