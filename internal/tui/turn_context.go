package tui

import (
	"fmt"
	"math"
	"os"
	"os/user"
	"strings"

	"github.com/rivo/tview"
	"github.com/tjcoder-labs/cli/internal/auth"
)

// Per-turn TUI metadata ("turn context").
//
// When the user sends a message, buildTurnContext snapshots who is using
// the TUI, what the UI is showing, and how much room the transcript pane
// has, and renders it as a compact <tui_context> block. The runner attaches
// the block (tooling.WithTurnContext) to the most recent user message of
// each request it builds — never to stored history — so it is not persisted
// to session.json, never shown in the transcript, and older user turns are
// always replayed clean.
//
// Disable entirely with CODER_TURN_CONTEXT=0 (or "false"/"off"); omit the
// account email/name with CODER_TURN_CONTEXT_EMAIL=0.

const (
	// wordBudgetCharsPerWord is the average rendered width of one English
	// word including its trailing space.
	wordBudgetCharsPerWord = 6.0
	// wordBudgetReservedLines covers the assistant label + timestamp rows
	// rendered above every reply.
	wordBudgetReservedLines = 2
	// wordBudgetFill is the share of the remaining area prose is assumed
	// to fill; the rest absorbs ragged word-wrap, blank lines between
	// paragraphs, list markers, headings and code fences.
	wordBudgetFill = 0.8
	wordBudgetMin  = 40
	wordBudgetMax  = 1500
	// Fallback pane size when the layout has not been drawn yet.
	fallbackPaneCols  = 80
	fallbackPaneLines = 24
)

// WordBudget returns the maximum number of words a reply should use so it
// fits the transcript pane's visible area of cols × lines cells:
//
//	floor( (cols / 6) × (lines − 2) × 0.8 ), clamped to [40, 1500]
//
// e.g. 100 cols × 30 lines → 16.67 words/line × 28 lines × 0.8 = 373.
func WordBudget(cols, lines int) int {
	if cols <= 0 {
		cols = fallbackPaneCols
	}
	if lines <= 0 {
		lines = fallbackPaneLines
	}
	usable := lines - wordBudgetReservedLines
	if usable < 1 {
		usable = 1
	}
	n := int(math.Floor(float64(cols) / wordBudgetCharsPerWord * float64(usable) * wordBudgetFill))
	if n < wordBudgetMin {
		return wordBudgetMin
	}
	if n > wordBudgetMax {
		return wordBudgetMax
	}
	return n
}

// turnContextInfo is the snapshot rendered by formatTurnContext. It is a
// plain struct so the formatter is testable without a live tview screen.
type turnContextInfo struct {
	User            string
	Email           string
	Layout          string // split | fullscreen | about | modal
	RightPanel      string // activity | tasks | memories | articles | canvas | test
	CanvasFile      string // "path" or "path:L10-40" when RightPanel == canvas
	FocusedPane     string
	Cwd             string
	Cols            int // transcript inner width (columns)
	Lines           int // transcript visible height (lines)
	TranscriptLines int // wrapped lines currently in the transcript
	Attachments     []fileAttachment
}

// formatTurnContext renders info as the <tui_context> block.
func formatTurnContext(info turnContextInfo) string {
	var b strings.Builder
	line := func(k, v string) {
		v = strings.TrimSpace(strings.ReplaceAll(v, "\n", " "))
		if v != "" {
			fmt.Fprintf(&b, "%s: %s\n", k, v)
		}
	}
	b.WriteString("<tui_context>\n")
	line("note", "ephemeral client metadata for this turn only; not written by the user; do not quote it unless asked")
	line("user", info.User)
	line("email", info.Email)
	line("layout", info.Layout)
	line("right_panel", info.RightPanel)
	line("canvas_file", info.CanvasFile)
	line("focused_pane", info.FocusedPane)
	line("cwd", info.Cwd)
	cols, lines := info.Cols, info.Lines
	if cols > 0 {
		line("transcript_width_cols", fmt.Sprint(cols))
	}
	if lines > 0 {
		line("transcript_visible_lines", fmt.Sprint(lines))
	}
	line("transcript_line_count", fmt.Sprint(info.TranscriptLines))
	if cols <= 0 {
		cols = fallbackPaneCols
	}
	line("response_length", fmt.Sprintf("respond in fewer than %d words so the reply fits the transcript pane without scrolling; wrap tables and code blocks to at most %d columns", WordBudget(info.Cols, info.Lines), cols))
	for _, a := range info.Attachments {
		parts := []string{"path=" + a.Path, "size=" + humanBytes(a.Size)}
		if !a.Binary {
			parts = append(parts, fmt.Sprintf("lines=%d", a.Lines))
		}
		if a.Mime != "" {
			parts = append(parts, "mime="+a.Mime)
		}
		if a.Lang != "" {
			parts = append(parts, "lang="+a.Lang)
		}
		switch {
		case a.Binary:
			parts = append(parts, "binary=true (metadata only)")
		case a.Omitted:
			parts = append(parts, "omitted=true (total size cap)")
		case a.Truncated:
			parts = append(parts, fmt.Sprintf("truncated=true (first %s attached)", humanBytes(int64(len(a.Content)))))
		}
		line("attachment", strings.Join(parts, " "))
	}
	b.WriteString("</tui_context>")
	return b.String()
}

