package tui

import (
	"regexp"
	"testing"
	"time"

	"github.com/tjcoder-labs/cli/internal/reminders"
)

func mustParse(t *testing.T, expr string) cronFields {
	t.Helper()
	f, err := parseCronFields(expr)
	if err != nil {
		t.Fatalf("parseCronFields(%q): %v", expr, err)
	}
	return f
}

func TestParseCronFields_EveryTenMinutes(t *testing.T) {
	f := mustParse(t, "*/10 * * * *")
	// Minute bits set: 0,10,20,30,40,50
	for m := 0; m < 60; m++ {
		want := m%10 == 0
		got := f.min&(1<<uint(m)) != 0
		if got != want {
			t.Errorf("minute %d: match=%v, want %v", m, got, want)
		}
	}
	// Hours/days/months/dow unrestricted.
	if f.hour != 0xFFFFFF { // 24 bits
		t.Errorf("hour mask = %#x, want 24 bits set", f.hour)
	}
	if f.domRestricted || f.dowRestricted {
		t.Error("*/10 * * * * should mark dom/dow unrestricted")
	}
}

func TestParseCronFields_ListsRangesSteps(t *testing.T) {
	f := mustParse(t, "0,30 9-17/2 1,15 * 1-5")
	if f.min != (1<<0|1<<30) {
		t.Errorf("minute mask wrong: %#b", f.min)
	}
	// hours: 9,11,13,15,17
	for h := 0; h < 24; h++ {
		want := h >= 9 && h <= 17 && (h-9)%2 == 0
		got := f.hour&(1<<uint(h)) != 0
		if got != want {
			t.Errorf("hour %d: match=%v, want %v", h, got, want)
		}
	}
	if f.dom&(1<<1) == 0 || f.dom&(1<<15) == 0 || f.dom&(1<<2) != 0 {
		t.Errorf("dom mask wrong: %#b", f.dom)
	}
	for m := 1; m <= 7; m++ {
		want := m >= 1 && m <= 5
		// weekday in dow mask: 1=Mon..5=Fri
		got := f.dow&(1<<uint(m)) != 0
		if got != want {
			t.Errorf("dow %d: match=%v, want %v", m, got, want)
		}
	}
	if !f.domRestricted || !f.dowRestricted {
		t.Error("dom/dow should be restricted when not '*'")
	}
}

func TestParseCronFields_Invalid(t *testing.T) {
	for _, expr := range []string{
		"", "* * * *", "* * * * * *", "61 * * * *", "* 25 * * *",
		"* * 0 * *", "* * * 13 *", "* * * * 8", "*/0 * * * *",
		"a * * * *", "* * *", "1- * * * *", "*-2 * * * *",
	} {
		if _, err := parseCronFields(expr); err == nil {
			t.Errorf("parseCronFields(%q) unexpectedly succeeded", expr)
		}
	}
}

func TestCronMatches_DomDowOrSemantics(t *testing.T) {
	// Standard cron: when both dom and dow are restricted, a match on
	// EITHER fires. Expression "0 12 1 * 1" = noon on the 1st, OR
	// noon on any Monday.
	f := mustParse(t, "0 12 1 * 1")
	theFirst := time.Date(2026, time.July, 1, 12, 0, 0, 0, time.UTC) // Wednesday July 1 2026
	if theFirst.Weekday() == time.Monday {
		t.Fatal("test precondition: July 1 2026 is not Monday")
	}
	if !cronMatches(f, theFirst) {
		t.Error("expected match on day-of-month=1 even though not Monday")
	}
	// A Monday that is NOT the 1st: July 6 2026 is a Monday.
	monday := time.Date(2026, time.July, 6, 12, 0, 0, 0, time.UTC)
	if monday.Weekday() != time.Monday {
		t.Fatal("test precondition: July 6 2026 is Monday")
	}
	if !cronMatches(f, monday) {
		t.Error("expected match on Monday even though day-of-month != 1")
	}
	// Neither the 1st nor Monday at noon: July 2 2026 (Thursday).
	if cronMatches(f, time.Date(2026, time.July, 2, 12, 0, 0, 0, time.UTC)) {
		t.Error("expected no match when neither dom nor dow matches")
	}
}

func TestScheduleDue_BasicFire(t *testing.T) {
	now := time.Date(2026, time.September, 16, 17, 40, 0, 0, time.UTC)
	e := reminders.Entry{ID: "x", CronExpr: "*/10 * * * *", Prompt: "p"}
	due, next := scheduleDue(e, now)
	if !due {
		t.Error("expected due at a */10 boundary")
	}
	if next != 0 {
		t.Errorf("next should be 0 when due, got %v", next)
	}
	// One minute past the boundary: not due, next tick in 9 min.
	due, next = scheduleDue(e, now.Add(time.Minute))
	if due {
		t.Error("not expected due off-boundary")
	}
	if next != 9*time.Minute {
		t.Errorf("next = %v, want 9m", next)
	}
}

