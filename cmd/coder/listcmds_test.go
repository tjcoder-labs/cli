package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tjcoder-labs/cli/internal/reminders"
)

type memCrontab struct {
	content string
	missing bool // behave as if crontab(1) is not installed
	writes  int
}

func (m *memCrontab) Read() (string, error) {
	if m.missing {
		return "", reminders.ErrNoCrontab
	}
	return m.content, nil
}

func (m *memCrontab) Write(s string) error {
	if m.missing {
		return reminders.ErrNoCrontab
	}
	m.writes++
	m.content = s
	return nil
}

var cliNow = time.Date(2026, 10, 29, 8, 30, 0, 0, time.Local)

func testEnv(cron reminders.CrontabRunner) (cliEnv, *bytes.Buffer, *bytes.Buffer) {
	var out, errb bytes.Buffer
	return cliEnv{stdout: &out, stderr: &errb, now: cliNow, cron: cron}, &out, &errb
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

const seedReminders = `[
 {"id":"rem-aaaaaaaaaaaa0001","cron_expr":"0 9 * * 1-5","message":"","prompt":"summarise CI","agent":"software-engineer","created_at":"2026-10-20T15:00:00Z","installed":true,"last_run":"2026-10-28T15:00:00Z"},
 {"id":"rem-aaaaaaaaaaaa0002","cron_expr":"*/30 * * * *","message":"stretch\nand drink water","created_at":"2026-10-27T16:10:00Z"},
 {"id":"rem-aaaaaaaaaaaa0003","cron_expr":"0 8 1 10 *","message":"renew domain","created_at":"2026-09-30T15:00:00Z","one_shot":true}
]`

func seedWorkspace(t *testing.T) string {
	t.Helper()
	ws := t.TempDir()
	writeFile(t, reminders.FilePath(ws), seedReminders)
	return ws
}

func TestRemindersList_TablePlainAndSorted(t *testing.T) {
	ws := seedWorkspace(t)
	env, out, _ := testEnv(&memCrontab{})
	if code := runReminders([]string{"ls"}, ws, env); code != 0 {
		t.Fatalf("exit %d", code)
	}
	s := out.String()
	if strings.Contains(s, "\x1b[") {
		t.Fatalf("plain output has escape codes:\n%s", s)
	}
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) != 4 || !strings.HasPrefix(lines[0], "ID") {
		t.Fatalf("want header + 3 rows, got:\n%s", s)
	}
	// Soonest first (every 30 min → 09:00, weekdays 09:00 tie keeps
	// stable order), missed one-shot last.
	if !strings.Contains(lines[3], "rem-aaaaaaaaaaaa0003") || !strings.Contains(lines[3], "missed") {
		t.Fatalf("missed one-shot should sort last:\n%s", s)
	}
	for _, want := range []string{"every 30 minutes", "weekdays at 09:00", "schedule", "reminder", "yes", "never", "stretch and drink water"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
	for _, l := range lines {
		if strings.HasSuffix(l, " ") {
			t.Errorf("trailing whitespace: %q", l)
		}
	}
	// Columns line up: every row has the CRON column at the header's offset.
	col := strings.Index(lines[0], "CRON")
	for _, l := range lines[1:] {
		if r := []rune(l); len(r) < col || r[col-1] != ' ' || r[col] == ' ' {
			t.Errorf("misaligned row: %q", l)
		}
	}
}

func TestRemindersList_JSONHasNextRunAndKind(t *testing.T) {
	ws := seedWorkspace(t)
	env, out, _ := testEnv(&memCrontab{})
	if code := runReminders([]string{"list", "--json"}, ws, env); code != 0 {
		t.Fatalf("exit %d", code)
	}
	var got []map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("bad json: %v\n%s", err, out)
	}
	if len(got) != 3 {
		t.Fatalf("want 3, got %d", len(got))
	}
	byID := map[string]map[string]any{}
	for _, g := range got {
		byID[g["id"].(string)] = g
	}
	sch := byID["rem-aaaaaaaaaaaa0001"]
	if sch["kind"] != "schedule" || sch["cron_expr"] != "0 9 * * 1-5" || sch["prompt"] != "summarise CI" {
		t.Fatalf("schedule entry fields: %v", sch)
	}
	next, err := time.Parse(time.RFC3339, sch["next_run"].(string))
	if err != nil || !next.Equal(time.Date(2026, 10, 29, 9, 0, 0, 0, time.Local)) {
		t.Fatalf("next_run = %v (%v)", sch["next_run"], err)
	}
	miss := byID["rem-aaaaaaaaaaaa0003"]
	if miss["missed"] != true || miss["next_run"] != nil {
		t.Fatalf("missed one-shot: %v", miss)
	}
}

