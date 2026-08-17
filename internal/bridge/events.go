package bridge

import (
	"strings"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"github.com/idle-sync/whatsapp-connect-mcp/internal/store"
)

// handleEvent is the whatsmeow event handler registered by Open: a thin
// dispatcher that decodes each event it recognizes and hands the result to
// the ingest store. Event types it doesn't recognize are ignored. Ingest
// errors are dropped rather than surfaced: there is no caller to report
// them to here, and a redelivery of the same WhatsApp event (which
// whatsmeow does on reconnect for history) repairs a transient write
// failure via the idempotent upserts. It performs no network I/O itself:
// every case below only decodes evt and writes to the store.
func (b *Bridge) handleEvent(raw any) {
	switch evt := raw.(type) {
	case *events.Message:
		b.ingestMessage(evt)
	case *events.Receipt:
		b.ingestReceipt(evt)
	case *events.CallOffer:
		b.ingestCall(decodeCallOffer(evt))
	case *events.CallAccept:
		b.ingestCall(decodeCallAccept(evt))
	case *events.CallTerminate:
		b.ingestCall(decodeCallTerminate(evt))
	case *events.HistorySync:
		b.ingestHistorySync(evt)
	case *events.Contact:
		b.ingestContact(evt)
	case *events.PushName:
		b.ingestPushName(evt)
	case *events.GroupInfo:
		b.ingestGroupInfoName(evt)
	}
}

// ingestMessage decodes evt and writes it to the store. The chat row is
// always upserted, even when chatName comes back empty (an unknown group
// name, or a from-me DM where the peer's name isn't in this event):
// UpsertChat's name column only ever overwrites with a non-empty value, so
// this can't blank a name learned elsewhere, and the row (plus its
// last_message_at bump) has to exist regardless for the chat to show up in
// listings. Group names are never looked up live here — doing a GetGroupInfo
// IQ per message would serialize behind whatsmeow's single event-handling
// goroutine and stall all ingestion; they arrive instead via
// ingestGroupInfoName and ingestHistorySync below.
func (b *Bridge) ingestMessage(evt *events.Message) {
	m, chatName, isGroup := decodeMessage(evt)

	_ = b.store.UpsertChat(m.ChatJID, chatName, isGroup, m.TS)
	_ = b.store.UpsertMessage(m)

	if !m.FromMe && evt.Info.PushName != "" {
		_ = b.store.UpsertContact(m.SenderJID, phoneFromJID(m.SenderJID), evt.Info.PushName, "", "")
	}
}

func (b *Bridge) ingestReceipt(evt *events.Receipt) {
	chatJID, ids, readAt, ok := decodeReceipt(evt)
	if !ok {
		return
	}
	_ = b.store.MarkRead(chatJID, ids, readAt)
}

func (b *Bridge) ingestCall(id, peerJID string, ts int64, direction, status string, isVideo bool) {
	_ = b.store.InsertCall(id, peerJID, ts, direction, status, isVideo)
}

// ingestHistorySync writes the conversations and messages carried in a
// history-sync payload (delivered after pairing, and periodically after).
// Per-message decoding reuses ingestMessage/decodeMessage via
// Client.ParseWebMessage, which turns a waWeb.WebMessageInfo into the same
// *events.Message shape a live message arrives as; ParseWebMessage does
// only local parsing (JID parsing and a local device-store read for our
// own JID), never network I/O.
func (b *Bridge) ingestHistorySync(evt *events.HistorySync) {
	for _, conv := range evt.Data.GetConversations() {
		jid, name, isGroup, ok := decodeHistorySyncConversation(conv)
		if !ok {
			continue
		}
		_ = b.store.UpsertChat(jid, name, isGroup, int64(conv.GetConversationTimestamp())) // #nosec G115 -- WhatsApp timestamps fit int64 for the foreseeable future

		chatJID, err := types.ParseJID(jid)
		if err != nil {
			continue
		}
		for _, hsMsg := range conv.GetMessages() {
			webMsg := hsMsg.GetMessage()
			if webMsg == nil {
				continue
			}
			msgEvt, err := b.client.ParseWebMessage(chatJID, webMsg)
			if err != nil {
				continue
			}
			b.ingestMessage(msgEvt)
		}
	}
}

func (b *Bridge) ingestContact(evt *events.Contact) {
	jid, fullName, ok := decodeContact(evt)
	if !ok {
		return
	}
	_ = b.store.UpsertContact(jid, phoneFromJID(jid), "", fullName, "")
}

