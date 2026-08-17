package clients

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func clientNames(t *testing.T) []string {
	t.Helper()
	var names []string
	for _, c := range Detect(t.TempDir()) {
		names = append(names, c.Name)
	}
	return names
}

func configPathFor(t *testing.T, home, name string) string {
	t.Helper()
	for _, c := range Detect(home) {
		if c.Name == name {
			return c.ConfigPath
		}
	}
	t.Fatalf("client %q not found in Detect() output", name)
	return ""
}

func findClient(cs []Client, name string) Client {
	for _, c := range cs {
		if c.Name == name {
			return c
		}
	}
	return Client{}
}

func TestDetectReturnsKnownClients(t *testing.T) {
	home := t.TempDir()
	got := Detect(home)

	wantNames := []string{"Claude Desktop", "Cursor", "Windsurf", "Cline", "Claude Code"}
	if len(got) != len(wantNames) {
		t.Fatalf("Detect() returned %d clients, want %d", len(got), len(wantNames))
	}
	for i, c := range got {
		if c.Name != wantNames[i] {
			t.Fatalf("client[%d].Name = %q, want %q", i, c.Name, wantNames[i])
		}
		if c.ConfigPath == "" {
			t.Fatalf("client %q has empty ConfigPath", c.Name)
		}
		if !strings.HasPrefix(c.ConfigPath, home) {
			t.Fatalf("client %q ConfigPath %q is not under home %q", c.Name, c.ConfigPath, home)
		}
		if c.Installed {
			t.Fatalf("client %q Installed = true in empty home, want false", c.Name)
		}
		if c.Injected {
			t.Fatalf("client %q Injected = true in empty home, want false", c.Name)
		}
	}
}

func TestDetectReportsInstalledAndInjected(t *testing.T) {
	home := t.TempDir()
	cursor := findClient(Detect(home), "Cursor")
	if cursor.Name == "" {
		t.Fatal("Cursor not found in Detect() output")
	}

	if err := os.MkdirAll(filepath.Dir(cursor.ConfigPath), 0o700); err != nil {
		t.Fatalf("seed cursor dir: %v", err)
	}
	found := findClient(Detect(home), "Cursor")
	if !found.Installed {
		t.Fatal("Cursor Installed = false after creating its directory, want true")
	}
	if found.Injected {
		t.Fatal("Cursor Injected = true before Inject, want false")
	}

	if err := Inject(cursor.ConfigPath, "/usr/local/bin/whatsapp-connect-mcp"); err != nil {
		t.Fatalf("Inject() error: %v", err)
	}
	found = findClient(Detect(home), "Cursor")
	if !found.Injected {
		t.Fatal("Cursor Injected = false after Inject, want true")
	}

	other := findClient(Detect(home), "Windsurf")
	if other.Installed {
		t.Fatal("Windsurf Installed = true, want false (its directory was never created)")
	}
}

func TestInjectPreservesSiblings(t *testing.T) {
	for _, name := range clientNames(t) {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			path := configPathFor(t, home, name)
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatalf("seed dir: %v", err)
			}
			seed := `{"mcpServers":{"other":{"command":"other-bin","args":["run"]}},"unrelatedTopLevel":{"nested":true,"n":3}}`
			if err := os.WriteFile(path, []byte(seed), 0o600); err != nil {
				t.Fatalf("seed config: %v", err)
			}

			if err := Inject(path, "/abs/bin"); err != nil {
				t.Fatalf("Inject() error: %v", err)
			}

			data, err := os.ReadFile(path) // #nosec G304 -- path comes from Detect() in a t.TempDir(), not network input
			if err != nil {
				t.Fatalf("read config: %v", err)
			}
			var root map[string]json.RawMessage
			if err := json.Unmarshal(data, &root); err != nil {
				t.Fatalf("result is not valid JSON: %v", err)
			}
			if _, ok := root["unrelatedTopLevel"]; !ok {
				t.Fatal("unrelatedTopLevel key lost after Inject")
			}
			var servers map[string]json.RawMessage
			if err := json.Unmarshal(root["mcpServers"], &servers); err != nil {
				t.Fatalf("mcpServers is not an object: %v", err)
			}
			if _, ok := servers["other"]; !ok {
				t.Fatal("sibling mcpServers entry lost after Inject")
			}
			if _, ok := servers["whatsapp"]; !ok {
				t.Fatal("whatsapp entry missing after Inject")
			}
		})
	}
}

