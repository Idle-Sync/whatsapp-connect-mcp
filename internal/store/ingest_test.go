package store

import (
	"path/filepath"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "messages.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestUpsertChatTwiceYieldsOneRowWithNewestValues(t *testing.T) {
	s := newTestStore(t)
	jid := "123@s.whatsapp.net"

	if err := s.UpsertChat(jid, "Old Name", false, 100); err != nil {
		t.Fatalf("first UpsertChat: %v", err)
	}
	if err := s.UpsertChat(jid, "New Name", true, 200); err != nil {
		t.Fatalf("second UpsertChat: %v", err)
	}

	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM chats`).Scan(&count); err != nil {
		t.Fatalf("count chats: %v", err)
	}
	if count != 1 {
		t.Fatalf("chats row count = %d, want 1", count)
	}

	var name string
	var isGroup bool
	var lastMessageAt int64
	if err := s.db.QueryRow(
		`SELECT name, is_group, last_message_at FROM chats WHERE jid = ?`, jid,
	).Scan(&name, &isGroup, &lastMessageAt); err != nil {
		t.Fatalf("query chat: %v", err)
	}
	if name != "New Name" || !isGroup || lastMessageAt != 200 {
		t.Fatalf("chat = (%q, %v, %d), want (New Name, true, 200)", name, isGroup, lastMessageAt)
	}
}

func TestUpsertChatEmptyNameDoesNotOverwriteExistingName(t *testing.T) {
	s := newTestStore(t)
	jid := "123@s.whatsapp.net"

	if err := s.UpsertChat(jid, "Real Name", false, 100); err != nil {
		t.Fatalf("first UpsertChat: %v", err)
	}
	if err := s.UpsertChat(jid, "", false, 200); err != nil {
		t.Fatalf("second UpsertChat (empty name): %v", err)
	}

	var name string
	var lastMessageAt int64
	if err := s.db.QueryRow(
		`SELECT name, last_message_at FROM chats WHERE jid = ?`, jid,
	).Scan(&name, &lastMessageAt); err != nil {
		t.Fatalf("query chat: %v", err)
	}
	if name != "Real Name" {
		t.Fatalf("name = %q, want Real Name (an empty name must not blank a known one)", name)
	}
	if lastMessageAt != 200 {
		t.Fatalf("last_message_at = %d, want 200 (other fields still update)", lastMessageAt)
	}
}

func TestUpsertChatFirstInsertWithEmptyNameLeavesRowBlank(t *testing.T) {
	s := newTestStore(t)
	jid := "123@s.whatsapp.net"

	if err := s.UpsertChat(jid, "", true, 100); err != nil {
		t.Fatalf("UpsertChat: %v", err)
	}

	var name string
	if err := s.db.QueryRow(`SELECT name FROM chats WHERE jid = ?`, jid).Scan(&name); err != nil {
		t.Fatalf("query chat: %v", err)
	}
	if name != "" {
		t.Fatalf("name = %q, want empty (no prior name to preserve on first insert)", name)
	}
}

func TestUpsertChatLastMessageAtNeverGoesBackward(t *testing.T) {
	s := newTestStore(t)
	jid := "123@s.whatsapp.net"

	if err := s.UpsertChat(jid, "Name", false, 200); err != nil {
		t.Fatalf("first UpsertChat: %v", err)
	}
	if err := s.UpsertChat(jid, "Name", false, 50); err != nil {
		t.Fatalf("second UpsertChat: %v", err)
	}

	var lastMessageAt int64
	if err := s.db.QueryRow(`SELECT last_message_at FROM chats WHERE jid = ?`, jid).Scan(&lastMessageAt); err != nil {
		t.Fatalf("query chat: %v", err)
	}
	if lastMessageAt != 200 {
		t.Fatalf("last_message_at = %d, want 200 (must not move backward)", lastMessageAt)
	}
}

func TestUpsertMessageTwiceYieldsOneRowWithNewestValues(t *testing.T) {
	s := newTestStore(t)
	jid := "123@s.whatsapp.net"
	if err := s.UpsertChat(jid, "Name", false, 0); err != nil {
		t.Fatalf("UpsertChat: %v", err)
	}

	first := Message{ChatJID: jid, ID: "msg1", SenderJID: jid, FromMe: false, TS: 100, Kind: "text", Text: "hello"}
	if err := s.UpsertMessage(first); err != nil {
		t.Fatalf("first UpsertMessage: %v", err)
	}
	second := Message{ChatJID: jid, ID: "msg1", SenderJID: jid, FromMe: true, TS: 150, Kind: "text", Text: "revised", QuotedID: "msg0"}
	if err := s.UpsertMessage(second); err != nil {
		t.Fatalf("second UpsertMessage: %v", err)
	}

	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM messages`).Scan(&count); err != nil {
		t.Fatalf("count messages: %v", err)
	}
	if count != 1 {
		t.Fatalf("messages row count = %d, want 1", count)
	}

	var text, quotedID string
	var fromMe bool
	var ts int64
	if err := s.db.QueryRow(
		`SELECT text, quoted_id, from_me, ts FROM messages WHERE chat_jid = ? AND id = ?`, jid, "msg1",
	).Scan(&text, &quotedID, &fromMe, &ts); err != nil {
		t.Fatalf("query message: %v", err)
	}
	if text != "revised" || quotedID != "msg0" || !fromMe || ts != 150 {
		t.Fatalf("message = (%q, %q, %v, %d), want (revised, msg0, true, 150)", text, quotedID, fromMe, ts)
	}
}