func TestRemindersList_EmptyIsFriendly(t *testing.T) {
	ws := t.TempDir()
	env, out, _ := testEnv(&memCrontab{})
	if code := runReminders(nil, ws, env); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out.String(), "No reminders in "+ws) {
		t.Fatalf("got %q", out)
	}
	out.Reset()
	runReminders([]string{"ls", "--json"}, ws, env)
	if strings.TrimSpace(out.String()) != "[]" {
		t.Fatalf("empty json = %q", out)
	}
}

func TestRemindersList_WorkspaceRootFlag(t *testing.T) {
	ws := seedWorkspace(t)
	env, out, _ := testEnv(&memCrontab{})
	if code := runReminders([]string{"ls", "--workspace-root", ws}, t.TempDir(), env); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out.String(), "rem-aaaaaaaaaaaa0001") {
		t.Fatalf("--workspace-root not honoured:\n%s", out)
	}
}

func TestRemindersShow_SuffixAndCrontabState(t *testing.T) {
	ws := seedWorkspace(t)
	e := reminders.Entry{ID: "rem-aaaaaaaaaaaa0001", CronExpr: "0 9 * * 1-5", Prompt: "summarise CI"}
	cron := &memCrontab{content: mustCronLine(t, e, ws) + "\n"}
	env, out, _ := testEnv(cron)
	if code := runReminders([]string{"show", "0001"}, ws, env); code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, want := range []string{"rem-aaaaaaaaaaaa0001", "weekdays at 09:00", "(0 9 * * 1-5)", "installed · line present", "summarise CI", "software-engineer"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("show missing %q:\n%s", want, out)
		}
	}

	cron.content = ""
	out.Reset()
	runReminders([]string{"show", "…0001"}, ws, env)
	if !strings.Contains(out.String(), "line missing from crontab") {
		t.Errorf("want missing-line note:\n%s", out)
	}

	env.cron = &memCrontab{missing: true}
	out.Reset()
	runReminders([]string{"show", "rem-aaaaaaaaaaaa0001", "--json"}, ws, env)
	var v map[string]any
	if err := json.Unmarshal(out.Bytes(), &v); err != nil || v["kind"] != "schedule" {
		t.Fatalf("show --json: %v %s", err, out)
	}
}

func TestRemindersShow_UnknownAndAmbiguous(t *testing.T) {
	ws := seedWorkspace(t)
	env, _, errb := testEnv(&memCrontab{})
	if code := runReminders([]string{"show", "nope"}, ws, env); code != 1 || !strings.Contains(errb.String(), `no entry with id "nope"`) {
		t.Fatalf("unknown: exit %d, %q", code, errb)
	}
	errb.Reset()
	if code := runReminders([]string{"show", "rem-aaaa"}, ws, env); code != 1 || !strings.Contains(errb.String(), "ambiguous") {
		t.Fatalf("ambiguous: exit %d, %q", code, errb)
	}
	errb.Reset()
	if code := runReminders([]string{"frobnicate"}, ws, env); code != 2 {
		t.Fatalf("unknown subcommand exit %d", code)
	}
}

