package schedule

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/idle-sync/whatsapp-connect-mcp/internal/gate"
)

func mustLoad(t *testing.T, dir string) *Store {
	t.Helper()
	s, dropped, err := Load(dir, time.Now())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(dropped) != 0 {
		t.Fatalf("Load dropped %v on a fresh/kept store, want none", dropped)
	}
	return s
}

func TestAddListRemoveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := mustLoad(t, dir)

	future := time.Now().Add(time.Hour).Unix()
	e1, err := s.Add(gate.Delivery{Kind: "text", To: "111@s.whatsapp.net", Text: "later", FireAt: future + 60}, "preview-1")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	e2, err := s.Add(gate.Delivery{Kind: "text", To: "222@g.us", Text: "sooner", FireAt: future}, "preview-2")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if e1.ID == "" || e1.ID == e2.ID {
		t.Fatalf("ids = %q, %q — want distinct non-empty ids", e1.ID, e2.ID)
	}

	list := s.List()
	if len(list) != 2 || list[0].ID != e2.ID || list[1].ID != e1.ID {
		t.Fatalf("List() = %+v, want both entries soonest-first", list)
	}

	ok, err := s.Remove(e1.ID)
	if err != nil || !ok {
		t.Fatalf("Remove = (%v, %v), want removed", ok, err)
	}
	if ok, _ := s.Remove(e1.ID); ok {
		t.Fatal("second Remove of the same id reported removed")
	}
	if len(s.List()) != 1 {
		t.Fatalf("List() after remove = %+v, want one entry", s.List())
	}
}

// Schedules must survive a serve restart: that is the whole point of
// persisting them.
func TestSchedulesSurviveReload(t *testing.T) {
	dir := t.TempDir()
	s := mustLoad(t, dir)

	future := time.Now().Add(time.Hour).Unix()
	added, err := s.Add(gate.Delivery{Kind: "text", To: "111@s.whatsapp.net", Text: "persisted", FireAt: future}, "p")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	s2 := mustLoad(t, dir)
	list := s2.List()
	if len(list) != 1 || list[0].ID != added.ID || list[0].Delivery.Text != "persisted" {
		t.Fatalf("reloaded List() = %+v, want the persisted entry", list)
	}
}

// A schedule whose fire time passed while serve was down still fires if it
// is recent (within the missed-grace window); anything older is dropped —
// a "good morning" must not go out at 3pm.
func TestLoadDropsLongMissedSchedules(t *testing.T) {
	dir := t.TempDir()
	s := mustLoad(t, dir)

	now := time.Now()
	_, _ = s.Add(gate.Delivery{Kind: "text", To: "1@s.whatsapp.net", Text: "long missed", FireAt: now.Add(-2 * time.Hour).Unix()}, "p")
	kept, _ := s.Add(gate.Delivery{Kind: "text", To: "2@s.whatsapp.net", Text: "just missed", FireAt: now.Add(-time.Minute).Unix()}, "p")
	futureE, _ := s.Add(gate.Delivery{Kind: "text", To: "3@s.whatsapp.net", Text: "future", FireAt: now.Add(time.Hour).Unix()}, "p")

	s2, dropped, err := Load(dir, now)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(dropped) != 1 || dropped[0].Delivery.Text != "long missed" {
		t.Fatalf("dropped = %+v, want only the long-missed entry", dropped)
	}
	ids := map[string]bool{}
	for _, e := range s2.List() {
		ids[e.ID] = true
	}
	if !ids[kept.ID] || !ids[futureE.ID] || len(ids) != 2 {
		t.Fatalf("kept entries = %v, want the just-missed and future ones", ids)
	}
}

// --- Runner ---

// recordingDeliver is a deliver func that records calls and returns
// scripted errors.
type recordingDeliver struct {
	mu    sync.Mutex
	calls []gate.Delivery
	errs  []error // consumed per call; nil beyond the end
}

func (r *recordingDeliver) deliver(_ context.Context, d gate.Delivery) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, d)
	if len(r.errs) > 0 {
		err := r.errs[0]
		r.errs = r.errs[1:]
		return err
	}
	return nil
}

func (r *recordingDeliver) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestRunnerFiresDueEntryAndRemovesIt(t *testing.T) {
	dir := t.TempDir()
	s := mustLoad(t, dir)
	rec := &recordingDeliver{}
	r := NewRunner(s, rec.deliver, io.Discard)
	r.retryEvery = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.Run(ctx)

	// Added while the runner is already waiting: the change must wake it.
	if _, err := s.Add(gate.Delivery{Kind: "text", To: "111@s.whatsapp.net", Text: "due now", FireAt: time.Now().Unix()}, "p"); err != nil {
		t.Fatalf("Add: %v", err)
	}

	waitFor(t, "delivery", func() bool { return rec.count() == 1 })
	waitFor(t, "entry removal", func() bool { return len(s.List()) == 0 })
}

func TestRunnerRetriesRateLimitedDeliveries(t *testing.T) {
	dir := t.TempDir()
	s := mustLoad(t, dir)
	rec := &recordingDeliver{errs: []error{gate.ErrRateLimited, gate.ErrRateLimited}}
	r := NewRunner(s, rec.deliver, io.Discard)
	r.retryEvery = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.Run(ctx)

	_, _ = s.Add(gate.Delivery{Kind: "text", To: "111@s.whatsapp.net", Text: "rate limited twice", FireAt: time.Now().Unix()}, "p")

	waitFor(t, "third attempt succeeding", func() bool { return rec.count() >= 3 && len(s.List()) == 0 })
}

