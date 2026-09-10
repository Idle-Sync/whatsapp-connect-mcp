package mcpserv

import (
	"errors"
	"sort"
	"strings"

	"github.com/idle-sync/whatsapp-connect-mcp/internal/store"
)

// Scope decides which chats the MCP tool surface may read. Satisfied by
// *config.ScopeReader; the methods are answered per call, so editing the
// scope takes effect without a restart.
type Scope interface {
	// Readable reports whether chat jid may be read.
	Readable(jid string) bool
	// Active reports whether an allowlist is in force. False means
	// every chat is readable and no filtering is needed.
	Active() bool
	// List returns the configured allowlist.
	List() []string
}

// ErrOutOfScope is returned by every chat-addressed read when the chat is
// outside the configured scope. It says the chat is off limits rather than
// that it does not exist: an agent that cannot tell the two apart will
// retry, re-fetch, and generally waste the user's time trying to work
// around a deliberate setting.
var ErrOutOfScope = errors.New("this chat is outside the chats this server is allowed to read")

// scopedStore wraps a Store so every read is confined to the allowed
// chats. Chat-addressed calls fail with ErrOutOfScope; calls that sweep
// across chats have their results filtered. Aggregates that carry no
// message content (LatestRowID, QuickCheck) pass through untouched.
//
// The dashboard is NOT wrapped: it talks to the *store.Store directly,
// because it is the local human's own view of their own messages.
type scopedStore struct {
	Store
	scope Scope
}

// newScopedStore returns st unchanged when no scope is configured, so the
// default path carries no wrapper and no per-call cost.
func newScopedStore(st Store, scope Scope) Store {
	if scope == nil {
		return st
	}
	return &scopedStore{Store: st, scope: scope}
}

// allow is the gate every chat-addressed method runs first.
func (s *scopedStore) allow(chatJID string) error {
	if s.scope.Readable(chatJID) {
		return nil
	}
	return ErrOutOfScope
}

func (s *scopedStore) Chat(jid string) (store.ChatRow, bool, error) {
	if err := s.allow(jid); err != nil {
		return store.ChatRow{}, false, err
	}
	return s.Store.Chat(jid)
}

// Chats lists only the allowed chats. When a scope is set it resolves the
// allowlist directly rather than filtering a recency-ordered page: the
// underlying query caps at store.MaxLimit, so an allowed chat that has
// been quiet for a while would otherwise fall off the end and look as
// though it had been revoked.
func (s *scopedStore) Chats(query string, includeArchived bool, limit int) ([]store.ChatRow, error) {
	if !s.scope.Active() {
		return s.Store.Chats(query, includeArchived, limit)
	}
	needle := strings.ToLower(query)
	var out []store.ChatRow
	for _, jid := range s.scope.List() {
		row, ok, err := s.Store.Chat(jid)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue // allowlisted but no such chat in the store yet
		}
		if !includeArchived && row.Archived {
			continue
		}
		if needle != "" && !strings.Contains(strings.ToLower(row.Name), needle) {
			continue
		}
		out = append(out, row)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].LastMessageAt > out[j].LastMessageAt })
	if n := store.ClampLimit(limit); len(out) > n {
		out = out[:n]
	}
	return out, nil
}

func (s *scopedStore) Messages(chatJID string, beforeTS, afterTS int64, limit int) ([]store.MessageRow, error) {
	if err := s.allow(chatJID); err != nil {
		return nil, err
	}
	return s.Store.Messages(chatJID, beforeTS, afterTS, limit)
}

// SearchMessages narrowed to one chat is a chat-addressed read; across all
// chats it is a sweep, and the results are filtered.
func (s *scopedStore) SearchMessages(query, chatJID string, limit int) ([]store.MessageRow, error) {
	if chatJID != "" {
		if err := s.allow(chatJID); err != nil {
			return nil, err
		}
		return s.Store.SearchMessages(query, chatJID, limit)
	}
	rows, err := s.Store.SearchMessages(query, "", limit)
	if err != nil {
		return nil, err
	}
	return s.keepAllowed(rows), nil
}

