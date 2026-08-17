package doctor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/idle-sync/whatsapp-connect-mcp/internal/clients"
	"github.com/idle-sync/whatsapp-connect-mcp/internal/store"
)

func openTestStore(t *testing.T, dir string) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(dir, "messages.db"))
	if err != nil {
		t.Fatalf("store.Open() error: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// --- crash guard ---

func TestRunGuardedRecoversFromPanickingCheck(t *testing.T) {
	c := Check{
		Name: "boom",
		Run: func(context.Context, Env) Finding {
			panic("check bug")
		},
	}

	got := runGuarded(context.Background(), c, Env{})

	if got.Status != StatusFail {
		t.Fatalf("Status = %q, want %q", got.Status, StatusFail)
	}
	if got.Check != "boom" {
		t.Fatalf("Check = %q, want %q", got.Check, "boom")
	}
	if got.Detail == "" || got.Fix == "" {
		t.Fatalf("expected non-empty Detail and Fix, got %+v", got)
	}
}

func TestRunGuardedPassesThroughNormalResult(t *testing.T) {
	want := Finding{Check: "ok-check", Status: StatusOK, Detail: "fine"}
	c := Check{Name: "ok-check", Run: func(context.Context, Env) Finding { return want }}

	got := runGuarded(context.Background(), c, Env{})

	if got != want {
		t.Fatalf("runGuarded() = %+v, want %+v", got, want)
	}
}

// --- registry / Run ---

// offlineRegistry mirrors the production registry but swaps the live
// version check (real GitHub URL) for one pointed at a closed test server,
// so tests that exercise the full check list never depend on network
// access.
func offlineRegistry(t *testing.T) []Check {
	t.Helper()
	srv := httptest.NewServer(nil)
	client := srv.Client()
	url := srv.URL
	srv.Close() // closed before any request: fails fast, no network needed

	checks := registry()
	checks[len(checks)-1] = Check{Name: "version", Run: checkVersion(client, url)}
	return checks
}

func TestRunReturnsOneFindingPerRegisteredCheck(t *testing.T) {
	dir := t.TempDir()
	env := Env{DataDir: dir, BinaryPath: filepath.Join(dir, "bin"), Home: dir, Store: openTestStore(t, dir)}

	checks := offlineRegistry(t)
	findings := runWith(context.Background(), checks, env)

	if len(findings) != len(checks) {
		t.Fatalf("Run() returned %d findings, want %d (one per registered check)", len(findings), len(checks))
	}
	for i, f := range findings {
		if f.Check != checks[i].Name {
			t.Fatalf("finding[%d].Check = %q, want %q", i, f.Check, checks[i].Name)
		}
		if f.Status != StatusOK && f.Status != StatusWarn && f.Status != StatusFail {
			t.Fatalf("finding[%d].Status = %q, want one of ok/warn/fail", i, f.Status)
		}
	}
}

// --- session check ---

func TestCheckSession(t *testing.T) {
	trueFn := func() bool { return true }
	falseFn := func() bool { return false }

	tests := []struct {
		name         string
		needsPairing func() bool
		loggedIn     func() bool
		wantStatus   string
	}{
		// bridge.Open creates session.db on disk unconditionally (its
		// Upgrade path runs a PRAGMA immediately), whether or not pairing
		// ever completed, so NeedsPairing — not file presence — is the
		// signal that must drive this: a never-paired user still has a
		// session.db on disk.
		{name: "never paired", needsPairing: trueFn, loggedIn: falseFn, wantStatus: StatusFail},
		{name: "never paired, NeedsPairing nil defaults to unpaired", needsPairing: nil, loggedIn: trueFn, wantStatus: StatusFail},
		{name: "paired but not logged in", needsPairing: falseFn, loggedIn: falseFn, wantStatus: StatusWarn},
		{name: "paired but LoggedIn nil defaults to not connected", needsPairing: falseFn, loggedIn: nil, wantStatus: StatusWarn},
		{name: "paired and logged in", needsPairing: falseFn, loggedIn: trueFn, wantStatus: StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := checkSession(context.Background(), Env{NeedsPairing: tt.needsPairing, LoggedIn: tt.loggedIn})
			if got.Status != tt.wantStatus {
				t.Fatalf("Status = %q, want %q (finding: %+v)", got.Status, tt.wantStatus, got)
			}
		})
	}
}

// --- database check ---

func TestCheckDatabaseNilStoreFails(t *testing.T) {
	got := checkDatabase(context.Background(), Env{})
	if got.Status != StatusFail {
		t.Fatalf("Status = %q, want %q", got.Status, StatusFail)
	}
}

func TestCheckDatabasePassesOnFreshStore(t *testing.T) {
	dir := t.TempDir()
	got := checkDatabase(context.Background(), Env{Store: openTestStore(t, dir)})
	if got.Status != StatusOK {
		t.Fatalf("Status = %q, want %q (finding: %+v)", got.Status, StatusOK, got)
	}
}

// --- clients check ---

func TestCheckClients(t *testing.T) {
	t.Run("nothing injected is ok", func(t *testing.T) {
		home := t.TempDir()
		got := checkClients(context.Background(), Env{Home: home, BinaryPath: filepath.Join(home, "bin")})
		if got.Status != StatusOK {
			t.Fatalf("Status = %q, want %q (finding: %+v)", got.Status, StatusOK, got)
		}
	})

	t.Run("injected client pointing at the running binary is ok", func(t *testing.T) {
		home := t.TempDir()
		binPath := filepath.Join(home, "whatsapp-connect-mcp")
		if err := os.WriteFile(binPath, []byte("x"), 0o600); err != nil {
			t.Fatalf("seed binary: %v", err)
		}
		cursor := findClient(t, clients.Detect(home), "Cursor")
		if err := os.MkdirAll(filepath.Dir(cursor.ConfigPath), 0o700); err != nil {
			t.Fatalf("seed client dir: %v", err)
		}
		if err := clients.Inject(cursor.ConfigPath, binPath); err != nil {
			t.Fatalf("Inject() error: %v", err)
		}

		got := checkClients(context.Background(), Env{Home: home, BinaryPath: binPath})
		if got.Status != StatusOK {
			t.Fatalf("Status = %q, want %q (finding: %+v)", got.Status, StatusOK, got)
		}
	})

	t.Run("injected client pointing at a missing binary fails and names the client, not the path", func(t *testing.T) {
		home := t.TempDir()
		missingBin := filepath.Join(home, "gone")
		cursor := findClient(t, clients.Detect(home), "Cursor")
		if err := os.MkdirAll(filepath.Dir(cursor.ConfigPath), 0o700); err != nil {
			t.Fatalf("seed client dir: %v", err)
		}
		if err := clients.Inject(cursor.ConfigPath, missingBin); err != nil {
			t.Fatalf("Inject() error: %v", err)
		}

		got := checkClients(context.Background(), Env{Home: home, BinaryPath: filepath.Join(home, "current-bin")})
		if got.Status != StatusFail {
			t.Fatalf("Status = %q, want %q (finding: %+v)", got.Status, StatusFail, got)
		}
		if !strings.Contains(got.Detail, "Cursor") {
			t.Fatalf("Detail = %q, want it to name Cursor", got.Detail)
		}
		if strings.Contains(got.Detail, home) || strings.Contains(got.Fix, home) {
			t.Fatalf("finding leaks the home directory path: %+v", got)
		}
	})
}

func findClient(t *testing.T, cs []clients.Client, name string) clients.Client {
	t.Helper()
	for _, c := range cs {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("client %q not found", name)
	return clients.Client{}
}

// --- permissions check ---

func TestCheckPermissionsSkipsOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows-only behavior")
	}
	got := checkPermissions(context.Background(), Env{DataDir: t.TempDir()})
	if got.Status != StatusOK {
		t.Fatalf("Status = %q, want %q on windows", got.Status, StatusOK)
	}
}

func TestCheckPermissionsFlagsWorldReadableDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX-only check")
	}
	dir := t.TempDir()
	// World-readable is exactly the condition under test; 0705 must stay
	// world-readable for this test to mean anything.
	if err := os.Chmod(dir, 0o705); err != nil { // #nosec G302 -- deliberately permissive: this is the fixture for the "too open" case
		t.Fatalf("chmod: %v", err)
	}
	got := checkPermissions(context.Background(), Env{DataDir: dir})
	if got.Status != StatusFail {
		t.Fatalf("Status = %q, want %q for a world-readable directory", got.Status, StatusFail)
	}
}

func TestCheckPermissionsPassesForPrivateDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX-only check")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil { // #nosec G302 -- 0700 (owner rwx, no group/other) is the private mode this check verifies, not an overly permissive one
		t.Fatalf("chmod: %v", err)
	}
	got := checkPermissions(context.Background(), Env{DataDir: dir})
	if got.Status != StatusOK {
		t.Fatalf("Status = %q, want %q for a private directory", got.Status, StatusOK)
	}
}

// --- version check ---

func TestCheckVersionUnreachableServerIsOK(t *testing.T) {
	srv := httptest.NewServer(nil)
	client := srv.Client()
	url := srv.URL
	srv.Close() // closed before the request: connection refused, not a timeout

	run := checkVersion(client, url)
	got := run(context.Background(), Env{})

	if got.Status != StatusOK {
		t.Fatalf("Status = %q, want %q for an unreachable server", got.Status, StatusOK)
	}
	if got.Detail == "" {
		t.Fatal("expected a non-empty Detail explaining the check could not run")
	}
}

