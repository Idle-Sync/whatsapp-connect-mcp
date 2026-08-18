package store

import (
	"regexp"
	"strings"
)

// mentionRE matches a standalone WhatsApp mention token: "@" plus the
// mentioned account's numeric local part (a phone number or a privacy
// LID), at the start of the text or after whitespace. The leading guard is
// what keeps an email address ("bob@123456.com") or a mid-word "@" from
// ever being rewritten; the 5-digit minimum keeps casual text like "@123"
// out.
var mentionRE = regexp.MustCompile(`(^|\s)@(\d{5,})`)

// resolveMentionRows rewrites the "@<digits>" mention tokens in every
// row's text to "@<name>", resolved through the same chain sender names
// use: a contact on the LID itself, the lid_map's phone contact, a
// contact on the digits as a phone JID, then the mapped phone number's
// digits — an unresolvable token is left exactly as it arrived. One cache
// spans the whole result set, so a token mentioned in fifty rows costs
// one lookup.
func (s *Store) resolveMentionRows(rows []MessageRow) {
	cache := map[string]string{}
	for i := range rows {
		rows[i].Text = s.resolveMentions(rows[i].Text, cache)
	}
}

// resolveMentions rewrites the mention tokens in one text. cache maps
// digits to their resolved name ("" for unresolvable, cached too).
func (s *Store) resolveMentions(text string, cache map[string]string) string {
	if !strings.Contains(text, "@") {
		return text
	}
	return mentionRE.ReplaceAllStringFunc(text, func(m string) string {
		at := strings.Index(m, "@")
		prefix, digits := m[:at], m[at+1:]

		name, ok := cache[digits]
		if !ok {
			name = s.mentionName(digits)
			cache[digits] = name
		}
		if name == "" {
			return m
		}
		return prefix + "@" + name
	})
}

// mentionName resolves one mention's digits to a display name, or "" when
// nothing on record improves on the raw token. Names are flattened of
// tabs, since rendered rows are tab-separated.
func (s *Store) mentionName(digits string) string {
	lid := digits + "@lid"

	var pn string
	_ = s.db.QueryRow(`SELECT pn FROM lid_map WHERE lid = ?`, lid).Scan(&pn)

	candidates := []string{lid}
	if pn != "" {
		candidates = append(candidates, pn)
	}
	candidates = append(candidates, digits+"@s.whatsapp.net")

	for _, jid := range candidates {
		var full, push, business string
		if err := s.db.QueryRow(
			`SELECT full_name, push_name, business_name FROM contacts WHERE jid = ?`, jid,
		).Scan(&full, &push, &business); err != nil {
			continue
		}
		for _, name := range []string{full, push, business} {
			if name != "" {
				return strings.ReplaceAll(name, "\t", " ")
			}
		}
	}

	// No contact anywhere, but a mapped phone number still beats a LID.
	if pn != "" {
		if i := strings.Index(pn, "@"); i > 0 {
			return pn[:i]
		}
	}
	return ""
}
