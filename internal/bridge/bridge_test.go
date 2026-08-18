package bridge

import (
	"testing"

	"go.mau.fi/whatsmeow/proto/waCompanionReg"
	waStore "go.mau.fi/whatsmeow/store"
)

// TestAnnouncedDeviceIdentity proves the package init actually overrode
// whatsmeow's defaults, which announce an OS string of "whatsmeow" with an
// UNKNOWN platform type. Both are process-global variables owned by another
// module, so a whatsmeow upgrade can silently reset either one; without this
// test that regression would only surface as a device name on someone's
// phone after they had already paired.
func TestAnnouncedDeviceIdentity(t *testing.T) {
	if got := waStore.DeviceProps.GetOs(); got != deviceOS {
		t.Errorf("announced OS = %q, want %q", got, deviceOS)
	}

	if got := waStore.DeviceProps.GetPlatformType(); got != devicePlatform {
		t.Errorf("announced platform type = %v, want %v", got, devicePlatform)
	}

	if waStore.DeviceProps.GetPlatformType() == waCompanionReg.DeviceProps_UNKNOWN {
		t.Error("platform type is still whatsmeow's UNKNOWN default")
	}
}

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
