package doctor

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/idle-sync/whatsapp-connect-mcp/internal/clients"
	"github.com/idle-sync/whatsapp-connect-mcp/internal/version"
)

// releaseAPIURL is GitHub's "latest release" API endpoint for this project.
const releaseAPIURL = "https://api.github.com/repos/idle-sync/whatsapp-connect-mcp/releases/latest"

// versionCheckTimeout bounds the version check's HTTP round trip, so a
// slow or unreachable network never makes `doctor`/`check` hang.
const versionCheckTimeout = 2 * time.Second

// registry lists every check Run executes, in the order their findings are
// returned.
func registry() []Check {
	return []Check{
		{Name: "session", Run: checkSession},
		{Name: "database", Run: checkDatabase},
		{Name: "clients", Run: checkClients},
		{Name: "permissions", Run: checkPermissions},
		{Name: "version", Run: checkVersion(&http.Client{Timeout: versionCheckTimeout}, releaseAPIURL)},
	}
}

// checkSession reports whether a WhatsApp session has been paired
// (session.db exists under env.DataDir) and, if so, whether it is
// currently connected (env.LoggedIn).
func checkSession(_ context.Context, env Env) Finding {
	if _, err := os.Stat(filepath.Join(env.DataDir, "session.db")); err != nil {
		return Finding{
			Check:  "session",
			Status: StatusFail,
			Detail: "no paired WhatsApp session found",
			Fix:    "run `whatsapp-connect-mcp setup` to pair a device",
		}
	}
	if env.LoggedIn == nil || !env.LoggedIn() {
		return Finding{
			Check:  "session",
			Status: StatusWarn,
			Detail: "session is paired but not currently connected",
			Fix:    "run `whatsapp-connect-mcp serve` to reconnect, or `setup` again if that fails",
		}
	}
	return Finding{Check: "session", Status: StatusOK, Detail: "session is paired and connected"}
}

// checkDatabase runs the message store's integrity check.
func checkDatabase(_ context.Context, env Env) Finding {
	if env.Store == nil {
		return Finding{
			Check:  "database",
			Status: StatusFail,
			Detail: "no database connection available",
			Fix:    "restart the server and try again",
		}
	}
	if err := env.Store.QuickCheck(); err != nil {
		return Finding{
			Check:  "database",
			Status: StatusFail,
			Detail: "database integrity check failed",
			Fix:    "restore messages.db from a backup, or run `whatsapp-connect-mcp reset` to start fresh (this deletes stored messages)",
		}
	}
	return Finding{Check: "database", Status: StatusOK, Detail: "database integrity check passed"}
}

// checkClients verifies every detected client that has this program
// injected: its config parses, the injected command path exists, and that
// path is exactly env.BinaryPath. A broken client is named by its
// human-readable Name only, never by its config path — see the package
// doc.
func checkClients(_ context.Context, env Env) Finding {
	var broken []string
	checked := 0
	for _, c := range clients.Detect(env.Home) {
		if !c.Injected {
			continue
		}
		checked++
		cmd, ok := clients.InjectedCommand(c.ConfigPath)
		if !ok || cmd != env.BinaryPath {
			broken = append(broken, c.Name)
			continue
		}
		if _, err := os.Stat(cmd); err != nil {
			broken = append(broken, c.Name)
		}
	}

	if checked == 0 {
		return Finding{Check: "clients", Status: StatusOK, Detail: "no MCP clients have this program injected"}
	}
	if len(broken) == 0 {
		return Finding{Check: "clients", Status: StatusOK, Detail: "every injected client config is valid"}
	}
	return Finding{
		Check:  "clients",
		Status: StatusFail,
		Detail: "broken client config: " + strings.Join(broken, ", "),
		Fix:    "run `whatsapp-connect-mcp setup` to re-inject the affected client(s)",
	}
}

// checkPermissions reports whether env.DataDir is readable by users other
// than its owner. POSIX permission bits have no meaningful equivalent on
// Windows (every file reports the same fixed mode), so this check is a
// no-op there.
func checkPermissions(_ context.Context, env Env) Finding {
	if runtime.GOOS == "windows" {
		return Finding{Check: "permissions", Status: StatusOK, Detail: "not applicable on this platform"}
	}

	info, err := os.Stat(env.DataDir)
	if err != nil {
		return Finding{
			Check:  "permissions",
			Status: StatusFail,
			Detail: "could not read the data directory's permissions",
			Fix:    "verify the data directory exists and is readable",
		}
	}
	if info.Mode().Perm()&0o004 != 0 {
		return Finding{
			Check:  "permissions",
			Status: StatusFail,
			Detail: "the data directory is readable by other users on this machine",
			Fix:    "remove group/world access from the data directory (e.g. chmod 700)",
		}
	}
	return Finding{Check: "permissions", Status: StatusOK, Detail: "data directory permissions are private"}
}

// releaseInfo is the subset of GitHub's release API response this check
// reads.
type releaseInfo struct {
	TagName string `json:"tag_name"`
}

// checkVersion returns a check that compares this build's version against
// the latest release published at url, fetched via client. Any failure to
// reach or parse url — unreachable host, timeout, non-200 response,
// malformed body — yields StatusOK with "could not check", never a
// failure: the running server's function must never depend on this
// succeeding.
func checkVersion(client *http.Client, url string) func(ctx context.Context, env Env) Finding {
	return func(ctx context.Context, _ Env) Finding {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return Finding{Check: "version", Status: StatusOK, Detail: "could not check for updates"}
		}

		resp, err := client.Do(req)
		if err != nil {
			return Finding{Check: "version", Status: StatusOK, Detail: "could not check for updates"}
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			return Finding{Check: "version", Status: StatusOK, Detail: "could not check for updates"}
		}

		var rel releaseInfo
		if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
			return Finding{Check: "version", Status: StatusOK, Detail: "could not check for updates"}
		}

		latest := strings.TrimPrefix(strings.TrimSpace(rel.TagName), "v")
		if latest == "" || latest == version.Version {
			return Finding{Check: "version", Status: StatusOK, Detail: "running the latest version"}
		}
		return Finding{
			Check:  "version",
			Status: StatusWarn,
			Detail: "a newer version has been released",
			Fix:    "download the latest release from the project's GitHub releases page",
		}
	}
}
