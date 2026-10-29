package tui

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/tjcoder-labs/cli/internal/reminders"
)

var formTestNow = time.Date(2026, time.October, 29, 8, 37, 0, 0, time.FixedZone("MDT", -6*3600))

func TestReminderForm_DefaultReadout(t *testing.T) {
	f := newReminderForm(formTestNow)
	pv := f.preview(formTestNow)
	if pv.Err != nil || pv.Cron != "*/5 * * * *" || pv.Summary != "every 5 minutes" {
		t.Fatalf("default preview = %+v", pv)
	}
	if want := time.Date(2026, 10, 29, 8, 40, 0, 0, formTestNow.Location()); !pv.Next.Equal(want) {
		t.Fatalf("next = %v", pv.Next)
	}
}

func TestReminderForm_OnceReadoutAndPastRejected(t *testing.T) {
	f := newReminderForm(formTestNow)
	f.Spec.Preset = reminders.PresetOnce
	f.Spec.Date, f.Spec.Time = "2026-11-03", "14:30"
	pv := f.preview(formTestNow)
	if pv.Cron != "30 14 3 11 *" || !pv.OneShot || pv.Summary != "once at Nov 3 14:30" {
		t.Fatalf("once preview = %+v", pv)
	}
	f.Spec.Time = "07:00"
	f.Spec.Date = "2026-10-29"
	if pv := f.preview(formTestNow); pv.Err == nil || !strings.Contains(pv.Err.Error(), "past") {
		t.Fatalf("past once accepted: %+v", pv)
	}
}

func TestReminderForm_CustomUsesCrontabInjectionCheck(t *testing.T) {
	f := newReminderForm(formTestNow)
	f.Spec.Preset = reminders.PresetCustom
	f.Spec.Custom = "0 9 * * 1-5"
	if pv := f.preview(formTestNow); pv.Err != nil || pv.Summary != "weekdays at 09:00" {
		t.Fatalf("custom preview = %+v", pv)
	}
	f.Spec.Custom = "0 9 * * x"
	if pv := f.preview(formTestNow); pv.Err == nil {
		t.Fatal("invalid custom cron accepted")
	}
}

func TestReminderForm_SubmitSavesToStore(t *testing.T) {
	ws := t.TempDir()
	f := newReminderForm(formTestNow)
	if _, _, err := f.submit(ws, "coder", formTestNow); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("empty message: err = %v", err)
	}
	f.Message = "   "
	if _, _, err := f.submit(ws, "coder", formTestNow); err == nil {
		t.Fatal("blank message accepted")
	}
	if _, err := os.Stat(reminders.FilePath(ws)); !os.IsNotExist(err) {
		t.Fatal("rejected submit must not create reminders.json")
	}

	f.Message = "stand up and stretch"
	e, _, err := f.submit(ws, "coder", formTestNow)
	if err != nil || e.CronExpr != "*/5 * * * *" || e.Message != "stand up and stretch" || e.Prompt != "" || e.OneShot {
		t.Fatalf("saved %+v %v", e, err)
	}

	g := newReminderForm(formTestNow)
	g.Message = "summarise my inbox"
	g.AsPrompt = true
	g.Spec.Preset = reminders.PresetOnce
	g.Spec.Date, g.Spec.Time = "2026-11-03", "14:30"
	e2, _, err := g.submit(ws, "researcher", formTestNow)
	if err != nil || e2.Prompt != "summarise my inbox" || e2.Agent != "researcher" || e2.Message != "" || !e2.OneShot || e2.CronExpr != "30 14 3 11 *" {
		t.Fatalf("saved schedule %+v %v", e2, err)
	}
	list, _ := reminders.NewStore().Load(ws)
	if list.Len() != 2 {
		t.Fatalf("store has %d entries", list.Len())
	}
	if got, _ := list.Get(e2.ID); !got.OneShot {
		t.Fatal("one_shot not persisted")
	}
}

type tuiFakeCrontab struct{ content string }

func (f *tuiFakeCrontab) Read() (string, error) { return f.content, nil }
func (f *tuiFakeCrontab) Write(c string) error  { f.content = c; return nil }

// A due passive reminder is surfaced in Activity; a due one-shot is
// removed from reminders.json together with its crontab line.
func TestScheduler_PassiveNotifyAndOneShotRetire(t *testing.T) {
	a, _ := startSimApp(t)
	ws := a.workspaceRoot
	s := reminders.NewStore()
	_, rec, _ := s.Create(ws, reminders.CreateInput{CronExpr: "*/5 * * * *", Message: "drink water"})
	_, once, _ := s.Create(ws, reminders.CreateInput{CronExpr: "40 8 29 10 *", Message: "call mum", OneShot: true, InstallCron: true})
	line, _ := reminders.CronLine(once, "/bin/coder", ws)
	fc := &tuiFakeCrontab{content: "17 3 * * * backup\n" + line + "\n"}

	w := newScheduleWorker(a)
	w.cron = fc
	now := time.Date(2026, 10, 29, 8, 40, 0, 0, time.Local)
	w.evaluate(now)

	waitForText(t, a, "call mum", func() string { return a.activity.GetText(true) })
	var act string
	onLoop(t, a, func() { act = a.activity.GetText(true) })
	if !strings.Contains(act, "◆ reminder drink water") || !strings.Contains(act, "◆ reminder call mum") {
		t.Fatalf("activity = %q", act)
	}
	waitFor(t, a, "one-shot retired", func() bool { return strings.Contains(a.activity.GetText(true), "done and removed") })
	list, _ := s.Load(ws)
	if _, ok := list.Get(once.ID); ok {
		t.Fatal("one-shot still in reminders.json")
	}
	if got, ok := list.Get(rec.ID); !ok || got.LastRun == "" {
		t.Fatalf("recurring reminder = %+v %v (want kept, last_run stamped)", got, ok)
	}
	if fc.content != "17 3 * * * backup\n" {
		t.Fatalf("crontab = %q", fc.content)
	}
	// Same minute again: nothing refires.
	before := strings.Count(a.activity.GetText(true), "drink water")
	w.evaluate(now)
	time.Sleep(50 * time.Millisecond)
	var after int
	onLoop(t, a, func() { after = strings.Count(a.activity.GetText(true), "drink water") })
	if after != before {
		t.Fatal("reminder refired in the same minute")
	}
}