func TestInjectIntoMissingFileCreatesMinimalConfig(t *testing.T) {
	for _, name := range clientNames(t) {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			path := configPathFor(t, home, name)

			if err := Inject(path, "/abs/bin"); err != nil {
				t.Fatalf("Inject() error: %v", err)
			}

			cmd, ok := InjectedCommand(path)
			if !ok {
				t.Fatal("InjectedCommand() ok = false after Inject into missing file")
			}
			if cmd != "/abs/bin" {
				t.Fatalf("InjectedCommand() = %q, want %q", cmd, "/abs/bin")
			}
		})
	}
}

func TestDoubleInjectIsIdempotent(t *testing.T) {
	for _, name := range clientNames(t) {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			path := configPathFor(t, home, name)

			if err := Inject(path, "/abs/bin"); err != nil {
				t.Fatalf("first Inject() error: %v", err)
			}
			if err := Inject(path, "/abs/bin"); err != nil {
				t.Fatalf("second Inject() error: %v", err)
			}

			data, err := os.ReadFile(path) // #nosec G304 -- path comes from Detect() in a t.TempDir(), not network input
			if err != nil {
				t.Fatalf("read config: %v", err)
			}
			var root map[string]json.RawMessage
			if err := json.Unmarshal(data, &root); err != nil {
				t.Fatalf("result is not valid JSON: %v", err)
			}
			var servers map[string]json.RawMessage
			if err := json.Unmarshal(root["mcpServers"], &servers); err != nil {
				t.Fatalf("mcpServers is not an object: %v", err)
			}
			if len(servers) != 1 {
				t.Fatalf("mcpServers has %d entries after double inject, want 1", len(servers))
			}
		})
	}
}

func TestRemoveDeletesOnlyOurEntry(t *testing.T) {
	for _, name := range clientNames(t) {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			path := configPathFor(t, home, name)
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatalf("seed dir: %v", err)
			}
			seed := `{"mcpServers":{"other":{"command":"other-bin","args":["run"]}}}`
			if err := os.WriteFile(path, []byte(seed), 0o600); err != nil {
				t.Fatalf("seed config: %v", err)
			}
			if err := Inject(path, "/abs/bin"); err != nil {
				t.Fatalf("Inject() error: %v", err)
			}

			if err := Remove(path); err != nil {
				t.Fatalf("Remove() error: %v", err)
			}

			data, err := os.ReadFile(path) // #nosec G304 -- path comes from Detect() in a t.TempDir(), not network input
			if err != nil {
				t.Fatalf("read config: %v", err)
			}
			var root map[string]json.RawMessage
			if err := json.Unmarshal(data, &root); err != nil {
				t.Fatalf("result is not valid JSON: %v", err)
			}
			var servers map[string]json.RawMessage
			if err := json.Unmarshal(root["mcpServers"], &servers); err != nil {
				t.Fatalf("mcpServers is not an object: %v", err)
			}
			if _, ok := servers["whatsapp"]; ok {
				t.Fatal("whatsapp entry still present after Remove")
			}
			if _, ok := servers["other"]; !ok {
				t.Fatal("sibling entry removed by Remove, want it kept")
			}
		})
	}
}

