// Package wizard drives the interactive "setup" flow: pair via QR if
// needed, detect installed MCP clients, let the user pick which ones to
// configure, and write nothing until a final explicit confirmation
// (commit-at-end). Every effectful step is an injected function so the flow
// is testable without a real WhatsApp session or filesystem.
package wizard

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/mdp/qrterminal/v3"
)

// ErrAborted is returned by Run whenever the flow ends without configuring
// anything: the user declines the final confirmation, or ctx is cancelled
// (Ctrl+C) at any prompt. Callers surface Error() verbatim and exit
// non-zero.
var ErrAborted = errors.New("aborted — nothing was changed")

// Client is one MCP client candidate offered to the user: either detected
// by Deps.Detect or the synthetic "custom path" entry Run appends, which
// carries an empty ConfigPath as its marker.
type Client struct {
	Name       string
	ConfigPath string
	Installed  bool
	Injected   bool
}

// Deps are the effectful operations Run drives. cmd/ wires the real
// bridge, clients, and config packages; tests supply fakes so the flow can
// be scripted without touching WhatsApp or the filesystem.
type Deps struct {
	// NeedsPairing reports whether QR pairing must run before any client
	// can be configured.
	NeedsPairing func() bool
	// PairQR runs QR pairing, invoking show with each QR payload
	// WhatsApp issues until the phone scans one, pairing fails, or ctx is
	// cancelled.
	PairQR func(ctx context.Context, show func(code string)) error
	// Detect lists the known MCP clients on this machine.
	Detect func() []Client
	// Inject writes the injected-server entry into configPath, pointed
	// at BinaryPath. Called once per selected client, only after the
	// user confirms.
	Inject func(configPath, binaryPath string) error
	// InjectHTTP writes an HTTP-transport entry into configPath, pointed
	// at the shared server on 127.0.0.1:port. Called instead of Inject
	// when the user picks the http transport.
	InjectHTTP func(configPath string, port int) error
	// BinaryPath is the absolute path to this binary, written into every
	// injected entry.
	BinaryPath string
}

// defaultHTTPPort is the port the http transport prompt offers when the
// user just presses enter: outside the common dev-server range, so a
// shared server on it is unlikely to collide with anything.
const defaultHTTPPort = 2178

// transport is the user's connection-mode choice.
type transport struct {
	http bool
	port int // meaningful only when http
}

// Run drives the flow over in/out to completion. Nothing is written to
// disk before the user's final "y" confirmation: every step before it only
// reads input and writes prompts to out. Cancelling ctx is checked before
// every prompt and returns ErrAborted immediately, without calling
// Deps.Inject.
func Run(ctx context.Context, in io.Reader, out io.Writer, deps Deps) error {
	if err := ctx.Err(); err != nil {
		return ErrAborted
	}

	if deps.NeedsPairing() {
		if err := runPairing(ctx, out, deps); err != nil {
			return err
		}
	}

	r := bufio.NewReader(in)

	clients := append([]Client{}, deps.Detect()...)
	clients = append(clients, Client{Name: "Custom path"})

	printClientList(out, clients)

	_, _ = fmt.Fprint(out, "\nSelect clients to configure (e.g. 1,3 or 'all'): ")
	selLine, err := readLine(ctx, r)
	if err != nil {
		return abortOr(err)
	}

	selected, err := parseSelection(selLine, clients)
	if err != nil {
		_, _ = fmt.Fprintln(out, err.Error())
		return err
	}

	targets, err := resolveTargets(ctx, r, out, clients, selected)
	if err != nil {
		return abortOr(err)
	}
	if len(targets) == 0 {
		_, _ = fmt.Fprintln(out, "Nothing selected.")
		return ErrAborted
	}

	tr, err := askTransport(ctx, r, out)
	if err != nil {
		return abortOr(err)
	}

	if !confirmTargets(ctx, r, out, targets, tr) {
		return ErrAborted
	}

	return injectAll(out, deps, targets, tr)
}

