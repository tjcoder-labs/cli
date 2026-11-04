package tui

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
)

// captureDefaults holds the rasterization defaults for /screenshot and
// /capture. It is kept small and tunable; the important knobs are the
// font size (cell height in pixels) and the horizontal padding so the
// resulting PNG has breathing room for social media.
type captureDefaults struct {
	fontSizePx int // pixel height of one cell
	padPx      int // uniform border padding in pixels
}

var defaultCapture = captureDefaults{fontSizePx: 22, padPx: 24}

// parseCaptureFont parses the embedded Go Mono TTF once and caches the
// sfnt font so repeated captures don't re-parse the font data.
var (
	captureFontOnce sync.Once
	captureFont     *sfnt.Font
	captureFontErr  error
)

func loadCaptureFont() (*sfnt.Font, error) {
	captureFontOnce.Do(func() {
		captureFont, captureFontErr = sfnt.Parse(gomono.TTF)
	})
	return captureFont, captureFontErr
}

// renderScreenPNG renders the current TUI root into an in-memory PNG at
// the given cell dimensions and returns the encoded bytes. It mounts a
// fresh simulation screen, draws the root primitives into it, reads the
// cell contents back, and rasterizes them with the embedded Go Mono font.
func (a *App) renderScreenPNG(cols, rows int) ([]byte, error) {
	if a.tv == nil || a.pages == nil {
		return nil, fmt.Errorf("screen not initialised")
	}

	// Build a simulation screen at the requested size and render the
	// live root into it. This is read-only with respect to the real
	// terminal: it never touches the interactive tcell screen.
	sim := tcell.NewSimulationScreen("")
	if err := sim.Init(); err != nil {
		return nil, fmt.Errorf("simulation screen init: %w", err)
	}
	defer sim.Fini()
	sim.SetSize(cols, rows)

	// Draw the current root at the simulation size. We borrow a tiny
	// throwaway Application so Draw runs the full beforeDraw/afterDraw
	// pipeline against the simulation screen instead of the real one.
	tmp := tview.NewApplication()
	tmp.SetScreen(sim)
	tmp.SetRoot(a.pages, true)
	tmp.ForceDraw()

	cells, w, h := sim.GetContents()
	_ = w
	_ = h

	fnt, err := loadCaptureFont()
	if err != nil {
		return nil, fmt.Errorf("load embedded font: %w", err)
	}

	// Rasterize into an RGBA image with a dark terminal background.
	// Cell width follows the font's real advance width, not a hardcoded
	// fraction, so the frame keeps the correct character aspect ratio.
	d := defaultCapture
	cellH := d.fontSizePx

	bg := color.RGBA{0x12, 0x12, 0x18, 0xff}
	fg := color.RGBA{0xdc, 0xdc, 0xe0, 0xff}

	faceOpts := &opentype.FaceOptions{
		Size:    float64(cellH),
		DPI:     72,
		Hinting: font.HintingFull,
	}
	face, err := opentype.NewFace(fnt, faceOpts)
	if err != nil {
		return nil, fmt.Errorf("new face: %w", err)
	}
	defer face.Close()

	// Cell width follows the font's real advance width, not a hardcoded
	// fraction, so the frame keeps the correct character aspect ratio.
	cellW := font.MeasureString(face, " ").Ceil()
	if cellW <= 0 {
		cellW = cellH * 6 / 10
	}
	imgW := d.padPx*2 + cols*cellW
	imgH := d.padPx*2 + rows*cellH
	img := image.NewRGBA(image.Rect(0, 0, imgW, imgH))

	// Fill background.
	for y := 0; y < imgH; y++ {
		for x := 0; x < imgW; x++ {
			img.SetRGBA(x, y, bg)
		}
	}

	drawString := func(x, y int, s string, c color.RGBA) {
		d := font.Drawer{
			Dst:  img,
			Src:  image.NewUniform(c),
			Face: face,
			Dot:  fixed.P(x, y+cellH-face.Metrics().Descent.Ceil()),
		}
		d.DrawString(s)
	}

	for cy := 0; cy < rows; cy++ {
		for cx := 0; cx < cols; cx++ {
			idx := cy*cols + cx
			if idx >= len(cells) {
				continue
			}
			cell := cells[idx]
			// Default foreground/background; map tcell colors.
			ch := ' '
			if len(cell.Runes) > 0 {
				ch = cell.Runes[0]
			}
			bgCol := cellBG(cell.Style, bg)
			fgCol := cellFG(cell.Style, fg)

			px := d.padPx + cx*cellW
			py := d.padPx + cy*cellH
			// Paint the cell background.
			for yy := 0; yy < cellH; yy++ {
				for xx := 0; xx < cellW; xx++ {
					img.SetRGBA(px+xx, py+yy, bgCol)
				}
			}
			if ch != ' ' {
				drawString(px, py, string(ch), fgCol)
			}
		}
	}

	// Encode PNG.
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("encode png: %w", err)
	}
	return buf.Bytes(), nil
}

// cellBG resolves a tcell style background to an RGBA color, falling back
// to the provided default when ColorDefault.
func cellBG(st tcell.Style, def color.RGBA) color.RGBA {
	_, bg, _ := st.Decompose()
	if bg == tcell.ColorDefault {
		return def
	}
	r, g, b := bg.RGB()
	return color.RGBA{uint8(r), uint8(g), uint8(b), 0xff}
}

// cellFG resolves a tcell style foreground to an RGBA color, falling back
// to the provided default when ColorDefault.
func cellFG(st tcell.Style, def color.RGBA) color.RGBA {
	fgc, _, _ := st.Decompose()
	if fgc == tcell.ColorDefault {
		return def
	}
	r, g, b := fgc.RGB()
	return color.RGBA{uint8(r), uint8(g), uint8(b), 0xff}
}