func TestRemoveOnMissingFileIsNoop(t *testing.T) {
	home := t.TempDir()
	path := configPathFor(t, home, "Claude Code")

	if err := Remove(path); err != nil {
		t.Fatalf("Remove() error: %v", err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("Remove() created a config file that never existed")
	}
}

func TestInjectMalformedJSONLeavesFileUntouched(t *testing.T) {
	for _, name := range clientNames(t) {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			path := configPathFor(t, home, name)
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatalf("seed dir: %v", err)
			}
			const broken = "{not valid json"
			if err := os.WriteFile(path, []byte(broken), 0o600); err != nil {
				t.Fatalf("seed config: %v", err)
			}

			err := Inject(path, "/abs/bin")
			if err == nil {
				t.Fatal("Inject() error = nil, want error for malformed JSON")
			}

			data, readErr := os.ReadFile(path) // #nosec G304 -- path comes from Detect() in a t.TempDir(), not network input
			if readErr != nil {
				t.Fatalf("read config: %v", readErr)
			}
			if string(data) != broken {
				t.Fatalf("file contents changed after failed Inject: %q", string(data))
			}
		})
	}
}

func TestRemoveMalformedJSONLeavesFileUntouched(t *testing.T) {
	home := t.TempDir()
	path := configPathFor(t, home, "Cursor")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("seed dir: %v", err)
	}
	const broken = "[1, 2, 3]"
	if err := os.WriteFile(path, []byte(broken), 0o600); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	err := Remove(path)
	if err == nil {
		t.Fatal("Remove() error = nil, want error for malformed JSON")
	}

	data, readErr := os.ReadFile(path) // #nosec G304 -- path comes from Detect() in a t.TempDir(), not network input
	if readErr != nil {
		t.Fatalf("read config: %v", readErr)
	}
	if string(data) != broken {
		t.Fatalf("file contents changed after failed Remove: %q", string(data))
	}
}

func TestInjectNullConfigFileDoesNotPanic(t *testing.T) {
	home := t.TempDir()
	path := configPathFor(t, home, "Cursor")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("seed dir: %v", err)
	}
	if err := os.WriteFile(path, []byte("null"), 0o600); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	if err := Inject(path, "/abs/bin"); err != nil {
		t.Fatalf("Inject() error: %v", err)
	}

	cmd, ok := InjectedCommand(path)
	if !ok || cmd != "/abs/bin" {
		t.Fatalf("InjectedCommand() = (%q, %v), want (%q, true)", cmd, ok, "/abs/bin")
	}
}

func TestInjectNullMcpServersDoesNotPanic(t *testing.T) {
	home := t.TempDir()
	path := configPathFor(t, home, "Cursor")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("seed dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(`{"mcpServers": null}`), 0o600); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	if err := Inject(path, "/abs/bin"); err != nil {
		t.Fatalf("Inject() error: %v", err)
	}

	cmd, ok := InjectedCommand(path)
	if !ok || cmd != "/abs/bin" {
		t.Fatalf("InjectedCommand() = (%q, %v), want (%q, true)", cmd, ok, "/abs/bin")
	}
}

func TestRemoveNullConfigShapesAreNoop(t *testing.T) {
	tests := []struct {
		name string
		seed string
	}{
		{"null document", "null"},
		{"null mcpServers", `{"mcpServers": null}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			path := configPathFor(t, home, "Cursor")
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatalf("seed dir: %v", err)
			}
			if err := os.WriteFile(path, []byte(tt.seed), 0o600); err != nil {
				t.Fatalf("seed config: %v", err)
			}

			if err := Remove(path); err != nil {
				t.Fatalf("Remove() error: %v", err)
			}
		})
	}
}

func TestInjectMcpServersWrongTypeIsRejected(t *testing.T) {
	tests := []struct {
		name string
		seed string
	}{
		{"array", `{"mcpServers": ["x"]}`},
		{"string", `{"mcpServers": "x"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			path := configPathFor(t, home, "Cursor")
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatalf("seed dir: %v", err)
			}
			if err := os.WriteFile(path, []byte(tt.seed), 0o600); err != nil {
				t.Fatalf("seed config: %v", err)
			}

			err := Inject(path, "/abs/bin")
			if err == nil {
				t.Fatal("Inject() error = nil, want error for non-object mcpServers")
			}

			data, readErr := os.ReadFile(path) // #nosec G304 -- path comes from Detect() in a t.TempDir(), not network input
			if readErr != nil {
				t.Fatalf("read config: %v", readErr)
			}
			if string(data) != tt.seed {
				t.Fatalf("file contents changed after failed Inject: %q", string(data))
			}
		})
	}
}

