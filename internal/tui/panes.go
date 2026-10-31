package tui

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/rivo/tview"
	"github.com/tjcoder-labs/cli/internal/reminders"
)

// Right-pane helpers shared by the toggleable panel commands
// (/tasks, /reminders, /canvas, /test, /articles).

const (
	// remindersPollEvery is how often the open reminders pane checks
	// reminders.json for changes made by the agent, the scheduler, or
	// `coder reminders rm`.
	remindersPollEvery = 2 * time.Second
	// remindersRedrawEvery re-renders the pane even without a file
	// change so the "next" readouts never go stale.
	remindersRedrawEvery = 30 * time.Second
)

// togglePanel shows name in the right column, or returns to the
// activity stream when name is already showing, so running a pane
// command a second time closes it.
func (a *App) togglePanel(name string) {
	if panelKey(a.activePanel) == panelKey(name) && panelKey(name) != "activity" {
		a.showPanel("activity")
		return
	}
	a.showPanel(name)
}

// panelKey folds panel aliases onto the name stored in activePanel.
func panelKey(name string) string {
	switch name = strings.ToLower(strings.TrimSpace(name)); name {
	case "code", "reference":
		return "canvas"
	case "":
		return "activity"
	}
	return name
}

// closeSidePanel returns the right column to the activity stream when
// another pane is showing. It reports whether anything changed.
func (a *App) closeSidePanel() bool {
	if panelKey(a.activePanel) == "activity" {
		return false
	}
	a.showPanel("activity")
	return true
}

// buildRemindersPanel creates the /reminders list. It is built once;
// refreshRemindersList repopulates it from reminders.json.
func (a *App) buildRemindersPanel() {
	a.remindersList = tview.NewList().
		ShowSecondaryText(true).
		SetHighlightFullLine(true)
	a.remindersList.SetBackgroundColor(a.palette.BgReasoning)
	a.remindersList.SetMainTextColor(a.palette.TextMain)
	a.remindersList.SetSecondaryTextColor(a.palette.TextDim)
	a.remindersList.SetSelectedBackgroundColor(a.palette.BgSelect)
	a.remindersList.SetSelectedTextColor(a.palette.Lavender)
	a.remindersList.SetBorderPadding(1, 1, 3, 2)
	a.remindersTitle = tview.NewTextView().SetDynamicColors(true)
	a.remindersTitle.SetBackgroundColor(a.palette.BgReasoning)
	a.remindersTitle.SetBorderPadding(0, 0, 2, 2)
	a.setRemindersTitle(0)
	a.remindersPanel = tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(a.remindersTitle, 1, 0, false).
		AddItem(a.remindersList, 0, 1, true)
	a.remindersPanel.SetBackgroundColor(a.palette.BgReasoning)
}

func (a *App) setRemindersTitle(n int) {
	if a.remindersTitle == nil {
		return
	}
	count := ""
	if n > 0 {
		count = fmt.Sprintf(" [%s]%d[-]", a.palette.HexLavender, n)
	}
	a.remindersTitle.SetText(fmt.Sprintf(" [%s]REMINDERS[-]%s [%s]/reminder adds · Esc closes[-]",
		a.palette.HexPurple, count, a.palette.HexFaint))
}

// reminderRow is the rendered form of one reminder in the pane.
type reminderRow struct {
	main, secondary string
}

// reminderNext mirrors `coder reminders list`: the next tick after now,
// or missed for a one-shot whose single moment already passed.
func reminderNext(e reminders.Entry, now time.Time) (next time.Time, missed, ok bool) {
	n, err := reminders.NextRun(e.CronExpr, now)
	if err != nil {
		return time.Time{}, false, false
	}
	if e.OneShot {
		if c, err := time.Parse(time.RFC3339, e.CreatedAt); err == nil {
			if first, err := reminders.NextRun(e.CronExpr, c.In(now.Location())); err == nil && !first.After(now) {
				return time.Time{}, true, true
			}
		}
	}
	return n, false, true
}

