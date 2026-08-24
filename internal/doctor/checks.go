package doctor

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
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
		{Name: "events", Run: checkEventFlow},
		{Name: "ingest", Run: checkIngest},
		{Name: "database", Run: checkDatabase},
		{Name: "clients", Run: checkClients},
		{Name: "permissions", Run: checkPermissions},
		{Name: "version", Run: checkVersion(&http.Client{Timeout: versionCheckTimeout}, releaseAPIURL)},
	}
}

// checkSession reports whether a WhatsApp session has been paired
// (env.NeedsPairing) and, if so, whether it is currently connected
// (env.LoggedIn). NeedsPairing is the authoritative pairing signal, not a
// session.db existence check: bridge.Open creates session.db on disk as
// soon as it runs, regardless of whether pairing ever completed, so a
// file-existence check would report "paired" for every never-paired user
// too.
func checkSession(_ context.Context, env Env) Finding {
	if env.NeedsPairing == nil || env.NeedsPairing() {
		detail := "no paired WhatsApp session found"
		if env.LastDisconnect != nil && env.LastDisconnect() == "logged_out" {
			detail = "WhatsApp logged this install out — it is no longer paired"
		}
		return Finding{
			Check:  "session",
			Status: StatusFail,
			Detail: detail,
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

// eventStallThreshold is how long a connected session may go without a
// single WhatsApp event before checkEventFlow warns. Receipts, presence,
// and notifications normally arrive far more often than this on any
// active account, but a quiet account can legitimately go silent for a
// while — hence a warn, not a fail.
const eventStallThreshold = 30 * time.Minute

// checkEventFlow reports whether WhatsApp events are actually reaching the
// event handler. The failure mode it exists for: the socket keepalive
// stays healthy (LoggedIn true) while the event pipeline is dead, so
// messages are silently lost with every other check green. It can only
// warn — event silence has innocent explanations — but the warning names
// the one state the operator cannot otherwise see.
func checkEventFlow(_ context.Context, env Env) Finding {
	if env.LoggedIn == nil || !env.LoggedIn() {
		return Finding{Check: "events", Status: StatusOK, Detail: "not connected — no event flow expected"}
	}
	if env.LastEventAt == nil || env.OpenedAt == nil {
		return Finding{Check: "events", Status: StatusOK, Detail: "event flow not observable here"}
	}

	// The baseline is the later of "last event seen" and "bridge started":
	// a fresh start with no events yet is not a stall.
	base := env.OpenedAt()
	if last := env.LastEventAt(); last.After(base) {
		base = last
	}
	if base.IsZero() {
		return Finding{Check: "events", Status: StatusOK, Detail: "event flow not observable here"}
	}

	quiet := time.Since(base)
	if quiet > eventStallThreshold {
		return Finding{
			Check:  "events",
			Status: StatusWarn,
			Detail: fmt.Sprintf("no WhatsApp events received for %s despite a live connection", quiet.Round(time.Minute)),
			Fix:    "if messages are arriving on the phone, the event pipeline may be stalled — restart serve",
		}
	}
	return Finding{Check: "events", Status: StatusOK, Detail: "events are flowing"}
}

// checkIngest reports whether inbound events are actually landing in the
// message store. The failure mode it exists for: a full disk or corrupted
// messages.db makes every write fail while the connection stays green —
// messages are silently lost with every other check passing.
func checkIngest(_ context.Context, env Env) Finding {
	if env.IngestErrors == nil {
		return Finding{Check: "ingest", Status: StatusOK, Detail: "ingest failures not observable here"}
	}
	if n := env.IngestErrors(); n > 0 {
		return Finding{
			Check:  "ingest",
			Status: StatusFail,
			Detail: fmt.Sprintf("%d incoming events failed to write to the message store since start", n),
			Fix:    "check free disk space and the database finding below; restart serve after resolving",
		}
	}
	return Finding{Check: "ingest", Status: StatusOK, Detail: "incoming events are writing to the message store"}
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
		cmd, url, ok := clients.InjectedEntry(c.ConfigPath)
		if !ok {
			broken = append(broken, c.Name)
			continue
		}
		// An http entry points at a shared server URL rather than a binary;
		// there is no path to validate (whether that server is running is a
		// runtime state, not a config defect).
		if url != "" {
			continue
		}
		if cmd != env.BinaryPath {
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
		// The mismatch itself is the finding: naming both versions lets an
		// agent relay the exact gap instead of a vague "update available".
		return Finding{
			Check:  "version",
			Status: StatusWarn,
			Detail: fmt.Sprintf("running %s, but the latest release is %s", version.Version, latest),
			Fix:    "update with the same method used to install — re-run the install script, `npm update -g whatsapp-connect-mcp`, or the GitHub releases page; see the README's Updating section",
		}
	}
}
