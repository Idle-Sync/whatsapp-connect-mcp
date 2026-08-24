package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/idle-sync/whatsapp-connect-mcp/internal/config"
	"github.com/idle-sync/whatsapp-connect-mcp/internal/store"
)

// runBackup implements the "backup" subcommand: a consistent snapshot of
// messages.db, safe to run while serve is up. Messages are the one thing
// that cannot be recovered any other way — a session can be re-paired,
// history cannot be re-fetched beyond what the phone still holds.
func runBackup(args []string) int {
	fs := flag.NewFlagSet("backup", flag.ContinueOnError)
	dest := fs.String("dest", "", "backup file path (default: <data-dir>/backups/messages-<timestamp>.db)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	dataDir, err := config.Dir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "backup: %v\n", err)
		return 1
	}

	st, err := store.Open(filepath.Join(dataDir, "messages.db"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "backup: %v\n", err)
		return 1
	}
	defer func() { _ = st.Close() }()

	path := *dest
	if path == "" {
		backupDir := filepath.Join(dataDir, "backups")
		if err := os.MkdirAll(backupDir, 0o700); err != nil {
			fmt.Fprintf(os.Stderr, "backup: create backups directory: %v\n", err)
			return 1
		}
		path = filepath.Join(backupDir,
			"messages-"+time.Now().UTC().Format("20060102-150405")+".db")
	}

	if err := st.BackupTo(path); err != nil {
		fmt.Fprintf(os.Stderr, "backup: %v\n", err)
		return 1
	}
	// The copy holds message content; keep it owner-only like the original.
	if err := os.Chmod(path, 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "backup: restrict backup file permissions: %v\n", err)
		return 1
	}

	info, err := os.Stat(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "backup: %v\n", err)
		return 1
	}
	fmt.Printf("backup written: %s (%d bytes)\n", path, info.Size())
	return 0
}
