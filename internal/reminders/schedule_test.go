package reminders

import (
	"errors"
	"strings"
	"testing"
	"time"
)

var denver = func() *time.Location {
	l, err := time.LoadLocation("America/Denver")
	if err != nil {
		return time.FixedZone("MDT", -6*3600)
	}
	return l
}()

// Thu Oct 29 2026 08:37 local.
var formNow = time.Date(2026, time.October, 29, 8, 37, 12, 0, denver)

func TestBuildCron_Presets(t *testing.T) {
	cases := []struct {
		spec Spec
		want string
		once bool
	}{
		{Spec{Preset: PresetEvery5}, "*/5 * * * *", false},
		{Spec{Preset: PresetEvery15}, "*/15 * * * *", false},
		{Spec{Preset: PresetEvery30}, "*/30 * * * *", false},
		{Spec{Preset: PresetHourly}, "0 * * * *", false},
		{Spec{Preset: PresetDaily, Time: "09:05"}, "5 9 * * *", false},
		{Spec{Preset: PresetDaily, Time: "7:30"}, "30 7 * * *", false},
		{Spec{Preset: PresetWeekdays, Time: "18:00"}, "0 18 * * 1-5", false},
		{Spec{Preset: PresetWeekly, Time: "10:15", Weekday: time.Wednesday}, "15 10 * * 3", false},
		{Spec{Preset: PresetWeekly, Time: "10:15", Weekday: time.Sunday}, "15 10 * * 0", false},
		{Spec{Preset: PresetOnce, Date: "2026-11-03", Time: "14:30"}, "30 14 3 11 *", true},
		{Spec{Preset: PresetCustom, Custom: "  0  */2 * * 1-5 "}, "0 */2 * * 1-5", false},
	}
	for _, c := range cases {
		got, once, err := BuildCron(c.spec, formNow)
		if err != nil || got != c.want || once != c.once {
			t.Errorf("%s: got (%q, %v, %v), want (%q, %v)", PresetLabels[c.spec.Preset], got, once, err, c.want, c.once)
		}
	}
}

func TestBuildCron_Validation(t *testing.T) {
	cases := []struct {
		spec Spec
		want string
	}{
		{Spec{Preset: PresetDaily, Time: "9am"}, "HH:MM"},
		{Spec{Preset: PresetDaily, Time: "25:00"}, "HH:MM"},
		{Spec{Preset: PresetOnce, Date: "11/03/2026", Time: "14:30"}, "YYYY-MM-DD"},
		{Spec{Preset: PresetOnce, Date: "2026-10-29", Time: "08:00"}, "in the past"},
		{Spec{Preset: PresetOnce, Date: "2026-10-29", Time: "08:37"}, "in the past"}, // same minute is not future
		{Spec{Preset: PresetOnce, Date: "2028-01-01", Time: "08:00"}, "within a year"},
		{Spec{Preset: PresetCustom, Custom: ""}, "5-field"},
		{Spec{Preset: PresetCustom, Custom: "* * *"}, "5 fields"},
		{Spec{Preset: PresetCustom, Custom: "61 * * * *"}, "minute"},
	}
	for _, c := range cases {
		_, _, err := BuildCron(c.spec, formNow)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%+v: err = %v, want containing %q", c.spec, err, c.want)
		}
	}
}

func TestDefaultSpec_RevealedFieldsAreValid(t *testing.T) {
	s := DefaultSpec(formNow)
	if s.Preset != PresetEvery5 || PresetLabels[s.Preset] != "Every 5 minutes" {
		t.Fatalf("default preset = %v", s.Preset)
	}
	if s.Date != "2026-10-29" || s.Time != "08:42" {
		t.Fatalf("defaults = %s %s, want today and now+5m", s.Date, s.Time)
	}
	s.Preset = PresetOnce
	cron, once, err := BuildCron(s, formNow)
	if err != nil || cron != "42 8 29 10 *" || !once {
		t.Fatalf("once with defaults = %q %v %v", cron, once, err)
	}
}

