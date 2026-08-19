package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"runtime"

	"github.com/idle-sync/whatsapp-connect-mcp/internal/config"
	"github.com/idle-sync/whatsapp-connect-mcp/internal/service"
)

// runService implements the "service" subcommand: install, uninstall, or
// restart a background service running `serve --http` — a launchd user
// agent on macOS, a systemd user unit on Linux. The service definition is
// rendered by internal/service from the resolved binary path, so it never
// depends on the init system's own PATH.
func runService(args []string) int {
	if len(args) < 1 {
		printServiceUsage()
		return 2
	}
	action := args[0]

	fs := flag.NewFlagSet("service", flag.ContinueOnError)
	httpAddr := fs.String("http", fmt.Sprintf("127.0.0.1:%d", config.DefaultHTTPPort),
		"address the managed serve listens on (install only; keep it loopback)")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}

	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "service: resolve home directory: %v\n", err)
		return 1
	}
	exe, err := resolveBinaryPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "service: %v\n", err)
		return 1
	}
	program, nodeDir, err := service.ResolveProgram(exe, exec.LookPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "service: %v\n", err)
		return 1
	}

	cfg := service.Config{
		BinaryPath: program,
		NodeDir:    nodeDir,
		HTTPAddr:   *httpAddr,
		Home:       home,
		UID:        os.Getuid(),
	}
	// launchctl and systemctl report their own failures on stderr; the
	// runner surfaces that output directly rather than swallowing it.
	run := func(name string, cmdArgs ...string) error {
		c := exec.Command(name, cmdArgs...) // #nosec G204 -- name is a fixed init-system binary; args are built from resolved local paths, not untrusted input
		c.Stdout, c.Stderr = os.Stdout, os.Stderr
		return c.Run()
	}

	switch action {
	case "install":
		err = service.Install(runtime.GOOS, cfg, run, os.Stdout)
	case "uninstall":
		err = service.Uninstall(runtime.GOOS, cfg, run, os.Stdout)
	case "restart":
		err = service.Restart(runtime.GOOS, cfg, run, os.Stdout)
	default:
		printServiceUsage()
		return 2
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "service: %v\n", err)
		return 1
	}
	return 0
}

func printServiceUsage() {
	fmt.Fprintln(os.Stderr, "usage: whatsapp-connect-mcp service <install|uninstall|restart> [--http addr]")
	fmt.Fprintln(os.Stderr, "\ninstall    write and start a background serve --http service (launchd/systemd)")
	fmt.Fprintln(os.Stderr, "uninstall  stop the service and remove its definition")
	fmt.Fprintln(os.Stderr, "restart    restart it, e.g. after updating the binary")
}
