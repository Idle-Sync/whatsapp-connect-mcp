package gate

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeClock is a manually advanced clock so tests never sleep.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock(start time.Time) *fakeClock {
	return &fakeClock{now: start}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// fakeDeliverer records every Delivery handed to it and returns a
// deterministic message ID.
type fakeDeliverer struct {
	mu        sync.Mutex
	delivered []Delivery
	nextID    int
	// validateErr, when set, is returned by Validate for every delivery, so
	// tests can prove the gate refuses ahead of drafting and rate limiting.
	validateErr error
}

func (f *fakeDeliverer) Validate(_ Delivery) error { return f.validateErr }

func (f *fakeDeliverer) Deliver(_ context.Context, d Delivery) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.delivered = append(f.delivered, d)
	f.nextID++
	return "msg-" + itoa(f.nextID), nil
}

func (f *fakeDeliverer) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.delivered)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

func trustNone(string) bool { return false }

func trustOnly(jid string) func(string) bool {
	return func(candidate string) bool { return candidate == jid }
}

func resolveNoName(string) string { return "" }

func TestSubmitUntrustedTwoStepHappyPath(t *testing.T) {
	clock := newFakeClock(time.Unix(0, 0))
	deliverer := &fakeDeliverer{}
	g := New(deliverer, trustNone, 3, 12, clock.Now)

	d := Delivery{Kind: "text", To: "111@s.whatsapp.net", Text: "hello there"}

	first, err := g.Submit(context.Background(), d, "", resolveNoName)
	if err != nil {
		t.Fatalf("first Submit() error: %v", err)
	}
	if first.Sent {
		t.Fatalf("first Submit() Sent = true, want false (draft only)")
	}
	if first.DraftToken == "" {
		t.Fatal("first Submit() DraftToken empty, want a token")
	}
	if first.Preview == "" {
		t.Fatal("first Submit() Preview empty, want a preview")
	}
	if deliverer.count() != 0 {
		t.Fatalf("deliverer called %d times after draft, want 0", deliverer.count())
	}

	second, err := g.Submit(context.Background(), d, first.DraftToken, resolveNoName)
	if err != nil {
		t.Fatalf("second Submit() error: %v", err)
	}
	if !second.Sent {
		t.Fatal("second Submit() Sent = false, want true")
	}
	if second.MessageID == "" {
		t.Fatal("second Submit() MessageID empty, want a message id")
	}
	if second.Preview == "" {
		t.Fatal("second Submit() Preview empty, want a preview")
	}
	if deliverer.count() != 1 {
		t.Fatalf("deliverer called %d times after commit, want 1", deliverer.count())
	}
}

func TestSubmitAlteredContentWithValidTokenErrors(t *testing.T) {
	clock := newFakeClock(time.Unix(0, 0))
	deliverer := &fakeDeliverer{}
	g := New(deliverer, trustNone, 3, 12, clock.Now)

	d := Delivery{Kind: "text", To: "111@s.whatsapp.net", Text: "hello there"}

	draft, err := g.Submit(context.Background(), d, "", resolveNoName)
	if err != nil {
		t.Fatalf("draft Submit() error: %v", err)
	}

	altered := d
	altered.Text = "hello there!!!"

	_, err = g.Submit(context.Background(), altered, draft.DraftToken, resolveNoName)
	if err == nil {
		t.Fatal("Submit() with altered content error = nil, want error")
	}
	if !strings.Contains(err.Error(), "draft expired or content differs") {
		t.Fatalf("Submit() error = %q, want it to mention draft expired or content differs", err.Error())
	}
	if deliverer.count() != 0 {
		t.Fatalf("deliverer called %d times, want 0 (nothing delivered)", deliverer.count())
	}
}

func TestSubmitAlteredAuthorWithValidTokenErrors(t *testing.T) {
	clock := newFakeClock(time.Unix(0, 0))
	deliverer := &fakeDeliverer{}
	g := New(deliverer, trustNone, 3, 12, clock.Now)

	d := Delivery{Kind: "reaction", To: "111@s.whatsapp.net", Text: "👍", QuotedID: "msg1", Author: "111@s.whatsapp.net"}

	draft, err := g.Submit(context.Background(), d, "", resolveNoName)
	if err != nil {
		t.Fatalf("draft Submit() error: %v", err)
	}

	altered := d
	altered.Author = "222@s.whatsapp.net"

	_, err = g.Submit(context.Background(), altered, draft.DraftToken, resolveNoName)
	if err == nil {
		t.Fatal("Submit() with altered author error = nil, want error")
	}
	if !strings.Contains(err.Error(), "draft expired or content differs") {
		t.Fatalf("Submit() error = %q, want it to mention draft expired or content differs", err.Error())
	}
	if deliverer.count() != 0 {
		t.Fatalf("deliverer called %d times, want 0 (nothing delivered): Author must be part of the compared draft content", deliverer.count())
	}

	// The original, unaltered delivery still commits: Author alone
	// changing is what must be rejected, not any and all commits.
	committed, err := g.Submit(context.Background(), d, draft.DraftToken, resolveNoName)
	if err != nil {
		t.Fatalf("Submit() with unaltered author error: %v", err)
	}
	if !committed.Sent {
		t.Fatal("Submit() with unaltered author Sent = false, want true")
	}
}

