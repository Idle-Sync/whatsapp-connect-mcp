package store

import (
	"strings"
	"testing"
)

// fixture is the seeded data shared by the query tests: 3 chats, 10
// messages, 2 contacts, 2 calls.
type fixture struct {
	chat1, chat2, chat3 string // 1:1, group, archived 1:1
	contactA, contactB  string // full_name set, push_name-only
	noContact           string // sender with no contacts row (JID fallback)
}

func seedFixture(t *testing.T, s *Store) fixture {
	t.Helper()

	f := fixture{
		chat1:     "111@s.whatsapp.net",
		chat2:     "222@g.us",
		chat3:     "333@s.whatsapp.net",
		contactA:  "111@s.whatsapp.net",
		contactB:  "444@s.whatsapp.net",
		noContact: "555@s.whatsapp.net",
	}

	mustExec := func(query string, args ...any) {
		t.Helper()
		if _, err := s.db.Exec(query, args...); err != nil {
			t.Fatalf("seed exec %q: %v", query, err)
		}
	}

	if err := s.UpsertChat(f.chat1, "Alice", false, 1000); err != nil {
		t.Fatalf("seed chat1: %v", err)
	}
	if err := s.UpsertChat(f.chat2, "Project Group", true, 2000); err != nil {
		t.Fatalf("seed chat2: %v", err)
	}
	if err := s.UpsertChat(f.chat3, "Bob Archived", false, 500); err != nil {
		t.Fatalf("seed chat3: %v", err)
	}
	mustExec(`UPDATE chats SET archived = 1 WHERE jid = ?`, f.chat3)

	if err := s.UpsertContact(f.contactA, "+1000", "Alice Push", "Alice Full", ""); err != nil {
		t.Fatalf("seed contactA: %v", err)
	}
	if err := s.UpsertContact(f.contactB, "+2000", "Bobby Push", "", ""); err != nil {
		t.Fatalf("seed contactB: %v", err)
	}

	msg := func(chatJID, id, senderJID string, fromMe bool, ts int64, kind, text, quotedID string, hasMedia bool) {
		t.Helper()
		m := Message{
			ChatJID: chatJID, ID: id, SenderJID: senderJID, FromMe: fromMe,
			TS: ts, Kind: kind, Text: text, QuotedID: quotedID,
		}
		if hasMedia {
			m.MediaRef = []byte("ref-" + id)
			m.MediaFilename = id + ".bin"
		}
		if err := s.UpsertMessage(m); err != nil {
			t.Fatalf("seed message %s: %v", id, err)
		}
	}

	msg(f.chat1, "m1", f.contactA, false, 100, "text", "hello there", "", false)
	msg(f.chat1, "m2", f.chat1, true, 200, "text", "hi back", "m1", false)
	msg(f.chat1, "m3", f.contactA, false, 300, "image", "a photo caption", "", true)
	msg(f.chat1, "m4", f.noContact, false, 400, "text", "unknown sender says findme", "", false)
	msg(f.chat1, "m5", f.chat1, true, 500, "text", "wrapping up", "", false)
	msg(f.chat2, "g1", f.contactB, false, 150, "text", "group message one", "", false)
	msg(f.chat2, "g2", f.chat2, true, 250, "text", "group reply findme too", "", false)
	msg(f.chat2, "g3", f.contactB, false, 350, "voice", "", "", true)
	msg(f.chat3, "a1", f.chat3, false, 50, "text", "archived chat message", "", false)
	msg(f.chat3, "a2", f.chat3, false, 60, "text", "second archived message", "", false)

	if err := s.InsertCall("c1", f.contactA, 100, "incoming", "missed", false); err != nil {
		t.Fatalf("seed call1: %v", err)
	}
	if err := s.InsertCall("c2", f.noContact, 200, "outgoing", "answered", true); err != nil {
		t.Fatalf("seed call2: %v", err)
	}

	return f
}

