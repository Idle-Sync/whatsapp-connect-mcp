// Package watchdog ends the stdio server when its parent process goes away.
//
// The stdio transport already exits when its stdin closes, which is the
// usual signal that the MCP client has stopped. This is the belt to that
// suspender: if the client dies without cleanly closing the pipe, the
// server's parent process id changes (on Unix it is reparented, so the id
// stops matching the client's), and this detects that and asks the server
// to stop — so a crashed client does not leave an orphan holding the
// WhatsApp connection open.
//
// On platforms where the parent id never changes after a parent exits
// (Windows returns the creating process id and does not update it), the
// watch simply never fires, which is a safe no-op: stdin-close still ends
// the server there.
package watchdog

import (
	"context"
	"time"
)

// DefaultInterval is how often the parent id is polled. Parent death is not
// urgent — the orphan is idle, just holding a connection — so this is
// unhurried enough to be invisible.
const DefaultInterval = 5 * time.Second

// WatchParent blocks until the parent process id read by getppid differs
// from original, or until ctx is cancelled. It returns true only in the
// former case: the caller uses that to distinguish "the parent went away,
// shut down" from "we were asked to stop for our own reasons".
//
// getppid is a parameter rather than a direct os.Getppid call so the
// reparenting can be exercised in a test without actually forking.
func WatchParent(ctx context.Context, original int, interval time.Duration, getppid func() int) (parentGone bool) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
			if getppid() != original {
				return true
			}
		}
	}
}
