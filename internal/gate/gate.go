// Package gate is the only path by which an outbound WhatsApp send reaches
// the bridge. It enforces the draft-then-commit preview flow for untrusted
// recipients, auto-commit for trusted recipients, and a hard rate-limit
// floor ahead of every delivery attempt.
package gate

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

const (
	// draftTTL is how long an uncommitted draft stays valid.
	draftTTL = 5 * time.Minute
	// draftCap bounds the in-memory draft map; the oldest draft is
	// evicted once it is exceeded.
	draftCap = 32
	// floorSeconds is the minimum interval between deliveries; no
	// configuration can go below it.
	floorSeconds = 5
)

// errDraftInvalid is returned when a draft token does not match a live,
// byte-identical draft. It is deliberately generic so callers cannot use it
// to probe whether a token ever existed.
var errDraftInvalid = errors.New("draft expired or content differs — re-issue the send to get a fresh preview")

// Deliverer is implemented by the bridge's send side (Task 8). It performs
// the actual WhatsApp send and must not be reachable except through Gate.
type Deliverer interface {
	Deliver(ctx context.Context, d Delivery) (messageID string, err error)
}

// Delivery describes one outbound action, spanning every kind the gate
// covers so a single rate limiter and draft flow guards all of them.
type Delivery struct {
	Kind       string // text|media|voice|reaction|read
	To         string // JID
	Text       string // body, caption, or emoji
	Path       string // media/voice source file
	QuotedID   string
	MessageIDs []string // read receipts
}

// Result is the outcome of Submit. Preview is always set; DraftToken is set
// only when Sent is false.
type Result struct {
	Sent       bool
	MessageID  string
	DraftToken string // set when Sent == false
	Preview    string // recipient name+jid and exact content, always set
}

type draftEntry struct {
	delivery Delivery
	expires  time.Time
}

// Gate holds pending drafts and the rate limiter guarding every delivery.
// Zero value is not usable; construct with New.
type Gate struct {
	deliver    Deliverer
	trusted    func(jid string) bool
	limiter    *rate.Limiter
	perSeconds int
	now        func() time.Time

	mu     sync.Mutex
	drafts map[string]*draftEntry
	order  []string // draft tokens in insertion order, oldest first
}

// New builds a Gate. perSeconds below the 5s floor is silently raised to it;
// now supplies the clock the limiter and draft TTL are measured against, so
// tests can drive it without sleeping.
func New(d Deliverer, trusted func(jid string) bool, burst, perSeconds int, now func() time.Time) *Gate {
	if perSeconds < floorSeconds {
		perSeconds = floorSeconds
	}
	return &Gate{
		deliver:    d,
		trusted:    trusted,
		limiter:    rate.NewLimiter(rate.Every(time.Duration(perSeconds)*time.Second), burst),
		perSeconds: perSeconds,
		now:        now,
		drafts:     make(map[string]*draftEntry),
	}
}

// Submit runs one send through the gate. With no draftToken it either
// auto-commits (trusted recipient) or stores a draft and returns its token
// for the caller to re-submit. With draftToken it commits an existing draft
// if the content matches exactly and has not expired. Every path that
// reaches the deliverer takes a rate-limit token first; Submit never blocks
// waiting for one — it returns an error instead.
func (g *Gate) Submit(ctx context.Context, d Delivery, draftToken string, resolveName func(jid string) string) (Result, error) {
	name := ""
	if resolveName != nil {
		name = resolveName(d.To)
	}
	preview := buildPreview(name, d)

	if draftToken != "" {
		return g.commit(ctx, d, draftToken, preview)
	}

	// Read receipts have no draft_token parameter on the MCP tool side
	// (ARCHITECTURE.md §6), so they can never be re-submitted to commit a
	// draft — they must always deliver on the first call, trusted or not.
	// They still consume a limiter token like any other delivery.
	if d.Kind == "read" || (g.trusted != nil && g.trusted(d.To)) {
		return g.deliverNow(ctx, d, preview)
	}

	return g.createDraft(d, preview)
}

func (g *Gate) createDraft(d Delivery, preview string) (Result, error) {
	token, err := newDraftToken(d)
	if err != nil {
		return Result{}, fmt.Errorf("generate draft token: %w", err)
	}

	now := g.now()
	g.mu.Lock()
	g.storeDraftLocked(now, token, d)
	g.mu.Unlock()

	return Result{Sent: false, DraftToken: token, Preview: preview}, nil
}

