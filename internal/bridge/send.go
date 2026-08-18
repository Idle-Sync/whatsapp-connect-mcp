package bridge

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"github.com/idle-sync/whatsapp-connect-mcp/internal/gate"
)

// errVoiceFormat is returned by Deliver for a "voice" delivery whose Path
// doesn't look like an Ogg Opus file. whatsapp-connect-mcp v1 does no
// transcoding, so the input must already be in that format.
var errVoiceFormat = errors.New("voice notes must be Ogg Opus (.ogg)")

// Validate reports whether d could be delivered, without delivering it or
// touching the network. gate.Gate calls it before minting a draft, so a
// send naming an unreadable file is refused on the first call rather than
// after a preview has been approved.
//
// Deliver re-checks the same thing at the point of reading. That is not
// redundant: this method's guarantee must not depend on a caller having
// remembered to ask.
func (b *Bridge) Validate(d gate.Delivery) error {
	switch d.Kind {
	case "media", "voice":
		return b.mediaRoots.Allows(d.Path)
	case "poll":
		return validatePoll(d)
	default:
		return nil
	}
}

// validatePoll rejects a poll that could never be created, so it is refused
// before a draft is minted rather than after a preview is approved.
func validatePoll(d gate.Delivery) error {
	if strings.TrimSpace(d.Text) == "" {
		return errors.New("poll requires a question")
	}
	if len(d.Options) < 2 {
		return errors.New("poll requires at least two options")
	}
	if d.SelectableCount > len(d.Options) {
		return errors.New("poll allows choosing more options than it offers")
	}
	return nil
}

// Deliver performs the outbound WhatsApp action described by d. It is the
// only method that reaches the wire; gate.Gate is the sole caller, so every
// send in the process passes through the gate's draft and rate-limit
// policy before reaching here. Errors are category-only (see waErr):
// gate wraps and returns them to the calling model verbatim.
func (b *Bridge) Deliver(ctx context.Context, d gate.Delivery) (string, error) {
	switch d.Kind {
	case "text":
		return b.deliverText(ctx, d)
	case "media":
		return b.deliverMedia(ctx, d)
	case "voice":
		return b.deliverVoice(ctx, d)
	case "reaction":
		return b.deliverReaction(ctx, d)
	case "edit":
		return b.deliverEdit(ctx, d)
	case "revoke":
		return b.deliverRevoke(ctx, d)
	case "poll":
		return b.deliverPoll(ctx, d)
	case "block":
		return b.deliverBlocklist(ctx, d, events.BlocklistChangeActionBlock)
	case "unblock":
		return b.deliverBlocklist(ctx, d, events.BlocklistChangeActionUnblock)
	case "read":
		return b.deliverRead(ctx, d)
	default:
		return "", fmt.Errorf("unknown delivery kind %q", d.Kind)
	}
}

// deliverBlocklist blocks or unblocks a contact. There is no message id for
// a block-list change, so the returned id is empty; the gate reports the
// committed preview, which already names the action and the contact.
func (b *Bridge) deliverBlocklist(ctx context.Context, d gate.Delivery, action events.BlocklistChangeAction) (string, error) {
	jid, err := parseRecipient(d.To)
	if err != nil {
		return "", err
	}
	if _, err := b.client.UpdateBlocklist(ctx, jid, action); err != nil {
		return "", waErr("update block list", err)
	}
	return "", nil
}

// deliverPoll creates a poll message in the chat. It re-validates rather
// than trusting Validate to have run, for the same reason the media path is
// re-checked at delivery: the guarantee must not depend on a caller.
func (b *Bridge) deliverPoll(ctx context.Context, d gate.Delivery) (string, error) {
	if err := validatePoll(d); err != nil {
		return "", err
	}
	to, err := parseRecipient(d.To)
	if err != nil {
		return "", err
	}

	selectable := d.SelectableCount
	if selectable < 1 {
		selectable = 1
	}

	msg := b.client.BuildPollCreation(d.Text, d.Options, selectable)
	resp, err := b.client.SendMessage(ctx, to, msg)
	if err != nil {
		return "", waErr("create poll", err)
	}
	b.recordOutbound(to, resp.ID, resp.Timestamp, msg)
	return resp.ID, nil
}

// deliverEdit replaces the text of an already-sent message. WhatsApp only
// permits editing your own messages, and only within a fixed window after
// sending; both are enforced by WhatsApp and surface here as a send error.
func (b *Bridge) deliverEdit(ctx context.Context, d gate.Delivery) (string, error) {
	to, err := parseRecipient(d.To)
	if err != nil {
		return "", err
	}
	if d.QuotedID == "" {
		return "", errors.New("edit requires a target message id")
	}

	newContent := &waE2E.Message{Conversation: proto.String(d.Text)}
	resp, err := b.client.SendMessage(ctx, to, b.client.BuildEdit(to, d.QuotedID, newContent))
	if err != nil {
		return "", waErr("edit message", err)
	}
	return resp.ID, nil
}

