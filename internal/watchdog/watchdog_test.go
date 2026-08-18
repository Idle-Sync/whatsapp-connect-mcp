package watchdog

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestWatchParentReturnsWhenParentChanges(t *testing.T) {
	// ppid starts at the original and flips after the first poll, standing
	// in for the process being reparented when its parent exits.
	var ppid atomic.Int64
	ppid.Store(1000)

	getppid := func() int {
		v := ppid.Load()
		ppid.Store(1) // reparented to init on the next read
		return int(v)
	}

	done := make(chan bool, 1)
	go func() {
		done <- WatchParent(context.Background(), 1000, time.Millisecond, getppid)
	}()

	select {
	case gone := <-done:
		if !gone {
			t.Error("WatchParent returned false, want true when the parent id changed")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("WatchParent did not return after the parent id changed")
	}
}

func TestWatchParentReturnsFalseOnContextCancel(t *testing.T) {
	// A parent that never changes: only ctx cancellation should end the wait.
	getppid := func() int { return 1000 }

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan bool, 1)
	go func() {
		done <- WatchParent(ctx, 1000, time.Millisecond, getppid)
	}()

	// Let it poll a few times, confirming a steady parent does not trip it.
	time.Sleep(20 * time.Millisecond)
	select {
	case <-done:
		t.Fatal("WatchParent returned while the parent id was unchanged")
	default:
	}

	cancel()
	select {
	case gone := <-done:
		if gone {
			t.Error("WatchParent returned true on cancellation, want false")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("WatchParent did not return after ctx was cancelled")
	}
}
