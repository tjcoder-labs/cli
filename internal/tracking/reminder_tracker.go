package tracking

import (
	"fmt"
	"strings"

	"github.com/tjcoder-labs/cli/internal/reminders"
	"github.com/tjcoder-labs/cli/internal/session"
)

// ReminderTracker adapts reminders.Store to the generic Tracker
// interface. It carries an explicit workspaceRoot because reminders
// live in the workspace filesystem rather than in the in-memory
// session state.
type ReminderTracker struct {
	store         *reminders.Store
	workspaceRoot string
}

func NewReminderTracker(workspaceRoot string) *ReminderTracker {
	return &ReminderTracker{
		store:         reminders.NewStore(),
		workspaceRoot: workspaceRoot,
	}
}

func (t *ReminderTracker) Type() string { return "reminder" }

func (t *ReminderTracker) List(state session.State) []interface{} {
	// Reminders are file-backed, not session-state-backed. We
	// deliberately ignore `state` and read from disk so the
	// /reminders pane always shows the persisted list.
	list, err := t.store.Load(t.workspaceRoot)
	if err != nil {
		return nil
	}
	all := list.All()
	out := make([]interface{}, len(all))
	for i := range all {
		out[i] = all[i]
	}
	return out
}

func (t *ReminderTracker) Get(state session.State, id string) interface{} {
	list, err := t.store.Load(t.workspaceRoot)
	if err != nil {
		return nil
	}
	e, ok := list.Get(id)
	if !ok {
		return nil
	}
	return e
}

func (t *ReminderTracker) Create(state session.State, input map[string]interface{}) (session.State, interface{}, error) {
	cron, _ := input["cron_expr"].(string)
	msg, _ := input["message"].(string)
	install, _ := input["install_cron"].(bool)
	prompt, _ := input["prompt"].(string)
	agentName, _ := input["agent"].(string)
	platform, _ := input["platform"].(string)
	dailyCap, _ := input["daily_cap"].(float64)
	_, entry, err := t.store.Create(t.workspaceRoot, reminders.CreateInput{
		CronExpr:    cron,
		Message:     msg,
		InstallCron: install,
		Prompt:      prompt,
		Agent:       agentName,
		Platform:    platform,
		DailyCap:    int(dailyCap),
	})
	if err != nil {
		return state, nil, err
	}
	return state, entry, nil
}

func (t *ReminderTracker) Update(state session.State, id string, input map[string]interface{}) (session.State, interface{}, error) {
	// Reminders don't have a partial update shape today; treat
	// update as a delete + recreate to keep the contract simple.
	// Schedule fields (Prompt/Agent/Platform/DailyCap) are preserved
	// from the existing entry unless explicitly overridden, so a
	// schedule does not silently degrade into a passive reminder.
	list, err := t.store.Load(t.workspaceRoot)
	if err != nil {
		return state, nil, err
	}
	existing, ok := list.Get(id)
	if !ok {
		return state, nil, fmt.Errorf("reminder not found")
	}
	cron, _ := input["cron_expr"].(string)
	if cron == "" {
		cron = existing.CronExpr
	}
	msg, _ := input["message"].(string)
	if msg == "" {
		msg = existing.Message
	}
	prompt, _ := input["prompt"].(string)
	if prompt == "" {
		prompt = existing.Prompt
	}
	agentName, _ := input["agent"].(string)
	if agentName == "" {
		agentName = existing.Agent
	}
	platform, _ := input["platform"].(string)
	if platform == "" {
		platform = existing.Platform
	}
	dailyCap, _ := input["daily_cap"].(float64)
	if dailyCap == 0 {
		dailyCap = float64(existing.DailyCap)
	}
	if _, _, err := t.store.Delete(t.workspaceRoot, id); err != nil {
		return state, nil, err
	}
	_, updated, err := t.store.Create(t.workspaceRoot, reminders.CreateInput{
		CronExpr:    cron,
		Message:     msg,
		InstallCron: existing.Installed,
		Prompt:      prompt,
		Agent:       agentName,
		Platform:    platform,
		DailyCap:    int(dailyCap),
	})
	if err != nil {
		return state, nil, err
	}
	return state, updated, nil
}

func (t *ReminderTracker) Delete(state session.State, id string) (session.State, bool, error) {
	_, removed, err := t.store.Delete(t.workspaceRoot, strings.TrimSpace(id))
	return state, removed, err
}

func (t *ReminderTracker) Format(obj interface{}) string {
	if e, ok := obj.(reminders.Entry); ok {
		installed := ""
		if e.Installed {
			installed = " (installed)"
		}
		if e.Prompt != "" {
			return fmt.Sprintf("[%s] schedule %s%s (%s)", e.CronExpr, truncSchedulePrompt(e.Prompt), installed, e.ID)
		}
		return fmt.Sprintf("[%s] %s%s (%s)", e.CronExpr, e.Message, installed, e.ID)
	}
	return fmt.Sprintf("%v", obj)
}

func truncSchedulePrompt(s string) string {
	if len(s) <= 48 {
		return s
	}
	return s[:47] + "…"
}