// askTransport asks how clients should connect: stdio (each client starts
// its own server process; only one client can be connected at a time,
// because one serve holds the data directory's exclusive lock) or http
// (one shared server on a local port that every client and session
// connects to at once). Empty keeps the stdio default; picking http asks
// for the port, defaulting to defaultHTTPPort.
func askTransport(ctx context.Context, r *bufio.Reader, out io.Writer) (transport, error) {
	_, _ = fmt.Fprintln(out, "\nHow should MCP clients connect?")
	_, _ = fmt.Fprintln(out, "  1) stdio (default) — each client starts its own server; only one client/session at a time")
	_, _ = fmt.Fprintln(out, "  2) http — one shared local server; several clients and sessions at once")
	_, _ = fmt.Fprint(out, "Choose [1/2, default 1]: ")

	answer, err := readLine(ctx, r)
	if err != nil {
		return transport{}, err
	}
	switch strings.ToLower(answer) {
	case "", "1", "stdio":
		return transport{}, nil
	case "2", "http":
	default:
		return transport{}, fmt.Errorf("invalid transport %q: enter 1 (stdio) or 2 (http)", answer)
	}

	_, _ = fmt.Fprintf(out, "Port for the shared server [%d]: ", defaultHTTPPort)
	portLine, err := readLine(ctx, r)
	if err != nil {
		return transport{}, err
	}
	port := defaultHTTPPort
	if portLine != "" {
		port, err = strconv.Atoi(portLine)
		if err != nil || port < 1 || port > 65535 {
			return transport{}, fmt.Errorf("invalid port %q: enter a number between 1 and 65535", portLine)
		}
	}
	return transport{http: true, port: port}, nil
}

// runPairing prints the pairing prompt and drives Deps.PairQR, rendering
// each QR code to out as qrterminal half-block glyphs.
func runPairing(ctx context.Context, out io.Writer, deps Deps) error {
	_, _ = fmt.Fprintln(out, "No paired WhatsApp session. Scan this QR code with WhatsApp > Linked devices > Link a device:")
	err := deps.PairQR(ctx, func(code string) {
		qrterminal.GenerateHalfBlock(code, qrterminal.L, out)
	})
	if err != nil {
		return abortOr(err)
	}
	_, _ = fmt.Fprintln(out, "Paired.")
	return nil
}

// printClientList renders the numbered client menu, including the
// synthetic custom-path entry Run appended as the final item.
func printClientList(out io.Writer, clients []Client) {
	_, _ = fmt.Fprintln(out, "\nDetected MCP clients:")
	for i, c := range clients {
		status := ""
		switch {
		case c.ConfigPath == "":
			// custom-path entry: no status
		case c.Injected:
			status = " [already configured]"
		case !c.Installed:
			status = " [not detected]"
		}
		_, _ = fmt.Fprintf(out, "  %d) %s%s\n", i+1, c.Name, status)
	}
}

// resolveTargets turns the selected indices into concrete Clients,
// prompting for a path when the custom-path entry (identified by an empty
// ConfigPath) is among them.
func resolveTargets(ctx context.Context, r *bufio.Reader, out io.Writer, clients []Client, selected []int) ([]Client, error) {
	var targets []Client
	for _, idx := range selected {
		c := clients[idx]
		if c.ConfigPath == "" {
			_, _ = fmt.Fprint(out, "Custom config file path: ")
			path, err := readLine(ctx, r)
			if err != nil {
				return nil, err
			}
			if path == "" {
				_, _ = fmt.Fprintln(out, "No path given, skipping.")
				continue
			}
			c = Client{Name: path, ConfigPath: path}
		}
		targets = append(targets, c)
	}
	return targets, nil
}

// confirmTargets prints the summary and reads the final yes/no answer.
// A read error (including context cancellation) counts as declined.
func confirmTargets(ctx context.Context, r *bufio.Reader, out io.Writer, targets []Client, tr transport) bool {
	_, _ = fmt.Fprintln(out, "\nWill configure:")
	for _, c := range targets {
		_, _ = fmt.Fprintf(out, "  - %s (%s)\n", c.Name, c.ConfigPath)
	}
	if tr.http {
		_, _ = fmt.Fprintf(out, "Transport: http — shared server at http://127.0.0.1:%d\n", tr.port)
	} else {
		_, _ = fmt.Fprintln(out, "Transport: stdio — each client starts its own server")
	}
	_, _ = fmt.Fprint(out, "\nProceed? [y/N]: ")

	answer, err := readLine(ctx, r)
	if err != nil {
		return false
	}
	answer = strings.ToLower(strings.TrimSpace(answer))
	return answer == "y" || answer == "yes"
}

