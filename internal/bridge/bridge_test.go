package bridge

import "testing"

// TestEventHandlerRegisteredExactlyOnce proves that no matter how many
// times ensureHandlerRegistered is called — Open calls it once at
// construction; Connect and PairQR each call it defensively too, in case
// either is ever invoked more than once over a Bridge's lifetime — the
// underlying whatsmeow AddEventHandler call happens exactly once. That's
// what guarantees a second Connect can't cause every inbound event to be
// dispatched (and ingested) twice.
func TestEventHandlerRegisteredExactlyOnce(t *testing.T) {
	b, _ := newTestBridge(t)

	if b.handlerRegistrations != 1 {
		t.Fatalf("handlerRegistrations after Open = %d, want 1", b.handlerRegistrations)
	}

	// Simulate what Connect/PairQR do on every call.
	b.ensureHandlerRegistered()
	b.ensureHandlerRegistered()
	b.ensureHandlerRegistered()

	if b.handlerRegistrations != 1 {
		t.Fatalf("handlerRegistrations after repeated calls = %d, want 1 (must stay a no-op)", b.handlerRegistrations)
	}
}
