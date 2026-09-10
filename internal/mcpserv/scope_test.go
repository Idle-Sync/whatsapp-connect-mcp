package mcpserv

import (
	"errors"
	"testing"

	"github.com/idle-sync/whatsapp-connect-mcp/internal/store"
)

// fakeScope is a Scope backed by a fixed allowlist.
type fakeScope struct {
	active bool
	allow  []string
}

func (f fakeScope) Active() bool { return f.active }
func (f fakeScope) List() []string {
	return f.allow
}
func (f fakeScope) Readable(jid string) bool {
	if !f.active {
		return true
	}
	for _, a := range f.allow {
		if a == jid {
			return true
		}
	}
	return false
}

const (
	allowed = "111@s.whatsapp.net"
	denied  = "999@s.whatsapp.net"
)

func scoped(t *testing.T, st Store, allow ...string) Store {
	t.Helper()
	return newScopedStore(st, fakeScope{active: true, allow: allow})
}

// Every chat-addressed read refuses a chat outside the allowlist, and says
// why rather than pretending the chat does not exist.
func TestScopeRefusesEveryChatAddressedRead(t *testing.T) {
	s := scoped(t, &fakeStore{}, allowed)

	checks := map[string]func() error{
		"Chat":            func() error { _, _, err := s.Chat(denied); return err },
		"Messages":        func() error { _, err := s.Messages(denied, 0, 0, 10); return err },
		"SearchMessages":  func() error { _, err := s.SearchMessages("hi", denied, 10); return err },
		"MessageContext":  func() error { _, err := s.MessageContext(denied, "id", 1, 1); return err },
		"LastInteraction": func() error { _, _, err := s.LastInteraction(denied); return err },
		"OldestMessage":   func() error { _, _, err := s.OldestMessage(denied); return err },
		"CountMessagesOlderThan": func() error {
			_, err := s.CountMessagesOlderThan(denied, 0)
			return err
		},
		"MessagesAfterRowID": func() error { _, _, err := s.MessagesAfterRowID(denied, 0, false, 10); return err },
		"TailRowID":          func() error { _, err := s.TailRowID(denied, false, 1); return err },
		"Calls":              func() error { _, err := s.Calls(denied, 0, 0, 10); return err },
		"MessageMediaRef":    func() error { _, _, _, err := s.MessageMediaRef(denied, "id"); return err },
		"MediaMessageIDs":    func() error { _, err := s.MediaMessageIDs(denied, 0, 0, "", 10); return err },
	}
	for name, call := range checks {
		if err := call(); !errors.Is(err, ErrOutOfScope) {
			t.Errorf("%s(denied chat) error = %v, want ErrOutOfScope", name, err)
		}
	}
}

// The same reads pass straight through for an allowed chat.
func TestScopeAllowsListedChat(t *testing.T) {
	fs := &fakeStore{chatOK: true, chatRet: store.ChatRow{JID: allowed, Name: "Allowed"}}
	s := scoped(t, fs, allowed)

	got, ok, err := s.Chat(allowed)
	if err != nil || !ok || got.JID != allowed {
		t.Fatalf("Chat(allowed) = %+v, %v, %v", got, ok, err)
	}
	if _, err := s.Messages(allowed, 0, 0, 10); err != nil {
		t.Fatalf("Messages(allowed) error = %v", err)
	}
}

