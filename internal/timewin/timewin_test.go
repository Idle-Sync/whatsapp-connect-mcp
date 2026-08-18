package timewin

import (
	"strings"
	"testing"
	"time"
)

// Fixed reference instant for every test: 2026-08-18T12:34:56Z, which is
// 18:04:56 IST the same day. The derived epoch values below were computed
// externally (GNU date), not with this package's own arithmetic, so a test
// cannot pass by mirroring an implementation bug.
const (
	nowUnix  = 1787056496 // 2026-08-18T12:34:56Z
	istAug18 = 1786991400 // 2026-08-18T00:00:00+05:30
	istAug17 = 1786905000 // 2026-08-17T00:00:00+05:30
	utcAug18 = 1787011200 // 2026-08-18T00:00:00Z
	nycMar08 = 1772946000 // 2026-03-08T00:00:00-05:00 (EST)
	nycMar09 = 1773028800 // 2026-03-09T00:00:00-04:00 (EDT — a 23h day between)
)

func testNow() time.Time {
	return time.Unix(nowUnix, 0).UTC()
}

// resolve is a test helper that fails the test on error.
func resolve(t *testing.T, spec Spec) (int64, int64) {
	t.Helper()
	after, before, err := Resolve(spec, testNow())
	if err != nil {
		t.Fatalf("Resolve(%+v): unexpected error: %v", spec, err)
	}
	return after, before
}

func TestResolveEmptySpecIsUnbounded(t *testing.T) {
	after, before := resolve(t, Spec{})
	if after != 0 || before != 0 {
		t.Fatalf("empty spec: got after=%d before=%d, want 0, 0", after, before)
	}
}

func TestResolveUnixSecondsPassThrough(t *testing.T) {
	after, before := resolve(t, Spec{After: "1786905000", Before: "1786991400"})
	if after != 1786905000 || before != 1786991400 {
		t.Fatalf("unix digits: got after=%d before=%d, want verbatim values", after, before)
	}
}

func TestResolveRFC3339(t *testing.T) {
	after, before := resolve(t, Spec{
		After:  "2026-08-17T18:30:00Z",
		Before: "2026-08-18T00:00:00+05:30",
	})
	if after != istAug18 {
		t.Fatalf("RFC3339 after: got %d, want %d", after, istAug18)
	}
	if before != istAug18 {
		t.Fatalf("RFC3339 before with offset: got %d, want %d", before, istAug18)
	}
}

// A bare date as `after` must include that day's very first second: the
// store compares strictly (ts > after), so the resolved bound is one second
// before local midnight. As `before` it must exclude the named day, i.e.
// resolve to local midnight exactly.
func TestResolveBareDateBounds(t *testing.T) {
	after, before := resolve(t, Spec{After: "2026-08-18", TZ: "Asia/Kolkata"})
	if after != istAug18-1 {
		t.Fatalf("bare date after: got %d, want %d (midnight-1 so the day's first second is included)", after, istAug18-1)
	}
	if before != 0 {
		t.Fatalf("bare date after: before side should stay unbounded, got %d", before)
	}

	_, before = resolve(t, Spec{Before: "2026-08-18", TZ: "Asia/Kolkata"})
	if before != istAug18 {
		t.Fatalf("bare date before: got %d, want %d (local midnight, excluding the day)", before, istAug18)
	}
}

func TestResolveDateExpandsToLocalDay(t *testing.T) {
	after, before := resolve(t, Spec{Date: "2026-08-17", TZ: "Asia/Kolkata"})
	if after != istAug17-1 || before != istAug18 {
		t.Fatalf("date day window: got after=%d before=%d, want %d, %d", after, before, istAug17-1, istAug18)
	}
}

