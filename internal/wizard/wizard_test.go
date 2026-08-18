package wizard

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

// fakeDetect returns three clients, all installed (so "all" behaves the
// same as picking every number) and none injected yet, so every selection
// path is exercised without extra state to track.
func fakeDetect() []Client {
	return []Client{
		{Name: "Alpha", ConfigPath: "/cfg/alpha.json", Installed: true},
		{Name: "Beta", ConfigPath: "/cfg/beta.json", Installed: true},
		{Name: "Gamma", ConfigPath: "/cfg/gamma.json", Installed: true},
	}
}

// baseDeps returns Deps wired to fakeDetect, a no-op PairQR, and an Inject
// that records every call in injected. NeedsPairing defaults to false so
// tests that don't care about pairing skip straight to selection.
func baseDeps(t *testing.T, injected *[]string) Deps {
	t.Helper()
	return Deps{
		NeedsPairing: func() bool { return false },
		PairQR: func(context.Context, func(string)) error {
			t.Fatal("PairQR called though NeedsPairing() = false")
			return nil
		},
		Detect: fakeDetect,
		Inject: func(configPath, binaryPath string) error {
			if binaryPath != "/bin/whatsapp-connect-mcp" {
				t.Fatalf("Inject binaryPath = %q, want /bin/whatsapp-connect-mcp", binaryPath)
			}
			*injected = append(*injected, configPath)
			return nil
		},
		BinaryPath: "/bin/whatsapp-connect-mcp",
	}
}

func TestRunCommitAtEnd(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantErr     error
		wantInject  []string
		wantErrText string
	}{
		{
			name:       "confirm injects only the selected clients",
			input:      "1,3\n\ny\n",
			wantInject: []string{"/cfg/alpha.json", "/cfg/gamma.json"},
		},
		{
			name:       "yes spelled out also confirms",
			input:      "2\n\nyes\n",
			wantInject: []string{"/cfg/beta.json"},
		},
		{
			name:       "all selects every detected client, never the custom entry",
			input:      "all\n\ny\n",
			wantInject: []string{"/cfg/alpha.json", "/cfg/beta.json", "/cfg/gamma.json"},
		},
		{
			name:       "declining the confirm injects nothing",
			input:      "1\n\nn\n",
			wantErr:    ErrAborted,
			wantInject: nil,
		},
		{
			name:       "empty confirm answer is a decline",
			input:      "1\n\n",
			wantErr:    ErrAborted,
			wantInject: nil,
		},
		{
			name:        "out-of-range selection is rejected before any read of the confirm line",
			input:       "9\n",
			wantErrText: "invalid selection",
			wantInject:  nil,
		},
		{
			name:        "non-numeric selection is rejected",
			input:       "abc\n",
			wantErrText: "invalid selection",
			wantInject:  nil,
		},
		{
			name:       "custom path entry prompts for a path and injects it",
			input:      "4\n/custom/config.json\n\ny\n",
			wantInject: []string{"/custom/config.json"},
		},
		{
			name:       "custom path left empty is skipped, nothing left selected aborts",
			input:      "4\n\n",
			wantErr:    ErrAborted,
			wantInject: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var injected []string
			deps := baseDeps(t, &injected)

			var out bytes.Buffer
			err := Run(context.Background(), strings.NewReader(tt.input), &out, deps)

			switch {
			case tt.wantErr != nil:
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("Run() error = %v, want %v", err, tt.wantErr)
				}
			case tt.wantErrText != "":
				if err == nil || !strings.Contains(err.Error(), tt.wantErrText) {
					t.Fatalf("Run() error = %v, want containing %q", err, tt.wantErrText)
				}
			default:
				if err != nil {
					t.Fatalf("Run() error = %v, want nil", err)
				}
			}

			if !slices.Equal(injected, tt.wantInject) {
				t.Fatalf("injected = %v, want %v", injected, tt.wantInject)
			}
		})
	}
}

// TestRunAllSelectionExcludesNotInstalledClients proves "all" no longer
// writes a config for a client whose app isn't actually on this machine:
// Beta is detected (e.g. a config path convention matched) but not
// installed, so "all" must skip it while still picking up Alpha and Gamma.
func TestRunAllSelectionExcludesNotInstalledClients(t *testing.T) {
	var injected []string
	deps := baseDeps(t, &injected)
	deps.Detect = func() []Client {
		return []Client{
			{Name: "Alpha", ConfigPath: "/cfg/alpha.json", Installed: true},
			{Name: "Beta", ConfigPath: "/cfg/beta.json", Installed: false},
			{Name: "Gamma", ConfigPath: "/cfg/gamma.json", Installed: true},
		}
	}

	var out bytes.Buffer
	err := Run(context.Background(), strings.NewReader("all\n\ny\n"), &out, deps)
	if err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
	if !slices.Equal(injected, []string{"/cfg/alpha.json", "/cfg/gamma.json"}) {
		t.Fatalf("injected = %v, want only the installed clients (Beta filtered out of 'all')", injected)
	}
}

