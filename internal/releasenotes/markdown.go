package releasenotes

import (
	"fmt"
	"strings"
)

var categoryHeadings = map[string]string{
	"added":      "Added",
	"changed":    "Changed",
	"deprecated": "Deprecated",
	"removed":    "Removed",
	"fixed":      "Fixed",
	"security":   "Security",
}

// esc keeps text such as "/add <folder>" visible in rendered Markdown.
var esc = strings.NewReplacer("<", "&lt;", ">", "&gt;").Replace

// Markdown renders CHANGELOG.md. The output is deterministic so a test
// can compare it with the committed file.
func Markdown(n *Notes) string {
	var b strings.Builder
	b.WriteString("# Changelog\n\n")
	b.WriteString("<!-- Generated from release-notes/releases.json by `go run ./cmd/releasenotes`. Do not edit by hand. -->\n\n")
	b.WriteString("All notable changes to Coder CLI. The source of truth is [release-notes/releases.json](release-notes/releases.json).\n")
	for _, r := range n.Releases {
		b.WriteString("\n## ")
		if r.CoversFrom != "" {
			fmt.Fprintf(&b, "%s – %s", r.CoversFrom, r.Version)
		} else {
			b.WriteString(r.Version)
		}
		fmt.Fprintf(&b, " — %s\n\n", r.Date)
		fmt.Fprintf(&b, "**%s.** %s\n", esc(strings.TrimSuffix(r.Title, ".")), esc(r.Summary))
		if r.RequiresRestart || r.MinUpgradeFrom != "" {
			b.WriteString("\n")
			if r.RequiresRestart {
				b.WriteString("> Restart required after updating.\n")
			}
			if r.MinUpgradeFrom != "" {
				fmt.Fprintf(&b, "> Upgrade to %s first if you are on an older version.\n", r.MinUpgradeFrom)
			}
		}
		for _, c := range Categories {
			items := r.Changes[c]
			if len(items) == 0 {
				continue
			}
			fmt.Fprintf(&b, "\n### %s\n\n", categoryHeadings[c])
			for _, it := range items {
				b.WriteString("- ")
				if it.Breaking {
					b.WriteString("**Breaking:** ")
				}
				b.WriteString(esc(it.Text))
				if len(it.Commands) > 0 {
					cmds := make([]string, len(it.Commands))
					for i, c := range it.Commands {
						cmds[i] = "`" + c + "`"
					}
					fmt.Fprintf(&b, " (%s)", strings.Join(cmds, ", "))
				}
				b.WriteString("\n")
				if it.Details != "" {
					fmt.Fprintf(&b, "  %s\n", esc(it.Details))
				}
			}
		}
		if len(r.Notes) > 0 {
			b.WriteString("\n### Known limits\n\n")
			for _, note := range r.Notes {
				fmt.Fprintf(&b, "- %s\n", esc(note))
			}
		}
	}
	return b.String()
}
