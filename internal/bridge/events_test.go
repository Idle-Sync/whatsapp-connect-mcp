package bridge

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/proto/waSyncAction"
	"go.mau.fi/whatsmeow/proto/waWeb"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"github.com/idle-sync/whatsapp-connect-mcp/internal/mediapath"
	"github.com/idle-sync/whatsapp-connect-mcp/internal/store"
)

func dmSource(fromMe bool) types.MessageSource {
	return types.MessageSource{
		Chat:     types.NewJID("111", types.DefaultUserServer),
		Sender:   types.NewJID("111", types.DefaultUserServer),
		IsFromMe: fromMe,
		IsGroup:  false,
	}
}

func groupSource() types.MessageSource {
	return types.MessageSource{
		Chat:     types.NewJID("222", types.GroupServer),
		Sender:   types.NewJID("333", types.DefaultUserServer),
		IsFromMe: false,
		IsGroup:  true,
	}
}

func messageEvent(source types.MessageSource, pushName string, msg *waE2E.Message) *events.Message {
	return &events.Message{
		Info: types.MessageInfo{
			MessageSource: source,
			ID:            "MSG1",
			PushName:      pushName,
			Timestamp:     time.Unix(1700000000, 0),
		},
		Message: msg,
	}
}

func TestDecodeMessageText(t *testing.T) {
	evt := messageEvent(dmSource(false), "Alice", &waE2E.Message{
		Conversation: proto.String("hello there"),
	})

	m, chatName, isGroup := decodeMessage(evt)

	if m.Kind != "text" || m.Text != "hello there" {
		t.Fatalf("kind/text = %q/%q, want text/%q", m.Kind, m.Text, "hello there")
	}
	if m.ChatJID != "111@s.whatsapp.net" || m.SenderJID != "111@s.whatsapp.net" {
		t.Fatalf("chat/sender JID = %q/%q, want 111@s.whatsapp.net for both", m.ChatJID, m.SenderJID)
	}
	if m.ID != "MSG1" || m.FromMe || m.TS != 1700000000 {
		t.Fatalf("id/fromMe/ts = %q/%v/%d, want MSG1/false/1700000000", m.ID, m.FromMe, m.TS)
	}
	if m.QuotedID != "" || m.MediaRef != nil {
		t.Fatalf("quotedID/mediaRef = %q/%v, want empty/nil for plain text", m.QuotedID, m.MediaRef)
	}
	if chatName != "Alice" || isGroup {
		t.Fatalf("chatName/isGroup = %q/%v, want Alice/false", chatName, isGroup)
	}
}

func TestDecodeMessageExtendedTextWithQuote(t *testing.T) {
	evt := messageEvent(dmSource(false), "Alice", &waE2E.Message{
		ExtendedTextMessage: &waE2E.ExtendedTextMessage{
			Text:        proto.String("replying"),
			ContextInfo: &waE2E.ContextInfo{StanzaID: proto.String("ORIG1")},
		},
	})

	m, _, _ := decodeMessage(evt)

	if m.Kind != "text" || m.Text != "replying" {
		t.Fatalf("kind/text = %q/%q, want text/replying", m.Kind, m.Text)
	}
	if m.QuotedID != "ORIG1" {
		t.Fatalf("quotedID = %q, want ORIG1", m.QuotedID)
	}
}

func TestDecodeMessageImageWithCaption(t *testing.T) {
	evt := messageEvent(dmSource(false), "Alice", &waE2E.Message{
		ImageMessage: &waE2E.ImageMessage{
			Caption:  proto.String("look at this"),
			Mimetype: proto.String("image/jpeg"),
		},
	})

	m, _, _ := decodeMessage(evt)

	if m.Kind != "image" || m.Text != "look at this" {
		t.Fatalf("kind/text = %q/%q, want image/look at this", m.Kind, m.Text)
	}
	if m.MediaRef == nil {
		t.Fatal("mediaRef = nil, want non-nil for an image message")
	}

	var unmarshaled waE2E.Message
	if err := proto.Unmarshal(m.MediaRef, &unmarshaled); err != nil {
		t.Fatalf("mediaRef does not unmarshal as waE2E.Message: %v", err)
	}
	if unmarshaled.GetImageMessage().GetCaption() != "look at this" {
		t.Fatalf("mediaRef roundtrip caption = %q, want look at this", unmarshaled.GetImageMessage().GetCaption())
	}
}

func TestDecodeMessageVoiceNote(t *testing.T) {
	evt := messageEvent(dmSource(false), "Alice", &waE2E.Message{
		AudioMessage: &waE2E.AudioMessage{
			PTT:      proto.Bool(true),
			Mimetype: proto.String("audio/ogg; codecs=opus"),
		},
	})

	m, _, _ := decodeMessage(evt)

	if m.Kind != "voice" {
		t.Fatalf("kind = %q, want voice", m.Kind)
	}
	if m.MediaRef == nil {
		t.Fatal("mediaRef = nil, want non-nil for a voice note")
	}
}