func TestRunnerDropsPermanentlyFailedEntry(t *testing.T) {
	dir := t.TempDir()
	s := mustLoad(t, dir)
	rec := &recordingDeliver{errs: []error{errors.New("recipient not found")}}
	r := NewRunner(s, rec.deliver, io.Discard)
	r.retryEvery = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.Run(ctx)

	_, _ = s.Add(gate.Delivery{Kind: "text", To: "111@s.whatsapp.net", Text: "will fail", FireAt: time.Now().Unix()}, "p")

	waitFor(t, "entry dropped after permanent failure", func() bool { return len(s.List()) == 0 })
	time.Sleep(50 * time.Millisecond)
	if rec.count() != 1 {
		t.Fatalf("deliver called %d times, want exactly 1 (no retry on a permanent error)", rec.count())
	}
}

func TestRunnerWaitsForFutureEntries(t *testing.T) {
	dir := t.TempDir()
	s := mustLoad(t, dir)
	rec := &recordingDeliver{}
	r := NewRunner(s, rec.deliver, io.Discard)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.Run(ctx)

	_, _ = s.Add(gate.Delivery{Kind: "text", To: "111@s.whatsapp.net", Text: "not yet", FireAt: time.Now().Add(time.Hour).Unix()}, "p")

	time.Sleep(150 * time.Millisecond)
	if rec.count() != 0 {
		t.Fatalf("deliver called %d times for a future entry, want 0", rec.count())
	}
	if len(s.List()) != 1 {
		t.Fatal("future entry vanished")
	}
}

// A cancelled entry must not fire even if it was already due next.
func TestRunnerHonorsCancellation(t *testing.T) {
	dir := t.TempDir()
	s := mustLoad(t, dir)
	rec := &recordingDeliver{}
	r := NewRunner(s, rec.deliver, io.Discard)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.Run(ctx)

	e, _ := s.Add(gate.Delivery{Kind: "text", To: "111@s.whatsapp.net", Text: "cancel me", FireAt: time.Now().Add(2 * time.Second).Unix()}, "p")
	if ok, _ := s.Remove(e.ID); !ok {
		t.Fatal("Remove failed")
	}

	time.Sleep(150 * time.Millisecond)
	if rec.count() != 0 {
		t.Fatalf("deliver called %d times for a cancelled entry, want 0", rec.count())
	}
}

// --- StoreDeliverer: the schedule-time gate's deliverer ---

func fixedNow() time.Time { return time.Unix(1787056496, 0) } // 2026-08-18T12:34:56Z

func TestStoreDelivererStoresConfirmedSchedule(t *testing.T) {
	s := mustLoad(t, t.TempDir())
	sd := NewStoreDeliverer(s, func(gate.Delivery) error { return nil }, fixedNow)

	d := gate.Delivery{Kind: "text", To: "111@s.whatsapp.net", Text: "morning", FireAt: fixedNow().Add(time.Hour).Unix()}
	if err := sd.Validate(d); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	id, err := sd.Deliver(context.Background(), d)
	if err != nil || id == "" {
		t.Fatalf("Deliver = (%q, %v), want a schedule id", id, err)
	}

	list := s.List()
	if len(list) != 1 || list[0].ID != id || list[0].Delivery.Text != "morning" {
		t.Fatalf("List() = %+v, want the stored schedule", list)
	}
	if list[0].Preview == "" {
		t.Fatal("stored entry has no preview for list_scheduled to show")
	}
}

func TestStoreDelivererRefusesPastAndFarFuture(t *testing.T) {
	s := mustLoad(t, t.TempDir())
	sd := NewStoreDeliverer(s, func(gate.Delivery) error { return nil }, fixedNow)

	past := gate.Delivery{Kind: "text", To: "1@s.whatsapp.net", Text: "x", FireAt: fixedNow().Add(-time.Minute).Unix()}
	if err := sd.Validate(past); err == nil {
		t.Fatal("Validate accepted a fire time in the past")
	}
	far := gate.Delivery{Kind: "text", To: "1@s.whatsapp.net", Text: "x", FireAt: fixedNow().Add(31 * 24 * time.Hour).Unix()}
	if err := sd.Validate(far); err == nil {
		t.Fatal("Validate accepted a fire time beyond the horizon")
	}
}

// Content validation (the media-roots allowlist, poll shape, ...) must run
// at scheduling time too, so a schedule that could never deliver is
// refused before anyone confirms it.
func TestStoreDelivererConsultsContentValidator(t *testing.T) {
	s := mustLoad(t, t.TempDir())
	wantErr := errors.New("outside media roots")
	sd := NewStoreDeliverer(s, func(gate.Delivery) error { return wantErr }, fixedNow)

	d := gate.Delivery{Kind: "media", To: "1@s.whatsapp.net", Path: "/x", FireAt: fixedNow().Add(time.Hour).Unix()}
	if err := sd.Validate(d); !errors.Is(err, wantErr) {
		t.Fatalf("Validate = %v, want the content validator's error", err)
	}
}