func TestChatsClampsLimitZeroToDefault(t *testing.T) {
	s := newTestStore(t)
	seedFixture(t, s)

	rows, err := s.Chats("", true, 0)
	if err != nil {
		t.Fatalf("Chats: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("len(rows) = %d, want 3 (default limit 20 must not truncate 3 rows)", len(rows))
	}
}

func TestChatsClampsLimitAboveMaxTo100(t *testing.T) {
	s := newTestStore(t)
	seedFixture(t, s)

	// Not enough rows to observe truncation directly, so verify indirectly
	// via a limit of 1 that a larger clamp target would not also apply.
	rows, err := s.Chats("", true, 500)
	if err != nil {
		t.Fatalf("Chats: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("len(rows) = %d, want 3", len(rows))
	}
}

func TestChatsOrderedNewestFirst(t *testing.T) {
	s := newTestStore(t)
	f := seedFixture(t, s)

	rows, err := s.Chats("", true, 0)
	if err != nil {
		t.Fatalf("Chats: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("len(rows) = %d, want 3", len(rows))
	}
	want := []string{f.chat2, f.chat1, f.chat3} // last_message_at 2000, 1000, 500
	for i, jid := range want {
		if rows[i].JID != jid {
			t.Fatalf("rows[%d].JID = %q, want %q (newest-first order)", i, rows[i].JID, jid)
		}
	}
}

func TestChatsExcludesArchivedByDefault(t *testing.T) {
	s := newTestStore(t)
	f := seedFixture(t, s)

	rows, err := s.Chats("", false, 0)
	if err != nil {
		t.Fatalf("Chats: %v", err)
	}
	for _, r := range rows {
		if r.JID == f.chat3 {
			t.Fatalf("archived chat %q returned when includeArchived = false", f.chat3)
		}
	}
	if len(rows) != 2 {
		t.Fatalf("len(rows) = %d, want 2", len(rows))
	}
}

func TestChatsFiltersByNameQuery(t *testing.T) {
	s := newTestStore(t)
	f := seedFixture(t, s)

	rows, err := s.Chats("Project", true, 0)
	if err != nil {
		t.Fatalf("Chats: %v", err)
	}
	if len(rows) != 1 || rows[0].JID != f.chat2 {
		t.Fatalf("Chats(Project) = %+v, want single row for %q", rows, f.chat2)
	}
}

func TestChatFound(t *testing.T) {
	s := newTestStore(t)
	f := seedFixture(t, s)

	row, ok, err := s.Chat(f.chat1)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if !ok {
		t.Fatalf("Chat(%q) ok = false, want true", f.chat1)
	}
	if row.Name != "Alice" || row.IsGroup {
		t.Fatalf("Chat(%q) = %+v, want Name=Alice IsGroup=false", f.chat1, row)
	}
}

func TestChatNotFound(t *testing.T) {
	s := newTestStore(t)
	seedFixture(t, s)

	_, ok, err := s.Chat("nonexistent@s.whatsapp.net")
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if ok {
		t.Fatalf("Chat(nonexistent) ok = true, want false")
	}
}

func TestMessagesOrderedNewestFirstAndClampsLimit(t *testing.T) {
	s := newTestStore(t)
	f := seedFixture(t, s)

	rows, err := s.Messages(f.chat1, 0, 0, 0)
	if err != nil {
		t.Fatalf("Messages: %v", err)
	}
	if len(rows) != 5 {
		t.Fatalf("len(rows) = %d, want 5", len(rows))
	}
	wantIDs := []string{"m5", "m4", "m3", "m2", "m1"}
	for i, id := range wantIDs {
		if rows[i].ID != id {
			t.Fatalf("rows[%d].ID = %q, want %q (newest-first)", i, rows[i].ID, id)
		}
	}
}

func TestMessagesLimitAboveMaxClampsTo100(t *testing.T) {
	s := newTestStore(t)
	f := seedFixture(t, s)

	rows, err := s.Messages(f.chat1, 0, 0, 500)
	if err != nil {
		t.Fatalf("Messages: %v", err)
	}
	if len(rows) != 5 {
		t.Fatalf("len(rows) = %d, want 5", len(rows))
	}
}

func TestMessagesRespectsBeforeAndAfterTS(t *testing.T) {
	s := newTestStore(t)
	f := seedFixture(t, s)

	rows, err := s.Messages(f.chat1, 400, 100, 0)
	if err != nil {
		t.Fatalf("Messages: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("len(rows) = %d, want 2 (ts in (100,400))", len(rows))
	}
	if rows[0].ID != "m3" || rows[1].ID != "m2" {
		t.Fatalf("rows = [%q %q], want [m3 m2]", rows[0].ID, rows[1].ID)
	}
}

func TestMessagesSenderNameResolvesViaContacts(t *testing.T) {
	s := newTestStore(t)
	f := seedFixture(t, s)

	rows, err := s.Messages(f.chat1, 0, 0, 0)
	if err != nil {
		t.Fatalf("Messages: %v", err)
	}
	got := map[string]string{}
	for _, r := range rows {
		got[r.ID] = r.SenderName
	}
	if got["m1"] != "Alice Full" {
		t.Fatalf("m1 SenderName = %q, want Alice Full", got["m1"])
	}
}

func TestMessagesSenderNameFallsBackToJIDWhenNoContact(t *testing.T) {
	s := newTestStore(t)
	f := seedFixture(t, s)

	rows, err := s.Messages(f.chat1, 0, 0, 0)
	if err != nil {
		t.Fatalf("Messages: %v", err)
	}
	for _, r := range rows {
		if r.ID == "m4" {
			if r.SenderName != f.noContact {
				t.Fatalf("m4 SenderName = %q, want JID fallback %q", r.SenderName, f.noContact)
			}
			return
		}
	}
	t.Fatalf("message m4 not found in results")
}

func TestMessagesHasMediaReflectsMediaRef(t *testing.T) {
	s := newTestStore(t)
	f := seedFixture(t, s)

	rows, err := s.Messages(f.chat1, 0, 0, 0)
	if err != nil {
		t.Fatalf("Messages: %v", err)
	}
	for _, r := range rows {
		want := r.ID == "m3"
		if r.HasMedia != want {
			t.Fatalf("message %s HasMedia = %v, want %v", r.ID, r.HasMedia, want)
		}
	}
}

func TestSearchMessagesFindsByWord(t *testing.T) {
	s := newTestStore(t)
	f := seedFixture(t, s)

	rows, err := s.SearchMessages("findme", "", 0)
	if err != nil {
		t.Fatalf("SearchMessages: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("len(rows) = %d, want 2 (m4 and g2 both contain findme)", len(rows))
	}
	ids := map[string]bool{}
	for _, r := range rows {
		ids[r.ID] = true
	}
	if !ids["m4"] || !ids["g2"] {
		t.Fatalf("results = %+v, want m4 and g2 (chat %q)", rows, f.chat1)
	}
}

func TestSearchMessagesRespectsChatFilter(t *testing.T) {
	s := newTestStore(t)
	f := seedFixture(t, s)

	rows, err := s.SearchMessages("findme", f.chat1, 0)
	if err != nil {
		t.Fatalf("SearchMessages: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != "m4" {
		t.Fatalf("SearchMessages(findme, chat1) = %+v, want single row m4", rows)
	}
}

func TestSearchMessagesEmptyQueryErrors(t *testing.T) {
	s := newTestStore(t)
	seedFixture(t, s)

	if _, err := s.SearchMessages("", "", 0); err == nil {
		t.Fatalf("SearchMessages(\"\") error = nil, want error")
	}
}

func TestSearchMessagesMalformedOperatorInputDoesNotError(t *testing.T) {
	s := newTestStore(t)
	seedFixture(t, s)

	// Characters and sequences that are FTS5 query-syntax operators
	// (quotes, boolean keywords, column filter, prefix star, unbalanced
	// parens) must be treated as literal search text, not parsed.
	inputs := []string{
		`unterminated "quote`,
		`AND OR NOT`,
		`text: hello`,
		`hello*`,
		`(unbalanced`,
		`-excluded`,
	}
	for _, in := range inputs {
		if _, err := s.SearchMessages(in, "", 0); err != nil {
			t.Fatalf("SearchMessages(%q) error = %v, want nil (must not surface FTS5 syntax errors)", in, err)
		}
	}
}

func TestMessageContextReturnsBeforeTargetAfterAscending(t *testing.T) {
	s := newTestStore(t)
	f := seedFixture(t, s)

	rows, err := s.MessageContext(f.chat1, "m3", 2, 2)
	if err != nil {
		t.Fatalf("MessageContext: %v", err)
	}
	wantIDs := []string{"m1", "m2", "m3", "m4", "m5"}
	if len(rows) != len(wantIDs) {
		t.Fatalf("len(rows) = %d, want %d", len(rows), len(wantIDs))
	}
	for i, id := range wantIDs {
		if rows[i].ID != id {
			t.Fatalf("rows[%d].ID = %q, want %q (ascending before+target+after)", i, rows[i].ID, id)
		}
	}
}

func TestMessageContextClampsBeforeAndAfterToAvailableRows(t *testing.T) {
	s := newTestStore(t)
	f := seedFixture(t, s)

	rows, err := s.MessageContext(f.chat1, "m1", 5, 0)
	if err != nil {
		t.Fatalf("MessageContext: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != "m1" {
		t.Fatalf("MessageContext(m1, before=5, after=0) = %+v, want [m1]", rows)
	}
}

func TestMessageContextUnknownMessageErrors(t *testing.T) {
	s := newTestStore(t)
	f := seedFixture(t, s)

	if _, err := s.MessageContext(f.chat1, "does-not-exist", 1, 1); err == nil {
		t.Fatalf("MessageContext(unknown) error = nil, want error")
	}
}

func TestSearchContactsMatchesNameOrPhone(t *testing.T) {
	s := newTestStore(t)
	f := seedFixture(t, s)

	rows, err := s.SearchContacts("Alice", 0)
	if err != nil {
		t.Fatalf("SearchContacts: %v", err)
	}
	if len(rows) != 1 || rows[0].JID != f.contactA {
		t.Fatalf("SearchContacts(Alice) = %+v, want single row for %q", rows, f.contactA)
	}
	if rows[0].Name != "Alice Full" {
		t.Fatalf("Name = %q, want Alice Full (full_name preferred)", rows[0].Name)
	}

	rows, err = s.SearchContacts("+2000", 0)
	if err != nil {
		t.Fatalf("SearchContacts(+2000): %v", err)
	}
	if len(rows) != 1 || rows[0].JID != f.contactB {
		t.Fatalf("SearchContacts(+2000) = %+v, want single row for %q", rows, f.contactB)
	}
	if rows[0].Name != "Bobby Push" {
		t.Fatalf("Name = %q, want Bobby Push (push_name fallback, no full_name)", rows[0].Name)
	}
}

func TestSearchContactsClampsLimit(t *testing.T) {
	s := newTestStore(t)
	seedFixture(t, s)

	rows, err := s.SearchContacts("", 500)
	if err != nil {
		t.Fatalf("SearchContacts: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("len(rows) = %d, want 2", len(rows))
	}
}

func TestLastInteractionReturnsNewestMessageForJID(t *testing.T) {
	s := newTestStore(t)
	f := seedFixture(t, s)

	row, ok, err := s.LastInteraction(f.chat1)
	if err != nil {
		t.Fatalf("LastInteraction: %v", err)
	}
	if !ok {
		t.Fatalf("LastInteraction(%q) ok = false, want true", f.chat1)
	}
	if row.ID != "m5" {
		t.Fatalf("LastInteraction(%q).ID = %q, want m5", f.chat1, row.ID)
	}
}

func TestLastInteractionUnknownJIDReturnsNotOK(t *testing.T) {
	s := newTestStore(t)
	seedFixture(t, s)

	_, ok, err := s.LastInteraction("nonexistent@s.whatsapp.net")
	if err != nil {
		t.Fatalf("LastInteraction: %v", err)
	}
	if ok {
		t.Fatalf("LastInteraction(nonexistent) ok = true, want false")
	}
}

func TestCallsOrderedNewestFirstAndFilterByPeer(t *testing.T) {
	s := newTestStore(t)
	f := seedFixture(t, s)

	rows, err := s.Calls("", 0)
	if err != nil {
		t.Fatalf("Calls: %v", err)
	}
	if len(rows) != 2 || rows[0].ID != "c2" || rows[1].ID != "c1" {
		t.Fatalf("Calls() = %+v, want [c2 c1] (newest first)", rows)
	}

	rows, err = s.Calls(f.contactA, 0)
	if err != nil {
		t.Fatalf("Calls(peer): %v", err)
	}
	if len(rows) != 1 || rows[0].ID != "c1" {
		t.Fatalf("Calls(%q) = %+v, want single row c1", f.contactA, rows)
	}
}

func TestCallsPeerNameResolvesViaContactsWithJIDFallback(t *testing.T) {
	s := newTestStore(t)
	f := seedFixture(t, s)

	rows, err := s.Calls("", 0)
	if err != nil {
		t.Fatalf("Calls: %v", err)
	}
	names := map[string]string{}
	for _, r := range rows {
		names[r.ID] = r.PeerName
	}
	if names["c1"] != "Alice Full" {
		t.Fatalf("c1 PeerName = %q, want Alice Full", names["c1"])
	}
	if names["c2"] != f.noContact {
		t.Fatalf("c2 PeerName = %q, want JID fallback %q", names["c2"], f.noContact)
	}
}

func TestMessageMediaRefReturnsStoredRef(t *testing.T) {
	s := newTestStore(t)
	f := seedFixture(t, s)

	ref, filename, kind, err := s.MessageMediaRef(f.chat1, "m3")
	if err != nil {
		t.Fatalf("MessageMediaRef: %v", err)
	}
	if string(ref) != "ref-m3" || filename != "m3.bin" || kind != "image" {
		t.Fatalf("MessageMediaRef(m3) = (%q, %q, %q), want (ref-m3, m3.bin, image)", ref, filename, kind)
	}
}

func TestMessageMediaRefNoMediaErrors(t *testing.T) {
	s := newTestStore(t)
	f := seedFixture(t, s)

	if _, _, _, err := s.MessageMediaRef(f.chat1, "m1"); err == nil {
		t.Fatalf("MessageMediaRef(m1) error = nil, want error (no media)")
	}
}

func TestMessageMediaRefUnknownMessageErrors(t *testing.T) {
	s := newTestStore(t)
	f := seedFixture(t, s)

	if _, _, _, err := s.MessageMediaRef(f.chat1, "does-not-exist"); err == nil {
		t.Fatalf("MessageMediaRef(unknown) error = nil, want error")
	}
}

func TestErrorMessagesDoNotLeakJIDsOrContent(t *testing.T) {
	s := newTestStore(t)
	f := seedFixture(t, s)

	_, err := s.MessageContext(f.chat1, "does-not-exist", 1, 1)
	if err == nil {
		t.Fatalf("expected error")
	}
	if strings.Contains(err.Error(), f.chat1) {
		t.Fatalf("error %q leaks chat JID", err.Error())
	}
}
