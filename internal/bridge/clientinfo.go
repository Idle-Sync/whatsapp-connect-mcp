package bridge

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waCompanionReg"
	waStore "go.mau.fi/whatsmeow/store"
	"google.golang.org/protobuf/proto"
)

// Everything in this file mutates process-global state owned by whatsmeow's
// store package. All of it is read when a client builds its registration or
// login payload, so every function here must run before Connect or PairQR to
// have any effect.

// deviceOS and devicePlatform are the client identity announced to WhatsApp
// when a device pairs. They are also what the paired phone shows under
// Linked Devices.
const deviceOS = "Chrome"

var devicePlatform = waCompanionReg.DeviceProps_CHROME

// init sets the announced client identity process-wide.
//
// whatsmeow's defaults announce an OS string of "whatsmeow" with an UNKNOWN
// platform type, which distinguishes this client from an official one on
// inspection of the pairing payload alone.
//
// Two things this does not do. It does not rename an already-paired session:
// DeviceProps is read only when building the pairing registration payload,
// so an existing session keeps the identity it registered with until it is
// paired again. And it does not make the client indistinguishable — the
// registration payload still carries a BuildHash derived from the WhatsApp
// Web version (see RefreshWAVersion), the user agent keeps whatsmeow's
// placeholder carrier and manufacturer fields, and the on-wire behaviour of
// the session is unchanged. See the README's "Ban risk" section.
func init() {
	waStore.SetOSInfo(deviceOS, waStore.GetWAVersion())
	waStore.DeviceProps.PlatformType = devicePlatform.Enum()
}

// waVersionTimeout bounds the WhatsApp Web version lookup, so a slow or
// unreachable host delays startup by at most this long. It matches the
// timeout the release-version check in internal/doctor uses.
const waVersionTimeout = 2 * time.Second

// RefreshWAVersion looks up the current WhatsApp Web client version and
// applies it process-wide.
//
// The version feeds two fields: the app version reported in every client
// payload, and the BuildHash in the pairing registration payload, which is
// the md5 of the version string. Without this the build is pinned to
// whichever version was vendored with whatsmeow at compile time, so an
// installation drifts further from any real WhatsApp Web release the longer
// it goes without an upgrade.
//
// This reaches web.whatsapp.com over HTTPS and fetches nothing but the page
// the version number is embedded in. It sends no JID, phone number, or
// message content, and carries no session credentials. Callers should treat
// a failure as non-fatal: a stale version still connects.
func RefreshWAVersion(ctx context.Context) error {
	v, err := whatsmeow.GetLatestVersion(ctx, &http.Client{Timeout: waVersionTimeout})
	if err != nil {
		return fmt.Errorf("look up WhatsApp Web version: %w", err)
	}
	applyWAVersion(*v)
	return nil
}

// applyWAVersion stamps v across every field that carries a version.
//
// SetWAVersion moves the app version and the BuildHash, but not the device
// props version or the user agent's OS version fields, which init seeded
// from the compile-time version. Both calls are needed for the announcement
// to move as a unit rather than straddling two versions.
func applyWAVersion(v waStore.WAVersionContainer) {
	waStore.SetWAVersion(v)
	waStore.SetOSInfo(deviceOS, v)
}

// fullSyncSizeMbLimit caps the on-demand history sync payload. It is a
// ceiling on what we are willing to receive, not a request for that much.
const fullSyncSizeMbLimit = 10240

// RequestFullHistory asks the phone, at pair time, for up to days of history
// instead of whatsmeow's default of "recent" only (which the phone typically
// answers with around three months).
//
// This has effect only on a fresh pair: RequireFullSync and HistorySyncConfig
// are read when building the registration payload, so calling it for a
// session that is already paired does nothing at all. Re-pairing is the only
// way to widen the window of an existing install.
//
// The phone remains the authority on what it actually sends. days is a
// request, not a guarantee, and messages the phone itself no longer holds
// cannot be recovered by asking.
//
// The existing HistorySyncConfig is mutated rather than replaced, so
// whatsmeow's support flags (call log history, group history, reactions and
// polls, and so on) survive. Replacing the struct wholesale would silently
// opt out of the very categories a caller asking for full history wants.
func RequestFullHistory(days uint32) {
	waStore.DeviceProps.RequireFullSync = proto.Bool(true)

	cfg := waStore.DeviceProps.GetHistorySyncConfig()
	if cfg == nil {
		cfg = &waCompanionReg.DeviceProps_HistorySyncConfig{}
		waStore.DeviceProps.HistorySyncConfig = cfg
	}
	cfg.FullSyncDaysLimit = proto.Uint32(days)
	cfg.FullSyncSizeMbLimit = proto.Uint32(fullSyncSizeMbLimit)
}