func TestRemindersRm_RemovesOnlyItsCrontabLine(t *testing.T) {
	ws := seedWorkspace(t)
	e := reminders.Entry{ID: "rem-aaaaaaaaaaaa0001", CronExpr: "0 9 * * 1-5", Prompt: "summarise CI"}
	keep := []string{
		"# keep me",
		"17 3 * * * /usr/bin/true backup",
		"0 9 * * 1-5 /usr/local/bin/coder schedule run rem-aaaaaaaaaaaa00012",
	}
	cron := &memCrontab{content: keep[0] + "\n" + keep[1] + "\n" + mustCronLine(t, e, ws) + "\n" + keep[2] + "\n"}
	env, out, errb := testEnv(cron)
	if code := runReminders([]string{"rm", "0001"}, ws, env); code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if !strings.Contains(out.String(), "removed schedule rem-aaaaaaaaaaaa0001") || !strings.Contains(out.String(), "crontab line removed") {
		t.Fatalf("confirmation: %q", out)
	}
	if want := strings.Join(keep, "\n") + "\n"; cron.content != want {
		t.Fatalf("crontab after rm:\n%q\nwant\n%q", cron.content, want)
	}
	list, err := reminders.NewStore().Load(ws)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := list.Get("rem-aaaaaaaaaaaa0001"); ok || list.Len() != 2 {
		t.Fatalf("entry still stored; len=%d", list.Len())
	}

	// Not installed → crontab untouched.
	writes := cron.writes
	out.Reset()
	if code := runReminders([]string{"delete", "rem-aaaaaaaaaaaa0002"}, ws, env); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if cron.writes != writes || strings.Contains(out.String(), "crontab") {
		t.Fatalf("uninstalled entry touched crontab (writes %d→%d): %q", writes, cron.writes, out)
	}

	// Already gone → exit 1, no prompt.
	errb.Reset()
	if code := runReminders([]string{"rm", "rem-aaaaaaaaaaaa0002"}, ws, env); code != 1 || !strings.Contains(errb.String(), "no entry") {
		t.Fatalf("second rm: exit %d %q", code, errb)
	}
}

func TestRemindersRm_CrontabUnavailableStillRemoves(t *testing.T) {
	ws := seedWorkspace(t)
	env, out, _ := testEnv(&memCrontab{missing: true})
	if code := runReminders([]string{"rm", "0001"}, ws, env); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out.String(), "crontab not available, skipped") {
		t.Fatalf("got %q", out)
	}
	list, _ := reminders.NewStore().Load(ws)
	if list.Len() != 2 {
		t.Fatalf("entry not removed: %d left", list.Len())
	}
}

const seedSession = `{"current_agent":"software-engineer","tasks":[
 {"id":"T-0001","title":"wire list","status":"doing","created_at":"2026-10-29T14:20:00Z","updated_at":"2026-10-29T14:20:00Z","due":"2026-10-29T23:00:00Z"},
 {"id":"T-0002","title":"write tests","status":"todo","created_at":"2026-10-29T14:21:00Z","updated_at":"2026-10-29T14:21:00Z"},
 {"id":"T-0003","title":"waiting on mirror","status":"blocked","created_at":"2026-10-28T18:00:00Z","updated_at":"2026-10-28T18:00:00Z","due":"2026-10-28T23:00:00Z"},
 {"id":"T-0004","title":"ship form","status":"done","created_at":"2026-10-29T13:00:00Z","updated_at":"2026-10-29T13:00:00Z"},
 {"id":"T-0005","title":"sms idea","status":"cancelled","created_at":"2026-10-01T13:00:00Z","updated_at":"2026-10-01T13:00:00Z"}
]}`

func seedTasks(t *testing.T) string {
	t.Helper()
	ws := t.TempDir()
	writeFile(t, filepath.Join(ws, ".ergo-cli-go", "session.json"), seedSession)
	return ws
}

func TestTasksList_AllStatusesWithGlyphs(t *testing.T) {
	ws := seedTasks(t)
	env, out, _ := testEnv(nil)
	if code := runTasks(nil, ws, env); code != 0 {
		t.Fatalf("exit %d", code)
	}
	s := out.String()
	due, _ := time.Parse(time.RFC3339, "2026-10-28T23:00:00Z")
	for _, want := range []string{"◐ doing", "○ todo", "✕ blocked", "● done", "⊘ cancelled", due.Local().Format(shortTime) + " !"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q:\n%s", want, s)
		}
	}
	if strings.Count(s, "!") != 1 {
		t.Errorf("only the overdue open task should be flagged:\n%s", s)
	}
}

