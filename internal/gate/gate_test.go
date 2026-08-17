package gate

import (
	"context"
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
}

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
