package bridge

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/idle-sync/whatsapp-connect-mcp/internal/gate"
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