func TestDecodeMessageNonPTTAudioIsAudioNotVoice(t *testing.T) {
	evt := messageEvent(dmSource(false), "Alice", &waE2E.Message{
		AudioMessage: &waE2E.AudioMessage{PTT: proto.Bool(false)},
	})

	m, _, _ := decodeMessage(evt)

	if m.Kind != "audio" {
		t.Fatalf("kind = %q, want audio", m.Kind)
	}
}

func TestDecodeMessageReaction(t *testing.T) {
	evt := messageEvent(dmSource(false), "Alice", &waE2E.Message{
		ReactionMessage: &waE2E.ReactionMessage{
			Key:  &waCommon.MessageKey{ID: proto.String("TARGET1")},
			Text: proto.String("👍"),
		},
	})

	m, _, _ := decodeMessage(evt)

	if m.Kind != "reaction" || m.Text != "👍" {
		t.Fatalf("kind/text = %q/%q, want reaction/👍", m.Kind, m.Text)
	}
	if m.QuotedID != "TARGET1" {
		t.Fatalf("quotedID = %q, want TARGET1", m.QuotedID)
	}
	if m.MediaRef != nil {
		t.Fatalf("mediaRef = %v, want nil for a reaction", m.MediaRef)
	}
}

func TestDecodeMessageGroupChatNameIsUnknown(t *testing.T) {
	evt := messageEvent(groupSource(), "Bob", &waE2E.Message{
		Conversation: proto.String("hi group"),
	})

	m, chatName, isGroup := decodeMessage(evt)

	if !isGroup {
		t.Fatal("isGroup = false, want true")
	}
	if chatName != "" {
		t.Fatalf("chatName = %q, want empty (not derivable from a group message)", chatName)
	}
	if m.ChatJID != "222@g.us" || m.SenderJID != "333@s.whatsapp.net" {
		t.Fatalf("chat/sender JID = %q/%q, want 222@g.us/333@s.whatsapp.net", m.ChatJID, m.SenderJID)
	}
}

func TestDecodeMessageFromMeHasNoChatName(t *testing.T) {
	evt := messageEvent(dmSource(true), "MyOwnPushName", &waE2E.Message{
		Conversation: proto.String("sent from another device"),
	})

	m, chatName, _ := decodeMessage(evt)

	if !m.FromMe {
		t.Fatal("fromMe = false, want true")
	}
	if chatName != "" {
		t.Fatalf("chatName = %q, want empty (push name on a from-me event is our own, not the peer's)", chatName)
	}
}

func TestDecodeReceiptReadMapsToMarkRead(t *testing.T) {
	evt := &events.Receipt{
		MessageSource: dmSource(false),
		MessageIDs:    []string{"A", "B"},
		Timestamp:     time.Unix(1700000100, 0),
		Type:          types.ReceiptTypeRead,
	}

	chatJID, ids, readAt, ok := decodeReceipt(evt)

	if !ok {
		t.Fatal("ok = false, want true for a read receipt")
	}
	if chatJID != "111@s.whatsapp.net" || len(ids) != 2 || ids[0] != "A" || ids[1] != "B" || readAt != 1700000100 {
		t.Fatalf("decoded = (%q, %v, %d), want (111@s.whatsapp.net, [A B], 1700000100)", chatJID, ids, readAt)
	}
}

func TestDecodeReceiptDeliveredIsIgnored(t *testing.T) {
	evt := &events.Receipt{
		MessageSource: dmSource(false),
		MessageIDs:    []string{"A"},
		Type:          types.ReceiptTypeDelivered,
	}

	_, _, _, ok := decodeReceipt(evt)

	if ok {
		t.Fatal("ok = true, want false for a delivery receipt (not a read)")
	}
}

func TestCallStatusMapping(t *testing.T) {
	tests := []struct {
		reason string
		want   string
	}{
		{"timeout", "missed"},
		{"reject", "rejected"},
		{"decline", "rejected"},
		{"busy", "rejected"},
		{"", "unknown"},
		{"something-else", "unknown"},
	}
	for _, tt := range tests {
		if got := callStatus(tt.reason); got != tt.want {
			t.Errorf("callStatus(%q) = %q, want %q", tt.reason, got, tt.want)
		}
	}
}

// fakeIngest records every call made through the Ingest interface so tests
// can assert on the bridge's dispatch behavior without a real store.
type fakeIngest struct {
	chats       []fakeChatCall
	messages    []store.Message
	contacts    []fakeContactCall
	reads       []fakeReadCall
	calls       []fakeCallCall
	lidMappings []fakeLIDMappingCall
}

type fakeLIDMappingCall struct {
	lid, pn string
}

