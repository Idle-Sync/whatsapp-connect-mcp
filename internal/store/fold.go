package store

import (
	"database/sql"
	"fmt"
	"strings"
)

// FoldStats reports what FoldLIDs rewrote.
type FoldStats struct {
	Chats    int // LID chat rows folded into their phone-number chat
	Messages int // message rows re-keyed to a phone number (as chat or sender)
	Contacts int // LID contact rows folded into their phone-number contact
	Calls    int // call rows re-keyed to a phone-number peer
}

// Total is the number of rows FoldLIDs touched.
func (s FoldStats) Total() int { return s.Chats + s.Messages + s.Contacts + s.Calls }

// FoldLIDs rewrites every privacy-LID identity ("...@lid") that resolve
// can name into its phone-number JID, so one person is one chat and one
// contact instead of a phone-number half and a LID half side by side.
// resolve takes a bare LID JID and returns the bare phone JID or "" when
// the pairing is unknown; unresolvable LIDs are left untouched.
//
// A LID chat's messages move into the phone chat (a message already
// present there under the same id wins and the LID copy is dropped), the
// phone chat inherits the LID chat's name and archived flag only where it
// has none of its own, and last_message_at is the later of the two. LID
// senders — device-qualified or not — are rewritten on every message they
// appear in, LID contacts fill in whatever names the phone contact lacks
// before being removed, and call peers follow the same rule. Every pairing
// used is also recorded in lid_map for the read-side fallbacks.
//
// The whole fold is one transaction and is idempotent: a second run with
// the same resolver finds nothing left to move.
func (s *Store) FoldLIDs(resolve func(lid string) string) (FoldStats, error) {
	var stats FoldStats
	tx, err := s.db.Begin()
	if err != nil {
		return stats, fmt.Errorf("fold lids: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := foldLIDChats(tx, resolve, &stats); err != nil {
		return stats, fmt.Errorf("fold lids: %w", err)
	}
	if err := foldLIDSenders(tx, resolve, &stats); err != nil {
		return stats, fmt.Errorf("fold lids: %w", err)
	}
	if err := foldLIDContacts(tx, resolve, &stats); err != nil {
		return stats, fmt.Errorf("fold lids: %w", err)
	}
	if err := foldLIDCalls(tx, resolve, &stats); err != nil {
		return stats, fmt.Errorf("fold lids: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return stats, fmt.Errorf("fold lids: %w", err)
	}
	return stats, nil
}

type lidChat struct {
	jid, name     string
	archived      bool
	lastMessageAt int64
}

func foldLIDChats(tx *sql.Tx, resolve func(string) string, stats *FoldStats) error {
	rows, err := tx.Query(`SELECT jid, name, archived, last_message_at FROM chats WHERE jid LIKE '%@lid'`)
	if err != nil {
		return err
	}
	var chats []lidChat
	for rows.Next() {
		var c lidChat
		if err := rows.Scan(&c.jid, &c.name, &c.archived, &c.lastMessageAt); err != nil {
			_ = rows.Close()
			return err
		}
		chats = append(chats, c)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, c := range chats {
		pn := resolve(c.jid)
		if pn == "" {
			continue
		}
		if _, err := tx.Exec(`
			INSERT INTO chats (jid, name, is_group, archived, last_message_at)
			VALUES (?, ?, 0, ?, ?)
			ON CONFLICT (jid) DO UPDATE SET
				name = CASE WHEN chats.name <> '' THEN chats.name ELSE excluded.name END,
				last_message_at = MAX(chats.last_message_at, excluded.last_message_at)`,
			pn, c.name, c.archived, c.lastMessageAt,
		); err != nil {
			return err
		}
		res, err := tx.Exec(`UPDATE OR IGNORE messages SET chat_jid = ? WHERE chat_jid = ?`, pn, c.jid)
		if err != nil {
			return err
		}
		moved, _ := res.RowsAffected()
		stats.Messages += int(moved)
		// Whatever is left under the LID key collided with a copy the
		// phone chat already had — the same message seen both ways.
		if _, err := tx.Exec(`DELETE FROM messages WHERE chat_jid = ?`, c.jid); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM chats WHERE jid = ?`, c.jid); err != nil {
			return err
		}
		if err := upsertLIDMappingTx(tx, c.jid, pn); err != nil {
			return err
		}
		stats.Chats++
	}
	return nil
}

func foldLIDSenders(tx *sql.Tx, resolve func(string) string, stats *FoldStats) error {
	senders, err := distinctStrings(tx, `SELECT DISTINCT sender_jid FROM messages WHERE sender_jid LIKE '%@lid'`)
	if err != nil {
		return err
	}
	for _, sender := range senders {
		bare := bareLID(sender)
		pn := resolve(bare)
		if pn == "" {
			continue
		}
		res, err := tx.Exec(`UPDATE messages SET sender_jid = ? WHERE sender_jid = ?`, pn, sender)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		stats.Messages += int(n)
		for _, lid := range []string{sender, bare} {
			if err := upsertLIDMappingTx(tx, lid, pn); err != nil {
				return err
			}
		}
	}
	return nil
}

func foldLIDContacts(tx *sql.Tx, resolve func(string) string, stats *FoldStats) error {
	rows, err := tx.Query(`SELECT jid, push_name, full_name, business_name FROM contacts WHERE jid LIKE '%@lid'`)
	if err != nil {
		return err
	}
	type lidContact struct{ jid, push, full, business string }
	var contacts []lidContact
	for rows.Next() {
		var c lidContact
		if err := rows.Scan(&c.jid, &c.push, &c.full, &c.business); err != nil {
			_ = rows.Close()
			return err
		}
		contacts = append(contacts, c)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, c := range contacts {
		pn := resolve(bareLID(c.jid))
		if pn == "" {
			continue
		}
		if _, err := tx.Exec(`
			INSERT INTO contacts (jid, phone, push_name, full_name, business_name)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT (jid) DO UPDATE SET
				phone = CASE WHEN contacts.phone <> '' THEN contacts.phone ELSE excluded.phone END,
				push_name = CASE WHEN contacts.push_name <> '' THEN contacts.push_name ELSE excluded.push_name END,
				full_name = CASE WHEN contacts.full_name <> '' THEN contacts.full_name ELSE excluded.full_name END,
				business_name = CASE WHEN contacts.business_name <> '' THEN contacts.business_name ELSE excluded.business_name END`,
			pn, phoneDigits(pn), c.push, c.full, c.business,
		); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM contacts WHERE jid = ?`, c.jid); err != nil {
			return err
		}
		stats.Contacts++
	}
	return nil
}

func foldLIDCalls(tx *sql.Tx, resolve func(string) string, stats *FoldStats) error {
	peers, err := distinctStrings(tx, `SELECT DISTINCT peer_jid FROM calls WHERE peer_jid LIKE '%@lid'`)
	if err != nil {
		return err
	}
	for _, peer := range peers {
		pn := resolve(bareLID(peer))
		if pn == "" {
			continue
		}
		res, err := tx.Exec(`UPDATE calls SET peer_jid = ? WHERE peer_jid = ?`, pn, peer)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		stats.Calls += int(n)
	}
	return nil
}

func upsertLIDMappingTx(tx *sql.Tx, lid, pn string) error {
	_, err := tx.Exec(`
		INSERT INTO lid_map (lid, pn) VALUES (?, ?)
		ON CONFLICT (lid) DO UPDATE SET pn = excluded.pn`,
		lid, pn,
	)
	return err
}

func distinctStrings(tx *sql.Tx, query string) ([]string, error) {
	rows, err := tx.Query(query)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// bareLID strips the device suffix from a LID JID ("user:6@lid" →
// "user@lid"); the pairing is per user, not per device.
func bareLID(jid string) string {
	user, server, ok := strings.Cut(jid, "@")
	if !ok {
		return jid
	}
	if i := strings.IndexByte(user, ':'); i >= 0 {
		user = user[:i]
	}
	return user + "@" + server
}

// phoneDigits is the phone number a phone JID's local part spells, or ""
// when it is not all digits.
func phoneDigits(pn string) string {
	user, server, ok := strings.Cut(pn, "@")
	if !ok || server != "s.whatsapp.net" || user == "" {
		return ""
	}
	for _, r := range user {
		if r < '0' || r > '9' {
			return ""
		}
	}
	return user
}

// PruneStubMessages deletes message rows that carry nothing a reader could
// use — kind "other" with no text and no media — and then any LID direct
// chat left with no messages at all. Such rows come from history-sync
// entries that are stubs (a status line WhatsApp keeps in the chat, with
// no message payload); the ingest path now skips those, and this clears
// the ones stored before it did. Returns how many messages and chats went.
func (s *Store) PruneStubMessages() (messages, chats int, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, 0, fmt.Errorf("prune stub messages: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.Exec(`DELETE FROM messages WHERE kind = 'other' AND text = '' AND media_ref IS NULL`)
	if err != nil {
		return 0, 0, fmt.Errorf("prune stub messages: %w", err)
	}
	m, _ := res.RowsAffected()
	res, err = tx.Exec(`
		DELETE FROM chats WHERE jid LIKE '%@lid' AND is_group = 0
		  AND NOT EXISTS (SELECT 1 FROM messages WHERE messages.chat_jid = chats.jid)`)
	if err != nil {
		return 0, 0, fmt.Errorf("prune stub messages: %w", err)
	}
	c, _ := res.RowsAffected()
	if err := tx.Commit(); err != nil {
		return 0, 0, fmt.Errorf("prune stub messages: %w", err)
	}
	return int(m), int(c), nil
}
