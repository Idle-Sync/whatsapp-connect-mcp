package bridge

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waAdv"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	"github.com/idle-sync/whatsapp-connect-mcp/internal/mediapath"
)

// pairContainer mints a paired device directly in b's session container —
// what a completed QR pairing leaves behind — without any network.
// NewDevice pre-populates the key material PutDevice requires; only the
// identity and Account (PutDevice dereferences it unconditionally, and
// NewDevice leaves it nil since a real pairing always fills it in before
// saving) are set here.
func pairContainer(t *testing.T, b *Bridge) {
	t.Helper()
	device := b.container.NewDevice()
	jid := types.JID{User: "15551234567", Server: types.DefaultUserServer}
	device.ID = &jid
	device.Account = &waAdv.ADVSignedDeviceIdentity{
		Details:             []byte{},
		AccountSignature:    make([]byte, 64),
		AccountSignatureKey: make([]byte, 32),
		DeviceSignature:     make([]byte, 64),
	}
	if err := b.container.PutDevice(context.Background(), device); err != nil {
		t.Fatalf("PutDevice: %v", err)
	}
}

// TestLoggedOutReinitializesToUnpaired: after the server-side logout event
// (whatsmeow has already deleted the device by the time it fires — the
// explicit DeleteDevice simulates that), the SAME Bridge must come back as
// a fresh unpaired client rather than a zombie.
func TestLoggedOutReinitializesToUnpaired(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // ends the recovery goroutine the re-init starts

	fake := &fakeIngest{}
	b, err := Open(ctx, t.TempDir(), fake, mediapath.Roots{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = b.Close() })
	b.pairPoll = 10 * time.Millisecond

	pairContainer(t, b)
	device, err := b.container.GetFirstDevice(ctx)
	if err != nil {
		t.Fatalf("GetFirstDevice: %v", err)
	}
	b.setClient(device)
	if b.NeedsPairing() {
		t.Fatal("precondition: bridge should be paired")
	}

	// whatsmeow deletes the device store before dispatching LoggedOut.
	if err := b.container.DeleteDevice(ctx, device); err != nil {
		t.Fatalf("DeleteDevice: %v", err)
	}
	regsBefore := b.handlerRegistrations
	b.handleEvent(&events.LoggedOut{})

	if !b.NeedsPairing() {
		t.Fatal("bridge still reports paired after LoggedOut")
	}
	if got := b.Status(); got.State != "unpaired" || got.LastDisconnect != "logged_out" {
		t.Fatalf("Status = %+v, want unpaired/logged_out", got)
	}
	if b.handlerRegistrations != regsBefore+1 {
		t.Fatalf("handlerRegistrations = %d, want %d (one fresh client)", b.handlerRegistrations, regsBefore+1)
	}
}

// TestWaitForPairingSeesExternalPairing: pairing completes in another
// process (setup); WaitForPairing on this Bridge must notice via
// ReloadDevice without the Bridge being closed and reopened.
func TestWaitForPairingSeesExternalPairing(t *testing.T) {
	b, _ := newTestBridge(t)
	b.pairPoll = 10 * time.Millisecond

	go func() {
		time.Sleep(50 * time.Millisecond)
		pairContainer(t, b)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := b.WaitForPairing(ctx); err != nil {
		t.Fatalf("WaitForPairing: %v", err)
	}
	if b.NeedsPairing() {
		t.Fatal("still unpaired after WaitForPairing returned")
	}
	if got := b.Status().State; got != "offline" {
		t.Fatalf("state after external pairing = %q, want offline (paired, not connected)", got)
	}
}

// TestRecoverPairingReportsReloadFailure: a real ReloadDevice error during
// the post-logout pairing wait (e.g. an unreadable session store) must
// surface a diagnostic — it is not a shutdown signal, and nothing else will
// tell the operator the recovery goroutine died.
func TestRecoverPairingReportsReloadFailure(t *testing.T) {
	b, _ := newTestBridge(t)
	b.pairPoll = 10 * time.Millisecond
	var buf bytes.Buffer
	b.diag = &buf

	if err := b.container.Close(); err != nil {
		t.Fatalf("close container: %v", err)
	}

	b.recoverPairing()

	if !strings.Contains(buf.String(), "restart the server") {
		t.Fatalf("diag = %q, want a re-check-failed diagnostic", buf.String())
	}
}

// TestRecoverPairingStaysSilentOnCancellation: process shutdown during the
// pairing wait is a clean stop, not a failure — recoverPairing must not
// write a diagnostic for it.
func TestRecoverPairingStaysSilentOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	fake := &fakeIngest{}
	b, err := Open(ctx, t.TempDir(), fake, mediapath.Roots{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = b.Close() })
	b.pairPoll = time.Hour // never fires before cancellation

	var buf bytes.Buffer
	b.diag = &buf
	cancel()

	b.recoverPairing()

	if buf.Len() != 0 {
		t.Fatalf("diag = %q, want silence on context cancellation", buf.String())
	}
}