type fakeChatCall struct {
	jid, name     string
	isGroup       bool
	lastMessageAt int64
}

type fakeContactCall struct {
	jid, phone, pushName, fullName, businessName string
}

type fakeReadCall struct {
	chatJID string
	ids     []string
	readAt  int64
}

type fakeCallCall struct {
	id, peerJID       string
	ts                int64
	direction, status string
	isVideo           bool
}

func (f *fakeIngest) UpsertChat(jid, name string, isGroup bool, lastMessageAt int64) error {
	f.chats = append(f.chats, fakeChatCall{jid, name, isGroup, lastMessageAt})
	return nil
}

func (f *fakeIngest) UpsertMessage(m store.Message) error {
	f.messages = append(f.messages, m)
	return nil
}

func (f *fakeIngest) UpsertContact(jid, phone, pushName, fullName, businessName string) error {
	f.contacts = append(f.contacts, fakeContactCall{jid, phone, pushName, fullName, businessName})
	return nil
}

func (f *fakeIngest) MarkRead(chatJID string, ids []string, readAt int64) error {
	f.reads = append(f.reads, fakeReadCall{chatJID, ids, readAt})
	return nil
}

func (f *fakeIngest) UpsertLIDMapping(lid, pn string) error {
	f.lidMappings = append(f.lidMappings, fakeLIDMappingCall{lid, pn})
	return nil
}

func (f *fakeIngest) InsertCall(id, peerJID string, ts int64, direction, status string, isVideo bool) error {
	f.calls = append(f.calls, fakeCallCall{id, peerJID, ts, direction, status, isVideo})
	return nil
}

// newTestBridge opens a Bridge against a temp dataDir with fake as the
// ingest sink. The underlying whatsmeow client is never connected, so this
// never touches the network: any live lookup handleEvent attempts (e.g.
// group info) fails fast with "not connected" instead of dialing out.
func newTestBridge(t *testing.T) (*Bridge, *fakeIngest) {
	t.Helper()
	return newTestBridgeWithRoots(t, mediapath.Roots{})
}

// newTestBridgeWithRoots is newTestBridge for the tests that need outbound
// media reads to be permitted from somewhere.
func newTestBridgeWithRoots(t *testing.T, roots mediapath.Roots) (*Bridge, *fakeIngest) {
	t.Helper()
	fake := &fakeIngest{}
	b, err := Open(context.Background(), t.TempDir(), fake, roots)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	t.Cleanup(func() { _ = b.Close() })
	return b, fake
}

func TestHandleEventMessageUpsertsChatMessageAndContact(t *testing.T) {
	b, fake := newTestBridge(t)
	evt := messageEvent(dmSource(false), "Alice", &waE2E.Message{
		Conversation: proto.String("hi"),
	})

	b.handleEvent(evt)

	if len(fake.messages) != 1 || fake.messages[0].Text != "hi" {
		t.Fatalf("messages = %+v, want one message with text hi", fake.messages)
	}
	if len(fake.chats) != 1 || fake.chats[0].name != "Alice" {
		t.Fatalf("chats = %+v, want one chat named Alice", fake.chats)
	}
	if len(fake.contacts) != 1 || fake.contacts[0].pushName != "Alice" || fake.contacts[0].phone != "111" {
		t.Fatalf("contacts = %+v, want one contact with pushName Alice and phone 111 (digit-shaped JID local part)", fake.contacts)
	}
}

func TestHandleEventGroupMessageUpsertsChatWithEmptyNameWhenUnknown(t *testing.T) {
	b, fake := newTestBridge(t)
	evt := messageEvent(groupSource(), "Bob", &waE2E.Message{
		Conversation: proto.String("hi group"),
	})

	b.handleEvent(evt)

	if len(fake.messages) != 1 {
		t.Fatalf("messages = %+v, want one message even though the chat name is unknown", fake.messages)
	}
	// The chat row (and its last_message_at bump) must exist even when the
	// group's name isn't known yet, or the chat never shows up in listings.
	// UpsertChat's empty-name guard (store package) means this can never
	// blank a real name learned later from a GroupInfo/HistorySync event.
	// No live GetGroupInfo lookup happens here — the message path does zero
	// network I/O.
	if len(fake.chats) != 1 {
		t.Fatalf("chats = %+v, want one UpsertChat call", fake.chats)
	}
	if fake.chats[0].name != "" || !fake.chats[0].isGroup || fake.chats[0].jid != "222@g.us" {
		t.Fatalf("chats[0] = %+v, want (jid=222@g.us, name=\"\", isGroup=true)", fake.chats[0])
	}
	// The sender's push name is still worth recording as contact info,
	// independent of whether the group's own name is known.
	if len(fake.contacts) != 1 || fake.contacts[0].pushName != "Bob" || fake.contacts[0].phone != "333" {
		t.Fatalf("contacts = %+v, want one contact with pushName Bob and phone 333 (sender's JID, not the group's)", fake.contacts)
	}
}

