package tasks

import (
	"fmt"
	"strings"
	"time"
)

// Glyph is the single-character status indicator used in the TUI.
// Picked to read well in monospaced terminals with the existing
// purple palette.
func Glyph(status string) string {
	switch NormalizeStatus(status) {
	case StatusTodo:
		return "○"
	case StatusDoing:
		return "◐"
	case StatusDone:
		return "●"
	case StatusBlocked:
		return "✕"
	case StatusCancelled:
		return "⊘"
	}
	return "·"
}

// GlyphFor returns the colored glyph for in-line rendering. It uses
// the TUI's hex color codes so callers can drop the result straight
// into a tview dynamic-color string.
func GlyphFor(status string) string {
	switch NormalizeStatus(status) {
	case StatusTodo:
		return "[#968CB2]○[-]"
	case StatusDoing:
		return "[#A77CF8]◐[-]"
	case StatusDone:
		return "[#564A70]●[-]"
	case StatusBlocked:
		return "[#C73CDC]✕[-]"
	case StatusCancelled:
		return "[#564A70]⊘[-]"
	}
	return "[#564A70]·[-]"
}

// FormatPromptBlock renders the open task list as a flat text block
// suitable for the model's system prompt. The output is deterministic
// and capped at maxLines entries; older/lower-priority items are
// dropped silently. maxLines <= 0 means "no cap".
func FormatPromptBlock(list *List, maxLines int) string {
	if list == nil || list.Len() == 0 {
		return ""
	}
	open := list.Open()
	if len(open) == 0 {
		return ""
	}
	if maxLines > 0 && len(open) > maxLines {
		open = open[:maxLines]
	}
	var b strings.Builder
	b.WriteString("[tasks]\n")
	for _, t := range open {
		fmt.Fprintf(&b, "- [%s] %s", t.Status, t.Title)
		if len(t.Meta) > 0 {
			if priority, ok := t.Meta["priority"].(string); ok && priority != "" {
				fmt.Fprintf(&b, " (priority=%s)", priority)
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}

// FormatContextBar renders the task list as a short string suitable
// for the existing context bar in the TUI. The output is a single
// line; if there are no tasks it returns the empty string. The line
// is colored to match the existing context bar palette and uses the
// status glyphs from GlyphFor so it sits naturally next to the cwd,
// spinner, and ref markers.
func FormatContextBar(list *List) string {
	if list == nil || list.Len() == 0 {
		return ""
	}
	open := list.Open()
	counts := map[Status]int{}
	for _, t := range list.All() {
		counts[Status(t.Status)]++
	}
	var b strings.Builder
	b.WriteString(GlyphFor(string(StatusDoing)))
	fmt.Fprintf(&b, "[#C4A5FF]%d[-]", counts[StatusDoing])
	if c := counts[StatusTodo]; c > 0 {
		b.WriteString(" ")
		b.WriteString(GlyphFor(string(StatusTodo)))
		fmt.Fprintf(&b, "[#968CB2]%d[-]", c)
	}
	if c := counts[StatusBlocked]; c > 0 {
		b.WriteString(" ")
		b.WriteString(GlyphFor(string(StatusBlocked)))
		fmt.Fprintf(&b, "[#C73CDC]%d[-]", c)
	}
	if done := counts[StatusDone] + counts[StatusCancelled]; done > 0 {
		b.WriteString(" ")
		fmt.Fprintf(&b, "[#564A70]✓%d[-]", done)
	}
	if len(open) > 0 {
		// Surface the most recent doing task as a one-line hint so the
		// user can see what the model is working on without expanding
		// the panel.
		for _, t := range open {
			if Status(t.Status) == StatusDoing {
				b.WriteString("  ")
				fmt.Fprintf(&b, "[#E8E2F5]%s[-]", truncate(t.Title, 40))
				break
			}
		}
	}
	return b.String()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "…"
}

// RelativeStamp returns a short human-readable timestamp for a task
// ("just now", "5m ago", "3h ago", "2d ago"). It's deliberately
// coarse — the full timestamp is in the data and is what gets
// persisted.
func RelativeStamp(updatedAt string) string {
	t, err := time.Parse(time.RFC3339, updatedAt)
	if err != nil {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