func TestTasksList_StatusFilterAndJSON(t *testing.T) {
	ws := seedTasks(t)
	env, out, _ := testEnv(nil)
	if code := runTasks([]string{"ls", "--status", "doing,blocked", "--json"}, ws, env); code != 0 {
		t.Fatalf("exit %d", code)
	}
	var got []map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(got) != 2 || got[0]["id"] != "T-0001" || got[1]["id"] != "T-0003" {
		t.Fatalf("filtered = %v", got)
	}

	out.Reset()
	runTasks([]string{"ls", "--status", "done"}, ws, env)
	if s := out.String(); !strings.Contains(s, "ship form") || strings.Contains(s, "write tests") {
		t.Fatalf("done filter:\n%s", s)
	}

	out.Reset()
	runTasks([]string{"ls", "--status", "todo"}, t.TempDir(), env)
	if !strings.Contains(out.String(), "No session at") {
		t.Fatalf("no session: %q", out)
	}

	env2, _, errb := testEnv(nil)
	if code := runTasks([]string{"ls", "--status", "wip"}, ws, env2); code != 2 || !strings.Contains(errb.String(), `unknown status "wip"`) {
		t.Fatalf("bad status: exit %d %q", code, errb)
	}
}

func TestTasksList_SessionFlagForms(t *testing.T) {
	ws := seedTasks(t)
	other := t.TempDir()
	for _, arg := range []string{ws, filepath.Join(ws, ".ergo-cli-go"), filepath.Join(ws, ".ergo-cli-go", "session.json")} {
		env, out, errb := testEnv(nil)
		if code := runTasks([]string{"list", "--session", arg}, other, env); code != 0 {
			t.Fatalf("--session %s: exit %d %s", arg, code, errb)
		}
		if !strings.Contains(out.String(), "wire list") {
			t.Fatalf("--session %s:\n%s", arg, out)
		}
	}
	env, _, errb := testEnv(nil)
	if code := runTasks([]string{"ls", "--all"}, ws, env); code != 2 || !strings.Contains(errb.String(), "not indexed") {
		t.Fatalf("--all: exit %d %q", code, errb)
	}
}

func TestTasksShow(t *testing.T) {
	ws := seedTasks(t)
	env, out, _ := testEnv(nil)
	if code := runTasks([]string{"show", "0003"}, ws, env); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if s := out.String(); !strings.Contains(s, "✕ blocked") || !strings.Contains(s, "waiting on mirror") {
		t.Fatalf("show:\n%s", s)
	}
}

func TestColorOutputUsesThemeAndNoColorWhenOff(t *testing.T) {
	ws := seedTasks(t)
	env, out, _ := testEnv(nil)
	env.color = true
	runTasks(nil, ws, env)
	if !strings.Contains(out.String(), "\x1b[38;2;") {
		t.Fatalf("expected truecolor output")
	}
	// Alignment is computed on plain text: stripping escapes gives the
	// same table as the uncoloured run.
	plainEnv, plain, _ := testEnv(nil)
	runTasks(nil, ws, plainEnv)
	if stripANSI(out.String()) != plain.String() {
		t.Fatalf("coloured table differs from plain once escapes are stripped:\n%s\nvs\n%s", stripANSI(out.String()), plain)
	}

	t.Setenv("NO_COLOR", "1")
	if colorEnabled(os.Stdout) {
		t.Fatal("NO_COLOR must disable colour")
	}
}

func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && s[j] != 'm' {
				j++
			}
			i = j
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func TestTruncate(t *testing.T) {
	if got := truncate("hello world", 20); got != "hello world" {
		t.Fatal(got)
	}
	if got := truncate("hello\n  world  again", 9); got != "hello wo…" || width(got) != 9 {
		t.Fatalf("%q", got)
	}
	if got := truncate("日本語のテキスト", 7); width(got) > 7 || !strings.HasSuffix(got, "…") {
		t.Fatalf("%q w=%d", got, width(got))
	}
}

func mustCronLine(t *testing.T, e reminders.Entry, ws string) string {
	t.Helper()
	l, err := reminders.CronLine(e, ws, "/usr/local/bin/coder")
	if err != nil {
		t.Fatal(err)
	}
	return l
}
