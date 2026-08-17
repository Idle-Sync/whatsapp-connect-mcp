package store

import (
	"fmt"
	"strings"
)

// Message is one WhatsApp message to write via UpsertMessage.
type Message struct {
	ChatJID, ID, SenderJID string
	FromMe                 bool
	TS                     int64
	Kind, Text, QuotedID   string
	MediaRef               []byte
	MediaFilename          string
}

// UpsertChat inserts or updates the chat row for jid. name passed empty
// leaves the existing stored name untouched rather than blanking it (the
// caller may not know the chat's name yet, e.g. a group before its first
// GroupInfo/HistorySync event); isGroup is always set to the given value.
// last_message_at only ever moves forward, so a stale or missing timestamp
// never overwrites a newer one.
func (s *Store) UpsertChat(jid, name string, isGroup bool, lastMessageAt int64) error {
	_, err := s.db.Exec(`
		INSERT INTO chats (jid, name, is_group, last_message_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT (jid) DO UPDATE SET
			name = CASE WHEN excluded.name <> '' THEN excluded.name ELSE chats.name END,
			is_group = excluded.is_group,
			last_message_at = MAX(chats.last_message_at, excluded.last_message_at)`,
		jid, name, isGroup, lastMessageAt,
	)
	if err != nil {
		return fmt.Errorf("upsert chat: %w", err)
	}
	return nil
}

// UpsertMessage inserts or updates the message row keyed by (chat_jid, id).
// A redelivered id overwrites every field with the newest values except
// read_at, which is left untouched. It also bumps the owning chat's
// last_message_at forward to m.TS; it never moves it backward and is a
// no-op if the chat row does not exist yet.
func (s *Store) UpsertMessage(m Message) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("upsert message: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`
		INSERT INTO messages (chat_jid, id, sender_jid, from_me, ts, kind, text, quoted_id, media_ref, media_filename)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (chat_jid, id) DO UPDATE SET
			sender_jid = excluded.sender_jid,
			from_me = excluded.from_me,
			ts = excluded.ts,
			kind = excluded.kind,
			text = excluded.text,
			quoted_id = excluded.quoted_id,
			media_ref = excluded.media_ref,
			media_filename = excluded.media_filename`,
		m.ChatJID, m.ID, m.SenderJID, m.FromMe, m.TS, m.Kind, m.Text, m.QuotedID, m.MediaRef, m.MediaFilename,
	); err != nil {
		return fmt.Errorf("upsert message: %w", err)
	}

	if _, err := tx.Exec(
		`UPDATE chats SET last_message_at = MAX(last_message_at, ?) WHERE jid = ?`,
		m.TS, m.ChatJID,
	); err != nil {
		return fmt.Errorf("upsert message: bump chat last_message_at: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("upsert message: %w", err)
	}
	return nil
}

// UpsertContact inserts or updates the contact row for jid. Any of phone,
// pushName, fullName, or businessName passed empty leaves the existing
// stored value untouched rather than overwriting it with blank.
func (s *Store) UpsertContact(jid, phone, pushName, fullName, businessName string) error {
	_, err := s.db.Exec(`
		INSERT INTO contacts (jid, phone, push_name, full_name, business_name)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (jid) DO UPDATE SET
			phone = CASE WHEN excluded.phone <> '' THEN excluded.phone ELSE contacts.phone END,
			push_name = CASE WHEN excluded.push_name <> '' THEN excluded.push_name ELSE contacts.push_name END,
			full_name = CASE WHEN excluded.full_name <> '' THEN excluded.full_name ELSE contacts.full_name END,
			business_name = CASE WHEN excluded.business_name <> '' THEN excluded.business_name ELSE contacts.business_name END`,
		jid, phone, pushName, fullName, businessName,
	)
	if err != nil {
		return fmt.Errorf("upsert contact: %w", err)
	}
	return nil
}

// MarkRead sets read_at on the given message ids within chatJID, moving it
// forward only. Ids that do not exist (or belong to a different chat) are
// silently skipped rather than treated as an error. An empty ids slice is a
// no-op.
func (s *Store) MarkRead(chatJID string, ids []string, readAt int64) error {
	if len(ids) == 0 {
		return nil
	}

	placeholders := strings.Repeat("?,", len(ids))
	placeholders = placeholders[:len(placeholders)-1]

	args := make([]any, 0, len(ids)+2)
	args = append(args, readAt, chatJID)
	for _, id := range ids {
		args = append(args, id)
	}

	var query strings.Builder
	query.WriteString(`UPDATE messages SET read_at = MAX(read_at, ?) WHERE chat_jid = ? AND id IN (`)
	query.WriteString(placeholders)
	query.WriteString(`)`)
	if _, err := s.db.Exec(query.String(), args...); err != nil {
		return fmt.Errorf("mark read: %w", err)
	}
	return nil
}

// InsertCall inserts or updates the call row for id with the newest fields,
// so a later status report (e.g. ringing -> missed) overwrites in place.
func (s *Store) InsertCall(id, peerJID string, ts int64, direction, status string, isVideo bool) error {
	_, err := s.db.Exec(`
		INSERT INTO calls (id, peer_jid, ts, direction, status, is_video)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET
			peer_jid = excluded.peer_jid,
			ts = excluded.ts,
			direction = excluded.direction,
			status = excluded.status,
			is_video = excluded.is_video`,
		id, peerJID, ts, direction, status, isVideo,
	)
	if err != nil {
		return fmt.Errorf("insert call: %w", err)
	}
	return nil
}
