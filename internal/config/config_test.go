package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDirCreatesAndReturnsPath(t *testing.T) {
	base := t.TempDir()
	switch runtime.GOOS {
	case "windows":
		t.Setenv("AppData", base)
	case "darwin":
		t.Setenv("HOME", base)
	default:
		t.Setenv("XDG_CONFIG_HOME", base)
	}

	dir, err := Dir()
	if err != nil {
		t.Fatalf("Dir() error: %v", err)
	}
	if filepath.Base(dir) != "whatsapp-connect-mcp" {
		t.Fatalf("Dir() = %q, want basename whatsapp-connect-mcp", dir)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat created dir: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("Dir() path %q is not a directory", dir)
	}
	if runtime.GOOS != "windows" {
		if perm := info.Mode().Perm(); perm != 0o700 {
			t.Fatalf("dir mode = %o, want 0700", perm)
		}
	}
}

func TestLoadMissingReturnsDefaults(t *testing.T) {
	dir := t.TempDir()

	got, err := Load(dir)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	want := Config{RateBurst: 3, RatePerSeconds: 12}
	if got.RateBurst != want.RateBurst || got.RatePerSeconds != want.RatePerSeconds || len(got.TrustedJIDs) != 0 {
		t.Fatalf("Load() = %+v, want %+v", got, want)
	}
}

func TestLoadPartialConfigFileGetsRateDefaults(t *testing.T) {
	dir := t.TempDir()
	body := `{"trusted_jids": ["111@s.whatsapp.net"], "rate_burst": 0, "rate_per_seconds": 0}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(body), 0o600); err != nil {
		t.Fatalf("seed config.json: %v", err)
	}

	got, err := Load(dir)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if got.RateBurst != 3 || got.RatePerSeconds != 12 {
		t.Fatalf("Load() rate = (%d, %d), want defaults (3, 12) for a hand-written config missing them", got.RateBurst, got.RatePerSeconds)
	}
	if len(got.TrustedJIDs) != 1 || got.TrustedJIDs[0] != "111@s.whatsapp.net" {
		t.Fatalf("Load() TrustedJIDs = %v, want the file's own value preserved", got.TrustedJIDs)
	}
}

func TestLoadExplicitRateValuesAreRespected(t *testing.T) {
	dir := t.TempDir()
	body := `{"trusted_jids": [], "rate_burst": 7, "rate_per_seconds": 30}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(body), 0o600); err != nil {
		t.Fatalf("seed config.json: %v", err)
	}

	got, err := Load(dir)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if got.RateBurst != 7 || got.RatePerSeconds != 30 {
		t.Fatalf("Load() rate = (%d, %d), want the file's own explicit values (7, 30) untouched", got.RateBurst, got.RatePerSeconds)
	}
}

