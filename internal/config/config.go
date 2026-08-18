// Package config resolves the on-disk data directory and loads or saves
// config.json, the user-editable trust and rate-limit settings.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

const (
	dirName  = "whatsapp-connect-mcp"
	fileName = "config.json"

	// mediaDirName is the directory outbound media may be read from unless
	// config.json widens it.
	mediaDirName = "outbox"

	defaultRateBurst      = 3
	defaultRatePerSeconds = 12
)

// Config holds user-editable settings persisted in config.json.
type Config struct {
	TrustedJIDs    []string `json:"trusted_jids"`
	RateBurst      int      `json:"rate_burst"`
	RatePerSeconds int      `json:"rate_per_seconds"`
	// MediaRoots lists the directories an outbound send may read files
	// from. Defaulting it to a single dedicated directory, rather than to
	// the whole filesystem, is what keeps a send tool from being able to
	// attach an arbitrary readable file.
	MediaRoots []string `json:"media_roots"`
}

// DefaultMediaDir returns the directory outbound media is read from when
// config.json does not say otherwise.
func DefaultMediaDir(dir string) string {
	return filepath.Join(dir, mediaDirName)
}

// Dir returns the application's data directory under the OS user config
// directory, creating it with mode 0700 if it does not already exist.
func Dir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config dir: %w", err)
	}
	dir := filepath.Join(base, dirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create data dir: %w", err)
	}
	return dir, nil
}

// Load reads config.json from dir. A missing file returns the defaults
// (RateBurst 3, RatePerSeconds 12); those same defaults are also applied
// field-by-field to a file that exists but predates one or both rate keys
// (or has them explicitly zeroed), so a hand-edited config.json missing
// "rate_burst"/"rate_per_seconds" doesn't leave every send permanently
// rate-limited to zero.
func Load(dir string) (Config, error) {
	data, err := os.ReadFile(filepath.Join(dir, fileName)) // #nosec G304 -- dir is caller-supplied (config.Dir() or a test dir), not network input
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Config{
				RateBurst:      defaultRateBurst,
				RatePerSeconds: defaultRatePerSeconds,
				MediaRoots:     []string{DefaultMediaDir(dir)},
			}, nil
		}
		return Config{}, fmt.Errorf("read config file: %w", err)
	}

	var c Config
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return Config{}, errors.New("config file is not valid JSON")
	}

	if c.RateBurst <= 0 {
		c.RateBurst = defaultRateBurst
	}
	if c.RatePerSeconds <= 0 {
		c.RatePerSeconds = defaultRatePerSeconds
	}
	// Applied the same way as the rate defaults above, so a config.json
	// written before this key existed gets the dedicated directory rather
	// than an empty list — which would refuse every media send.
	if len(c.MediaRoots) == 0 {
		c.MediaRoots = []string{DefaultMediaDir(dir)}
	}
	return c, nil
}

// Save atomically writes c to config.json in dir via a temp file plus
// rename, with file mode 0600.
func Save(dir string, c Config) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}

	tmp, err := os.CreateTemp(dir, fileName+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp config file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }() // no-op once renamed away

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp config file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp config file: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		return fmt.Errorf("set config file mode: %w", err)
	}
	if err := os.Rename(tmpPath, filepath.Join(dir, fileName)); err != nil {
		return fmt.Errorf("rename temp config file: %w", err)
	}
	return nil
}

// IsTrusted reports whether jid exactly matches an entry in TrustedJIDs.
func (c Config) IsTrusted(jid string) bool {
	for _, t := range c.TrustedJIDs {
		if t == jid {
			return true
		}
	}
	return false
}

// TrustReader answers trust checks against the CURRENT config.json rather
// than a startup snapshot, so `trust --add`/`--remove` take effect in a
// running serve without a restart (issue #11). The file is tiny and sends
// are rate-limited to at most one per five seconds, so a read per check
// costs nothing; a transiently unreadable or invalid file (a hand-edit in
// progress) neither grants nor revokes — the last successfully read list
// stays in force. The trust decision stays CLI-only exactly as before: no
// MCP tool writes config.json.
type TrustReader struct {
	dir string

	mu       sync.Mutex
	lastGood []string
}

// NewTrustReader builds a TrustReader over dir's config.json.
func NewTrustReader(dir string) *TrustReader {
	r := &TrustReader{dir: dir}
	if c, err := Load(dir); err == nil {
		r.lastGood = c.TrustedJIDs
	}
	return r
}

// Trusted reports whether jid is on the trust list as of the file's
// current content (or the last good read, when the file is momentarily
// unreadable).
func (r *TrustReader) Trusted(jid string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	if c, err := Load(r.dir); err == nil {
		r.lastGood = c.TrustedJIDs
	}
	for _, t := range r.lastGood {
		if t == jid {
			return true
		}
	}
	return false
}
