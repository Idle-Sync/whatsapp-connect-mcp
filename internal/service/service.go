// Package service installs, removes, and restarts a background service
// that runs `serve --http` — a launchd user agent on macOS, a systemd user
// unit on Linux. It owns the unit file content and the launchctl/systemctl
// invocations; external commands run through an injected Runner so the
// lifecycle is testable without either init system.
package service

// The path/filepath split matters here: every path this package
// manipulates — unit locations, PATH entries, the npm shim — is a POSIX
// path on the only platforms with service support, so all path math uses
// package path (forward slashes always), keeping rendered unit content
// identical no matter which OS the tests run on. Only os.MkdirAll and
// os.WriteFile touch the real filesystem, and they accept these paths on
// every supported platform.
import (
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
)

// Label is the launchd agent label and plist basename on macOS.
const Label = "com.idle-sync.whatsapp-connect-mcp"

// UnitName is the systemd user unit name (sans ".service") on Linux.
const UnitName = "whatsapp-connect-mcp"

// Config describes the service to manage.
type Config struct {
	// BinaryPath is the program the service execs. It must be stable
	// across updates: the real binary for direct installs, the npm shim
	// for npm installs (see ResolveProgram).
	BinaryPath string
	// NodeDir is the directory holding the node executable when
	// BinaryPath is a Node shim, empty otherwise. It leads the service's
	// PATH so the shim's `#!/usr/bin/env node` resolves under launchd and
	// systemd, whose default PATHs carry no Homebrew, npm, or nvm
	// locations (issue #15).
	NodeDir string
	// HTTPAddr is the address passed to `serve --http`. Install only.
	HTTPAddr string
	// Home is the user's home directory; unit files live beneath it.
	Home string
	// UID is the numeric user id, addressing launchd's gui domain. macOS
	// only.
	UID int
}

// Runner executes an external command and returns its failure, if any.
// The CLI wires exec.Command; tests record calls.
type Runner func(name string, args ...string) error

// Install writes the platform's unit file and starts the service, replacing
// any previously installed copy.
func Install(goos string, cfg Config, run Runner, out io.Writer) error {
	switch goos {
	case "darwin":
		return installDarwin(cfg, run, out)
	case "linux":
		return installLinux(cfg, run, out)
	default:
		return unsupported(goos)
	}
}

// Uninstall stops the service and removes its unit file. A system with no
// service installed is reported, not an error.
func Uninstall(goos string, cfg Config, run Runner, out io.Writer) error {
	switch goos {
	case "darwin":
		return uninstallDarwin(cfg, run, out)
	case "linux":
		return uninstallLinux(cfg, run, out)
	default:
		return unsupported(goos)
	}
}

// Restart restarts the installed service, so an updated binary (or shim
// target) actually starts serving — a kept-alive service never re-execs on
// its own.
func Restart(goos string, cfg Config, run Runner, out io.Writer) error {
	switch goos {
	case "darwin":
		if err := run("launchctl", "kickstart", "-k", fmt.Sprintf("gui/%d/%s", cfg.UID, Label)); err != nil {
			return fmt.Errorf("restart service: %w", err)
		}
		_, _ = fmt.Fprintln(out, "service restarted")
		return nil
	case "linux":
		if err := run("systemctl", "--user", "restart", UnitName); err != nil {
			return fmt.Errorf("restart service: %w", err)
		}
		_, _ = fmt.Fprintln(out, "service restarted")
		return nil
	default:
		return unsupported(goos)
	}
}

// ResolveProgram picks the program a service should exec, given the path of
// the currently running executable, and the node directory that must join
// the service's PATH when that program is a Node shim.
//
// The npm package caches the real binary inside itself under a
// version-suffixed name (whatsapp-connect-mcp_<version>_<os>_<arch>) that
// changes on every update; a service pointing there would keep serving the
// old version forever. The underscore identifies that case — no other
// install method produces it — and the service execs the stable npm shim
// instead, which needs node resolvable at exec time.
func ResolveProgram(exePath string, lookPath func(string) (string, error)) (program, nodeDir string, err error) {
	if !strings.HasPrefix(path.Base(exePath), UnitName+"_") {
		return exePath, "", nil
	}

	shim, err := lookPath(UnitName)
	if err != nil {
		return "", "", fmt.Errorf("running from the npm package, but %q is not on PATH — install globally (npm install -g %s) before installing a service", UnitName, UnitName)
	}
	node, err := lookPath("node")
	if err != nil {
		return "", "", fmt.Errorf("running from the npm package, but node is not on PATH — the service could not start the npm shim")
	}
	return shim, path.Dir(node), nil
}

func unsupported(goos string) error {
	return fmt.Errorf("service management is not supported on %s — run `serve --http` under your own process manager instead", goos)
}

// servicePATH is the PATH the unit runs with: the node directory (when
// set) and the program's own directory first, then the locations launchd
// and systemd omit but user installs live in.
func servicePATH(cfg Config) string {
	dirs := []string{}
	if cfg.NodeDir != "" {
		dirs = append(dirs, cfg.NodeDir)
	}
	dirs = append(dirs,
		path.Dir(cfg.BinaryPath),
		"/opt/homebrew/bin",
		"/usr/local/bin",
		"/usr/bin",
		"/bin",
		"/usr/sbin",
		"/sbin",
	)
	return strings.Join(dirs, ":")
}