func TestScheduleDue_AlreadyRanSameMinute(t *testing.T) {
	now := time.Date(2026, time.September, 16, 17, 40, 0, 0, time.UTC)
	e := reminders.Entry{
		ID:       "x",
		CronExpr: "*/10 * * * *",
		Prompt:   "p",
		LastRun:  now.Format(time.RFC3339),
	}
	due, _ := scheduleDue(e, now)
	if due {
		t.Error("must not refire when LastRun is this exact minute")
	}
	// Within the same minute: a fire 30s into :40 still consumes :40.
	e.LastRun = now.Add(30 * time.Second).Format(time.RFC3339)
	due, _ = scheduleDue(e, now)
	if due {
		t.Error("must not refire when LastRun is inside this minute")
	}
}

func TestScheduleDue_CatchUpMissedTick(t *testing.T) {
	// A schedule due every 10 min that last ran 45 min ago: at a 10-min
	// boundary it should fire (catch-up) exactly once, and stamping the
	// current minute prevents a second immediate refire.
	now := time.Date(2026, time.September, 16, 17, 40, 0, 0, time.UTC)
	e := reminders.Entry{
		ID:       "x",
		CronExpr: "*/10 * * * *",
		Prompt:   "p",
		LastRun:  now.Add(-45 * time.Minute).Format(time.RFC3339),
	}
	due, _ := scheduleDue(e, now)
	if !due {
		t.Error("expected a catch-up fire for the missed tick")
	}
	e.LastRun = now.Format(time.RFC3339)
	due, _ = scheduleDue(e, now)
	if due {
		t.Error("second immediate refire after catch-up must not happen")
	}
}

func TestScheduleDue_PassiveReminderFilteredByWorker(t *testing.T) {
	// The gating rule lives in evaluate(): entries with no Prompt are
	// skipped before scheduleDue is consulted, so passive reminders
	// never fire and never influence the next-wake delay. scheduleDue
	// itself treats any entry with a valid cron expr as a candidate;
	// it is the worker's Prompt=="" filter that applies the policy.
	passive := reminders.Entry{ID: "x", CronExpr: "*/10 * * * *"} // no Prompt
	now := time.Date(2026, time.September, 16, 17, 41, 0, 0, time.UTC)

	// Raw function still computes a sane next tick for the expr —
	// the worker just never calls it for passive entries.
	due, next := scheduleDue(passive, now)
	if due {
		t.Error("17:41 is not a */10 boundary; should not be due")
	}
	if next != 9*time.Minute {
		t.Errorf("next = %v, want 9m (next */10 boundary)", next)
	}
}

func TestScheduleDue_InvalidCronFallsBack(t *testing.T) {
	e := reminders.Entry{ID: "x", CronExpr: "not a cron", Prompt: "p"}
	due, next := scheduleDue(e, time.Now())
	if due {
		t.Error("invalid cron must never fire")
	}
	if next != schedulePollFallback {
		t.Errorf("invalid cron should yield fallback wake, got %v", next)
	}
}

func TestScaleSchedulePrompt_ScalesToBudget(t *testing.T) {
	p := "Like 2-3 posts from AI engineers. Verify the like."
	out := scaleSchedulePrompt(p, 2, "linkedin", 28, 30)
	if out == p {
		t.Error("expected prompt to be rewritten when budget < 3")
	}
	// The like-instruction must be scaled to fit remaining (=2).
	if !regexp.MustCompile(`(?i)like\s+1-2\s+posts`).MatchString(out) {
		t.Errorf("expected scaled like target in %q", out)
	}
	// Budget note must carry the used/cap numbers.
	if !regexp.MustCompile(`28/30 linkedin likes used`).MatchString(out) {
		t.Errorf("expected budget note in %q", out)
	}
}

func TestScaleSchedulePrompt_LeavesNonLikePromptAlone(t *testing.T) {
	p := "Summarize today's trending AI repos."
	out := scaleSchedulePrompt(p, 10, "linkedin", 0, 30)
	// No leading like-instruction to rewrite: only the note is appended.
	if len(out) <= len(p) {
		t.Error("expected budget note appended")
	}
	if out[:len(p)] != p {
		t.Error("original prompt prefix must be preserved verbatim")
	}
}