func (s *scopedStore) MessageContext(chatJID, id string, before, after int) ([]store.MessageRow, error) {
	if err := s.allow(chatJID); err != nil {
		return nil, err
	}
	return s.Store.MessageContext(chatJID, id, before, after)
}

// SearchContacts hides contacts whose one-to-one chat is out of scope. A
// scoped agent that could still enumerate the address book would leak
// exactly the thing the scope exists to withhold: who the user talks to.
func (s *scopedStore) SearchContacts(query string, limit int) ([]store.ContactRow, error) {
	rows, err := s.Store.SearchContacts(query, limit)
	if err != nil || !s.scope.Active() {
		return rows, err
	}
	out := rows[:0]
	for _, c := range rows {
		if s.scope.Readable(c.JID) {
			out = append(out, c)
		}
	}
	return out, nil
}

func (s *scopedStore) LastInteraction(jid string) (store.MessageRow, bool, error) {
	if err := s.allow(jid); err != nil {
		return store.MessageRow{}, false, err
	}
	return s.Store.LastInteraction(jid)
}

func (s *scopedStore) OldestMessage(chatJID string) (store.MessageRow, bool, error) {
	if err := s.allow(chatJID); err != nil {
		return store.MessageRow{}, false, err
	}
	return s.Store.OldestMessage(chatJID)
}

func (s *scopedStore) CountMessagesOlderThan(chatJID string, ts int64) (int, error) {
	if err := s.allow(chatJID); err != nil {
		return 0, err
	}
	return s.Store.CountMessagesOlderThan(chatJID, ts)
}

// MessagesAfterRowID is the poll loop's feed. With an empty chatJID it
// watches every chat, so the rows are filtered — but the returned cursor
// is the underlying one, unfiltered, so polling still advances past
// messages in chats this agent cannot see instead of stalling on them.
func (s *scopedStore) MessagesAfterRowID(chatJID string, afterRowID int64, includeOwn bool, limit int) ([]store.MessageRow, int64, error) {
	if chatJID != "" {
		if err := s.allow(chatJID); err != nil {
			return nil, 0, err
		}
		return s.Store.MessagesAfterRowID(chatJID, afterRowID, includeOwn, limit)
	}
	rows, cursor, err := s.Store.MessagesAfterRowID("", afterRowID, includeOwn, limit)
	if err != nil {
		return nil, 0, err
	}
	return s.keepAllowed(rows), cursor, nil
}

func (s *scopedStore) TailRowID(chatJID string, includeOwn bool, n int) (int64, error) {
	if chatJID != "" {
		if err := s.allow(chatJID); err != nil {
			return 0, err
		}
	}
	return s.Store.TailRowID(chatJID, includeOwn, n)
}

func (s *scopedStore) Calls(peerJID string, beforeTS, afterTS int64, limit int) ([]store.CallRow, error) {
	if peerJID != "" {
		if err := s.allow(peerJID); err != nil {
			return nil, err
		}
		return s.Store.Calls(peerJID, beforeTS, afterTS, limit)
	}
	rows, err := s.Store.Calls("", beforeTS, afterTS, limit)
	if err != nil || !s.scope.Active() {
		return rows, err
	}
	out := rows[:0]
	for _, c := range rows {
		if s.scope.Readable(c.PeerJID) {
			out = append(out, c)
		}
	}
	return out, nil
}

func (s *scopedStore) MessageMediaRef(chatJID, id string) ([]byte, string, string, error) {
	if err := s.allow(chatJID); err != nil {
		return nil, "", "", err
	}
	return s.Store.MessageMediaRef(chatJID, id)
}

func (s *scopedStore) MediaMessageIDs(chatJID string, beforeTS, afterTS int64, kind string, limit int) ([]string, error) {
	if err := s.allow(chatJID); err != nil {
		return nil, err
	}
	return s.Store.MediaMessageIDs(chatJID, beforeTS, afterTS, kind, limit)
}

// keepAllowed filters message rows in place; unscoped it is a no-op.
func (s *scopedStore) keepAllowed(rows []store.MessageRow) []store.MessageRow {
	if !s.scope.Active() {
		return rows
	}
	out := rows[:0]
	for _, m := range rows {
		if s.scope.Readable(m.ChatJID) {
			out = append(out, m)
		}
	}
	return out
}
