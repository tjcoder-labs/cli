package tui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/tjcoder-labs/cli/internal/tools"
	"github.com/tjcoder-labs/cli/internal/update"
)

// BackgroundHook runs in its own goroutine once the TUI event loop is
// up; ctx is cancelled when the TUI exits. main registers telemetry and
// heartbeat hooks here so the tui package stays free of their config.
type BackgroundHook func(ctx context.Context, a *App)

var backgroundHooks []BackgroundHook

// OnBackground registers a hook for every App.Run.
func OnBackground(h BackgroundHook) { backgroundHooks = append(backgroundHooks, h) }

// UpdateChecker is the checker used by the TUI; tests may replace it.
var UpdateChecker = update.NewChecker

// BuildMethod is stamped by main from its ldflags ("binary" for
// release builds); see update.DetectMethod.
var BuildMethod string

func (a *App) startBackground(ctx context.Context) {
	for _, h := range backgroundHooks {
		go h(ctx, a)
	}
	go a.startTelemetry(ctx)
	if reason := update.Disabled(false); reason == "" {
		go a.backgroundUpdateCheck(ctx)
	}
}

// backgroundUpdateCheck runs at most once per 24h (cached) with a short
// timeout. Failure is silent; success with a newer version shows a
// one-line notice.
func (a *App) backgroundUpdateCheck(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(1500 * time.Millisecond): // let the first frame settle
	}
	cctx, cancel := context.WithTimeout(ctx, update.DefaultTimeout)
	defer cancel()
	res, err := UpdateChecker().CheckCached(cctx, a.appVersion)
	if TelemetryEvent != nil {
		TelemetryEvent("update_check", map[string]any{"ok": err == nil, "available": err == nil && res.Available()})
	}
	if err != nil || !res.Available() || ctx.Err() != nil {
		return
	}
	a.Notice(fmt.Sprintf("Update available: v%s → v%s · run /update", a.appVersion, res.Latest))
}

// TelemetryEvent is set by main to forward anonymous events; nil = off.
var TelemetryEvent func(name string, props map[string]any)

// Notice prints a one-line dim notice into the conversation. Safe from
// any goroutine, including the UI event loop (see postUI).
func (a *App) Notice(text string) { a.Record(text) }

// runUpdate implements /update (always on its own goroutine). It
// re-checks without the cache, explains the plan, and asks for
// confirmation through the in-transcript prompt before changing anything.
func (a *App) runUpdate(parts []string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	c := UpdateChecker()
	cctx, ccancel := context.WithTimeout(ctx, 10*time.Second)
	res, err := c.Check(cctx, a.appVersion)
	ccancel()
	if err != nil {
		a.Notice("Update check failed: " + err.Error())
		return
	}
	if !res.Available() {
		a.Notice(fmt.Sprintf("coder v%s is up to date", a.appVersion))
		return
	}
	exe, _ := os.Executable()
	method := update.DetectMethod(exe, BuildMethod)
	plan, err := c.Prepare(ctx, method, a.appVersion, res.Latest, exe)
	if err != nil {
		a.Notice("Update unavailable: " + err.Error())
		return
	}
	if method == update.MethodSource {
		a.Notice(fmt.Sprintf("v%s is available. This is a source build; update with: %s", plan.Version, plan.Command))
		return
	}
	detail := plan.Command
	if method == update.MethodBinary {
		detail = "replace " + plan.Target + " with " + plan.Asset
	}
	ans, err := a.Ask(ctx, tools.PromptRequest{
		Kind:     tools.PromptAuthorization,
		Title:    "Update",
		Question: fmt.Sprintf("Update coder v%s → v%s (%s install)?", a.appVersion, plan.Version, method),
		Detail:   detail,
		Options:  []string{"Update now", "Not now"},
	})
	if err != nil || ans.Index != 0 {
		return
	}
	a.Notice(fmt.Sprintf("Updating to v%s…", plan.Version))
	var out strings.Builder
	if err := c.Apply(ctx, plan, &out); err != nil {
		a.Notice("Update failed: " + firstLine(err.Error()) + lastLine(out.String()))
		return
	}
	a.Notice(fmt.Sprintf("Updated to v%s. Restart coder to use it.", plan.Version))
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	return s
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	return " — " + s
}