func (b *Bridge) ingestPushName(evt *events.PushName) {
	jid, pushName := decodePushName(evt)
	if pushName == "" {
		return
	}
	_ = b.store.UpsertContact(jid, phoneFromJID(jid), pushName, "", "")
}

func (b *Bridge) ingestGroupInfoName(evt *events.GroupInfo) {
	jid, name, ok := decodeGroupInfoName(evt)
	if !ok {
		return
	}
	_ = b.store.UpsertChat(jid, name, true, evt.Timestamp.Unix())
}

// decodeMessage is pure: it turns a whatsmeow message event into the
// store.Message row to upsert, plus the chat's display name (empty if
// unknown from this event alone) and whether the chat is a group. An empty
// chatName is a normal result, not a failure: see ingestMessage for how the
// store treats it.
//
// chatName is only ever derived for a direct chat from the sender's push
// name (never from our own, since IsFromMe messages don't tell us the
// peer's name); group names aren't carried on individual messages at all —
// see ingestGroupInfoName and ingestHistorySync for where those come from.
func decodeMessage(evt *events.Message) (m store.Message, chatName string, isGroup bool) {
	info := evt.Info
	m.ChatJID = info.Chat.String()
	m.SenderJID = info.Sender.String()
	m.ID = info.ID
	m.FromMe = info.IsFromMe
	m.TS = info.Timestamp.Unix()

	isGroup = info.IsGroup
	if !isGroup && !info.IsFromMe {
		chatName = info.PushName
	}

	msg := evt.Message
	switch {
	case msg.GetConversation() != "":
		m.Kind = "text"
		m.Text = msg.GetConversation()
	case msg.GetExtendedTextMessage() != nil:
		et := msg.GetExtendedTextMessage()
		m.Kind = "text"
		m.Text = et.GetText()
		m.QuotedID = et.GetContextInfo().GetStanzaID()
	case msg.GetImageMessage() != nil:
		im := msg.GetImageMessage()
		m.Kind = "image"
		m.Text = im.GetCaption()
		m.QuotedID = im.GetContextInfo().GetStanzaID()
		m.MediaRef = marshalMediaRef(msg)
	case msg.GetVideoMessage() != nil:
		vm := msg.GetVideoMessage()
		m.Kind = "video"
		m.Text = vm.GetCaption()
		m.QuotedID = vm.GetContextInfo().GetStanzaID()
		m.MediaRef = marshalMediaRef(msg)
	case msg.GetAudioMessage() != nil:
		am := msg.GetAudioMessage()
		if am.GetPTT() {
			m.Kind = "voice"
		} else {
			m.Kind = "audio"
		}
		m.QuotedID = am.GetContextInfo().GetStanzaID()
		m.MediaRef = marshalMediaRef(msg)
	case msg.GetDocumentMessage() != nil:
		dm := msg.GetDocumentMessage()
		m.Kind = "document"
		m.Text = dm.GetCaption()
		m.QuotedID = dm.GetContextInfo().GetStanzaID()
		m.MediaFilename = dm.GetFileName()
		m.MediaRef = marshalMediaRef(msg)
	case msg.GetStickerMessage() != nil:
		sm := msg.GetStickerMessage()
		m.Kind = "sticker"
		m.QuotedID = sm.GetContextInfo().GetStanzaID()
		m.MediaRef = marshalMediaRef(msg)
	case msg.GetReactionMessage() != nil:
		rm := msg.GetReactionMessage()
		m.Kind = "reaction"
		m.Text = rm.GetText()
		m.QuotedID = rm.GetKey().GetID()
	default:
		m.Kind = "other"
	}

	return m, chatName, isGroup
}

// marshalMediaRef serializes msg for later use with Bridge.DownloadMedia.
// It returns nil (rather than an error) on a marshal failure, since the
// message row is still valid without a working media reference and
// decodeMessage has no error return.
func marshalMediaRef(msg *waE2E.Message) []byte {
	data, err := proto.Marshal(msg)
	if err != nil {
		return nil
	}
	return data
}

// decodeReceipt extracts a read-receipt update from evt. ok is false for
// receipt types that don't represent the user having read a message
// (delivered, retry, and so on), which callers should ignore.
func decodeReceipt(evt *events.Receipt) (chatJID string, ids []string, readAt int64, ok bool) {
	if evt.Type != types.ReceiptTypeRead && evt.Type != types.ReceiptTypeReadSelf {
		return "", nil, 0, false
	}
	return evt.Chat.String(), evt.MessageIDs, evt.Timestamp.Unix(), true
}