func TestUpsertMessageBumpsChatLastMessageAtForwardOnly(t *testing.T) {
	s := newTestStore(t)
	jid := "123@s.whatsapp.net"
	if err := s.UpsertChat(jid, "Name", false, 0); err != nil {
		t.Fatalf("UpsertChat: %v", err)
	}

	if err := s.UpsertMessage(Message{ChatJID: jid, ID: "msg1", SenderJID: jid, TS: 100, Kind: "text", Text: "a"}); err != nil {
		t.Fatalf("UpsertMessage msg1: %v", err)
	}
	if err := s.UpsertMessage(Message{ChatJID: jid, ID: "msg2", SenderJID: jid, TS: 50, Kind: "text", Text: "b"}); err != nil {
		t.Fatalf("UpsertMessage msg2 (older): %v", err)
	}

	var lastMessageAt int64
	if err := s.db.QueryRow(`SELECT last_message_at FROM chats WHERE jid = ?`, jid).Scan(&lastMessageAt); err != nil {
		t.Fatalf("query chat: %v", err)
	}
	if lastMessageAt != 100 {
		t.Fatalf("last_message_at = %d, want 100 (older message must not move it backward)", lastMessageAt)
	}

	if err := s.UpsertMessage(Message{ChatJID: jid, ID: "msg3", SenderJID: jid, TS: 200, Kind: "text", Text: "c"}); err != nil {
		t.Fatalf("UpsertMessage msg3 (newer): %v", err)
	}
	if err := s.db.QueryRow(`SELECT last_message_at FROM chats WHERE jid = ?`, jid).Scan(&lastMessageAt); err != nil {
		t.Fatalf("query chat: %v", err)
	}
	if lastMessageAt != 200 {
		t.Fatalf("last_message_at = %d, want 200", lastMessageAt)
	}
}