func TestSubmitExpiredDraftErrors(t *testing.T) {
	clock := newFakeClock(time.Unix(0, 0))
	deliverer := &fakeDeliverer{}
	g := New(deliverer, trustNone, 3, 12, clock.Now)

	d := Delivery{Kind: "text", To: "111@s.whatsapp.net", Text: "hello there"}

	draft, err := g.Submit(context.Background(), d, "", resolveNoName)
	if err != nil {
		t.Fatalf("draft Submit() error: %v", err)
	}

	clock.Advance(6 * time.Minute)

	_, err = g.Submit(context.Background(), d, draft.DraftToken, resolveNoName)
	if err == nil {
		t.Fatal("Submit() with expired draft error = nil, want error")
	}
	if !strings.Contains(err.Error(), "draft expired or content differs") {
		t.Fatalf("Submit() error = %q, want it to mention draft expired or content differs", err.Error())
	}
	if deliverer.count() != 0 {
		t.Fatalf("deliverer called %d times, want 0 (nothing delivered)", deliverer.count())
	}
}

func TestSubmitTrustedSendsOnFirstCall(t *testing.T) {
	clock := newFakeClock(time.Unix(0, 0))
	deliverer := &fakeDeliverer{}
	g := New(deliverer, trustOnly("222@s.whatsapp.net"), 3, 12, clock.Now)

	d := Delivery{Kind: "text", To: "222@s.whatsapp.net", Text: "trusted send"}

	result, err := g.Submit(context.Background(), d, "", func(string) string { return "Alice" })
	if err != nil {
		t.Fatalf("Submit() error: %v", err)
	}
	if !result.Sent {
		t.Fatal("Submit() Sent = false, want true for trusted recipient")
	}
	if result.MessageID == "" {
		t.Fatal("Submit() MessageID empty, want a message id")
	}
	if !strings.Contains(result.Preview, "Alice") || !strings.Contains(result.Preview, "222@s.whatsapp.net") {
		t.Fatalf("Submit() Preview = %q, want it to contain recipient name and jid", result.Preview)
	}
	if deliverer.count() != 1 {
		t.Fatalf("deliverer called %d times, want 1", deliverer.count())
	}
}

func TestSubmitUntrustedReadDeliversImmediatelyAndConsumesLimiterToken(t *testing.T) {
	clock := newFakeClock(time.Unix(0, 0))
	deliverer := &fakeDeliverer{}
	g := New(deliverer, trustNone, 1, 12, clock.Now)

	first := Delivery{Kind: "read", To: "111@s.whatsapp.net", MessageIDs: []string{"a"}}
	result, err := g.Submit(context.Background(), first, "", resolveNoName)
	if err != nil {
		t.Fatalf("Submit() error: %v", err)
	}
	if !result.Sent {
		t.Fatal("Submit() Sent = false, want true for a read receipt (no draft_token round trip exists)")
	}
	if result.DraftToken != "" {
		t.Fatalf("Submit() DraftToken = %q, want empty for an immediate send", result.DraftToken)
	}
	if deliverer.count() != 1 {
		t.Fatalf("deliverer called %d times, want 1", deliverer.count())
	}

	// The single burst token was consumed by the read above, so a second
	// read for the same untrusted JID must be rate limited, proving the
	// read path takes a limiter token like any other delivery.
	second := Delivery{Kind: "read", To: "111@s.whatsapp.net", MessageIDs: []string{"b"}}
	_, err = g.Submit(context.Background(), second, "", resolveNoName)
	if err == nil {
		t.Fatal("Submit() second read error = nil, want rate limit error")
	}
	if !strings.Contains(err.Error(), "rate limit reached") {
		t.Fatalf("Submit() error = %q, want it to mention rate limit reached", err.Error())
	}
	if deliverer.count() != 1 {
		t.Fatalf("deliverer called %d times, want still 1", deliverer.count())
	}
}

func TestSubmitCommittedTokenCannotBeReused(t *testing.T) {
	clock := newFakeClock(time.Unix(0, 0))
	deliverer := &fakeDeliverer{}
	g := New(deliverer, trustNone, 3, 12, clock.Now)

	d := Delivery{Kind: "text", To: "111@s.whatsapp.net", Text: "hello there"}

	draft, err := g.Submit(context.Background(), d, "", resolveNoName)
	if err != nil {
		t.Fatalf("draft Submit() error: %v", err)
	}

	first, err := g.Submit(context.Background(), d, draft.DraftToken, resolveNoName)
	if err != nil {
		t.Fatalf("first commit Submit() error: %v", err)
	}
	if !first.Sent {
		t.Fatal("first commit Submit() Sent = false, want true")
	}

	_, err = g.Submit(context.Background(), d, draft.DraftToken, resolveNoName)
	if err == nil {
		t.Fatal("second commit with the same token error = nil, want error (single use)")
	}
	if !strings.Contains(err.Error(), "draft expired or content differs") {
		t.Fatalf("Submit() error = %q, want it to mention draft expired or content differs", err.Error())
	}
	if deliverer.count() != 1 {
		t.Fatalf("deliverer called %d times, want 1 (token must not deliver twice)", deliverer.count())
	}
}

