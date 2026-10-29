package tui

import (
	"strings"
	"testing"

	"github.com/rivo/tview"
	"github.com/tjcoder-labs/cli/internal/client"
)

func newCompactTipApp(t *testing.T) *App {
	t.Helper()
	a := newModelTestApp(t, client.NewOpenAICompatible("http://x", "", 0))
	a.input = tview.NewInputField()
	a.contextBar = tview.NewTextView()
	a.contextBar.SetRect(0, 0, 100, 1)
	a.currentModel = "gpt-4o"
	a.compactWindows = map[string]int{"gpt-4o": 128000, "ergo-lite": 0}
	return a
}

func TestCompactTipShowsModelAndPercentOfItsWindow(t *testing.T) {
	a := newCompactTipApp(t)
	a.contextInfo = "ctx: 79.4K / 1M used (7.9%), remaining=920K, output=1K"
	got := a.compactTip("compact")
	if want := "Compact with gpt-4o · context 62% of 128k"; got != want {
		t.Fatalf("tip = %q, want %q", got, want)
	}
}

func TestCompactTipUsesConfigAndArgOverride(t *testing.T) {
	a := newCompactTipApp(t)
	a.contextInfo = "ctx: 64K / 1M used"
	a.config.CompactModel = "gpt-4o"
	a.currentModel = "other-model"
	if got := a.compactTip("compact"); !strings.HasPrefix(got, "Compact with gpt-4o · context 50% of 128k") {
		t.Fatalf("config override tip = %q", got)
	}
	a.compactWindows["claude-opus-4"] = 200000
	if got := a.compactTip("compact claude-opus-4"); got != "Compact with claude-opus-4 · context 32% of 200k" {
		t.Fatalf("arg override tip = %q", got)
	}
}

func TestCompactTipUnknownWindowShowsTokensOnly(t *testing.T) {
	a := newCompactTipApp(t)
	a.contextInfo = "ctx: unavailable"
	a.currentModel = "ergo-lite"
	a.history = []client.Message{{Role: "user", Content: strings.Repeat("x", 3996)}}
	got := a.compactTip("compact")
	if got != "Compact with ergo-lite · context ~1k tokens" {
		t.Fatalf("tip = %q", got)
	}
	if strings.Contains(got, "%") {
		t.Fatalf("unknown window must not show a percentage: %q", got)
	}
}

func TestCommandPaletteShowsCompactTip(t *testing.T) {
	a := newCompactTipApp(t)
	a.contextInfo = "ctx: 79.4K / 1M used"
	a.input.SetText("/compact")
	a.updateInputHint("/compact")
	line := plain(a.refreshContextBar())
	if !strings.Contains(line, "/compact") || !strings.Contains(line, "Compact with gpt-4o · context 62% of 128k") {
		t.Fatalf("palette line missing compact tip: %q", line)
	}
}
