package tools

import (
	"errors"
	"strings"
	"testing"

	"github.com/tjcoder-labs/cli/internal/reminders"
)

type memCrontab struct {
	content  string
	writeErr error
}

func (m *memCrontab) Read() (string, error) { return m.content, nil }
func (m *memCrontab) Write(c string) error {
	if m.writeErr != nil {
		return m.writeErr
	}
	m.content = c
	return nil
}

func withCrontab(t *testing.T, c reminders.CrontabRunner) {
	t.Helper()
	old := crontabRunner
	crontabRunner = c
	t.Cleanup(func() { crontabRunner = old })
}

func TestCreateReminder_SavesAndInstallsOneShot(t *testing.T) {
	mc := &memCrontab{content: "17 3 * * * backup\n"}
	withCrontab(t, mc)
	ws := t.TempDir()
	e, err := CreateReminder(ExecEnv{WorkspaceRoot: ws}, reminders.CreateInput{
		CronExpr: "30 14 3 11 *", Message: "dentist", InstallCron: true, OneShot: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !e.Installed || !e.OneShot {
		t.Fatalf("entry = %+v", e)
	}
	if !strings.Contains(mc.content, "backup") || !strings.Contains(mc.content, "30 14 3 11 * cd '"+ws+"'") ||
		!strings.Contains(mc.content, "schedule run "+e.ID) {
		t.Fatalf("crontab = %q", mc.content)
	}
}

func TestCreateReminder_InstallFailureKeepsEntryUninstalled(t *testing.T) {
	withCrontab(t, &memCrontab{writeErr: errors.New("boom")})
	ws := t.TempDir()
	e, err := CreateReminder(ExecEnv{WorkspaceRoot: ws}, reminders.CreateInput{CronExpr: "*/5 * * * *", Message: "m", InstallCron: true})
	if err == nil || e.ID == "" || e.Installed {
		t.Fatalf("want saved+uninstalled entry and an error, got %+v %v", e, err)
	}
	list, _ := reminders.NewStore().Load(ws)
	if got, ok := list.Get(e.ID); !ok || got.Installed {
		t.Fatalf("stored = %+v %v", got, ok)
	}
}

func TestCreateReminder_RejectsInjectionAndBadRanges(t *testing.T) {
	ws := t.TempDir()
	for _, c := range []string{"* * * * * ; rm -rf ~", "* * * * *\n* * * * * evil", "99 * * * *", "* * *"} {
		if _, err := CreateReminder(ExecEnv{WorkspaceRoot: ws}, reminders.CreateInput{CronExpr: c, Message: "m"}); err == nil {
			t.Errorf("cron %q accepted", c)
		}
	}
	if list, _ := reminders.NewStore().Load(ws); list.Len() != 0 {
		t.Fatal("rejected input must not be stored")
	}
}