func TestSubmitCommitRateLimitedRestoresDraftForRetry(t *testing.T) {
	clock := newFakeClock(time.Unix(0, 0))
	deliverer := &fakeDeliverer{}
	g := New(deliverer, trustNone, 1, 12, clock.Now)

	// Burn the single burst token on an (immediate, untrusted) read so the
	// limiter is exhausted before we ever attempt to commit the draft below.
	consume := Delivery{Kind: "read", To: "999@s.whatsapp.net", MessageIDs: []string{"x"}}
	if _, err := g.Submit(context.Background(), consume, "", resolveNoName); err != nil {
		t.Fatalf("consume Submit() error: %v", err)
	}

	d := Delivery{Kind: "text", To: "111@s.whatsapp.net", Text: "please survive"}
	draft, err := g.Submit(context.Background(), d, "", resolveNoName)
	if err != nil {
		t.Fatalf("draft Submit() error: %v", err)
	}

	_, err = g.Submit(context.Background(), d, draft.DraftToken, resolveNoName)
	if err == nil {
		t.Fatal("commit while rate limited error = nil, want rate limit error")
	}
	if !strings.Contains(err.Error(), "rate limit reached") {
		t.Fatalf("Submit() error = %q, want it to mention rate limit reached", err.Error())
	}
	if deliverer.count() != 1 {
		t.Fatalf("deliverer called %d times, want still 1 (draft commit must not have delivered)", deliverer.count())
	}

	// Let the limiter refill, then retry with the SAME token: the draft
	// the user already approved must still be there.
	clock.Advance(12 * time.Second)
	result, err := g.Submit(context.Background(), d, draft.DraftToken, resolveNoName)
	if err != nil {
		t.Fatalf("retry commit Submit() error: %v, want the restored draft to commit", err)
	}
	if !result.Sent {
		t.Fatal("retry commit Submit() Sent = false, want true")
	}
	if deliverer.count() != 2 {
		t.Fatalf("deliverer called %d times, want 2", deliverer.count())
	}
}

func TestSubmitCommitRateLimitedRestoresOriginalExpiryOnly(t *testing.T) {
	clock := newFakeClock(time.Unix(0, 0))
	deliverer := &fakeDeliverer{}
	// perSeconds is set far beyond the test's time window so the limiter
	// stays exhausted throughout — the second commit attempt below must
	// fail on draft expiry, not on the limiter, which is what lets this
	// test distinguish "restored with original expiry" from "restored
	// with expiry extended from the restore time."
	g := New(deliverer, trustNone, 1, 1000, clock.Now)

	consume := Delivery{Kind: "read", To: "999@s.whatsapp.net", MessageIDs: []string{"x"}}
	if _, err := g.Submit(context.Background(), consume, "", resolveNoName); err != nil {
		t.Fatalf("consume Submit() error: %v", err)
	}

	d := Delivery{Kind: "text", To: "111@s.whatsapp.net", Text: "please expire on time"}
	draft, err := g.Submit(context.Background(), d, "", resolveNoName)
	if err != nil {
		t.Fatalf("draft Submit() error: %v", err)
	}

	clock.Advance(100 * time.Second)
	_, err = g.Submit(context.Background(), d, draft.DraftToken, resolveNoName)
	if err == nil {
		t.Fatal("commit at +100s error = nil, want rate limit error")
	}
	if !strings.Contains(err.Error(), "rate limit reached") {
		t.Fatalf("Submit() error = %q, want it to mention rate limit reached (restore sanity check)", err.Error())
	}

	// Total elapsed since the draft was created: 305s, past its original
	// 5 minute (300s) TTL. If the restore above had reset or extended the
	// expiry from the retry time (100s + 300s = 400s), this would still
	// succeed or fail with a rate-limit error instead of an expiry error.
	clock.Advance(205 * time.Second)
	_, err = g.Submit(context.Background(), d, draft.DraftToken, resolveNoName)
	if err == nil {
		t.Fatal("commit at +305s error = nil, want draft expired error")
	}
	if !strings.Contains(err.Error(), "draft expired or content differs") {
		t.Fatalf("Submit() error = %q, want draft expired (original TTL preserved, not extended)", err.Error())
	}
	if deliverer.count() != 1 {
		t.Fatalf("deliverer called %d times, want still 1 (draft never committed)", deliverer.count())
	}
}

