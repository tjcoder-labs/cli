package theme

import (
	"math"
	"strings"
	"testing"
)

func hueDist(a, b float64) float64 {
	d := math.Abs(a - b)
	if d > 180 {
		d = 360 - d
	}
	return d
}

func TestEveryRoleSharesThePrimaryHue(t *testing.T) {
	for _, primary := range []string{DefaultPrimary, "#2F80ED", "#E5484D", "#30A46C"} {
		for _, dark := range []bool{true, false} {
			th := MustNew(primary, dark)
			ph, _, _ := HSL(th.Primary)
			for _, c := range th.All() {
				h, s, l := HSL(c)
				if s < 0.05 { // pure neutrals (black/white backgrounds)
					continue
				}
				tol := 3.0
				if l > 0.92 || l < 0.08 { // 8-bit rounding of near-white/black tints
					tol = 15
				}
				if hueDist(h, ph) > tol {
					t.Errorf("primary %s dark=%v: %s hue %.1f far from %.1f", primary, dark, c, h, ph)
				}
			}
		}
	}
}

func TestDefaultMatchesBrand(t *testing.T) {
	d := Default()
	if !strings.EqualFold(d.Primary, "#A77CF8") {
		t.Fatalf("primary = %s", d.Primary)
	}
	seen := map[string]bool{}
	for _, c := range d.Accents() {
		if seen[c] {
			t.Errorf("duplicate accent %s", c)
		}
		seen[c] = true
	}
}

func TestMarksEscapeAndUseGlyphs(t *testing.T) {
	d := Default()
	for mark, want := range map[string]string{d.OK("[red]x"): "✓", d.Err("e"): "✗", d.Warn("w"): "!", d.Info("i"): "•"} {
		if !strings.Contains(mark, want) {
			t.Errorf("%q lacks %s", mark, want)
		}
	}
	if !strings.Contains(d.OK("[red]x"), "[red[]x") {
		t.Errorf("label not escaped: %s", d.OK("[red]x"))
	}
}

func TestNormalizeLegacyMarkup(t *testing.T) {
	th := Default()
	in := "[red]Provider save failed[-]: x [green]ok[-] [yellow::b]warn[-:-:-] [#C73CDC::bu]✗ e[-::BU] [#A77CF8]keep[-] [::b]b[::-] [red[] lit [white:black]w[-:-]"
	out := th.Normalize(in)
	want := "[" + th.Bright + "]Provider save failed[-]: x [" + th.Bright + "]ok[-] [" + th.Bright + "::b]warn[-:-:-] [" + th.Bright + "::bu]✗ e[-::BU] [#A77CF8]keep[-] [::b]b[::-] [red[] lit [white:black]w[-:-]"
	if out != want {
		t.Errorf("Normalize:\n got %s\nwant %s", out, want)
	}
}
