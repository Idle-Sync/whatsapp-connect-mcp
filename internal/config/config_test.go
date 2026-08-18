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

	r := NewTrustReader(dir)
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

	r := NewTrustReader(dir)
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

	r := NewTrustReader(dir)
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
	r := NewTrustReader(t.TempDir())
	if r.Trusted("111@s.whatsapp.net") {
		t.Fatal("Trusted() = true with no config file at all")
	}
}