func TestHandleEventFromMeMessageSkipsContactUpsertButUpsertsChat(t *testing.T) {
	b, fake := newTestBridge(t)
	evt := messageEvent(dmSource(true), "MyOwnPushName", &waE2E.Message{
		Conversation: proto.String("sent from another device"),
	})

	b.handleEvent(evt)

	if len(fake.messages) != 1 {
		t.Fatalf("messages = %+v, want one message", fake.messages)
	}
	if len(fake.contacts) != 0 {
		t.Fatalf("contacts = %+v, want none: a from-me event's push name is our own, not the peer's", fake.contacts)
	}
	// The chat row must still exist (with an empty name, since a from-me
	// event doesn't tell us the peer's name) so last_message_at advances
	// and the chat shows up in listings.
	if len(fake.chats) != 1 || fake.chats[0].name != "" {
		t.Fatalf("chats = %+v, want one UpsertChat call with an empty name", fake.chats)
	}
}

func TestHandleEventReceiptReadCallsMarkRead(t *testing.T) {
	b, fake := newTestBridge(t)
	evt := &events.Receipt{
		MessageSource: dmSource(false),
		MessageIDs:    []string{"A", "B"},
		Timestamp:     time.Unix(1700000100, 0),
		Type:          types.ReceiptTypeRead,
	}

	b.handleEvent(evt)

	if len(fake.reads) != 1 || fake.reads[0].chatJID != "111@s.whatsapp.net" || len(fake.reads[0].ids) != 2 {
		t.Fatalf("reads = %+v, want one MarkRead call for chat 111@s.whatsapp.net with 2 ids", fake.reads)
	}
}

func TestHandleEventReceiptDeliveredIsIgnored(t *testing.T) {
	b, fake := newTestBridge(t)
	evt := &events.Receipt{
		MessageSource: dmSource(false),
		MessageIDs:    []string{"A"},
		Type:          types.ReceiptTypeDelivered,
	}

	b.handleEvent(evt)

	if len(fake.reads) != 0 {
		t.Fatalf("reads = %+v, want none for a delivery receipt", fake.reads)
	}
}

func TestHandleEventCallOfferAcceptTerminate(t *testing.T) {
	b, fake := newTestBridge(t)
	from := types.NewJID("111", types.DefaultUserServer)

	b.handleEvent(&events.CallOffer{BasicCallMeta: types.BasicCallMeta{
		From: from, CallID: "CALL1", Timestamp: time.Unix(1700000000, 0),
	}})
	b.handleEvent(&events.CallAccept{BasicCallMeta: types.BasicCallMeta{
		From: from, CallID: "CALL1", Timestamp: time.Unix(1700000005, 0),
	}})
	b.handleEvent(&events.CallTerminate{
		BasicCallMeta: types.BasicCallMeta{From: from, CallID: "CALL1", Timestamp: time.Unix(1700000030, 0)},
		Reason:        "normal-clearing",
	})

	if len(fake.calls) != 3 {
		t.Fatalf("calls = %+v, want 3 InsertCall calls (offer, accept, terminate)", fake.calls)
	}
	if fake.calls[0].status != "ringing" || fake.calls[1].status != "answered" || fake.calls[2].status != "unknown" {
		t.Fatalf("call statuses = %q/%q/%q, want ringing/answered/unknown",
			fake.calls[0].status, fake.calls[1].status, fake.calls[2].status)
	}
	for _, c := range fake.calls {
		if c.id != "CALL1" || c.peerJID != "111@s.whatsapp.net" || c.direction != "incoming" {
			t.Fatalf("call = %+v, want id CALL1, peer 111@s.whatsapp.net, direction incoming", c)
		}
	}
}

func TestDecodeHistorySyncConversation(t *testing.T) {
	conv := &waHistorySync.Conversation{
		ID:                    proto.String("222@g.us"),
		Name:                  proto.String("Group Name"),
		ConversationTimestamp: proto.Uint64(1700000000),
	}

	jid, name, isGroup, ok := decodeHistorySyncConversation(conv)

	if !ok {
		t.Fatal("ok = false, want true for a valid conversation")
	}
	if jid != "222@g.us" || name != "Group Name" || !isGroup {
		t.Fatalf("decoded = (%q, %q, %v), want (222@g.us, Group Name, true)", jid, name, isGroup)
	}
}

func TestDecodeHistorySyncConversationDirectChatIsNotGroup(t *testing.T) {
	conv := &waHistorySync.Conversation{ID: proto.String("111@s.whatsapp.net")}

	jid, name, isGroup, ok := decodeHistorySyncConversation(conv)

	if !ok || isGroup {
		t.Fatalf("ok/isGroup = %v/%v, want true/false for a direct chat", ok, isGroup)
	}
	if jid != "111@s.whatsapp.net" || name != "" {
		t.Fatalf("jid/name = %q/%q, want 111@s.whatsapp.net/\"\"", jid, name)
	}
}

