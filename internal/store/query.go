package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

const (
	defaultLimit = 20
	maxLimit     = 100
)

// clampLimit is the one place every list query normalizes its caller-
// supplied limit: 0 (or negative) becomes the default of 20, anything
// above 100 is capped at 100.
func clampLimit(limit int) int {
	switch {
	case limit <= 0:
		return defaultLimit
	case limit > maxLimit:
		return maxLimit
	default:
		return limit
	}
}

// clampContext bounds a MessageContext before/after count to [0, maxLimit].
// Unlike clampLimit, 0 is a meaningful request (e.g. after=0 means "nothing
// following the target"), so it is left alone rather than promoted to a
// default.
func clampContext(n int) int {
	switch {
	case n < 0:
		return 0
	case n > maxLimit:
		return maxLimit
	default:
		return n
	}
}

// ChatRow is one row returned by Chats and Chat.
type ChatRow struct {
	JID, Name     string
	IsGroup       bool
	Archived      bool
	LastMessageAt int64
}

// MessageRow is one row returned by the message-listing queries.
// SenderName resolves through contacts (first non-empty of full_name,
// push_name, business_name) and falls back to SenderJID when no contact
// record is known.
type MessageRow struct {
	ChatJID, ID, SenderJID, SenderName string
	FromMe                             bool
	TS                                 int64
	Kind, Text, QuotedID               string
	HasMedia                           bool
}

// ContactRow is one row returned by SearchContacts. Name is the first
// non-empty of full_name, push_name, business_name, falling back to JID
// when a contact has none of the three set.
type ContactRow struct {
	JID, Phone, Name string
}

// CallRow is one row returned by Calls. PeerName resolves the same way as
// MessageRow.SenderName.
type CallRow struct {
	ID, PeerJID, PeerName, Direction, Status string
	TS                                       int64
	IsVideo                                  bool
}

// scanner is satisfied by both *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

// querier is satisfied by *sql.DB; it is the seam queryMessages runs
// against.
type querier interface {
	Query(query string, args ...any) (*sql.Rows, error)
}

// messageSelect is the shared column list and join every message-row query
// builds on: it resolves sender_name via contacts and leaves the caller to
// append its own WHERE/ORDER/LIMIT.
const messageSelect = `
SELECT m.chat_jid, m.id, m.sender_jid, m.from_me, m.ts, m.kind, m.text, m.quoted_id, m.media_ref IS NOT NULL,
       CASE
         WHEN c.full_name <> '' THEN c.full_name
         WHEN c.push_name <> '' THEN c.push_name
         WHEN c.business_name <> '' THEN c.business_name
         ELSE m.sender_jid
       END
FROM messages m
LEFT JOIN contacts c ON c.jid = m.sender_jid`

func scanMessageRow(row scanner) (MessageRow, error) {
	var m MessageRow
	err := row.Scan(&m.ChatJID, &m.ID, &m.SenderJID, &m.FromMe, &m.TS, &m.Kind, &m.Text, &m.QuotedID, &m.HasMedia, &m.SenderName)
	return m, err
}

func scanChatRow(row scanner) (ChatRow, error) {
	var c ChatRow
	err := row.Scan(&c.JID, &c.Name, &c.IsGroup, &c.Archived, &c.LastMessageAt)
	return c, err
}

