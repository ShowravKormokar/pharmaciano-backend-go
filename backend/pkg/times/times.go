package times

import (
	"errors"
	"fmt"
	"sync/atomic"
	"time"
)

var defaultLoc atomic.Pointer[time.Location]

func init() { defaultLoc.Store(time.UTC) }

// SetDefaultTimezone loads and sets the process-wide default timezone.
func SetDefaultTimezone(name string) error {
	loc, err := time.LoadLocation(name)
	if err != nil {
		return fmt.Errorf("times: load timezone %q: %w", name, err)
	}
	defaultLoc.Store(loc)
	return nil
}

// DefaultTimezone returns the current process-wide default location.
func DefaultTimezone() *time.Location { return defaultLoc.Load() }

func Now() time.Time { return NowIn(DefaultTimezone()) }

// NowIn returns the current instant rendered in loc (DefaultTimezone() if nil).
func NowIn(loc *time.Location) time.Time {
	if loc == nil {
		loc = DefaultTimezone()
	}
	return time.Now().In(loc)
}

// UTCNow always returns UTC — use for DB writes.
func UTCNow() time.Time { return time.Now().UTC() }

// ParseISO parses RFC3339 (with or without fractional seconds) or a plain
// YYYY-MM-DD date, the latter interpreted at UTC midnight.
func ParseISO(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, errors.New("times: empty timestamp")
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	if t, err := time.ParseInLocation("2006-01-02", s, time.UTC); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("times: %q is not RFC3339 or YYYY-MM-DD", s)
}

type Range struct {
	Start time.Time
	End   time.Time
}

func (r Range) IsValid() bool {
	return !r.Start.IsZero() && !r.End.IsZero() && r.End.After(r.Start)
}

// Contains reports whether t falls within [Start, End) — Start inclusive,
// End exclusive.
func (r Range) Contains(t time.Time) bool {
	return !t.Before(r.Start) && t.Before(r.End)
}

// Days returns the number of whole days in the range.
func (r Range) Days() int { return int(r.End.Sub(r.Start).Hours() / 24) }

var ErrIncompleteRange = errors.New("times: both 'from' and 'to' are required together, or neither")

// ParseDateRange parses the `?from=&to=` query-parameter pair per API.md's convention (RFC3339 or YYYY-MM-DD; from inclusive, to exclusive).
func ParseDateRange(fromStr, toStr string) (Range, error) {
	if fromStr == "" && toStr == "" {
		return Range{}, nil
	}
	if fromStr == "" || toStr == "" {
		return Range{}, ErrIncompleteRange
	}
	from, err := ParseISO(fromStr)
	if err != nil {
		return Range{}, fmt.Errorf("times: parse 'from': %w", err)
	}
	to, err := ParseISO(toStr)
	if err != nil {
		return Range{}, fmt.Errorf("times: parse 'to': %w", err)
	}
	r := Range{Start: from, End: to}
	if !r.IsValid() {
		return Range{}, fmt.Errorf("times: 'to' (%s) must be after 'from' (%s)", toStr, fromStr)
	}
	return r, nil
}

// Today returns the [00:00, tomorrow-00:00) range in the default timezone.
func Today() Range {
	return TodayIn(DefaultTimezone())
}

// TodayIn is Today for an explicit location (DefaultTimezone() if nil).
func TodayIn(loc *time.Location) Range {
	now := NowIn(loc)
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	return Range{Start: start, End: start.Add(24 * time.Hour)}
}

// ThisMonth returns [1st 00:00, next-1st 00:00) in the default timezone.
func ThisMonth() Range {
	return ThisMonthIn(DefaultTimezone())
}

// ThisMonthIn is ThisMonth for an explicit location (DefaultTimezone() if nil).
func ThisMonthIn(loc *time.Location) Range {
	now := NowIn(loc)
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	return Range{Start: start, End: start.AddDate(0, 1, 0)}
}

// Last returns the previous N days as a Range ending at "now" (UTC).
func Last(n int) Range {
	now := UTCNow()
	return Range{Start: now.AddDate(0, 0, -n), End: now}
}

// StartOfDay in the given location (DefaultTimezone() if nil).
func StartOfDay(t time.Time, loc *time.Location) time.Time {
	if loc == nil {
		loc = DefaultTimezone()
	}
	t = t.In(loc)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
}

// EndOfDay returns the last representable instant of the day (23:59:59.999999999).
func EndOfDay(t time.Time, loc *time.Location) time.Time {
	return StartOfDay(t, loc).Add(24*time.Hour - time.Nanosecond)
}

// AddMonths adds `months` calendar months to t, clamping the day-of-month
// to the last valid day of the target month when it would otherwise
// overflow — e.g. Jan 31 + 1 month = Feb 28 (or Feb 29 in a leap year), NOT
// Mar 3. This is deliberately NOT `t.AddDate(0, months, 0)`: Go's stdlib
// AddDate does not clamp — "Feb 31" silently normalizes by rolling over
// into March, which would corrupt monthly billing/target-period boundaries
// (see enums.TargetPeriodMonthly) for any date on the 29th–31st.
func AddMonths(t time.Time, months int) time.Time {
	year, month, day := t.Date()
	totalMonths := int(month) - 1 + months
	targetYear := year + totalMonths/12
	targetMonthIdx := totalMonths % 12
	if targetMonthIdx < 0 {
		targetMonthIdx += 12
		targetYear--
	}
	targetMonth := time.Month(targetMonthIdx + 1)
	if last := lastDayOfMonth(targetYear, targetMonth); day > last {
		day = last
	}
	return time.Date(targetYear, targetMonth, day, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location())
}

func lastDayOfMonth(year int, month time.Month) int {
	// Day 0 of the month AFTER `month` is the last day of `month` itself —
	// a standard trick that avoids hand-rolling a leap-year table.
	return time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

// FormatDate formats as "02-Jan-2006" — the app-wide default display
// format.
func FormatDate(t time.Time) string { return t.Format("02-Jan-2006") }

// FormatDateWithLayout formats t using a caller-supplied Go reference
// layout — the escape hatch for a future per-organization date_format
// setting, without every call site needing to know Go's reference-time
// syntax.
func FormatDateWithLayout(t time.Time, layout string) string { return t.Format(layout) }
