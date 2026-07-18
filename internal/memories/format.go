package memories

import (
	"fmt"
	"strings"
)

// FormatPromptBlock renders the stored memories as a flat text block
// suitable for the model's system prompt. The output is deterministic
// and capped at maxLines entries; older/lower-priority items are dropped
// silently. maxLines <= 0 means "no cap".
func FormatPromptBlock(list *List, maxLines int) string {
	if list == nil || list.Len() == 0 {
		return ""
	}
	items := list.All()
	if len(items) == 0 {
		return ""
	}
	if maxLines > 0 && len(items) > maxLines {
		items = items[:maxLines]
	}
	var b strings.Builder
	b.WriteString("[memories]\n")
	for _, m := range items {
		fmt.Fprintf(&b, "- %s", m.Title)
		if m.Body != "" {
			fmt.Fprintf(&b, ": %s", strings.TrimSpace(m.Body))
		}
		if len(m.Tags) > 0 {
			fmt.Fprintf(&b, " [tags:%s]", strings.Join(m.Tags, ","))
		}
		b.WriteString("\n")
	}
	return b.String()
}
