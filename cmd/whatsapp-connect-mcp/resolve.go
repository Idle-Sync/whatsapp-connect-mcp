package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/idle-sync/whatsapp-connect-mcp/internal/config"
	"github.com/idle-sync/whatsapp-connect-mcp/internal/store"
	"github.com/idle-sync/whatsapp-connect-mcp/internal/target"
)

// resolveTarget turns a user-typed name, phone number, or JID into a JID,
// asking which was meant when a name matches several contacts.
//
// It opens the message store read-only for the lookup and closes it again,
// rather than holding it: `trust` and `scope` are short commands that may
// well run while a serve process owns the same files, and SQLite's WAL
// mode plus the busy timeout make a concurrent read safe.
//
// A name that cannot be resolved is an error, never a silent fallthrough
// to storing the raw text. A JID that never matches anything is exactly
// the failure this indirection exists to remove.
func resolveTarget(cmd, input string, in io.Reader, out io.Writer) (string, bool) {
	if target.IsJID(strings.TrimSpace(input)) {
		return strings.TrimSpace(input), true
	}

	dataDir, err := config.Dir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", cmd, err)
		return "", false
	}
	st, err := store.Open(filepath.Join(dataDir, "messages.db"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", cmd, err)
		return "", false
	}
	defer func() { _ = st.Close() }()

	jid, err := target.Resolve(st, input)
	if err == nil {
		return jid, true
	}

	var amb *target.AmbiguousError
	if errors.As(err, &amb) {
		return chooseCandidate(cmd, amb, in, out)
	}
	fmt.Fprintf(os.Stderr, "%s: %v\n", cmd, err)
	if errors.Is(err, target.ErrNoMatch) {
		fmt.Fprintf(os.Stderr, "%s: try the phone number instead, e.g. +15551234567\n", cmd)
	}
	return "", false
}

// chooseCandidate prints the matches and asks which one was meant. When
// nothing can be read back — a script, a pipe, a CI job — it prints the
// same list and fails, so an unattended run never picks a person for you.
func chooseCandidate(cmd string, amb *target.AmbiguousError, in io.Reader, out io.Writer) (string, bool) {
	_, _ = fmt.Fprintf(out, "%q matches several contacts:\n", amb.Input)
	for i, c := range amb.Candidates {
		_, _ = fmt.Fprintf(out, "  %d) %s\n", i+1, target.Describe(c))
	}
	_, _ = fmt.Fprint(out, "Which one? Enter a number, or the phone number itself: ")

	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && line == "" {
		fmt.Fprintf(os.Stderr, "\n%s: several matches — re-run with the phone number\n", cmd)
		return "", false
	}
	answer := strings.TrimSpace(line)
	if answer == "" {
		fmt.Fprintf(os.Stderr, "%s: nothing chosen\n", cmd)
		return "", false
	}
	if n, err := strconv.Atoi(answer); err == nil {
		if n < 1 || n > len(amb.Candidates) {
			fmt.Fprintf(os.Stderr, "%s: %d is not one of the choices\n", cmd, n)
			return "", false
		}
		return amb.Candidates[n-1].JID, true
	}
	// Not a menu number, so treat it as a fresh answer — but only a phone
	// number or a JID, since another ambiguous name would just loop.
	if target.IsJID(answer) {
		return answer, true
	}
	if jid, err := target.Resolve(noLookup{}, answer); err == nil {
		return jid, true
	}
	fmt.Fprintf(os.Stderr, "%s: expected one of the numbers above, or a phone number\n", cmd)
	return "", false
}

// noLookup resolves phone numbers and JIDs only — it is handed to Resolve
// when a name lookup has already happened and must not happen again.
type noLookup struct{}

func (noLookup) SearchContacts(string, int) ([]store.ContactRow, error) { return nil, nil }
func (noLookup) Chats(string, bool, int) ([]store.ChatRow, error)       { return nil, nil }