// A date on a DST spring-forward day is 23 hours long. Computing the end as
// start+24h would leak an hour of the next day into the window; the end must
// be the next day's real local midnight.
func TestResolveDateOnDSTTransitionDay(t *testing.T) {
	after, before := resolve(t, Spec{Date: "2026-03-08", TZ: "America/New_York"})
	if after != nycMar08-1 || before != nycMar09 {
		t.Fatalf("DST day window: got after=%d before=%d, want %d, %d", after, before, nycMar08-1, nycMar09)
	}
	if before-(after+1) != 23*3600 {
		t.Fatalf("DST day length: got %ds, want 23h", before-(after+1))
	}
}

func TestResolveWindowToday(t *testing.T) {
	after, before := resolve(t, Spec{Window: "today", TZ: "Asia/Kolkata"})
	if after != istAug18-1 || before != 0 {
		t.Fatalf("today: got after=%d before=%d, want %d, 0", after, before, istAug18-1)
	}
}

func TestResolveWindowYesterday(t *testing.T) {
	after, before := resolve(t, Spec{Window: "yesterday", TZ: "Asia/Kolkata"})
	if after != istAug17-1 || before != istAug18 {
		t.Fatalf("yesterday: got after=%d before=%d, want %d, %d", after, before, istAug17-1, istAug18)
	}
}

func TestResolveWindowLast24h(t *testing.T) {
	after, before := resolve(t, Spec{Window: "last_24h"})
	if after != nowUnix-24*3600 || before != 0 {
		t.Fatalf("last_24h: got after=%d before=%d, want %d, 0", after, before, nowUnix-24*3600)
	}
}

func TestResolveWindowLast7d(t *testing.T) {
	after, before := resolve(t, Spec{Window: "last_7d"})
	if after != nowUnix-7*24*3600 || before != 0 {
		t.Fatalf("last_7d: got after=%d before=%d, want %d, 0", after, before, nowUnix-7*24*3600)
	}
}

// An empty TZ means UTC, the same zone every rendered row's timestamp is in.
func TestResolveDefaultTZIsUTC(t *testing.T) {
	after, _ := resolve(t, Spec{Window: "today"})
	if after != utcAug18-1 {
		t.Fatalf("today in default tz: got after=%d, want %d (UTC midnight - 1)", after, utcAug18-1)
	}
}

func TestResolveConflictingFormsError(t *testing.T) {
	specs := []Spec{
		{Window: "today", After: "1786905000"},
		{Window: "today", Date: "2026-08-17"},
		{Date: "2026-08-17", Before: "1786991400"},
	}
	for _, spec := range specs {
		if _, _, err := Resolve(spec, testNow()); err == nil {
			t.Errorf("Resolve(%+v): want a conflict error, got nil", spec)
		}
	}
}

func TestResolveUnknownWindowErrorNamesValidOnes(t *testing.T) {
	_, _, err := Resolve(Spec{Window: "last_week"}, testNow())
	if err == nil {
		t.Fatal("unknown window: want error, got nil")
	}
	if !strings.Contains(err.Error(), "yesterday") {
		t.Fatalf("unknown-window error should name the valid windows, got: %v", err)
	}
}

func TestResolveBadTZError(t *testing.T) {
	if _, _, err := Resolve(Spec{Window: "today", TZ: "Mars/Olympus_Mons"}, testNow()); err == nil {
		t.Fatal("bad tz: want error, got nil")
	}
}

func TestResolveBadTimestampErrorNamesFormats(t *testing.T) {
	_, _, err := Resolve(Spec{After: "banana"}, testNow())
	if err == nil {
		t.Fatal("bad timestamp: want error, got nil")
	}
	if !strings.Contains(err.Error(), "RFC 3339") {
		t.Fatalf("bad-timestamp error should name the accepted formats, got: %v", err)
	}
}

// A bound at or before the epoch would flow into the store as 0/negative,
// which the store treats as "unbounded" — silently inverting the caller's
// intent. Resolve must refuse it instead.
func TestResolvePreEpochBoundError(t *testing.T) {
	if _, _, err := Resolve(Spec{Before: "1969-12-31T23:00:00Z"}, testNow()); err == nil {
		t.Fatal("pre-epoch bound: want error, got nil")
	}
}
