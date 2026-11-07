package tui

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"github.com/tjcoder-labs/cli/internal/mcpserver"
	"github.com/tjcoder-labs/cli/internal/session"
)

const testBarToken = "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG"
const testBarURL = mcpCapabilityPrefix + testBarToken

func attachTestTunnelBar(a *App) {
	a.mcpTunnel = &interactiveMCPTunnel{ready: true, cancel: func() {}, done: make(chan struct{})}
	a.setMCPTunnelLink(mcpserver.Publication{URL: testBarURL, ExpiresAt: "2026-11-07T10:00:00Z"})
}

func TestMCPTunnelBarMaskedByDefaultAndNoModal(t *testing.T) {
	a, sim := startSimApp(t)
	onLoop(t, a, func() {
		a.input.SetText("unfinished draft")
		attachTestTunnelBar(a)
		if a.mcpTunnel.revealed || strings.Contains(a.mcpURLView.GetText(false), testBarToken) {
			t.Fatal("token revealed by default")
		}
		if !strings.HasPrefix(a.mcpURLView.GetText(false), mcpCapabilityPrefix) || !strings.Contains(a.mcpURLView.GetText(false), "•••") {
			t.Fatal(a.mcpURLView.GetText(false))
		}
		for _, key := range []string{"F7 show token", "F8 copy link"} {
			if !strings.Contains(a.mcpURLHint.GetText(false), key) {
				t.Error(a.mcpURLHint.GetText(false))
			}
		}
		if a.pages.GetPageCount() != 1 || a.tv.GetFocus() != a.input {
			t.Fatal("bar added an overlay or stole input focus")
		}
		a.handleMCPTunnelBarKey(tcell.NewEventKey(tcell.KeyF8, 0, tcell.ModNone))
		if len(sim.GetClipboardData()) != 0 {
			t.Fatal("hidden token copied without revealing")
		}
		if a.input.GetText() != "unfinished draft" {
			t.Fatal("draft overwritten")
		}
	})
}

func TestMCPTunnelBarRevealCopyHideDoesNotPersistSecret(t *testing.T) {
	a, sim := startSimApp(t)
	onLoop(t, a, func() {
		attachTestTunnelBar(a)
		a.input.SetText("draft")
		if a.globalKeys(tcell.NewEventKey(tcell.KeyF7, 0, tcell.ModNone)) != nil {
			t.Fatal("F7 not consumed")
		}
		if got := a.mcpURLView.GetText(false); got != testBarURL {
			t.Fatalf("revealed text=%q", got)
		}
		if !strings.Contains(a.mcpURLHint.GetText(false), "F7 hide token") {
			t.Fatal(a.mcpURLHint.GetText(false))
		}
		if a.globalKeys(tcell.NewEventKey(tcell.KeyF8, 0, tcell.ModNone)) != nil {
			t.Fatal("F8 not consumed")
		}
		if got := string(sim.GetClipboardData()); got != testBarURL {
			t.Fatalf("clipboard=%q", got)
		}
		if !strings.Contains(a.mcpURLHint.GetText(false), "copy requested") {
			t.Fatal("copy support not explained")
		}
		if a.input.GetText() != "draft" {
			t.Fatal("URL reached chat input/history")
		}
		event := tcell.NewEventKey(tcell.KeyRune, 's', tcell.ModNone)
		if a.globalKeys(event) != event {
			t.Fatal("ordinary typing captured")
		}
		if err := a.saveSession(); err != nil {
			t.Fatal(err)
		}
		state, _, err := session.Load(a.workspaceRoot)
		if err != nil {
			t.Fatal(err)
		}
		saved, _ := json.Marshal(state)
		for _, text := range []string{string(saved), a.activityLogText(), a.transcript.GetText(true)} {
			if strings.Contains(text, testBarToken) {
				t.Fatal("URL persisted in history/activity/transcript")
			}
		}
		a.globalKeys(tcell.NewEventKey(tcell.KeyF7, 0, tcell.ModNone))
		if strings.Contains(a.mcpURLView.GetText(false), testBarToken) {
			t.Fatal("token not hidden")
		}
		sim.SetClipboard([]byte("previous clipboard"))
		a.globalKeys(tcell.NewEventKey(tcell.KeyF8, 0, tcell.ModNone))
		if string(sim.GetClipboardData()) != "previous clipboard" {
			t.Fatal("hidden link overwrote clipboard")
		}
	})
}

