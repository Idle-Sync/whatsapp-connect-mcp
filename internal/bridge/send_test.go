package bridge

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"

	"github.com/idle-sync/whatsapp-connect-mcp/internal/gate"
	"github.com/idle-sync/whatsapp-connect-mcp/internal/mediapath"
)

// These tests cover only the validation performed by Deliver before it
// would touch the network (invalid input, missing required fields): the
// happy paths call whatsmeow's SendMessage/MarkRead/Upload, which need a
// live, paired connection this test suite has no way to provide and are
// instead exercised manually against a real account before release.

func TestDeliverUnknownKindErrors(t *testing.T) {
	b, _ := newTestBridge(t)

	_, err := b.Deliver(context.Background(), gate.Delivery{Kind: "bogus", To: "111@s.whatsapp.net"})

	if err == nil {
		t.Fatal("Deliver() error = nil, want error for an unrecognized kind")
	}
}

func TestDeliverInvalidRecipientErrors(t *testing.T) {
	b, _ := newTestBridge(t)

	_, err := b.Deliver(context.Background(), gate.Delivery{Kind: "text", To: "", Text: "hi"})

	if err == nil {
		t.Fatal("Deliver() error = nil, want error for an empty recipient")
	}
	if strings.Contains(err.Error(), "@") {
		t.Fatalf("Deliver() error = %q, must not echo the invalid recipient value", err.Error())
	}
}

func TestDeliverBlockRejectsInvalidRecipient(t *testing.T) {
	b, _ := newTestBridge(t)

	for _, kind := range []string{"block", "unblock"} {
		_, err := b.Deliver(context.Background(), gate.Delivery{Kind: kind, To: "not a jid"})
		if err == nil {
			t.Fatalf("Deliver(%s) error = nil, want an error for an invalid recipient", kind)
		}
		if strings.Contains(err.Error(), "not a jid") {
			t.Fatalf("Deliver(%s) error = %q, must not echo the recipient value", kind, err.Error())
		}
	}
}

func TestDeliverPollValidation(t *testing.T) {
	b, _ := newTestBridge(t)

	for _, tc := range []struct {
		name string
		d    gate.Delivery
		want string
	}{
		{"no question", gate.Delivery{Kind: "poll", To: "111@s.whatsapp.net", Options: []string{"a", "b"}}, "question"},
		{"one option", gate.Delivery{Kind: "poll", To: "111@s.whatsapp.net", Text: "Q?", Options: []string{"a"}}, "two options"},
		{"selectable exceeds options", gate.Delivery{
			Kind: "poll", To: "111@s.whatsapp.net", Text: "Q?", Options: []string{"a", "b"}, SelectableCount: 3,
		}, "more options than it offers"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := b.Deliver(context.Background(), tc.d)
			if err == nil {
				t.Fatalf("Deliver(%s) error = nil, want a validation error", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Deliver(%s) error = %q, want it to mention %q", tc.name, err.Error(), tc.want)
			}
		})
	}
}

func TestDeliverEditRequiresTargetID(t *testing.T) {
	b, _ := newTestBridge(t)

	_, err := b.Deliver(context.Background(), gate.Delivery{
		Kind: "edit", To: "111@s.whatsapp.net", Text: "fixed text",
	})
	if err == nil {
		t.Fatal("Deliver(edit) error = nil, want error when the target message id is empty")
	}
	if !strings.Contains(err.Error(), "target message id") {
		t.Fatalf("Deliver(edit) error = %q, want it to name the missing target message id", err.Error())
	}
}

func TestDeliverRevokeRequiresTargetID(t *testing.T) {
	b, _ := newTestBridge(t)

	_, err := b.Deliver(context.Background(), gate.Delivery{
		Kind: "revoke", To: "111@s.whatsapp.net", Author: "111@s.whatsapp.net",
	})
	if err == nil {
		t.Fatal("Deliver(revoke) error = nil, want error when the target message id is empty")
	}
	if !strings.Contains(err.Error(), "target message id") {
		t.Fatalf("Deliver(revoke) error = %q, want it to name the missing target message id", err.Error())
	}
}

