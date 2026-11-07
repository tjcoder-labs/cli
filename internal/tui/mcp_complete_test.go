package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/tjcoder-labs/cli/internal/mcpserver"
)

func TestMCPArgumentPaletteMatchesAdd(t *testing.T) {
	a, _ := startSimApp(t)
	onLoop(t, a, func() {
		a.config.MCPServers = map[string]MCPServerConfig{"github": {}, "gitlab": {}, "docs": {}}
		a.input.SetText("/mcp ")
		a.updateInputHint("/mcp ")
		if a.hintMode != "wsargs" || a.wsArgCmd != "mcp" {
			t.Fatalf("mode %q", a.hintMode)
		}
		if got := strings.Join(wsLabels(a), "|"); got != "list|status|add <name> <url|command>|remove <name>|tunnel|tunnel stop" {
			t.Fatal(got)
		}
		if txt := stripTags(a.renderWorkspaceArgPalette()); !strings.Contains(txt, "list") || !strings.Contains(txt, "mcp:") {
			t.Fatal(txt)
		}
		a.updateInputHint("/mcp tu")
		if got := strings.Join(wsLabels(a), "|"); got != "tunnel|tunnel stop" {
			t.Fatal(got)
		}
		// Shift+Tab cycles, Tab inserts the highlighted option — same keys as /add.
		a.input.SetText("/mcp tu")
		a.input.GetInputCapture()(tcell.NewEventKey(tcell.KeyBacktab, 0, tcell.ModShift))
		a.input.GetInputCapture()(tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone))
		if a.input.GetText() != "/mcp tunnel stop" {
			t.Fatalf("tab: %q", a.input.GetText())
		}
		a.updateInputHint("/mcp rm")
		if got := strings.Join(wsLabels(a), "|"); got != "remove <name>" {
			t.Fatal(got)
		}
		a.updateInputHint("/mcp rm gi")
		if got := strings.Join(wsLabels(a), "|"); got != "github|gitlab" {
			t.Fatal(got)
		}
		a.completeWorkspaceArg()
		if a.input.GetText() != "/mcp rm github" {
			t.Fatal(a.input.GetText())
		}
		a.updateInputHint("/mcp remove zz")
		if !strings.Contains(stripTags(a.renderWorkspaceArgPalette()), "no configured MCP server") {
			t.Fatal("no-match tip")
		}
		a.updateInputHint("/mcp add foo https://x")
		if a.hintMode != "mcp" {
			t.Fatalf("free-form add should keep usage tip, got %q", a.hintMode)
		}
	})
}

func TestMCPTunnelBannerConnectionsToolAndReconnect(t *testing.T) {
	a, _ := startSimApp(t)
	onLoop(t, a, func() {
		attachTestTunnelBar(a)
		tun := a.mcpTunnel
		hint := func() string { return a.mcpURLHint.GetText(false) }
		if !strings.Contains(hint(), "0 connections") {
			t.Fatal(hint())
		}
		for _, id := range []string{"c1", "c2", "c1"} {
			a.handleMCPRelayEvent(tun, mcpserver.RelayEvent{Kind: mcpserver.RelayConsumer, ConsumerID: id})
		}
		if !strings.Contains(hint(), "2 connections") {
			t.Fatal(hint())
		}
		tun.inFlight = []string{"run_command"}
		a.refreshMCPTunnelBar()
		if !strings.Contains(hint(), "→ run_command") {
			t.Fatal(hint())
		}
		a.handleMCPRelayEvent(tun, mcpserver.RelayEvent{Kind: mcpserver.RelayReconnecting, Attempt: 1})
		if !strings.Contains(hint(), "reconnecting…") || strings.Contains(hint(), testBarToken) {
			t.Fatal(hint())
		}
		a.handleMCPRelayEvent(tun, mcpserver.RelayEvent{Kind: mcpserver.RelayReconnected})
		if strings.Contains(hint(), "reconnecting") {
			t.Fatal(hint())
		}
		tun.consumers["c1"] = time.Now().Add(-6 * time.Minute)
		a.refreshMCPTunnelBar()
		if !strings.Contains(hint(), "1 connection ") {
			t.Fatal(hint())
		}
		if strings.Contains(a.activityLogText(), testBarToken) {
			t.Fatal("secret in activity")
		}
	})
}
