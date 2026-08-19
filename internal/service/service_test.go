package service

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testConfig(home string) Config {
	return Config{
		BinaryPath: "/home/u/.local/bin/whatsapp-connect-mcp",
		HTTPAddr:   "127.0.0.1:2178",
		Home:       home,
		UID:        501,
	}
}

// recorder is a Runner that records every command it is asked to run and
// can fail selected ones.
type recorder struct {
	calls  []string
	failOn string // command prefix that returns an error
}

func (r *recorder) run(name string, args ...string) error {
	call := strings.Join(append([]string{name}, args...), " ")
	r.calls = append(r.calls, call)
	if r.failOn != "" && strings.HasPrefix(call, r.failOn) {
		return errors.New("exit status 1")
	}
	return nil
}

// --- unit file rendering ---

func TestLaunchdPlistContent(t *testing.T) {
	cfg := testConfig("/Users/u")
	cfg.NodeDir = "/opt/homebrew/bin"
	got := launchdPlist(cfg)

	for _, want := range []string{
		"<string>" + Label + "</string>",
		"<string>/home/u/.local/bin/whatsapp-connect-mcp</string>",
		"<string>serve</string>",
		"<string>--http</string>",
		"<string>127.0.0.1:2178</string>",
		"<key>RunAtLoad</key>",
		"<key>SuccessfulExit</key>",
		"<string>/Users/u/Library/Logs/whatsapp-connect-mcp.log</string>",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("plist missing %q:\n%s", want, got)
		}
	}

	// The env-node trap (issue #15): launchd's default PATH has no
	// Homebrew or npm locations, so the plist must carry an explicit PATH
	// with the node directory first when the program is a Node shim.
	if !strings.Contains(got, "<key>PATH</key>") {
		t.Fatalf("plist has no PATH environment variable:\n%s", got)
	}
	if !strings.Contains(got, "<string>/opt/homebrew/bin:") {
		t.Errorf("PATH does not lead with the node directory:\n%s", got)
	}
}

func TestLaunchdPlistEscapesXML(t *testing.T) {
	cfg := testConfig("/Users/u")
	cfg.BinaryPath = "/Users/u/a&b/whatsapp-connect-mcp"
	got := launchdPlist(cfg)
	if strings.Contains(got, "a&b") {
		t.Fatalf("plist contains unescaped &:\n%s", got)
	}
	if !strings.Contains(got, "a&amp;b") {
		t.Fatalf("plist does not XML-escape the binary path:\n%s", got)
	}
}

func TestSystemdUnitContent(t *testing.T) {
	cfg := testConfig("/home/u")
	cfg.NodeDir = "/home/u/.nvm/versions/node/v24.0.0/bin"
	got := systemdUnit(cfg)

	for _, want := range []string{
		`ExecStart="/home/u/.local/bin/whatsapp-connect-mcp" serve --http 127.0.0.1:2178`,
		"Restart=on-failure",
		"WantedBy=default.target",
		"Environment=PATH=/home/u/.nvm/versions/node/v24.0.0/bin:",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("unit missing %q:\n%s", want, got)
		}
	}
}

// --- install / uninstall / restart ---

func TestInstallDarwinWritesPlistAndBootstraps(t *testing.T) {
	home := t.TempDir()
	cfg := testConfig(home)
	// A previous copy may be loaded; bootout failing (nothing loaded) must
	// not fail the install.
	rec := &recorder{failOn: "launchctl bootout"}

	var out bytes.Buffer
	if err := Install("darwin", cfg, rec.run, &out); err != nil {
		t.Fatalf("Install() error: %v", err)
	}

	// Unit locations are POSIX paths on every supported platform, so the
	// package joins them with forward slashes even when its tests run
	// elsewhere.
	plistPath := home + "/Library/LaunchAgents/" + Label + ".plist"
	if _, err := os.Stat(plistPath); err != nil {
		t.Fatalf("plist not written: %v", err)
	}
	wantCalls := []string{
		"launchctl bootout gui/501/" + Label,
		"launchctl bootstrap gui/501 " + plistPath,
	}
	if len(rec.calls) != len(wantCalls) {
		t.Fatalf("calls = %v, want %v", rec.calls, wantCalls)
	}
	for i, want := range wantCalls {
		if rec.calls[i] != want {
			t.Fatalf("call[%d] = %q, want %q", i, rec.calls[i], want)
		}
	}
}

