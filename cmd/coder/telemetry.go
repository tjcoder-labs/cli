package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/tjcoder-labs/cli/internal/telemetry"
	"github.com/tjcoder-labs/cli/internal/tui"
)

// Telemetry keys are injected at build time (see Makefile and
// scripts/build-release.sh). Empty means telemetry is a no-op; the
// SENTRY_DSN / POSTHOG_KEY / POSTHOG_HOST env vars override them.
var (
	sentryDSN   string
	posthogKey  string
	posthogHost string
)

// commandsHelp lists the positional subcommands for `coder -h`.
const commandsHelp = `
  coder [flags]                      start the interactive TUI
  coder ask [text] | -p "..."        one-shot headless prompt (or pipe stdin)
  coder reminders list|show|rm       list, inspect or remove reminders and schedules
  coder tasks list|show              list or inspect the session's tasks
  coder schedule run <id>            run a scheduled entry now (used by crontab)
  coder models [list|info <name>]    list provider models
  coder agent list|show|add|remove   manage agents
  coder update | telemetry | version

Global flags go before the command, e.g. coder --workspace-root ~/proj reminders ls.
`

func init() {
	flag.Usage = func() {
		out := flag.CommandLine.Output()
		fmt.Fprintf(out, "Usage of %s:\n", os.Args[0])
		fmt.Fprint(out, commandsHelp)
		fmt.Fprintln(out, "\nFlags:")
		flag.PrintDefaults()
		fmt.Fprintln(out, "\nTelemetry: coder sends anonymous usage stats, crash reports and an install heartbeat")
		fmt.Fprintln(out, "(never prompts, code, paths or keys). Disable with `coder telemetry off`,")
		fmt.Fprintln(out, "DO_NOT_TRACK=1 or CODER_TELEMETRY=0. Always off in CI; the heartbeat is TUI-only.")
	}
}

// startTelemetry starts the anonymous telemetry client (nil when off)
// and wires the TUI event hook.
func startTelemetry(headless bool) *telemetry.Client {
	c := telemetry.Start(telemetry.Config{
		SentryDSN:   sentryDSN,
		PostHogKey:  posthogKey,
		PostHogHost: posthogHost,
		Version:     version,
		Headless:    headless,
	})
	if c != nil {
		tui.TelemetryEvent = c.Track
	}
	return c
}

// cmdTelemetry implements `coder telemetry [on|off|status]`.
func cmdTelemetry(args []string) {
	sub := "status"
	if len(args) > 0 {
		sub = strings.ToLower(args[0])
	}
	switch sub {
	case "on", "off":
		if err := telemetry.SetEnabled(sub == "on"); err != nil {
			fatal("telemetry: %v", err)
		}
		fmt.Printf("Telemetry %s.\n", sub)
	case "status":
		fmt.Println(telemetry.StatusLine())
	default:
		fmt.Fprintln(os.Stderr, "usage: coder telemetry [on|off|status]")
		os.Exit(2)
	}
}
