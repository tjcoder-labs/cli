package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/tjcoder-labs/cli/internal/reminders"
)

// /reminders lists each entry with its cron readout and next run;
// running it again closes the pane, and Esc in the input closes it too.
func TestRemindersPaneToggleAndEsc(t *testing.T) {
	a, sim := startSimApp(t)
	store := reminders.NewStore()
	if _, _, err := store.Create(a.workspaceRoot, reminders.CreateInput{CronExpr: "30 9 * * 1-5", Message: "stand-up notes"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Create(a.workspaceRoot, reminders.CreateInput{CronExpr: "0 * * * *", Message: "x", Prompt: "check the deploy", Agent: "coder"}); err != nil {
		t.Fatal(err)
	}

	typeLine(sim, "/reminders")
	waitFor(t, a, "reminders pane open", func() bool { return a.activePanel == "reminders" })
	onLoop(t, a, func() {
		if n := a.remindersList.GetItemCount(); n != 2 {
			t.Fatalf("reminders rows = %d, want 2", n)
		}
		var all []string
		for i := 0; i < 2; i++ {
			m, s := a.remindersList.GetItemText(i)
			all = append(all, m+" | "+s)
		}
		joined := strings.Join(all, "\n")
		for _, want := range []string{"stand-up notes", reminders.Describe("30 9 * * 1-5", false), "next ", "check the deploy", "agent run · coder", "every hour"} {
			if !strings.Contains(joined, want) {
				t.Errorf("pane missing %q in:\n%s", want, joined)
			}
		}
		if !strings.Contains(a.remindersTitle.GetText(true), "REMINDERS 2") {
			t.Errorf("title %q lacks count", a.remindersTitle.GetText(true))
		}
		if a.right.GetItem(1) != a.remindersPanel {
			t.Error("reminders panel not mounted in the right column")
		}
		if a.remindersStop == nil {
			t.Error("watcher not running while the pane is open")
		}
	})

	// Same command again closes it.
	typeLine(sim, "/reminders")
	waitFor(t, a, "reminders pane closed by toggle", func() bool { return a.activePanel == "activity" && a.remindersStop == nil })

	// Esc in the input closes any side pane.
	for _, cmd := range []string{"/tasks", "/reminders", "/test"} {
		typeLine(sim, cmd)
		want := strings.TrimPrefix(cmd, "/")
		waitFor(t, a, cmd+" open", func() bool { return a.activePanel == want })
		injectKey(sim, tcell.KeyEscape, 0)
		waitFor(t, a, cmd+" closed by Esc", func() bool { return a.activePanel == "activity" })
	}
	// Esc with activity showing is a no-op.
	injectKey(sim, tcell.KeyEscape, 0)
	waitFor(t, a, "still activity", func() bool { return a.activePanel == "activity" })
}

// The watcher picks up entries written behind the pane's back (the
// agent's set_reminder tool, the scheduler, the CLI).
func TestRemindersPaneLiveRefresh(t *testing.T) {
	a, sim := startSimApp(t)
	typeLine(sim, "/reminders")
	waitFor(t, a, "pane open", func() bool { return a.activePanel == "reminders" })
	onLoop(t, a, func() {
		if m, _ := a.remindersList.GetItemText(0); !strings.Contains(m, "No reminders yet") {
			t.Errorf("empty state = %q", m)
		}
	})
	if _, _, err := reminders.NewStore().Create(a.workspaceRoot, reminders.CreateInput{CronExpr: "0 8 * * *", Message: "water plants"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * remindersPollEvery)
	for {
		got := ""
		onLoop(t, a, func() { got, _ = a.remindersList.GetItemText(0) })
		if strings.Contains(got, "water plants") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("pane did not refresh; row 0 = %q", got)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestReminderRowsMissedOneShot(t *testing.T) {
	a := &App{palette: darkPalette()}
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.Local)
	rows := a.reminderRows([]reminders.Entry{{
		ID: "01ABCDEFGHJKMNPQRSTVWXYZ00", CronExpr: "0 9 29 10 *", Message: "once", OneShot: true,
		CreatedAt: now.Add(-48 * time.Hour).Format(time.RFC3339), Installed: true,
	}}, now)
	if len(rows) != 1 || !strings.Contains(rows[0].secondary, "missed") || !strings.Contains(rows[0].secondary, "crontab") {
		t.Fatalf("rows = %+v", rows)
	}
}

// /tasks and /canvas toggle too; /code is the canvas alias.
func TestPaneCommandsToggle(t *testing.T) {
	a, sim := startSimApp(t)
	for _, c := range [][2]string{{"/tasks", "/tasks"}, {"/canvas", "/code"}, {"/test", "/test"}, {"/articles", "/articles"}} {
		typeLine(sim, c[0])
		want := panelKey(strings.TrimPrefix(c[0], "/"))
		waitFor(t, a, c[0]+" open", func() bool { return panelKey(a.activePanel) == want })
		typeLine(sim, c[1])
		waitFor(t, a, c[1]+" closes "+c[0], func() bool { return a.activePanel == "activity" })
	}
	// Switching straight between panes does not close.
	typeLine(sim, "/tasks")
	waitFor(t, a, "tasks", func() bool { return a.activePanel == "tasks" })
	typeLine(sim, "/reminders")
	waitFor(t, a, "reminders replaces tasks", func() bool { return a.activePanel == "reminders" })
}

// Side panes take two thirds of the right column (cognition one
// third); activity keeps its split and canvas the full height.
func TestSidePaneGetsLargerShare(t *testing.T) {
	a, sim := startSimApp(t)
	sim.SetSize(160, 61)
	heights := func() (cog, body int) {
		a.tv.Draw()
		onLoop(t, a, func() {
			_, _, _, cog = a.reasoningPanel.GetRect()
			_, _, _, body = a.right.GetItem(1).GetRect()
		})
		return
	}
	typeLine(sim, "/tasks")
	waitFor(t, a, "tasks", func() bool { return a.activePanel == "tasks" })
	cog, body := heights()
	if cog <= 0 || body < 2*cog-2 || body > 2*cog+2 {
		t.Fatalf("tasks pane: cognition %d, body %d; want ≈1:2", cog, body)
	}
	typeLine(sim, "/reminders")
	waitFor(t, a, "reminders", func() bool { return a.activePanel == "reminders" })
	if c2, b2 := heights(); c2 != cog || b2 != body {
		t.Fatalf("reminders pane split %d:%d differs from tasks %d:%d", c2, b2, cog, body)
	}
}