func TestInstallLinuxWritesUnitAndEnables(t *testing.T) {
	home := t.TempDir()
	cfg := testConfig(home)
	rec := &recorder{}

	var out bytes.Buffer
	if err := Install("linux", cfg, rec.run, &out); err != nil {
		t.Fatalf("Install() error: %v", err)
	}

	unitPath := home + "/.config/systemd/user/" + UnitName + ".service"
	if _, err := os.Stat(unitPath); err != nil {
		t.Fatalf("unit not written: %v", err)
	}
	wantCalls := []string{
		"systemctl --user daemon-reload",
		"systemctl --user enable --now " + UnitName,
	}
	if len(rec.calls) != len(wantCalls) {
		t.Fatalf("calls = %v, want %v", rec.calls, wantCalls)
	}
	for i, want := range wantCalls {
		if rec.calls[i] != want {
			t.Fatalf("call[%d] = %q, want %q", i, rec.calls[i], want)
		}
	}
	// Headless boxes need lingering or the user manager dies at logout;
	// the install must at least say so.
	if !strings.Contains(out.String(), "loginctl enable-linger") {
		t.Errorf("install output does not mention loginctl enable-linger: %q", out.String())
	}
}

func TestRestart(t *testing.T) {
	tests := []struct {
		goos string
		want string
	}{
		{goos: "darwin", want: "launchctl kickstart -k gui/501/" + Label},
		{goos: "linux", want: "systemctl --user restart " + UnitName},
	}
	for _, tt := range tests {
		t.Run(tt.goos, func(t *testing.T) {
			rec := &recorder{}
			var out bytes.Buffer
			if err := Restart(tt.goos, testConfig(t.TempDir()), rec.run, &out); err != nil {
				t.Fatalf("Restart() error: %v", err)
			}
			if len(rec.calls) != 1 || rec.calls[0] != tt.want {
				t.Fatalf("calls = %v, want [%q]", rec.calls, tt.want)
			}
		})
	}
}

