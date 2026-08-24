package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"runtime"

	"github.com/idle-sync/whatsapp-connect-mcp/internal/config"
	"github.com/idle-sync/whatsapp-connect-mcp/internal/httpauth"
)

// runDashboard implements the "dashboard" subcommand: print (and try to
// open) a one-time login URL for the local dashboard. This is the only
// place the bearer token appears in a URL, produced on demand by the
// human — serve never logs it.
func runDashboard(args []string) int {
	fs := flag.NewFlagSet("dashboard", flag.ContinueOnError)
	port := fs.Int("port", config.DefaultHTTPPort, "port the shared serve --http listens on")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	dataDir, err := config.Dir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "dashboard: %v\n", err)
		return 1
	}
	token, _, err := httpauth.LoadOrCreateToken(dataDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dashboard: %v\n", err)
		return 1
	}

	url := fmt.Sprintf("http://127.0.0.1:%d/ui/login?token=%s", *port, token)
	fmt.Println(url)
	fmt.Println("(the server must be running: whatsapp-connect-mcp serve --http, or its installed service)")
	openBrowser(url)
	return 0
}

// openBrowser best-effort opens url in the default browser; failure is
// silent — the printed URL is the contract.
func openBrowser(url string) {
	switch runtime.GOOS {
	case "windows":
		_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start() // #nosec G204 -- url is built locally from the data dir's own token, not untrusted input
	case "darwin":
		_ = exec.Command("open", url).Start() // #nosec G204 -- url is built locally from the data dir's own token, not untrusted input
	default:
		_ = exec.Command("xdg-open", url).Start() // #nosec G204 -- url is built locally from the data dir's own token, not untrusted input
	}
}