// A sweep across every chat is filtered rather than refused: the caller
// asked a question about all chats, and the answer is the allowed subset.
func TestScopeFiltersSweeps(t *testing.T) {
	fs := &fakeStore{
		searchMessagesRet: []store.MessageRow{
			{ChatJID: allowed, ID: "a"},
			{ChatJID: denied, ID: "b"},
			{ChatJID: allowed, ID: "c"},
		},
		searchContactsRet: []store.ContactRow{{JID: allowed}, {JID: denied}},
		callsRet:          []store.CallRow{{PeerJID: allowed}, {PeerJID: denied}},
	}
	s := scoped(t, fs, allowed)

	msgs, err := s.SearchMessages("x", "", 10)
	if err != nil {
		t.Fatalf("SearchMessages error = %v", err)
	}
	if len(msgs) != 2 {
		t.Errorf("SearchMessages returned %d rows, want 2", len(msgs))
	}
	for _, m := range msgs {
		if m.ChatJID != allowed {
			t.Errorf("leaked message from %s", m.ChatJID)
		}
	}

	contacts, err := s.SearchContacts("x", 10)
	if err != nil {
		t.Fatalf("SearchContacts error = %v", err)
	}
	if len(contacts) != 1 || contacts[0].JID != allowed {
		t.Errorf("SearchContacts = %+v, want only the allowed contact", contacts)
	}

	calls, err := s.Calls("", 0, 0, 10)
	if err != nil {
		t.Fatalf("Calls error = %v", err)
	}
	if len(calls) != 1 || calls[0].PeerJID != allowed {
		t.Errorf("Calls = %+v, want only the allowed peer", calls)
	}
}

// Polling must keep advancing past messages it cannot see; a cursor pinned
// to the last VISIBLE row would re-scan the same hidden ones forever.
func TestScopePollingCursorAdvancesPastHiddenRows(t *testing.T) {
	fs := &fakeStore{
		afterRowsRet:  []store.MessageRow{{ChatJID: denied, ID: "b"}},
		afterRowsNext: 42,
	}
	s := scoped(t, fs, allowed)

	rows, cursor, err := s.MessagesAfterRowID("", 0, false, 10)
	if err != nil {
		t.Fatalf("MessagesAfterRowID error = %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("returned %d rows, want 0", len(rows))
	}
	if cursor != 42 {
		t.Errorf("cursor = %d, want the underlying 42", cursor)
	}
}

// Chats resolves the allowlist directly, so an allowed chat that is not in
// the recent page still shows up.
func TestScopeChatsListsAllowlistNotRecentPage(t *testing.T) {
	fs := &fakeStore{
		chatsRet: []store.ChatRow{{JID: denied, Name: "Loud"}}, // what an unscoped list would return
		chatOK:   true,
		chatRet:  store.ChatRow{JID: allowed, Name: "Quiet", LastMessageAt: 5},
	}
	s := scoped(t, fs, allowed)

	rows, err := s.Chats("", false, 10)
	if err != nil {
		t.Fatalf("Chats error = %v", err)
	}
	if len(rows) != 1 || rows[0].JID != allowed {
		t.Fatalf("Chats = %+v, want just the allowlisted chat", rows)
	}
}

// An allowlist that is switched on and empty means nothing is readable —
// the state setup leaves behind when the user picks chats later. It must
// not fall back to "everything".
func TestScopeActiveButEmptyAllowsNothing(t *testing.T) {
	fs := &fakeStore{chatsRet: []store.ChatRow{{JID: denied}}}
	s := newScopedStore(fs, fakeScope{active: true})

	rows, err := s.Chats("", false, 10)
	if err != nil {
		t.Fatalf("Chats error = %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("Chats = %+v, want none", rows)
	}
	if _, _, err := s.Chat(allowed); !errors.Is(err, ErrOutOfScope) {
		t.Fatalf("Chat error = %v, want ErrOutOfScope", err)
	}
}

// With no scope configured the store is handed back unwrapped, so the
// default path costs nothing and behaves exactly as before.
func TestScopeInactivePassesThrough(t *testing.T) {
	fs := &fakeStore{chatsRet: []store.ChatRow{{JID: denied}}}

	if got := newScopedStore(fs, nil); got != Store(fs) {
		t.Error("nil scope should return the store unwrapped")
	}

	s := newScopedStore(fs, fakeScope{active: false})
	rows, err := s.Chats("", false, 10)
	if err != nil {
		t.Fatalf("Chats error = %v", err)
	}
	if len(rows) != 1 || rows[0].JID != denied {
		t.Errorf("Chats = %+v, want the unfiltered list", rows)
	}
	if _, _, err := s.Chat(denied); err != nil {
		t.Errorf("Chat error = %v, want nil", err)
	}
}
