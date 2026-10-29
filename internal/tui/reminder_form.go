package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/rivo/tview"
	"github.com/tjcoder-labs/cli/internal/reminders"
	"github.com/tjcoder-labs/cli/internal/tools"
)

// reminderForm is the UI-free state behind /reminder. The tview form
// mirrors it field by field; preview and submit are pure so tests can
// drive the preset → cron mapping, validation, and saving directly.
type reminderForm struct {
	Message  string
	Spec     reminders.Spec
	AsPrompt bool // run Message as an agent prompt each tick (a schedule)
	Install  bool // also append a crontab line
}

func newReminderForm(now time.Time) *reminderForm {
	return &reminderForm{Spec: reminders.DefaultSpec(now)}
}

// reminderPreview is the live readout under the schedule dropdown.
type reminderPreview struct {
	Cron    string    // 5-field expression ("" when invalid)
	OneShot bool      // once-at entries are removed after firing
	Summary string    // plain English ("every 5 minutes")
	Next    time.Time // next fire time in now's zone
	Err     error
}

func (f *reminderForm) preview(now time.Time) reminderPreview {
	cron, oneShot, err := reminders.BuildCron(f.Spec, now)
	if err == nil && f.Spec.Preset == reminders.PresetCustom {
		err = tools.ValidateCronExpr(cron)
	}
	if err != nil {
		return reminderPreview{Err: err}
	}
	next, err := reminders.NextRun(cron, now)
	if err != nil {
		return reminderPreview{Cron: cron, Err: err}
	}
	return reminderPreview{Cron: cron, OneShot: oneShot, Summary: reminders.Describe(cron, oneShot), Next: next}
}

// input validates the form and returns the CreateInput to save.
func (f *reminderForm) input(now time.Time, agent string) (reminders.CreateInput, reminderPreview, error) {
	msg := strings.TrimSpace(f.Message)
	if msg == "" {
		return reminders.CreateInput{}, reminderPreview{}, fmt.Errorf("message must not be empty")
	}
	if strings.ContainsAny(msg, "\n\r") {
		return reminders.CreateInput{}, reminderPreview{}, fmt.Errorf("message must be a single line")
	}
	pv := f.preview(now)
	if pv.Err != nil {
		return reminders.CreateInput{}, pv, pv.Err
	}
	in := reminders.CreateInput{CronExpr: pv.Cron, InstallCron: f.Install, OneShot: pv.OneShot}
	if f.AsPrompt {
		in.Prompt = msg
		in.Agent = agent
	} else {
		in.Message = msg
	}
	return in, pv, nil
}

// submit validates and saves through the same path as set_reminder
// (reminders.json, optional crontab line). The in-session scheduler
// picks the new entry up on its next wake via the file's mtime.
func (f *reminderForm) submit(workspaceRoot, agent string, now time.Time) (reminders.Entry, reminderPreview, error) {
	in, pv, err := f.input(now, agent)
	if err != nil {
		return reminders.Entry{}, pv, err
	}
	entry, err := tools.CreateReminder(tools.ExecEnv{WorkspaceRoot: workspaceRoot}, in)
	return entry, pv, err
}

const reminderNextLayout = "Mon Jan 2 15:04 MST"

// reminderWeekdays lists the weekly-preset day options Monday-first.
var reminderWeekdays = []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday, time.Saturday, time.Sunday}

