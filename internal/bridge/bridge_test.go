package bridge

import (
	"context"
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

// TestClientConstructionRegistersHandlerExactlyOnce proves the handler
// registration invariant: setClient is the only way a whatsmeow client
// comes to exist on a Bridge, and each call registers handleEvent exactly
// once on the client it builds. Connect and PairQR register nothing, so no
// call sequence can ever double-dispatch events into the store.
func TestClientConstructionRegistersHandlerExactlyOnce(t *testing.T) {
	b, _ := newTestBridge(t)

	if b.handlerRegistrations != 1 {
		t.Fatalf("handlerRegistrations after Open = %d, want 1", b.handlerRegistrations)
	}

	// A second client (what a logout re-init does) registers exactly once
	// more — one registration per client ever constructed.
	device, err := b.container.GetFirstDevice(context.Background())
	if err != nil {
		t.Fatalf("GetFirstDevice: %v", err)
	}
	b.setClient(device)
	if b.handlerRegistrations != 2 {
		t.Fatalf("handlerRegistrations after second setClient = %d, want 2", b.handlerRegistrations)
	}
}
