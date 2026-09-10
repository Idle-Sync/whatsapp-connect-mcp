package main

import (
	"flag"
	"fmt"
	"os"
	"sort"

	"github.com/idle-sync/whatsapp-connect-mcp/internal/config"
)

// runScope implements the "scope" subcommand: read and edit the allowlist
// of chats the MCP tool surface may read.
//
// The dashboard edits the same setting, but only exists alongside
// `serve --http`. Someone on the stdio transport has no dashboard at all,
// so without this they could answer setup's scope question once and never
// change their mind. Like `trust`, it stays CLI-and-dashboard-only: no MCP
// tool writes config.json, so an agent cannot widen its own scope.
func runScope(args []string) int {
	fs := flag.NewFlagSet("scope", flag.ContinueOnError)
	allow := fs.String("allow", "", "allow agents to read this chat — a name, phone number, or JID (turns the limit on)")
	deny := fs.String("deny", "", "stop agents reading this chat — a name, phone number, or JID")
	all := fs.Bool("all", false, "turn the limit off — agents may read every chat")
	fs.Bool("list", false, "show the current scope (default with no flags)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	dataDir, err := config.Dir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "scope: %v\n", err)
		return 1
	}
	acct, err := cliAccount(dataDir, os.Stdout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "scope: %v\n", err)
		return 1
	}
	cfg, err := config.LoadFor(dataDir, acct.Dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "scope: %v\n", err)
		return 1
	}

	switch {
	case *allow != "":
		jid, ok := resolveTarget("scope", *allow, os.Stdin, os.Stdout)
		if !ok {
			return 1
		}
		return scopeAllow(dataDir, acct.Dir, cfg, jid)
	case *deny != "":
		jid, ok := resolveTarget("scope", *deny, os.Stdin, os.Stdout)
		if !ok {
			return 1
		}
		return scopeDeny(dataDir, acct.Dir, cfg, jid)
	case *all:
		return scopeAll(dataDir, acct.Dir, cfg)
	default:
		return scopeList(cfg)
	}
}

// scopeAllow adds one chat and switches the limit on. Naming a chat to
// allow is a request to restrict; leaving the mode alone would accept the
// argument and change nothing an agent can observe.
func scopeAllow(dataDir, accountDir string, cfg config.Config, jid string) int {
	cfg.ChatScope = config.ScopeAllowlist
	if !cfg.IsReadable(jid) {
		cfg.ReadableChats = append(cfg.ReadableChats, jid)
		sort.Strings(cfg.ReadableChats)
	}
	if err := config.SaveFor(dataDir, accountDir, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "scope: %v\n", err)
		return 1
	}
	fmt.Printf("readable: %s\n", jid)
	fmt.Printf("Agents may now read %d of your chats and nothing else.\n", len(cfg.ReadableChats))
	fmt.Println("Applies immediately, including to a serve process already running.")
	return 0
}

// scopeDeny removes one chat, leaving the mode alone. Emptying the list
// leaves agents able to read nothing, which is the safe reading of
// "remove the last chat I allowed"; widening is scopeAll's job.
func scopeDeny(dataDir, accountDir string, cfg config.Config, jid string) int {
	kept := cfg.ReadableChats[:0]
	for _, c := range cfg.ReadableChats {
		if c != jid {
			kept = append(kept, c)
		}
	}
	cfg.ReadableChats = kept
	if err := config.SaveFor(dataDir, accountDir, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "scope: %v\n", err)
		return 1
	}
	fmt.Printf("no longer readable: %s\n", jid)
	if cfg.ScopeActive() && len(cfg.ReadableChats) == 0 {
		fmt.Println("No chats are readable now — every read tool will come back empty.")
		fmt.Println("Use --allow to name one, or --all to drop the limit entirely.")
	}
	return 0
}

func scopeAll(dataDir, accountDir string, cfg config.Config) int {
	cfg.ChatScope = config.ScopeAll
	if err := config.SaveFor(dataDir, accountDir, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "scope: %v\n", err)
		return 1
	}
	fmt.Println("Agents may read every chat.")
	if len(cfg.ReadableChats) > 0 {
		// Kept rather than cleared, so turning the limit back on does not
		// mean retyping the list.
		fmt.Printf("The previous list of %d chats is kept, ready if you turn the limit back on.\n", len(cfg.ReadableChats))
	}
	return 0
}

func scopeList(cfg config.Config) int {
	if !cfg.ScopeActive() {
		fmt.Println("no limit — agents may read every chat")
		return 0
	}
	if len(cfg.ReadableChats) == 0 {
		fmt.Println("no chats are readable — every read tool will come back empty")
		return 0
	}
	for _, jid := range cfg.ReadableChats {
		fmt.Println(jid)
	}
	return 0
}