func TestSaveThenLoadRoundTrips(t *testing.T) {
	dir := t.TempDir()
	want := Config{
		TrustedJIDs:    []string{"111@s.whatsapp.net", "222@s.whatsapp.net"},
		RateBurst:      7,
		RatePerSeconds: 30,
	}

	if err := Save(dir, want); err != nil {
		t.Fatalf("Save() error: %v", err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if got.RateBurst != want.RateBurst || got.RatePerSeconds != want.RatePerSeconds {
		t.Fatalf("Load() = %+v, want %+v", got, want)
	}
	if len(got.TrustedJIDs) != len(want.TrustedJIDs) {
		t.Fatalf("TrustedJIDs = %v, want %v", got.TrustedJIDs, want.TrustedJIDs)
	}
	for i := range want.TrustedJIDs {
		if got.TrustedJIDs[i] != want.TrustedJIDs[i] {
			t.Fatalf("TrustedJIDs[%d] = %q, want %q", i, got.TrustedJIDs[i], want.TrustedJIDs[i])
		}
	}
}

func TestSaveFileMode(t *testing.T) {
	dir := t.TempDir()
	if err := Save(dir, Config{RateBurst: 3, RatePerSeconds: 12}); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	info, err := os.Stat(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("stat config.json: %v", err)
	}

	if runtime.GOOS == "windows" {
		t.Skip("file mode bits are not meaningful on windows")
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("config.json mode = %o, want 0600", perm)
	}
}

func TestLoadCorruptedJSON(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{not valid json"), 0o600); err != nil {
		t.Fatalf("seed corrupted config.json: %v", err)
	}

	_, err := Load(dir)
	if err == nil {
		t.Fatal("Load() error = nil, want error for corrupted JSON")
	}
	if !strings.Contains(err.Error(), "config file is not valid JSON") {
		t.Fatalf("Load() error = %q, want it to mention the category", err.Error())
	}
	if strings.Contains(err.Error(), "not valid json") {
		t.Fatalf("Load() error = %q, must not echo file contents", err.Error())
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	dir := t.TempDir()
	body := `{"trusted_jids": [], "rate_burst": 3, "rate_per_seconds": 12, "extra_field": true}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(body), 0o600); err != nil {
		t.Fatalf("seed config.json: %v", err)
	}

	_, err := Load(dir)
	if err == nil {
		t.Fatal("Load() error = nil, want error for unknown field")
	}
}

func TestIsTrusted(t *testing.T) {
	c := Config{TrustedJIDs: []string{"111@s.whatsapp.net", "222@s.whatsapp.net"}}

	tests := []struct {
		name string
		jid  string
		want bool
	}{
		{"exact match", "111@s.whatsapp.net", true},
		{"other exact match", "222@s.whatsapp.net", true},
		{"no match", "333@s.whatsapp.net", false},
		{"prefix only is not a match", "111", false},
		{"empty jid", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := c.IsTrusted(tt.jid); got != tt.want {
				t.Fatalf("IsTrusted(%q) = %v, want %v", tt.jid, got, tt.want)
			}
		})
	}
}

// TestLoadDefaultsMediaRootsToOutbox covers all three ways the key can be
// absent. Each must land on the dedicated directory rather than an empty
// list, because an empty list denies every media send — a config predating
// this key would otherwise silently break sending on upgrade.
func TestLoadDefaultsMediaRootsToOutbox(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string // empty means: write no file at all
	}{
		{"no config file", ""},
		{"file without the key", `{"trusted_jids": [], "rate_burst": 3, "rate_per_seconds": 12}`},
		{"key present but empty", `{"trusted_jids": [], "rate_burst": 3, "rate_per_seconds": 12, "media_roots": []}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.body != "" {
				if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(tc.body), 0o600); err != nil {
					t.Fatalf("seed config.json: %v", err)
				}
			}

			got, err := Load(dir)
			if err != nil {
				t.Fatalf("Load() error: %v", err)
			}
			want := DefaultMediaDir(dir)
			if len(got.MediaRoots) != 1 || got.MediaRoots[0] != want {
				t.Fatalf("Load() MediaRoots = %v, want [%s]", got.MediaRoots, want)
			}
		})
	}
}