// TestRunExplicitSelectionCanStillPickANotInstalledClient proves the "all"
// filter doesn't reach explicit numbered selection: a user who deliberately
// types the number for a not-detected-as-installed client still gets it
// configured.
func TestRunExplicitSelectionCanStillPickANotInstalledClient(t *testing.T) {
	var injected []string
	deps := baseDeps(t, &injected)
	deps.Detect = func() []Client {
		return []Client{
			{Name: "Alpha", ConfigPath: "/cfg/alpha.json", Installed: true},
			{Name: "Beta", ConfigPath: "/cfg/beta.json", Installed: false},
		}
	}

	var out bytes.Buffer
	err := Run(context.Background(), strings.NewReader("2\n\ny\n"), &out, deps)
	if err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
	if !slices.Equal(injected, []string{"/cfg/beta.json"}) {
		t.Fatalf("injected = %v, want Beta injected despite not being installed (explicit numbered pick)", injected)
	}
}

// TestRunPairingSuccessThenInjects proves the pairing step runs first, and
// only on success does the flow continue to selection and injection.
func TestRunPairingSuccessThenInjects(t *testing.T) {
	var injected []string
	deps := baseDeps(t, &injected)
	deps.NeedsPairing = func() bool { return true }
	pairCalled := false
	deps.PairQR = func(_ context.Context, show func(string)) error {
		pairCalled = true
		show("fake-qr-payload")
		return nil
	}

	var out bytes.Buffer
	err := Run(context.Background(), strings.NewReader("1\n\ny\n"), &out, deps)
	if err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
	if !pairCalled {
		t.Fatal("PairQR was never called though NeedsPairing() = true")
	}
	if !slices.Equal(injected, []string{"/cfg/alpha.json"}) {
		t.Fatalf("injected = %v, want [/cfg/alpha.json]", injected)
	}
	if out.Len() == 0 {
		t.Fatal("no output written during pairing")
	}
}

