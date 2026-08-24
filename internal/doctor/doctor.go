// Package doctor runs a fixed set of local diagnostic checks — session
// state, database integrity, injected MCP client configuration, data
// directory permissions, and the latest published release — and reports
// each as a sanitized Finding. No check ever sends WhatsApp data anywhere;
// the version check is the only one that makes a network call, and it
// contacts GitHub's release API, never a WhatsApp endpoint.
//
// Every Finding field is safe to show a model or print to a terminal: no
// check may put a JID, phone number, message content, or filesystem path
// into a Detail or Fix string. Checks that need to name something specific
// name a client by its human-readable name, never by config path.
package doctor

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"
)

// Finding statuses, in ascending severity order.
const (
	StatusOK   = "ok"
	StatusWarn = "warn"
	StatusFail = "fail"
)

// Finding is one check's diagnostic result. Detail and Fix must never
// carry a JID, phone number, message content, or filesystem path — see the
// package doc.
type Finding struct {
	Check  string
	Status string // one of StatusOK, StatusWarn, StatusFail
	Detail string
	Fix    string
}

// DBChecker is the store capability checkDatabase needs: an integrity
// check over the message database. Satisfied by *store.Store's QuickCheck
// method; kept as a small consumer-defined interface (rather than
// importing *store.Store directly) so mcpserv can hand it the same Store
// value its other tools already use, without doctor needing to know it is
// a *store.Store.
type DBChecker interface {
	QuickCheck() error
}

// Env holds every dependency a check may need. Home is the user's home
// directory, distinct from DataDir (this program's own data directory):
// checkClients needs it to locate MCP client config files the same way
// clients.Detect does everywhere else in this program. NeedsPairing and
// LoggedIn are *bridge.Bridge's own methods of those names — NeedsPairing
// is the authoritative "has this device ever been paired" signal
// checkSession uses; a nil NeedsPairing is treated the same as true (not
// paired), the more cautious of the two findings.
type Env struct {
	DataDir      string
	BinaryPath   string
	Home         string
	Store        DBChecker
	NeedsPairing func() bool
	LoggedIn     func() bool
	// LastEventAt and OpenedAt are *bridge.Bridge's methods of those
	// names: when the last WhatsApp event reached the event handler (zero
	// for none yet) and when the bridge was constructed. checkEventFlow
	// compares them against LoggedIn to notice a stalled event pipeline;
	// nil skips that check.
	LastEventAt func() time.Time
	OpenedAt    func() time.Time
	// IngestErrors reports how many inbound WhatsApp events failed to
	// write to the message store since the server started; nil skips the
	// ingest check. LastDisconnect is the category of the most recent
	// involuntary disconnect ("" if none); checkSession uses it to name a
	// mid-run logout. Both are *bridge.Bridge methods of those names.
	IngestErrors   func() uint64
	LastDisconnect func() string
}

// Check is one named diagnostic.
type Check struct {
	Name string
	Run  func(ctx context.Context, env Env) Finding
}

// Run executes every registered check against env and returns one Finding
// per check, in registration order. A check that panics — a bug in the
// check itself, not a real diagnostic result — yields a fail Finding
// naming that check instead of crashing the caller.
func Run(ctx context.Context, env Env) []Finding {
	return runWith(ctx, registry(), env)
}

// runWith executes checks against env. Factored out of Run so tests can
// exercise the same guarded-execution loop against a registry that swaps
// the live version check for one pointed at a test server, without ever
// making a real network call.
func runWith(ctx context.Context, checks []Check, env Env) []Finding {
	findings := make([]Finding, len(checks))
	for i, c := range checks {
		findings[i] = runGuarded(ctx, c, env)
	}
	return findings
}

// runGuarded calls c.Run and recovers a panic into a fail Finding, so one
// broken check can never take down doctor.Run or its caller.
func runGuarded(ctx context.Context, c Check, env Env) (finding Finding) {
	defer func() {
		if r := recover(); r != nil {
			finding = Finding{
				Check:  c.Name,
				Status: StatusFail,
				Detail: "check failed unexpectedly",
				Fix:    "report this as a bug",
			}
		}
	}()
	return c.Run(ctx, env)
}

// Render formats findings as aligned "status  check  detail  fix" columns,
// one finding per line — the shared rendering the `check` CLI command and
// the doctor MCP tool both use, so the two never drift into two different
// formats for the same data.
func Render(findings []Finding) string {
	var buf bytes.Buffer
	tw := tabwriter.NewWriter(&buf, 0, 2, 2, ' ', 0)
	for _, f := range findings {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", f.Status, f.Check, f.Detail, f.Fix)
	}
	_ = tw.Flush()
	return strings.TrimRight(buf.String(), "\n")
}
