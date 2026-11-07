package tui

import (
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// Pixel/cell-level parity guards the shared chrome, rather than merely
// asserting both widgets happen to reference a similarly named color.
func TestMCPTunnelSurfaceMatchesInputChrome(t *testing.T) {
	for _, palette := range []Palette{darkPalette(), lightPalette()} {
		for _, width := range []int{40, 80, 120} {
			for _, focused := range []bool{false, true} {
				a := &App{palette: palette, input: tview.NewInputField(), contextBar: tview.NewTextView()}
				a.input.SetFieldBackgroundColor(palette.BgInput).SetBackgroundColor(palette.BgInput)
				a.input.SetBorderPadding(1, 0, promptTextInset, promptTextInset)
				a.contextBar.SetBackgroundColor(palette.BgInput)
				a.contextBar.SetBorderPadding(0, 1, promptTextInset, promptTextInset)
				if focused {
					a.input.Focus(nil)
				}
				sim := tcell.NewSimulationScreen("UTF-8")
				if err := sim.Init(); err != nil {
					t.Fatal(err)
				}
				sim.SetSize(width, 24)
				a.terminalScreen = sim
				a.inputRow = a.newInputRow()
				attachTestTunnelBar(a)
				dock := a.newInputDock()
				height := a.inputDockHeight()
				dock.SetRect(0, 0, width, height)
				dock.Draw(sim)
				sim.Show()
				cells, cw, _ := sim.GetContents()
				_, inputY, _, _ := a.inputRow.GetRect()
				_, _, _, barHeight := a.mcpURLBar.GetRect()
				inputEdge, _ := measureSurfaceRow(a, cells, cw, inputY)
				for row := 0; row < barHeight-panelGutter; row++ {
					edge, _ := measureSurfaceRow(a, cells, cw, row)
					if edge.leftMargin != inputEdge.leftMargin || edge.rightMargin != inputEdge.rightMargin || edge.leftBeam != 1 {
						t.Errorf("width=%d focused=%v row=%d banner edges=%+v input=%+v", width, focused, row, edge, inputEdge)
					}
					for col := marginX - 1; col < width-(marginX-1); col++ {
						_, bg, _ := cells[row*cw+col].Style.Decompose()
						if bg != palette.BgInput {
							t.Errorf("banner surface gap at (%d,%d)", col, row)
						}
					}
					fg, _, _ := cells[row*cw+marginX-1].Style.Decompose()
					inputFG, _, _ := cells[inputY*cw+marginX-1].Style.Decompose()
					if fg != inputFG {
						t.Error("banner/input beams use different focus colors")
					}
				}
				// Its own top/bottom padding and a root-colored separation row make
				// this two sibling surfaces, not one extended input block.
				for _, row := range []int{0, barHeight - panelGutter - 1} {
					for col := marginX; col < width-(marginX-1); col++ {
						if runes := cells[row*cw+col].Runes; len(runes) > 0 && runes[0] != ' ' {
							t.Errorf("surface padding contains text at (%d,%d)", col, row)
						}
					}
				}
				for row := barHeight - panelGutter; row < barHeight; row++ {
					for col := 0; col < width; col++ {
						_, bg, _ := cells[row*cw+col].Style.Decompose()
						if bg != palette.BgRoot {
							t.Errorf("missing separation gutter at (%d,%d)", col, row)
						}
					}
				}
				urlX, _, _, _ := a.mcpURLView.GetInnerRect()
				inputX, _, _, _ := a.input.GetInnerRect()
				if urlX != inputX {
					t.Errorf("URL/input text insets differ: %d vs %d", urlX, inputX)
				}
				sim.Fini()
			}
		}
	}
}