func TestLoadExplicitMediaRootsAreRespected(t *testing.T) {
	dir := t.TempDir()
	body := `{"trusted_jids": [], "rate_burst": 3, "rate_per_seconds": 12, "media_roots": ["/srv/pics", "/srv/docs"]}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(body), 0o600); err != nil {
		t.Fatalf("seed config.json: %v", err)
	}

	got, err := Load(dir)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if len(got.MediaRoots) != 2 || got.MediaRoots[0] != "/srv/pics" || got.MediaRoots[1] != "/srv/docs" {
		t.Fatalf("Load() MediaRoots = %v, want the file's own values preserved", got.MediaRoots)
	}
}

// TrustReader is what lets `trust --add` take effect in a running serve
// without a restart (issue #11): the trust list is re-read on every check
// instead of being a startup snapshot.

func TestTrustReaderSeesAdditionsLive(t *testing.T) {
	dir := t.TempDir()
	if err := Save(dir, Config{}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	r := NewTrustReader(dir, "")
	const jid = "111@s.whatsapp.net"
	if r.Trusted(jid) {
		t.Fatal("Trusted() = true before the JID was added")
	}

	if err := Save(dir, Config{TrustedJIDs: []string{jid}}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if !r.Trusted(jid) {
		t.Fatal("Trusted() = false after trust --add wrote the config; the running server must see it without a restart")
	}
}

func TestTrustReaderSeesRemovalsLive(t *testing.T) {
	dir := t.TempDir()
	const jid = "111@s.whatsapp.net"
	if err := Save(dir, Config{TrustedJIDs: []string{jid}}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	r := NewTrustReader(dir, "")
	if !r.Trusted(jid) {
		t.Fatal("Trusted() = false for a listed JID")
	}

	if err := Save(dir, Config{}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if r.Trusted(jid) {
		t.Fatal("Trusted() = true after trust --remove; revocation must apply without a restart")
	}
}

// A transiently broken config.json (e.g. mid hand-edit) must neither grant
// nor revoke: the last successfully read list stays in force.
func TestTrustReaderKeepsLastGoodListOnBrokenFile(t *testing.T) {
	dir := t.TempDir()
	const jid = "111@s.whatsapp.net"
	if err := Save(dir, Config{TrustedJIDs: []string{jid}}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	r := NewTrustReader(dir, "")
	if !r.Trusted(jid) {
		t.Fatal("Trusted() = false for a listed JID")
	}

	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("corrupt config: %v", err)
	}
	if !r.Trusted(jid) {
		t.Fatal("Trusted() = false while the file is broken; the last good list must stay in force")
	}
	if r.Trusted("222@s.whatsapp.net") {
		t.Fatal("Trusted() granted an unlisted JID while the file is broken")
	}
}

func TestTrustReaderNoFileMeansNoTrust(t *testing.T) {
	r := NewTrustReader(t.TempDir(), "")
	if r.Trusted("111@s.whatsapp.net") {
		t.Fatal("Trusted() = true with no config file at all")
	}
}

// The scope mode is what separates "no limit" from "nothing allowed yet",
// so an empty list has to mean opposite things under the two modes.
func TestChatScopeSemantics(t *testing.T) {
	tests := []struct {
		name       string
		cfg        Config
		wantActive bool
		readable   map[string]bool
	}{
		{
			name:       "zero value reads everything",
			cfg:        Config{},
			wantActive: false,
			readable:   map[string]bool{"a@s.whatsapp.net": true, "b@s.whatsapp.net": true},
		},
		{
			name:       "explicit all ignores a leftover list",
			cfg:        Config{ChatScope: ScopeAll, ReadableChats: []string{"a@s.whatsapp.net"}},
			wantActive: false,
			readable:   map[string]bool{"a@s.whatsapp.net": true, "b@s.whatsapp.net": true},
		},
		{
			name:       "allowlist permits only what it lists",
			cfg:        Config{ChatScope: ScopeAllowlist, ReadableChats: []string{"a@s.whatsapp.net"}},
			wantActive: true,
			readable:   map[string]bool{"a@s.whatsapp.net": true, "b@s.whatsapp.net": false},
		},
		{
			name:       "allowlist with an empty list permits nothing",
			cfg:        Config{ChatScope: ScopeAllowlist},
			wantActive: true,
			readable:   map[string]bool{"a@s.whatsapp.net": false, "b@s.whatsapp.net": false},
		},
		{
			name:       "a hand-added list with no mode is read as a restriction",
			cfg:        Config{ReadableChats: []string{"a@s.whatsapp.net"}},
			wantActive: true,
			readable:   map[string]bool{"a@s.whatsapp.net": true, "b@s.whatsapp.net": false},
		},
		{
			name:       "an unrecognised mode falls back to reading everything",
			cfg:        Config{ChatScope: "banana"},
			wantActive: false,
			readable:   map[string]bool{"a@s.whatsapp.net": true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.ScopeActive(); got != tt.wantActive {
				t.Errorf("ScopeActive() = %v, want %v", got, tt.wantActive)
			}
			for jid, want := range tt.readable {
				if got := tt.cfg.IsReadable(jid); got != want {
					t.Errorf("IsReadable(%q) = %v, want %v", jid, got, want)
				}
			}
		})
	}
}

// ScopeReader answers from the file as it stands now, so an edit lands in
// a running serve without a restart.
func TestScopeReaderTracksTheFile(t *testing.T) {
	dir := t.TempDir()
	r := NewScopeReader(dir, "")

	if !r.Readable("a@s.whatsapp.net") || r.Active() {
		t.Fatal("a fresh data dir should read every chat")
	}

	if err := Save(dir, Config{ChatScope: ScopeAllowlist, ReadableChats: []string{"a@s.whatsapp.net"}}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if !r.Active() {
		t.Error("Active() = false after the file turned the scope on")
	}
	if !r.Readable("a@s.whatsapp.net") || r.Readable("b@s.whatsapp.net") {
		t.Error("Readable did not follow the file")
	}
	if got := r.List(); len(got) != 1 || got[0] != "a@s.whatsapp.net" {
		t.Errorf("List() = %v", got)
	}
}

// A trust grant and a readable-chat list belong to one WhatsApp account;
// the rate limits and outbox roots belong to this machine. Saving must put
// each in the file that survives the right events.
func TestConfigSplitsAccountFromMachine(t *testing.T) {
	dir, acctA, acctB := t.TempDir(), t.TempDir(), t.TempDir()

	err := SaveFor(dir, acctA, Config{
		TrustedJIDs:    []string{"a@s.whatsapp.net"},
		ChatScope:      ScopeAllowlist,
		ReadableChats:  []string{"a@s.whatsapp.net"},
		RateBurst:      7,
		RatePerSeconds: 30,
		MediaRoots:     []string{"/tmp/roots"},
	})
	if err != nil {
		t.Fatalf("SaveFor: %v", err)
	}

	// Account A sees its own trust and scope.
	got, err := LoadFor(dir, acctA)
	if err != nil {
		t.Fatalf("LoadFor: %v", err)
	}
	if len(got.TrustedJIDs) != 1 || !got.ScopeActive() {
		t.Errorf("account A lost its own settings: %+v", got)
	}
	if got.RateBurst != 7 || got.MediaRoots[0] != "/tmp/roots" {
		t.Errorf("machine settings did not survive: %+v", got)
	}

	// Account B, on the same machine, starts clean — nobody it trusts, no
	// chat list — but shares the machine's limits and roots.
	got, err = LoadFor(dir, acctB)
	if err != nil {
		t.Fatalf("LoadFor: %v", err)
	}
	if len(got.TrustedJIDs) != 0 {
		t.Errorf("account B inherited account A's trust list: %v", got.TrustedJIDs)
	}
	if got.ScopeActive() || len(got.ReadableChats) != 0 {
		t.Errorf("account B inherited account A's readable chats: %+v", got)
	}
	if got.RateBurst != 7 || got.MediaRoots[0] != "/tmp/roots" {
		t.Errorf("account B did not share the machine settings: %+v", got)
	}
}

// Between upgrading and the next serve there is no account file yet. The
// values still in config.json are this account's — it is the only one
// there has ever been — so they must be read, not blanked.
func TestConfigReadsLegacyValuesBeforeTheSplit(t *testing.T) {
	dir, acct := t.TempDir(), t.TempDir()
	if err := Save(dir, Config{TrustedJIDs: []string{"legacy@s.whatsapp.net"}, ChatScope: ScopeAllowlist}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := LoadFor(dir, acct)
	if err != nil {
		t.Fatalf("LoadFor: %v", err)
	}
	if len(got.TrustedJIDs) != 1 || got.TrustedJIDs[0] != "legacy@s.whatsapp.net" {
		t.Fatalf("legacy trust list was dropped: %+v", got)
	}

	// Splitting moves them across, after which the account file is the one
	// that counts.
	split, err := SplitLegacy(dir, acct)
	if err != nil || !split {
		t.Fatalf("SplitLegacy = %v, %v", split, err)
	}
	got, err = LoadFor(dir, acct)
	if err != nil {
		t.Fatalf("LoadFor: %v", err)
	}
	if len(got.TrustedJIDs) != 1 {
		t.Errorf("trust list lost in the split: %+v", got)
	}

	// A second run has nothing left to move.
	if split, err := SplitLegacy(dir, acct); err != nil || split {
		t.Errorf("second SplitLegacy = %v, %v; want no-op", split, err)
	}
}