func TestInjectEntryShape(t *testing.T) {
	home := t.TempDir()
	path := configPathFor(t, home, "Claude Desktop")

	if err := Inject(path, "/abs/bin/whatsapp-connect-mcp"); err != nil {
		t.Fatalf("Inject() error: %v", err)
	}

	data, err := os.ReadFile(path) // #nosec G304 -- path comes from Detect() in a t.TempDir(), not network input
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("result is not valid JSON: %v", err)
	}
	var servers map[string]json.RawMessage
	if err := json.Unmarshal(root["mcpServers"], &servers); err != nil {
		t.Fatalf("mcpServers is not an object: %v", err)
	}
	var entry struct {
		Command string   `json:"command"`
		Args    []string `json:"args"`
	}
	if err := json.Unmarshal(servers["whatsapp"], &entry); err != nil {
		t.Fatalf("whatsapp entry is not valid: %v", err)
	}
	if entry.Command != "/abs/bin/whatsapp-connect-mcp" {
		t.Fatalf("entry.Command = %q, want %q", entry.Command, "/abs/bin/whatsapp-connect-mcp")
	}
	if len(entry.Args) != 1 || entry.Args[0] != "serve" {
		t.Fatalf("entry.Args = %v, want [serve]", entry.Args)
	}
}

func TestInjectedCommand(t *testing.T) {
	home := t.TempDir()
	path := configPathFor(t, home, "Windsurf")

	if _, ok := InjectedCommand(path); ok {
		t.Fatal("InjectedCommand() ok = true for missing file, want false")
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("seed dir: %v", err)
	}
	if err := os.WriteFile(path, []byte("{not valid"), 0o600); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	if _, ok := InjectedCommand(path); ok {
		t.Fatal("InjectedCommand() ok = true for malformed file, want false")
	}

	if err := os.WriteFile(path, []byte(`{"mcpServers":{}}`), 0o600); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	if _, ok := InjectedCommand(path); ok {
		t.Fatal("InjectedCommand() ok = true with no whatsapp entry, want false")
	}

	if err := Inject(path, "/abs/bin/whatsapp-connect-mcp"); err != nil {
		t.Fatalf("Inject() error: %v", err)
	}
	cmd, ok := InjectedCommand(path)
	if !ok {
		t.Fatal("InjectedCommand() ok = false after Inject, want true")
	}
	if cmd != "/abs/bin/whatsapp-connect-mcp" {
		t.Fatalf("InjectedCommand() = %q, want %q", cmd, "/abs/bin/whatsapp-connect-mcp")
	}
}

func TestInjectFileMode(t *testing.T) {
	home := t.TempDir()
	path := configPathFor(t, home, "Cursor")
	if err := Inject(path, "/abs/bin"); err != nil {
		t.Fatalf("Inject() error: %v", err)
	}
	if runtime.GOOS == "windows" {
		t.Skip("file mode bits are not meaningful on windows")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat config: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("config mode = %o, want 0600", perm)
	}
}

func TestInjectErrorDoesNotLeakPathOrContent(t *testing.T) {
	home := t.TempDir()
	path := configPathFor(t, home, "Cline")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("seed dir: %v", err)
	}
	const broken = "{unparseable-secret-token-xyz"
	if err := os.WriteFile(path, []byte(broken), 0o600); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	err := Inject(path, "/abs/bin")
	if err == nil {
		t.Fatal("Inject() error = nil, want error")
	}
	if strings.Contains(err.Error(), home) {
		t.Fatalf("Inject() error = %q, must not embed the config path", err.Error())
	}
	if strings.Contains(err.Error(), "unparseable-secret-token-xyz") {
		t.Fatalf("Inject() error = %q, must not echo file contents", err.Error())
	}
}
