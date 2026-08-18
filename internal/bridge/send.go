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
	default:
		return nil
	}
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
	case "read":
		return b.deliverRead(ctx, d)
	default:
		return "", fmt.Errorf("unknown delivery kind %q", d.Kind)
	}
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

	resp, err := b.client.SendMessage(ctx, to, build(up))
	if err != nil {
		return "", waErr("send media", err)
	}
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