// queryMessages runs query against q and scans every row as a MessageRow.
func queryMessages(q querier, query string, args ...any) ([]MessageRow, error) {
	rows, err := q.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []MessageRow
	for rows.Next() {
		m, err := scanMessageRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// Chats lists chats newest-first (by last_message_at), optionally filtered
// by a case-insensitive substring match on name and by archived state.
func (s *Store) Chats(query string, includeArchived bool, limit int) ([]ChatRow, error) {
	limit = clampLimit(limit)

	var b strings.Builder
	b.WriteString(`SELECT jid, name, is_group, archived, last_message_at FROM chats WHERE 1 = 1`)
	var args []any
	if query != "" {
		b.WriteString(` AND name LIKE '%' || ? || '%'`)
		args = append(args, query)
	}
	if !includeArchived {
		b.WriteString(` AND archived = 0`)
	}
	b.WriteString(` ORDER BY last_message_at DESC LIMIT ?`)
	args = append(args, limit)

	rows, err := s.db.Query(b.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("list chats: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []ChatRow
	for rows.Next() {
		c, err := scanChatRow(rows)
		if err != nil {
			return nil, fmt.Errorf("list chats: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list chats: %w", err)
	}
	return out, nil
}

// Chat looks up a single chat by jid. ok is false when no such chat exists.
func (s *Store) Chat(jid string) (ChatRow, bool, error) {
	row := s.db.QueryRow(`SELECT jid, name, is_group, archived, last_message_at FROM chats WHERE jid = ?`, jid)
	c, err := scanChatRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ChatRow{}, false, nil
	}
	if err != nil {
		return ChatRow{}, false, fmt.Errorf("get chat: %w", err)
	}
	return c, true, nil
}

// Messages lists messages in chatJID newest-first. beforeTS and afterTS,
// when positive, bound the window to ts < beforeTS and ts > afterTS
// respectively; either left at 0 leaves that side unbounded.
func (s *Store) Messages(chatJID string, beforeTS, afterTS int64, limit int) ([]MessageRow, error) {
	limit = clampLimit(limit)

	var b strings.Builder
	b.WriteString(messageSelect)
	b.WriteString(` WHERE m.chat_jid = ?`)
	args := []any{chatJID}
	if beforeTS > 0 {
		b.WriteString(` AND m.ts < ?`)
		args = append(args, beforeTS)
	}
	if afterTS > 0 {
		b.WriteString(` AND m.ts > ?`)
		args = append(args, afterTS)
	}
	b.WriteString(` ORDER BY m.ts DESC, m.id DESC LIMIT ?`)
	args = append(args, limit)

	out, err := queryMessages(s.db, b.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("list messages: %w", err)
	}
	return out, nil
}

// ftsPhrase turns arbitrary user search text into a syntactically valid
// FTS5 MATCH argument. The whole string is wrapped as one quoted phrase,
// with embedded double quotes doubled to escape them. This is a deliberate
// choice over passing query through as raw FTS5 syntax: a bare word still
// matches exactly as expected, and text containing FTS5 operator
// characters (AND, OR, NOT, -, :, *, parentheses, an unterminated quote)
// is matched as literal text instead of being parsed as query syntax — so
// malformed operator input can never reach the FTS5 parser as a syntax
// error, only ever as an (possibly empty) ordinary phrase match.
func ftsPhrase(query string) string {
	return `"` + strings.ReplaceAll(query, `"`, `""`) + `"`
}

// SearchMessages full-text searches message bodies via FTS5, optionally
// scoped to one chat. See ftsPhrase for how query is interpreted.
func (s *Store) SearchMessages(query, chatJID string, limit int) ([]MessageRow, error) {
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("search messages: query required")
	}
	limit = clampLimit(limit)

	var b strings.Builder
	b.WriteString(messageSelect)
	b.WriteString(` JOIN messages_fts f ON f.rowid = m.rowid WHERE f.messages_fts MATCH ?`)
	args := []any{ftsPhrase(query)}
	if chatJID != "" {
		b.WriteString(` AND m.chat_jid = ?`)
		args = append(args, chatJID)
	}
	b.WriteString(` ORDER BY m.ts DESC, m.id DESC LIMIT ?`)
	args = append(args, limit)

	out, err := queryMessages(s.db, b.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("search messages: %w", err)
	}
	return out, nil
}

// messageByID fetches the single message identified by (chatJID, id).
func (s *Store) messageByID(chatJID, id string) (MessageRow, bool, error) {
	row := s.db.QueryRow(messageSelect+` WHERE m.chat_jid = ? AND m.id = ?`, chatJID, id)
	m, err := scanMessageRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return MessageRow{}, false, nil
	}
	if err != nil {
		return MessageRow{}, false, err
	}
	return m, true, nil
}

// MessageContext returns up to `before` messages preceding id, id's own
// message, and up to `after` messages following it, all within chatJID,
// ordered oldest to newest. before and after are clamped to [0, 100]; see
// clampContext for why 0 is not promoted to a default here.
func (s *Store) MessageContext(chatJID, id string, before, after int) ([]MessageRow, error) {
	before = clampContext(before)
	after = clampContext(after)

	target, ok, err := s.messageByID(chatJID, id)
	if err != nil {
		return nil, fmt.Errorf("message context: %w", err)
	}
	if !ok {
		return nil, fmt.Errorf("message context: message not found")
	}

	var b strings.Builder
	b.WriteString(messageSelect)
	b.WriteString(` WHERE m.chat_jid = ? AND (m.ts < ? OR (m.ts = ? AND m.id < ?)) ORDER BY m.ts DESC, m.id DESC LIMIT ?`)
	beforeRows, err := queryMessages(s.db, b.String(), chatJID, target.TS, target.TS, target.ID, before)
	if err != nil {
		return nil, fmt.Errorf("message context: %w", err)
	}
	reverseMessages(beforeRows)

	b.Reset()
	b.WriteString(messageSelect)
	b.WriteString(` WHERE m.chat_jid = ? AND (m.ts > ? OR (m.ts = ? AND m.id > ?)) ORDER BY m.ts ASC, m.id ASC LIMIT ?`)
	afterRows, err := queryMessages(s.db, b.String(), chatJID, target.TS, target.TS, target.ID, after)
	if err != nil {
		return nil, fmt.Errorf("message context: %w", err)
	}

	out := make([]MessageRow, 0, len(beforeRows)+1+len(afterRows))
	out = append(out, beforeRows...)
	out = append(out, target)
	out = append(out, afterRows...)
	return out, nil
}

func reverseMessages(rows []MessageRow) {
	for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
		rows[i], rows[j] = rows[j], rows[i]
	}
}

// SearchContacts matches query against contact name and phone (LIKE,
// case-insensitive for ASCII). An empty query matches every contact.
// Results are ordered by resolved display name — contacts carry no
// timestamp to order newest-first by.
func (s *Store) SearchContacts(query string, limit int) ([]ContactRow, error) {
	limit = clampLimit(limit)

	var b strings.Builder
	b.WriteString(`
SELECT jid, phone,
       CASE
         WHEN full_name <> '' THEN full_name
         WHEN push_name <> '' THEN push_name
         WHEN business_name <> '' THEN business_name
         ELSE jid
       END AS name
FROM contacts`)
	var args []any
	if query != "" {
		b.WriteString(` WHERE full_name LIKE '%' || ? || '%' OR push_name LIKE '%' || ? || '%' OR business_name LIKE '%' || ? || '%' OR phone LIKE '%' || ? || '%'`)
		args = append(args, query, query, query, query)
	}
	b.WriteString(` ORDER BY name LIMIT ?`)
	args = append(args, limit)

	rows, err := s.db.Query(b.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("search contacts: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []ContactRow
	for rows.Next() {
		var c ContactRow
		if err := rows.Scan(&c.JID, &c.Phone, &c.Name); err != nil {
			return nil, fmt.Errorf("search contacts: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("search contacts: %w", err)
	}
	return out, nil
}

// LastInteraction returns the most recent message associated with jid,
// whether as the chat itself (a 1:1 conversation) or as a sender within a
// chat (e.g. a participant's last message in a group). ok is false when
// jid has no messages at all.
func (s *Store) LastInteraction(jid string) (MessageRow, bool, error) {
	row := s.db.QueryRow(
		messageSelect+` WHERE m.chat_jid = ? OR m.sender_jid = ? ORDER BY m.ts DESC, m.id DESC LIMIT 1`,
		jid, jid,
	)
	m, err := scanMessageRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return MessageRow{}, false, nil
	}
	if err != nil {
		return MessageRow{}, false, fmt.Errorf("last interaction: %w", err)
	}
	return m, true, nil
}

// Calls lists calls newest-first, optionally filtered to one peer JID.
func (s *Store) Calls(peerJID string, limit int) ([]CallRow, error) {
	limit = clampLimit(limit)

	var b strings.Builder
	b.WriteString(`
SELECT ca.id, ca.peer_jid,
       CASE
         WHEN c.full_name <> '' THEN c.full_name
         WHEN c.push_name <> '' THEN c.push_name
         WHEN c.business_name <> '' THEN c.business_name
         ELSE ca.peer_jid
       END,
       ca.direction, ca.status, ca.ts, ca.is_video
FROM calls ca
LEFT JOIN contacts c ON c.jid = ca.peer_jid`)
	var args []any
	if peerJID != "" {
		b.WriteString(` WHERE ca.peer_jid = ?`)
		args = append(args, peerJID)
	}
	b.WriteString(` ORDER BY ca.ts DESC, ca.id DESC LIMIT ?`)
	args = append(args, limit)

	rows, err := s.db.Query(b.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("list calls: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []CallRow
	for rows.Next() {
		var c CallRow
		if err := rows.Scan(&c.ID, &c.PeerJID, &c.PeerName, &c.Direction, &c.Status, &c.TS, &c.IsVideo); err != nil {
			return nil, fmt.Errorf("list calls: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list calls: %w", err)
	}
	return out, nil
}

// MessageMediaRef returns the stored media download reference for a
// message. err is non-nil (and ref/filename/kind zero) when the message
// does not exist or carries no media.
func (s *Store) MessageMediaRef(chatJID, id string) (ref []byte, filename, kind string, err error) {
	row := s.db.QueryRow(`SELECT media_ref, media_filename, kind FROM messages WHERE chat_jid = ? AND id = ?`, chatJID, id)
	if scanErr := row.Scan(&ref, &filename, &kind); scanErr != nil {
		if errors.Is(scanErr, sql.ErrNoRows) {
			return nil, "", "", fmt.Errorf("message media: message not found")
		}
		return nil, "", "", fmt.Errorf("message media: %w", scanErr)
	}
	if ref == nil {
		return nil, "", "", fmt.Errorf("message media: message has no media")
	}
	return ref, filename, kind, nil
}