func TestDecodeHistorySyncConversationInvalidIDIsIgnored(t *testing.T) {
	conv := &waHistorySync.Conversation{ID: proto.String("")}

	_, _, _, ok := decodeHistorySyncConversation(conv)

	if ok {
		t.Fatal("ok = true, want false for an empty conversation ID")
	}
}

func TestDecodeContact(t *testing.T) {
	evt := &events.Contact{
		JID:    types.NewJID("111", types.DefaultUserServer),
		Action: &waSyncAction.ContactAction{FullName: proto.String("Alice Smith")},
	}

	jid, fullName, ok := decodeContact(evt)

	if !ok || jid != "111@s.whatsapp.net" || fullName != "Alice Smith" {
		t.Fatalf("decoded = (%q, %q, %v), want (111@s.whatsapp.net, Alice Smith, true)", jid, fullName, ok)
	}
}

func TestDecodeContactNoFullNameIsIgnored(t *testing.T) {
	evt := &events.Contact{JID: types.NewJID("111", types.DefaultUserServer), Action: nil}

	_, _, ok := decodeContact(evt)

	if ok {
		t.Fatal("ok = true, want false when the contact action has no full name")
	}
}

func TestDecodePushName(t *testing.T) {
	evt := &events.PushName{JID: types.NewJID("111", types.DefaultUserServer), NewPushName: "Alice"}

	jid, pushName := decodePushName(evt)

	if jid != "111@s.whatsapp.net" || pushName != "Alice" {
		t.Fatalf("decoded = (%q, %q), want (111@s.whatsapp.net, Alice)", jid, pushName)
	}
}

func TestDecodeGroupInfoName(t *testing.T) {
	evt := &events.GroupInfo{
		JID:  types.NewJID("222", types.GroupServer),
		Name: &types.GroupName{Name: "New Name"},
	}

	jid, name, ok := decodeGroupInfoName(evt)

	if !ok || jid != "222@g.us" || name != "New Name" {
		t.Fatalf("decoded = (%q, %q, %v), want (222@g.us, New Name, true)", jid, name, ok)
	}
}

func TestDecodeGroupInfoNameIgnoresNonRenameChanges(t *testing.T) {
	evt := &events.GroupInfo{
		JID:   types.NewJID("222", types.GroupServer),
		Topic: &types.GroupTopic{Topic: "new topic, not a rename"},
	}

	_, _, ok := decodeGroupInfoName(evt)

	if ok {
		t.Fatal("ok = true, want false for a GroupInfo event that isn't a rename")
	}
}

func TestHandleEventContactUpsertsFullName(t *testing.T) {
	b, fake := newTestBridge(t)

	b.handleEvent(&events.Contact{
		JID:    types.NewJID("111", types.DefaultUserServer),
		Action: &waSyncAction.ContactAction{FullName: proto.String("Alice Smith")},
	})

	if len(fake.contacts) != 1 || fake.contacts[0].fullName != "Alice Smith" || fake.contacts[0].phone != "111" {
		t.Fatalf("contacts = %+v, want one contact with fullName Alice Smith and phone 111", fake.contacts)
	}
}

func TestHandleEventPushNameUpsertsPushName(t *testing.T) {
	b, fake := newTestBridge(t)

	b.handleEvent(&events.PushName{JID: types.NewJID("111", types.DefaultUserServer), NewPushName: "Alice"})

	if len(fake.contacts) != 1 || fake.contacts[0].pushName != "Alice" || fake.contacts[0].phone != "111" {
		t.Fatalf("contacts = %+v, want one contact with pushName Alice and phone 111", fake.contacts)
	}
}

func TestPhoneFromJID(t *testing.T) {
	cases := []struct {
		name string
		jid  string
		want string
	}{
		{"digit-shaped personal JID", "15551234567@s.whatsapp.net", "15551234567"},
		{"group JID has no phone", "222@g.us", ""},
		{"non-digit local part", "abc123@s.whatsapp.net", ""},
		{"empty jid", "", ""},
		{"unparseable jid", "not a jid", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := phoneFromJID(c.jid); got != c.want {
				t.Fatalf("phoneFromJID(%q) = %q, want %q", c.jid, got, c.want)
			}
		})
	}
}