// TestDeliverRevokeRejectsUnparsableAuthor guards the admin-delete path: a
// non-empty but malformed Author must fail without echoing the value, since
// an empty Author is the legitimate own-message case and must not be the
// only path that is validated.
func TestDeliverRevokeRejectsUnparsableAuthor(t *testing.T) {
	b, _ := newTestBridge(t)

	_, err := b.Deliver(context.Background(), gate.Delivery{
		Kind: "revoke", To: "111@s.whatsapp.net", QuotedID: "MSG1", Author: "not a jid",
	})
	if err == nil {
		t.Fatal("Deliver(revoke) error = nil, want error for an unparsable author")
	}
	if strings.Contains(err.Error(), "not a jid") {
		t.Fatalf("Deliver(revoke) error = %q, must not echo the invalid author", err.Error())
	}
}

func TestDeliverReactionRequiresQuotedID(t *testing.T) {
	b, _ := newTestBridge(t)

	_, err := b.Deliver(context.Background(), gate.Delivery{
		Kind: "reaction", To: "111@s.whatsapp.net", Text: "👍", Author: "111@s.whatsapp.net",
	})

	if err == nil {
		t.Fatal("Deliver() error = nil, want error when QuotedID is empty")
	}
	if !strings.Contains(err.Error(), "target message id") {
		t.Fatalf("Deliver() error = %q, want it to mention the missing target message id", err.Error())
	}
}

func TestDeliverReactionRequiresAuthor(t *testing.T) {
	b, _ := newTestBridge(t)

	_, err := b.Deliver(context.Background(), gate.Delivery{
		Kind: "reaction", To: "111@s.whatsapp.net", Text: "👍", QuotedID: "MSG1",
	})

	if err == nil {
		t.Fatal("Deliver() error = nil, want error when Author is empty")
	}
	if !strings.Contains(err.Error(), "target message author") {
		t.Fatalf("Deliver() error = %q, want it to mention the missing target message author", err.Error())
	}
}

func TestDeliverReactionRejectsUnparsableAuthor(t *testing.T) {
	b, _ := newTestBridge(t)

	_, err := b.Deliver(context.Background(), gate.Delivery{
		Kind: "reaction", To: "111@s.whatsapp.net", Text: "👍", QuotedID: "MSG1", Author: "not-a-real-jid:::",
	})

	if err == nil {
		t.Fatal("Deliver() error = nil, want error for an unparsable Author JID")
	}
}

func TestDeliverReadRequiresMessageIDs(t *testing.T) {
	b, _ := newTestBridge(t)

	_, err := b.Deliver(context.Background(), gate.Delivery{
		Kind: "read", To: "111@s.whatsapp.net", Author: "111@s.whatsapp.net",
	})

	if err == nil {
		t.Fatal("Deliver() error = nil, want error when MessageIDs is empty")
	}
	if !strings.Contains(err.Error(), "message id") {
		t.Fatalf("Deliver() error = %q, want it to mention the missing message id", err.Error())
	}
}

func TestDeliverReadRequiresAuthor(t *testing.T) {
	b, _ := newTestBridge(t)

	_, err := b.Deliver(context.Background(), gate.Delivery{
		Kind: "read", To: "111@s.whatsapp.net", MessageIDs: []string{"MSG1"},
	})

	if err == nil {
		t.Fatal("Deliver() error = nil, want error when Author is empty")
	}
	if !strings.Contains(err.Error(), "sender") {
		t.Fatalf("Deliver() error = %q, want it to mention the missing sender", err.Error())
	}
}

func TestDeliverVoiceRejectsNonOggExtension(t *testing.T) {
	b, _ := newTestBridge(t)

	_, err := b.Deliver(context.Background(), gate.Delivery{
		Kind: "voice", To: "111@s.whatsapp.net", Path: filepath.Join(t.TempDir(), "note.mp3"),
	})

	if err == nil || err != errVoiceFormat {
		t.Fatalf("Deliver() error = %v, want errVoiceFormat", err)
	}
}

// TestValidateConfinesMediaToRoots covers the check gate.Gate relies on to
// refuse a send before drafting it.
func TestValidateConfinesMediaToRoots(t *testing.T) {
	allowed := t.TempDir()
	forbidden := t.TempDir()

	inside := filepath.Join(allowed, "photo.jpg")
	outside := filepath.Join(forbidden, "id_rsa")
	for _, p := range []string{inside, outside} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}

	roots, err := mediapath.New([]string{allowed})
	if err != nil {
		t.Fatalf("mediapath.New: %v", err)
	}
	b, _ := newTestBridgeWithRoots(t, roots)

	for _, kind := range []string{"media", "voice"} {
		if err := b.Validate(gate.Delivery{Kind: kind, Path: inside}); err != nil {
			t.Errorf("Validate(%s, inside roots) = %v, want nil", kind, err)
		}
		if err := b.Validate(gate.Delivery{Kind: kind, Path: outside}); !errors.Is(err, mediapath.ErrOutsideRoots) {
			t.Errorf("Validate(%s, outside roots) = %v, want ErrOutsideRoots", kind, err)
		}
	}

	// Kinds that name no file must not be judged by the media roots, or a
	// plain text send would be refused for having an empty path.
	for _, kind := range []string{"text", "reaction", "read"} {
		if err := b.Validate(gate.Delivery{Kind: kind, To: "111@s.whatsapp.net"}); err != nil {
			t.Errorf("Validate(%s) = %v, want nil (no file involved)", kind, err)
		}
	}
}

