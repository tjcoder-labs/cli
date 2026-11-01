package backgroundjobs

import (
	"fmt"
	"strings"
	"time"

	"github.com/tjcoder-labs/cli/internal/session"
)

// FormatPromptBlock renders a compact status table of background jobs
// for injection into the system prompt. Jobs are grouped by status
// (running first, then others). Output is deterministic and capped.
// Empty list returns empty string.
func FormatPromptBlock(jobs []session.BackgroundJob, maxLines int) string {
	if len(jobs) == 0 {
		return ""
	}
	if maxLines <= 0 {
		maxLines = 20
	}

	running := make([]session.BackgroundJob, 0)
	other := make([]session.BackgroundJob, 0)
	for _, j := range jobs {
		switch j.Status {
		case "running":
			running = append(running, j)
		default:
			other = append(other, j)
		}
	}

	var b strings.Builder
	b.WriteString("[background_jobs]\n")
	count := 0
	for _, j := range append(running, other...) {
		if count >= maxLines {
			break
		}
		line := formatJobLine(j)
		if line != "" {
			b.WriteString(line)
			b.WriteString("\n")
			count++
		}
	}
	if count == 0 {
		return ""
	}
	return b.String()
}

func formatJobLine(j session.BackgroundJob) string {
	if j.ID == "" {
		return ""
	}
	elapsed := ""
	if j.StartedAt != "" {
		if t, err := time.Parse(time.RFC3339, j.StartedAt); err == nil {
			d := time.Since(t)
			switch {
			case d < time.Minute:
				elapsed = fmt.Sprintf(" %ds", int(d.Seconds()))
			case d < time.Hour:
				elapsed = fmt.Sprintf(" %dm", int(d.Minutes()))
			default:
				elapsed = fmt.Sprintf(" %dh", int(d.Hours()))
			}
		}
	}
	cmd := j.Command
	if len(cmd) > 60 {
		cmd = cmd[:57] + "..."
	}
	status := j.Status
	if status == "" {
		status = "unknown"
	}
	return fmt.Sprintf("- %s [%s%s] %s", j.ID, status, elapsed, cmd)
}
