package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"github.com/tjcoder-labs/cli/internal/workspace"
)

// localWorkspaceRoots maps the lower-cased names of added local
// workspaces to their roots (for "@name:path" attachments).
func (a *App) localWorkspaceRoots() map[string]string {
	if a.workspaces == nil {
		return nil
	}
	out := map[string]string{}
	for _, w := range a.workspaces.Extra() {
		if w.Kind == workspace.KindLocal {
			out[strings.ToLower(w.Name)] = w.Root
		}
	}
	return out
}

// workspaceFileIndex returns the cached picker index, rebuilding it when
// older than fileIndexTTL.
func (a *App) workspaceFileIndex() []string {
	if a.fileIndex != nil && time.Since(a.fileIndexAt) < fileIndexTTL {
		return a.fileIndex
	}
	if a.workspaceRoot == "" {
		return nil
	}
	a.fileIndex = listWorkspaceFiles(a.workspaceRoot)
	if a.fileIndex == nil {
		a.fileIndex = []string{}
	}
	// Added local workspaces join the picker as "name:rel/path" (the
	// same form the tools accept). SSH workspaces are left out: a
	// remote walk per keystroke-driven refresh would be too slow.
	if a.workspaces != nil {
		for _, w := range a.workspaces.Extra() {
			if w.Kind != workspace.KindLocal {
				continue
			}
			for _, f := range listWorkspaceFiles(w.Root) {
				a.fileIndex = append(a.fileIndex, w.Name+":"+f)
			}
		}
	}
	a.fileIndexAt = time.Now()
	return a.fileIndex
}

// updateReferenceHint switches the hint line into the "@" file palette
// when the input ends in an "@query" token. Returns false (leaving the
// hint state for the caller) when no mention is being typed or the user
// dismissed this token with Esc.
func (a *App) updateReferenceHint(text string) bool {
	q, ok := activeMentionToken(text)
	if !ok {
		a.refDismissed = ""
		return false
	}
	if a.refDismissed != "" && a.refDismissed == q {
		return false
	}
	a.refDismissed = ""
	a.hintMode = "reference"
	a.hintMatches = nil
	a.refMatches = filterFileMatches(a.workspaceFileIndex(), q, fileMatchCap)
	if a.hintSelected >= len(a.refMatches) {
		a.hintSelected = 0
	}
	return true
}

// handleReferenceKey implements picker navigation while the "@" palette is
// open. handled=false lets the normal input capture run.
func (a *App) handleReferenceKey(event *tcell.EventKey) (*tcell.EventKey, bool) {
	if a.hintMode != "reference" {
		return event, false
	}
	switch event.Key() {
	case tcell.KeyEscape:
		if q, ok := activeMentionToken(a.input.GetText()); ok {
			a.refDismissed = q
		}
		a.hintMode = ""
		a.refMatches = nil
		a.hintSelected = 0
		a.refreshContextBar()
		return nil, true
	}
	if len(a.refMatches) == 0 {
		return event, false
	}
	switch event.Key() {
	case tcell.KeyDown:
		a.hintSelected = (a.hintSelected + 1) % len(a.refMatches)
		a.refreshContextBar()
		return nil, true
	case tcell.KeyUp, tcell.KeyBacktab:
		a.hintSelected = (a.hintSelected - 1 + len(a.refMatches)) % len(a.refMatches)
		a.refreshContextBar()
		return nil, true
	case tcell.KeyTab, tcell.KeyEnter:
		a.selectReference(a.refMatches[a.hintSelected])
		return nil, true
	}
	return event, false
}

// selectReference completes the trailing "@query" token to "@path ".
// The attachment itself is resolved from the final text at send time, so
// deleting the mention before sending also drops the attachment.
func (a *App) selectReference(rel string) {
	text := a.input.GetText()
	if _, ok := activeMentionToken(text); !ok {
		return
	}
	a.input.SetText(replaceMentionToken(text, rel))
	a.hintSelected = 0
}

// renderReferencePalette renders the "@" file matches as one horizontally
// windowed line, mirroring renderCommandPalette.
func (a *App) renderReferencePalette() string {
	if len(a.refMatches) == 0 {
		return fmt.Sprintf(" [%s]@[-] [%s]no matching files[-] ", a.palette.HexPurple, a.palette.HexFaint)
	}
	labels := make([]string, len(a.refMatches))
	for i, m := range a.refMatches {
		labels[i] = "@" + m
	}
	const sep = "  "
	budget := a.paletteWidth() - 4
	if budget < 8 {
		budget = 8
	}
	sel := a.hintSelected
	if sel >= len(labels) {
		sel = 0
	}
	start, end := sel, sel+1
	used := len([]rune(labels[sel]))
	for {
		grew := false
		if end < len(labels) {
			if cost := len(sep) + len([]rune(labels[end])); used+cost <= budget {
				used += cost
				end++
				grew = true
			}
		}
		if start > 0 {
			if cost := len(sep) + len([]rune(labels[start-1])); used+cost <= budget {
				used += cost
				start--
				grew = true
			}
		}
		if !grew {
			break
		}
	}
	var b strings.Builder
	b.WriteString(" ")
	if start > 0 {
		fmt.Fprintf(&b, "[%s]‹[-] ", a.palette.HexPurple)
	}
	for i := start; i < end; i++ {
		if i > start {
			b.WriteString(sep)
		}
		if i == sel {
			fmt.Fprintf(&b, "[%s::b]%s[-:-:-]", a.palette.HexLavender, tview.Escape(labels[i]))
		} else {
			fmt.Fprintf(&b, "[%s]%s[-]", a.palette.HexPurple, tview.Escape(labels[i]))
		}
	}
	if end < len(labels) {
		fmt.Fprintf(&b, " [%s]›[-]", a.palette.HexPurple)
	}
	hint := fmt.Sprintf("%d/%d  ↑↓ move · Tab/Enter attach · Esc cancel", sel+1, len(labels))
	if used+len([]rune(hint))+3 <= budget {
		fmt.Fprintf(&b, "   [%s]%s[-]", a.palette.HexFaint, hint)
	}
	b.WriteString(" ")
	return b.String()
}

// renderUserTurn renders a user message bubble with any attached files
// shown as compact chips underneath (contents are never dumped into the
// transcript).
func (a *App) renderUserTurn(content string) string {
	text, chips := splitAttachments(content)
	if strings.TrimSpace(text) == "" && len(chips) > 0 {
		text = "(attached files)"
	}
	out := a.renderUserMessage(text)
	if len(chips) == 0 {
		return out
	}
	var b strings.Builder
	// Chips sit directly under the bubble; the bubble's trailing blank
	// line (after its style reset) moves below the chips.
	b.WriteString(strings.TrimSuffix(out, "\n"))
	for _, c := range chips {
		detail := fmt.Sprintf("%d lines", c.Lines)
		if c.Binary {
			detail = "binary, " + humanBytes(c.Size)
		}
		fmt.Fprintf(&b, "[%s]📎[-] [%s]%s[-] [%s](%s)[-]\n",
			a.palette.HexPurple, a.palette.HexLavender, tview.Escape(c.Path), a.palette.HexDim, detail)
	}
	b.WriteString("\n")
	return b.String()
}