// TestRunPairingFailureAborts proves a PairQR error (e.g. the phone never
// scans and the flow is cancelled) stops the wizard before any client is
// ever detected or injected.
func TestRunPairingFailureAborts(t *testing.T) {
	var injected []string
	deps := baseDeps(t, &injected)
	deps.NeedsPairing = func() bool { return true }
	deps.PairQR = func(ctx context.Context, _ func(string)) error {
		return ctx.Err() // simulates PairQR observing the cancellation itself
	}
	deps.Detect = func() []Client {
		t.Fatal("Detect called after a failed pairing")
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := Run(ctx, strings.NewReader(""), &bytes.Buffer{}, deps)
	if !errors.Is(err, ErrAborted) {
		t.Fatalf("Run() error = %v, want ErrAborted", err)
	}
	if len(injected) != 0 {
		t.Fatalf("injected = %v, want none", injected)
	}
}

// TestRunCtxCancelledBeforeStartAbortsImmediately proves Ctrl+C pending
// before Run even begins short-circuits the whole flow: nothing is
// detected, read, or injected.
func TestRunCtxCancelledBeforeStartAbortsImmediately(t *testing.T) {
	deps := Deps{
		NeedsPairing: func() bool {
			t.Fatal("NeedsPairing called though ctx was already cancelled")
			return false
		},
		PairQR: func(context.Context, func(string)) error {
			t.Fatal("PairQR called though ctx was already cancelled")
			return nil
		},
		Detect: func() []Client {
			t.Fatal("Detect called though ctx was already cancelled")
			return nil
		},
		Inject: func(string, string) error {
			t.Fatal("Inject called though ctx was already cancelled")
			return nil
		},
		BinaryPath: "/bin/whatsapp-connect-mcp",
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := Run(ctx, strings.NewReader("1\n\ny\n"), &bytes.Buffer{}, deps)
	if !errors.Is(err, ErrAborted) {
		t.Fatalf("Run() error = %v, want ErrAborted", err)
	}
	if err.Error() != "aborted — nothing was changed" {
		t.Fatalf("Run() error text = %q, want exact abort message", err.Error())
	}
}

// clientsFixture builds a menu of len(installed) detected clients (each
// Installed per the given flag) plus the synthetic custom-path entry
// parseSelection always expects last.
func clientsFixture(installed ...bool) []Client {
	cs := make([]Client, len(installed)+1)
	for i, inst := range installed {
		cs[i] = Client{Name: "c", Installed: inst}
	}
	cs[len(installed)] = Client{Name: "Custom path"}
	return cs
}

func TestParseSelection(t *testing.T) {
	allInstalled := clientsFixture(true, true, true)
	oneNotInstalled := clientsFixture(true, false, true)

	tests := []struct {
		name    string
		input   string
		clients []Client
		want    []int
		wantErr bool
	}{
		{"single", "2", allInstalled, []int{1}, false},
		{"multiple sorted", "3,1", allInstalled, []int{0, 2}, false},
		{"duplicates collapsed", "1,1,2", allInstalled, []int{0, 1}, false},
		{"all excludes custom entry", "all", allInstalled, []int{0, 1, 2}, false},
		{"ALL case-insensitive", "ALL", allInstalled, []int{0, 1, 2}, false},
		{"all filters out a not-installed client", "all", oneNotInstalled, []int{0, 2}, false},
		{"explicit number can still pick a not-installed client", "2", oneNotInstalled, []int{1}, false},
		{"empty input selects nothing", "", allInstalled, nil, false},
		{"zero is out of range", "0", allInstalled, nil, true},
		{"above total is out of range", "5", allInstalled, nil, true},
		{"non-numeric token", "x", allInstalled, nil, true},
		{"custom entry selectable by number", "4", allInstalled, []int{3}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseSelection(tt.input, tt.clients)
			if tt.wantErr {
				if err == nil {
					t.Fatal("parseSelection() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("parseSelection() error = %v, want nil", err)
			}
			if !slices.Equal(got, tt.want) {
				t.Fatalf("parseSelection() = %v, want %v", got, tt.want)
			}
		})
	}
}

// httpDeps is baseDeps plus a recording InjectHTTP; tests drive the
// transport prompt to reach it.
func httpDeps(t *testing.T, injected *[]string, ports *[]int) Deps {
	t.Helper()
	deps := baseDeps(t, injected)
	deps.InjectHTTP = func(configPath string, port int) error {
		*injected = append(*injected, configPath)
		*ports = append(*ports, port)
		return nil
	}
	return deps
}

// Choosing the HTTP transport asks for a port (defaulting when left
// empty), routes every selected client through InjectHTTP instead of
// Inject, and tells the user how to start the shared server.
func TestRunHTTPTransportDefaultPort(t *testing.T) {
	var injected []string
	var ports []int
	deps := httpDeps(t, &injected, &ports)
	deps.Inject = func(string, string) error {
		t.Fatal("stdio Inject called though the http transport was chosen")
		return nil
	}

	var out bytes.Buffer
	// select Alpha, transport 2 (http), empty port (default), confirm.
	err := Run(context.Background(), strings.NewReader("1\n2\n\ny\n"), &out, deps)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if !slices.Equal(injected, []string{"/cfg/alpha.json"}) || !slices.Equal(ports, []int{2178}) {
		t.Fatalf("InjectHTTP calls = %v ports %v, want alpha on default port 2178", injected, ports)
	}
	if !strings.Contains(out.String(), "serve --http 127.0.0.1:2178") {
		t.Fatalf("output does not tell the user how to start the shared server:\n%s", out.String())
	}
}

func TestRunHTTPTransportExplicitPort(t *testing.T) {
	var injected []string
	var ports []int
	deps := httpDeps(t, &injected, &ports)

	var out bytes.Buffer
	err := Run(context.Background(), strings.NewReader("1,2\n2\n9137\ny\n"), &out, deps)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !slices.Equal(ports, []int{9137, 9137}) {
		t.Fatalf("ports = %v, want 9137 for both clients", ports)
	}
}

func TestRunHTTPTransportRejectsBadPort(t *testing.T) {
	for _, port := range []string{"abc", "0", "70000", "-5"} {
		var injected []string
		var ports []int
		deps := httpDeps(t, &injected, &ports)

		var out bytes.Buffer
		err := Run(context.Background(), strings.NewReader("1\n2\n"+port+"\n"), &out, deps)
		if err == nil || !strings.Contains(err.Error(), "port") {
			t.Fatalf("Run(port=%q) error = %v, want a port validation error", port, err)
		}
		if len(injected) != 0 {
			t.Fatalf("Run(port=%q) injected %v, want nothing on a rejected port", port, injected)
		}
	}
}

// An unrecognized transport answer is an error, not silently stdio.
func TestRunTransportRejectsUnknownAnswer(t *testing.T) {
	var injected []string
	deps := baseDeps(t, &injected)

	var out bytes.Buffer
	err := Run(context.Background(), strings.NewReader("1\nbanana\n"), &out, deps)
	if err == nil || !strings.Contains(err.Error(), "transport") {
		t.Fatalf("Run() error = %v, want a transport validation error", err)
	}
	if len(injected) != 0 {
		t.Fatalf("injected = %v, want nothing", injected)
	}
}

// The empty answer keeps the old behavior: stdio, via Deps.Inject.
func TestRunTransportDefaultsToStdio(t *testing.T) {
	var injected []string
	deps := baseDeps(t, &injected)
	deps.InjectHTTP = func(string, int) error {
		t.Fatal("InjectHTTP called though stdio was chosen by default")
		return nil
	}

	var out bytes.Buffer
	err := Run(context.Background(), strings.NewReader("1\n\ny\n"), &out, deps)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !slices.Equal(injected, []string{"/cfg/alpha.json"}) {
		t.Fatalf("injected = %v, want alpha via stdio Inject", injected)
	}
}
