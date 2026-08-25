package bridge

import (
	"context"

	"go.mau.fi/whatsmeow/types"

	"github.com/idle-sync/whatsapp-connect-mcp/internal/store"
)

// WhatsApp addresses a growing share of users by a privacy LID
// ("...@lid") instead of their phone JID, and the same person can arrive
// both ways — as the sender of one message and the LID sender of the next,
// or as a phone-number chat and a LID chat side by side. The store is keyed
// on the phone number: every LID the bridge can resolve is rewritten to it
// before anything is written, using the pairings messages carry plus the
// LID↔phone map whatsmeow maintains in the session store (fed by history
// sync, contact sync and the messages themselves). What cannot be resolved
// stays a LID and is folded later, once a pairing turns up.

// phoneJID returns jid's phone-number identity when jid is a LID with a
// known pairing, and jid unchanged otherwise. A resolved LID comes back
// device-stripped: the pairing is per user, and one contact row per person
// is the point.
func (b *Bridge) phoneJID(jid types.JID) types.JID {
	if jid.Server != types.HiddenUserServer {
		return jid
	}
	bare := jid.ToNonAD()
	if v, ok := b.lidCache.Load(bare.User); ok {
		return v.(types.JID)
	}
	dev := b.wa().Store
	if dev == nil || dev.LIDs == nil {
		return jid // an unpaired device has no LID map yet
	}
	pn, err := dev.LIDs.GetPNForLID(context.Background(), bare)
	if err != nil || pn.IsEmpty() {
		return jid
	}
	pn = pn.ToNonAD()
	b.lidCache.Store(bare.User, pn)
	return pn
}

// phoneJIDString is phoneJID over the string form, for the call sites that
// only hold a rendered JID. An unparseable string is returned as is.
func (b *Bridge) phoneJIDString(jid string) string {
	parsed, err := types.ParseJID(jid)
	if err != nil {
		return jid
	}
	return b.phoneJID(parsed).String()
}

// rememberLID caches a pairing learned from a message, so the lookup that
// follows in the same ingest — and every later one — never hits the
// session store for it. Both JIDs are taken bare.
func (b *Bridge) rememberLID(lid, pn types.JID) {
	if lid.Server != types.HiddenUserServer || pn.Server != types.DefaultUserServer {
		return
	}
	b.lidCache.Store(lid.ToNonAD().User, pn.ToNonAD())
}

// chatPairing extracts the LID↔phone-number pairing a direct chat's
// addresses carry, if any: the chat JID and the recipient's alternative
// address, in either order. ok is false for groups and for sources with no
// alternative recipient address.
func chatPairing(src types.MessageSource) (lid, pn types.JID, ok bool) {
	if src.IsGroup || src.RecipientAlt.IsEmpty() {
		return types.EmptyJID, types.EmptyJID, false
	}
	switch {
	case src.Chat.Server == types.HiddenUserServer && src.RecipientAlt.Server == types.DefaultUserServer:
		return src.Chat.ToNonAD(), src.RecipientAlt.ToNonAD(), true
	case src.RecipientAlt.Server == types.HiddenUserServer && src.Chat.Server == types.DefaultUserServer:
		return src.RecipientAlt.ToNonAD(), src.Chat.ToNonAD(), true
	}
	return types.EmptyJID, types.EmptyJID, false
}

// FoldLIDs rewrites every LID identity already in the store whose phone
// number is now known into that phone number — see store.FoldLIDs. serve
// runs it at startup, and the bridge runs it after each history sync,
// since a pairing often arrives later than the first message that needed
// it. Reads only the local session store, never the network.
func (b *Bridge) FoldLIDs() (store.FoldStats, error) {
	return b.store.FoldLIDs(func(lid string) string {
		parsed, err := types.ParseJID(lid)
		if err != nil || parsed.Server != types.HiddenUserServer {
			return ""
		}
		pn := b.phoneJID(parsed)
		if pn.Server != types.DefaultUserServer {
			return ""
		}
		return pn.String()
	})
}
