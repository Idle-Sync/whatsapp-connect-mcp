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
	// accountFileName holds the settings that belong to one WhatsApp
	// account rather than to this machine.
	accountFileName = "account.json"

	// mediaDirName is the directory outbound media may be read from unless
	// config.json widens it.
	mediaDirName = "outbox"

	defaultRateBurst      = 3
	defaultRatePerSeconds = 12
)

// DefaultHTTPPort is the port the http transport defaults to everywhere a
// port is offered or assumed (setup's prompt, service install): outside the
// common dev-server range, so a shared server on it is unlikely to collide
// with anything.
const DefaultHTTPPort = 2178

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
	// ChatScope selects which chats the MCP tool surface may read:
	// ScopeAll (the default, and the behaviour before this setting
	// existed) or ScopeAllowlist, where ReadableChats is a strict
	// allowlist. The mode is separate from the list precisely so that an
	// EMPTY allowlist can mean "nothing readable yet" — the state right
	// after someone chooses to pick their chats later — instead of
	// collapsing into "everything readable", which is the one reading that
	// would silently hand out more access than was asked for.
	//
	// The dashboard is deliberately not subject to any of this: it is the
	// local human's own window onto their own messages, not an agent's.
	//
	// This is one setting for the whole server, not one per MCP client.
	// Every client authenticates with the same bearer token, so the server
	// cannot tell one from another; per-client scopes need per-client
	// tokens first.
	ChatScope     string   `json:"chat_scope"`
	ReadableChats []string `json:"readable_chats"`
}

// The two values ChatScope takes. An unrecognised or empty value is
// normalised to ScopeAll by Load, except that a config carrying a
// non-empty ReadableChats with no mode set is read as ScopeAllowlist: that
// combination can only come from a hand-edit meaning to restrict, and
// honouring the list is the reading that cannot leak.
const (
	ScopeAll       = "all"
	ScopeAllowlist = "allowlist"
)

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
func Load(dir string) (Config, error) { return LoadFor(dir, "") }

// LoadFor reads the machine-wide settings from dir's config.json and, when
// accountDir is non-empty, this account's own settings from its
// account.json — merging both into one Config, so every caller keeps
// reading cfg.TrustedJIDs and cfg.ReadableChats without caring which file
// they came from.
//
// The split is by ownership. A trust grant and a readable-chat allowlist
// are statements about one WhatsApp account's contacts, and carrying them
// across a re-pair would auto-send from the wrong number and expose the
// wrong chats. Rate limits, the outbox roots, and the HTTP port are
// statements about this machine, and are shared deliberately: one rate
// limiter across accounts is safer than one per account, not worse.
//
// An empty accountDir keeps the whole config in one file, which is what
// an unpaired install and the config package's own tests want.
func LoadFor(dir, accountDir string) (Config, error) {
	c, err := readFile(filepath.Join(dir, fileName))
	if err != nil {
		return Config{}, err
	}

	if accountDir != "" {
		path := filepath.Join(accountDir, accountFileName)
		if _, statErr := os.Stat(path); statErr == nil {
			acct, err := readFile(path)
			if err != nil {
				return Config{}, err
			}
			// Once written, the account file is authoritative for the keys it
			// owns; anything left in config.json for them is pre-split residue.
			c.TrustedJIDs = acct.TrustedJIDs
			c.ChatScope = acct.ChatScope
			c.ReadableChats = acct.ReadableChats
		}
		// No account file yet means SplitLegacy has not run. The values still
		// in config.json are this account's — it is the only one there has
		// ever been — so they are kept rather than blanked, and a `trust
		// --list` between upgrading and the next serve shows the truth.
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

// readFile decodes one settings file. A missing file is the zero Config,
// not an error: both halves are optional until something is written.
func readFile(path string) (Config, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- path is caller-supplied (config.Dir() or a test dir), not network input
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Config{}, nil
		}
		return Config{}, fmt.Errorf("read config file: %w", err)
	}
	var c Config
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return Config{}, errors.New("config file is not valid JSON")
	}
	return c, nil
}

// Save writes c to dir's config.json.
func Save(dir string, c Config) error { return SaveFor(dir, "", c) }

// SaveFor writes c back to the two files LoadFor read it from. With an
// empty accountDir everything goes to config.json.
func SaveFor(dir, accountDir string, c Config) error {
	if accountDir == "" {
		return writeFile(dir, fileName, c)
	}
	if err := writeFile(accountDir, accountFileName, Config{
		TrustedJIDs:   c.TrustedJIDs,
		ChatScope:     c.ChatScope,
		ReadableChats: c.ReadableChats,
	}); err != nil {
		return err
	}
	return writeFile(dir, fileName, Config{
		RateBurst:      c.RateBurst,
		RatePerSeconds: c.RatePerSeconds,
		MediaRoots:     c.MediaRoots,
	})
}