// TestWaErrDistinguishesUnpairedFromDisconnected: rule 7 messages stay
// category-only, but "no longer paired" and "briefly not connected" demand
// different operator actions and must not share one message.
func TestWaErrDistinguishesUnpairedFromDisconnected(t *testing.T) {
	b, _ := newTestBridge(t) // unpaired
	err := b.waErr("send message", whatsmeow.ErrNotLoggedIn)
	if err == nil || !strings.Contains(err.Error(), "no longer paired") {
		t.Fatalf("unpaired waErr = %v, want the re-pair message", err)
	}

	pairContainer(t, b)
	if err := b.ReloadDevice(context.Background()); err != nil {
		t.Fatalf("ReloadDevice: %v", err)
	}
	err = b.waErr("send message", whatsmeow.ErrNotLoggedIn)
	if err == nil || !strings.Contains(err.Error(), "not connected to WhatsApp") {
		t.Fatalf("paired-but-offline waErr = %v, want the not-connected message", err)
	}
	if errors.Is(err, whatsmeow.ErrNotLoggedIn) {
		t.Fatal("category error must not wrap the raw whatsmeow error")
	}
}

// TestConnectAlreadyConnectedIsSuccess: with the dashboard pairing
// in-process while a background WaitForPairing→Connect goroutine runs,
// the second Connect on an already-connected client must be a no-op, not
// a category error printed as a failure.
func TestConnectAlreadyConnectedIsSuccess(t *testing.T) {
	b, _ := newTestBridge(t)
	if err := b.connectErr(whatsmeow.ErrAlreadyConnected); err != nil {
		t.Fatalf("ErrAlreadyConnected mapped to %v, want nil", err)
	}
	if err := b.connectErr(whatsmeow.ErrNotLoggedIn); err == nil {
		t.Fatal("a real connect error must still surface")
	}
}

// TestConnectOutcomeSettlesState: the state Connect enters (connecting)
// must not outlive a failed attempt. Nothing retries a connect that never
// produced a connection, so a bridge left in "connecting" would report it
// forever — the dashboard showed exactly that after a service started
// before DNS was up. Already-connected lands in connected directly, since
// no Connected event fires for it.
func TestConnectOutcomeSettlesState(t *testing.T) {
	b, _ := newTestBridge(t)

	b.setState(stConnecting)
	_ = b.connectErr(whatsmeow.ErrNotLoggedIn)
	if got := b.Status().State; got != "offline" {
		t.Fatalf("after a failed connect: state %q, want offline", got)
	}

	b.setState(stConnecting)
	_ = b.connectErr(whatsmeow.ErrAlreadyConnected)
	if got := b.Status().State; got != "connected" {
		t.Fatalf("after already-connected: state %q, want connected", got)
	}

	// Success leaves connecting in place: the Connected event advances it.
	b.setState(stConnecting)
	_ = b.connectErr(nil)
	if got := b.Status().State; got != "connecting" {
		t.Fatalf("after a successful connect call: state %q, want connecting until the Connected event", got)
	}
}