func TestSubmitRateLimiterBlocksWithoutBlocking(t *testing.T) {
	clock := newFakeClock(time.Unix(0, 0))
	deliverer := &fakeDeliverer{}
	g := New(deliverer, trustOnly("222@s.whatsapp.net"), 2, 12, clock.Now)

	d := Delivery{Kind: "text", To: "222@s.whatsapp.net", Text: "burst"}

	for i := 0; i < 2; i++ {
		if _, err := g.Submit(context.Background(), d, "", resolveNoName); err != nil {
			t.Fatalf("Submit() call %d error: %v", i, err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already-canceled context proves Submit cannot be blocking on Wait

	_, err := g.Submit(ctx, d, "", resolveNoName)
	if err == nil {
		t.Fatal("Submit() over burst error = nil, want rate limit error")
	}
	if !strings.Contains(err.Error(), "rate limit reached") {
		t.Fatalf("Submit() error = %q, want it to mention rate limit reached", err.Error())
	}
	if deliverer.count() != 2 {
		t.Fatalf("deliverer called %d times, want 2 (third blocked)", deliverer.count())
	}
}

func TestSubmitRateLimiterFloorRaisesSubSecondConfig(t *testing.T) {
	clock := newFakeClock(time.Unix(0, 0))
	deliverer := &fakeDeliverer{}
	g := New(deliverer, trustOnly("222@s.whatsapp.net"), 1, 1, clock.Now)

	d := Delivery{Kind: "text", To: "222@s.whatsapp.net", Text: "floor"}

	if _, err := g.Submit(context.Background(), d, "", resolveNoName); err != nil {
		t.Fatalf("first Submit() error: %v", err)
	}

	_, err := g.Submit(context.Background(), d, "", resolveNoName)
	if err == nil {
		t.Fatal("second Submit() immediately after burst error = nil, want rate limit error")
	}
	if !strings.Contains(err.Error(), "retry after 5s") {
		t.Fatalf("Submit() error = %q, want it to mention the 5s floor", err.Error())
	}

	clock.Advance(4 * time.Second)
	if _, err := g.Submit(context.Background(), d, "", resolveNoName); err == nil {
		t.Fatal("Submit() at +4s error = nil, want still rate limited (floor is 5s)")
	}

	clock.Advance(time.Second)
	if _, err := g.Submit(context.Background(), d, "", resolveNoName); err != nil {
		t.Fatalf("Submit() at +5s error: %v, want allowed", err)
	}
	if deliverer.count() != 2 {
		t.Fatalf("deliverer called %d times, want 2", deliverer.count())
	}
}

// TestNewFloorsBurstBelowOneToOne proves the defense-in-depth floor: a
// burst of 0 (or negative) must not construct a limiter that denies every
// delivery forever — New raises it to 1 so at least one delivery can always
// get through, the same way config.Load's own default protects a
// hand-written config.json missing rate_burst.
func TestNewFloorsBurstBelowOneToOne(t *testing.T) {
	for _, burst := range []int{0, -5} {
		clock := newFakeClock(time.Unix(0, 0))
		deliverer := &fakeDeliverer{}
		g := New(deliverer, trustOnly("222@s.whatsapp.net"), burst, floorSeconds, clock.Now)

		d := Delivery{Kind: "text", To: "222@s.whatsapp.net", Text: "floored"}
		if _, err := g.Submit(context.Background(), d, "", resolveNoName); err != nil {
			t.Fatalf("burst=%d: first Submit() error = %v, want the floored burst of 1 to allow it", burst, err)
		}
		if deliverer.count() != 1 {
			t.Fatalf("burst=%d: deliverer called %d times, want exactly 1", burst, deliverer.count())
		}
	}
}

func TestSubmitConcurrentCommitDeliversOnce(t *testing.T) {
	clock := newFakeClock(time.Unix(0, 0))
	deliverer := &fakeDeliverer{}
	g := New(deliverer, trustNone, 100, 12, clock.Now)

	d := Delivery{Kind: "text", To: "111@s.whatsapp.net", Text: "race"}
	draft, err := g.Submit(context.Background(), d, "", resolveNoName)
	if err != nil {
		t.Fatalf("draft Submit() error: %v", err)
	}

	const racers = 20
	var wg sync.WaitGroup
	successes := make([]bool, racers)
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r, err := g.Submit(context.Background(), d, draft.DraftToken, resolveNoName)
			successes[i] = err == nil && r.Sent
		}(i)
	}
	wg.Wait()

	sentCount := 0
	for _, ok := range successes {
		if ok {
			sentCount++
		}
	}
	if sentCount != 1 {
		t.Fatalf("successful commits = %d, want exactly 1 (single-use draft)", sentCount)
	}
	if deliverer.count() != 1 {
		t.Fatalf("deliverer called %d times, want exactly 1", deliverer.count())
	}
}

func TestSubmitConcurrentCreationAndRateLimitedCommitsNeverGhostOrExceedCap(t *testing.T) {
	clock := newFakeClock(time.Unix(0, 0))
	deliverer := &fakeDeliverer{}
	// New floors burst to 1 (never 0, which would deny every delivery
	// forever), so the single token has to be burned deliberately here to
	// get the same guarantee the old burst-0 gate gave for free: every
	// commit attempt below rate limited deterministically, no matter how
	// goroutines are scheduled, without depending on consuming tokens or
	// advancing the clock during the concurrent phase itself.
	g := New(deliverer, trustNone, 1, floorSeconds, clock.Now)
	consume := Delivery{Kind: "read", To: "999@s.whatsapp.net", MessageIDs: []string{"x"}}
	if _, err := g.Submit(context.Background(), consume, "", resolveNoName); err != nil {
		t.Fatalf("consume Submit() error: %v", err)
	}

	type pending struct {
		token    string
		delivery Delivery
	}
	fill := make([]pending, draftCap)
	for i := 0; i < draftCap; i++ {
		d := Delivery{Kind: "text", To: "111@s.whatsapp.net", Text: "fill-" + itoa(i)}
		r, err := g.Submit(context.Background(), d, "", resolveNoName)
		if err != nil {
			t.Fatalf("fill Submit() %d error: %v", i, err)
		}
		fill[i] = pending{token: r.DraftToken, delivery: d}
	}

	var wg sync.WaitGroup

	// Attempt a rate-limited commit of every existing draft concurrently
	// — including whichever one currently sits at the front of the
	// eviction order at any given instant — while a burst of new draft
	// creations keeps evicting the current oldest. This is the
	// interleaving the ghost-entry bug needed: a commit deleting a token
	// from g.drafts while it is still the front of g.order, racing a
	// concurrent prune/evict that pops the same token from g.order too,
	// followed by the commit's rate-limit path restoring it into
	// g.drafts alone — a map entry with no order entry, invisible to cap
	// enforcement from then on.
	for _, p := range fill {
		wg.Add(1)
		go func(p pending) {
			defer wg.Done()
			_, err := g.Submit(context.Background(), p.delivery, p.token, resolveNoName)
			if err == nil {
				t.Error("concurrent commit error = nil, want a rate-limit or draft-invalid error")
				return
			}
			if !strings.Contains(err.Error(), "rate limit reached") &&
				!strings.Contains(err.Error(), "draft expired or content differs") {
				t.Errorf("concurrent commit error = %q, want rate-limit or draft-invalid category", err.Error())
			}
		}(p)
	}

	const extraCreates = 200
	for i := 0; i < extraCreates; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			d := Delivery{Kind: "text", To: "111@s.whatsapp.net", Text: "extra-" + itoa(i)}
			if _, err := g.Submit(context.Background(), d, "", resolveNoName); err != nil {
				t.Errorf("concurrent create %d error: %v", i, err)
			}
		}(i)
	}

	wg.Wait()

	if deliverer.count() != 1 {
		t.Fatalf("deliverer called %d times, want exactly 1 (only the token-burning read at the top; every commit below must be rate limited)", deliverer.count())
	}

	g.mu.Lock()
	draftsLen := len(g.drafts)
	inOrder := make(map[string]bool, len(g.order))
	for _, tok := range g.order {
		inOrder[tok] = true
	}
	var ghosts []string
	for tok := range g.drafts {
		if !inOrder[tok] {
			ghosts = append(ghosts, tok)
		}
	}
	g.mu.Unlock()

	if draftsLen > draftCap {
		t.Fatalf("len(g.drafts) = %d, want it to never exceed draftCap %d", draftsLen, draftCap)
	}
	if len(ghosts) > 0 {
		t.Fatalf("found %d draft(s) in g.drafts absent from g.order (ghost entries invisible to cap eviction): %v", len(ghosts), ghosts)
	}
}

