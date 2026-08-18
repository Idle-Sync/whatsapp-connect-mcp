// Package timewin resolves the human-shaped time-window inputs the read
// tools accept (RFC 3339 timestamps, bare dates, IANA timezones, named
// windows like "yesterday") into the Unix-second bounds the store queries
// on. Resolution happens here, server-side, against the server's own clock:
// the agent calling a tool must never have to do timezone arithmetic or
// know the current time to ask for "today's messages".
package timewin

import (
	"fmt"
	"regexp"
	"strconv"
	"time"
	// The IANA timezone database is not present on every OS this server
	// runs on (Windows ships none), so embed Go's copy: LoadLocation must
	// work identically everywhere or "tz" would be a Unix-only parameter.
	_ "time/tzdata"
)

// Spec carries a tool's raw time-window inputs, all optional. Exactly one
// form may be used per call: After/Before, Date, or Window (TZ composes
// with any of them).
type Spec struct {
	// After and Before each accept Unix seconds (digits), an RFC 3339
	// timestamp, or a bare YYYY-MM-DD date interpreted in TZ.
	After, Before string
	// Date is a YYYY-MM-DD convenience form: the whole of that local day.
	Date string
	// Window is a named window resolved against the server's clock:
	// "today", "yesterday", "last_24h", or "last_7d".
	Window string
	// TZ is an IANA zone name like "Asia/Kolkata"; empty means UTC.
	TZ string
}

var dateRE = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// Resolve turns spec into (afterTS, beforeTS) in the store's convention:
// strict comparisons (ts > afterTS, ts < beforeTS), 0 leaving that side
// unbounded. Bounds that name a day are half-open [start of day, start of
// next day): because the store compares strictly on whole seconds, an
// inclusive day start resolves to one second before local midnight.
func Resolve(spec Spec, now time.Time) (afterTS, beforeTS int64, err error) {
	loc := time.UTC
	if spec.TZ != "" {
		loc, err = time.LoadLocation(spec.TZ)
		if err != nil {
			return 0, 0, fmt.Errorf("unknown tz %q: use an IANA name like Asia/Kolkata", spec.TZ)
		}
	}

	forms := 0
	if spec.After != "" || spec.Before != "" {
		forms++
	}
	if spec.Date != "" {
		forms++
	}
	if spec.Window != "" {
		forms++
	}
	if forms > 1 {
		return 0, 0, fmt.Errorf("use only one of after/before, date, or window per call")
	}

	switch {
	case spec.Window != "":
		afterTS, beforeTS, err = resolveWindow(spec.Window, now.In(loc))
	case spec.Date != "":
		afterTS, beforeTS, err = dayBounds(spec.Date, loc)
	default:
		if spec.After != "" {
			afterTS, err = parseBound(spec.After, loc, true)
			if err != nil {
				return 0, 0, fmt.Errorf("after: %w", err)
			}
		}
		if spec.Before != "" {
			beforeTS, err = parseBound(spec.Before, loc, false)
			if err != nil {
				return 0, 0, fmt.Errorf("before: %w", err)
			}
		}
	}
	if err != nil {
		return 0, 0, err
	}

	// A resolved bound at or before the epoch would reach the store as its
	// "unbounded" sentinel and silently drop the caller's constraint —
	// refuse it instead. A negative value is always that case; an exact 0
	// is only when the caller actually supplied that side (0 is otherwise
	// the legitimate "unbounded" result).
	if afterTS < 0 || beforeTS < 0 ||
		(spec.After != "" && afterTS == 0) ||
		(spec.Before != "" && beforeTS == 0) ||
		(spec.Date != "" && beforeTS == 0) {
		return 0, 0, fmt.Errorf("time bound resolves to 1970 or earlier, which cannot bound WhatsApp messages")
	}

	return afterTS, beforeTS, nil
}

// resolveWindow expands a named window against now, already shifted into
// the caller's zone.
func resolveWindow(window string, now time.Time) (int64, int64, error) {
	startOfToday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	switch window {
	case "today":
		return startOfToday.Unix() - 1, 0, nil
	case "yesterday":
		// Day arithmetic via time.Date normalization, not AddDate/-24h:
		// both mishandle DST-transition days, where a day is 23 or 25 hours.
		startOfYesterday := time.Date(now.Year(), now.Month(), now.Day()-1, 0, 0, 0, 0, now.Location())
		return startOfYesterday.Unix() - 1, startOfToday.Unix(), nil
	case "last_24h":
		return now.Unix() - 24*3600, 0, nil
	case "last_7d":
		return now.Unix() - 7*24*3600, 0, nil
	default:
		return 0, 0, fmt.Errorf("unknown window %q: valid windows are today, yesterday, last_24h, last_7d", window)
	}
}

// dayBounds returns the half-open bounds covering the whole of the named
// YYYY-MM-DD day in loc, in the store's strict-comparison convention.
func dayBounds(date string, loc *time.Location) (int64, int64, error) {
	day, err := parseDate(date, loc)
	if err != nil {
		return 0, 0, err
	}
	next := time.Date(day.Year(), day.Month(), day.Day()+1, 0, 0, 0, 0, loc)
	return day.Unix() - 1, next.Unix(), nil
}

// parseDate parses a bare YYYY-MM-DD into that day's local midnight.
func parseDate(s string, loc *time.Location) (time.Time, error) {
	if !dateRE.MatchString(s) {
		return time.Time{}, fmt.Errorf("%q is not a date", s)
	}
	day, err := time.ParseInLocation("2006-01-02", s, loc)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid date %q: %w", s, err)
	}
	return day, nil
}

// parseBound parses one after/before value: Unix seconds verbatim, an RFC
// 3339 timestamp (whose own offset wins over loc), or a bare date meaning
// that day's local midnight — inclusive of the day's first second on the
// after side, exclusive of the whole day on the before side.
func parseBound(s string, loc *time.Location, isAfter bool) (int64, error) {
	if unix, err := strconv.ParseInt(s, 10, 64); err == nil {
		return unix, nil
	}
	if dateRE.MatchString(s) {
		day, err := parseDate(s, loc)
		if err != nil {
			return 0, err
		}
		if isAfter {
			return day.Unix() - 1, nil
		}
		return day.Unix(), nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.Unix(), nil
	}
	return 0, fmt.Errorf("%q is not a recognized time: use Unix seconds, an RFC 3339 timestamp, or a YYYY-MM-DD date", s)
}