func TestMCPTunnelBarReservesFullWidthRowsAboveInput(t *testing.T) {
	a, sim := startSimApp(t)
	onLoop(t, a, func() {
		a.transcript.SetText("CHAT-UNDERLAY")
		a.activity.SetText("PANEL-UNDERLAY")
		attachTestTunnelBar(a)
		a.handleMCPTunnelBarKey(tcell.NewEventKey(tcell.KeyF7, 0, tcell.ModNone))
		for _, width := range []int{140, 60, 100} {
			sim.SetSize(width, 40)
			a.beforeDraw(sim)
			a.rootFlex.SetRect(0, 0, width, 40)
			a.rootFlex.Draw(sim)
			x, y, w, h := a.mcpURLView.GetRect()
			_, inputY, _, _ := a.inputRow.GetRect()
			if y+h > inputY || h < 1 || w != width-2*(marginX-1) {
				t.Fatalf("overlap/non-fullwidth: url=(%d,%d,%d,%d) inputY=%d", x, y, w, h, inputY)
			}
			var visible strings.Builder
			for row := y; row < y+h; row++ {
				var line strings.Builder
				for col := 0; col < width; col++ {
					r, _, _, _ := sim.GetContent(col, row)
					line.WriteRune(r)
				}
				text := line.String()
				if strings.Contains(text, "CHAT-UNDERLAY") || strings.Contains(text, "PANEL-UNDERLAY") || strings.Contains(text, "MCP TUNNEL") {
					t.Fatal("URL row contaminated with underlying widgets")
				}
				visible.WriteString(strings.TrimSpace(text))
			}
			if visible.String() != testBarURL {
				t.Fatalf("width %d: URL missing/wrapped incorrectly: %q", width, visible.String())
			}
			a.handleMCPTunnelBarKey(tcell.NewEventKey(tcell.KeyF8, 0, tcell.ModNone))
			if string(sim.GetClipboardData()) != testBarURL {
				t.Fatal("narrow terminal copied incomplete URL")
			}
		}
		a.toggleFullscreen()
		if !a.hasMCPTunnelBar() || a.inputDockHeight() <= inputRowHeight {
			t.Fatal("fullscreen hid pinned bar")
		}
	})
}

func TestMCPTunnelBarStopScrubsAndRemovesItImmediately(t *testing.T) {
	a, _ := startSimApp(t)
	onLoop(t, a, func() {
		attachTestTunnelBar(a)
		a.handleMCPTunnelBarKey(tcell.NewEventKey(tcell.KeyF7, 0, tcell.ModNone))
		oldView := a.mcpURLView
		a.handleMCPTunnel([]string{"stop"})
		if a.hasMCPTunnelBar() || a.mcpURLView != nil || a.mcpTunnel.url != "" || a.mcpTunnel.revealed {
			t.Fatal("stop retained URL/bar")
		}
		if oldView.GetText(false) != "" || a.inputDockHeight() != inputRowHeight {
			t.Fatal("detached view retained secret or input height")
		}
		if a.handleMCPTunnelBarKey(tcell.NewEventKey(tcell.KeyF7, 0, tcell.ModNone)) {
			t.Fatal("stopped tunnel consumed F7")
		}
	})
}

func TestMCPTunnelBarInvalidPublicationAndNewLinkReset(t *testing.T) {
	a, _ := startSimApp(t)
	onLoop(t, a, func() {
		attachTestTunnelBar(a)
		a.handleMCPTunnelBarKey(tcell.NewEventKey(tcell.KeyF7, 0, tcell.ModNone))
		a.setMCPTunnelLink(mcpserver.Publication{URL: testBarURL})
		if a.mcpTunnel.revealed {
			t.Fatal("new publication inherited reveal state")
		}
		a.clearMCPTunnelBar()
		a.setMCPTunnelLink(mcpserver.Publication{URL: "https://evil.test/credential"})
		if a.hasMCPTunnelBar() {
			t.Fatal("invalid URL displayed")
		}
	})
}

func TestMCPTunnelBarExitScrubsURLBuffers(t *testing.T) {
	a, _ := startSimApp(t)
	onLoop(t, a, func() {
		attachTestTunnelBar(a)
		a.handleMCPTunnelBarKey(tcell.NewEventKey(tcell.KeyF7, 0, tcell.ModNone))
		close(a.mcpTunnel.done)
		a.shutdownMCPTunnel()
		if a.mcpTunnel.url != "" || a.mcpTunnel.revealed || a.mcpURLView.GetText(false) != "" || a.mcpURLHint.GetText(false) != "" {
			t.Fatal("TUI exit retained URL buffers")
		}
	})
}

func TestMCPTunnelBarDoesNotStealFormFocus(t *testing.T) {
	a, _ := startSimApp(t)
	onLoop(t, a, func() {
		field := tview.NewInputField().SetText("unfinished form value")
		a.showModal("Test form", field)
		attachTestTunnelBar(a)
		if a.tv.GetFocus() != field || field.GetText() != "unfinished form value" {
			t.Fatal("pinned bar stole form focus")
		}
		a.clearMCPTunnelBar()
		if a.tv.GetFocus() != field {
			t.Fatal("bar removal stole form focus")
		}
	})
}