// --- macOS (launchd) ---

func plistPath(cfg Config) string {
	return path.Join(cfg.Home, "Library", "LaunchAgents", Label+".plist")
}

func logPath(cfg Config) string {
	return path.Join(cfg.Home, "Library", "Logs", UnitName+".log")
}

// launchdPlist renders the launch agent: keep-alive on failure only (a
// clean stop stays stopped, and an unpaired serve waits idle rather than
// exiting, so there is no crash loop to hide), stderr to a log file the
// user can actually find, and an explicit PATH.
func launchdPlist(cfg Config) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + Label + `</string>
	<key>ProgramArguments</key>
	<array>
		<string>` + xmlEscape(cfg.BinaryPath) + `</string>
		<string>serve</string>
		<string>--http</string>
		<string>` + xmlEscape(cfg.HTTPAddr) + `</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<dict>
		<key>SuccessfulExit</key>
		<false/>
	</dict>
	<key>EnvironmentVariables</key>
	<dict>
		<key>PATH</key>
		<string>` + xmlEscape(servicePATH(cfg)) + `</string>
	</dict>
	<key>StandardErrorPath</key>
	<string>` + xmlEscape(logPath(cfg)) + `</string>
</dict>
</plist>
`
}

func installDarwin(cfg Config, run Runner, out io.Writer) error {
	unitFile := plistPath(cfg)
	if err := writeUnitFile(unitFile, launchdPlist(cfg)); err != nil {
		return err
	}
	// A previous copy may be loaded; bootout is best-effort so a fresh
	// install (nothing to boot out) succeeds too.
	_ = run("launchctl", "bootout", fmt.Sprintf("gui/%d/%s", cfg.UID, Label))
	if err := run("launchctl", "bootstrap", fmt.Sprintf("gui/%d", cfg.UID), unitFile); err != nil {
		return fmt.Errorf("start service: %w", err)
	}
	_, _ = fmt.Fprintf(out, "service installed and started: %s\n", unitFile)
	_, _ = fmt.Fprintf(out, "logs: %s\n", logPath(cfg))
	return nil
}

func uninstallDarwin(cfg Config, run Runner, out io.Writer) error {
	// Best-effort: the service may not be loaded (or never installed).
	_ = run("launchctl", "bootout", fmt.Sprintf("gui/%d/%s", cfg.UID, Label))
	return removeUnitFile(plistPath(cfg), out)
}

// --- Linux (systemd user unit) ---

func unitPath(cfg Config) string {
	return path.Join(cfg.Home, ".config", "systemd", "user", UnitName+".service")
}

// systemdUnit renders the user unit. Restart=on-failure mirrors the
// launchd agent's SuccessfulExit=false; the unpaired case waits idle
// inside serve itself, so restart-on-failure cannot crash-loop it.
func systemdUnit(cfg Config) string {
	return `[Unit]
Description=WhatsApp Connect MCP server (shared HTTP)
After=network-online.target
Wants=network-online.target

[Service]
ExecStart="` + cfg.BinaryPath + `" serve --http ` + cfg.HTTPAddr + `
Environment=PATH=` + servicePATH(cfg) + `
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
`
}

func installLinux(cfg Config, run Runner, out io.Writer) error {
	unitFile := unitPath(cfg)
	if err := writeUnitFile(unitFile, systemdUnit(cfg)); err != nil {
		return err
	}
	if err := run("systemctl", "--user", "daemon-reload"); err != nil {
		return fmt.Errorf("reload systemd: %w", err)
	}
	if err := run("systemctl", "--user", "enable", "--now", UnitName); err != nil {
		return fmt.Errorf("start service: %w", err)
	}
	_, _ = fmt.Fprintf(out, "service installed and started: %s\n", unitFile)
	_, _ = fmt.Fprintf(out, "logs: journalctl --user -u %s\n", UnitName)
	_, _ = fmt.Fprintf(out, "to keep it running while logged out: loginctl enable-linger %s\n", path.Base(cfg.Home))
	return nil
}

func uninstallLinux(cfg Config, run Runner, out io.Writer) error {
	// Best-effort: the unit may already be stopped or never enabled.
	_ = run("systemctl", "--user", "disable", "--now", UnitName)
	if err := removeUnitFile(unitPath(cfg), out); err != nil {
		return err
	}
	_ = run("systemctl", "--user", "daemon-reload")
	return nil
}

// --- shared file handling ---

func writeUnitFile(unitFile, content string) error {
	if err := os.MkdirAll(path.Dir(unitFile), 0o700); err != nil {
		return fmt.Errorf("create service directory: %w", err)
	}
	if err := os.WriteFile(unitFile, []byte(content), 0o600); err != nil {
		return fmt.Errorf("write service file: %w", err)
	}
	return nil
}

func removeUnitFile(unitFile string, out io.Writer) error {
	err := os.Remove(unitFile)
	if errors.Is(err, os.ErrNotExist) {
		_, _ = fmt.Fprintln(out, "no service installed")
		return nil
	}
	if err != nil {
		return fmt.Errorf("remove service file: %w", err)
	}
	_, _ = fmt.Fprintf(out, "service removed: %s\n", unitFile)
	return nil
}

// xmlEscape escapes s for use inside a plist <string> element.
func xmlEscape(s string) string {
	return strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
	).Replace(s)
}