func TestSubmitDraftCapEvictsOldest(t *testing.T) {
	clock := newFakeClock(time.Unix(0, 0))
	deliverer := &fakeDeliverer{}
	g := New(deliverer, trustNone, 3, 12, clock.Now)

	var first Result
	for i := 0; i < 33; i++ {
		d := Delivery{Kind: "text", To: "111@s.whatsapp.net", Text: "msg-" + itoa(i)}
		r, err := g.Submit(context.Background(), d, "", resolveNoName)
		if err != nil {
			t.Fatalf("Submit() call %d error: %v", i, err)
		}
		if i == 0 {
			first = r
		}
	}

	firstDelivery := Delivery{Kind: "text", To: "111@s.whatsapp.net", Text: "msg-0"}
	_, err := g.Submit(context.Background(), firstDelivery, first.DraftToken, resolveNoName)
	if err == nil {
		t.Fatal("Submit() commit for evicted draft error = nil, want error")
	}
	if !strings.Contains(err.Error(), "draft expired or content differs") {
		t.Fatalf("Submit() error = %q, want it to mention draft expired or content differs", err.Error())
	}
}

// TestBlockAlwaysDraftsEvenForTrustedRecipient is the whole point of the
// always-draft rule: trust means "auto-send messages to this person", which
// must not silently authorise changing their block status. A block to a
// trusted recipient must still draft, and only commit on re-submission.
func TestBlockAlwaysDraftsEvenForTrustedRecipient(t *testing.T) {
	clock := newFakeClock(time.Unix(0, 0))
	deliverer := &fakeDeliverer{}
	// Everyone is trusted: a normal send would auto-commit here.
	g := New(deliverer, func(string) bool { return true }, 3, 12, clock.Now)

	for _, kind := range []string{"block", "unblock"} {
		before := deliverer.count()

		res, err := g.Submit(context.Background(),
			Delivery{Kind: kind, To: "111@s.whatsapp.net"}, "", nil)
		if err != nil {
			t.Fatalf("Submit(%s) error = %v", kind, err)
		}
		if res.Sent {
			t.Errorf("Submit(%s) Sent = true for a trusted recipient, want a draft", kind)
		}
		if res.DraftToken == "" {
			t.Errorf("Submit(%s) returned no draft token", kind)
		}
		if deliverer.count() != before {
			t.Fatalf("deliverer called on the drafting %s call, want no delivery", kind)
		}

		// Re-submitting the identical call with the token commits it.
		if _, err := g.Submit(context.Background(),
			Delivery{Kind: kind, To: "111@s.whatsapp.net"}, res.DraftToken, nil); err != nil {
			t.Fatalf("Submit(%s) commit error = %v", kind, err)
		}
		if deliverer.count() != before+1 {
			t.Fatalf("commit of %s delivered %d times, want one", kind, deliverer.count()-before)
		}
	}
}

