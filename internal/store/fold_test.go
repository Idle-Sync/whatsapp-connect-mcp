package store

import (
	"testing"
)

// resolver maps bare LIDs to phone JIDs the way the bridge's session-store
// lookup would.
func resolver(m map[string]string) func(string) string {
	return func(lid string) string { return m[lid] }
}

func mustExec(t *testing.T, s *Store, query string, args ...any) {
	t.Helper()
	if _, err := s.db.Exec(query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

func count(t *testing.T, s *Store, query string, args ...any) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

// TestFoldLIDsMergesChatIntoPhoneChat: a person seen as both a LID chat
// and a phone-number chat ends up as one chat keyed on the phone number,
// with every message under it, a message stored both ways kept once, the
// name inherited from whichever half had it, and the pairing recorded.
func TestFoldLIDsMergesChatIntoPhoneChat(t *testing.T) {
	s := newTestStore(t)
	const lid, pn = "249791271452696@lid", "917980466253@s.whatsapp.net"

	mustExec(t, s, `INSERT INTO chats (jid, name, last_message_at) VALUES (?, 'Soumyadeep', 200)`, lid)
	mustExec(t, s, `INSERT INTO chats (jid, name, last_message_at) VALUES (?, '', 100)`, pn)
	for _, m := range []struct {
		chat, id, sender, text string
		ts                     int64
	}{
		{lid, "A", lid, "hi via lid", 200},
		{lid, "SHARED", lid, "seen both ways", 150},
		{pn, "SHARED", pn, "seen both ways", 150},
		{pn, "B", pn, "hi via phone", 100},
	} {
		if err := s.UpsertMessage(Message{ChatJID: m.chat, ID: m.id, SenderJID: m.sender, TS: m.ts, Kind: "text", Text: m.text}); err != nil {
			t.Fatalf("UpsertMessage: %v", err)
		}
	}

	stats, err := s.FoldLIDs(resolver(map[string]string{lid: pn}))
	if err != nil {
		t.Fatalf("FoldLIDs: %v", err)
	}
	if stats.Chats != 1 {
		t.Fatalf("stats = %+v, want 1 chat folded", stats)
	}
	if n := count(t, s, `SELECT COUNT(*) FROM chats`); n != 1 {
		t.Fatalf("%d chats after fold, want 1", n)
	}
	chat, ok, err := s.Chat(pn)
	if err != nil || !ok {
		t.Fatalf("Chat(%s) = %v, %v", pn, ok, err)
	}
	if chat.Name != "Soumyadeep" || chat.LastMessageAt != 200 {
		t.Fatalf("phone chat = %+v, want the LID chat's name and the later timestamp", chat)
	}
	if n := count(t, s, `SELECT COUNT(*) FROM messages WHERE chat_jid = ?`, pn); n != 3 {
		t.Fatalf("%d messages under the phone chat, want 3 (A, SHARED once, B)", n)
	}
	if n := count(t, s, `SELECT COUNT(*) FROM messages WHERE chat_jid = ? OR sender_jid = ?`, lid, lid); n != 0 {
		t.Fatalf("%d messages still reference the LID, want none", n)
	}
	if n := count(t, s, `SELECT COUNT(*) FROM messages_fts WHERE messages_fts MATCH 'lid'`); n != 1 {
		t.Fatalf("full-text index has %d hits for the moved message, want 1", n)
	}
	if n := count(t, s, `SELECT COUNT(*) FROM lid_map WHERE lid = ? AND pn = ?`, lid, pn); n != 1 {
		t.Fatal("the pairing used must be recorded in lid_map")
	}

	// Idempotent: nothing left to move.
	again, err := s.FoldLIDs(resolver(map[string]string{lid: pn}))
	if err != nil || again.Total() != 0 {
		t.Fatalf("second fold = %+v, %v; want nothing touched", again, err)
	}
}

// TestFoldLIDsRewritesSendersContactsAndCalls: group messages from a LID
// sender (device-qualified or not) get the phone sender, the LID contact's
// names fill the phone contact's gaps and the LID row goes, and call peers
// follow — while a LID nobody can resolve stays exactly as it was.
func TestFoldLIDsRewritesSendersContactsAndCalls(t *testing.T) {
	s := newTestStore(t)
	const group = "222@g.us"
	const lid, lidDev, pn = "99566015803422@lid", "99566015803422:6@lid", "444@s.whatsapp.net"
	const stranger = "555@lid"

	mustExec(t, s, `INSERT INTO chats (jid, name, is_group, last_message_at) VALUES (?, 'Team', 1, 300)`, group)
	for i, sender := range []string{lid, lidDev, stranger} {
		if err := s.UpsertMessage(Message{ChatJID: group, ID: string(rune('A' + i)), SenderJID: sender, TS: int64(i), Kind: "text", Text: "x"}); err != nil {
			t.Fatalf("UpsertMessage: %v", err)
		}
	}
	if err := s.UpsertContact(lidDev, "", "Bobby", "", ""); err != nil {
		t.Fatalf("UpsertContact: %v", err)
	}
	if err := s.UpsertContact(pn, "444", "", "Robert Example", ""); err != nil {
		t.Fatalf("UpsertContact: %v", err)
	}
	if err := s.InsertCall("call1", lid, 10, "incoming", "missed", false); err != nil {
		t.Fatalf("InsertCall: %v", err)
	}

	stats, err := s.FoldLIDs(resolver(map[string]string{lid: pn}))
	if err != nil {
		t.Fatalf("FoldLIDs: %v", err)
	}
	if stats.Messages != 2 || stats.Contacts != 1 || stats.Calls != 1 || stats.Chats != 0 {
		t.Fatalf("stats = %+v, want 2 messages, 1 contact, 1 call, 0 chats", stats)
	}
	if n := count(t, s, `SELECT COUNT(*) FROM messages WHERE sender_jid = ?`, pn); n != 2 {
		t.Fatalf("%d messages carry the phone sender, want 2", n)
	}
	if n := count(t, s, `SELECT COUNT(*) FROM messages WHERE sender_jid = ?`, stranger); n != 1 {
		t.Fatal("an unresolvable LID sender must be left alone")
	}
	var push, full string
	if err := s.db.QueryRow(`SELECT push_name, full_name FROM contacts WHERE jid = ?`, pn).Scan(&push, &full); err != nil {
		t.Fatalf("phone contact: %v", err)
	}
	if push != "Bobby" || full != "Robert Example" {
		t.Fatalf("phone contact = %q/%q, want the LID push name filled in and the full name kept", push, full)
	}
	if n := count(t, s, `SELECT COUNT(*) FROM contacts WHERE jid LIKE '%@lid'`); n != 0 {
		t.Fatalf("%d LID contact rows remain, want none", n)
	}
	if n := count(t, s, `SELECT COUNT(*) FROM calls WHERE peer_jid = ?`, pn); n != 1 {
		t.Fatal("the call's peer must be the phone JID now")
	}
	for _, l := range []string{lid, lidDev} {
		if n := count(t, s, `SELECT COUNT(*) FROM lid_map WHERE lid = ? AND pn = ?`, l, pn); n != 1 {
			t.Fatalf("lid_map lacks %s → %s", l, pn)
		}
	}
}

// TestPruneStubMessages: rows with nothing in them (kind other, no text,
// no media) go, and so does a LID direct chat that held only such rows —
// while a chat with real content, and an empty phone-number chat, stay.
func TestPruneStubMessages(t *testing.T) {
	s := newTestStore(t)
	const stubChat, realChat, emptyPN = "190319228375093@lid", "111@s.whatsapp.net", "777@s.whatsapp.net"

	for _, jid := range []string{stubChat, realChat, emptyPN} {
		mustExec(t, s, `INSERT INTO chats (jid, last_message_at) VALUES (?, 1)`, jid)
	}
	stub := func(chat, id string) Message {
		return Message{ChatJID: chat, ID: id, SenderJID: "me", FromMe: true, TS: 1, Kind: "other"}
	}
	for _, m := range []Message{
		stub(stubChat, "S1"), stub(stubChat, "S2"),
		stub(realChat, "S3"),
		{ChatJID: realChat, ID: "T1", SenderJID: realChat, TS: 2, Kind: "text", Text: "hello"},
		{ChatJID: realChat, ID: "O1", SenderJID: realChat, TS: 3, Kind: "other:template", Text: ""},
		{ChatJID: realChat, ID: "M1", SenderJID: realChat, TS: 4, Kind: "other", MediaRef: []byte{1}},
	} {
		if err := s.UpsertMessage(m); err != nil {
			t.Fatalf("UpsertMessage: %v", err)
		}
	}

	msgs, chats, err := s.PruneStubMessages()
	if err != nil {
		t.Fatalf("PruneStubMessages: %v", err)
	}
	if msgs != 3 || chats != 1 {
		t.Fatalf("pruned %d messages, %d chats; want 3 and 1", msgs, chats)
	}
	if n := count(t, s, `SELECT COUNT(*) FROM messages`); n != 3 {
		t.Fatalf("%d messages remain, want the text, the subtyped other and the media one", n)
	}
	if _, ok, _ := s.Chat(stubChat); ok {
		t.Fatal("the stub-only LID chat must be gone")
	}
	for _, jid := range []string{realChat, emptyPN} {
		if _, ok, _ := s.Chat(jid); !ok {
			t.Fatalf("chat %s must survive", jid)
		}
	}
}