// captureDir returns the configured media directory, defaulting to the
// user's ~/Downloads when unset or unresolvable.
func (a *App) captureDir() string {
	dir := a.config.CaptureDir
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "."
		}
		dir = filepath.Join(home, "Downloads")
	}
	return dir
}

// writeCaptureFile writes data to dir with a timestamped filename and
// returns the full path. It creates the directory if needed.
func (a *App) writeCaptureFile(kind string, data []byte) (string, error) {
	dir := a.captureDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	name := fmt.Sprintf("%s-%s.png", kind, time.Now().Format("20060102-150405"))
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// handleScreenshot renders and saves a single PNG of the current screen.
func (a *App) handleScreenshot() {
	cols, rows := a.screenCells()
	data, err := a.renderScreenPNG(cols, rows)
	if err != nil {
		a.appendActivity(fmt.Sprintf("["+a.palette.HexAlert+"::b]✗ screenshot failed[-:-:-]: %v", err))
		return
	}
	path, err := a.writeCaptureFile("screenshot", data)
	if err != nil {
		a.appendActivity(fmt.Sprintf("["+a.palette.HexAlert+"::b]✗ screenshot failed[-:-:-]: %v", err))
		return
	}
	a.appendActivity(fmt.Sprintf("Saved screenshot to %s (%dx%d)", path, cols, rows))
}

// screenCells returns the current terminal cell dimensions, or a sane
// default when the real screen is unavailable.
func (a *App) screenCells() (int, int) {
	if a.tv == nil {
		return 100, 40
	}
	// tview does not expose the screen; use a fixed high-res default.
	// The simulation render is independent of the physical terminal size,
	// so a fixed 120x45 canvas gives a consistent, social-media-friendly
	// frame regardless of the user's actual terminal.
	return 120, 45
}

// captureMinIntervalDefault is the minimum time between sampled frames
// while recording. It throttles the afterDraw hook so a fast redraw
// burst doesn't capture hundreds of near-identical frames.
const captureMinIntervalDefault = 100 * time.Millisecond

// sampleCaptureFrame is called from afterDraw on every draw pass. When a
// capture is recording, it renders the current screen to a PNG and appends
// it to captureFrames, throttled so the recording is a smooth slideshow
// rather than a burst of duplicates.
func (a *App) sampleCaptureFrame() {
	if !a.captureRecording {
		return
	}
	now := time.Now()
	if a.captureMinInterval <= 0 {
		a.captureMinInterval = captureMinIntervalDefault
	}
	if now.Sub(a.captureLastSample) < a.captureMinInterval {
		return
	}
	a.captureLastSample = now
	cols, rows := a.screenCells()
	data, err := a.renderScreenPNG(cols, rows)
	if err != nil {
		// Don't spam the activity log on every failed frame.
		a.appendActivity(fmt.Sprintf("["+a.palette.HexAlert+"::b]✗ capture frame failed[-:-:-]: %v", err))
		return
	}
	a.captureFrames = append(a.captureFrames, data)
}

// finishCapture finalises an in-progress capture: it stops recording and
// writes the accumulated frames to disk. A single frame is saved as a
// screenshot; multiple frames are written as a numbered sequence of PNGs
// (video encoding, e.g. to WebM/GIF, is a follow-up).
func (a *App) finishCapture() {
	a.captureRecording = false
	n := len(a.captureFrames)
	if n == 0 {
		a.appendActivity("Capture stopped — no frames were recorded.")
		return
	}
	if n == 1 {
		path, err := a.writeCaptureFile("screenshot", a.captureFrames[0])
		if err != nil {
			a.appendActivity(fmt.Sprintf("["+a.palette.HexAlert+"::b]✗ capture save failed[-:-:-]: %v", err))
			return
		}
		a.appendActivity(fmt.Sprintf("Capture saved %d frame to %s", n, path))
		return
	}
	// Multiple frames: write a numbered sequence under a capture-specific
	// subdirectory so the frames can later be assembled into a video.
	base := a.captureDir()
	dir := filepath.Join(base, fmt.Sprintf("capture-%s", time.Now().Format("20060102-150405")))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		a.appendActivity(fmt.Sprintf("["+a.palette.HexAlert+"::b]✗ capture save failed[-:-:-]: %v", err))
		return
	}
	for i, frame := range a.captureFrames {
		name := fmt.Sprintf("frame-%04d.png", i+1)
		if err := os.WriteFile(filepath.Join(dir, name), frame, 0o644); err != nil {
			a.appendActivity(fmt.Sprintf("["+a.palette.HexAlert+"::b]✗ capture save failed[-:-:-]: %v", err))
			return
		}
	}
	a.appendActivity(fmt.Sprintf("Capture saved %d frames to %s", n, dir))
}

// handleCapture handles "/capture start|stop|screenshot".
func (a *App) handleCapture(parts []string) {
	sub := ""
	if len(parts) > 1 {
		sub = strings.ToLower(parts[1])
	}
	switch sub {
	case "screenshot":
		// Shorthand for /screenshot: render a single frame immediately.
		a.handleScreenshot()
	case "start":
		a.captureRecording = true
		a.captureFrames = a.captureFrames[:0]
		a.captureLastSample = time.Time{}
		a.appendActivity("Capture started. Frames will be recorded. Use /capture stop to save, or /capture screenshot for a single frame.")
	case "stop":
		if !a.captureRecording {
			a.appendActivity("Capture not running. Use /capture start first.")
			return
		}
		a.finishCapture()
	default:
		a.appendActivity("Usage: /capture start | stop | screenshot  ·  /screenshot saves the current screen")
	}
}
