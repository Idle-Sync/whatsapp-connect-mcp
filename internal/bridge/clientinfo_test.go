package bridge

import (
	"testing"

	"go.mau.fi/whatsmeow/proto/waCompanionReg"
	waStore "go.mau.fi/whatsmeow/store"
	"google.golang.org/protobuf/proto"
)

// restoreClientInfo snapshots the whatsmeow globals this package mutates and
// restores them when the test ends. They are process-wide, so without this a
// test that changes one would leak into every test that runs after it.
func restoreClientInfo(t *testing.T) {
	t.Helper()

	version := waStore.GetWAVersion()
	os := waStore.DeviceProps.GetOs()
	platform := waStore.DeviceProps.GetPlatformType()
	requireFullSync := waStore.DeviceProps.GetRequireFullSync()
	syncConfig := proto.Clone(waStore.DeviceProps.GetHistorySyncConfig()).(*waCompanionReg.DeviceProps_HistorySyncConfig)

	t.Cleanup(func() {
		waStore.SetWAVersion(version)
		waStore.SetOSInfo(os, version)
		waStore.DeviceProps.PlatformType = platform.Enum()
		waStore.DeviceProps.RequireFullSync = proto.Bool(requireFullSync)
		waStore.DeviceProps.HistorySyncConfig = syncConfig
	})
}

// TestApplyWAVersionMovesEveryVersionField proves the refresh does not leave
// the announcement straddling two versions. SetWAVersion alone moves the app
// version and BuildHash but silently leaves the device props version and the
// user agent OS version at whatever init seeded them with, which would mean
// announcing a current app version alongside a stale OS version — a
// mismatch that does not occur in a real client.
func TestApplyWAVersionMovesEveryVersionField(t *testing.T) {
	restoreClientInfo(t)

	want := waStore.WAVersionContainer{2, 9999, 123456}
	before := waStore.GetWAVersion()
	if before == want {
		t.Fatal("test version equals the current one, so this proves nothing")
	}

	applyWAVersion(want)

	if got := waStore.GetWAVersion(); got != want {
		t.Errorf("WA version = %v, want %v", got, want)
	}
	if got := waStore.DeviceProps.GetVersion(); got.GetPrimary() != want[0] ||
		got.GetSecondary() != want[1] || got.GetTertiary() != want[2] {
		t.Errorf("device props version = %v, want %v", got, want)
	}
	if got := waStore.BaseClientPayload.GetUserAgent().GetOsVersion(); got != want.String() {
		t.Errorf("user agent OS version = %q, want %q", got, want.String())
	}
	if got := waStore.BaseClientPayload.GetUserAgent().GetAppVersion(); got.GetPrimary() != want[0] {
		t.Errorf("user agent app version = %v, want primary %d", got, want[0])
	}

	// The identity must survive a version change: the whole point is to move
	// the version without reverting to whatsmeow's default OS string.
	if got := waStore.DeviceProps.GetOs(); got != deviceOS {
		t.Errorf("announced OS after version refresh = %q, want %q", got, deviceOS)
	}
}

// TestRequestFullHistoryPreservesSupportFlags is the reason RequestFullHistory
// mutates the existing config instead of assigning a fresh struct. whatsmeow's
// default HistorySyncConfig opts into categories like call log and group
// history; replacing the struct to set a day limit would silently opt back
// out of them, so asking for more history would deliver less of it.
func TestRequestFullHistoryPreservesSupportFlags(t *testing.T) {
	restoreClientInfo(t)

	before := waStore.DeviceProps.GetHistorySyncConfig()
	if !before.GetSupportCallLogHistory() || !before.GetSupportGroupHistory() {
		t.Skip("whatsmeow's defaults no longer set the flags this test guards")
	}
	quotaBefore := before.GetStorageQuotaMb()

	RequestFullHistory(fullHistoryTestDays)

	cfg := waStore.DeviceProps.GetHistorySyncConfig()
	if !waStore.DeviceProps.GetRequireFullSync() {
		t.Error("RequireFullSync = false, want true")
	}
	if got := cfg.GetFullSyncDaysLimit(); got != fullHistoryTestDays {
		t.Errorf("FullSyncDaysLimit = %d, want %d", got, fullHistoryTestDays)
	}
	if got := cfg.GetFullSyncSizeMbLimit(); got != fullSyncSizeMbLimit {
		t.Errorf("FullSyncSizeMbLimit = %d, want %d", got, fullSyncSizeMbLimit)
	}

	if !cfg.GetSupportCallLogHistory() {
		t.Error("SupportCallLogHistory was cleared; the config was replaced, not mutated")
	}
	if !cfg.GetSupportGroupHistory() {
		t.Error("SupportGroupHistory was cleared; the config was replaced, not mutated")
	}
	if got := cfg.GetStorageQuotaMb(); got != quotaBefore {
		t.Errorf("StorageQuotaMb = %d, want %d (untouched)", got, quotaBefore)
	}
}

// fullHistoryTestDays is deliberately not the production constant, so the
// test would still catch RequestFullHistory ignoring its argument.
const fullHistoryTestDays = 1234
