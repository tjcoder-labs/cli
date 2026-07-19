package tui

import (
	"strings"
	"testing"
)

// stripTags removes tview color/style tags so tests can assert on the
// visible text of the rendered hint line.
func stripTags(s string) string {
	var b strings.Builder
	depth := 0
	for _, r := range s {
		switch r {
		case '[':
			depth++
		case ']':
			if depth > 0 {
				depth--
				continue
			}
			b.WriteRune(r)
		default:
			if depth == 0 {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}

func TestUpdateInputHintModes(t *testing.T) {
	app := &App{palette: darkPalette()}

	app.updateInputHint("hello")
	if app.hintMode != "" {
		t.Fatalf("plain text should be meta mode, got %q", app.hintMode)
	}

	app.updateInputHint("@READ")
	if app.hintMode != "reference" {
		t.Fatalf("@ prefix should be reference mode, got %q", app.hintMode)
	}

	app.updateInputHint("/mod")
	if app.hintMode != "command" {
		t.Fatalf("/ prefix should be command mode, got %q", app.hintMode)
	}
	if len(app.hintMatches) != 1 || app.hintMatches[0].cmd != "model" {
		t.Fatalf("/mod should match only 'model', got %+v", app.hintMatches)
	}

	// Argument tokens after the command should not disturb matching.
	app.updateInputHint("/scroll up 10")
	if len(app.hintMatches) != 1 || app.hintMatches[0].cmd != "scroll" {
		t.Fatalf("/scroll with args should match 'scroll', got %+v", app.hintMatches)
	}
}

func TestRenderCommandPaletteChevronsAndSelection(t *testing.T) {
	app := &App{palette: darkPalette()}
	app.updateInputHint("/") // all commands match

	// Highlight an entry far enough in that content must be clipped on
	// the left, forcing a leading chevron.
	app.hintSelected = len(app.hintMatches) - 1
	out := stripTags(app.renderCommandPalette())
	if !strings.Contains(out, "‹") {
		t.Fatalf("expected leading chevron when window is clipped left: %q", out)
	}
	// The highlighted command's label must be visible.
	want := "/" + app.hintMatches[app.hintSelected].cmd
	if !strings.Contains(out, want) {
		t.Fatalf("expected highlighted command %q to be visible: %q", want, out)
	}

	// Selecting the first entry should clip the right edge instead.
	app.hintSelected = 0
	out = stripTags(app.renderCommandPalette())
	if !strings.Contains(out, "›") {
		t.Fatalf("expected trailing chevron when window is clipped right: %q", out)
	}
	if !strings.Contains(out, "/"+app.hintMatches[0].cmd) {
		t.Fatalf("expected first command visible: %q", out)
	}
}

func TestRenderCommandPaletteNoMatches(t *testing.T) {
	app := &App{palette: darkPalette()}
	app.updateInputHint("/zzzznotacommand")
	if len(app.hintMatches) != 0 {
		t.Fatalf("expected no matches, got %+v", app.hintMatches)
	}
	out := stripTags(app.renderCommandPalette())
	if !strings.Contains(out, "no matching commands") {
		t.Fatalf("expected empty-state text, got %q", out)
	}
}
