package reminders

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// CrontabRunner reads and replaces the user's crontab. The default
// shells out to `crontab -l` / `crontab -`; tests inject a fake.
type CrontabRunner interface {
	// Read returns the current crontab. A user with no crontab yields
	// "" and a nil error.
	Read() (string, error)
	Write(content string) error
}

// ErrNoCrontab is returned when the crontab binary is not installed.
var ErrNoCrontab = fmt.Errorf("crontab is not available on this system")

// SystemCrontab is the exec-backed CrontabRunner.
type SystemCrontab struct{}

func (SystemCrontab) Read() (string, error) {
	if _, err := exec.LookPath("crontab"); err != nil {
		return "", ErrNoCrontab
	}
	out, err := exec.Command("crontab", "-l").Output()
	if err != nil {
		// `crontab -l` exits non-zero with "no crontab for <user>".
		if _, ok := err.(*exec.ExitError); ok && len(out) == 0 {
			return "", nil
		}
		return "", err
	}
	return string(out), nil
}

func (SystemCrontab) Write(content string) error {
	if _, err := exec.LookPath("crontab"); err != nil {
		return ErrNoCrontab
	}
	cmd := exec.Command("crontab", "-")
	cmd.Stdin = strings.NewReader(content)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("crontab -: %v %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func shQuote(s string) string { return strings.ReplaceAll(s, "'", "'\\''") }

// CronLine renders the crontab line for e. Schedules (Prompt set) and
// one-shot reminders re-invoke the binary (`<bin> schedule run <id>`)
// so the runner can execute/notify and then remove a one-shot entry
// together with this line. Plain recurring reminders keep the legacy
// echo-to-reminders.log line.
func CronLine(e Entry, bin, workspaceRoot string) (string, error) {
	dir := filepath.Join(workspaceRoot, ".ergo-cli-go")
	if e.Prompt != "" || e.OneShot {
		if bin == "" {
			return "", fmt.Errorf("resolve executable path: empty")
		}
		return fmt.Sprintf("%s cd '%s' && '%s' schedule run %s >> '%s/schedule.log' 2>&1",
			e.CronExpr, shQuote(workspaceRoot), bin, e.ID, dir), nil
	}
	if strings.ContainsAny(e.Message, "\n\r") {
		return "", fmt.Errorf("reminder message must not contain newlines when installing cron")
	}
	return fmt.Sprintf("%s echo '%s' >> %s/reminders.log", e.CronExpr, shQuote(e.Message), dir), nil
}

// InstallCronLine appends line to the crontab.
func InstallCronLine(r CrontabRunner, line string) error {
	existing, err := r.Read()
	if err != nil {
		return err
	}
	if existing != "" && !strings.HasSuffix(existing, "\n") {
		existing += "\n"
	}
	if err := r.Write(existing + line + "\n"); err != nil {
		return fmt.Errorf("failed to install crontab: %v", err)
	}
	return nil
}

// RemoveCronLines deletes the crontab line(s) belonging to e: any line
// that runs exactly `schedule run <id>` (followed by a space or end of
// line, so id "ab" never matches "abc"), or, for legacy passive
// reminders, the exact echo line CronLine would have written. Every
// other line is preserved byte-for-byte. It returns how many lines
// were removed; the crontab is only rewritten when that is > 0.
func RemoveCronLines(r CrontabRunner, e Entry, workspaceRoot string) (int, error) {
	content, err := r.Read()
	if err != nil {
		return 0, err
	}
	runRe := regexp.MustCompile(`(^|\s)schedule run ` + regexp.QuoteMeta(e.ID) + `(\s|$)`)
	echoLine := ""
	if e.Prompt == "" {
		pe := e
		pe.OneShot = false
		echoLine, _ = CronLine(pe, "", workspaceRoot)
	}
	lines := strings.Split(content, "\n")
	kept := make([]string, 0, len(lines))
	removed := 0
	for _, ln := range lines {
		trimmed := strings.TrimSpace(ln)
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") &&
			(runRe.MatchString(trimmed) || (echoLine != "" && trimmed == echoLine)) {
			removed++
			continue
		}
		kept = append(kept, ln)
	}
	if removed == 0 {
		return 0, nil
	}
	if err := r.Write(strings.Join(kept, "\n")); err != nil {
		return 0, err
	}
	return removed, nil
}