func TestCheckVersionNon200IsOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	run := checkVersion(srv.Client(), srv.URL)
	got := run(context.Background(), Env{})

	if got.Status != StatusOK {
		t.Fatalf("Status = %q, want %q for a non-200 response", got.Status, StatusOK)
	}
}

// --- sanitization ---

var (
	jidPattern     = regexp.MustCompile(`\d{5,}@(s\.whatsapp\.net|g\.us)`)
	phonePattern   = regexp.MustCompile(`\+?\d{7,}`)
	dsnPattern     = regexp.MustCompile(`(?i)file:.*\.db|_pragma=|sqlite://`)
	messageContent = "the secret plan is at midnight"
)

func TestFindingsAreSanitized(t *testing.T) {
	home := t.TempDir()
	dataDir := filepath.Join(home, "data")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatalf("seed data dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "session.db"), []byte("x"), 0o600); err != nil {
		t.Fatalf("seed session.db: %v", err)
	}

	st := openTestStore(t, dataDir)
	seedSensitiveData(t, st)

	binPath := filepath.Join(home, "whatsapp-connect-mcp")
	env := Env{
		DataDir:      dataDir,
		BinaryPath:   binPath,
		Home:         home,
		Store:        st,
		NeedsPairing: func() bool { return false },
		LoggedIn:     func() bool { return false },
	}

	for _, f := range runWith(context.Background(), offlineRegistry(t), env) {
		for _, field := range []string{f.Detail, f.Fix} {
			if field == "" {
				continue
			}
			if jidPattern.MatchString(field) {
				t.Errorf("check %q leaked a JID: %q", f.Check, field)
			}
			if phonePattern.MatchString(field) {
				t.Errorf("check %q leaked a phone number: %q", f.Check, field)
			}
			if dsnPattern.MatchString(field) {
				t.Errorf("check %q leaked a DSN-like string: %q", f.Check, field)
			}
			if strings.Contains(field, home) {
				t.Errorf("check %q leaked the home directory prefix: %q", f.Check, field)
			}
			if strings.Contains(field, messageContent) {
				t.Errorf("check %q leaked message content: %q", f.Check, field)
			}
		}
	}
}

func seedSensitiveData(t *testing.T, st *store.Store) {
	t.Helper()
	const chatJID = "15551234567@s.whatsapp.net"
	if err := st.UpsertChat(chatJID, "Alice", false, 1700000000); err != nil {
		t.Fatalf("seed chat: %v", err)
	}
	if err := st.UpsertContact(chatJID, "+15551234567", "Alice", "Alice Example", ""); err != nil {
		t.Fatalf("seed contact: %v", err)
	}
	msg := store.Message{
		ChatJID: chatJID, ID: "msg1", SenderJID: chatJID,
		TS: 1700000000, Kind: "text", Text: messageContent,
	}
	if err := st.UpsertMessage(msg); err != nil {
		t.Fatalf("seed message: %v", err)
	}
}

// --- Render ---

func TestRenderProducesOneLinePerFinding(t *testing.T) {
	findings := []Finding{
		{Check: "session", Status: StatusOK, Detail: "connected"},
		{Check: "clients", Status: StatusFail, Detail: "broken client config: Cursor", Fix: "run setup"},
	}
	out := Render(findings)
	lines := strings.Split(out, "\n")
	if len(lines) != 2 {
		t.Fatalf("Render() produced %d lines, want 2: %q", len(lines), out)
	}
	for _, want := range []string{"session", "connected", "clients", "Cursor", "run setup"} {
		if !strings.Contains(out, want) {
			t.Fatalf("Render() output missing %q: %q", want, out)
		}
	}
}
