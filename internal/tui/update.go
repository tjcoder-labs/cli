package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync/atomic"
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

// Seams for tests: how the running executable was installed, and the
// prepare/apply steps of an install. Production uses the real ones.
var (
	currentExecutable = os.Executable
	prepareUpdate     = func(ctx context.Context, c *update.Checker, m update.Method, current, latest, exe string) (update.Plan, error) {
		return c.Prepare(ctx, m, current, latest, exe)
	}
	applyUpdate = func(ctx context.Context, c *update.Checker, p update.Plan, out io.Writer) error {
		return c.Apply(ctx, p, out)
	}
)

// installing is set while an update is being installed (automatic or
// /update) so two installs never run at once.
var installing atomic.Bool

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
// timeout. Failure is silent. With a newer version available it either
// installs it in the background (automatic updates on, npm or release
// binary) or shows a one-line notice. It only runs from the interactive
// TUI, and startBackground skips it entirely in CI or with
// CODER_NO_UPDATE_CHECK=1 / update_check=false.
func (a *App) backgroundUpdateCheck(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(1500 * time.Millisecond): // let the first frame settle
	}
	a.checkAndMaybeAutoUpdate(ctx)
}

func (a *App) checkAndMaybeAutoUpdate(ctx context.Context) {
	c := UpdateChecker()
	cctx, cancel := context.WithTimeout(ctx, update.DefaultTimeout)
	res, err := c.CheckCached(cctx, a.appVersion)
	cancel()
	if TelemetryEvent != nil {
		TelemetryEvent("update_check", map[string]any{"ok": err == nil, "available": err == nil && res.Available()})
	}
	if err != nil || ctx.Err() != nil {
		return
	}
	exe, _ := currentExecutable()
	method := update.DetectMethod(exe, BuildMethod)
	auto, _ := update.AutoUpdateEnabled()
	switch update.Decide(res, method, auto) {
	case update.ActionNotify:
		a.notifyUpdateAvailable(res.Latest)
	case update.ActionSourceNotice:
		a.Notice(fmt.Sprintf("Update available: v%s → v%s · source build; update with: %s", a.appVersion, res.Latest, update.SourceInstructions))
	case update.ActionInstall:
		a.autoInstall(ctx, c, method, res.Latest, exe)
	}
}

func (a *App) notifyUpdateAvailable(latest string) {
	a.Notice(fmt.Sprintf("Update available: v%s → v%s · run /update", a.appVersion, latest))
}

// autoInstall installs latest without prompting (background goroutine
// only). It never overlaps another install, and reports exactly one
// line: success, or a short failure pointing at /update.
func (a *App) autoInstall(ctx context.Context, c *update.Checker, method update.Method, latest, exe string) {
	if !installing.CompareAndSwap(false, true) {
		return
	}
	defer installing.Store(false)
	ictx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	report := func(ok bool) {
		if TelemetryEvent != nil {
			TelemetryEvent("update_auto", map[string]any{"ok": ok, "method": string(method)})
		}
	}
	plan, err := prepareUpdate(ictx, c, method, a.appVersion, latest, exe)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		if errors.Is(err, update.ErrNoBinaryRelease) {
			// main is ahead of the newest release; nothing to install yet.
			a.notifyUpdateAvailable(latest)
			return
		}
		report(false)
		a.Notice(fmt.Sprintf("Automatic update to v%s failed: %s · run /update", latest, firstLine(err.Error())))
		return
	}
	var out strings.Builder
	if err := applyUpdate(ictx, c, plan, &out); err != nil {
		if ctx.Err() != nil {
			return // coder is exiting; npm/the download was cancelled
		}
		report(false)
		a.Notice(fmt.Sprintf("Automatic update to v%s failed: %s · run /update", plan.Version, firstLine(err.Error())))
		return
	}
	report(true)
	a.Notice(fmt.Sprintf("Updated to v%s · restart coder to use it", plan.Version))
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
	exe, _ := currentExecutable()
	method := update.DetectMethod(exe, BuildMethod)
	plan, err := prepareUpdate(ctx, c, method, a.appVersion, res.Latest, exe)
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
	// Only an explicit "Update now" pick installs; Esc, "Not now",
	// a cancelled turn or an empty answer never do.
	if err != nil || ans.Cancelled || ans.Index != 0 || ans.Choice != "Update now" {
		return
	}
	if !installing.CompareAndSwap(false, true) {
		a.Notice("An update is already being installed; wait for it to finish.")
		return
	}
	defer installing.Store(false)
	a.Notice(fmt.Sprintf("Updating to v%s…", plan.Version))
	var out strings.Builder
	if err := applyUpdate(ctx, c, plan, &out); err != nil {
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
