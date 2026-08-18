package bridge

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
