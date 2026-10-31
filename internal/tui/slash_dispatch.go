package tui

import (
	"fmt"
	"strings"

	"github.com/rivo/tview"
)

// slashMode says what a slash command does when it is submitted while a
// turn is in flight (a.busy).
type slashMode int

const (
	// slashImmediate runs at once, mid-turn, without touching the
	// in-flight turn. These only change UI state or settings that a turn
	// snapshots when it starts (model, agent, tools, environment), so
	// they apply from the next request.
	slashImmediate slashMode = iota
	// slashQueued is deferred until the turn settles, then run in
	// order. These swap state the running turn reads on every step (the
	// provider, the tool registry, the history it will merge into).
	slashQueued
	// slashBlocked is refused mid-turn with a notice; the user re-runs
	// it once the turn is done.
	slashBlocked
)

func (m slashMode) String() string {
	switch m {
	case slashQueued:
		return "queued"
	case slashBlocked:
		return "blocked"
	}
	return "immediate"
}

// slashModeFor classifies a parsed command line ("/cmd arg…" split into
// fields, without the slash). Unknown commands are immediate: they only
// print a hint.
func slashModeFor(parts []string) slashMode {
	if len(parts) == 0 {
		return slashImmediate
	}
	sub := ""
	if len(parts) > 1 {
		sub = strings.ToLower(parts[1])
	}
	switch strings.ToLower(parts[0]) {
	case "clear":
		// Wiping history mid-turn would race the reply being merged
		// back, and replaying it after the turn would silently discard
		// the answer the user just watched arrive.
		return slashBlocked
	case "compact", "login", "logout", "update":
		// compact runs its own model call over the history; login and
		// logout swap the live provider the runner reads per step;
		// update may replace the binary.
		return slashQueued
	case "providers":
		if sub == "list" {
			return slashImmediate
		}
		return slashQueued // may switch the live provider
	case "mcp":
		if sub == "list" || sub == "" {
			return slashImmediate
		}
		return slashQueued // add/remove mutate the tool registry
	}
	return slashImmediate
}

// dispatchSlash runs a user-typed slash command, honouring the mid-turn
// policy. Must be called on the UI goroutine without a.mu held.
func (a *App) dispatchSlash(cmd string) {
	parts := strings.Fields(strings.TrimPrefix(cmd, "/"))
	if !a.busy {
		a.handleSlashCommand(cmd)
		return
	}
	name := ""
	if len(parts) > 0 {
		name = strings.ToLower(parts[0])
	}
	switch slashModeFor(parts) {
	case slashBlocked:
		a.appendActivity(fmt.Sprintf("[%s::b]/%s[-::B] [%s]is unavailable while a turn is running; try again when it finishes[-]",
			a.palette.HexPurple, tview.Escape(name), a.palette.HexDim))
	case slashQueued:
		a.appendActivity(fmt.Sprintf("[%s::b]/%s[-::B] [%s]queued; runs when the current turn finishes[-]",
			a.palette.HexPurple, tview.Escape(name), a.palette.HexDim))
		a.whenIdle(func() { a.handleSlashCommand(cmd) })
	default:
		a.handleSlashCommand(cmd)
	}
}

// whenIdle runs fn now when no turn is in flight, otherwise after the
// turn settles (runIdleQueue). UI goroutine only.
func (a *App) whenIdle(fn func()) {
	if !a.busy {
		fn()
		return
	}
	a.idleQueue = append(a.idleQueue, fn)
}

// runIdleQueue drains work deferred by whenIdle, in order. A queued item
// that starts a new busy period (e.g. /compact) leaves the rest queued
// for the next idle point. UI goroutine only.
func (a *App) runIdleQueue() {
	for len(a.idleQueue) > 0 && !a.busy {
		fn := a.idleQueue[0]
		a.idleQueue = a.idleQueue[1:]
		fn()
	}
}

// isSlashCommandLine reports whether a submitted prompt is a command
// rather than a message: one line starting with "/". A pasted multi-line
// block is always a message.
func isSlashCommandLine(prompt string) bool {
	return strings.HasPrefix(prompt, "/") && !strings.Contains(prompt, "\n")
}