// reminderRows renders entries for the pane. Colours are shades of the
// primary scheme colour only.
func (a *App) reminderRows(entries []reminders.Entry, now time.Time) []reminderRow {
	rows := make([]reminderRow, 0, len(entries))
	for _, e := range entries {
		glyph, text := "◷", e.Message
		if e.Kind() == "schedule" {
			glyph, text = "▶", e.Prompt
		}
		text = strings.Join(strings.Fields(text), " ")
		if r := []rune(text); len(r) > 120 {
			text = string(r[:119]) + "…"
		}
		main := fmt.Sprintf("[%s]%s[-] %s", a.palette.HexPurple, glyph, tview.Escape(text))

		parts := []string{reminders.Describe(e.CronExpr, e.OneShot)}
		switch next, missed, ok := reminderNext(e, now); {
		case !ok:
			parts = append(parts, "cron "+e.CronExpr)
		case missed:
			parts = append(parts, "missed")
		default:
			parts = append(parts, "next "+next.Format(reminderNextLayout))
		}
		if e.Kind() == "schedule" {
			label := "agent run"
			if e.Agent != "" {
				label += " · " + e.Agent
			}
			parts = append(parts, label)
		}
		if e.Installed {
			parts = append(parts, "crontab")
		}
		parts = append(parts, shortScheduleID(e.ID))
		rows = append(rows, reminderRow{main: main, secondary: "  " + tview.Escape(strings.Join(parts, " · "))})
	}
	return rows
}

// refreshRemindersList reloads reminders.json into the pane.
func (a *App) refreshRemindersList() {
	if a.remindersList == nil {
		return
	}
	cur := a.remindersList.GetCurrentItem()
	a.remindersList.Clear()
	list, err := reminders.NewStore().Load(a.workspaceRoot)
	if err != nil {
		a.setRemindersTitle(0)
		a.remindersList.AddItem(a.palette.Theme.Err("could not read reminders: "+err.Error()), "", 0, nil)
		return
	}
	a.remindersKey = remindersFileKey(a.workspaceRoot)
	a.remindersDrawn = time.Now()
	a.setRemindersTitle(list.Len())
	if list.Len() == 0 {
		a.remindersList.AddItem(
			fmt.Sprintf("[%s]No reminders yet.[-] [%s]Use /reminder to add one.[-]",
				a.palette.HexDim, a.palette.HexFaint),
			"", 0, nil,
		)
		return
	}
	for _, r := range a.reminderRows(list.All(), time.Now()) {
		a.remindersList.AddItem(r.main, r.secondary, 0, nil)
	}
	if cur > 0 && cur < a.remindersList.GetItemCount() {
		a.remindersList.SetCurrentItem(cur)
	}
}

// remindersFileKey identifies one version of reminders.json.
func remindersFileKey(root string) string {
	info, err := os.Stat(reminders.FilePath(root))
	if err != nil {
		return "missing"
	}
	return fmt.Sprintf("%d/%d", info.ModTime().UnixNano(), info.Size())
}

// startRemindersWatch keeps the open reminders pane current. It stops
// when another pane is shown (stopRemindersWatch) or the app exits.
func (a *App) startRemindersWatch() {
	if a.remindersStop != nil {
		return
	}
	stop := make(chan struct{})
	a.remindersStop = stop
	root := a.workspaceRoot
	go func() {
		t := time.NewTicker(remindersPollEvery)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
			}
			key := remindersFileKey(root)
			a.postUI(func() {
				if a.remindersStop != stop || a.activePanel != "reminders" {
					return
				}
				if key != a.remindersKey || time.Since(a.remindersDrawn) >= remindersRedrawEvery {
					a.refreshRemindersList()
				}
			})
		}
	}()
}

func (a *App) stopRemindersWatch() {
	if a.remindersStop != nil {
		close(a.remindersStop)
		a.remindersStop = nil
	}
}

// refreshRemindersIfOpen re-renders the pane after a local change.
func (a *App) refreshRemindersIfOpen() {
	if a.activePanel == "reminders" {
		a.refreshRemindersList()
	}
}
