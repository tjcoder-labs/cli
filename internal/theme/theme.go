// Package theme is the single source of truth for every colour the CLI
// draws. All colours are derived from ONE primary hue (the ergo brand
// purple by default, the same #A77CF8 used on ergo-ai-server's /auth
// page): accents are lighter/darker shades of that hue, text and
// backgrounds are low-saturation tints of it. Nothing else in the code
// base should hard-code a hex value or a named colour; call Current()
// (or receive a Theme) and use its roles instead, so that a future
// `/theme <hex>` only has to call SetCurrent(New(hex, dark)).
//
// Status is never encoded by hue. Use the Mark helpers, which pair a
// shade with a glyph (✓ ✗ ! •) and bold/dim attributes.
package theme

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// DefaultPrimary is the ergo brand purple.
const DefaultPrimary = "#A77CF8"

// Theme is a derived palette. Every field is a "#RRGGBB" string.
type Theme struct {
	Dark bool

	// Accent shades of the primary hue, ordered roughly light → dark.
	Primary string // the brand colour itself; titles, labels, keys
	Bright  string // lighter, more saturated; emphasis, headings, errors (with ✗ + bold)
	Soft    string // pale tint; string literals, quotes, secondary emphasis
	Deep    string // darker, saturated; types, numbers, tool arrows
	Muted   string // dark desaturated; inactive beams/accents

	// Near-neutral text tints (very low saturation of the same hue).
	Text  string // body text
	Dim   string // secondary text, timestamps, gutters
	Faint string // borders, rules, comments

	// Background tints.
	BgRoot      string
	BgReasoning string
	BgActivity  string
	BgInput     string
	BgModal     string
	BgSelect    string
}

type tone struct{ s, l float64 }

// Role recipes: saturation/lightness per role. Hue always comes from the
// primary. The dark recipes reproduce the original hand-tuned palette
// when the primary is the default brand purple.
var darkRecipe = map[string]tone{
	"Bright": {1.00, 0.82}, "Soft": {0.95, 0.87}, "Deep": {0.83, 0.58}, "Muted": {0.43, 0.34},
	"Text": {0.49, 0.92}, "Dim": {0.20, 0.62}, "Faint": {0.20, 0.36},
	"BgReasoning": {0.48, 0.053}, "BgActivity": {0.57, 0.045}, "BgInput": {0.44, 0.098},
	"BgModal": {0.43, 0.159}, "BgSelect": {0.42, 0.216},
}

var lightRecipe = map[string]tone{
	"Primary": {0.55, 0.52}, "Bright": {0.58, 0.47}, "Soft": {0.40, 0.42}, "Deep": {0.71, 0.41}, "Muted": {0.30, 0.70},
	"Text": {0.33, 0.15}, "Dim": {0.20, 0.49}, "Faint": {0.18, 0.69},
	"BgReasoning": {0.20, 0.975}, "BgActivity": {0.20, 0.96}, "BgInput": {1.0, 0.99},
	"BgModal": {0.33, 0.955}, "BgSelect": {0.60, 0.90},
}

// New derives a full Theme from one primary colour.
func New(primary string, dark bool) (Theme, error) {
	r, g, b, err := parseHex(primary)
	if err != nil {
		return Theme{}, err
	}
	h, _, _ := rgbToHSL(r, g, b)
	recipe := darkRecipe
	if !dark {
		recipe = lightRecipe
	}
	mk := func(role string) string {
		t := recipe[role]
		return hslHex(h, t.s, t.l)
	}
	th := Theme{
		Dark:        dark,
		Primary:     strings.ToUpper(normalizeHex(primary)),
		Bright:      mk("Bright"),
		Soft:        mk("Soft"),
		Deep:        mk("Deep"),
		Muted:       mk("Muted"),
		Text:        mk("Text"),
		Dim:         mk("Dim"),
		Faint:       mk("Faint"),
		BgReasoning: mk("BgReasoning"),
		BgActivity:  mk("BgActivity"),
		BgInput:     mk("BgInput"),
		BgModal:     mk("BgModal"),
		BgSelect:    mk("BgSelect"),
	}
	if dark {
		th.BgRoot = "#000000"
	} else {
		th.BgRoot = "#FFFFFF"
		th.Primary = mk("Primary")
	}
	return th, nil
}

// MustNew is New for known-good inputs.
func MustNew(primary string, dark bool) Theme {
	t, err := New(primary, dark)
	if err != nil {
		panic(err)
	}
	return t
}

// Default is the dark theme derived from the brand purple.
func Default() Theme { return MustNew(DefaultPrimary, true) }

var current atomic.Pointer[Theme]

func init() {
	t := Default()
	current.Store(&t)
}

// Current returns the process-wide active theme.
func Current() Theme { return *current.Load() }

// SetCurrent swaps the process-wide theme (e.g. from a future /theme).
func SetCurrent(t Theme) { current.Store(&t) }

// Accents lists the accent roles; used by tests and the colour audit.
func (t Theme) Accents() []string {
	return []string{t.Primary, t.Bright, t.Soft, t.Deep, t.Muted}
}

// All lists every colour in the theme.
func (t Theme) All() []string {
	return append(t.Accents(), t.Text, t.Dim, t.Faint,
		t.BgRoot, t.BgReasoning, t.BgActivity, t.BgInput, t.BgModal, t.BgSelect)
}

// ---- status marks (tview markup; label must already be safe markup) ----

// OK renders "✓ label" in the bright shade, bold.
func (t Theme) OK(label string) string {
	return fmt.Sprintf("[%s::b]✓ %s[-::B]", t.Bright, tview.Escape(label))
}

// Err renders "✗ label" in the bright shade, bold + underline so it
// stands apart from OK without a different hue.
func (t Theme) Err(label string) string {
	return fmt.Sprintf("[%s::bu]✗ %s[-::BU]", t.Bright, tview.Escape(label))
}

