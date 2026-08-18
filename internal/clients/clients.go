// Package clients detects locally installed MCP clients, and injects or
// removes this program's entry in their configuration files.
//
// Every supported client stores its MCP server list as a top-level
// "mcpServers" JSON object keyed by server name, so a single codec handles
// all of them: unrelated top-level keys and sibling server entries are
// preserved, only the "whatsapp" key is touched.
package clients

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
)

// entryKey is the mcpServers key this program injects and removes.
const entryKey = "whatsapp"

// Client describes one MCP client on the local machine.
type Client struct {
	Name       string // human-readable client name
	ConfigPath string // absolute path to the client's MCP config file
	Installed  bool   // whether the client's own data directory exists
	Injected   bool   // whether the config currently has our entry
}

// mcpEntry is the value written under mcpServers["whatsapp"]: a stdio
// entry carries Command/Args, an http entry carries Type/URL/Headers.
// Every field is omitempty so neither shape leaks the other's keys.
type mcpEntry struct {
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Type    string            `json:"type,omitempty"`
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

// clientDef is a known client's path resolution logic.
type clientDef struct {
	name string
	// configPath returns the client's MCP config file path for home.
	configPath func(home string) string
	// installedDir returns the directory whose existence indicates the
	// client itself is installed, independent of whether it has ever
	// written an MCP config.
	installedDir func(home string) string
}

func knownClients() []clientDef {
	return []clientDef{
		{
			name:         "Claude Desktop",
			configPath:   claudeDesktopConfigPath,
			installedDir: func(home string) string { return filepath.Dir(claudeDesktopConfigPath(home)) },
		},
		{
			name:         "Cursor",
			configPath:   func(home string) string { return filepath.Join(home, ".cursor", "mcp.json") },
			installedDir: func(home string) string { return filepath.Join(home, ".cursor") },
		},
		{
			name:         "Windsurf",
			configPath:   func(home string) string { return filepath.Join(home, ".codeium", "windsurf", "mcp_config.json") },
			installedDir: func(home string) string { return filepath.Join(home, ".codeium", "windsurf") },
		},
		{
			name:         "Cline",
			configPath:   clineConfigPath,
			installedDir: func(home string) string { return filepath.Dir(filepath.Dir(clineConfigPath(home))) },
		},
		{
			name:         "Claude Code",
			configPath:   func(home string) string { return filepath.Join(home, ".claude.json") },
			installedDir: func(home string) string { return filepath.Join(home, ".claude") },
		},
	}
}

// claudeDesktopConfigPath returns claude_desktop_config.json's path under
// home, following Claude Desktop's per-OS application-support directory.
func claudeDesktopConfigPath(home string) string {
	switch runtime.GOOS {
	case "windows":
		return filepath.Join(home, "AppData", "Roaming", "Claude", "claude_desktop_config.json")
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Claude", "claude_desktop_config.json")
	default:
		return filepath.Join(home, ".config", "Claude", "claude_desktop_config.json")
	}
}

// clineConfigPath returns cline_mcp_settings.json's path under home,
// following the VS Code extension globalStorage layout Cline uses.
func clineConfigPath(home string) string {
	const ext = "saoudrizwan.claude-dev"
	switch runtime.GOOS {
	case "windows":
		return filepath.Join(home, "AppData", "Roaming", "Code", "User", "globalStorage", ext, "settings", "cline_mcp_settings.json")
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Code", "User", "globalStorage", ext, "settings", "cline_mcp_settings.json")
	default:
		return filepath.Join(home, ".config", "Code", "User", "globalStorage", ext, "settings", "cline_mcp_settings.json")
	}
}

// Detect reports every known MCP client under home, whether each is
// installed on this machine, and whether it currently has our entry. home
// is the user's home directory, passed in rather than read from the
// environment so callers can point it at a test directory.
func Detect(home string) []Client {
	defs := knownClients()
	out := make([]Client, 0, len(defs))
	for _, d := range defs {
		path := d.configPath(home)
		installed := isDir(d.installedDir(home))
		_, injected := InjectedCommand(path)
		out = append(out, Client{
			Name:       d.name,
			ConfigPath: path,
			Installed:  installed,
			Injected:   injected,
		})
	}
	return out
}

func isDir(path string) bool {
	info, err := os.Stat(path) // #nosec G304 -- path is derived from a caller-supplied home directory, not network input
	return err == nil && info.IsDir()
}

// Inject writes an mcpServers["whatsapp"] entry pointing at binaryPath into
// the config file at configPath, running it as "<binaryPath> serve". It
// preserves every other key in the file, creates the file (and its parent
// directory) if missing, and is idempotent. A config file that exists but
// fails to parse is left untouched and reported as a category error.
func Inject(configPath, binaryPath string) error {
	root, err := loadRawObject(configPath)
	if err != nil {
		return err
	}
	servers, err := extractObject(root, "mcpServers")
	if err != nil {
		return err
	}

	entryData, err := json.Marshal(mcpEntry{Command: binaryPath, Args: []string{"serve"}})
	if err != nil {
		return errUnwritable
	}
	servers[entryKey] = entryData

	serversData, err := json.Marshal(servers)
	if err != nil {
		return errUnwritable
	}
	root["mcpServers"] = serversData

	return writeAtomic(configPath, root)
}

// InjectHTTP writes an mcpServers["whatsapp"] entry that connects to an
// already-running shared server at url over streamable HTTP,
// authenticating with token as a bearer Authorization header. It fully
// replaces any existing entry (a stale stdio command must not survive a
// transport switch), preserves every other key in the file, and creates
// the file if missing. Unlike the stdio entry, nothing in it spawns a
// process: the user runs `serve --http` themselves.
func InjectHTTP(configPath, url, token string) error {
	root, err := loadRawObject(configPath)
	if err != nil {
		return err
	}
	servers, err := extractObject(root, "mcpServers")
	if err != nil {
		return err
	}

	entryData, err := json.Marshal(mcpEntry{
		Type:    "http",
		URL:     url,
		Headers: map[string]string{"Authorization": "Bearer " + token},
	})
	if err != nil {
		return errUnwritable
	}
	servers[entryKey] = entryData

	serversData, err := json.Marshal(servers)
	if err != nil {
		return errUnwritable
	}
	root["mcpServers"] = serversData

	return writeAtomic(configPath, root)
}

// Remove deletes the mcpServers["whatsapp"] entry from the config file at
// configPath, leaving every other key untouched. Removing from a config
// that doesn't exist, or that has no such entry, is a no-op. A config file
// that exists but fails to parse is left untouched and reported as a
// category error.
func Remove(configPath string) error {
	root, err := loadRawObject(configPath)
	if err != nil {
		return err
	}
	if _, ok := root["mcpServers"]; !ok {
		return nil
	}
	servers, err := extractObject(root, "mcpServers")
	if err != nil {
		return err
	}
	if _, ok := servers[entryKey]; !ok {
		return nil
	}
	delete(servers, entryKey)

	serversData, err := json.Marshal(servers)
	if err != nil {
		return errUnwritable
	}
	root["mcpServers"] = serversData

	return writeAtomic(configPath, root)
}

// InjectedCommand reports the command currently configured for our entry in
// the config file at configPath. It returns false if the file is missing,
// fails to parse, or has no "whatsapp" entry under mcpServers. An http
// entry reports ok with an empty command; use InjectedEntry to tell the
// transports apart.
func InjectedCommand(configPath string) (string, bool) {
	command, _, ok := InjectedEntry(configPath)
	return command, ok
}

// InjectedEntry reports our entry's command (stdio) and url (http) — at
// most one is non-empty — and whether an entry exists at all. It returns
// ok == false if the file is missing, fails to parse, or has no
// "whatsapp" entry under mcpServers.
func InjectedEntry(configPath string) (command, url string, ok bool) {
	root, err := loadRawObject(configPath)
	if err != nil {
		return "", "", false
	}
	servers, err := extractObject(root, "mcpServers")
	if err != nil {
		return "", "", false
	}
	raw, present := servers[entryKey]
	if !present {
		return "", "", false
	}
	var entry mcpEntry
	if err := json.Unmarshal(raw, &entry); err != nil {
		return "", "", false
	}
	return entry.Command, entry.URL, true
}

var (
	errUnreadable   = errors.New("client config file could not be read")
	errInvalidJSON  = errors.New("client config file is not valid JSON")
	errInvalidShape = errors.New("client config's mcpServers value is not a JSON object")
	errUnwritable   = errors.New("client config file could not be written")
)

// loadRawObject reads configPath and unmarshals its top level into a raw
// object, so callers can touch one key while leaving the rest byte
// equivalent. A missing file yields an empty object, not an error.
func loadRawObject(path string) (map[string]json.RawMessage, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- path is a caller-supplied client config location, not network input
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]json.RawMessage{}, nil
		}
		return nil, errUnreadable
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, errInvalidJSON
	}
	// A file containing the literal JSON null unmarshals successfully into a
	// nil map (encoding/json's documented behavior for null into a map), not
	// an error. Treat it the same as an empty object rather than risk a nil
	// map being written into by a caller.
	if root == nil {
		root = map[string]json.RawMessage{}
	}
	return root, nil
}

// extractObject unmarshals root[key] as a raw object. A missing key, or a
// key whose value is the literal JSON null, yields an empty object; a key
// present with any other non-object value is a category error.
func extractObject(root map[string]json.RawMessage, key string) (map[string]json.RawMessage, error) {
	raw, ok := root[key]
	if !ok {
		return map[string]json.RawMessage{}, nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, errInvalidShape
	}
	// Same null-into-map normalization as loadRawObject: {"mcpServers":
	// null} unmarshals to a nil map with no error, and a null server list
	// means no servers, not a shape error.
	if obj == nil {
		obj = map[string]json.RawMessage{}
	}
	return obj, nil
}

// writeAtomic marshals root and writes it to path via a temp file plus
// rename, creating path's parent directory if needed, with file mode 0600.
func writeAtomic(path string, root map[string]json.RawMessage) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return errUnwritable
	}

	data, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return errUnwritable
	}

	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return errUnwritable
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }() // no-op once renamed away

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return errUnwritable
	}
	if err := tmp.Close(); err != nil {
		return errUnwritable
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		return errUnwritable
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return errUnwritable
	}
	return nil
}
