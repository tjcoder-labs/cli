package tui

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// renderInputRowMask draws the prompt row exactly as build() composes it
// (BgRoot margins around newInputSurface, inputRowHeight tall, with a
// BgRoot row below standing in for the footer) and returns a mask:
// '│' beam, '░' bgInput cell, '.' anything else, 'x' text on bgInput.
func renderInputRowMask(t *testing.T, width int) []string {
	t.Helper()
	a := &App{input: tview.NewInputField(), contextBar: tview.NewTextView(), palette: darkPalette()}
	a.input.SetFieldBackgroundColor(a.palette.BgInput)
	a.input.SetBackgroundColor(a.palette.BgInput)
	a.contextBar.SetDynamicColors(true).SetWrap(false)
	a.contextBar.SetBackgroundColor(a.palette.BgInput)
	a.contextBar.SetBorderPadding(1, 1, 2, 2)
	a.contextBar.SetText("● ~/src/coder  ctx 12K / 128K")
	row := a.newInputRow()
	layout := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(row, inputRowHeight, 0, true).
		AddItem(spacerBox(a.palette.BgRoot), 1, 0, false)

	sim := tcell.NewSimulationScreen("UTF-8")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	defer sim.Fini()
	h := inputRowHeight + 1
	sim.SetSize(width, h)
	layout.SetRect(0, 0, width, h)
	layout.Draw(sim)
	sim.Show()
	cells, cw, ch := sim.GetContents()
	out := make([]string, ch)
	for y := 0; y < ch; y++ {
		var b strings.Builder
		for x := 0; x < cw; x++ {
			c := cells[y*cw+x]
			_, bg, _ := c.Style.Decompose()
			r := ' '
			if len(c.Runes) > 0 {
				r = c.Runes[0]
			}
			switch {
			case r == '│':
				b.WriteRune('│')
			case bg == a.palette.BgInput && r == ' ':
				b.WriteRune('░')
			case bg == a.palette.BgInput:
				b.WriteRune('x')
			default:
				b.WriteRune('.')
			}
		}
		out[y] = b.String()
	}
	return out
}

func TestInputSurfaceGolden(t *testing.T) {
	got := strings.Join(renderInputRowMask(t, 40), "\n")
	want := strings.Join([]string{
		"..│░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░..",
		"..│░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░..",
		"..│░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░..",
		"..│░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░..",
		"..│░░░░x░xxxxxxxxxxx░░xxx░xxx░x░x░░░░░..",
		"..│░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░..",
		"........................................",
	}, "\n")
	if got != want {
		t.Fatalf("input surface render mismatch\n got:\n%s\nwant:\n%s", got, want)
	}
}

// Every surface row, including the bottom hint row, must carry the left
// beam and span the same bgInput columns; nothing may spill below.
func TestInputSurfaceBottomRowMatchesRowsAbove(t *testing.T) {
	for _, width := range []int{20, 32, 60, 120} {
		rows := renderInputRowMask(t, width)
		first := rows[0]
		if []rune(first)[marginX-1] != '│' {
			t.Fatalf("w=%d: expected beam at col %d: %q", width, marginX-1, first)
		}
		for y := 1; y < inputRowHeight; y++ {
			r, f := []rune(rows[y]), []rune(first)
			if r[marginX-1] != '│' {
				t.Errorf("w=%d row %d: missing left beam: %q", width, y, rows[y])
			}
			// bgInput extent must match the first row exactly.
			if strings.IndexAny(rows[y], "░x") != strings.IndexAny(first, "░x") ||
				lastSurface(r) != lastSurface(f) {
				t.Errorf("w=%d row %d: surface extent differs from top row:\n%q\n%q", width, y, first, rows[y])
			}
		}
		if strings.ContainsAny(rows[inputRowHeight], "░x│") {
			t.Errorf("w=%d: surface spilled below its row: %q", width, rows[inputRowHeight])
		}
	}
}

func lastSurface(r []rune) int {
	for i := len(r) - 1; i >= 0; i-- {
		if r[i] == '░' || r[i] == 'x' {
			return i
		}
	}
	return -1
}
