package bridge

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

// TestStatusTracksLifecycle drives handleEvent through a connect,
// disconnect, reconnect sequence and checks the Status snapshot after each
// step. newTestBridge is never paired, so the starting state is unpaired.
func TestStatusTracksLifecycle(t *testing.T) {
	b, _ := newTestBridge(t)

	if got := b.Status(); got.State != "unpaired" || got.LastDisconnect != "" {
		t.Fatalf("initial Status = %+v, want state unpaired, no disconnect", got)
	}

	b.handleEvent(&events.Connected{})
	if got := b.Status(); got.State != "connected" || got.Reconnects != 0 {
		t.Fatalf("after first Connected: %+v, want connected with 0 reconnects", got)
	}

	b.handleEvent(&events.Disconnected{})
	if got := b.Status(); got.State != "connecting" || got.LastDisconnect != "disconnected" {
		t.Fatalf("after Disconnected: %+v, want connecting/disconnected", got)
	}

	b.handleEvent(&events.Connected{})
	if got := b.Status(); got.State != "connected" || got.Reconnects != 1 {
		t.Fatalf("after reconnect: %+v, want connected with 1 reconnect", got)
	}
}

// TestFreshUnpairedBridgeSinceIsRecent guards against the connState zero
// value (stUnpaired) colliding with atomic.Int32's zero value: Open sets
// stUnpaired on an unpaired device, and setState must still record that
// entry rather than mistaking it for "state unchanged" and leaving
// stateSince at its zero value (the Unix epoch).
func TestFreshUnpairedBridgeSinceIsRecent(t *testing.T) {
	start := time.Now()
	b, _ := newTestBridge(t)
	end := time.Now()

	// setState stores whole-second Unix timestamps, so allow a second of
	// slack on either side of the [start, end] window rather than demanding
	// sub-second precision.
	since := b.Status().Since
	if since.Before(start.Add(-time.Second)) || since.After(end.Add(time.Second)) {
		t.Fatalf("Status().Since = %v, want within a second of [%v, %v]", since, start, end)
	}
}

// TestConnectFailureFamilyLandsOffline: whatsmeow does not auto-reconnect
// after these three, so reporting "connecting" would be a lie.
func TestConnectFailureFamilyLandsOffline(t *testing.T) {
	cases := []struct {
		name     string
		evt      any
		category string
	}{
		{"connect_failure", &events.ConnectFailure{}, "connect_failure"},
		{"temporary_ban", &events.TemporaryBan{}, "temporary_ban"},
		{"client_outdated", &events.ClientOutdated{}, "client_outdated"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, _ := newTestBridge(t)
			b.handleEvent(&events.Connected{})
			b.handleEvent(tc.evt)
			got := b.Status()
			if got.State != "offline" || got.LastDisconnect != tc.category {
				t.Fatalf("Status = %+v, want offline/%s", got, tc.category)
			}
		})
	}
}

// TestIngestFailuresAreCountedAndReportedOnce: the write stays
// fire-and-forget, but a failing store must be countable, and the one-time
// diagnostic must never leak the raw error text (rule 7 — driver errors
// can carry arbitrary strings).
func TestIngestFailuresAreCountedAndReportedOnce(t *testing.T) {
	b, fake := newTestBridge(t)
	var buf bytes.Buffer
	b.diag = &buf
	fake.failWith = errors.New("disk full: /secret/path")

	msg := &waE2E.Message{Conversation: proto.String("hi")}
	b.handleEvent(messageEvent(dmSource(false), "Some Name", msg))

	if b.IngestErrors() == 0 {
		t.Fatal("IngestErrors = 0 after failing ingest, want > 0")
	}
	if !strings.Contains(buf.String(), "failed to write") {
		t.Fatalf("diag = %q, want an ingest-failure line", buf.String())
	}
	if strings.Contains(buf.String(), "disk full") {
		t.Fatalf("diag leaked the raw store error: %q", buf.String())
	}

	before := buf.Len()
	b.handleEvent(messageEvent(dmSource(false), "Some Name", msg))
	if buf.Len() != before {
		t.Error("ingest diagnostic printed more than once")
	}
	if b.IngestErrors() < 4 { // chat + message upserts fail on both events
		t.Errorf("IngestErrors = %d, want every failed write counted", b.IngestErrors())
	}
}
