package tui

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/tjcoder-labs/cli/internal/client"
	"github.com/tjcoder-labs/cli/internal/tools"
)

// surfaceEdges are the per-row edge measurements of the prompt surface,
// in terminal cells.
type surfaceEdges struct {
	leftMargin  int // BgRoot cells before the surface
	leftBeam    int // '│' cells
	leftPad     int // bgInput cells between the beam and the first glyph
	rightPad    int // bgInput cells after the last glyph
	rightMargin int // BgRoot cells after the surface
	width       int // total cells in the row
}

// measureSurfaceRow classifies one rendered row. Rows without content
// report their padding as the whole bgInput run on that side (-1 marks
// "no glyph" so callers can skip inset comparisons on blank rows).
func measureSurfaceRow(a *App, cells []tcell.SimCell, cw, y int) (e surfaceEdges, hasText bool) {
	e.width = cw
	kind := make([]byte, cw) // r=root b=beam p=bgInput space t=text
	for x := 0; x < cw; x++ {
		c := cells[y*cw+x]
		_, bg, _ := c.Style.Decompose()
		r := ' '
		if len(c.Runes) > 0 {
			r = c.Runes[0]
		}
		switch {
		case r == '│':
			kind[x] = 'b'
		case bg != a.palette.BgInput:
			kind[x] = 'r'
		case r == ' ':
			kind[x] = 'p'
		default:
			kind[x] = 't'
		}
	}
	i := 0
	for ; i < cw && kind[i] == 'r'; i++ {
		e.leftMargin++
	}
	for ; i < cw && kind[i] == 'b'; i++ {
		e.leftBeam++
	}
	for ; i < cw && kind[i] == 'p'; i++ {
		e.leftPad++
	}
	j := cw - 1
	for ; j >= 0 && kind[j] == 'r'; j-- {
		e.rightMargin++
	}
	for ; j >= 0 && kind[j] == 'p'; j-- {
		e.rightPad++
	}
	hasText = strings.IndexByte(string(kind), 't') >= 0
	return e, hasText
}

// TestInputSurfaceEdgesSymmetric renders the real prompt surface (as
// New/build configure it) and asserts that every surface row, the
// bottom hint row included, has the same left and right edge columns
// and total width, and that the two ends mirror each other.
func TestInputSurfaceEdgesSymmetric(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	a := New(client.NewOllama("http://127.0.0.1:1"), tools.NewRegistry(nil), t.TempDir(), "m", "coder", "TJ", "0.0.0")
	// Fill both content rows past the edge so the content insets on
	// both sides are measurable.
	a.input.SetText(strings.Repeat("abcdefghij", 20))

	for _, width := range []int{40, 80, 120} {
		sim := tcell.NewSimulationScreen("UTF-8")
		if err := sim.Init(); err != nil {
			t.Fatal(err)
		}
		sim.SetSize(width, inputRowHeight)
		a.inputRow.SetRect(0, 0, width, inputRowHeight)
		a.contextBar.SetText(strings.Repeat("abcdefghij", 20))
		a.inputRow.Draw(sim)
		sim.Show()
		cells, cw, _ := sim.GetContents()
		sim.Fini()

		var ref surfaceEdges
		var refText surfaceEdges
		textRows := 0
		for y := 0; y < inputRowHeight; y++ {
			e, hasText := measureSurfaceRow(a, cells, cw, y)
			t.Logf("w=%d row %d: %+v text=%v", width, y, e, hasText)
			if e.width != width {
				t.Errorf("w=%d row %d: width %d", width, y, e.width)
			}
			if e.leftBeam != 1 {
				t.Errorf("w=%d row %d: beam cells = %d, want 1", width, y, e.leftBeam)
			}
			outer := surfaceEdges{leftMargin: e.leftMargin, leftBeam: e.leftBeam, rightMargin: e.rightMargin, width: e.width}
			if y == 0 {
				ref = outer
			} else if outer != ref {
				t.Errorf("w=%d row %d: outer edges %+v differ from row 0 %+v", width, y, outer, ref)
			}
			if !hasText {
				// Blank rows: bgInput spans beam to margin.
				if got, want := e.leftMargin+e.leftBeam+e.leftPad+e.rightMargin, width; got != want {
					t.Errorf("w=%d row %d: blank row covers %d cells, want %d", width, y, got, want)
				}
				continue
			}
			textRows++
			if textRows == 1 {
				refText = e
			} else if e != refText {
				t.Errorf("w=%d row %d: content edges %+v differ from first content row %+v", width, y, e, refText)
			}
		}
		if textRows != 2 {
			t.Fatalf("w=%d: expected 2 content rows (input + hint), got %d", width, textRows)
		}
		// Both ends mirror each other: BgRoot margin, and highlight
		// columns (beam + padding on the left vs padding on the right).
		if ref.leftMargin != ref.rightMargin {
			t.Errorf("w=%d: margins differ: left %d right %d", width, ref.leftMargin, ref.rightMargin)
		}
		if l, r := refText.leftBeam+refText.leftPad, refText.rightPad; l != r {
			t.Errorf("w=%d: highlight edge differs: left %d (beam+pad) right %d", width, l, r)
		}
	}
}
