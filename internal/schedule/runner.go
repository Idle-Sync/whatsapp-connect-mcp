package schedule

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/idle-sync/whatsapp-connect-mcp/internal/gate"
)

// defaultRetryEvery is how long the runner waits before retrying a
// delivery the shared rate limiter refused. Just above the gate's 5s
// floor would waste one interval; a couple of seconds keeps the fire
// close to due without hammering.
const defaultRetryEvery = 2 * time.Second

// Runner delivers due schedule entries. Construct with NewRunner and run
// its Run loop in a goroutine for the life of the process.
type Runner struct {
	store   *Store
	deliver func(ctx context.Context, d gate.Delivery) error
	log     io.Writer

	// retryEvery is the rate-limited retry interval; a field so tests
	// don't sleep real seconds.
	retryEvery time.Duration
}

// NewRunner builds a Runner over store that delivers via deliver — in
// production a closure over gate.DeliverScheduled, the path that shares
// the live sends' rate limiter — and reports outcomes to log (stderr).
func NewRunner(store *Store, deliver func(ctx context.Context, d gate.Delivery) error, log io.Writer) *Runner {
	return &Runner{store: store, deliver: deliver, log: log, retryEvery: defaultRetryEvery}
}

// Run fires entries as they come due until ctx is cancelled. A
// rate-limited delivery is retried until a token frees up (the entry
// stays); any other failure drops the entry — a send that failed once
// must not silently retry into a duplicate delivery later.
func (r *Runner) Run(ctx context.Context) {
	for {
		entry, ok := r.next()
		if !ok {
			if !r.waitForChange(ctx) {
				return
			}
			continue
		}

		if wait := time.Until(time.Unix(entry.Delivery.FireAt, 0)); wait > 0 {
			if !r.sleepOrChange(ctx, wait) {
				return
			}
			continue // re-evaluate: the set may have changed while waiting
		}

		err := r.deliver(ctx, entry.Delivery)
		switch {
		case errors.Is(err, gate.ErrRateLimited):
			if !r.sleep(ctx, r.retryEvery) {
				return
			}
		case err != nil:
			_, _ = fmt.Fprintf(r.log, "schedule %s failed: %v\n", entry.ID, err)
			_, _ = r.store.Remove(entry.ID)
		default:
			_, _ = fmt.Fprintf(r.log, "schedule %s delivered\n", entry.ID)
			_, _ = r.store.Remove(entry.ID)
		}
	}
}

// next returns the soonest pending entry.
func (r *Runner) next() (Entry, bool) {
	list := r.store.List()
	if len(list) == 0 {
		return Entry{}, false
	}
	return list[0], true
}

// waitForChange blocks until the schedule set changes or ctx ends,
// reporting whether to keep running.
func (r *Runner) waitForChange(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return false
	case <-r.store.Changed():
		return true
	}
}

// sleepOrChange waits up to d, returning early if the schedule set
// changes; reports whether to keep running.
func (r *Runner) sleepOrChange(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-r.store.Changed():
		return true
	case <-timer.C:
		return true
	}
}

// sleep waits d, reporting whether to keep running.
func (r *Runner) sleep(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
