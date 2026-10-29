package reminders

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// CronSchedule is an allocation-free 5-field cron representation: each
// field is a bitmask over its valid range (min 0-59, hour 0-23, dom
// 1-31, month 1-12, dow 0-6). It is shared by the in-session TUI
// scheduler, the /reminder form's next-run readout, and the
// `coder reminders` CLI so all three agree on when an entry fires.
type CronSchedule struct {
	Min   uint64 // bits 0..59
	Hour  uint32 // bits 0..23
	Dom   uint32 // bits 1..31
	Month uint16 // bits 1..12
	Dow   uint8  // bits 0..6 (Sunday=0)
	// DomRestricted/DowRestricted implement the standard cron rule:
	// when both fields are restricted, a day matches if EITHER
	// matches; when only one is restricted, that one gates.
	DomRestricted bool
	DowRestricted bool
}

// ParseCron parses a standard 5-field cron expression. Fields support
// "*", lists ("1,2,3"), ranges ("9-17"), and steps ("*/10", "1-30/2").
func ParseCron(expr string) (CronSchedule, error) {
	var out CronSchedule
	parts := strings.Fields(expr)
	if len(parts) != 5 {
		return out, fmt.Errorf("cron expression must have 5 fields")
	}
	var err error
	if out.Min, err = parseCronField(parts[0], 0, 59); err != nil {
		return out, fmt.Errorf("minute: %w", err)
	}
	var v uint64
	if v, err = parseCronField(parts[1], 0, 23); err != nil {
		return out, fmt.Errorf("hour: %w", err)
	}
	out.Hour = uint32(v)
	if v, err = parseCronField(parts[2], 1, 31); err != nil {
		return out, fmt.Errorf("day-of-month: %w", err)
	}
	out.Dom = uint32(v)
	out.DomRestricted = parts[2] != "*"
	if v, err = parseCronField(parts[3], 1, 12); err != nil {
		return out, fmt.Errorf("month: %w", err)
	}
	out.Month = uint16(v)
	if v, err = parseCronField(parts[4], 0, 6); err != nil {
		return out, fmt.Errorf("day-of-week: %w", err)
	}
	out.Dow = uint8(v)
	out.DowRestricted = parts[4] != "*"
	return out, nil
}

// parseCronField parses one cron field into a bitmask over [lo, hi].
func parseCronField(s string, lo, hi int) (uint64, error) {
	if s == "" {
		return 0, fmt.Errorf("empty field")
	}
	var bits uint64
	for _, part := range strings.Split(s, ",") {
		step := 1
		rangePart := part
		if i := strings.IndexByte(part, '/'); i >= 0 {
			n, err := strconv.Atoi(part[i+1:])
			if err != nil || n <= 0 {
				return 0, fmt.Errorf("invalid step %q", part[i+1:])
			}
			step = n
			rangePart = part[:i]
		}
		start, end := lo, hi
		switch {
		case rangePart == "*":
			// full range
		case strings.Contains(rangePart, "-"):
			a, b, ok := strings.Cut(rangePart, "-")
			if !ok {
				return 0, fmt.Errorf("invalid range %q", rangePart)
			}
			na, err1 := strconv.Atoi(a)
			nb, err2 := strconv.Atoi(b)
			if err1 != nil || err2 != nil || na < lo || nb > hi || na > nb {
				return 0, fmt.Errorf("invalid range %q", rangePart)
			}
			start, end = na, nb
		default:
			n, err := strconv.Atoi(rangePart)
			if err != nil || n < lo || n > hi {
				return 0, fmt.Errorf("invalid value %q", rangePart)
			}
			start, end = n, n
		}
		for v := start; v <= end; v += step {
			bits |= 1 << uint(v)
		}
	}
	return bits, nil
}