// envOff reports whether env var name is set to a disabling value.
func envOff(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "0", "false", "off", "no":
		return true
	}
	return false
}

func turnContextEnabled() bool { return !envOff("CODER_TURN_CONTEXT") }

// localUserName is the OS login name, used when the ergo profile has no
// display name.
func localUserName() string {
	if u, err := user.Current(); err == nil {
		if n := strings.TrimSpace(u.Name); n != "" {
			return n
		}
		if u.Username != "" {
			return u.Username
		}
	}
	return os.Getenv("USER")
}

// transcriptMetrics measures the transcript pane from the live tview
// layout: inner width/height exclude the body's border padding (and the
// panel label and gutters, which are separate primitives).
func (a *App) transcriptMetrics() (cols, lines, count int) {
	if a.transcript == nil {
		return 0, 0, 0
	}
	_, _, cols, lines = a.transcript.GetInnerRect()
	count = a.transcript.GetWrappedLineCount()
	if cols < 0 {
		cols = 0
	}
	if lines < 0 {
		lines = 0
	}
	return cols, lines, count
}

// uiLayoutState names the current left-column/right-column arrangement
// using the App's real state fields.
func (a *App) uiLayoutState() (layout, right, canvas string) {
	switch {
	case a.leftPane != nil:
		layout = "modal (a form/list pane replaces the transcript)"
	case a.aboutMode:
		layout = "about"
	case a.fullscreen:
		layout = "fullscreen (transcript only; right column hidden)"
	default:
		layout = "split (transcript + right column)"
	}
	right = a.activePanel
	if right == "" {
		right = "activity"
	}
	if right == "canvas" && a.canvasPath != "" {
		canvas = a.canvasPath
		if a.canvasStart > 0 {
			canvas = fmt.Sprintf("%s:L%d", canvas, a.canvasStart)
			if a.canvasEnd > a.canvasStart {
				canvas = fmt.Sprintf("%s-%d", canvas, a.canvasEnd)
			}
		}
	}
	return layout, right, canvas
}

// focusedPaneName maps the focused primitive to a pane name.
func (a *App) focusedPaneName() string {
	if a.tv == nil {
		return ""
	}
	f := a.tv.GetFocus()
	switch {
	case f == nil:
		return ""
	case a.input != nil && f == tview.Primitive(a.input):
		return "input"
	case a.transcript != nil && f == tview.Primitive(a.transcript):
		return "transcript"
	case a.tasksList != nil && f == tview.Primitive(a.tasksList):
		return "tasks"
	case a.memoriesList != nil && f == tview.Primitive(a.memoriesList):
		return "memories"
	case a.remindersList != nil && f == tview.Primitive(a.remindersList):
		return "reminders"
	case a.activity != nil && f == tview.Primitive(a.activity):
		return "activity"
	case a.reasoning != nil && f == tview.Primitive(a.reasoning):
		return "cognition"
	case a.leftPane != nil:
		return "modal"
	}
	return "other"
}

// buildTurnContext snapshots the UI for the message being sent. It must
// run on the UI goroutine (it reads live tview geometry). Returns "" when
// disabled via CODER_TURN_CONTEXT=0.
func (a *App) buildTurnContext(atts []fileAttachment) string {
	if !turnContextEnabled() {
		return ""
	}
	info := turnContextInfo{Cwd: a.workspaceRoot, Attachments: atts}
	if info.Cwd == "" {
		info.Cwd, _ = os.Getwd()
	}
	if !envOff("CODER_TURN_CONTEXT_EMAIL") {
		if id, ok := auth.LoadErgoIdentity(); ok {
			info.User = id.Name
			info.Email = id.Email
		}
	}
	if info.User == "" {
		info.User = localUserName()
	}
	info.Layout, info.RightPanel, info.CanvasFile = a.uiLayoutState()
	info.FocusedPane = a.focusedPaneName()
	info.Cols, info.Lines, info.TranscriptLines = a.transcriptMetrics()
	return formatTurnContext(info)
}