// TestSubmitRefusesBeforeDraftingAndBeforeRateLimiting proves a delivery the
// deliverer rejects costs nothing: no draft token is minted, and no
// rate-limit token is spent. Spending either would let a caller that keeps
// naming an unusable file exhaust the limiter for the sends that would have
// worked, and would hand back a preview for something that can never send.
func TestSubmitRefusesBeforeDraftingAndBeforeRateLimiting(t *testing.T) {
	clock := newFakeClock(time.Unix(0, 0))
	deliverer := &fakeDeliverer{validateErr: errors.New("file is outside the allowed directories")}
	// burst 1: if the refused call consumed the token, the good send below
	// would be rate-limited rather than delivered.
	g := New(deliverer, trustNone, 1, 12, clock.Now)

	res, err := g.Submit(context.Background(),
		Delivery{Kind: "media", To: "111@s.whatsapp.net", Path: "/etc/shadow"}, "", nil)
	if err == nil {
		t.Fatal("Submit() error = nil, want the deliverer's validation error")
	}
	if res.DraftToken != "" {
		t.Errorf("Submit() minted draft token %q for a delivery that cannot send", res.DraftToken)
	}
	if deliverer.count() != 0 {
		t.Errorf("deliverer delivered %d times, want 0", deliverer.count())
	}

	deliverer.validateErr = nil
	if _, err := g.Submit(context.Background(),
		Delivery{Kind: "read", To: "111@s.whatsapp.net", MessageIDs: []string{"m1"}}, "", nil); err != nil {
		t.Fatalf("a valid send after a refused one failed: %v — the refused call spent a rate token", err)
	}
}

// FireAt is part of what the human confirms: a commit whose fire time
// differs from the drafted one must be rejected like any other content
// change.
func TestSubmitAlteredFireAtWithValidTokenErrors(t *testing.T) {
	clock := newFakeClock(time.Unix(0, 0))
	deliverer := &fakeDeliverer{}
	g := New(deliverer, trustNone, 3, 5, clock.Now)

	d := Delivery{Kind: "text", To: "111@s.whatsapp.net", Text: "morning!", FireAt: 5000}
	res, err := g.Submit(context.Background(), d, "", nil)
	if err != nil {
		t.Fatalf("Submit(draft) error = %v", err)
	}

	altered := d
	altered.FireAt = 9000
	if _, err := g.Submit(context.Background(), altered, res.DraftToken, nil); err == nil {
		t.Fatal("Submit(commit) accepted a changed FireAt under the drafted token")
	}
	if deliverer.count() != 0 {
		t.Fatalf("deliverer called %d times, want 0", deliverer.count())
	}
}

func TestPreviewNamesTheFireTime(t *testing.T) {
	clock := newFakeClock(time.Unix(0, 0))
	g := New(&fakeDeliverer{}, trustNone, 3, 5, clock.Now)

	res, err := g.Submit(context.Background(), Delivery{
		Kind: "text", To: "111@s.whatsapp.net", Text: "morning!", FireAt: 1787056496,
	}, "", nil)
	if err != nil {
		t.Fatalf("Submit error = %v", err)
	}
	if !strings.Contains(res.Preview, "2026-08-18T12:34:56Z") {
		t.Fatalf("Preview = %q, want it to show the scheduled fire time", res.Preview)
	}
}

