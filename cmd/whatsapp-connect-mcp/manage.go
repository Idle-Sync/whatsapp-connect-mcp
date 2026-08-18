package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/idle-sync/whatsapp-connect-mcp/internal/bridge"
	"github.com/idle-sync/whatsapp-connect-mcp/internal/clients"
	"github.com/idle-sync/whatsapp-connect-mcp/internal/config"
	"github.com/idle-sync/whatsapp-connect-mcp/internal/doctor"
	"github.com/idle-sync/whatsapp-connect-mcp/internal/mediapath"
	"github.com/idle-sync/whatsapp-connect-mcp/internal/store"
)

// runCheck implements the "check" subcommand: it runs doctor.Run and
// prints each finding as an aligned status/check/detail/fix line, sharing
// the exact rendering the doctor MCP tool uses. It exits 1 if any check
// reports fail, 0 otherwise (a warn finding does not fail the command).
func runCheck(args []string) int {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return 2
	}

	dataDir, err := config.Dir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "check: %v\n", err)
		return 1
	}

	st, err := store.Open(filepath.Join(dataDir, "messages.db"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "check: %v\n", err)
		return 1
	}
	defer func() { _ = st.Close() }()

	// check never connects: it reports what LoggedIn already knows (never
	// true against a bridge that Open alone, without Connect, has left
	// offline) rather than establishing a live connection just to answer a
	// diagnostic question.
	br, err := bridge.Open(context.Background(), dataDir, st, mediapath.Roots{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "check: %v\n", err)
		return 1
	}
	defer func() { _ = br.Close() }()

	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "check: resolve home directory: %v\n", err)
		return 1
	}
	binaryPath, err := resolveBinaryPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "check: %v\n", err)
		return 1
	}

	env := doctor.Env{
		DataDir:      dataDir,
		BinaryPath:   binaryPath,
		Home:         home,
		Store:        st,
		NeedsPairing: br.NeedsPairing,
		LoggedIn:     br.LoggedIn,
	}
	findings := doctor.Run(context.Background(), env)

	fmt.Println(doctor.Render(findings))

	for _, f := range findings {
		if f.Status == doctor.StatusFail {
			return 1
		}
	}
	return 0
}

// runStatus implements the "status" subcommand: whether a session is
// paired, how many rows each store table holds, and which MCP clients have
// this program injected.
func runStatus(args []string) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return 2
	}

	dataDir, err := config.Dir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "status: %v\n", err)
		return 1
	}

	st, err := store.Open(filepath.Join(dataDir, "messages.db"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "status: %v\n", err)
		return 1
	}
	defer func() { _ = st.Close() }()

	br, err := bridge.Open(context.Background(), dataDir, st, mediapath.Roots{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "status: %v\n", err)
		return 1
	}
	defer func() { _ = br.Close() }()

	fmt.Printf("paired: %v\n", !br.NeedsPairing())

	counts, err := st.Counts()
	if err != nil {
		fmt.Fprintf(os.Stderr, "status: %v\n", err)
		return 1
	}
	fmt.Printf("chats: %d\n", counts.Chats)
	fmt.Printf("messages: %d\n", counts.Messages)
	fmt.Printf("contacts: %d\n", counts.Contacts)
	fmt.Printf("calls: %d\n", counts.Calls)

	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "status: resolve home directory: %v\n", err)
		return 1
	}
	fmt.Println("clients:")
	for _, c := range clients.Detect(home) {
		fmt.Printf("  %-16s installed=%-5v injected=%v\n", c.Name, c.Installed, c.Injected)
	}
	return 0
}

// runClients implements the "clients" subcommand: lists detected MCP
// clients and their injection state, or with --remove uninjects this
// program's entry from every client that currently has it.
func runClients(args []string) int {
	fs := flag.NewFlagSet("clients", flag.ContinueOnError)
	remove := fs.Bool("remove", false, "remove this program's entry from every client that has it")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "clients: resolve home directory: %v\n", err)
		return 1
	}
	detected := clients.Detect(home)

	if !*remove {
		for _, c := range detected {
			fmt.Printf("%-16s installed=%-5v injected=%v\n", c.Name, c.Installed, c.Injected)
		}
		return 0
	}

	failed := false
	for _, c := range detected {
		if !c.Injected {
			continue
		}
		if err := clients.Remove(c.ConfigPath); err != nil {
			fmt.Fprintf(os.Stderr, "clients: remove from %s: %v\n", c.Name, err)
			failed = true
			continue
		}
		fmt.Printf("removed from %s\n", c.Name)
	}
	if failed {
		return 1
	}
	return 0
}

// runTrust implements the "trust" subcommand: add/remove a JID from
// config.json's trust list, or list it. With no flags it lists.
func runTrust(args []string) int {
	fs := flag.NewFlagSet("trust", flag.ContinueOnError)
	add := fs.String("add", "", "add a JID to the trusted list")
	remove := fs.String("remove", "", "remove a JID from the trusted list")
	fs.Bool("list", false, "list trusted JIDs (default with no flags)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	dataDir, err := config.Dir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "trust: %v\n", err)
		return 1
	}
	cfg, err := config.Load(dataDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "trust: %v\n", err)
		return 1
	}

	switch {
	case *add != "":
		return trustAdd(dataDir, cfg, *add)
	case *remove != "":
		return trustRemove(dataDir, cfg, *remove)
	default:
		return trustList(cfg)
	}
}