// TestHandleEventContactIngestMakesItFindableByPhoneSearch is an
// end-to-end regression test for the advertised phone search: it wires a
// real store.Store (not fakeIngest) so it proves the whole path a contacts
// event travels — decode, UpsertContact with the JID-derived phone,
// persisted row, then found back by SearchContacts on that same number.
func TestHandleEventContactIngestMakesItFindableByPhoneSearch(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "messages.db"))
	if err != nil {
		t.Fatalf("store.Open() error: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	b, err := Open(context.Background(), t.TempDir(), st, mediapath.Roots{})
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	t.Cleanup(func() { _ = b.Close() })

	b.handleEvent(&events.Contact{
		JID:    types.NewJID("15551234567", types.DefaultUserServer),
		Action: &waSyncAction.ContactAction{FullName: proto.String("Alice Smith")},
	})

	rows, err := st.SearchContacts("15551234567", 0)
	if err != nil {
		t.Fatalf("SearchContacts: %v", err)
	}
	if len(rows) != 1 || rows[0].JID != "15551234567@s.whatsapp.net" {
		t.Fatalf("SearchContacts(15551234567) = %+v, want a single row for the ingested contact", rows)
	}
}

func TestHandleEventGroupInfoRenameUpsertsChat(t *testing.T) {
	b, fake := newTestBridge(t)

	b.handleEvent(&events.GroupInfo{
		JID:  types.NewJID("222", types.GroupServer),
		Name: &types.GroupName{Name: "Renamed Group"},
	})

	if len(fake.chats) != 1 || fake.chats[0].name != "Renamed Group" || !fake.chats[0].isGroup {
		t.Fatalf("chats = %+v, want one chat named Renamed Group", fake.chats)
	}
}

func TestHandleEventHistorySyncUpsertsConversationAndItsMessages(t *testing.T) {
	b, fake := newTestBridge(t)

	conv := &waHistorySync.Conversation{
		ID:                    proto.String("111@s.whatsapp.net"),
		Name:                  proto.String("Alice"),
		ConversationTimestamp: proto.Uint64(1700000200),
		Messages: []*waHistorySync.HistorySyncMsg{{
			Message: &waWeb.WebMessageInfo{
				Key: &waCommon.MessageKey{
					RemoteJID:   proto.String("111@s.whatsapp.net"),
					FromMe:      proto.Bool(false),
					ID:          proto.String("HMSG1"),
					Participant: proto.String("111@s.whatsapp.net"),
				},
				Message:          &waE2E.Message{Conversation: proto.String("history hello")},
				MessageTimestamp: proto.Uint64(1700000199),
				PushName:         proto.String("Alice"),
			},
		}},
	}

	b.handleEvent(&events.HistorySync{Data: &waHistorySync.HistorySync{
		Conversations: []*waHistorySync.Conversation{conv},
	}})

	if len(fake.messages) != 1 {
		t.Fatalf("messages = %+v, want one message ingested from history sync", fake.messages)
	}
	msg := fake.messages[0]
	if msg.ChatJID != "111@s.whatsapp.net" || msg.ID != "HMSG1" || msg.Text != "history hello" {
		t.Fatalf("message = %+v, want chat 111@s.whatsapp.net, id HMSG1, text \"history hello\"", msg)
	}

	found := false
	for _, c := range fake.chats {
		if c.jid == "111@s.whatsapp.net" && c.name == "Alice" && !c.isGroup {
			found = true
		}
	}
	if !found {
		t.Fatalf("chats = %+v, want an UpsertChat call for 111@s.whatsapp.net named Alice", fake.chats)
	}
}

func TestHandleEventHistorySyncSkipsUnparsableConversationID(t *testing.T) {
	b, fake := newTestBridge(t)

	conv := &waHistorySync.Conversation{ID: proto.String("")}

	b.handleEvent(&events.HistorySync{Data: &waHistorySync.HistorySync{
		Conversations: []*waHistorySync.Conversation{conv},
	}})

	if len(fake.chats) != 0 || len(fake.messages) != 0 {
		t.Fatalf("chats/messages = %+v/%+v, want none for an unparsable conversation ID", fake.chats, fake.messages)
	}
}

// lidGroupSource is a group message whose sender arrives as a privacy LID,
// with the phone JID carried alongside as the alternative address.
func lidGroupSource() types.MessageSource {
	return types.MessageSource{
		Chat:      types.NewJID("222", types.GroupServer),
		Sender:    types.NewJID("99566015803422", types.HiddenUserServer),
		SenderAlt: types.NewJID("444", types.DefaultUserServer),
		IsFromMe:  false,
		IsGroup:   true,
	}
}

func TestHandleEventMessageRecordsLIDMapping(t *testing.T) {
	b, fake := newTestBridge(t)

	b.handleEvent(messageEvent(lidGroupSource(), "Bobby", &waE2E.Message{Conversation: proto.String("hi")}))

	want := fakeLIDMappingCall{lid: "99566015803422@lid", pn: "444@s.whatsapp.net"}
	if len(fake.lidMappings) != 1 || fake.lidMappings[0] != want {
		t.Fatalf("lid mappings = %+v, want exactly %+v", fake.lidMappings, want)
	}
}

// The sender's push name must land on the phone-JID contact too (with its
// phone number), not only on the LID row: the phone identity is the one the
// app-state contact sync and search_contacts know.
func TestHandleEventMessageLIDSenderNamesPhoneContact(t *testing.T) {
	b, fake := newTestBridge(t)

	b.handleEvent(messageEvent(lidGroupSource(), "Bobby", &waE2E.Message{Conversation: proto.String("hi")}))

	var pnRow *fakeContactCall
	for i := range fake.contacts {
		if fake.contacts[i].jid == "444@s.whatsapp.net" {
			pnRow = &fake.contacts[i]
		}
	}
	if pnRow == nil {
		t.Fatalf("contacts = %+v, want an upsert for the phone JID 444@s.whatsapp.net", fake.contacts)
	}
	if pnRow.pushName != "Bobby" || pnRow.phone != "444" {
		t.Fatalf("phone contact = %+v, want pushName Bobby and phone 444", *pnRow)
	}
}

// When the addresses arrive the other way around (sender already the phone
// JID, LID as the alternative), the same mapping is recorded.
func TestHandleEventMessageLIDAltAlsoRecordsMapping(t *testing.T) {
	b, fake := newTestBridge(t)

	src := types.MessageSource{
		Chat:      types.NewJID("222", types.GroupServer),
		Sender:    types.NewJID("444", types.DefaultUserServer),
		SenderAlt: types.NewJID("99566015803422", types.HiddenUserServer),
		IsFromMe:  false,
		IsGroup:   true,
	}
	b.handleEvent(messageEvent(src, "Bobby", &waE2E.Message{Conversation: proto.String("hi")}))

	want := fakeLIDMappingCall{lid: "99566015803422@lid", pn: "444@s.whatsapp.net"}
	if len(fake.lidMappings) != 1 || fake.lidMappings[0] != want {
		t.Fatalf("lid mappings = %+v, want exactly %+v", fake.lidMappings, want)
	}
}

func TestHandleEventMessageWithoutAltRecordsNoMapping(t *testing.T) {
	b, fake := newTestBridge(t)

	b.handleEvent(messageEvent(groupSource(), "Carol", &waE2E.Message{Conversation: proto.String("hi")}))

	if len(fake.lidMappings) != 0 {
		t.Fatalf("lid mappings = %+v, want none for a message with no alternative address", fake.lidMappings)
	}
}

// --- kind decoding beyond the basics (issue #7: no more opaque "other") ---

func decodeKind(t *testing.T, msg *waE2E.Message) store.Message {
	t.Helper()
	m, _, _ := decodeMessage(messageEvent(groupSource(), "Carol", msg))
	return m
}

func TestDecodeMessageContactCard(t *testing.T) {
	m := decodeKind(t, &waE2E.Message{ContactMessage: &waE2E.ContactMessage{DisplayName: proto.String("Bob Vcard")}})
	if m.Kind != "contact" || m.Text != "Bob Vcard" {
		t.Fatalf("contact card decoded as kind=%q text=%q, want contact/Bob Vcard", m.Kind, m.Text)
	}
}

func TestDecodeMessageLocation(t *testing.T) {
	m := decodeKind(t, &waE2E.Message{LocationMessage: &waE2E.LocationMessage{
		Name: proto.String("Cafe X"), Address: proto.String("12 Road"),
	}})
	if m.Kind != "location" {
		t.Fatalf("location decoded as kind=%q, want location", m.Kind)
	}
	if !strings.Contains(m.Text, "Cafe X") || !strings.Contains(m.Text, "12 Road") {
		t.Fatalf("location text = %q, want it to carry name and address", m.Text)
	}
}

func TestDecodeMessagePollCreation(t *testing.T) {
	poll := &waE2E.PollCreationMessage{Name: proto.String("Lunch where?")}
	for name, msg := range map[string]*waE2E.Message{
		"v1": {PollCreationMessage: poll},
		"v3": {PollCreationMessageV3: poll},
	} {
		m := decodeKind(t, msg)
		if m.Kind != "poll" || m.Text != "Lunch where?" {
			t.Fatalf("%s poll decoded as kind=%q text=%q, want poll/Lunch where?", name, m.Kind, m.Text)
		}
	}
}

func TestDecodeMessageGroupInvite(t *testing.T) {
	m := decodeKind(t, &waE2E.Message{GroupInviteMessage: &waE2E.GroupInviteMessage{GroupName: proto.String("Fun Group")}})
	if m.Kind != "group_invite" || m.Text != "Fun Group" {
		t.Fatalf("group invite decoded as kind=%q text=%q, want group_invite/Fun Group", m.Kind, m.Text)
	}
}

func TestDecodeMessageVideoNote(t *testing.T) {
	m := decodeKind(t, &waE2E.Message{PtvMessage: &waE2E.VideoMessage{URL: proto.String("https://example.invalid/v")}})
	if m.Kind != "video_note" {
		t.Fatalf("ptv decoded as kind=%q, want video_note", m.Kind)
	}
	if len(m.MediaRef) == 0 {
		t.Fatal("video note stored without a MediaRef; download_media could not fetch it")
	}
}

func TestDecodeMessageProtocolIsSystem(t *testing.T) {
	m := decodeKind(t, &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{}})
	if m.Kind != "system" {
		t.Fatalf("protocol message decoded as kind=%q, want system", m.Kind)
	}
}