// DeliverScheduled is the fire-time path for a schedule the human already
// confirmed when it was created: it delivers without the draft flow but
// still consumes a token from the same shared rate limiter as every other
// send.
func TestDeliverScheduledDeliversWithoutDrafting(t *testing.T) {
	clock := newFakeClock(time.Unix(0, 0))
	deliverer := &fakeDeliverer{}
	g := New(deliverer, trustNone, 3, 5, clock.Now)

	res, err := g.DeliverScheduled(context.Background(), Delivery{Kind: "text", To: "111@s.whatsapp.net", Text: "hi"})
	if err != nil {
		t.Fatalf("DeliverScheduled error = %v", err)
	}
	if !res.Sent || res.MessageID == "" {
		t.Fatalf("result = %+v, want a completed send", res)
	}
	if deliverer.count() != 1 {
		t.Fatalf("deliverer called %d times, want 1", deliverer.count())
	}
}

func TestDeliverScheduledSharesTheRateLimiter(t *testing.T) {
	clock := newFakeClock(time.Unix(0, 0))
	deliverer := &fakeDeliverer{}
	g := New(deliverer, trustNone, 1, 5, clock.Now) // burst 1

	if _, err := g.DeliverScheduled(context.Background(), Delivery{Kind: "text", To: "111@s.whatsapp.net", Text: "a"}); err != nil {
		t.Fatalf("first DeliverScheduled error = %v", err)
	}
	_, err := g.DeliverScheduled(context.Background(), Delivery{Kind: "text", To: "111@s.whatsapp.net", Text: "b"})
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("second DeliverScheduled error = %v, want ErrRateLimited (shared limiter, burst 1)", err)
	}
	if deliverer.count() != 1 {
		t.Fatalf("deliverer called %d times, want 1", deliverer.count())
	}
}

func TestDeliverScheduledStillValidates(t *testing.T) {
	clock := newFakeClock(time.Unix(0, 0))
	deliverer := &fakeDeliverer{validateErr: errors.New("outside media roots")}
	g := New(deliverer, trustNone, 3, 5, clock.Now)

	if _, err := g.DeliverScheduled(context.Background(), Delivery{Kind: "media", To: "111@s.whatsapp.net", Path: "/x"}); err == nil {
		t.Fatal("DeliverScheduled skipped Validate")
	}
	if deliverer.count() != 0 {
		t.Fatalf("deliverer called %d times, want 0", deliverer.count())
	}
}

// TestDraftsListsPendingWithPreview proves Drafts() is what the dashboard's
// Drafts tab renders: the same token and preview Submit already handed the
// caller, plus an expiry.
func TestDraftsListsPendingWithPreview(t *testing.T) {
	clock := newFakeClock(time.Unix(0, 0))
	deliverer := &fakeDeliverer{}
	g := New(deliverer, trustNone, 3, 12, clock.Now)

	res, err := g.Submit(context.Background(), Delivery{Kind: "text", To: "x@s.whatsapp.net", Text: "hello"}, "", nil)
	if err != nil || res.Sent {
		t.Fatalf("draft submit: %+v %v", res, err)
	}
	drafts := g.Drafts()
	if len(drafts) != 1 || drafts[0].Token != res.DraftToken || drafts[0].Preview != res.Preview {
		t.Fatalf("Drafts() = %+v, want the pending draft with its preview", drafts)
	}
	if !drafts[0].Expires.After(clock.Now()) {
		t.Fatalf("expiry not populated: %v", drafts[0].Expires)
	}
}

// TestApproveDeliversExactlyOnce proves Approve shares commit's single-use
// guarantee: once it has delivered, neither the model's own re-submit of
// the same token nor a second Approve can deliver again.
func TestApproveDeliversExactlyOnce(t *testing.T) {
	clock := newFakeClock(time.Unix(0, 0))
	deliverer := &fakeDeliverer{}
	g := New(deliverer, trustNone, 3, 12, clock.Now)

	res, err := g.Submit(context.Background(), Delivery{Kind: "text", To: "x@s.whatsapp.net", Text: "hello"}, "", nil)
	if err != nil || res.Sent {
		t.Fatalf("draft submit: %+v %v", res, err)
	}

	out, err := g.Approve(context.Background(), res.DraftToken)
	if err != nil || !out.Sent {
		t.Fatalf("Approve: %+v %v", out, err)
	}
	if deliverer.count() != 1 {
		t.Fatalf("deliveries = %d, want 1", deliverer.count())
	}

	// The model's re-submit with the same token must now fail — the
	// double-send guard.
	if _, err := g.Submit(context.Background(), Delivery{Kind: "text", To: "x@s.whatsapp.net", Text: "hello"}, res.DraftToken, nil); err == nil {
		t.Fatal("commit after Approve succeeded — double send possible")
	}
	// And a second Approve must fail too.
	if _, err := g.Approve(context.Background(), res.DraftToken); err == nil {
		t.Fatal("second Approve succeeded")
	}
}