// Warn renders "! label" in the primary shade, bold.
func (t Theme) Warn(label string) string {
	return fmt.Sprintf("[%s::b]! %s[-::B]", t.Primary, tview.Escape(label))
}

// Info renders "• label" in the dim text tint.
func (t Theme) Info(label string) string {
	return fmt.Sprintf("[%s]• %s[-]", t.Dim, tview.Escape(label))
}

// Tag wraps markup in a foreground colour tag.
func Tag(hex, s string) string { return "[" + hex + "]" + s + "[-]" }

// ---- colour math ----

func normalizeHex(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "#") {
		s = "#" + s
	}
	if len(s) == 4 { // #abc
		s = "#" + strings.Repeat(s[1:2], 2) + strings.Repeat(s[2:3], 2) + strings.Repeat(s[3:4], 2)
	}
	return s
}

func parseHex(s string) (r, g, b float64, err error) {
	s = normalizeHex(s)
	if len(s) != 7 {
		return 0, 0, 0, fmt.Errorf("theme: invalid colour %q", s)
	}
	v, err := strconv.ParseUint(s[1:], 16, 32)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("theme: invalid colour %q", s)
	}
	return float64(v>>16&0xff) / 255, float64(v>>8&0xff) / 255, float64(v&0xff) / 255, nil
}

// RGB parses a "#RRGGBB" string into 0-255 components.
func RGB(hex string) (int32, int32, int32) {
	r, g, b, err := parseHex(hex)
	if err != nil {
		return 0, 0, 0
	}
	return int32(math.Round(r * 255)), int32(math.Round(g * 255)), int32(math.Round(b * 255))
}

func rgbToHSL(r, g, b float64) (h, s, l float64) {
	mx := math.Max(r, math.Max(g, b))
	mn := math.Min(r, math.Min(g, b))
	l = (mx + mn) / 2
	if mx == mn {
		return 0, 0, l
	}
	d := mx - mn
	if l > 0.5 {
		s = d / (2 - mx - mn)
	} else {
		s = d / (mx + mn)
	}
	switch mx {
	case r:
		h = (g - b) / d
		if g < b {
			h += 6
		}
	case g:
		h = (b-r)/d + 2
	default:
		h = (r-g)/d + 4
	}
	return h * 60, s, l
}

// HSL returns the hue (degrees), saturation and lightness of a hex colour.
func HSL(hex string) (h, s, l float64) {
	r, g, b, err := parseHex(hex)
	if err != nil {
		return 0, 0, 0
	}
	return rgbToHSL(r, g, b)
}

func hslHex(h, s, l float64) string {
	hue := func(p, q, t float64) float64 {
		if t < 0 {
			t++
		}
		if t > 1 {
			t--
		}
		switch {
		case t < 1.0/6:
			return p + (q-p)*6*t
		case t < 0.5:
			return q
		case t < 2.0/3:
			return p + (q-p)*(2.0/3-t)*6
		}
		return p
	}
	var r, g, b float64
	if s == 0 {
		r, g, b = l, l, l
	} else {
		q := l * (1 + s)
		if l >= 0.5 {
			q = l + s - l*s
		}
		p := 2*l - q
		hh := h / 360
		r, g, b = hue(p, q, hh+1.0/3), hue(p, q, hh), hue(p, q, hh-1.0/3)
	}
	c := func(x float64) int { return int(math.Round(math.Max(0, math.Min(1, x)) * 255)) }
	return fmt.Sprintf("#%02X%02X%02X", c(r), c(g), c(b))
}

// legacyTagRe matches a tview style tag whose foreground and/or
// background is a colour name or #rrggbb, e.g. "[red]", "[#C73CDC::b]",
// "[yellow:black:b]". Escaped literals such as "[red[]" do not match.
var legacyTagRe = regexp.MustCompile(`\[([A-Za-z]+|#[0-9A-Fa-f]{6}|-)?(?:(:)([A-Za-z]+|#[0-9A-Fa-f]{6}|-)?)?(?:(:)([A-Za-z-]*))?\]`)

// OnScheme reports whether hex is a shade of t's primary hue (or a
// near-neutral grey/black/white, which carries no hue).
func (t Theme) OnScheme(hex string) bool {
	h, s, l := HSL(hex)
	if s < 0.12 || l < 0.04 || l > 0.97 {
		return true
	}
	ph, _, _ := HSL(t.Primary)
	d := math.Abs(h - ph)
	if d > 180 {
		d = 360 - d
	}
	return d <= 12
}

// Normalize rewrites colours in tview markup that are not on t's
// scheme. It is used for text persisted by older versions (the saved
// activity log), which may contain "[red]", "[green]" or the retired
// orchid accent: foregrounds become Bright (the alert shade, which old
// code always paired with a ✗/✓ label) and backgrounds BgSelect.
func (t Theme) Normalize(markup string) string {
	fix := func(c, repl string) string {
		if c == "" || c == "-" {
			return c
		}
		hex := c
		if !strings.HasPrefix(c, "#") {
			col := tcell.GetColor(strings.ToLower(c))
			if col == tcell.ColorDefault {
				return c // not a colour name (or "default")
			}
			r, g, b := col.RGB()
			hex = fmt.Sprintf("#%02X%02X%02X", r, g, b)
		}
		if t.OnScheme(hex) {
			return c
		}
		return repl
	}
	return legacyTagRe.ReplaceAllStringFunc(markup, func(tag string) string {
		m := legacyTagRe.FindStringSubmatch(tag)
		return "[" + fix(m[1], t.Bright) + m[2] + fix(m[3], t.BgSelect) + m[4] + m[5] + "]"
	})
}
