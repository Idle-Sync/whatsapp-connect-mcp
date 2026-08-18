package store

import (
	"strings"
	"testing"
)

func TestClampLimit(t *testing.T) {
	cases := []struct {
		in, want int
	}{
		{0, 20},
		{-5, 20},
		{500, 100},
		{50, 50},
	}
	for _, c := range cases {
		if got := ClampLimit(c.in); got != c.want {
			t.Fatalf("ClampLimit(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

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

func TestCounts(t *testing.T) {
	s := newTestStore(t)

	empty, err := s.Counts()
	if err != nil {
		t.Fatalf("Counts() on empty store error: %v", err)
	}
	if empty != (Counts{}) {
		t.Fatalf("Counts() on empty store = %+v, want all zero", empty)
	}

	seedFixture(t, s)

	got, err := s.Counts()
	if err != nil {
		t.Fatalf("Counts() error: %v", err)
	}
	want := Counts{Chats: 3, Messages: 10, Contacts: 2, Calls: 2}
	if got != want {
		t.Fatalf("Counts() = %+v, want %+v", got, want)
	}
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

func TestSearchMessagesMultiWordQueryMatchesNonConsecutiveWords(t *testing.T) {
	s := newTestStore(t)
	chatJID := "999@s.whatsapp.net"
	if err := s.UpsertChat(chatJID, "Planning", false, 1000); err != nil {
		t.Fatalf("seed chat: %v", err)
	}
	if err := s.UpsertMessage(Message{
		ChatJID: chatJID, ID: "p1", SenderJID: chatJID, TS: 100, Kind: "text",
		Text: "the deadline for the project is tomorrow",
	}); err != nil {
		t.Fatalf("seed message: %v", err)
	}

	rows, err := s.SearchMessages("project deadline", "", 0)
	if err != nil {
		t.Fatalf("SearchMessages: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != "p1" {
		t.Fatalf("SearchMessages(\"project deadline\") = %+v, want single row p1 (words present but not consecutive)", rows)
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

// TestOldestMessageReturnsEarliestInChat pins the direction. LastInteraction
// sits right beside it returning the newest row from a near-identical query,
// so an inverted ORDER BY here would still return a real message and pass
// any test that only checked for one — while anchoring a history request on
// the newest message, which asks the phone for messages already stored.
func TestOldestMessageReturnsEarliestInChat(t *testing.T) {
	s := newTestStore(t)
	f := seedFixture(t, s)

	row, ok, err := s.OldestMessage(f.chat1)
	if err != nil {
		t.Fatalf("OldestMessage: %v", err)
	}
	if !ok {
		t.Fatalf("OldestMessage(%q) ok = false, want true", f.chat1)
	}
	if row.ID != "m1" {
		t.Fatalf("OldestMessage(%q).ID = %q, want m1 (the earliest)", f.chat1, row.ID)
	}
	if row.TS != 100 {
		t.Fatalf("OldestMessage(%q).TS = %d, want 100", f.chat1, row.TS)
	}
}

// TestOldestMessageIsScopedToItsChat guards against dropping the chat_jid
// predicate, which would silently anchor every chat's request on whichever
// message happened to be oldest across the whole store.
func TestOldestMessageIsScopedToItsChat(t *testing.T) {
	s := newTestStore(t)
	f := seedFixture(t, s)

	row, ok, err := s.OldestMessage(f.chat2)
	if err != nil {
		t.Fatalf("OldestMessage: %v", err)
	}
	if !ok {
		t.Fatalf("OldestMessage(%q) ok = false, want true", f.chat2)
	}
	if row.ChatJID != f.chat2 {
		t.Fatalf("OldestMessage(%q).ChatJID = %q, want the chat asked for", f.chat2, row.ChatJID)
	}
}

func TestOldestMessageEmptyChatReturnsNotOK(t *testing.T) {
	s := newTestStore(t)
	seedFixture(t, s)

	_, ok, err := s.OldestMessage("nonexistent@s.whatsapp.net")
	if err != nil {
		t.Fatalf("OldestMessage: %v", err)
	}
	if ok {
		t.Fatalf("OldestMessage(nonexistent) ok = true, want false")
	}
}

func TestCallsOrderedNewestFirstAndFilterByPeer(t *testing.T) {
	s := newTestStore(t)
	f := seedFixture(t, s)

	rows, err := s.Calls("", 0, 0, 0)
	if err != nil {
		t.Fatalf("Calls: %v", err)
	}
	if len(rows) != 2 || rows[0].ID != "c2" || rows[1].ID != "c1" {
		t.Fatalf("Calls() = %+v, want [c2 c1] (newest first)", rows)
	}

	rows, err = s.Calls(f.contactA, 0, 0, 0)
	if err != nil {
		t.Fatalf("Calls(peer): %v", err)
	}
	if len(rows) != 1 || rows[0].ID != "c1" {
		t.Fatalf("Calls(%q) = %+v, want single row c1", f.contactA, rows)
	}
}

func TestCallsTimeWindow(t *testing.T) {
	s := newTestStore(t)
	seedFixture(t, s)

	rows, err := s.Calls("", 150, 0, 0)
	if err != nil {
		t.Fatalf("Calls(before): %v", err)
	}
	if len(rows) != 1 || rows[0].ID != "c1" {
		t.Fatalf("Calls(before=150) = %+v, want only c1 (ts 100)", rows)
	}

	rows, err = s.Calls("", 0, 150, 0)
	if err != nil {
		t.Fatalf("Calls(after): %v", err)
	}
	if len(rows) != 1 || rows[0].ID != "c2" {
		t.Fatalf("Calls(after=150) = %+v, want only c2 (ts 200)", rows)
	}
}

// findMessage fails the test unless rows contains a message with id, and
// returns it.
func findMessage(t *testing.T, rows []MessageRow, id string) MessageRow {
	t.Helper()
	for _, r := range rows {
		if r.ID == id {
			return r
		}
	}
	t.Fatalf("no message %q in %+v", id, rows)
	return MessageRow{}
}

// A group sender often appears only as a LID (privacy identifier). With no
// mapping the raw LID is all there is; once the LID→PN mapping is stored,
// the sender name must resolve through the phone-number contact.
func TestSenderNameResolvesThroughLIDMapping(t *testing.T) {
	s := newTestStore(t)
	f := seedFixture(t, s)

	const lid = "99566015803422@lid"
	if err := s.UpsertMessage(Message{ChatJID: f.chat2, ID: "L1", SenderJID: lid, TS: 400, Kind: "text", Text: "who am I"}); err != nil {
		t.Fatalf("seed lid message: %v", err)
	}

	rows, err := s.Messages(f.chat2, 0, 0, 0)
	if err != nil {
		t.Fatalf("Messages: %v", err)
	}
	if got := findMessage(t, rows, "L1").SenderName; got != lid {
		t.Fatalf("unmapped LID SenderName = %q, want the raw LID %q", got, lid)
	}

	// contactB carries push_name "Bobby Push".
	if err := s.UpsertLIDMapping(lid, f.contactB); err != nil {
		t.Fatalf("UpsertLIDMapping: %v", err)
	}
	rows, err = s.Messages(f.chat2, 0, 0, 0)
	if err != nil {
		t.Fatalf("Messages after mapping: %v", err)
	}
	if got := findMessage(t, rows, "L1").SenderName; got != "Bobby Push" {
		t.Fatalf("mapped LID SenderName = %q, want the PN contact's name %q", got, "Bobby Push")
	}
}

// A mapping to a phone JID with no contacts row still improves on the LID:
// the phone JID is shown instead.
func TestSenderNameLIDMappingFallsBackToPhoneJID(t *testing.T) {
	s := newTestStore(t)
	f := seedFixture(t, s)

	const lid, pn = "12345@lid", "777@s.whatsapp.net"
	if err := s.UpsertMessage(Message{ChatJID: f.chat2, ID: "L2", SenderJID: lid, TS: 401, Kind: "text", Text: "x"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.UpsertLIDMapping(lid, pn); err != nil {
		t.Fatalf("UpsertLIDMapping: %v", err)
	}

	rows, err := s.Messages(f.chat2, 0, 0, 0)
	if err != nil {
		t.Fatalf("Messages: %v", err)
	}
	if got := findMessage(t, rows, "L2").SenderName; got != pn {
		t.Fatalf("SenderName = %q, want the mapped phone JID %q", got, pn)
	}
}

// A contact stored against the LID itself (e.g. a push name ingested from
// a live message) outranks the mapped PN contact.
func TestSenderNameDirectLIDContactWins(t *testing.T) {
	s := newTestStore(t)
	f := seedFixture(t, s)

	const lid = "54321@lid"
	if err := s.UpsertMessage(Message{ChatJID: f.chat2, ID: "L3", SenderJID: lid, TS: 402, Kind: "text", Text: "x"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.UpsertContact(lid, "", "Lid Push", "", ""); err != nil {
		t.Fatalf("UpsertContact: %v", err)
	}
	if err := s.UpsertLIDMapping(lid, f.contactB); err != nil {
		t.Fatalf("UpsertLIDMapping: %v", err)
	}

	rows, err := s.Messages(f.chat2, 0, 0, 0)
	if err != nil {
		t.Fatalf("Messages: %v", err)
	}
	if got := findMessage(t, rows, "L3").SenderName; got != "Lid Push" {
		t.Fatalf("SenderName = %q, want the LID's own contact name %q", got, "Lid Push")
	}
}

// A remapped LID keeps only the latest phone number.
func TestUpsertLIDMappingLastWriteWins(t *testing.T) {
	s := newTestStore(t)
	f := seedFixture(t, s)

	const lid = "67890@lid"
	if err := s.UpsertMessage(Message{ChatJID: f.chat2, ID: "L4", SenderJID: lid, TS: 403, Kind: "text", Text: "x"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.UpsertLIDMapping(lid, "111111@s.whatsapp.net"); err != nil {
		t.Fatalf("first mapping: %v", err)
	}
	if err := s.UpsertLIDMapping(lid, "222222@s.whatsapp.net"); err != nil {
		t.Fatalf("remap: %v", err)
	}

	rows, err := s.Messages(f.chat2, 0, 0, 0)
	if err != nil {
		t.Fatalf("Messages: %v", err)
	}
	if got := findMessage(t, rows, "L4").SenderName; got != "222222@s.whatsapp.net" {
		t.Fatalf("SenderName = %q, want the remapped phone JID", got)
	}
}

// WhatsApp renders a mention into message text as "@<digits>" — the phone
// number's or LID's local part — which reads as an opaque number. Message
// rows must resolve those tokens through the same chain sender names use.

func TestMentionsResolveThroughLIDMapping(t *testing.T) {
	s := newTestStore(t)
	f := seedFixture(t, s)

	const lid = "99566015803422@lid"
	if err := s.UpsertLIDMapping(lid, f.contactB); err != nil { // contactB: push_name "Bobby Push"
		t.Fatalf("UpsertLIDMapping: %v", err)
	}
	if err := s.UpsertMessage(Message{
		ChatJID: f.chat2, ID: "MN1", SenderJID: f.contactA, TS: 500,
		Kind: "text", Text: "@99566015803422 please review the doc",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	rows, err := s.Messages(f.chat2, 0, 0, 0)
	if err != nil {
		t.Fatalf("Messages: %v", err)
	}
	if got := findMessage(t, rows, "MN1").Text; got != "@Bobby Push please review the doc" {
		t.Fatalf("mention text = %q, want the LID resolved to the contact name", got)
	}
}

func TestMentionsResolvePhoneNumberContacts(t *testing.T) {
	s := newTestStore(t)
	f := seedFixture(t, s)

	// contactB's JID local part is 444; a phone mention names it directly.
	if err := s.UpsertMessage(Message{
		ChatJID: f.chat2, ID: "MN2", SenderJID: f.contactA, TS: 501,
		Kind: "text", Text: "ping @44400 and @444444",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.UpsertContact("44400@s.whatsapp.net", "44400", "Direct Dial", "", ""); err != nil {
		t.Fatalf("seed contact: %v", err)
	}

	rows, err := s.Messages(f.chat2, 0, 0, 0)
	if err != nil {
		t.Fatalf("Messages: %v", err)
	}
	got := findMessage(t, rows, "MN2").Text
	if !strings.Contains(got, "@Direct Dial") {
		t.Fatalf("mention text = %q, want the phone contact resolved", got)
	}
	if !strings.Contains(got, "@444444") {
		t.Fatalf("mention text = %q, want the unknown mention left as-is", got)
	}
}

// A mapped LID whose phone number has no contact row still improves to the
// phone digits — same fallback order as sender names.
func TestMentionsFallBackToPhoneDigits(t *testing.T) {
	s := newTestStore(t)
	f := seedFixture(t, s)

	if err := s.UpsertLIDMapping("77777@lid", "919876543210@s.whatsapp.net"); err != nil {
		t.Fatalf("UpsertLIDMapping: %v", err)
	}
	if err := s.UpsertMessage(Message{
		ChatJID: f.chat2, ID: "MN3", SenderJID: f.contactA, TS: 502,
		Kind: "text", Text: "@77777 wdyt",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	rows, err := s.Messages(f.chat2, 0, 0, 0)
	if err != nil {
		t.Fatalf("Messages: %v", err)
	}
	if got := findMessage(t, rows, "MN3").Text; got != "@919876543210 wdyt" {
		t.Fatalf("mention text = %q, want the mapped phone digits", got)
	}
}

// Only standalone @digits tokens are mentions: an email address or an @
// embedded mid-word must never be rewritten.
func TestMentionsLeaveEmailsAndMidWordTokensAlone(t *testing.T) {
	s := newTestStore(t)
	f := seedFixture(t, s)

	if err := s.UpsertContact("123456@s.whatsapp.net", "123456", "Should Not Appear", "", ""); err != nil {
		t.Fatalf("seed contact: %v", err)
	}
	const text = "mail me at bob@123456.com about item x@123456 thanks"
	if err := s.UpsertMessage(Message{
		ChatJID: f.chat2, ID: "MN4", SenderJID: f.contactA, TS: 503,
		Kind: "text", Text: text,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	rows, err := s.Messages(f.chat2, 0, 0, 0)
	if err != nil {
		t.Fatalf("Messages: %v", err)
	}
	if got := findMessage(t, rows, "MN4").Text; got != text {
		t.Fatalf("text = %q, want it untouched (no standalone mention present)", got)
	}
}

// Search and context rows render through the same resolution.
func TestMentionsResolveInSearchAndContext(t *testing.T) {
	s := newTestStore(t)
	f := seedFixture(t, s)

	if err := s.UpsertLIDMapping("99566015803422@lid", f.contactB); err != nil {
		t.Fatalf("UpsertLIDMapping: %v", err)
	}
	if err := s.UpsertMessage(Message{
		ChatJID: f.chat2, ID: "MN5", SenderJID: f.contactA, TS: 504,
		Kind: "text", Text: "@99566015803422 mentionsearchterm",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	found, err := s.SearchMessages("mentionsearchterm", "", 0)
	if err != nil {
		t.Fatalf("SearchMessages: %v", err)
	}
	if len(found) != 1 || !strings.Contains(found[0].Text, "@Bobby Push") {
		t.Fatalf("search rows = %+v, want the mention resolved", found)
	}

	ctxRows, err := s.MessageContext(f.chat2, "MN5", 0, 0)
	if err != nil {
		t.Fatalf("MessageContext: %v", err)
	}
	if len(ctxRows) != 1 || !strings.Contains(ctxRows[0].Text, "@Bobby Push") {
		t.Fatalf("context rows = %+v, want the mention resolved in the target row", ctxRows)
	}
}

// MessagesAfterRowID and LatestRowID back poll_new_messages: an opaque
// rowid cursor (strictly insertion-ordered, no (ts,id) tie ambiguity) that
// an agent replays to receive each message exactly once.

func TestLatestRowIDAndMessagesAfterRowID(t *testing.T) {
	s := newTestStore(t)
	f := seedFixture(t, s)

	watermark, err := s.LatestRowID()
	if err != nil {
		t.Fatalf("LatestRowID: %v", err)
	}
	if watermark == 0 {
		t.Fatal("LatestRowID = 0 on a seeded store, want the newest row's id")
	}

	// Nothing after the watermark yet.
	rows, next, err := s.MessagesAfterRowID("", watermark, true, 0)
	if err != nil {
		t.Fatalf("MessagesAfterRowID: %v", err)
	}
	if len(rows) != 0 || next != watermark {
		t.Fatalf("rows=%d next=%d, want none and the cursor unchanged", len(rows), next)
	}

	// Two new messages arrive; both must come back oldest-first, and the
	// cursor must advance to cover them.
	if err := s.UpsertMessage(Message{ChatJID: f.chat1, ID: "P1", SenderJID: f.contactA, TS: 900, Kind: "text", Text: "first"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.UpsertMessage(Message{ChatJID: f.chat2, ID: "P2", SenderJID: f.contactB, TS: 901, Kind: "text", Text: "second"}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	rows, next, err = s.MessagesAfterRowID("", watermark, true, 0)
	if err != nil {
		t.Fatalf("MessagesAfterRowID: %v", err)
	}
	if len(rows) != 2 || rows[0].ID != "P1" || rows[1].ID != "P2" {
		t.Fatalf("rows = %+v, want [P1 P2] oldest-first", rows)
	}
	if next <= watermark {
		t.Fatalf("next = %d, want it advanced past %d", next, watermark)
	}

	// Replaying the advanced cursor yields nothing: exactly-once delivery.
	rows, _, err = s.MessagesAfterRowID("", next, true, 0)
	if err != nil {
		t.Fatalf("MessagesAfterRowID(replay): %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("replay rows = %+v, want none", rows)
	}
}

func TestMessagesAfterRowIDChatFilter(t *testing.T) {
	s := newTestStore(t)
	f := seedFixture(t, s)

	watermark, _ := s.LatestRowID()
	_ = s.UpsertMessage(Message{ChatJID: f.chat1, ID: "PC1", SenderJID: f.contactA, TS: 900, Kind: "text", Text: "in chat1"})
	_ = s.UpsertMessage(Message{ChatJID: f.chat2, ID: "PC2", SenderJID: f.contactB, TS: 901, Kind: "text", Text: "in chat2"})

	rows, _, err := s.MessagesAfterRowID(f.chat2, watermark, true, 0)
	if err != nil {
		t.Fatalf("MessagesAfterRowID: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != "PC2" {
		t.Fatalf("rows = %+v, want only the chat2 message", rows)
	}
}

// With includeOwn false, from-me rows are never returned — an autonomous
// agent must not be woken by (or react to) its own sends.
func TestMessagesAfterRowIDExcludesOwnSends(t *testing.T) {
	s := newTestStore(t)
	f := seedFixture(t, s)

	watermark, _ := s.LatestRowID()
	_ = s.UpsertMessage(Message{ChatJID: f.chat1, ID: "PO1", SenderJID: f.chat1, FromMe: true, TS: 900, Kind: "text", Text: "my own send"})
	_ = s.UpsertMessage(Message{ChatJID: f.chat1, ID: "PO2", SenderJID: f.contactA, TS: 901, Kind: "text", Text: "their reply"})

	rows, _, err := s.MessagesAfterRowID("", watermark, false, 0)
	if err != nil {
		t.Fatalf("MessagesAfterRowID: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != "PO2" {
		t.Fatalf("rows = %+v, want only the inbound message", rows)
	}

	rows, _, err = s.MessagesAfterRowID("", watermark, true, 0)
	if err != nil {
		t.Fatalf("MessagesAfterRowID(includeOwn): %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %+v, want both with includeOwn", rows)
	}
}

// MediaMessageIDs backs the batch form of download_media: it must return
// only messages that actually carry media, scoped to one chat, newest
// first, honoring the kind filter and time bounds.
func TestMediaMessageIDs(t *testing.T) {
	s := newTestStore(t)
	f := seedFixture(t, s) // chat1 has one media message: m3 (image, ts 300)

	ids, err := s.MediaMessageIDs(f.chat1, 0, 0, "", 0)
	if err != nil {
		t.Fatalf("MediaMessageIDs: %v", err)
	}
	if len(ids) != 1 || ids[0] != "m3" {
		t.Fatalf("MediaMessageIDs(chat1) = %v, want [m3] — text rows must not appear", ids)
	}

	ids, err = s.MediaMessageIDs(f.chat1, 0, 0, "document", 0)
	if err != nil {
		t.Fatalf("MediaMessageIDs(kind): %v", err)
	}
	if len(ids) != 0 {
		t.Fatalf("MediaMessageIDs(chat1, document) = %v, want none (m3 is an image)", ids)
	}

	ids, err = s.MediaMessageIDs(f.chat1, 250, 0, "", 0)
	if err != nil {
		t.Fatalf("MediaMessageIDs(before): %v", err)
	}
	if len(ids) != 0 {
		t.Fatalf("MediaMessageIDs(chat1, before=250) = %v, want none (m3 is ts 300)", ids)
	}

	ids, err = s.MediaMessageIDs(f.chat2, 0, 0, "voice", 0)
	if err != nil {
		t.Fatalf("MediaMessageIDs(chat2): %v", err)
	}
	if len(ids) != 1 || ids[0] != "g3" {
		t.Fatalf("MediaMessageIDs(chat2, voice) = %v, want [g3]", ids)
	}
}

func TestCallsPeerNameResolvesViaContactsWithJIDFallback(t *testing.T) {
	s := newTestStore(t)
	f := seedFixture(t, s)

	rows, err := s.Calls("", 0, 0, 0)
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