// SplitLegacy moves the account-owned keys out of a pre-0.3.8 config.json
// into accountDir's account.json, leaving the machine-wide keys behind.
//
// It runs once, and only when there is no account.json yet: after that the
// account file is authoritative and a stale key left in config.json is
// ignored rather than re-applied. Doing nothing when there is nothing to
// move makes it safe to call on every start.
func SplitLegacy(dir, accountDir string) (bool, error) {
	if accountDir == "" {
		return false, nil
	}
	if _, err := os.Stat(filepath.Join(accountDir, accountFileName)); err == nil {
		return false, nil // already split
	}
	legacy, err := readFile(filepath.Join(dir, fileName))
	if err != nil {
		return false, err
	}
	if len(legacy.TrustedJIDs) == 0 && legacy.ChatScope == "" && len(legacy.ReadableChats) == 0 {
		return false, nil // nothing account-owned to move
	}
	if err := SaveFor(dir, accountDir, legacy); err != nil {
		return false, err
	}
	return true, nil
}

// writeFile atomically writes c to dir/name via a temp file plus rename,
// with file mode 0600.
func writeFile(dir, name string, c Config) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}

	tmp, err := os.CreateTemp(dir, name+".tmp-*")
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
	if err := os.Rename(tmpPath, filepath.Join(dir, name)); err != nil {
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

// ScopeActive reports whether a chat allowlist is in force.
func (c Config) ScopeActive() bool { return normalizeScope(c) == ScopeAllowlist }

// IsReadable reports whether an agent may read chat jid.
func (c Config) IsReadable(jid string) bool {
	if !c.ScopeActive() {
		return true
	}
	for _, r := range c.ReadableChats {
		if r == jid {
			return true
		}
	}
	return false
}

// normalizeScope resolves the stored mode, treating a bare list with no
// mode as an allowlist and anything unrecognised as ScopeAll.
func normalizeScope(c Config) string {
	switch c.ChatScope {
	case ScopeAllowlist:
		return ScopeAllowlist
	case ScopeAll:
		return ScopeAll
	default:
		if len(c.ReadableChats) > 0 {
			return ScopeAllowlist
		}
		return ScopeAll
	}
}

// ScopeReader answers readable-chat checks against the CURRENT config.json
// rather than a startup snapshot, so editing the scope takes effect in a
// running serve without a restart — the same stance, and the same cost
// argument, as TrustReader.
//
// The failure mode is deliberately asymmetric with TrustReader's. A
// transiently unreadable file keeps the last good list in force, so a
// half-written config can neither widen the scope nor lock an agent out of
// chats it was allowed a moment ago.
type ScopeReader struct {
	dir        string
	accountDir string

	mu       sync.Mutex
	lastGood Config
}

// NewScopeReader builds a ScopeReader over dir's config.json and the
// account settings in accountDir. An empty accountDir reads everything
// from config.json.
func NewScopeReader(dir, accountDir string) *ScopeReader {
	r := &ScopeReader{dir: dir, accountDir: accountDir}
	if c, err := LoadFor(dir, accountDir); err == nil {
		r.lastGood = c
	}
	return r
}

// Readable reports whether chat jid is inside the configured scope as of
// the file's current content (or the last good read).
func (r *ScopeReader) Readable(jid string) bool { return r.current().IsReadable(jid) }

// Active reports whether an allowlist is in force. Callers use it to skip
// filtering work entirely in the common unscoped case.
func (r *ScopeReader) Active() bool { return r.current().ScopeActive() }

// List returns the current allowlist. The caller owns the copy.
func (r *ScopeReader) List() []string {
	l := r.current().ReadableChats
	out := make([]string, len(l))
	copy(out, l)
	return out
}

func (r *ScopeReader) current() Config {
	r.mu.Lock()
	defer r.mu.Unlock()
	if c, err := LoadFor(r.dir, r.accountDir); err == nil {
		r.lastGood = c
	}
	return r.lastGood
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
	dir        string
	accountDir string

	mu       sync.Mutex
	lastGood []string
}

// NewTrustReader builds a TrustReader over dir's config.json and the
// account settings in accountDir. An empty accountDir reads everything
// from config.json.
func NewTrustReader(dir, accountDir string) *TrustReader {
	r := &TrustReader{dir: dir, accountDir: accountDir}
	if c, err := LoadFor(dir, accountDir); err == nil {
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

	if c, err := LoadFor(r.dir, r.accountDir); err == nil {
		r.lastGood = c.TrustedJIDs
	}
	for _, t := range r.lastGood {
		if t == jid {
			return true
		}
	}
	return false
}