func (g *Gate) commit(ctx context.Context, d Delivery, token, preview string) (Result, error) {
	now := g.now()

	// Validate, rate-limit-check, and claim the draft as ONE critical
	// section. AllowN is non-blocking, so holding the lock across it is
	// cheap. Committing this way means a rate-limited attempt never
	// touches g.drafts or g.order at all — the draft is simply never
	// removed, so it keeps its original expiry and its place in the
	// eviction order (no separate "restore" step, and no window where a
	// concurrent createDraft could see it missing from g.drafts while it
	// is still present in g.order). The draft is deleted only once, at
	// the moment a commit is actually going to proceed to Deliver, so it
	// can never be committed twice.
	g.mu.Lock()
	entry, ok := g.drafts[token]
	if ok && (!entry.expires.After(now) || !reflect.DeepEqual(entry.delivery, d)) {
		ok = false
	}
	if !ok {
		g.mu.Unlock()
		return Result{}, errDraftInvalid
	}
	if !g.limiter.AllowN(now, 1) {
		g.mu.Unlock()
		return Result{}, g.rateLimitError()
	}
	delete(g.drafts, token)
	g.mu.Unlock()

	id, err := g.deliver.Deliver(ctx, d)
	if err != nil {
		// Ambiguous: the send may have partially happened. Keep the draft
		// consumed rather than risk a duplicate delivery on retry.
		return Result{}, fmt.Errorf("deliver message: %w", err)
	}
	return Result{Sent: true, MessageID: id, Preview: preview}, nil
}

func (g *Gate) deliverNow(ctx context.Context, d Delivery, preview string) (Result, error) {
	now := g.now()
	if !g.limiter.AllowN(now, 1) {
		return Result{}, g.rateLimitError()
	}

	id, err := g.deliver.Deliver(ctx, d)
	if err != nil {
		return Result{}, fmt.Errorf("deliver message: %w", err)
	}
	return Result{Sent: true, MessageID: id, Preview: preview}, nil
}

func (g *Gate) rateLimitError() error {
	return fmt.Errorf("rate limit reached — retry after %ds", g.perSeconds)
}

// storeDraftLocked prunes expired drafts, evicts the oldest one if the
// store is at capacity, then inserts token. Callers must hold g.mu.
func (g *Gate) storeDraftLocked(now time.Time, token string, d Delivery) {
	g.pruneExpiredLocked(now)
	if len(g.order) >= draftCap {
		g.evictOldestLocked()
	}
	g.drafts[token] = &draftEntry{delivery: d, expires: now.Add(draftTTL)}
	g.order = append(g.order, token)
}

// pruneExpiredLocked drops expired drafts from the front of g.order.
// Insertion order and expiry order coincide because TTL is a constant
// offset from a non-decreasing clock, so expired entries are always a
// prefix of g.order. Callers must hold g.mu.
func (g *Gate) pruneExpiredLocked(now time.Time) {
	for len(g.order) > 0 {
		entry, ok := g.drafts[g.order[0]]
		if ok && entry.expires.After(now) {
			break
		}
		delete(g.drafts, g.order[0])
		g.order = g.order[1:]
	}
}

// evictOldestLocked drops the single oldest draft. Callers must hold g.mu.
func (g *Gate) evictOldestLocked() {
	if len(g.order) == 0 {
		return
	}
	delete(g.drafts, g.order[0])
	g.order = g.order[1:]
}

// newDraftToken derives a draft token per ARCHITECTURE.md §5:
// hex(sha256(to || sha256(kind|text|path|quoted|ids) || nonce)).
func newDraftToken(d Delivery) (string, error) {
	content := sha256.Sum256([]byte(strings.Join(
		[]string{d.Kind, d.Text, d.Path, d.QuotedID, strings.Join(d.MessageIDs, ",")}, "|",
	)))

	nonce := make([]byte, 8)
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}

	buf := make([]byte, 0, len(d.To)+len(content)+len(nonce))
	buf = append(buf, d.To...)
	buf = append(buf, content[:]...)
	buf = append(buf, nonce...)

	sum := sha256.Sum256(buf)
	return hex.EncodeToString(sum[:]), nil
}

// buildPreview renders the recipient and exact outbound content so the
// caller can show it to the user before committing a draft.
func buildPreview(name string, d Delivery) string {
	who := d.To
	if name != "" {
		who = fmt.Sprintf("%s (%s)", name, d.To)
	}

	parts := []string{who, d.Kind}
	if d.Text != "" {
		parts = append(parts, fmt.Sprintf("text=%q", d.Text))
	}
	if d.Path != "" {
		parts = append(parts, fmt.Sprintf("path=%q", d.Path))
	}
	if d.QuotedID != "" {
		parts = append(parts, fmt.Sprintf("quoted=%s", d.QuotedID))
	}
	if len(d.MessageIDs) > 0 {
		parts = append(parts, fmt.Sprintf("ids=%s", strings.Join(d.MessageIDs, ",")))
	}
	return strings.Join(parts, " ")
}
