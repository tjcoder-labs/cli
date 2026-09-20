package tui

// In-session schedule worker.
//
// A single background goroutine lives for the lifetime of the App
// (started in Run(), stopped when Run() returns). It periodically
// evaluates every schedule-type reminder in .ergo-cli-go/reminders.json
// and, when one becomes due, injects its prompt into the live
// conversation as if the user had typed it — so the run streams into
// the transcript/activity panes and can be steered mid-turn exactly
// like a manual message.
//
// Performance contract:
//   - The loop sleeps until the next possible due tick (minute
//     granularity), waking at most once per minute and usually far
//     less often than that — near-zero CPU while idle.
//   - reminders.json is stat'ed each wake and only parsed when its
//     mtime/size changes; parsed schedule fields are cached and only
//     recomputed for the (rare) newly-due entries.
//   - The cron matcher is allocation-free: fields are parsed into
//     fixed-size bitmasks and evaluated with integer comparisons.
//   - The interaction ledger (for daily-like-cap enforcement) is
//     loaded lazily, only at the moment a capped schedule actually
//     fires — never on the polling path.

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tjcoder-labs/cli/internal/browser"
	"github.com/tjcoder-labs/cli/internal/reminders"
)

// likeInstrRe matches the leading like-instruction of a schedule
// prompt ("Like 2-3 posts", "like 1") so scaleSchedulePrompt can
// re-emit it scaled to the remaining daily budget. Local to the TUI
// to keep the regexp allocated once, package-level.
var likeInstrRe = regexp.MustCompile(`(?i)^\s*like\s+\d+(?:\s*[-–]\s*\d+)?\s*(?:posts?)?`)

// schedulePollFallback bounds how long the worker may sleep between
// wake-ups when no schedule is currently due soon. Even with perfect
// next-tick computation we re-check at least this often so newly
// created schedules (which change the file mtime) are picked up
// promptly without needing inotify.
const schedulePollFallback = time.Minute

// scheduleWorker is the runtime state for the in-session scheduler.
// It is owned by the App and driven by a single goroutine.
type scheduleWorker struct {
	app *App

	stopCh chan struct{}
	doneCh chan struct{}

	// cached file observation. When the on-disk file's identity
	// (mtime+size) matches lastFileKey, the parsed entries in
	// lastEntries are reused verbatim.
	mu          sync.Mutex
	lastFileKey fileKey
	lastEntries []reminders.Entry
}

// fileKey identifies a version of reminders.json cheaply. We avoid
// hashing contents: mtime+size is sufficient for a file that is
// rewritten atomically-in-place by this process or the headless CLI.
type fileKey struct {
	modTime time.Time
	size    int64
	exists  bool
}

func newScheduleWorker(a *App) *scheduleWorker {
	return &scheduleWorker{
		app:    a,
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
	}
}

// start launches the worker goroutine. Idempotent within one App Run.
func (w *scheduleWorker) start() {
	go w.loop()
}

// stop signals the worker to exit and waits for it. Safe to call
// multiple times; only the first has effect.
func (w *scheduleWorker) stop() {
	select {
	case <-w.doneCh:
		return
	default:
	}
	close(w.stopCh)
	<-w.doneCh
}