// A disappearing-chat message nests the real content inside an ephemeral
// wrapper; the wrapper must be peeled so the message reads as what it is,
// and the media reference must point at the inner message so download
// dispatch can find the attachment.
func TestDecodeMessageEphemeralUnwraps(t *testing.T) {
	inner := &waE2E.Message{ImageMessage: &waE2E.ImageMessage{Caption: proto.String("vanishing pic")}}
	m := decodeKind(t, &waE2E.Message{EphemeralMessage: &waE2E.FutureProofMessage{Message: inner}})
	if m.Kind != "image" || m.Text != "vanishing pic" {
		t.Fatalf("ephemeral image decoded as kind=%q text=%q, want image/vanishing pic", m.Kind, m.Text)
	}

	var ref waE2E.Message
	if err := proto.Unmarshal(m.MediaRef, &ref); err != nil {
		t.Fatalf("unmarshal media ref: %v", err)
	}
	if ref.GetImageMessage() == nil {
		t.Fatal("media ref does not carry the unwrapped ImageMessage; download dispatch would find nothing")
	}
}

func TestDecodeMessageViewOnceUnwraps(t *testing.T) {
	inner := &waE2E.Message{VideoMessage: &waE2E.VideoMessage{Caption: proto.String("view once")}}
	m := decodeKind(t, &waE2E.Message{ViewOnceMessageV2: &waE2E.FutureProofMessage{Message: inner}})
	if m.Kind != "video" || m.Text != "view once" {
		t.Fatalf("view-once video decoded as kind=%q text=%q, want video/view once", m.Kind, m.Text)
	}
}