// TestDeliverRechecksRootsIndependently proves the read path does not rely
// on Validate having been called. A Bridge whose roots allow nothing must
// refuse to read, which is what makes the confinement a property of the
// bridge rather than of its callers remembering to ask.
func TestDeliverRechecksRootsIndependently(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "photo.jpg")
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Zero roots: allows nothing.
	b, _ := newTestBridge(t)

	_, err := b.Deliver(context.Background(), gate.Delivery{
		Kind: "media", To: "111@s.whatsapp.net", Path: f,
	})
	if !errors.Is(err, mediapath.ErrOutsideRoots) {
		t.Errorf("Deliver(media) = %v, want ErrOutsideRoots before any read or network call", err)
	}
}

// recordOutbound is what makes a sent message readable back through
// list_messages: it must write the same store rows an inbound copy of the
// message would have produced.

func TestRecordOutboundStoresFromMeTextMessage(t *testing.T) {
	b, fake := newTestBridge(t)
	to, _ := types.ParseJID("111@s.whatsapp.net")
	sentAt := time.Unix(1787056496, 0)

	b.recordOutbound(to, "SENT1", sentAt, &waE2E.Message{Conversation: proto.String("hello from me")})

	if len(fake.messages) != 1 {
		t.Fatalf("stored %d messages, want 1", len(fake.messages))
	}
	m := fake.messages[0]
	if !m.FromMe {
		t.Fatalf("stored message FromMe = false, want true")
	}
	if m.ChatJID != "111@s.whatsapp.net" || m.ID != "SENT1" || m.TS != sentAt.Unix() {
		t.Fatalf("stored message = %+v, want chat 111@s.whatsapp.net, id SENT1, ts %d", m, sentAt.Unix())
	}
	if m.Kind != "text" || m.Text != "hello from me" {
		t.Fatalf("stored message kind/text = %q/%q, want text/hello from me", m.Kind, m.Text)
	}

	if len(fake.chats) != 1 {
		t.Fatalf("upserted %d chats, want 1 (the chat row must exist and bump last_message_at)", len(fake.chats))
	}
	if fake.chats[0].jid != "111@s.whatsapp.net" || fake.chats[0].lastMessageAt != sentAt.Unix() || fake.chats[0].isGroup {
		t.Fatalf("upserted chat = %+v, want the DM chat with last_message_at %d", fake.chats[0], sentAt.Unix())
	}
}

func TestRecordOutboundGroupChatIsGroup(t *testing.T) {
	b, fake := newTestBridge(t)
	to, _ := types.ParseJID("222@g.us")

	b.recordOutbound(to, "SENT2", time.Unix(1787056496, 0), &waE2E.Message{Conversation: proto.String("hi group")})

	if len(fake.chats) != 1 || !fake.chats[0].isGroup {
		t.Fatalf("upserted chats = %+v, want one group chat row", fake.chats)
	}
}

// An outbound media message must store a media reference, so download_media
// works on your own sends the same as on received ones.
func TestRecordOutboundMediaCarriesMediaRef(t *testing.T) {
	b, fake := newTestBridge(t)
	to, _ := types.ParseJID("111@s.whatsapp.net")

	b.recordOutbound(to, "SENT3", time.Unix(1787056496, 0), &waE2E.Message{
		ImageMessage: &waE2E.ImageMessage{Caption: proto.String("a photo"), URL: proto.String("https://example.invalid/x")},
	})

	if len(fake.messages) != 1 {
		t.Fatalf("stored %d messages, want 1", len(fake.messages))
	}
	m := fake.messages[0]
	if m.Kind != "image" || m.Text != "a photo" {
		t.Fatalf("stored message kind/caption = %q/%q, want image/a photo", m.Kind, m.Text)
	}
	if len(m.MediaRef) == 0 {
		t.Fatal("stored outbound media message has no MediaRef; download_media could not fetch it back")
	}
}