// Matches reports whether wall-clock time t (in t's own location)
// matches the schedule.
func (f CronSchedule) Matches(t time.Time) bool {
	if f.Min&(1<<uint(t.Minute())) == 0 {
		return false
	}
	if f.Hour&(1<<uint(t.Hour())) == 0 {
		return false
	}
	if f.Month&(1<<uint(t.Month())) == 0 {
		return false
	}
	domOK := f.Dom&(1<<uint(t.Day())) != 0
	dowOK := f.Dow&(1<<uint(int(t.Weekday()))) != 0
	switch {
	case f.DomRestricted && f.DowRestricted:
		return domOK || dowOK // standard cron OR semantics
	case f.DomRestricted:
		return domOK
	case f.DowRestricted:
		return dowOK
	default:
		return true
	}
}

// nextRunHorizon bounds the forward walk in Next. Four years plus a
// day covers a Feb 29 one-off; anything that never matches inside the
// horizon (e.g. Feb 30) reports no next run.
const nextRunHorizon = (4*366 + 1) * 24 * 60

// Next returns the first minute strictly after `after` (evaluated in
// after's location, like crontab evaluates in the system zone) at
// which the schedule matches.
func (f CronSchedule) Next(after time.Time) (time.Time, bool) {
	t := after.Truncate(time.Minute)
	for i := 1; i <= nextRunHorizon; i++ {
		c := t.Add(time.Duration(i) * time.Minute)
		if f.Matches(c) {
			return c, true
		}
	}
	return time.Time{}, false
}

// NextRun parses expr and returns its next fire time after `after`.
func NextRun(expr string, after time.Time) (time.Time, error) {
	f, err := ParseCron(expr)
	if err != nil {
		return time.Time{}, err
	}
	t, ok := f.Next(after)
	if !ok {
		return time.Time{}, fmt.Errorf("cron %q never fires", expr)
	}
	return t, nil
}

var weekdayNames = []string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}

// Describe renders a cron expression in plain English for the common
// shapes the /reminder form and set_reminder produce ("every 5
// minutes", "weekdays at 09:00", "once at Nov 3 14:30"). Anything
// else falls back to "cron <expr>".
func Describe(expr string, oneShot bool) string {
	p := strings.Fields(expr)
	if len(p) != 5 {
		return "cron " + expr
	}
	m, h, dom, mon, dow := p[0], p[1], p[2], p[3], p[4]
	num := func(s string, lo, hi int) (int, bool) {
		n, err := strconv.Atoi(s)
		return n, err == nil && n >= lo && n <= hi
	}
	mi, mOK := num(m, 0, 59)
	hi, hOK := num(h, 0, 23)
	at := fmt.Sprintf("%02d:%02d", hi, mi)
	if h == "*" && dom == "*" && mon == "*" && dow == "*" {
		switch {
		case m == "*":
			return "every minute"
		case strings.HasPrefix(m, "*/"):
			if n, ok := num(m[2:], 1, 59); ok {
				if n == 1 {
					return "every minute"
				}
				return fmt.Sprintf("every %d minutes", n)
			}
		case mOK && mi == 0:
			return "every hour"
		case mOK:
			return fmt.Sprintf("every hour at :%02d", mi)
		}
		return "cron " + expr
	}
	if !mOK || !hOK {
		return "cron " + expr
	}
	switch {
	case dom == "*" && mon == "*" && dow == "*":
		return "every day at " + at
	case dom == "*" && mon == "*" && dow == "1-5":
		return "weekdays at " + at
	case dom == "*" && mon == "*":
		if d, ok := num(dow, 0, 7); ok {
			return fmt.Sprintf("every %s at %s", weekdayNames[d%7], at)
		}
	case dow == "*":
		d, dOK := num(dom, 1, 31)
		mo, moOK := num(mon, 1, 12)
		if dOK && moOK {
			date := fmt.Sprintf("%s %d", time.Month(mo).String()[:3], d)
			if oneShot {
				return fmt.Sprintf("once at %s %s", date, at)
			}
			return fmt.Sprintf("every year on %s at %s", date, at)
		}
	}
	return "cron " + expr
}