// Types with no dedicated kind must still say what they are: the populated
// protobuf field's name becomes an "other:<subtype>" hint.
func TestDecodeMessageOtherCarriesSubtypeHint(t *testing.T) {
	m := decodeKind(t, &waE2E.Message{ButtonsMessage: &waE2E.ButtonsMessage{}})
	if m.Kind != "other:buttons" {
		t.Fatalf("buttons message decoded as kind=%q, want other:buttons", m.Kind)
	}
}

// The sender-key blob rides along with real content in group messages and
// must never be reported as the message's own type.
func TestDecodeMessageOtherSkipsSenderKeyDistribution(t *testing.T) {
	m := decodeKind(t, &waE2E.Message{
		SenderKeyDistributionMessage: &waE2E.SenderKeyDistributionMessage{},
		ButtonsMessage:               &waE2E.ButtonsMessage{},
	})
	if m.Kind != "other:buttons" {
		t.Fatalf("kind = %q, want other:buttons (the SKDM blob is transport, not content)", m.Kind)
	}
}

func TestDecodeMessageTrulyUnknownStaysPlainOther(t *testing.T) {
	m := decodeKind(t, &waE2E.Message{})
	if m.Kind != "other" {
		t.Fatalf("empty message decoded as kind=%q, want plain other", m.Kind)
	}
}

// Regression for the exact shape reported in issue #10: a document sent
// WITH a caption arrives wrapped in DocumentWithCaptionMessage and must
// decode with kind, caption, filename, and a media reference pointing at
// the inner DocumentMessage — not as a media-less "other" row.
func TestDecodeMessageCaptionedDocumentUnwraps(t *testing.T) {
	inner := &waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{
		Caption:  proto.String("Q3 report attached"),
		FileName: proto.String("report.pdf"),
	}}
	m := decodeKind(t, &waE2E.Message{DocumentWithCaptionMessage: &waE2E.FutureProofMessage{Message: inner}})

	if m.Kind != "document" || m.Text != "Q3 report attached" || m.MediaFilename != "report.pdf" {
		t.Fatalf("captioned document decoded as kind=%q text=%q filename=%q, want document/Q3 report attached/report.pdf",
			m.Kind, m.Text, m.MediaFilename)
	}

	var ref waE2E.Message
	if err := proto.Unmarshal(m.MediaRef, &ref); err != nil {
		t.Fatalf("unmarshal media ref: %v", err)
	}
	if ref.GetDocumentMessage() == nil {
		t.Fatal("media ref does not carry the unwrapped DocumentMessage; download_media would report no media")
	}
}
