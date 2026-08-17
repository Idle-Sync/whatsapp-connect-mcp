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
