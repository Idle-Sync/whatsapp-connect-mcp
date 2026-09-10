package target

import (
	"errors"
	"strings"
	"testing"

	"github.com/idle-sync/whatsapp-connect-mcp/internal/store"
)

type fakeLookup struct {
	contacts []store.ContactRow
	chats    []store.ChatRow
	err      error
}

func (f fakeLookup) SearchContacts(q string, _ int) ([]store.ContactRow, error) {
	if f.err != nil {
		return nil, f.err
	}
	var out []store.ContactRow
	for _, c := range f.contacts {
		if strings.Contains(strings.ToLower(c.Name), strings.ToLower(q)) {
			out = append(out, c)
		}
	}
	return out, nil
}

func (f fakeLookup) Chats(q string, _ bool, _ int) ([]store.ChatRow, error) {
	if f.err != nil {
		return nil, f.err
	}
	var out []store.ChatRow
	for _, c := range f.chats {
		if strings.Contains(strings.ToLower(c.Name), strings.ToLower(q)) {
			out = append(out, c)
		}
	}
	return out, nil
}

// A phone number resolves without consulting the store at all, so a chat
// can be named before its first message has ever arrived.
func TestResolvePhoneNumberFormats(t *testing.T) {
	empty := fakeLookup{}
	for _, in := range []string{
		"15551234567",
		"+15551234567",
		"+1 555 123 4567",
		"+1 (555) 123-4567",
		"1-555-123-4567",
	} {
		got, err := Resolve(empty, in)
		if err != nil {
			t.Errorf("Resolve(%q) error = %v", in, err)
			continue
		}
		if got != "15551234567@s.whatsapp.net" {
			t.Errorf("Resolve(%q) = %q", in, got)
		}
	}
}

// Too short to be a subscriber number, so it is a name, not a number.
func TestResolveShortDigitsAreNotAPhoneNumber(t *testing.T) {
	if _, ok := asPhone("007"); ok {
		t.Error("007 read as a phone number")
	}
	if _, ok := asPhone("123456789012345678"); ok {
		t.Error("an over-long string read as a phone number")
	}
}

func TestResolveJIDPassesThrough(t *testing.T) {
	for _, in := range []string{
		"15551234567@s.whatsapp.net",
		"120363000000000000@g.us",
		"12345@lid",
	} {
		got, err := Resolve(fakeLookup{}, in)
		if err != nil || got != in {
			t.Errorf("Resolve(%q) = %q, %v", in, got, err)
		}
	}
	if IsJID("not-a-jid") || IsJID("a@example.com") || IsJID("@g.us") {
		t.Error("IsJID accepted something that is not a JID")
	}
}

func TestResolveUniqueName(t *testing.T) {
	l := fakeLookup{contacts: []store.ContactRow{
		{JID: "1@s.whatsapp.net", Name: "Ashmi", Phone: "15551234567"},
		{JID: "2@s.whatsapp.net", Name: "Bhaskar"},
	}}
	got, err := Resolve(l, "ashmi")
	if err != nil {
		t.Fatalf("Resolve error = %v", err)
	}
	if got != "1@s.whatsapp.net" {
		t.Errorf("Resolve = %q", got)
	}
}

// Groups have no contact row, so a group name has to come back from the
// chat side of the search.
func TestResolveGroupName(t *testing.T) {
	l := fakeLookup{chats: []store.ChatRow{
		{JID: "120363@g.us", Name: "Dev Team", IsGroup: true},
	}}
	got, err := Resolve(l, "dev team")
	if err != nil {
		t.Fatalf("Resolve error = %v", err)
	}
	if got != "120363@g.us" {
		t.Errorf("Resolve = %q", got)
	}
}

// Guessing between two people named Ashmi is how you trust the wrong one,
// so ambiguity is reported with every candidate instead.
func TestResolveAmbiguousNameReportsCandidates(t *testing.T) {
	l := fakeLookup{contacts: []store.ContactRow{
		{JID: "1@s.whatsapp.net", Name: "Ashmi B", Phone: "15551234567"},
		{JID: "2@s.whatsapp.net", Name: "Ashmi G", Phone: "15559876543"},
	}}
	_, err := Resolve(l, "ashmi")

	var amb *AmbiguousError
	if !errors.As(err, &amb) {
		t.Fatalf("Resolve error = %v, want AmbiguousError", err)
	}
	if len(amb.Candidates) != 2 {
		t.Fatalf("candidates = %+v, want 2", amb.Candidates)
	}
	if !strings.Contains(Describe(amb.Candidates[0]), "+15551234567") {
		t.Errorf("Describe = %q, want the phone number in it", Describe(amb.Candidates[0]))
	}
}

func TestResolveNoMatch(t *testing.T) {
	_, err := Resolve(fakeLookup{}, "nobody")
	if !errors.Is(err, ErrNoMatch) {
		t.Fatalf("Resolve error = %v, want ErrNoMatch", err)
	}
}

// The same JID reachable as both a contact and a chat is one candidate.
func TestSearchDeduplicates(t *testing.T) {
	l := fakeLookup{
		contacts: []store.ContactRow{{JID: "1@s.whatsapp.net", Name: "Ashmi"}},
		chats:    []store.ChatRow{{JID: "1@s.whatsapp.net", Name: "Ashmi"}},
	}
	got, err := Search(l, "ashmi")
	if err != nil {
		t.Fatalf("Search error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Search = %+v, want 1 candidate", got)
	}
}

func TestResolveEmptyAndErrors(t *testing.T) {
	if _, err := Resolve(fakeLookup{}, "   "); err == nil {
		t.Error("empty input accepted")
	}
	if _, err := Resolve(fakeLookup{err: errors.New("db down")}, "ashmi"); err == nil {
		t.Error("a store error was swallowed")
	}
}
