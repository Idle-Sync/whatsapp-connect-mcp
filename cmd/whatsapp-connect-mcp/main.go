// Command whatsapp-connect-mcp is a WhatsApp MCP server shipped as a single
// static binary.
package main

import (
	"fmt"
	"os"

	"github.com/idle-sync/whatsapp-connect-mcp/internal/version"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 1 && (args[0] == "-v" || args[0] == "--version") {
		fmt.Println(version.String())
		return 0
	}

	if len(args) >= 1 {
		switch args[0] {
		case "serve":
			return runServe(args[1:])
		case "setup":
			return runSetup(args[1:])
		case "status":
			return runStatus(args[1:])
		case "check":
			return runCheck(args[1:])
		case "clients":
			return runClients(args[1:])
		case "trust":
			return runTrust(args[1:])
		case "service":
			return runService(args[1:])
		case "logout":
			return runLogout(args[1:])
		case "remove":
			return runRemove(args[1:])
		case "reset":
			return runReset(args[1:])
		case "backup":
			return runBackup(args[1:])
		case "dashboard":
			return runDashboard(args[1:])
		}
	}

	fmt.Fprintln(os.Stderr, "usage: whatsapp-connect-mcp [-v|--version] <command>")
	fmt.Fprintln(os.Stderr, "\ncommands:")
	fmt.Fprintln(os.Stderr, "  setup                  interactive pairing and MCP client setup")
	fmt.Fprintln(os.Stderr, "  serve [--http addr]    run the MCP server (stdio by default)")
	fmt.Fprintln(os.Stderr, "  status                 pairing state, store row counts, injected clients")
	fmt.Fprintln(os.Stderr, "  check                  run diagnostic checks (session, database, clients, ...)")
	fmt.Fprintln(os.Stderr, "  clients [--remove]     list or uninject MCP client entries")
	fmt.Fprintln(os.Stderr, "  trust [--add|--remove jid|--list]   manage the send-trust list")
	fmt.Fprintln(os.Stderr, "  service <install|uninstall|restart> [--http addr]   manage a background serve --http service (macOS/Linux/Windows)")
	fmt.Fprintln(os.Stderr, "  logout                 sign this device out on WhatsApp's servers (unlink), keeping local data")
	fmt.Fprintln(os.Stderr, "  remove                 delete the local session (unpair)")
	fmt.Fprintln(os.Stderr, "  reset                  remove, plus delete messages, media, and settings")
	fmt.Fprintln(os.Stderr, "  backup [--dest path]   snapshot messages.db into a backup file (safe while serve runs)")
	fmt.Fprintln(os.Stderr, "  dashboard [--port n]   print/open a login link for the local web dashboard")
	return 2
}
