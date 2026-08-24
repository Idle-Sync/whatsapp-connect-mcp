package bridge

import (
	"fmt"
	"time"
)

// connState is the Bridge's connection lifecycle state, stored in an
// atomic.Int32 and stringified only at the Status boundary. The values
// start above zero so the atomic's zero value (never explicitly stored)
// stays a distinct, detectable "unset" sentinel instead of colliding with
// stUnpaired — setState compares the stored value to detect a real
// transition, and a collision at zero would make the very first setState
// call after Open look like a no-op.
type connState int32

const (
	stUnpaired   connState = iota + 1 // no device identity; pairing required
	stOffline                         // paired, no connection attempt in progress
	stConnecting                      // Connect/PairQR called, or whatsmeow is auto-reconnecting
	stConnected                       // events.Connected received
)

func (s connState) String() string {
	switch s {
	case stUnpaired:
		return "unpaired"
	case stOffline:
		return "offline"
	case stConnecting:
		return "connecting"
	case stConnected:
		return "connected"
	default:
		// The zero value, before Open has run setState. Should never be
		// observable through Status once Open has returned.
		return "unknown"
	}
}

// Status is a point-in-time snapshot of connection health. Plain data,
// stdlib types only, safe for any package to consume; the status CLI and
// the dashboard read it, doctor consumes the same facts via Env funcs.
type Status struct {
	State          string    // unpaired | offline | connecting | connected
	Since          time.Time // when State was entered
	LastEventAt    time.Time // zero if no event since Open
	IngestErrors   uint64
	Reconnects     uint64
	LastDisconnect string // category, "" if never disconnected
}

// Status reports the current connection-health snapshot.
func (b *Bridge) Status() Status {
	b.disconnectMu.Lock()
	last := b.lastDisconnect
	b.disconnectMu.Unlock()
	return Status{
		State:          connState(b.state.Load()).String(),
		Since:          time.Unix(b.stateSince.Load(), 0),
		LastEventAt:    b.LastEventAt(),
		IngestErrors:   b.ingestErrors.Load(),
		Reconnects:     b.reconnects.Load(),
		LastDisconnect: last,
	}
}

// IngestErrors reports how many inbound events failed to write to the
// message store since Open. Matches doctor.Env's func field shape.
func (b *Bridge) IngestErrors() uint64 { return b.ingestErrors.Load() }

// LastDisconnect reports why the connection was last lost, as a fixed
// category string, "" if it never was. Matches doctor.Env's field shape.
func (b *Bridge) LastDisconnect() string {
	b.disconnectMu.Lock()
	defer b.disconnectMu.Unlock()
	return b.lastDisconnect
}

// setState records entering s, keeping the entry time of an unchanged
// state (repeated Disconnected events while reconnecting must not make the
// state look perpetually fresh).
func (b *Bridge) setState(s connState) {
	if connState(b.state.Swap(int32(s))) != s {
		b.stateSince.Store(time.Now().Unix())
	}
}

// noteDisconnect records why the connection was last lost. category is one
// of the fixed strings Status documents — never a value from the wire.
func (b *Bridge) noteDisconnect(category string) {
	b.disconnectMu.Lock()
	b.lastDisconnect = category
	b.disconnectMu.Unlock()
}

// noteIngest counts a failed store write. The write itself stays
// fire-and-forget — idempotent upserts plus WhatsApp's redelivery repair
// transient failures — but a persistently failing store (disk full,
// corruption) must be observable. The first failure also says so once on
// the diagnostic stream, without the error text: store errors wrap driver
// strings, and diagnostics carry categories only.
func (b *Bridge) noteIngest(err error) {
	if err == nil {
		return
	}
	b.ingestErrors.Add(1)
	b.ingestErrOnce.Do(func() {
		_, _ = fmt.Fprintln(b.diag,
			"whatsapp: failed to write an incoming WhatsApp event to the message store — run `whatsapp-connect-mcp check`")
	})
}