func TestNextRun_LocalWallClock(t *testing.T) {
	next, err := NextRun("*/5 * * * *", formNow)
	if err != nil || !next.Equal(time.Date(2026, 10, 29, 8, 40, 0, 0, denver)) {
		t.Fatalf("*/5 next = %v %v", next, err)
	}
	next, _ = NextRun("30 14 3 11 *", formNow)
	if want := time.Date(2026, 11, 3, 14, 30, 0, 0, denver); !next.Equal(want) {
		t.Fatalf("once next = %v, want %v", next, want)
	}
	if _, err := NextRun("0 0 30 2 *", formNow); err == nil {
		t.Fatal("Feb 30 must never fire")
	}
}

func TestDescribe(t *testing.T) {
	cases := map[string]string{
		"*/5 * * * *":   "every 5 minutes",
		"* * * * *":     "every minute",
		"0 * * * *":     "every hour",
		"15 * * * *":    "every hour at :15",
		"5 9 * * *":     "every day at 09:05",
		"0 18 * * 1-5":  "weekdays at 18:00",
		"15 10 * * 3":   "every Wednesday at 10:15",
		"30 14 3 11 *":  "every year on Nov 3 at 14:30",
		"0 */2 * * 1-5": "cron 0 */2 * * 1-5",
	}
	for expr, want := range cases {
		if got := Describe(expr, false); got != want {
			t.Errorf("Describe(%q) = %q, want %q", expr, got, want)
		}
	}
	if got := Describe("30 14 3 11 *", true); got != "once at Nov 3 14:30" {
		t.Errorf("one-shot = %q", got)
	}
}

type fakeCrontab struct {
	content string
	readErr error
	writes  int
}

func (f *fakeCrontab) Read() (string, error) { return f.content, f.readErr }
func (f *fakeCrontab) Write(c string) error  { f.content = c; f.writes++; return nil }

func TestCronLine_Shapes(t *testing.T) {
	ws := "/w/it's"
	sched, _ := CronLine(Entry{ID: "S1", CronExpr: "*/5 * * * *", Prompt: "p"}, "/bin/coder", ws)
	if want := `*/5 * * * * cd '/w/it'\''s' && '/bin/coder' schedule run S1 >> '/w/it's/.ergo-cli-go/schedule.log' 2>&1`; sched != want {
		t.Errorf("schedule line = %s", sched)
	}
	once, _ := CronLine(Entry{ID: "O1", CronExpr: "30 14 3 11 *", Message: "m", OneShot: true}, "/bin/coder", "/w")
	if !strings.Contains(once, "schedule run O1") {
		t.Errorf("one-shot passive must re-invoke the binary so it can retire itself: %s", once)
	}
	echo, _ := CronLine(Entry{ID: "R1", CronExpr: "0 9 * * *", Message: "it's 9"}, "", "/w")
	if echo != `0 9 * * * echo 'it'\''s 9' >> /w/.ergo-cli-go/reminders.log` {
		t.Errorf("echo line = %s", echo)
	}
	if _, err := CronLine(Entry{CronExpr: "* * * * *", Message: "a\nb"}, "", "/w"); err == nil {
		t.Error("newline in message must be rejected")
	}
}

func TestRemoveCronLines_ExactMatchOnly(t *testing.T) {
	ws := "/w"
	echo, _ := CronLine(Entry{ID: "R1", CronExpr: "0 9 * * *", Message: "hi"}, "", ws)
	fc := &fakeCrontab{content: strings.Join([]string{
		"# header comment mentioning schedule run AB",
		"17 3 * * * /usr/bin/true backup",
		"*/5 * * * * cd '/w' && '/bin/coder' schedule run AB >> '/w/.ergo-cli-go/schedule.log' 2>&1",
		"*/5 * * * * cd '/w' && '/bin/coder' schedule run ABC >> '/w/.ergo-cli-go/schedule.log' 2>&1",
		echo,
		"",
	}, "\n")}
	n, err := RemoveCronLines(fc, Entry{ID: "AB", Prompt: "p"}, ws)
	if err != nil || n != 1 {
		t.Fatalf("removed %d, err %v", n, err)
	}
	if strings.Contains(fc.content, "schedule run AB >>") || !strings.Contains(fc.content, "schedule run ABC") ||
		!strings.Contains(fc.content, "backup") || !strings.Contains(fc.content, "# header comment") {
		t.Fatalf("wrong lines removed:\n%s", fc.content)
	}
	n, _ = RemoveCronLines(fc, Entry{ID: "R1", CronExpr: "0 9 * * *", Message: "hi"}, ws)
	if n != 1 || strings.Contains(fc.content, "echo 'hi'") {
		t.Fatalf("legacy echo line not removed (%d):\n%s", n, fc.content)
	}
	writes := fc.writes
	if n, _ := RemoveCronLines(fc, Entry{ID: "ZZ", Prompt: "p"}, ws); n != 0 || fc.writes != writes {
		t.Fatal("no match must not rewrite the crontab")
	}
}

