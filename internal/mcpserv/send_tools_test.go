package mcpserv

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/idle-sync/whatsapp-connect-mcp/internal/gate"
	"github.com/idle-sync/whatsapp-connect-mcp/internal/store"
)

// sendFakeStore is a minimal Store fake for the send tool tests: only Chat
// and SearchContacts (name resolution) and MessageContext (author
// resolution) carry real behavior; the remaining Store methods are unused
// by send tools and stubbed to satisfy the interface.
type sendFakeStore struct {
	// chat maps a chat JID to the row Chat(jid) should return; presence in
	// the map is "ok", matching the real Store.Chat's (row, found, error).
	chat    map[string]store.ChatRow
	chatErr error

	searchContactsQuery string
	searchContactsLimit int
	// searchContactsRet is filtered by query the same way the real
	// Store.SearchContacts filters: a case-insensitive substring match
	// against Name or Phone, so a test can't accidentally get a match it
	// didn't earn by supplying an unrelated query.
	searchContactsRet []store.ContactRow
	searchContactsErr error

	// messageContext maps "chatJID|id" to the row MessageContext(chatJID,
	// id, 0, 0) should return as the sole element of its result slice.
	messageContext    map[string]store.MessageRow
	messageContextErr error
}

func (f *sendFakeStore) Chats(string, bool, int) ([]store.ChatRow, error) { return nil, nil }

func (f *sendFakeStore) Chat(jid string) (store.ChatRow, bool, error) {
	if f.chatErr != nil {
		return store.ChatRow{}, false, f.chatErr
	}
	row, ok := f.chat[jid]
	return row, ok, nil
}

func (f *sendFakeStore) Messages(string, int64, int64, int) ([]store.MessageRow, error) {
	return nil, nil
}
func (f *sendFakeStore) SearchMessages(string, string, int) ([]store.MessageRow, error) {
	return nil, nil
}

func (f *sendFakeStore) MessageContext(chatJID, id string, _, _ int) ([]store.MessageRow, error) {
	if f.messageContextErr != nil {
		return nil, f.messageContextErr
	}
	row, ok := f.messageContext[chatJID+"|"+id]
	if !ok {
		return nil, nil
	}
	return []store.MessageRow{row}, nil
}

func (f *sendFakeStore) SearchContacts(query string, limit int) ([]store.ContactRow, error) {
	f.searchContactsQuery = query
	f.searchContactsLimit = limit
	if f.searchContactsErr != nil {
		return nil, f.searchContactsErr
	}
	if query == "" {
		return f.searchContactsRet, nil
	}
	q := strings.ToLower(query)
	var out []store.ContactRow
	for _, c := range f.searchContactsRet {
		if strings.Contains(strings.ToLower(c.Name), q) || strings.Contains(strings.ToLower(c.Phone), q) {
			out = append(out, c)
		}
	}
	return out, nil
}

func (f *sendFakeStore) LastInteraction(string) (store.MessageRow, bool, error) {
	return store.MessageRow{}, false, nil
}
func (f *sendFakeStore) Calls(string, int64, int64, int) ([]store.CallRow, error) { return nil, nil }
func (f *sendFakeStore) LatestRowID() (int64, error) { return 0, nil }
func (f *sendFakeStore) MessagesAfterRowID(string, int64, bool, int) ([]store.MessageRow, int64, error) {
	return nil, 0, nil
}
func (f *sendFakeStore) MediaMessageIDs(string, int64, int64, string, int) ([]string, error) {
	return nil, nil
}
func (f *sendFakeStore) MessageMediaRef(string, string) ([]byte, string, string, error) {
	return nil, "", "", nil
}

func (f *sendFakeStore) OldestMessage(_ string) (store.MessageRow, bool, error) {
	return store.MessageRow{}, false, nil
}

func (f *sendFakeStore) QuickCheck() error { return nil }

// fakeDeliverer records every Delivery handed to it and returns a
// deterministic message ID, mirroring gate's own test fake since that one
// is unexported to its package.
type fakeDeliverer struct {
	mu        sync.Mutex
	delivered []gate.Delivery
	nextID    int
}

func (f *fakeDeliverer) Validate(_ gate.Delivery) error { return nil }

func (f *fakeDeliverer) Deliver(_ context.Context, d gate.Delivery) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.delivered = append(f.delivered, d)
	f.nextID++
	return "msg-" + itoaSend(f.nextID), nil
}

func (f *fakeDeliverer) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.delivered)
}

