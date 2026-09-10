// Package target turns what a person types — a phone number, a contact or
// group name, or a raw JID — into the JID the trust list and the chat
// scope are keyed by.
//
// Raw JIDs are an implementation detail leaking into the one place a human
// has to type something. Nobody knows their friend as
// 15551234567@s.whatsapp.net, and a mistyped JID fails silently: it is
// accepted, stored, and simply never matches anything.
//
// A name can of course match several people. That is reported as a list of
// candidates rather than guessed at, because guessing wrong here means
// trusting the wrong person or exposing the wrong chat.
package target

import (
	"errors"
	"fmt"
	"strings"

	"github.com/idle-sync/whatsapp-connect-mcp/internal/store"
)

// Candidate is one possible match for an ambiguous input.
type Candidate struct {
	JID     string `json:"jid"`
	Name    string `json:"name"`
	Phone   string `json:"phone"`
	IsGroup bool   `json:"is_group"`
}

// Lookup is the slice of the message store resolution needs. Satisfied by
// *store.Store.
type Lookup interface {
	SearchContacts(query string, limit int) ([]store.ContactRow, error)
	Chats(query string, includeArchived bool, limit int) ([]store.ChatRow, error)
}

// ErrNoMatch is returned when nothing matches the input at all.
var ErrNoMatch = errors.New("no contact or chat matches that name")

// AmbiguousError reports that several contacts or chats matched. Callers
// show Candidates and ask which was meant.
type AmbiguousError struct {
	Input      string
	Candidates []Candidate
}

func (e *AmbiguousError) Error() string {
	return fmt.Sprintf("%q matches %d contacts and chats", e.Input, len(e.Candidates))
}

// searchLimit bounds how many rows each lookup considers. Well past any
// plausible number of same-named contacts, and small enough that an
// ambiguity list stays readable.
const searchLimit = 50

// Resolve turns input into a single JID.
//
// A raw JID passes through untouched. Anything that reads as a phone
// number becomes one directly, without consulting the store — a number is
// unambiguous, and requiring it to already be a known contact would stop
// you allowing a chat before its first message arrives. Everything else is
// matched by name against contacts and chats.
func Resolve(l Lookup, input string) (string, error) {
	in := strings.TrimSpace(input)
	if in == "" {
		return "", errors.New("nothing to look up")
	}
	if IsJID(in) {
		return in, nil
	}
	if digits, ok := asPhone(in); ok {
		return digits + "@s.whatsapp.net", nil
	}

	found, err := Search(l, in)
	if err != nil {
		return "", err
	}
	switch len(found) {
	case 0:
		return "", fmt.Errorf("%w: %q", ErrNoMatch, in)
	case 1:
		return found[0].JID, nil
	default:
		return "", &AmbiguousError{Input: in, Candidates: found}
	}
}

// Search returns every contact and chat whose name matches input,
// deduplicated by JID, contacts first. Exported so the dashboard can offer
// the same candidates as a chooser.
func Search(l Lookup, input string) ([]Candidate, error) {
	needle := strings.ToLower(strings.TrimSpace(input))
	if needle == "" {
		return nil, nil
	}

	var out []Candidate
	seen := map[string]bool{}

	contacts, err := l.SearchContacts(input, searchLimit)
	if err != nil {
		return nil, fmt.Errorf("search contacts: %w", err)
	}
	for _, c := range contacts {
		if seen[c.JID] {
			continue
		}
		seen[c.JID] = true
		out = append(out, Candidate{JID: c.JID, Name: c.Name, Phone: c.Phone})
	}

	// Groups have no contact row, so they only ever turn up here. Archived
	// chats are included: an archived chat is still one you may want to
	// name, and leaving it out would look like the name was simply wrong.
	chats, err := l.Chats(input, true, searchLimit)
	if err != nil {
		return nil, fmt.Errorf("search chats: %w", err)
	}
	for _, c := range chats {
		if seen[c.JID] || !strings.Contains(strings.ToLower(c.Name), needle) {
			continue
		}
		seen[c.JID] = true
		out = append(out, Candidate{JID: c.JID, Name: c.Name, IsGroup: c.IsGroup})
	}
	return out, nil
}

// IsJID reports whether s is already a WhatsApp JID.
func IsJID(s string) bool {
	at := strings.LastIndex(s, "@")
	if at <= 0 || at == len(s)-1 {
		return false
	}
	switch s[at+1:] {
	case "s.whatsapp.net", "g.us", "lid", "broadcast", "newsletter":
		return true
	default:
		return false
	}
}

// asPhone reports whether s reads as a phone number and returns its
// digits. Spaces, dashes, dots, brackets and one leading + are the
// punctuation people actually paste; anything else means it is a name.
// Seven digits is the shortest real subscriber number, and the floor stops
// a name like "007" being dialled.
func asPhone(s string) (string, bool) {
	var digits strings.Builder
	for i, r := range s {
		switch {
		case r >= '0' && r <= '9':
			digits.WriteRune(r)
		case r == '+' && i == 0:
		case r == ' ' || r == '-' || r == '.' || r == '(' || r == ')':
		default:
			return "", false
		}
	}
	d := digits.String()
	if len(d) < 7 || len(d) > 15 { // E.164 caps at 15
		return "", false
	}
	return d, true
}

// Describe renders one candidate for a chooser or a confirmation line.
func Describe(c Candidate) string {
	label := c.Name
	if label == "" {
		label = c.JID
	}
	switch {
	case c.IsGroup:
		return label + " (group)"
	case c.Phone != "":
		return label + " (+" + c.Phone + ")"
	default:
		return label
	}
}
