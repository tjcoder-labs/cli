package tui

import (
	"regexp"
	"strings"
	"testing"

	"github.com/rivo/tview"
)

func TestUserMessageMaxWidthPinnedByConfig(t *testing.T) {
	app := &App{
		transcript: tview.NewTextView(),
		palette:    darkPalette(),
		config:     AppConfig{UserMessageMaxWidth: 50},
	}
	if got := app.userMessageMaxWidth(); got != 50 {
		t.Fatalf("expected pinned cap 50, got %d", got)
	}
}

func TestUserMessageMaxWidthClampsPinnedValue(t *testing.T) {
	app := &App{
		transcript: tview.NewTextView(),
		palette:    darkPalette(),
	}
	// Below the minimum should be clamped up to the minimum.
	app.config.UserMessageMaxWidth = 5
	if got := app.userMessageMaxWidth(); got != userMessageMinWidth {
		t.Fatalf("expected sub-min clamp to %d, got %d", userMessageMinWidth, got)
	}
	// Above the maximum should be clamped down to the maximum.
	app.config.UserMessageMaxWidth = 500
	if got := app.userMessageMaxWidth(); got != userMessageMaxWidth {
		t.Fatalf("expected super-max clamp to %d, got %d", userMessageMaxWidth, got)
	}
}

func TestUserMessageMaxWidthFallsBackWithoutScreen(t *testing.T) {
	// No transcript, no config: must still return a sane default
	// so unit tests that call renderUserMessage don't panic.
	app := &App{palette: darkPalette()}
	got := app.userMessageMaxWidth()
	if got < userMessageMinWidth || got > userMessageMaxWidth {
		t.Fatalf("default cap %d out of [%d, %d]", got, userMessageMinWidth, userMessageMaxWidth)
	}
}

func TestRenderUserMessageWrapsLongInput(t *testing.T) {
	// Pin the cap small so we can verify wrap behavior without
	// depending on terminal width. 24 is the minimum.
	app := &App{
		transcript: tview.NewTextView(),
		palette:    darkPalette(),
		config:     AppConfig{UserMessageMaxWidth: 24},
	}
	// 80-char single line of words; with cap=24 it should wrap to
	// at least 4 lines, none wider than 24.
	long := "alpha beta gamma delta epsilon zeta eta theta iota kappa lambda mu nu xi omicron pi rho sigma"
	got := app.renderUserMessage(long)
	// Strip color tags fully: any `[...]` is a tag, regardless of
	// contents. This is a stricter version of the old `["#"` hack
	// which would miss tags whose color name did not start with
	// `#` (e.g. `[white:black]`).
	stripTags := regexp.MustCompile(`\[[^\]]*\]`)
	cleaned := stripTags.ReplaceAllString(got, "")
	// Split into rendered bubble lines. Each visible line between
	// newlines that contains text is a candidate.
	lines := strings.Split(cleaned, "\n")
	worst := 0
	for _, line := range lines {
		// Cell count = number of runes minus non-breaking spaces
		// (which only pad; they shouldn't count as content). The
		// bubble pads each content line to exactly cap+2 with
		// non-breaking spaces, so the visible text width is the
		// first non-space run.
		visible := strings.TrimRight(strings.TrimSpace(line), " ")
		if l := len([]rune(visible)); l > worst {
			worst = l
		}
	}
	if worst > 26 {
		// cap (24) + 1 col padding either side = 26.
		t.Fatalf("bubble line %d cols exceeds cap+padding (24+2=26): %q", worst, lines)
	}
	// And the prompt must have produced multiple lines (no wrap
	// would have been a single line of ~73 chars).
	if len(lines) < 4 {
		t.Fatalf("expected long input to wrap to >=4 bubble lines, got %d", len(lines))
	}
}

func TestRenderUserMessageShortInputUnchanged(t *testing.T) {
	app := &App{
		transcript: tview.NewTextView(),
		palette:    darkPalette(),
	}
	got := app.renderUserMessage("hi")
	if !strings.Contains(got, "hi") {
		t.Fatalf("expected short input in bubble, got %q", got)
	}
	// Short input must not be padded out to the cap; the bubble
	// width should match the content + 2 padding, not the cap.
	// We assert by counting newlines: 3 lines (top border, content,
	// bottom border) is the minimum for any non-empty input.
	if strings.Count(got, "\n") < 3 {
		t.Fatalf("expected at least 3 newlines (borders + content), got %d in %q", strings.Count(got, "\n"), got)
	}
}