func itoaSend(n int) string {
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

func trustNoneSend(string) bool { return false }

// fakeClock is a manually advanced clock so tests never sleep.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock(start time.Time) *fakeClock {
	return &fakeClock{now: start}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// extractField pulls the value following "field: " on its own line out of a
// tool result's rendered text.
func extractField(t *testing.T, text, field string) string {
	t.Helper()
	marker := field + ": "
	idx := strings.Index(text, marker)
	if idx == -1 {
		t.Fatalf("text %q missing field %q", text, field)
	}
	rest := text[idx+len(marker):]
	if nl := strings.IndexByte(rest, '\n'); nl != -1 {
		return rest[:nl]
	}
	return rest
}

func TestSendMessageTwoStepRoundTripThroughToolHandlers(t *testing.T) {
	clock := newFakeClock(time.Unix(0, 0))
	deliverer := &fakeDeliverer{}
	g := gate.New(deliverer, trustNoneSend, 3, 12, clock.Now)
	d := &sendDeps{st: &sendFakeStore{}, g: g}

	in := sendMessageInput{To: "111@s.whatsapp.net", Text: "hello there"}

	first, _, err := d.sendMessage(context.Background(), nil, in)
	if err != nil {
		t.Fatalf("sendMessage() first call error = %v", err)
	}
	text := resultText(t, first)
	if !strings.Contains(text, "Confirm with the user, then re-issue this call with draft_token to send.") {
		t.Fatalf("sendMessage() draft text = %q, want the confirm sentence", text)
	}
	if !strings.HasPrefix(text, bannerWarning) {
		t.Fatalf("sendMessage() draft text = %q, want it to start with the banner warning", text)
	}
	token := extractField(t, text, "draft_token")
	if token == "" {
		t.Fatal("sendMessage() draft text missing a non-empty draft_token")
	}
	if deliverer.count() != 0 {
		t.Fatalf("deliverer called %d times after draft, want 0", deliverer.count())
	}

	in.DraftToken = token
	second, _, err := d.sendMessage(context.Background(), nil, in)
	if err != nil {
		t.Fatalf("sendMessage() commit call error = %v", err)
	}
	text2 := resultText(t, second)
	if extractField(t, text2, "message_id") == "" {
		t.Fatalf("sendMessage() sent text = %q, want a non-empty message_id", text2)
	}
	if strings.Contains(text2, "draft_token") {
		t.Fatalf("sendMessage() sent text = %q, must not mention draft_token", text2)
	}
	if deliverer.count() != 1 {
		t.Fatalf("deliverer called %d times after commit, want 1", deliverer.count())
	}
}

func TestSendMediaNonexistentPathIsCategoryError(t *testing.T) {
	deliverer := &fakeDeliverer{}
	g := gate.New(deliverer, trustNoneSend, 3, 12, time.Now)
	d := &sendDeps{st: &sendFakeStore{}, g: g}

	missing := filepath.Join(t.TempDir(), "does-not-exist.jpg")
	_, _, err := d.sendMedia(context.Background(), nil, sendMediaInput{To: "111@s.whatsapp.net", Path: missing})
	if err == nil {
		t.Fatal("sendMedia() error = nil, want a category error for a nonexistent path")
	}
	if strings.Contains(err.Error(), missing) {
		t.Fatalf("sendMedia() error = %q, must not echo the file path", err.Error())
	}
	if deliverer.count() != 0 {
		t.Fatalf("deliverer called %d times, want 0 (validation must happen before any send attempt)", deliverer.count())
	}
}

func TestSendVoiceNoteNonexistentPathIsCategoryError(t *testing.T) {
	deliverer := &fakeDeliverer{}
	g := gate.New(deliverer, trustNoneSend, 3, 12, time.Now)
	d := &sendDeps{st: &sendFakeStore{}, g: g}

	missing := filepath.Join(t.TempDir(), "does-not-exist.ogg")
	_, _, err := d.sendVoiceNote(context.Background(), nil, sendVoiceNoteInput{To: "111@s.whatsapp.net", Path: missing})
	if err == nil {
		t.Fatal("sendVoiceNote() error = nil, want a category error for a nonexistent path")
	}
	if !strings.Contains(err.Error(), "voice note file") {
		t.Fatalf("sendVoiceNote() error = %q, want it to use the \"voice note file\" category, not \"media file\"", err.Error())
	}
	if strings.Contains(err.Error(), "media file") {
		t.Fatalf("sendVoiceNote() error = %q, must not use the send_media category wording", err.Error())
	}
	if deliverer.count() != 0 {
		t.Fatalf("deliverer called %d times, want 0", deliverer.count())
	}
}

func TestSendVoiceNoteRejectsNonOggExtension(t *testing.T) {
	deliverer := &fakeDeliverer{}
	g := gate.New(deliverer, trustNoneSend, 3, 12, time.Now)
	d := &sendDeps{st: &sendFakeStore{}, g: g}

	path := filepath.Join(t.TempDir(), "note.mp3")
	if err := os.WriteFile(path, []byte("not ogg content"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, _, err := d.sendVoiceNote(context.Background(), nil, sendVoiceNoteInput{To: "111@s.whatsapp.net", Path: path})
	if err == nil {
		t.Fatal("sendVoiceNote() error = nil, want a category error for a non-.ogg extension")
	}
	if !strings.Contains(err.Error(), "Ogg Opus") {
		t.Fatalf("sendVoiceNote() error = %q, want it to mention Ogg Opus", err.Error())
	}
	if deliverer.count() != 0 {
		t.Fatalf("deliverer called %d times, want 0 (extension must be rejected before any send attempt)", deliverer.count())
	}
}

func TestSendMessageDraftPreviewPrefersChatNameOverContactName(t *testing.T) {
	deliverer := &fakeDeliverer{}
	g := gate.New(deliverer, trustNoneSend, 3, 12, time.Now)
	// A decoy contact under the same JID with a different name: if
	// SearchContacts were consulted despite Chat already resolving a name,
	// this wrong name would leak into the preview instead.
	st := &sendFakeStore{
		chat: map[string]store.ChatRow{"111@s.whatsapp.net": {JID: "111@s.whatsapp.net", Name: "Alice"}},
		searchContactsRet: []store.ContactRow{
			{JID: "111@s.whatsapp.net", Phone: "111", Name: "WrongName"},
		},
	}
	d := &sendDeps{st: st, g: g}

	result, _, err := d.sendMessage(context.Background(), nil, sendMessageInput{To: "111@s.whatsapp.net", Text: "hi"})
	if err != nil {
		t.Fatalf("sendMessage() error = %v", err)
	}
	text := resultText(t, result)
	if !strings.Contains(text, "Alice") {
		t.Fatalf("sendMessage() draft text = %q, want it to contain the chat's name %q", text, "Alice")
	}
	if strings.Contains(text, "WrongName") {
		t.Fatalf("sendMessage() draft text = %q, want the Chat lookup to take priority over SearchContacts", text)
	}
	if st.searchContactsQuery != "" {
		t.Fatalf("SearchContacts was queried (query = %q) even though Chat already resolved a name; it should not have been consulted", st.searchContactsQuery)
	}
	if !strings.Contains(text, "111@s.whatsapp.net") {
		t.Fatalf("sendMessage() draft text = %q, want it to contain the recipient JID", text)
	}
}

func TestSendMessageDraftPreviewFallsBackToContactNameWhenChatHasNoName(t *testing.T) {
	deliverer := &fakeDeliverer{}
	g := gate.New(deliverer, trustNoneSend, 3, 12, time.Now)
	st := &sendFakeStore{
		// The chat is known but carries no name yet, so resolution must
		// fall through to SearchContacts.
		chat: map[string]store.ChatRow{"111@s.whatsapp.net": {JID: "111@s.whatsapp.net", Name: ""}},
		searchContactsRet: []store.ContactRow{
			{JID: "111@s.whatsapp.net", Phone: "111", Name: "Bob"},
		},
	}
	d := &sendDeps{st: st, g: g}

	result, _, err := d.sendMessage(context.Background(), nil, sendMessageInput{To: "111@s.whatsapp.net", Text: "hi"})
	if err != nil {
		t.Fatalf("sendMessage() error = %v", err)
	}
	text := resultText(t, result)
	if !strings.Contains(text, "Bob") {
		t.Fatalf("sendMessage() draft text = %q, want the SearchContacts fallback name %q", text, "Bob")
	}
	if st.searchContactsQuery != "111" {
		t.Fatalf("SearchContacts queried with %q, want the JID's local part %q", st.searchContactsQuery, "111")
	}
}

func TestSendMessageDraftPreviewFallsBackToBareJIDWhenNothingResolves(t *testing.T) {
	deliverer := &fakeDeliverer{}
	g := gate.New(deliverer, trustNoneSend, 3, 12, time.Now)
	d := &sendDeps{st: &sendFakeStore{}, g: g}

	result, _, err := d.sendMessage(context.Background(), nil, sendMessageInput{To: "111@s.whatsapp.net", Text: "hi"})
	if err != nil {
		t.Fatalf("sendMessage() error = %v", err)
	}
	text := resultText(t, result)
	if !strings.Contains(text, "111@s.whatsapp.net") {
		t.Fatalf("sendMessage() draft text = %q, want it to contain the bare recipient JID", text)
	}
	if strings.Contains(text, "(111@s.whatsapp.net)") {
		t.Fatalf("sendMessage() draft text = %q, want no resolved-name-plus-JID form when nothing resolves", text)
	}
}

func TestMarkReadSkipsDraftingButHitsTheLimiter(t *testing.T) {
	clock := newFakeClock(time.Unix(0, 0))
	deliverer := &fakeDeliverer{}
	// burst 1: the first sender group consumes the sole token, so a second
	// distinct sender group in the same call must be rate limited.
	g := gate.New(deliverer, trustNoneSend, 1, 12, clock.Now)

	st := &sendFakeStore{messageContext: map[string]store.MessageRow{
		"c@g.us|a": {ChatJID: "c@g.us", ID: "a", SenderJID: "s1@s.whatsapp.net"},
		"c@g.us|b": {ChatJID: "c@g.us", ID: "b", SenderJID: "s2@s.whatsapp.net"},
	}}
	d := &sendDeps{st: st, g: g}

	_, _, err := d.markRead(context.Background(), nil, markReadInput{ChatJID: "c@g.us", MessageIDs: []string{"a", "b"}})
	if err == nil {
		t.Fatal("markRead() error = nil, want a rate limit error from the second sender group")
	}
	if !strings.Contains(err.Error(), "rate limit reached") {
		t.Fatalf("markRead() error = %q, want it to mention rate limit reached", err.Error())
	}
	// mark_read never drafts: the first sender group must have delivered
	// immediately before the second group hit the limiter.
	if deliverer.count() != 1 {
		t.Fatalf("deliverer called %d times, want 1 (first sender group delivered, no draft step)", deliverer.count())
	}
	if deliverer.delivered[0].Author != "s1@s.whatsapp.net" {
		t.Fatalf("first delivery Author = %q, want %q", deliverer.delivered[0].Author, "s1@s.whatsapp.net")
	}
}

func TestMarkReadGroupsMessageIDsBySenderIntoOneDeliveryEach(t *testing.T) {
	deliverer := &fakeDeliverer{}
	g := gate.New(deliverer, trustNoneSend, 5, 12, time.Now)

	st := &sendFakeStore{messageContext: map[string]store.MessageRow{
		"c@g.us|a": {ChatJID: "c@g.us", ID: "a", SenderJID: "s1@s.whatsapp.net"},
		"c@g.us|b": {ChatJID: "c@g.us", ID: "b", SenderJID: "s1@s.whatsapp.net"},
		"c@g.us|c": {ChatJID: "c@g.us", ID: "c", SenderJID: "s2@s.whatsapp.net"},
	}}
	d := &sendDeps{st: st, g: g}

	_, _, err := d.markRead(context.Background(), nil, markReadInput{ChatJID: "c@g.us", MessageIDs: []string{"a", "b", "c"}})
	if err != nil {
		t.Fatalf("markRead() error = %v", err)
	}
	if deliverer.count() != 2 {
		t.Fatalf("deliverer called %d times, want 2 (one per distinct sender)", deliverer.count())
	}
	if got := deliverer.delivered[0].MessageIDs; len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("first delivery MessageIDs = %v, want [a b] grouped under the shared sender", got)
	}
	if got := deliverer.delivered[1].MessageIDs; len(got) != 1 || got[0] != "c" {
		t.Fatalf("second delivery MessageIDs = %v, want [c]", got)
	}
}

func TestSendReactionResolvesAuthorFromMessageContext(t *testing.T) {
	deliverer := &fakeDeliverer{}
	// Trusted recipient so the reaction sends on the first call and we can
	// inspect exactly what reached the deliverer.
	g := gate.New(deliverer, func(jid string) bool { return jid == "c@g.us" }, 3, 12, time.Now)

	st := &sendFakeStore{messageContext: map[string]store.MessageRow{
		"c@g.us|m1": {ChatJID: "c@g.us", ID: "m1", SenderJID: "author@s.whatsapp.net"},
	}}
	d := &sendDeps{st: st, g: g}

	_, _, err := d.sendReaction(context.Background(), nil, sendReactionInput{ChatJID: "c@g.us", MessageID: "m1", Emoji: "👍"})
	if err != nil {
		t.Fatalf("sendReaction() error = %v", err)
	}
	if deliverer.count() != 1 {
		t.Fatalf("deliverer called %d times, want 1", deliverer.count())
	}
	got := deliverer.delivered[0]
	if got.Kind != "reaction" {
		t.Fatalf("delivered Kind = %q, want %q", got.Kind, "reaction")
	}
	if got.Author != "author@s.whatsapp.net" {
		t.Fatalf("delivered Author = %q, want the target message's sender %q", got.Author, "author@s.whatsapp.net")
	}
	if got.QuotedID != "m1" {
		t.Fatalf("delivered QuotedID = %q, want %q", got.QuotedID, "m1")
	}
}

func TestBlockContactDraftsThenCommits(t *testing.T) {
	deliverer := &fakeDeliverer{}
	// Trusted recipient: proves block ignores trust and still drafts.
	g := gate.New(deliverer, func(string) bool { return true }, 3, 12, time.Now)
	d := &sendDeps{st: &sendFakeStore{}, g: g}

	first, _, err := d.blockContact(context.Background(), nil, blockContactInput{JID: "111@s.whatsapp.net"})
	if err != nil {
		t.Fatalf("blockContact() first call error = %v", err)
	}
	if deliverer.count() != 0 {
		t.Fatalf("deliverer called %d times on the drafting call, want 0", deliverer.count())
	}
	token := extractField(t, resultText(t, first), "draft_token")
	if token == "" {
		t.Fatal("blockContact() first call returned no draft_token")
	}

	second, _, err := d.blockContact(context.Background(), nil, blockContactInput{
		JID: "111@s.whatsapp.net", DraftToken: token,
	})
	if err != nil {
		t.Fatalf("blockContact() commit error = %v", err)
	}
	if deliverer.count() != 1 {
		t.Fatalf("deliverer called %d times after commit, want 1", deliverer.count())
	}
	if got := deliverer.delivered[0]; got.Kind != "block" || got.To != "111@s.whatsapp.net" {
		t.Fatalf("delivered = %+v, want a block of the JID", got)
	}
	_ = second
}

// TestRenderSendResultNoMessageIDReportsDone covers the block-list case,
// where the committed action produces no message id: the render must report
// a plain confirmation, not an empty "message_id:" line.
func TestRenderSendResultNoMessageIDReportsDone(t *testing.T) {
	text := renderSendResult(gate.Result{Sent: true, MessageID: "", Preview: "Alice (111@s.whatsapp.net) block"})
	if strings.Contains(text, "message_id:") {
		t.Errorf("render = %q, want no message_id line for an id-less action", text)
	}
	if !strings.Contains(text, "done") {
		t.Errorf("render = %q, want a done confirmation", text)
	}
}

func TestCreatePollTrustedSendsPollKind(t *testing.T) {
	deliverer := &fakeDeliverer{}
	g := gate.New(deliverer, func(jid string) bool { return jid == "c@g.us" }, 3, 12, time.Now)
	d := &sendDeps{st: &sendFakeStore{}, g: g}

	_, _, err := d.createPoll(context.Background(), nil, createPollInput{
		To: "c@g.us", Question: "Lunch?", Options: []string{"Pizza", "Sushi"}, SelectableCount: 1,
	})
	if err != nil {
		t.Fatalf("createPoll() error = %v", err)
	}
	if deliverer.count() != 1 {
		t.Fatalf("deliverer called %d times, want 1", deliverer.count())
	}
	got := deliverer.delivered[0]
	if got.Kind != "poll" || got.Text != "Lunch?" {
		t.Fatalf("delivered = %+v, want a poll with the question", got)
	}
	if len(got.Options) != 2 || got.Options[0] != "Pizza" || got.Options[1] != "Sushi" {
		t.Fatalf("delivered Options = %v, want [Pizza Sushi]", got.Options)
	}
}

func TestEditMessageBuildsEditDeliveryAndDrafts(t *testing.T) {
	deliverer := &fakeDeliverer{}
	// Untrusted recipient: the first call must draft, not send.
	g := gate.New(deliverer, trustNoneSend, 3, 12, time.Now)
	d := &sendDeps{st: &sendFakeStore{}, g: g}

	result, _, err := d.editMessage(context.Background(), nil, editMessageInput{
		ChatJID: "111@s.whatsapp.net", MessageID: "m1", Text: "corrected",
	})
	if err != nil {
		t.Fatalf("editMessage() error = %v", err)
	}
	if deliverer.count() != 0 {
		t.Fatalf("deliverer called %d times on the drafting call, want 0", deliverer.count())
	}
	text := resultText(t, result)
	if extractField(t, text, "draft_token") == "" {
		t.Fatalf("editMessage() draft text = %q, want a draft_token", text)
	}
}

func TestEditMessageTrustedSendsEditKind(t *testing.T) {
	deliverer := &fakeDeliverer{}
	g := gate.New(deliverer, func(jid string) bool { return jid == "111@s.whatsapp.net" }, 3, 12, time.Now)
	d := &sendDeps{st: &sendFakeStore{}, g: g}

	_, _, err := d.editMessage(context.Background(), nil, editMessageInput{
		ChatJID: "111@s.whatsapp.net", MessageID: "m1", Text: "corrected",
	})
	if err != nil {
		t.Fatalf("editMessage() error = %v", err)
	}
	if deliverer.count() != 1 {
		t.Fatalf("deliverer called %d times, want 1", deliverer.count())
	}
	got := deliverer.delivered[0]
	if got.Kind != "edit" || got.QuotedID != "m1" || got.Text != "corrected" {
		t.Fatalf("delivered = %+v, want an edit of m1 with the new text", got)
	}
}

func TestDeleteMessageResolvesAuthorAndSendsRevokeKind(t *testing.T) {
	deliverer := &fakeDeliverer{}
	g := gate.New(deliverer, func(jid string) bool { return jid == "c@g.us" }, 3, 12, time.Now)

	st := &sendFakeStore{messageContext: map[string]store.MessageRow{
		"c@g.us|m1": {ChatJID: "c@g.us", ID: "m1", SenderJID: "author@s.whatsapp.net"},
	}}
	d := &sendDeps{st: st, g: g}

	_, _, err := d.deleteMessage(context.Background(), nil, deleteMessageInput{ChatJID: "c@g.us", MessageID: "m1"})
	if err != nil {
		t.Fatalf("deleteMessage() error = %v", err)
	}
	if deliverer.count() != 1 {
		t.Fatalf("deliverer called %d times, want 1", deliverer.count())
	}
	got := deliverer.delivered[0]
	if got.Kind != "revoke" {
		t.Fatalf("delivered Kind = %q, want %q", got.Kind, "revoke")
	}
	if got.QuotedID != "m1" {
		t.Fatalf("delivered QuotedID = %q, want %q", got.QuotedID, "m1")
	}
	// The author must be resolved so an admin-delete of someone else's
	// message carries the right sender.
	if got.Author != "author@s.whatsapp.net" {
		t.Fatalf("delivered Author = %q, want the target's sender %q", got.Author, "author@s.whatsapp.net")
	}
}

func TestDeleteMessageUnknownMessageIsCategoryError(t *testing.T) {
	deliverer := &fakeDeliverer{}
	g := gate.New(deliverer, trustNoneSend, 3, 12, time.Now)
	d := &sendDeps{st: &sendFakeStore{}, g: g}

	_, _, err := d.deleteMessage(context.Background(), nil, deleteMessageInput{ChatJID: "c@g.us", MessageID: "missing"})
	if err == nil {
		t.Fatal("deleteMessage() error = nil, want an error for an unknown target message")
	}
	if deliverer.count() != 0 {
		t.Fatalf("deliverer called %d times, want 0", deliverer.count())
	}
}

func TestSendReactionUnknownMessageIsCategoryError(t *testing.T) {
	deliverer := &fakeDeliverer{}
	g := gate.New(deliverer, trustNoneSend, 3, 12, time.Now)
	d := &sendDeps{st: &sendFakeStore{}, g: g}

	_, _, err := d.sendReaction(context.Background(), nil, sendReactionInput{ChatJID: "c@g.us", MessageID: "missing", Emoji: "👍"})
	if err == nil {
		t.Fatal("sendReaction() error = nil, want an error for an unknown target message")
	}
	if deliverer.count() != 0 {
		t.Fatalf("deliverer called %d times, want 0", deliverer.count())
	}
}

func TestRegisterSendToolsBuildsAllFiveSchemasWithoutPanicking(t *testing.T) {
	server := New(&fakeStore{}, &fakeLive{}, nil, t.TempDir(), DoctorEnv{})
	if server == nil {
		t.Fatal("New() returned a nil server")
	}
}
