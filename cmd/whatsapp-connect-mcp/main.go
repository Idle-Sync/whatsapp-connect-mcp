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
		}
	}

	fmt.Fprintln(os.Stderr, "usage: whatsapp-connect-mcp [-v|--version] <command>")
	fmt.Fprintln(os.Stderr, "\ncommands:")
	fmt.Fprintln(os.Stderr, "  serve [--http addr]   run the MCP server (stdio by default)")
	return 2
}