func TestStoreRemove_OneShotAndCrontab(t *testing.T) {
	ws := t.TempDir()
	s := NewStore()
	_, e, err := s.Create(ws, CreateInput{CronExpr: "30 14 3 11 *", Message: "dentist", InstallCron: true, OneShot: true})
	if err != nil {
		t.Fatal(err)
	}
	list, _ := s.Load(ws)
	if got, _ := list.Get(e.ID); !got.OneShot || !got.Installed {
		t.Fatalf("one_shot/installed not persisted: %+v", got)
	}
	line, _ := CronLine(e, "/bin/coder", ws)
	fc := &fakeCrontab{content: "17 3 * * * backup\n" + line + "\n"}
	res, found, err := s.Remove(ws, e.ID, fc)
	if err != nil || !found || res.CronRemoved != 1 || res.Entry.ID != e.ID {
		t.Fatalf("Remove = %+v %v %v", res, found, err)
	}
	if fc.content != "17 3 * * * backup\n" {
		t.Fatalf("crontab after = %q", fc.content)
	}
	if list, _ := s.Load(ws); list.Len() != 0 {
		t.Fatal("entry not removed from reminders.json")
	}
	if _, found, _ := s.Remove(ws, e.ID, fc); found {
		t.Fatal("second remove must report not found")
	}
}

func TestStoreRemove_CrontabUnavailableStillRemovesEntry(t *testing.T) {
	ws := t.TempDir()
	s := NewStore()
	_, e, _ := s.Create(ws, CreateInput{CronExpr: "* * * * *", Message: "m", InstallCron: true})
	res, found, err := s.Remove(ws, e.ID, &fakeCrontab{readErr: ErrNoCrontab})
	if err != nil || !found || !errors.Is(res.CronErr, ErrNoCrontab) {
		t.Fatalf("Remove = %+v %v %v", res, found, err)
	}
	if list, _ := s.Load(ws); list.Len() != 0 {
		t.Fatal("entry must be removed even when crontab is unavailable")
	}
}

func TestSetInstalled(t *testing.T) {
	ws := t.TempDir()
	s := NewStore()
	_, e, _ := s.Create(ws, CreateInput{CronExpr: "* * * * *", Message: "m", InstallCron: true})
	if err := s.SetInstalled(ws, e.ID, false); err != nil {
		t.Fatal(err)
	}
	list, _ := s.Load(ws)
	if got, _ := list.Get(e.ID); got.Installed {
		t.Fatal("Installed still true")
	}
}

func TestInstallCronLine_NoBlankLinesAndRoundTrip(t *testing.T) {
	for _, start := range []string{"", "17 3 * * * backup", "17 3 * * * backup\n"} {
		fc := &fakeCrontab{content: start}
		e := Entry{ID: "X1", CronExpr: "*/5 * * * *", Prompt: "p"}
		line, _ := CronLine(e, "/bin/coder", "/w")
		if err := InstallCronLine(fc, line); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(fc.content, "\n\n") || !strings.HasSuffix(fc.content, line+"\n") {
			t.Fatalf("start %q -> %q", start, fc.content)
		}
		RemoveCronLines(fc, e, "/w")
		if want := strings.TrimSuffix(start, "\n"); strings.TrimSuffix(fc.content, "\n") != want {
			t.Fatalf("round trip %q -> %q", start, fc.content)
		}
	}
}
