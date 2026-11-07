package tui

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"github.com/rivo/uniseg"
	"github.com/tjcoder-labs/cli/internal/mcpserver"
)

const mcpCapabilityPrefix = "https://api.tjcoder.com/mcp/hosts/"

var mcpBarCapability = regexp.MustCompile(`^https://api\.tjcoder\.com/mcp/hosts/[A-Za-z0-9_-]{43}$`)

// The live URL exists only in tunnel-owned memory and the dedicated URL view.
// Never put it in activity, transcript, input, config, or a modal overlay.
func (a *App) setMCPTunnelLink(p mcpserver.Publication) {
	if a.mcpTunnel == nil || a.mcpTunnel.stopping || !mcpBarCapability.MatchString(p.URL) {
		return
	}
	a.mcpTunnel.url = p.URL
	a.mcpTunnel.expires = p.ExpiresAt
	a.mcpTunnel.revealed = false
	a.mcpTunnel.notice = ""
	a.refreshMCPTunnelBar()
	a.rebuildMCPTunnelBarLayout()
}

func (a *App) hasMCPTunnelBar() bool {
	return a.mcpTunnel != nil && !a.mcpTunnel.stopping && a.mcpTunnel.url != ""
}

func (a *App) mcpDisplayedURL() string {
	if !a.hasMCPTunnelBar() {
		return ""
	}
	if a.mcpTunnel.revealed {
		return a.mcpTunnel.url
	}
	return mcpCapabilityPrefix + strings.Repeat("•", 43)
}

func (a *App) refreshMCPTunnelBar() {
	if !a.hasMCPTunnelBar() {
		return
	}
	if a.mcpURLView == nil {
		a.mcpURLView = tview.NewTextView().SetWrap(true).SetWordWrap(false).SetDynamicColors(false)
		a.mcpURLHint = tview.NewTextView().SetWrap(false).SetDynamicColors(false)
	}
	a.mcpURLView.SetTextColor(a.palette.Lavender).SetBackgroundColor(a.palette.BgInput)
	a.mcpURLHint.SetTextColor(a.palette.TextDim).SetBackgroundColor(a.palette.BgInput)
	action := "show token"
	if a.mcpTunnel.revealed {
		action = "hide token"
	}
	detail := a.mcpTunnel.notice
	if detail == "" {
		if expiry, err := time.Parse(time.RFC3339, a.mcpTunnel.expires); err == nil {
			detail = "expires " + expiry.Local().Format("Jan 2 3:04 PM MST")
		}
	}
	a.mcpURLHint.SetText(fmt.Sprintf("MCP TUNNEL · F7 %s · F8 copy link · %s", action, detail))
	a.mcpURLView.SetText(a.mcpDisplayedURL()).ScrollToBeginning()
}

// The dock spans the full terminal width, with the same horizontal insets as
// the input. URL-only rows have no side-by-side chat/panel text to contaminate
// terminal selection. F8 copies the entire URL even when it wraps or is clipped.
func (a *App) newInputDock() *tview.Flex {
	dock := tview.NewFlex().SetDirection(tview.FlexRow)
	dock.SetBackgroundColor(a.palette.BgRoot)
	if a.hasMCPTunnelBar() {
		a.refreshMCPTunnelBar()
		content := tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(a.mcpURLHint, 1, 0, false).
			AddItem(a.mcpURLView, 0, 1, false)
		content.SetBackgroundColor(a.palette.BgInput)
		a.mcpURLBar = tview.NewFlex().
			AddItem(spacerBox(a.palette.BgRoot), marginX-1, 0, false).
			AddItem(content, 0, 1, false).
			AddItem(spacerBox(a.palette.BgRoot), marginX-1, 0, false)
		a.mcpURLBar.SetBackgroundColor(a.palette.BgRoot)
		dock.AddItem(a.mcpURLBar, a.mcpTunnelBarHeight(), 0, false)
	}
	dock.AddItem(a.inputRow, inputRowHeight, 0, true)
	a.inputDock = dock
	return dock
}

func (a *App) mcpTunnelBarHeight() int {
	if !a.hasMCPTunnelBar() {
		return 0
	}
	width := 80
	if a.terminalScreen != nil {
		width, _ = a.terminalScreen.Size()
	}
	width -= 2 * (marginX - 1)
	if width < 1 {
		width = 1
	}
	rows := (uniseg.StringWidth(a.mcpDisplayedURL()) + width - 1) / width
	// Leave room for the conversation on tiny terminals. Dedicated copying is
	// independent of the visible width and always uses the complete URL.
	if rows > 4 {
		rows = 4
	}
	return 1 + rows
}

func (a *App) inputDockHeight() int { return inputRowHeight + a.mcpTunnelBarHeight() }

// This callback receives only the live application's screen, not a throwaway
// screenshot renderer. It also resizes the reserved region before layout draws.
func (a *App) beforeDraw(screen tcell.Screen) bool {
	a.terminalScreen = screen
	if a.inputDock != nil && a.rootFlex != nil {
		if a.mcpURLBar != nil && a.hasMCPTunnelBar() {
			a.inputDock.ResizeItem(a.mcpURLBar, a.mcpTunnelBarHeight(), 0)
		}
		if root, ok := a.rootFlex.(*tview.Flex); ok {
			root.ResizeItem(a.inputDock, a.inputDockHeight(), 0)
		}
	}
	return false
}

func (a *App) handleMCPTunnelBarKey(event *tcell.EventKey) bool {
	if !a.hasMCPTunnelBar() || (event.Key() != tcell.KeyF7 && event.Key() != tcell.KeyF8) {
		return false
	}
	if event.Key() == tcell.KeyF7 {
		a.mcpTunnel.revealed = !a.mcpTunnel.revealed
		a.mcpTunnel.notice = ""
	} else if !a.mcpTunnel.revealed {
		a.mcpTunnel.notice = "F7 reveals the token before copying"
	} else if a.terminalScreen == nil {
		a.mcpTunnel.notice = "select the revealed URL to copy"
	} else {
		// tcell uses the terminal's clipboard facility (OSC 52 where supported).
		// No shell, temporary file, clipboard read, or automatic copying on reveal.
		a.terminalScreen.SetClipboard([]byte(a.mcpTunnel.url))
		a.mcpTunnel.notice = "copy requested (terminal support required)"
	}
	a.refreshMCPTunnelBar()
	return true
}

func (a *App) scrubMCPTunnelLink() {
	if a.mcpTunnel != nil {
		a.mcpTunnel.url = ""
		a.mcpTunnel.expires = ""
		a.mcpTunnel.revealed = false
		a.mcpTunnel.notice = ""
	}
	if a.mcpURLView != nil {
		a.mcpURLView.SetText("")
	}
	if a.mcpURLHint != nil {
		a.mcpURLHint.SetText("")
	}
}

func (a *App) clearMCPTunnelBar() {
	a.scrubMCPTunnelLink()
	a.mcpURLBar = nil
	a.mcpURLView = nil
	a.mcpURLHint = nil
	a.rebuildMCPTunnelBarLayout()
}

// Adding/removing chrome must not steal focus from an approval card, form or
// unfinished chat draft. None of the bar's primitives takes keyboard focus.
func (a *App) rebuildMCPTunnelBarLayout() {
	var focus tview.Primitive
	if a.tv != nil {
		focus = a.tv.GetFocus()
	}
	a.rebuildLayout()
	if focus != nil {
		a.focusOrQueue(focus)
	}
}
