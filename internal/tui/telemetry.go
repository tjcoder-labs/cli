package tui

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/tjcoder-labs/cli/internal/appdir"
	"github.com/tjcoder-labs/cli/internal/auth"
	"github.com/tjcoder-labs/cli/internal/client"
	"github.com/tjcoder-labs/cli/internal/telemetry"
	"github.com/tjcoder-labs/cli/internal/tooling"
	"github.com/tjcoder-labs/cli/internal/update"
)

// TelemetryNotice is the one-line first-run notice.
const TelemetryNotice = "coder sends anonymous usage stats and crash reports (no prompts, code or paths). Turn off with /telemetry off or DO_NOT_TRACK=1."

// emit forwards an anonymous event when main wired telemetry in.
func emit(name string, props map[string]any) {
	if TelemetryEvent != nil {
		TelemetryEvent(name, props)
	}
}

// SetObserver attaches agent-loop instrumentation to the TUI runner.
func (a *App) SetObserver(o tooling.Observer) {
	if a.runner != nil {
		a.runner.Observer = o
	}
}

// ProviderType names the provider's implementation, never its
// user-chosen registry name or URL.
func ProviderType(p client.Provider) string {
	if p == nil {
		return "none"
	}
	return strings.TrimLeft(strings.TrimPrefix(fmt.Sprintf("%T", p), "*client."), "*")
}

// startTelemetry shows the first-run notice and runs the ergo
// heartbeat. TUI only; both honor the shared opt-out gates.
func (a *App) startTelemetry(ctx context.Context) {
	// No pings without a terminal (piped stdout) or when opted out.
	if fi, err := os.Stdout.Stat(); err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return
	}
	if telemetry.DisabledReason() != "" {
		return
	}
	if !appdir.Load().TelemetryNoticeShown {
		if _, err := appdir.Update(func(s *appdir.Settings) { s.TelemetryNoticeShown = true }); err == nil {
			a.Record(TelemetryNotice)
		}
	}
	id, first := telemetry.ClaimFirstRun()
	if first {
		emit("app_installed", map[string]any{"method": installMethod()})
	}
	hb := &telemetry.Heartbeat{
		BaseURL:   ergoBaseURL(),
		Version:   a.appVersion,
		InstallID: id,
		Token:     auth.ResolveErgoToken,
	}
	hb.Run(ctx, first)
}

// handleTelemetryCommand implements /telemetry [on|off|status].
func (a *App) handleTelemetryCommand(parts []string) {
	sub := "status"
	if len(parts) > 1 {
		sub = strings.ToLower(parts[1])
	}
	switch sub {
	case "on", "off":
		if err := telemetry.SetEnabled(sub == "on"); err != nil {
			a.Record("Couldn't save telemetry setting: " + err.Error())
			return
		}
		if sub == "on" {
			a.Record("Telemetry on (takes effect next launch).")
		} else {
			a.Record("Telemetry off. No more usage stats, crash reports or heartbeats will be sent.")
		}
	default:
		a.Record(telemetry.StatusLine())
	}
}

func installMethod() string {
	exe, _ := os.Executable()
	return string(update.DetectMethod(exe, BuildMethod))
}