// TestApproveRateLimitedKeepsDraft proves a rate-limited Approve leaves the
// draft intact for retry, mirroring commit's own rate-limit behavior.
func TestApproveRateLimitedKeepsDraft(t *testing.T) {
	clock := newFakeClock(time.Unix(0, 0))
	deliverer := &fakeDeliverer{}
	g := New(deliverer, trustNone, 1, 12, clock.Now)

	// Burn the single burst token on an (immediate, untrusted) read so the
	// limiter is exhausted before Approve is attempted.
	consume := Delivery{Kind: "read", To: "999@s.whatsapp.net", MessageIDs: []string{"x"}}
	if _, err := g.Submit(context.Background(), consume, "", resolveNoName); err != nil {
		t.Fatalf("consume Submit() error: %v", err)
	}

	res, err := g.Submit(context.Background(), Delivery{Kind: "text", To: "x@s.whatsapp.net", Text: "hello"}, "", nil)
	if err != nil {
		t.Fatalf("draft submit: %v", err)
	}
	if _, err := g.Approve(context.Background(), res.DraftToken); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("Approve under rate limit = %v, want ErrRateLimited", err)
	}
	if len(g.Drafts()) != 1 {
		t.Fatal("rate-limited Approve consumed the draft")
	}
}

// TestDiscard proves discarding a draft is single-use and permanently
// forecloses its Approve.
func TestDiscard(t *testing.T) {
	clock := newFakeClock(time.Unix(0, 0))
	deliverer := &fakeDeliverer{}
	g := New(deliverer, trustNone, 3, 12, clock.Now)

	res, err := g.Submit(context.Background(), Delivery{Kind: "text", To: "x@s.whatsapp.net", Text: "hello"}, "", nil)
	if err != nil {
		t.Fatalf("draft submit: %v", err)
	}
	if !g.Discard(res.DraftToken) {
		t.Fatal("Discard of a live draft returned false")
	}
	if g.Discard(res.DraftToken) {
		t.Fatal("second Discard returned true")
	}
	if _, err := g.Approve(context.Background(), res.DraftToken); err == nil {
		t.Fatal("Approve after Discard succeeded")
	}
}

// TestDraftsSkipsConsumedMidOrderTokens is a regression test: Discard and
// Approve delete a token from g.drafts but leave it in g.order (only
// pruneExpiredLocked's front-prefix trim removes order entries), so a
// consumed token that is not at the front of g.order leaves a stale entry
// behind. Drafts() must skip it rather than dereference the now-missing
// map entry.
func TestDraftsSkipsConsumedMidOrderTokens(t *testing.T) {
	clock := newFakeClock(time.Unix(0, 0))
	deliverer := &fakeDeliverer{}
	g := New(deliverer, trustNone, 3, 12, clock.Now)

	older, err := g.Submit(context.Background(), Delivery{Kind: "text", To: "x@s.whatsapp.net", Text: "older"}, "", nil)
	if err != nil {
		t.Fatalf("older draft submit: %v", err)
	}
	newer, err := g.Submit(context.Background(), Delivery{Kind: "text", To: "x@s.whatsapp.net", Text: "newer"}, "", nil)
	if err != nil {
		t.Fatalf("newer draft submit: %v", err)
	}

	if !g.Discard(newer.DraftToken) {
		t.Fatal("Discard of the newer draft returned false")
	}

	drafts := g.Drafts()
	if len(drafts) != 1 || drafts[0].Token != older.DraftToken {
		t.Fatalf("Drafts() = %+v, want only the older draft", drafts)
	}
}

// TestDraftsSkipsApprovedMidOrderToken is the Approve-side twin of
// TestDraftsSkipsConsumedMidOrderTokens: an Approve mid-order must not
// leave Drafts() dereferencing a stale g.order entry either.
func TestDraftsSkipsApprovedMidOrderToken(t *testing.T) {
	clock := newFakeClock(time.Unix(0, 0))
	deliverer := &fakeDeliverer{}
	g := New(deliverer, trustNone, 3, 12, clock.Now)

	older, err := g.Submit(context.Background(), Delivery{Kind: "text", To: "x@s.whatsapp.net", Text: "older"}, "", nil)
	if err != nil {
		t.Fatalf("older draft submit: %v", err)
	}
	newer, err := g.Submit(context.Background(), Delivery{Kind: "text", To: "x@s.whatsapp.net", Text: "newer"}, "", nil)
	if err != nil {
		t.Fatalf("newer draft submit: %v", err)
	}

	if _, err := g.Approve(context.Background(), newer.DraftToken); err != nil {
		t.Fatalf("Approve of the newer draft: %v", err)
	}

	drafts := g.Drafts()
	if len(drafts) != 1 || drafts[0].Token != older.DraftToken {
		t.Fatalf("Drafts() = %+v, want only the older draft", drafts)
	}
}