// deliverRevoke deletes a message for everyone in the chat. d.Author is the
// target message's sender: for your own message it is your own JID, which
// whatsmeow accepts; for someone else's it is that sender's JID, and the
// delete only succeeds if you are a group admin. WhatsApp enforces that and
// reports a failure here as a send error.
func (b *Bridge) deliverRevoke(ctx context.Context, d gate.Delivery) (string, error) {
	to, err := parseRecipient(d.To)
	if err != nil {
		return "", err
	}
	if d.QuotedID == "" {
		return "", errors.New("delete requires a target message id")
	}

	sender := types.EmptyJID
	if d.Author != "" {
		sender, err = parseRecipient(d.Author)
		if err != nil {
			return "", err
		}
	}

	resp, err := b.client.SendMessage(ctx, to, b.client.BuildRevoke(to, sender, d.QuotedID))
	if err != nil {
		return "", waErr("delete message", err)
	}
	return resp.ID, nil
}

func (b *Bridge) deliverText(ctx context.Context, d gate.Delivery) (string, error) {
	to, err := parseRecipient(d.To)
	if err != nil {
		return "", err
	}

	msg := &waE2E.Message{}
	if d.QuotedID != "" {
		// ContextInfo has no Participant or QuotedMessage here: gate.Delivery
		// only carries the quoted message's id, not its author or content, so
		// the reply thread linkage (StanzaID) is best-effort and the quote
		// preview card may not render on the recipient's client, which
		// expects Participant and QuotedMessage alongside StanzaID to show
		// one. The message still sends and still threads by id either way.
		msg.ExtendedTextMessage = &waE2E.ExtendedTextMessage{
			Text:        proto.String(d.Text),
			ContextInfo: &waE2E.ContextInfo{StanzaID: proto.String(d.QuotedID)},
		}
	} else {
		msg.Conversation = proto.String(d.Text)
	}

	resp, err := b.client.SendMessage(ctx, to, msg)
	if err != nil {
		return "", waErr("send message", err)
	}
	b.recordOutbound(to, resp.ID, resp.Timestamp, msg)
	return resp.ID, nil
}

func (b *Bridge) deliverMedia(ctx context.Context, d gate.Delivery) (string, error) {
	to, err := parseRecipient(d.To)
	if err != nil {
		return "", err
	}

	if err := b.mediaRoots.Allows(d.Path); err != nil {
		return "", err
	}

	data, mimetype, err := readFile(d.Path)
	if err != nil {
		return "", err
	}

	switch {
	case strings.HasPrefix(mimetype, "image/"):
		return b.uploadAndSend(ctx, to, whatsmeow.MediaImage, data, func(u whatsmeow.UploadResponse) *waE2E.Message {
			return &waE2E.Message{ImageMessage: &waE2E.ImageMessage{
				Caption: optionalString(d.Text), Mimetype: proto.String(mimetype),
				URL: proto.String(u.URL), DirectPath: proto.String(u.DirectPath),
				MediaKey: u.MediaKey, FileEncSHA256: u.FileEncSHA256, FileSHA256: u.FileSHA256,
				FileLength: proto.Uint64(u.FileLength),
			}}
		})
	case strings.HasPrefix(mimetype, "video/"):
		return b.uploadAndSend(ctx, to, whatsmeow.MediaVideo, data, func(u whatsmeow.UploadResponse) *waE2E.Message {
			return &waE2E.Message{VideoMessage: &waE2E.VideoMessage{
				Caption: optionalString(d.Text), Mimetype: proto.String(mimetype),
				URL: proto.String(u.URL), DirectPath: proto.String(u.DirectPath),
				MediaKey: u.MediaKey, FileEncSHA256: u.FileEncSHA256, FileSHA256: u.FileSHA256,
				FileLength: proto.Uint64(u.FileLength),
			}}
		})
	default:
		return b.uploadAndSend(ctx, to, whatsmeow.MediaDocument, data, func(u whatsmeow.UploadResponse) *waE2E.Message {
			return &waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{
				Caption: optionalString(d.Text), Mimetype: proto.String(mimetype),
				FileName: proto.String(filepath.Base(d.Path)),
				URL:      proto.String(u.URL), DirectPath: proto.String(u.DirectPath),
				MediaKey: u.MediaKey, FileEncSHA256: u.FileEncSHA256, FileSHA256: u.FileSHA256,
				FileLength: proto.Uint64(u.FileLength),
			}}
		})
	}
}

