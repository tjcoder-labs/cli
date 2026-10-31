package tui

import (
	"strings"

	"github.com/tjcoder-labs/cli/internal/render"
	"github.com/tjcoder-labs/cli/internal/theme"
)

// The prompt card is the single component behind every interactive
// transcript prompt: the sudo authorization and password prompts, the
// update prompt and the agent's ask_user questions. Both its live
// (focused, answerable) state and its answered state left in the
// transcript are drawn by renderCard, so they always look the same.
//
// Layout (every cell on BgInput; "▎" accent in Primary):
//
//	▎
//	▎  HEADING
//	▎  Question text, wrapped
//	▎  $ detail
//	▎
//	▎  › 1. Option          (or the answer row when answered)
//	▎    2. Option
//	▎
//	▎  hint
//	▎
const (
	cardAccent   = "▎"
	cardPadLeft  = 2 // cells between the accent and the content
	cardPadRight = 2
	cardMaxWidth = 96 // keep cards readable on very wide terminals
)

// cardWidth clamps the available pane width for a card.
func cardWidth(avail int) int {
	if avail <= 0 {
		avail = 80
	}
	if avail > cardMaxWidth {
		avail = cardMaxWidth
	}
	if avail < 20 {
		avail = 20
	}
	return avail
}

// cardErrorAccent is the red left rule used by error cards. It is a
// fixed hue so a failure never inherits the brand purple.
const cardErrorAccent = "#E85A5A"

// renderCard boxes content lines into a card exactly width cells wide.
// accent overrides the left rule colour; empty uses the brand primary.
// A nil line in content renders as a blank spacer row. Long lines are
// wrapped; continuation rows keep the first span's indent when it is
// pure whitespace (option descriptions, typed answers).
func renderCard(th theme.Theme, content []render.Line, width int, accent string) string {
	width = cardWidth(width)
	inner := width - 1 - cardPadLeft - cardPadRight
	if inner < 8 {
		inner = 8
	}
	if accent == "" {
		accent = th.Primary
	}
	row := func(l render.Line) string {
		out := render.Line{{Text: cardAccent, Fg: accent}, {Text: strings.Repeat(" ", cardPadLeft)}}
		for _, s := range l {
			if s.Fg == "" {
				s.Fg = th.Text
			}
			out = append(out, s)
		}
		if pad := width - out.Width(); pad > 0 {
			out = append(out, render.Span{Text: strings.Repeat(" ", pad)})
		}
		return out.WithBg(th.BgInput).Markup()
	}
	rows := []string{row(nil)}
	for _, l := range content {
		if l == nil {
			rows = append(rows, row(nil))
			continue
		}
		var indent render.Line
		if len(l) > 1 && strings.TrimSpace(l[0].Text) == "" {
			indent, l = l[:1], l[1:]
		}
		for _, w := range render.Wrap(l, inner-indent.Width()) {
			rows = append(rows, row(append(append(render.Line{}, indent...), w...)))
		}
	}
	rows = append(rows, row(nil))
	return strings.Join(rows, "\n")
}