func (a *App) openReminderModal() {
	st := newReminderForm(time.Now())
	form := a.newModalForm(nil)
	form.SetItemPadding(1)

	var cronView, statusView *tview.TextView
	building := false

	readout := func() {
		if statusView == nil {
			return
		}
		pv := st.preview(time.Now())
		th := a.palette.Theme
		if cronView != nil {
			if pv.Cron != "" {
				cronView.SetText(fmt.Sprintf("[%s::b]%s[-:-:-]", a.palette.HexLavender, pv.Cron))
			} else {
				cronView.SetText(fmt.Sprintf("[%s]—[-]", a.palette.HexDim))
			}
		}
		if pv.Err != nil {
			statusView.SetText(th.Err(pv.Err.Error()))
			return
		}
		if pv.OneShot {
			statusView.SetText(fmt.Sprintf("[%s]once on %s · removed after it fires[-]", a.palette.HexDim,
				pv.Next.Format(reminderNextLayout)))
			return
		}
		statusView.SetText(fmt.Sprintf("[%s]%s · next %s[-]", a.palette.HexDim,
			tview.Escape(pv.Summary), pv.Next.Format(reminderNextLayout)))
	}

	var build func(focus int)
	build = func(focus int) {
		building = true
		defer func() { building = false }()
		form.Clear(true)
		cronView, statusView = nil, nil

		form.AddInputField("Message", st.Message, 60, nil, func(s string) { st.Message = s })
		form.AddDropDown("Schedule", reminders.PresetLabels, int(st.Spec.Preset), func(_ string, idx int) {
			if building || idx < 0 || reminders.Preset(idx) == st.Spec.Preset {
				return
			}
			st.Spec.Preset = reminders.Preset(idx)
			// Rebuild after the dropdown finishes handling this key so
			// the revealed date/time fields appear below it.
			a.enqueueUI(func() { build(1) })
		})
		p := st.Spec.Preset
		if p.NeedsDate() {
			form.AddInputField("Date (YYYY-MM-DD)", st.Spec.Date, 12, nil, func(s string) { st.Spec.Date = s; readout() })
		}
		if p.NeedsWeekday() {
			names := make([]string, len(reminderWeekdays))
			cur := 0
			for i, d := range reminderWeekdays {
				names[i] = d.String()
				if d == st.Spec.Weekday {
					cur = i
				}
			}
			form.AddDropDown("Day", names, cur, func(_ string, idx int) {
				if idx >= 0 && idx < len(reminderWeekdays) {
					st.Spec.Weekday = reminderWeekdays[idx]
				}
				if !building {
					readout()
				}
			})
		}
		if p.NeedsTime() {
			form.AddInputField("Time (HH:MM, 24h)", st.Spec.Time, 7, nil, func(s string) { st.Spec.Time = s; readout() })
		}
		if p == reminders.PresetCustom {
			form.AddInputField("Cron (5 fields)", st.Spec.Custom, 24, nil, func(s string) { st.Spec.Custom = s; readout() })
		} else {
			cronView = tview.NewTextView().SetDynamicColors(true).SetScrollable(false) // read-only: Tab skips it
			cronView.SetLabel("Cron")
			cronView.SetSize(1, 24)
			form.AddFormItem(cronView)
		}
		statusView = tview.NewTextView().SetDynamicColors(true).SetScrollable(false)
		statusView.SetLabel(" ")
		statusView.SetSize(2, 0)
		statusView.SetWrap(true).SetWordWrap(true)
		form.AddFormItem(statusView)
		form.AddCheckbox("Run as agent prompt", st.AsPrompt, func(c bool) { st.AsPrompt = c })
		form.AddCheckbox("Install to crontab", st.Install, func(c bool) { st.Install = c })
		form.AddButton("Save", func() {
			agent := a.currentAgent.Name
			if agent == "" {
				agent = "coder"
			}
			now := time.Now()
			entry, pv, err := st.submit(a.workspaceRoot, agent, now)
			th := a.palette.Theme
			if err != nil && entry.ID == "" {
				statusView.SetText(th.Err(err.Error()))
				a.appendActivity(th.Err("reminder not saved: " + err.Error()))
				return
			}
			kind := entry.Kind()
			line := fmt.Sprintf("%s [%s]%s · %s · next %s[-]", th.OK(kind+" "+shortScheduleID(entry.ID)+" saved"),
				a.palette.HexDim, tview.Escape(pv.Summary), pv.Cron, pv.Next.Format(reminderNextLayout))
			if entry.OneShot {
				line += fmt.Sprintf(" [%s]· one-shot[-]", a.palette.HexDim)
			}
			if entry.Installed {
				line += fmt.Sprintf(" [%s]· crontab installed[-]", a.palette.HexDim)
			}
			a.appendActivity(line)
			if err != nil {
				a.appendActivity(th.Warn("crontab install failed: " + err.Error()))
			}
			a.closeModal()
		})
		form.AddButton("Cancel", func() { a.closeModal() })
		a.styleModalForm(form)
		readout()
		if focus >= 0 {
			form.SetFocus(focus)
			a.tv.SetFocus(form)
		}
	}
	build(-1)
	a.showModal("Set Reminder", form)
}