func (b *Bridge) deliverVoice(ctx context.Context, d gate.Delivery) (string, error) {
	if !strings.EqualFold(filepath.Ext(d.Path), ".ogg") {
		return "", errVoiceFormat
	}

	to, err := parseRecipient(d.To)
	if err != nil {
		return "", err
	}

	if err := b.mediaRoots.Allows(d.Path); err != nil {
		return "", err
	}

	data, err := os.ReadFile(d.Path) // #nosec G304 -- confined to mediaRoots immediately above
	if err != nil {
		return "", fileErr("read voice file", err)
	}
	if len(data) < 4 || string(data[:4]) != "OggS" {
		return "", errVoiceFormat
	}

	return b.uploadAndSend(ctx, to, whatsmeow.MediaAudio, data, func(u whatsmeow.UploadResponse) *waE2E.Message {
		return &waE2E.Message{AudioMessage: &waE2E.AudioMessage{
			PTT: proto.Bool(true), Mimetype: proto.String("audio/ogg; codecs=opus"),
			URL: proto.String(u.URL), DirectPath: proto.String(u.DirectPath),
			MediaKey: u.MediaKey, FileEncSHA256: u.FileEncSHA256, FileSHA256: u.FileSHA256,
			FileLength: proto.Uint64(u.FileLength),
		}}
	})
}

func (b *Bridge) deliverReaction(ctx context.Context, d gate.Delivery) (string, error) {
	to, err := parseRecipient(d.To)
	if err != nil {
		return "", err
	}
	if d.QuotedID == "" {
		return "", errors.New("reaction requires a target message id")
	}
	if d.Author == "" {
		return "", errors.New("reaction requires the target message author")
	}
	author, err := parseRecipient(d.Author)
	if err != nil {
		return "", err
	}

	msg := b.client.BuildReaction(to, author, d.QuotedID, d.Text)
	resp, err := b.client.SendMessage(ctx, to, msg)
	if err != nil {
		return "", waErr("send reaction", err)
	}
	b.recordOutbound(to, resp.ID, resp.Timestamp, msg)
	return resp.ID, nil
}

func (b *Bridge) deliverRead(ctx context.Context, d gate.Delivery) (string, error) {
	to, err := parseRecipient(d.To)
	if err != nil {
		return "", err
	}
	if len(d.MessageIDs) == 0 {
		return "", errors.New("mark_read requires at least one message id")
	}
	if d.Author == "" {
		return "", errors.New("read receipt requires the sender")
	}
	sender, err := parseRecipient(d.Author)
	if err != nil {
		return "", err
	}

	now := time.Now()
	if err := b.client.MarkRead(ctx, d.MessageIDs, now, to, sender); err != nil {
		return "", waErr("mark read", err)
	}
	if err := b.store.MarkRead(d.To, d.MessageIDs, now.Unix()); err != nil {
		return "", fmt.Errorf("update local read state: %w", err)
	}
	return "", nil
}

// recordOutbound writes a just-sent message into the local store through
// the same decode path (ingestMessage/decodeMessage) an inbound copy would
// take, so a sent message shows up in list_messages, search_messages, and
// get_message_context — and an outbound media message keeps a working
// media reference for download_media — exactly like a received one.
// whatsmeow does not echo this client's own sends back as events, so
// without this the outbound half of every conversation would be missing
// from the store. Called only after SendMessage succeeds: a failed send
// stores nothing.
func (b *Bridge) recordOutbound(to types.JID, id string, ts time.Time, msg *waE2E.Message) {
	sender := types.EmptyJID
	if own := b.client.Store.ID; own != nil {
		sender = own.ToNonAD()
	}
	b.ingestMessage(&events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{
				Chat:     to,
				Sender:   sender,
				IsFromMe: true,
				IsGroup:  to.Server == types.GroupServer,
			},
			ID:        id,
			Timestamp: ts,
		},
		Message: msg,
	})
}

// uploadAndSend uploads data as mediaType, builds the outbound message from
// the upload response via build, and sends it to.
func (b *Bridge) uploadAndSend(
	ctx context.Context, to types.JID, mediaType whatsmeow.MediaType, data []byte,
	build func(whatsmeow.UploadResponse) *waE2E.Message,
) (string, error) {
	up, err := b.client.Upload(ctx, data, mediaType)
	if err != nil {
		return "", waErr("upload media", err)
	}

	msg := build(up)
	resp, err := b.client.SendMessage(ctx, to, msg)
	if err != nil {
		return "", waErr("send media", err)
	}
	b.recordOutbound(to, resp.ID, resp.Timestamp, msg)
	return resp.ID, nil
}

// readFile reads path and determines its MIME type, preferring the
// extension (deterministic) and falling back to content sniffing.
func readFile(path string) (data []byte, mimetype string, err error) {
	data, err = os.ReadFile(path) // #nosec G304 -- callers confine path to the bridge's mediaRoots first
	if err != nil {
		return nil, "", fileErr("read media file", err)
	}

	mimetype = mime.TypeByExtension(filepath.Ext(path))
	if mimetype == "" {
		mimetype = http.DetectContentType(data)
	}
	if i := strings.IndexByte(mimetype, ';'); i >= 0 {
		mimetype = mimetype[:i]
	}
	return data, strings.TrimSpace(mimetype), nil
}

func optionalString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