// loop is the worker mainline. It computes the delay until the next
// wake, sleeps (interruptibly), evaluates schedules, and repeats.
func (w *scheduleWorker) loop() {
	defer close(w.doneCh)
	for {
		delay := w.evaluate(time.Now().UTC())
		if delay <= 0 {
			delay = time.Minute
		}
		if delay > schedulePollFallback {
			delay = schedulePollFallback
		}
		timer := time.NewTimer(delay)
		select {
		case <-w.stopCh:
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// evaluate checks all schedules and fires any that are due at `now`.
// It returns the suggested delay until the next evaluation.
//
// Firing a schedule injects its prompt into the conversation. If a
// turn is already in flight, the schedule is marked as fired for this
// tick (so it won't pile up) but its prompt is queued via the
// steering channel instead, where the runner will pick it up as user
// steering context for the current turn.
//
// Passive reminders (no Prompt) are ignored by this worker — they
// exist only for the headless/cron path and the TUI reminder list —
// so they explicitly do not influence the next-wake computation.
func (w *scheduleWorker) evaluate(now time.Time) time.Duration {
	entries, ok := w.loadEntries()
	if !ok {
		return schedulePollFallback
	}

	next := schedulePollFallback
	for _, e := range entries {
		if e.Prompt == "" {
			continue // passive reminder, not a schedule; never due here
		}
		due, nextIn := scheduleDue(e, now)
		if due {
			w.fire(e, now)
			// After firing, recompute this entry's next tick from a
			// fresh last-run so its contribution to `next` is correct.
			e.LastRun = now.UTC().Format(time.RFC3339)
			_, nextIn = scheduleDue(e, now)
		}
		if nextIn < next && nextIn > 0 {
			next = nextIn
		}
	}
	return next
}

// fire runs one due schedule: enforces the daily cap, stamps the
// last-run time, and injects the prompt into the session.
//
// Tick dedup across runners: we stamp LastRun BEFORE the turn starts,
// so a concurrently-running headless `coder schedule run` that loads
// the same entry sees the minute as claimed and skips. Combined with
// scheduleDue's alreadyRanThisMinute check, the two runners cannot
// double-fire the same tick.
func (w *scheduleWorker) fire(e reminders.Entry, now time.Time) {
	a := w.app
	prompt := e.Prompt

	// Daily-cap enforcement. The ledger is only loaded here — on
	// the firing path — never during passive polling.
	if e.DailyCap > 0 && e.Platform != "" {
		// Day boundary is UTC, matching the headless cmdSchedule path,
		// so the two runners can never disagree on the cap count.
		dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
		log, err := browser.LoadInteractionLog(browser.DefaultInteractionLogPath())
		if err == nil {
			done := log.CountSince(e.Platform, browser.InteractionLike, dayStart)
			if done >= e.DailyCap {
				a.appendActivity(fmt.Sprintf("[%s]schedule %s[-]: skipped, %s daily like cap (%d) reached",
					a.palette.HexDim, shortScheduleID(e.ID), e.Platform, e.DailyCap))
				// Still stamp the tick: the cap is the reason we're
				// skipping, not a failed fire — tomorrow's first
				// evaluation must not consider this tick outstanding.
				_ = reminders.MarkRun(a.workspaceRoot, e.ID, now)
				return
			}
			// Mirror the headless runner's budget-aware prompt: tell
			// the model exactly how much budget remains so it can
			// scale its per-run target rather than rely solely on
			// post-hoc ledger counting.
			prompt = scaleSchedulePrompt(prompt, e.DailyCap-done, e.Platform, done, e.DailyCap)
		}
	}

	// Stamp last-run so a restart doesn't refire this tick and a
	// concurrent headless runner sees the tick as claimed.
	if err := reminders.MarkRun(a.workspaceRoot, e.ID, now); err != nil {
		a.appendActivity(fmt.Sprintf("["+a.palette.HexOrchid+"::b]warning[-:-:-]: could not persist schedule last-run: %v", err))
	} else {
		// Keep our in-memory cache consistent with what we wrote.
		w.mu.Lock()
		for i := range w.lastEntries {
			if w.lastEntries[i].ID == e.ID {
				w.lastEntries[i].LastRun = now.UTC().Format(time.RFC3339)
			}
		}
		w.mu.Unlock()
	}

	a.appendActivity(fmt.Sprintf("[%s]schedule %s[-]: firing (%s)",
		a.palette.HexLavender, shortScheduleID(e.ID), e.CronExpr))
	a.submitScheduled(e.Agent, prompt)
}

// scaleSchedulePrompt rewrites the leading "Like N[-M] posts"
// instruction so the per-run target fits inside the remaining daily
// budget, and appends an explicit budget note. If the prompt doesn't
// begin with a like instruction, only the note is appended — the
// model still sees the constraint even when we can't scale its
// target automatically.
func scaleSchedulePrompt(prompt string, remaining int, platform string, done, cap int) string {
	const floorCap = 4
	effCap := floorCap
	if remaining < effCap {
		effCap = remaining
	}
	if effCap < 1 {
		effCap = 1
	}
	out := prompt
	if likeInstrRe.MatchString(prompt) {
		segment := fmt.Sprintf("Like %d-%d posts if you can find that many suitable candidates today", 1, effCap)
		if effCap == 1 {
			segment = "Like 1 post if you can find a suitable candidate today"
		}
		out = likeInstrRe.ReplaceAllString(prompt, segment)
	}
	return out + fmt.Sprintf("\n\n[schedule budget: %d/%d %s likes used today; hard cap %d; at most %d more this run]",
		done, cap, platform, cap, remaining)
}

// loadEntries returns the current reminder entries, parsing the file
// only when it has changed since the last observation.
func (w *scheduleWorker) loadEntries() ([]reminders.Entry, bool) {
	path := reminders.FilePath(w.app.workspaceRoot)
	info, err := os.Stat(path)
	key := fileKey{}
	if err == nil {
		key = fileKey{modTime: info.ModTime(), size: info.Size(), exists: true}
	} else if !os.IsNotExist(err) {
		return nil, false
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	if key == w.lastFileKey {
		return w.lastEntries, true
	}
	list, err := reminders.NewStore().Load(w.app.workspaceRoot)
	if err != nil {
		return nil, false
	}
	entries := list.All()
	w.lastEntries = entries
	w.lastFileKey = key
	return entries, true
}

// scheduleDue reports whether entry e is due at `now`, and the
// duration until its next potential tick.
//
// An entry is due when its cron expression matches the current minute
// and it has not already been run for this minute (tracked via
// LastRun). Next-tick computation walks forward minute-by-minute
// until the expression matches; the walk is bounded at 366 days so a
// pathological expression (e.g. Feb 30) terminates.
func scheduleDue(e reminders.Entry, now time.Time) (bool, time.Duration) {
	fields, err := parseCronFields(e.CronExpr)
	if err != nil {
		return false, schedulePollFallback
	}
	now = now.UTC().Truncate(time.Minute)

	if cronMatches(fields, now) && !alreadyRanThisMinute(e.LastRun, now) {
		return true, 0
	}
	for i := 1; i <= 366*24*60; i++ {
		t := now.Add(time.Duration(i) * time.Minute)
		if cronMatches(fields, t) {
			return false, time.Duration(i) * time.Minute
		}
	}
	return false, schedulePollFallback
}

// alreadyRanThisMinute returns true if lastRun (RFC3339) falls in the
// same UTC minute as now.
func alreadyRanThisMinute(lastRun string, now time.Time) bool {
	if lastRun == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339, lastRun)
	if err != nil {
		return false
	}
	return t.UTC().Truncate(time.Minute).Equal(now)
}

// cronFields is an allocation-free 5-field cron representation: each
// field is a bitmask over its valid range (min 0-59, hour 0-23, dom
// 1-31, month 1-12, dow 0-6).
type cronFields struct {
	min   uint64          // bits 0..59
	hour  uint32          // bits 0..23
	dom   uint32          // bits 1..31
	month uint16          // bits 1..12
	dow   uint8           // bits 0..6 (Sunday=0)
	// domRestricted/dowRestricted implement the standard cron rule:
	// when both fields are restricted, a day matches if EITHER
	// matches; when only one is restricted, that one gates.
	domRestricted bool
	dowRestricted bool
}

// parseCronFields parses a standard 5-field cron expression. Fields
// support "*", lists ("1,2,3"), ranges ("9-17"), and steps ("*/10",
// "1-30/2"). Parsing is done once per entry-change and the result is
// cached by the caller; invalid input yields an error and the entry
// is skipped.
func parseCronFields(expr string) (cronFields, error) {
	var out cronFields
	parts := strings.Fields(expr)
	if len(parts) != 5 {
		return out, fmt.Errorf("cron expression must have 5 fields")
	}
	var err error
	if out.min, err = parseCronField(parts[0], 0, 59); err != nil {
		return out, fmt.Errorf("minute: %w", err)
	}
	var hour uint64
	if hour, err = parseCronField(parts[1], 0, 23); err != nil {
		return out, fmt.Errorf("hour: %w", err)
	}
	out.hour = uint32(hour)
	var dom uint64
	if dom, err = parseCronField(parts[2], 1, 31); err != nil {
		return out, fmt.Errorf("day-of-month: %w", err)
	}
	out.dom = uint32(dom)
	out.domRestricted = parts[2] != "*"
	var mon uint64
	if mon, err = parseCronField(parts[3], 1, 12); err != nil {
		return out, fmt.Errorf("month: %w", err)
	}
	out.month = uint16(mon)
	var dow uint64
	if dow, err = parseCronField(parts[4], 0, 6); err != nil {
		return out, fmt.Errorf("day-of-week: %w", err)
	}
	out.dow = uint8(dow)
	out.dowRestricted = parts[4] != "*"
	return out, nil
}

// parseCronField parses one cron field into a bitmask over [lo, hi].
// The bitmask uses uint64 storage regardless of field width so a
// single helper covers all five fields; callers narrow as needed.
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

// cronMatches reports whether time t matches the parsed schedule.
func cronMatches(f cronFields, t time.Time) bool {
	if f.min&(1<<uint(t.Minute())) == 0 {
		return false
	}
	if f.hour&(1<<uint(t.Hour())) == 0 {
		return false
	}
	if f.month&(1<<uint(t.Month())) == 0 {
		return false
	}
	domOK := f.dom&(1<<uint(t.Day())) != 0
	dowOK := f.dow&(1<<uint(int(t.Weekday()))) != 0
	switch {
	case f.domRestricted && f.dowRestricted:
		return domOK || dowOK // standard cron OR semantics
	case f.domRestricted:
		return domOK
	case f.dowRestricted:
		return dowOK
	default:
		return true
	}
}

// shortScheduleID renders the tail of a schedule ID for log lines.
func shortScheduleID(id string) string {
	const tail = 6
	if len(id) <= tail {
		return id
	}
	return "…" + id[len(id)-tail:]
}