func TestUpsertContactTwiceYieldsOneRowWithNewestValues(t *testing.T) {
	s := newTestStore(t)
	jid := "123@s.whatsapp.net"

	if err := s.UpsertContact(jid, "+1000", "Old Push", "Old Full", ""); err != nil {
		t.Fatalf("first UpsertContact: %v", err)
	}
	if err := s.UpsertContact(jid, "+2000", "New Push", "New Full", "Acme"); err != nil {
		t.Fatalf("second UpsertContact: %v", err)
	}

	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM contacts`).Scan(&count); err != nil {
		t.Fatalf("count contacts: %v", err)
	}
	if count != 1 {
		t.Fatalf("contacts row count = %d, want 1", count)
	}

	var phone, pushName, fullName, businessName string
	if err := s.db.QueryRow(
		`SELECT phone, push_name, full_name, business_name FROM contacts WHERE jid = ?`, jid,
	).Scan(&phone, &pushName, &fullName, &businessName); err != nil {
		t.Fatalf("query contact: %v", err)
	}
	if phone != "+2000" || pushName != "New Push" || fullName != "New Full" || businessName != "Acme" {
		t.Fatalf("contact = (%q, %q, %q, %q), want (+2000, New Push, New Full, Acme)", phone, pushName, fullName, businessName)
	}
}

func TestUpsertContactEmptyFieldsKeepOldValues(t *testing.T) {
	s := newTestStore(t)
	jid := "123@s.whatsapp.net"

	if err := s.UpsertContact(jid, "+1000", "Alice", "Alice Smith", "Acme"); err != nil {
		t.Fatalf("first UpsertContact: %v", err)
	}
	if err := s.UpsertContact(jid, "", "", "", ""); err != nil {
		t.Fatalf("second UpsertContact (all empty): %v", err)
	}

	var phone, pushName, fullName, businessName string
	if err := s.db.QueryRow(
		`SELECT phone, push_name, full_name, business_name FROM contacts WHERE jid = ?`, jid,
	).Scan(&phone, &pushName, &fullName, &businessName); err != nil {
		t.Fatalf("query contact: %v", err)
	}
	if phone != "+1000" || pushName != "Alice" || fullName != "Alice Smith" || businessName != "Acme" {
		t.Fatalf("contact = (%q, %q, %q, %q), want unchanged (+1000, Alice, Alice Smith, Acme)", phone, pushName, fullName, businessName)
	}
}

func TestMarkReadOnUnknownIDIsNoOp(t *testing.T) {
	s := newTestStore(t)

	if err := s.MarkRead("123@s.whatsapp.net", []string{"does-not-exist"}, 500); err != nil {
		t.Fatalf("MarkRead on unknown id: %v", err)
	}
}

func TestMarkReadEmptyIDsIsNoOp(t *testing.T) {
	s := newTestStore(t)

	if err := s.MarkRead("123@s.whatsapp.net", nil, 500); err != nil {
		t.Fatalf("MarkRead with no ids: %v", err)
	}
}

func TestMarkReadSetsReadAtForKnownIDs(t *testing.T) {
	s := newTestStore(t)
	jid := "123@s.whatsapp.net"
	if err := s.UpsertChat(jid, "Name", false, 0); err != nil {
		t.Fatalf("UpsertChat: %v", err)
	}
	if err := s.UpsertMessage(Message{ChatJID: jid, ID: "msg1", SenderJID: jid, TS: 100, Kind: "text", Text: "hi"}); err != nil {
		t.Fatalf("UpsertMessage: %v", err)
	}

	if err := s.MarkRead(jid, []string{"msg1", "does-not-exist"}, 500); err != nil {
		t.Fatalf("MarkRead: %v", err)
	}

	var readAt int64
	if err := s.db.QueryRow(`SELECT read_at FROM messages WHERE chat_jid = ? AND id = ?`, jid, "msg1").Scan(&readAt); err != nil {
		t.Fatalf("query message: %v", err)
	}
	if readAt != 500 {
		t.Fatalf("read_at = %d, want 500", readAt)
	}
}

func TestMarkReadNeverGoesBackward(t *testing.T) {
	s := newTestStore(t)
	jid := "123@s.whatsapp.net"
	if err := s.UpsertChat(jid, "Name", false, 0); err != nil {
		t.Fatalf("UpsertChat: %v", err)
	}
	if err := s.UpsertMessage(Message{ChatJID: jid, ID: "msg1", SenderJID: jid, TS: 100, Kind: "text", Text: "hi"}); err != nil {
		t.Fatalf("UpsertMessage: %v", err)
	}

	if err := s.MarkRead(jid, []string{"msg1"}, 500); err != nil {
		t.Fatalf("first MarkRead: %v", err)
	}
	if err := s.MarkRead(jid, []string{"msg1"}, 100); err != nil {
		t.Fatalf("second MarkRead (older): %v", err)
	}

	var readAt int64
	if err := s.db.QueryRow(`SELECT read_at FROM messages WHERE chat_jid = ? AND id = ?`, jid, "msg1").Scan(&readAt); err != nil {
		t.Fatalf("query message: %v", err)
	}
	if readAt != 500 {
		t.Fatalf("read_at = %d, want 500 (must not move backward)", readAt)
	}
}

func TestInsertCallTwiceYieldsOneRowWithNewestValues(t *testing.T) {
	s := newTestStore(t)

	if err := s.InsertCall("call1", "123@s.whatsapp.net", 100, "incoming", "missed", false); err != nil {
		t.Fatalf("first InsertCall: %v", err)
	}
	if err := s.InsertCall("call1", "123@s.whatsapp.net", 100, "incoming", "answered", true); err != nil {
		t.Fatalf("second InsertCall: %v", err)
	}

	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM calls`).Scan(&count); err != nil {
		t.Fatalf("count calls: %v", err)
	}
	if count != 1 {
		t.Fatalf("calls row count = %d, want 1", count)
	}

	var status string
	var isVideo bool
	if err := s.db.QueryRow(`SELECT status, is_video FROM calls WHERE id = ?`, "call1").Scan(&status, &isVideo); err != nil {
		t.Fatalf("query call: %v", err)
	}
	if status != "answered" || !isVideo {
		t.Fatalf("call = (%q, %v), want (answered, true)", status, isVideo)
	}
}
