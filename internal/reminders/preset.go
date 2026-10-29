package reminders

import (
	"fmt"
	"strings"
	"time"
)

// Preset is one entry of the /reminder form's schedule dropdown.
type Preset int

const (
	PresetEvery5 Preset = iota
	PresetEvery15
	PresetEvery30
	PresetHourly
	PresetDaily
	PresetWeekdays
	PresetWeekly
	PresetOnce
	PresetCustom
)

// PresetLabels are the dropdown labels, indexed by Preset. The first
// entry is the default.
var PresetLabels = []string{
	"Every 5 minutes",
	"Every 15 minutes",
	"Every 30 minutes",
	"Every hour",
	"Every day at…",
	"Weekdays at…",
	"Every week on…",
	"Once at a date/time",
	"Custom cron",
}

// NeedsTime reports whether the preset reveals a time-of-day field.
func (p Preset) NeedsTime() bool {
	return p == PresetDaily || p == PresetWeekdays || p == PresetWeekly || p == PresetOnce
}

// NeedsDate reports whether the preset reveals a calendar-date field.
func (p Preset) NeedsDate() bool { return p == PresetOnce }

// NeedsWeekday reports whether the preset reveals a day-of-week field.
func (p Preset) NeedsWeekday() bool { return p == PresetWeekly }

// DateLayout and TimeLayout are the accepted input formats.
const (
	DateLayout = "2006-01-02"
	TimeLayout = "15:04"
)

// Spec is the form state BuildCron turns into a cron expression.
// Date and Time are interpreted in now's location (crontab runs in
// the system zone, so the form does too).
type Spec struct {
	Preset  Preset
	Date    string // YYYY-MM-DD (PresetOnce)
	Time    string // HH:MM, 24h (daily/weekdays/weekly/once)
	Weekday time.Weekday
	Custom  string // raw 5-field cron (PresetCustom)
}

// DefaultSpec returns the form defaults: every 5 minutes, with the
// date/time fields prefilled to today and now+5m so revealing them
// already yields a valid future time.
func DefaultSpec(now time.Time) Spec {
	soon := now.Add(5 * time.Minute)
	return Spec{
		Preset:  PresetEvery5,
		Date:    soon.Format(DateLayout),
		Time:    soon.Format(TimeLayout),
		Weekday: soon.Weekday(),
		Custom:  "*/5 * * * *",
	}
}

// ParseClock parses an HH:MM (or H:MM) 24-hour time.
func ParseClock(s string) (hour, minute int, err error) {
	s = strings.TrimSpace(s)
	t, err := time.Parse(TimeLayout, s) // "15" also accepts a 1-digit hour
	if err != nil {
		return 0, 0, fmt.Errorf("time must be HH:MM (24h), got %q", s)
	}
	return t.Hour(), t.Minute(), nil
}

// BuildCron maps a form Spec to a 5-field cron expression. oneShot is
// true for PresetOnce: the expression would otherwise repeat every
// year, so the entry must be removed after it fires. Custom
// expressions are only shape-checked here (ParseCron); callers also
// apply the crontab-injection check before saving.
func BuildCron(s Spec, now time.Time) (expr string, oneShot bool, err error) {
	var h, m int
	if s.Preset.NeedsTime() {
		if h, m, err = ParseClock(s.Time); err != nil {
			return "", false, err
		}
	}
	switch s.Preset {
	case PresetEvery5:
		return "*/5 * * * *", false, nil
	case PresetEvery15:
		return "*/15 * * * *", false, nil
	case PresetEvery30:
		return "*/30 * * * *", false, nil
	case PresetHourly:
		return "0 * * * *", false, nil
	case PresetDaily:
		return fmt.Sprintf("%d %d * * *", m, h), false, nil
	case PresetWeekdays:
		return fmt.Sprintf("%d %d * * 1-5", m, h), false, nil
	case PresetWeekly:
		if s.Weekday < time.Sunday || s.Weekday > time.Saturday {
			return "", false, fmt.Errorf("pick a day of the week")
		}
		return fmt.Sprintf("%d %d * * %d", m, h, int(s.Weekday)), false, nil
	case PresetOnce:
		d, derr := time.ParseInLocation(DateLayout, strings.TrimSpace(s.Date), now.Location())
		if derr != nil {
			return "", false, fmt.Errorf("date must be YYYY-MM-DD, got %q", strings.TrimSpace(s.Date))
		}
		at := time.Date(d.Year(), d.Month(), d.Day(), h, m, 0, 0, now.Location())
		if !at.After(now) {
			return "", false, fmt.Errorf("%s is in the past; pick a future date/time", at.Format("Jan 2 15:04"))
		}
		if at.Sub(now) > 366*24*time.Hour {
			return "", false, fmt.Errorf("one-off reminders must be within a year")
		}
		return fmt.Sprintf("%d %d %d %d *", m, h, d.Day(), int(d.Month())), true, nil
	case PresetCustom:
		c := strings.Join(strings.Fields(s.Custom), " ")
		if c == "" {
			return "", false, fmt.Errorf("enter a 5-field cron expression")
		}
		if _, err := ParseCron(c); err != nil {
			return "", false, err
		}
		return c, false, nil
	}
	return "", false, fmt.Errorf("unknown schedule preset %d", s.Preset)
}
