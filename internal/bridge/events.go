package bridge

import (
	"strings"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"github.com/idle-sync/whatsapp-connect-mcp/internal/store"
)

// handleEvent is the whatsmeow event handler registered by Connect: a thin
// dispatcher that decodes each event it recognizes and hands the result to
// the ingest store. Event types it doesn't recognize are ignored. Ingest
// errors are dropped rather than surfaced: there is no caller to report
// them to here, and a redelivery of the same WhatsApp event (which
// whatsmeow does on reconnect for history) repairs a transient write
// failure via the idempotent upserts.
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
	}
}

// ingestMessage decodes evt and writes it to the store. Group chats whose
// name decodeMessage could not determine from the message alone are
// resolved with a live (whatsmeow-cached) group info lookup; if that also
// fails, the chat's name is left untouched rather than overwritten with an
// empty string.
func (b *Bridge) ingestMessage(evt *events.Message) {
	m, chatName, isGroup := decodeMessage(evt)

	if chatName == "" && isGroup {
		if info, err := b.client.GetGroupInfo(b.client.BackgroundEventCtx, evt.Info.Chat); err == nil && info.Name != "" {
			chatName = info.Name
		}
	}
	if chatName != "" {
		_ = b.store.UpsertChat(m.ChatJID, chatName, isGroup, m.TS)
	}

	_ = b.store.UpsertMessage(m)

	if !m.FromMe && evt.Info.PushName != "" {
		_ = b.store.UpsertContact(m.SenderJID, "", evt.Info.PushName, "", "")
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

// decodeMessage is pure: it turns a whatsmeow message event into the
// store.Message row to upsert, plus the chat's display name (empty if
// unknown from this event alone) and whether the chat is a group.
//
// chatName is only ever derived for a direct chat from the sender's push
// name (never from our own, since IsFromMe messages don't tell us the
// peer's name); group names aren't carried on individual messages at all.
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
// for it, and the raw offer node's video indicator is undocumented, so
// guessing at it was judged worse than a fixed false (see task report).

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
// That's an accepted limitation of a status column that only ever holds
// the most recent report (see task report).
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
