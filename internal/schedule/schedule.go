// Package schedule holds sends the human has already confirmed for a
// future fire time, and the runner that delivers them when due.
//
// The safety shape (issue #3): confirmation happens at scheduling time —
// an untrusted recipient's schedule_send drafts and the human commits the
// draft into the schedule, a trusted recipient's schedules on the first
// call — and the fire-time delivery goes through gate.DeliverScheduled,
// which consumes the same shared rate limiter as every live send. A
// schedule entry existing at all is the proof of prior confirmation; no
// path adds one without passing the gate.
//
// Persistence: entries live in <dataDir>/schedules.json so they survive a
// serve restart. A schedule only fires while serve is running; one whose
// fire time passed while serve was down still fires on the next start if
// it is recent (missedGrace), and is dropped otherwise — a "good morning"
// must not go out at 3pm.
package schedule

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/idle-sync/whatsapp-connect-mcp/internal/gate"
)

// fileName is the schedule file's name inside the data directory.
const fileName = "schedules.json"

// missedGrace is how far past its fire time a schedule may still fire on
// startup. Beyond it the entry is dropped as missed.
const missedGrace = 15 * time.Minute

// Entry is one pending scheduled send. The fire time lives in
// Delivery.FireAt — it is part of what the human confirmed.
type Entry struct {
	ID       string        `json:"id"`
	Delivery gate.Delivery `json:"delivery"`
	Preview  string        `json:"preview"`
	Created  int64         `json:"created"`
}

// Store is the persistent set of pending schedules. Construct with Load.
type Store struct {
	mu      sync.Mutex
	path    string
	entries []Entry
	changed chan struct{}
}

// Load reads dataDir's schedule file, dropping entries whose fire time is
// more than missedGrace in the past (returned as dropped, for the caller
// to report). A missing file is an empty store.
func Load(dataDir string, now time.Time) (*Store, []Entry, error) {
	s := &Store{
		path:    filepath.Join(dataDir, fileName),
		changed: make(chan struct{}, 1),
	}

	data, err := os.ReadFile(s.path) // #nosec G304 -- path is derived from the server's own data directory
	if errors.Is(err, os.ErrNotExist) {
		return s, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("read schedules: %w", err)
	}
	var entries []Entry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, nil, fmt.Errorf("parse schedules: %w", err)
	}

	cutoff := now.Add(-missedGrace).Unix()
	var dropped []Entry
	for _, e := range entries {
		if e.Delivery.FireAt < cutoff {
			dropped = append(dropped, e)
			continue
		}
		s.entries = append(s.entries, e)
	}
	if len(dropped) > 0 {
		if err := s.persistLocked(); err != nil {
			return nil, nil, err
		}
	}
	return s, dropped, nil
}

// Add stores d (whose FireAt must already be set) with preview, assigns an
// id, persists, and wakes the runner.
func (s *Store) Add(d gate.Delivery, preview string) (Entry, error) {
	idBytes := make([]byte, 8)
	if _, err := rand.Read(idBytes); err != nil {
		return Entry{}, fmt.Errorf("schedule id: %w", err)
	}
	e := Entry{
		ID:       hex.EncodeToString(idBytes),
		Delivery: d,
		Preview:  preview,
		Created:  time.Now().Unix(),
	}

	s.mu.Lock()
	s.entries = append(s.entries, e)
	err := s.persistLocked()
	s.mu.Unlock()
	if err != nil {
		return Entry{}, err
	}
	s.notify()
	return e, nil
}

// List returns the pending entries, soonest fire time first.
func (s *Store) List() []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Entry, len(s.entries))
	copy(out, s.entries)
	sort.Slice(out, func(i, j int) bool { return out[i].Delivery.FireAt < out[j].Delivery.FireAt })
	return out
}

// Remove deletes the entry with id, persists, and wakes the runner.
// removed is false when no such entry exists.
func (s *Store) Remove(id string) (removed bool, err error) {
	s.mu.Lock()
	kept := s.entries[:0]
	for _, e := range s.entries {
		if e.ID == id {
			removed = true
			continue
		}
		kept = append(kept, e)
	}
	s.entries = kept
	if removed {
		err = s.persistLocked()
	}
	s.mu.Unlock()
	if removed {
		s.notify()
	}
	return removed, err
}

// Changed signals (coalesced) whenever the schedule set changes; the
// runner selects on it to recompute its next wake time.
func (s *Store) Changed() <-chan struct{} {
	return s.changed
}

func (s *Store) notify() {
	select {
	case s.changed <- struct{}{}:
	default:
	}
}

// persistLocked writes the entries atomically. Callers must hold s.mu.
func (s *Store) persistLocked() error {
	data, err := json.MarshalIndent(s.entries, "", "  ")
	if err != nil {
		return fmt.Errorf("encode schedules: %w", err)
	}

	dir := filepath.Dir(s.path)
	tmp, err := os.CreateTemp(dir, fileName+".tmp-*")
	if err != nil {
		return fmt.Errorf("write schedules: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }() // no-op once renamed away

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write schedules: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write schedules: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		return fmt.Errorf("write schedules: %w", err)
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		return fmt.Errorf("write schedules: %w", err)
	}
	return nil
}

// scheduleHorizon bounds how far ahead a send may be scheduled.
const scheduleHorizon = 30 * 24 * time.Hour

// StoreDeliverer is the gate.Deliverer behind the schedule-time gate: its
// "delivery" is storing the confirmed entry, not sending anything. Wiring
// it into its own gate.Gate is what buys the draft-then-confirm flow for
// scheduling with zero new confirmation machinery — an untrusted
// recipient's schedule_send drafts exactly like a live send, and the
// human's commit lands the entry here instead of on the wire.
type StoreDeliverer struct {
	store *Store
	// validateContent is the live deliverer's Validate (media roots, poll
	// shape, ...): a schedule that could never deliver must be refused
	// before anyone confirms it.
	validateContent func(gate.Delivery) error
	now             func() time.Time
}

// NewStoreDeliverer builds the schedule-time deliverer over store.
func NewStoreDeliverer(store *Store, validateContent func(gate.Delivery) error, now func() time.Time) *StoreDeliverer {
	return &StoreDeliverer{store: store, validateContent: validateContent, now: now}
}

// Validate refuses fire times in the past or beyond the horizon, then
// defers to the live content validation.
func (s *StoreDeliverer) Validate(d gate.Delivery) error {
	now := s.now()
	if d.FireAt <= now.Unix() {
		return errors.New("send_at must be in the future")
	}
	if d.FireAt > now.Add(scheduleHorizon).Unix() {
		return errors.New("send_at is too far ahead (max 30 days)")
	}
	return s.validateContent(d)
}

// Deliver stores the confirmed schedule and returns its id (surfaced to
// the caller where a live send would surface a message id).
func (s *StoreDeliverer) Deliver(_ context.Context, d gate.Delivery) (string, error) {
	e, err := s.store.Add(d, summarize(d))
	if err != nil {
		return "", err
	}
	return e.ID, nil
}

// summarize renders the one-line description list_scheduled shows for an
// entry.
func summarize(d gate.Delivery) string {
	when := time.Unix(d.FireAt, 0).UTC().Format(time.RFC3339)
	switch {
	case d.Path != "":
		return fmt.Sprintf("%s to %s at %s: %s", d.Kind, d.To, when, filepath.Base(d.Path))
	default:
		return fmt.Sprintf("%s to %s at %s: %q", d.Kind, d.To, when, d.Text)
	}
}