// injectAll calls Deps.Inject (stdio) or Deps.InjectHTTP (http) for every
// target. One client's failure does not stop the others; all failures are
// reported together. For the http transport it ends by saying how to start
// the shared server, since — unlike stdio — no client will start it.
func injectAll(out io.Writer, deps Deps, targets []Client, tr transport) error {
	var failed []string
	for _, c := range targets {
		var err error
		if tr.http {
			err = deps.InjectHTTP(c.ConfigPath, tr.port)
		} else {
			err = deps.Inject(c.ConfigPath, deps.BinaryPath)
		}
		if err != nil {
			failed = append(failed, fmt.Sprintf("%s: %v", c.Name, err))
			continue
		}
		_, _ = fmt.Fprintf(out, "Configured %s\n", c.Name)
	}
	if len(failed) > 0 {
		return fmt.Errorf("some clients failed to configure: %s", strings.Join(failed, "; "))
	}
	if tr.http {
		_, _ = fmt.Fprintf(out, "\nClients connect to a shared server you run yourself. Start it with:\n")
		_, _ = fmt.Fprintf(out, "  whatsapp-connect-mcp serve --http 127.0.0.1:%d\n", tr.port)
		_, _ = fmt.Fprintln(out, "Clients can only connect while it is running (it does not start on boot).")
	}
	return nil
}

// parseSelection parses a "1,3" or "all" answer against clients: a menu of
// detected clients followed by one synthetic custom-path entry, always
// last (the same shape Run builds, with a nil-or-empty-named ConfigPath
// telling it apart isn't needed here — only its position matters). "all"
// selects every detected client Installed reports as actually present on
// this machine — never the custom-path entry, and never a detected but
// not-installed one, so it can't create a config for an app that isn't
// there; an explicit number can still pick a not-installed client
// deliberately. Explicit numbers are 1-indexed and may include the
// custom-path entry. Out-of-range or non-numeric tokens are a category
// error; duplicates are collapsed.
func parseSelection(input string, clients []Client) ([]int, error) {
	numDetected := len(clients) - 1 // the synthetic custom-path entry is always last
	total := len(clients)

	input = strings.TrimSpace(input)
	if strings.EqualFold(input, "all") {
		var idx []int
		for i := 0; i < numDetected; i++ {
			if clients[i].Installed {
				idx = append(idx, i)
			}
		}
		return idx, nil
	}
	if input == "" {
		return nil, nil
	}

	seen := make(map[int]bool)
	var out []int
	for _, tok := range strings.Split(input, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		n, err := strconv.Atoi(tok)
		if err != nil || n < 1 || n > total {
			return nil, fmt.Errorf("invalid selection %q: enter numbers 1-%d, comma-separated, or 'all'", tok, total)
		}
		idx := n - 1
		if !seen[idx] {
			seen[idx] = true
			out = append(out, idx)
		}
	}
	sort.Ints(out)
	return out, nil
}

// abortOr maps a context cancellation to ErrAborted, passing any other
// error through unchanged.
func abortOr(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return ErrAborted
	}
	return err
}

// readLine reads one line from r, trimmed of its trailing newline and
// surrounding whitespace, or returns ctx.Err() if ctx is cancelled first —
// checked both before starting the read and while waiting for it, so a
// cancellation pending at call time is never missed.
func readLine(ctx context.Context, r *bufio.Reader) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	type result struct {
		line string
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		line, err := r.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			ch <- result{"", err}
			return
		}
		ch <- result{line, nil}
	}()

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case res := <-ch:
		if res.err != nil {
			return "", res.err
		}
		return strings.TrimSpace(res.line), nil
	}
}
