package tui

import (
	"bytes"
	"image/color"
	"image/png"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func TestRenderScreenPNGProducesValidImage(t *testing.T) {
	app := &App{tv: tview.NewApplication()}
	app.pages = tview.NewPages()
	app.pages.AddPage("main", tview.NewBox(), true, true)

	data, err := app.renderScreenPNG(80, 24)
	if err != nil {
		t.Fatalf("renderScreenPNG: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("empty PNG output")
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("png decode: %v", err)
	}
	b := img.Bounds()
	cellW := defaultCapture.fontSizePx
	cellH := defaultCapture.fontSizePx
	wantW := defaultCapture.padPx*2 + 80*cellW
	wantH := defaultCapture.padPx*2 + 24*cellH
	if b.Dx() != wantW || b.Dy() != wantH {
		t.Fatalf("image size %dx%d, want %dx%d", b.Dx(), b.Dy(), wantW, wantH)
	}
}

func TestCellFGDefaults(t *testing.T) {
	def := color.RGBA{0xaa, 0xbb, 0xcc, 0xff}
	if got := cellFG(tcell.StyleDefault, def); got != def {
		t.Fatalf("default fg = %v, want %v", got, def)
	}
	styled := tcell.StyleDefault.Foreground(tcell.NewRGBColor(1, 2, 3))
	got := cellFG(styled, def)
	if got.R != 1 || got.G != 2 || got.B != 3 {
		t.Fatalf("styled fg = %v", got)
	}
}