func TestUninstallDarwinRemovesPlist(t *testing.T) {
	home := t.TempDir()
	cfg := testConfig(home)
	plistPath := filepath.Join(home, "Library", "LaunchAgents", Label+".plist")
	if err := os.MkdirAll(filepath.Dir(plistPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plistPath, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Uninstalling a stopped service: bootout fails, uninstall proceeds.
	rec := &recorder{failOn: "launchctl bootout"}

	var out bytes.Buffer
	if err := Uninstall("darwin", cfg, rec.run, &out); err != nil {
		t.Fatalf("Uninstall() error: %v", err)
	}
	if _, err := os.Stat(plistPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("plist still present after uninstall")
	}
}

func TestUninstallLinuxRemovesUnitAndReloads(t *testing.T) {
	home := t.TempDir()
	cfg := testConfig(home)
	unitPath := filepath.Join(home, ".config", "systemd", "user", UnitName+".service")
	if err := os.MkdirAll(filepath.Dir(unitPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unitPath, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}

	var out bytes.Buffer
	if err := Uninstall("linux", cfg, rec.run, &out); err != nil {
		t.Fatalf("Uninstall() error: %v", err)
	}
	if _, err := os.Stat(unitPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unit still present after uninstall")
	}
	joined := strings.Join(rec.calls, "; ")
	if !strings.Contains(joined, "disable --now") || !strings.Contains(joined, "daemon-reload") {
		t.Fatalf("calls = %v, want disable --now and daemon-reload", rec.calls)
	}
}

func TestUninstallNothingInstalledSaysSo(t *testing.T) {
	rec := &recorder{failOn: "launchctl bootout"}
	var out bytes.Buffer
	if err := Uninstall("darwin", testConfig(t.TempDir()), rec.run, &out); err != nil {
		t.Fatalf("Uninstall() on a clean system must not error, got: %v", err)
	}
	if !strings.Contains(out.String(), "no service installed") {
		t.Errorf("output = %q, want it to say no service was installed", out.String())
	}
}

func TestUnsupportedPlatform(t *testing.T) {
	for _, fn := range []func(string, Config, Runner, *bytes.Buffer) error{
		func(g string, c Config, r Runner, o *bytes.Buffer) error { return Install(g, c, r, o) },
		func(g string, c Config, r Runner, o *bytes.Buffer) error { return Uninstall(g, c, r, o) },
		func(g string, c Config, r Runner, o *bytes.Buffer) error { return Restart(g, c, r, o) },
	} {
		rec := &recorder{}
		var out bytes.Buffer
		err := fn("windows", testConfig(t.TempDir()), rec.run, &out)
		if err == nil || !strings.Contains(err.Error(), "not supported") {
			t.Fatalf("error = %v, want a not-supported error on windows", err)
		}
		if len(rec.calls) != 0 {
			t.Fatalf("unsupported platform still ran commands: %v", rec.calls)
		}
	}
}

// --- npm shim resolution ---

func TestResolveProgram(t *testing.T) {
	look := func(entries map[string]string) func(string) (string, error) {
		return func(name string) (string, error) {
			if p, ok := entries[name]; ok {
				return p, nil
			}
			return "", errors.New("not found")
		}
	}

	t.Run("plain binary runs itself", func(t *testing.T) {
		program, nodeDir, err := ResolveProgram("/home/u/.local/bin/whatsapp-connect-mcp", look(nil))
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if program != "/home/u/.local/bin/whatsapp-connect-mcp" || nodeDir != "" {
			t.Fatalf("got (%q, %q), want the binary itself and no node dir", program, nodeDir)
		}
	})

	t.Run("npm-cached binary resolves to the shim plus node dir", func(t *testing.T) {
		// The npm package caches the real binary under a version-suffixed
		// name that changes on every update; a service must exec the
		// stable shim instead, and the shim needs node on PATH.
		exe := "/opt/homebrew/lib/node_modules/whatsapp-connect-mcp/.bin/whatsapp-connect-mcp_0.1.3_darwin_arm64"
		program, nodeDir, err := ResolveProgram(exe, look(map[string]string{
			"whatsapp-connect-mcp": "/opt/homebrew/bin/whatsapp-connect-mcp",
			"node":                 "/opt/homebrew/bin/node",
		}))
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if program != "/opt/homebrew/bin/whatsapp-connect-mcp" {
			t.Fatalf("program = %q, want the npm shim", program)
		}
		if nodeDir != "/opt/homebrew/bin" {
			t.Fatalf("nodeDir = %q, want node's directory", nodeDir)
		}
	})

	t.Run("npm-cached binary without the shim on PATH errors", func(t *testing.T) {
		exe := "/x/.bin/whatsapp-connect-mcp_0.1.3_linux_amd64"
		if _, _, err := ResolveProgram(exe, look(map[string]string{"node": "/usr/bin/node"})); err == nil {
			t.Fatal("expected an error when the npm shim is not on PATH")
		}
	})

	t.Run("npm-cached binary without node on PATH errors", func(t *testing.T) {
		exe := "/x/.bin/whatsapp-connect-mcp_0.1.3_linux_amd64"
		if _, _, err := ResolveProgram(exe, look(map[string]string{"whatsapp-connect-mcp": "/usr/local/bin/whatsapp-connect-mcp"})); err == nil {
			t.Fatal("expected an error when node is not on PATH")
		}
	})
}
