package wizard

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

// fakeDetect returns three clients: none installed/injected, so every
// selection path is exercised without extra state to track.
func fakeDetect() []Client {
	return []Client{
		{Name: "Alpha", ConfigPath: "/cfg/alpha.json"},
		{Name: "Beta", ConfigPath: "/cfg/beta.json"},
		{Name: "Gamma", ConfigPath: "/cfg/gamma.json"},
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
			input:      "1,3\ny\n",
			wantInject: []string{"/cfg/alpha.json", "/cfg/gamma.json"},
		},
		{
			name:       "yes spelled out also confirms",
			input:      "2\nyes\n",
			wantInject: []string{"/cfg/beta.json"},
		},
		{
			name:       "all selects every detected client, never the custom entry",
			input:      "all\ny\n",
			wantInject: []string{"/cfg/alpha.json", "/cfg/beta.json", "/cfg/gamma.json"},
		},
		{
			name:       "declining the confirm injects nothing",
			input:      "1\nn\n",
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
			input:      "4\n/custom/config.json\ny\n",
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
	err := Run(context.Background(), strings.NewReader("1\ny\n"), &out, deps)
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

	err := Run(ctx, strings.NewReader("1\ny\n"), &bytes.Buffer{}, deps)
	if !errors.Is(err, ErrAborted) {
		t.Fatalf("Run() error = %v, want ErrAborted", err)
	}
	if err.Error() != "aborted — nothing was changed" {
		t.Fatalf("Run() error text = %q, want exact abort message", err.Error())
	}
}

func TestParseSelection(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		numDetected int
		total       int
		want        []int
		wantErr     bool
	}{
		{"single", "2", 3, 4, []int{1}, false},
		{"multiple sorted", "3,1", 3, 4, []int{0, 2}, false},
		{"duplicates collapsed", "1,1,2", 3, 4, []int{0, 1}, false},
		{"all excludes custom entry", "all", 3, 4, []int{0, 1, 2}, false},
		{"ALL case-insensitive", "ALL", 3, 4, []int{0, 1, 2}, false},
		{"empty input selects nothing", "", 3, 4, nil, false},
		{"zero is out of range", "0", 3, 4, nil, true},
		{"above total is out of range", "5", 3, 4, nil, true},
		{"non-numeric token", "x", 3, 4, nil, true},
		{"custom entry selectable by number", "4", 3, 4, []int{3}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseSelection(tt.input, tt.numDetected, tt.total)
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
