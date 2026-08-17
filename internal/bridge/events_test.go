package bridge

import (
	"context"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

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
	chats    []fakeChatCall
	messages []store.Message
	contacts []fakeContactCall
	reads    []fakeReadCall
	calls    []fakeCallCall
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
	fake := &fakeIngest{}
	b, err := Open(context.Background(), t.TempDir(), fake)
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
	if len(fake.contacts) != 1 || fake.contacts[0].pushName != "Alice" {
		t.Fatalf("contacts = %+v, want one contact with pushName Alice", fake.contacts)
	}
}

func TestHandleEventGroupMessageSkipsChatUpsertWhenNameUnknown(t *testing.T) {
	b, fake := newTestBridge(t)
	evt := messageEvent(groupSource(), "Bob", &waE2E.Message{
		Conversation: proto.String("hi group"),
	})

	b.handleEvent(evt)

	if len(fake.messages) != 1 {
		t.Fatalf("messages = %+v, want one message even though the chat name is unknown", fake.messages)
	}
	if len(fake.chats) != 0 {
		t.Fatalf("chats = %+v, want no UpsertChat call: the live group-name lookup fails fast (client is unconnected) and an empty name must never overwrite a real one", fake.chats)
	}
	// The sender's push name is still worth recording as contact info,
	// independent of whether the group's own name is known.
	if len(fake.contacts) != 1 || fake.contacts[0].pushName != "Bob" {
		t.Fatalf("contacts = %+v, want one contact with pushName Bob", fake.contacts)
	}
}

func TestHandleEventFromMeMessageSkipsContactUpsert(t *testing.T) {
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
	if len(fake.chats) != 0 {
		t.Fatalf("chats = %+v, want none: from-me events don't tell us the peer's name", fake.chats)
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