func trustAdd(dataDir string, cfg config.Config, jid string) int {
	if !cfg.IsTrusted(jid) {
		cfg.TrustedJIDs = append(cfg.TrustedJIDs, jid)
		sort.Strings(cfg.TrustedJIDs)
		if err := config.Save(dataDir, cfg); err != nil {
			fmt.Fprintf(os.Stderr, "trust: %v\n", err)
			return 1
		}
	}
	fmt.Printf("trusted: %s\n", jid)
	return 0
}

func trustRemove(dataDir string, cfg config.Config, jid string) int {
	idx := -1
	for i, t := range cfg.TrustedJIDs {
		if t == jid {
			idx = i
			break
		}
	}
	if idx >= 0 {
		cfg.TrustedJIDs = append(cfg.TrustedJIDs[:idx], cfg.TrustedJIDs[idx+1:]...)
		if err := config.Save(dataDir, cfg); err != nil {
			fmt.Fprintf(os.Stderr, "trust: %v\n", err)
			return 1
		}
	}
	fmt.Printf("untrusted: %s\n", jid)
	return 0
}

func trustList(cfg config.Config) int {
	if len(cfg.TrustedJIDs) == 0 {
		fmt.Println("no trusted JIDs")
		return 0
	}
	for _, jid := range cfg.TrustedJIDs {
		fmt.Println(jid)
	}
	return 0
}

// runRemove implements the "remove" subcommand: after a typed "yes", it
// deletes the local session store (session.db and its WAL/SHM sidecars).
// This unpairs the device locally — the next `setup` requires pairing
// again — but does not itself notify WhatsApp's servers: remove is scoped
// to local state only, so it never opens an authenticated connection, and
// the phone keeps showing this device as linked until unlinked there.
// Messages already stored, client configuration, and settings are left
// untouched.
func runRemove(args []string) int {
	fs := flag.NewFlagSet("remove", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return 2
	}

	dataDir, err := config.Dir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "remove: %v\n", err)
		return 1
	}

	fmt.Println("This deletes the local WhatsApp session (session.db). Stored messages,")
	fmt.Println("client configuration, and settings are kept. WhatsApp itself keeps")
	fmt.Println("showing this device as linked until you unlink it from your phone.")
	fmt.Print(`Type "yes" to continue: `)
	if !readConfirmYes(os.Stdin) {
		fmt.Println("aborted — nothing was changed")
		return 1
	}

	if err := deleteSQLiteDB(dataDir, "session.db"); err != nil {
		fmt.Fprintf(os.Stderr, "remove: %v\n", err)
		return 1
	}
	fmt.Println("session removed")
	return 0
}

// runReset implements the "reset" subcommand: after a typed "yes", it does
// everything "remove" does, plus deletes messages.db, the media directory,
// and config.json. Injected client configuration entries are left as-is —
// run `clients --remove` separately if those should go too.
func runReset(args []string) int {
	fs := flag.NewFlagSet("reset", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return 2
	}

	dataDir, err := config.Dir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "reset: %v\n", err)
		return 1
	}

	fmt.Println("This deletes the local WhatsApp session, all stored messages, downloaded")
	fmt.Println("media, and settings (trust list, rate limits). Client configuration")
	fmt.Println("entries are left as-is; run \"clients --remove\" separately for those.")
	fmt.Print(`Type "yes" to continue: `)
	if !readConfirmYes(os.Stdin) {
		fmt.Println("aborted — nothing was changed")
		return 1
	}

	if err := deleteSQLiteDB(dataDir, "session.db"); err != nil {
		fmt.Fprintf(os.Stderr, "reset: %v\n", err)
		return 1
	}
	if err := deleteSQLiteDB(dataDir, "messages.db"); err != nil {
		fmt.Fprintf(os.Stderr, "reset: %v\n", err)
		return 1
	}
	if err := os.RemoveAll(filepath.Join(dataDir, "media")); err != nil {
		fmt.Fprintf(os.Stderr, "reset: delete media directory: %v\n", err)
		return 1
	}
	if err := os.Remove(filepath.Join(dataDir, "config.json")); err != nil && !errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(os.Stderr, "reset: delete config file: %v\n", err)
		return 1
	}

	fmt.Println("reset complete")
	return 0
}

// deleteSQLiteDB removes dataDir/name and its WAL/SHM sidecar files, if
// present. Sidecar absence is not an error; the main file's is, unless it
// was never created (nothing to unpair/reset).
func deleteSQLiteDB(dataDir, name string) error {
	path := filepath.Join(dataDir, name)
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("delete %s: %w", name, err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		_ = os.Remove(path + suffix)
	}
	return nil
}

// readConfirmYes reads one line from in and reports whether it is exactly
// "yes" (case-sensitive, trimmed of surrounding whitespace) — the
// deliberately unambiguous confirmation these destructive commands require.
func readConfirmYes(in io.Reader) bool {
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false
	}
	return strings.TrimSpace(line) == "yes"
}