// decodeCallOffer, decodeCallAccept, and decodeCallTerminate extract an
// InsertCall record from the corresponding whatsmeow call event. whatsmeow
// only reports calls placed to this account, so direction is always
// "incoming". isVideo is always false: neither event exposes a typed field
// for it, and the raw offer node's video indicator is undocumented — a
// fixed false is a known-wrong guess in the audio case, but a better one
// isn't available without depending on undocumented protocol internals.

func decodeCallOffer(evt *events.CallOffer) (id, peerJID string, ts int64, direction, status string, isVideo bool) {
	return evt.CallID, evt.From.String(), evt.Timestamp.Unix(), "incoming", "ringing", false
}

func decodeCallAccept(evt *events.CallAccept) (id, peerJID string, ts int64, direction, status string, isVideo bool) {
	return evt.CallID, evt.From.String(), evt.Timestamp.Unix(), "incoming", "answered", false
}

func decodeCallTerminate(evt *events.CallTerminate) (id, peerJID string, ts int64, direction, status string, isVideo bool) {
	return evt.CallID, evt.From.String(), evt.Timestamp.Unix(), "incoming", callStatus(evt.Reason), false
}

// callStatus maps a CallTerminate reason to the store's status enum. An
// answered call that ends normally also arrives here (whatsmeow doesn't
// distinguish "ended" from "never answered" in this event), and its reason
// string isn't one of the recognized ones below, so it falls through to
// "unknown" and overwrites whatever InsertCall recorded from CallAccept.
// The status column holds only the most recent report, not a history, so a
// normally-ended call reads as "unknown" rather than "answered" — a call
// row is a last-known-state snapshot, not a call log entry.
func callStatus(reason string) string {
	switch strings.ToLower(reason) {
	case "timeout":
		return "missed"
	case "reject", "decline", "busy":
		return "rejected"
	default:
		return "unknown"
	}
}

// decodeHistorySyncConversation is pure: it extracts the chat identity from
// one HistorySync conversation record. ok is false when the conversation's
// ID isn't a parseable, non-empty JID.
func decodeHistorySyncConversation(conv *waHistorySync.Conversation) (jid, name string, isGroup, ok bool) {
	parsed, err := types.ParseJID(conv.GetID())
	if err != nil || parsed.User == "" {
		return "", "", false, false
	}
	return parsed.String(), conv.GetName(), parsed.Server == types.GroupServer, true
}

// decodeContact is pure: it extracts a full-name contact update from an
// app-state Contact sync event. ok is false when the event carries no full
// name (whatsmeow's ContactAction has no field for phone or business name;
// those come from the message-driven push-name path and from Business
// vCards respectively, neither of which is this event).
func decodeContact(evt *events.Contact) (jid, fullName string, ok bool) {
	fullName = evt.Action.GetFullName()
	if fullName == "" {
		return "", "", false
	}
	return evt.JID.String(), fullName, true
}

// decodePushName is pure: it extracts the updated push name from a
// PushName event. Callers should ignore a returned empty pushName.
func decodePushName(evt *events.PushName) (jid, pushName string) {
	return evt.JID.String(), evt.NewPushName
}

// phoneFromJID derives a phone number from jid's local part when it looks
// like one — e.g. "15551234567@s.whatsapp.net" -> "15551234567". It returns
// "" for anything else a contact upsert's jid argument can be: a JID that
// fails to parse, one on a server other than the personal-account server
// (a group JID's local part is a group id, numeric but not a phone number),
// or a local part containing anything but digits. UpsertContact's
// empty-never-overwrites rule means returning "" here only ever leaves
// whatever phone (if any) is already on file, never blanks a better one.
func phoneFromJID(jid string) string {
	parsed, err := types.ParseJID(jid)
	if err != nil || parsed.User == "" || parsed.Server != types.DefaultUserServer {
		return ""
	}
	for _, r := range parsed.User {
		if r < '0' || r > '9' {
			return ""
		}
	}
	return parsed.User
}

// decodeGroupInfoName is pure: it extracts a group rename from a GroupInfo
// event. ok is false when this particular metadata change wasn't a rename
// (GroupInfo also carries topic, membership, and other changes this bridge
// doesn't track).
func decodeGroupInfoName(evt *events.GroupInfo) (jid, name string, ok bool) {
	if evt.Name == nil || evt.Name.Name == "" {
		return "", "", false
	}
	return evt.JID.String(), evt.Name.Name, true
}
