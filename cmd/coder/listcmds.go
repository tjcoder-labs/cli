package main

// Non-interactive `coder reminders …` and `coder tasks …`. These never
// start the TUI, never resolve a provider and never start telemetry, so
// they work headless (cron, ssh, CI). Output is plain text when stdout
// is not a terminal or NO_COLOR is set, and on-scheme theme shades
// otherwise.

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rivo/uniseg"

	"github.com/tjcoder-labs/cli/internal/reminders"
	"github.com/tjcoder-labs/cli/internal/session"
	"github.com/tjcoder-labs/cli/internal/tasks"
	"github.com/tjcoder-labs/cli/internal/theme"
)

const remindersUsage = `usage: coder [--workspace-root DIR] reminders <command>

  list, ls [--json]      list reminders and schedules in the workspace
  show <id> [--json]     show one entry (id, or a unique suffix such as 60ef31)
  rm, delete <id>        remove an entry and its crontab line, if installed

Entries are created with /reminder in the TUI or the set_reminder tool and
stored in <workspace>/.ergo-cli-go/reminders.json.
`

const tasksUsage = `usage: coder [--workspace-root DIR] tasks <command>

  list, ls [--status S[,S]] [--json] [--session PATH]
                         list the session's tasks (todo, doing, blocked,
                         done, cancelled)
  show <id> [--json] [--session PATH]
                         show one task (id, or a unique suffix)

Tasks live in the workspace session (<workspace>/.ergo-cli-go/session.json),
the same file the TUI loads. --session takes that file, its .ergo-cli-go
directory, or a workspace directory.
`

// cliEnv is everything the list commands touch, injectable for tests.
type cliEnv struct {
	stdout, stderr io.Writer
	color          bool
	now            time.Time
	cron           reminders.CrontabRunner
}

func defaultCLIEnv() cliEnv {
	return cliEnv{
		stdout: os.Stdout,
		stderr: os.Stderr,
		color:  colorEnabled(os.Stdout),
		now:    time.Now(),
		cron:   reminders.SystemCrontab{},
	}
}

// colorEnabled: stdout is a character device, NO_COLOR is unset or
// empty (https://no-color.org), and TERM is not "dumb".
func colorEnabled(f *os.File) bool {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return false
	}
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// ---- styling ----

type style struct {
	hex  string
	bold bool
}

func (e cliEnv) paint(s style, text string) string {
	if !e.color || text == "" || (s.hex == "" && !s.bold) {
		return text
	}
	var b strings.Builder
	b.WriteString("\x1b[")
	if s.bold {
		b.WriteString("1")
		if s.hex != "" {
			b.WriteString(";")
		}
	}
	if s.hex != "" {
		r, g, bl := theme.RGB(s.hex)
		fmt.Fprintf(&b, "38;2;%d;%d;%d", r, g, bl)
	}
	b.WriteString("m")
	b.WriteString(text)
	b.WriteString("\x1b[0m")
	return b.String()
}

type cell struct {
	text string
	st   style
}

func width(s string) int { return uniseg.StringWidth(s) }

// truncate shortens s to at most n columns, ending in "…" when cut.
// Newlines collapse to spaces so a table row stays one line.
func truncate(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if width(s) <= n {
		return s
	}
	var b strings.Builder
	w := 0
	g := uniseg.NewGraphemes(s)
	for g.Next() {
		cw := g.Width()
		if w+cw > n-1 {
			break
		}
		b.WriteString(g.Str())
		w += cw
	}
	return b.String() + "…"
}

// table writes left-aligned columns separated by two spaces. Padding is
// computed on the plain text so colour codes never skew alignment, and
// the last column is never padded (no trailing whitespace).
func (e cliEnv) table(headers []string, rows [][]cell) {
	th := theme.Current()
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = width(h)
	}
	for _, r := range rows {
		for i, c := range r {
			if w := width(c.text); w > widths[i] {
				widths[i] = w
			}
		}
	}
	line := func(cells []cell) {
		var b strings.Builder
		for i, c := range cells {
			b.WriteString(e.paint(c.st, c.text))
			if i < len(cells)-1 {
				b.WriteString(strings.Repeat(" ", widths[i]-width(c.text)+2))
			}
		}
		fmt.Fprintln(e.stdout, b.String())
	}
	hdr := make([]cell, len(headers))
	for i, h := range headers {
		hdr[i] = cell{h, style{th.Primary, true}}
	}
	line(hdr)
	for _, r := range rows {
		line(r)
	}
}

func (e cliEnv) kv(pairs [][2]string) {
	th := theme.Current()
	w := 0
	for _, p := range pairs {
		if width(p[0]) > w {
			w = width(p[0])
		}
	}
	for _, p := range pairs {
		if p[1] == "" {
			continue
		}
		lines := strings.Split(p[1], "\n")
		fmt.Fprintf(e.stdout, "%s%s  %s\n", e.paint(style{th.Primary, true}, p[0]), strings.Repeat(" ", w-width(p[0])), lines[0])
		for _, l := range lines[1:] {
			fmt.Fprintf(e.stdout, "%s  %s\n", strings.Repeat(" ", w), l)
		}
	}
}

func (e cliEnv) ok(msg string) {
	fmt.Fprintln(e.stdout, e.paint(style{theme.Current().Bright, true}, "✓ "+msg))
}

func (e cliEnv) warn(msg string) {
	fmt.Fprintln(e.stderr, e.paint(style{theme.Current().Primary, true}, "! "+msg))
}

func (e cliEnv) fail(msg string) int {
	fmt.Fprintln(e.stderr, e.paint(style{theme.Current().Bright, true}, "✗ "+msg))
	return 1
}

func (e cliEnv) dim(s string) string { return e.paint(style{theme.Current().Dim, false}, s) }

// ---- shared helpers ----

const (
	shortTime = "Jan 2 15:04"
	longTime  = "Mon Jan 2 15:04 MST"
)

func localTime(rfc string, layout string) string {
	if rfc == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339, rfc)
	if err != nil {
		return rfc
	}
	return t.Local().Format(layout)
}

// parseSubFlags parses fs from args while allowing flags after
// positional arguments (`show <id> --json`). It returns the positionals.
func parseSubFlags(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			return pos, nil
		}
		pos = append(pos, args[0])
		args = args[1:]
	}
}

// resolveID finds an exact id, or a unique suffix/prefix ("…60ef31",
// "60ef31", "rem-946"), among ids.
func resolveID(ids []string, arg string) (string, error) {
	arg = strings.TrimPrefix(strings.TrimSpace(arg), "…")
	if arg == "" {
		return "", errors.New("id is required")
	}
	var matches []string
	for _, id := range ids {
		if id == arg {
			return id, nil
		}
		if strings.HasSuffix(id, arg) || strings.HasPrefix(id, arg) {
			matches = append(matches, id)
		}
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no entry with id %q", arg)
	case 1:
		return matches[0], nil
	}
	sort.Strings(matches)
	return "", fmt.Errorf("id %q is ambiguous: %s", arg, strings.Join(matches, ", "))
}

func writeJSON(w io.Writer, v any) int {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return 1
	}
	return 0
}

// ---- coder reminders ----

func cmdReminders(args []string, workspaceRoot string) {
	os.Exit(runReminders(args, workspaceRoot, defaultCLIEnv()))
}

// reminderView is the --json shape: the stored entry plus derived fields.
type reminderView struct {
	reminders.Entry
	Kind        string `json:"kind"`
	Description string `json:"description"`
	NextRun     string `json:"next_run,omitempty"` // RFC3339, local offset
	Missed      bool   `json:"missed,omitempty"`   // one-shot whose time passed unfired
}

func viewReminder(e reminders.Entry, now time.Time) reminderView {
	v := reminderView{Entry: e, Kind: e.Kind(), Description: reminders.Describe(e.CronExpr, e.OneShot)}
	next, err := reminders.NextRun(e.CronExpr, now)
	if err != nil {
		return v
	}
	if e.OneShot {
		// A one-shot's moment is its first occurrence after creation; if
		// that is already behind us it was never fired (nothing was
		// running) and the cron expression would only recur next year.
		if c, err := time.Parse(time.RFC3339, e.CreatedAt); err == nil {
			if first, err := reminders.NextRun(e.CronExpr, c.In(now.Location())); err == nil && !first.After(now) {
				v.Missed = true
				return v
			}
		}
	}
	v.NextRun = next.Format(time.RFC3339)
	return v
}

func (e cliEnv) nextRunText(v reminderView, layout string) string {
	switch {
	case v.Missed:
		return "missed"
	case v.NextRun == "":
		return "—"
	}
	t, _ := time.Parse(time.RFC3339, v.NextRun)
	return t.In(e.now.Location()).Format(layout)
}

func runReminders(args []string, workspaceRoot string, env cliEnv) int {
	sub := "list"
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	fs := flag.NewFlagSet("coder reminders "+sub, flag.ContinueOnError)
	fs.SetOutput(env.stderr)
	fs.Usage = func() { fmt.Fprint(env.stderr, remindersUsage) }
	asJSON := fs.Bool("json", false, "machine-readable output")
	fs.StringVar(&workspaceRoot, "workspace-root", workspaceRoot, "workspace root")

	switch sub {
	case "help", "-h", "--help":
		fmt.Fprint(env.stdout, remindersUsage)
		return 0
	case "list", "ls", "show", "rm", "delete", "remove":
	default:
		fmt.Fprintf(env.stderr, "coder reminders: unknown command %q\n\n%s", sub, remindersUsage)
		return 2
	}
	pos, err := parseSubFlags(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	store := reminders.NewStore()
	list, err := store.Load(workspaceRoot)
	if err != nil {
		return env.fail(fmt.Sprintf("load %s: %v", reminders.FilePath(workspaceRoot), err))
	}

	switch sub {
	case "list", "ls":
		if len(pos) != 0 {
			fmt.Fprint(env.stderr, remindersUsage)
			return 2
		}
		entries := list.All()
		views := make([]reminderView, 0, len(entries))
		for _, e := range entries {
			views = append(views, viewReminder(e, env.now))
		}
		// Soonest first; missed and unparseable entries last.
		sort.SliceStable(views, func(i, j int) bool {
			a, b := views[i].NextRun, views[j].NextRun
			if (a == "") != (b == "") {
				return b == ""
			}
			ta, _ := time.Parse(time.RFC3339, a)
			tb, _ := time.Parse(time.RFC3339, b)
			return ta.Before(tb)
		})
		if *asJSON {
			return writeJSON(env.stdout, views)
		}
		if len(views) == 0 {
			fmt.Fprintf(env.stdout, "No reminders in %s. Create one with /reminder in the TUI.\n", workspaceRoot)
			return 0
		}
		th := theme.Current()
		rows := make([][]cell, 0, len(views))
		for _, v := range views {
			last := localTime(v.LastRun, shortTime)
			if last == "" {
				last = "never"
			}
			installed := "no"
			if v.Installed {
				installed = "yes"
			}
			text := v.Message
			if v.Prompt != "" {
				text = v.Prompt
			}
			next := cell{env.nextRunText(v, longTime), style{th.Text, false}}
			if v.Missed {
				next.st = style{th.Bright, true}
			}
			rows = append(rows, []cell{
				{v.ID, style{th.Deep, false}},
				{v.CronExpr, style{th.Soft, false}},
				{v.Description, style{th.Text, false}},
				next,
				{v.Kind, style{th.Primary, false}},
				{installed, style{th.Dim, false}},
				{last, style{th.Dim, false}},
				{truncate(text, 48), style{th.Text, false}},
			})
		}
		env.table([]string{"ID", "CRON", "SCHEDULE", "NEXT RUN", "KIND", "CRONTAB", "LAST RUN", "MESSAGE"}, rows)
		return 0

	case "show":
		if len(pos) != 1 {
			fmt.Fprint(env.stderr, remindersUsage)
			return 2
		}
		id, err := resolveID(list.IDs(), pos[0])
		if err != nil {
			return env.fail(err.Error())
		}
		e, _ := list.Get(id)
		v := viewReminder(e, env.now)
		if *asJSON {
			return writeJSON(env.stdout, v)
		}
		crontab := "not installed"
		if e.Installed {
			crontab = "installed"
			if env.cron != nil {
				switch content, err := env.cron.Read(); {
				case errors.Is(err, reminders.ErrNoCrontab):
					crontab += " · crontab not available here"
				case err != nil:
					crontab += " · could not read crontab: " + err.Error()
				case reminders.HasCronLine(content, e, workspaceRoot):
					crontab += " · line present"
				default:
					crontab += " · line missing from crontab"
				}
			}
		}
		last := localTime(e.LastRun, longTime)
		if last == "" {
			last = "never"
		}
		oneShot := "no"
		if e.OneShot {
			oneShot = "yes (removed after it fires)"
		}
		daily := ""
		if e.DailyCap > 0 {
			daily = fmt.Sprintf("%d per day", e.DailyCap)
		}
		env.kv([][2]string{
			{"ID", e.ID},
			{"Kind", v.Kind},
			{"Schedule", v.Description + "  " + env.dim("("+e.CronExpr+")")},
			{"Next run", env.nextRunText(v, longTime)},
			{"One-shot", oneShot},
			{"Crontab", crontab},
			{"Last run", last},
			{"Created", localTime(e.CreatedAt, longTime)},
			{"Agent", e.Agent},
			{"Platform", e.Platform},
			{"Daily cap", daily},
			{"Prompt", e.Prompt},
			{"Message", e.Message},
		})
		return 0

	default: // rm, delete, remove
		if len(pos) != 1 {
			fmt.Fprint(env.stderr, remindersUsage)
			return 2
		}
		id, err := resolveID(list.IDs(), pos[0])
		if err != nil {
			return env.fail(err.Error())
		}
		res, found, err := store.Remove(workspaceRoot, id, env.cron)
		if err != nil {
			return env.fail(fmt.Sprintf("remove %s: %v", id, err))
		}
		if !found {
			return env.fail(fmt.Sprintf("no entry with id %q", id))
		}
		e := res.Entry
		msg := fmt.Sprintf("removed %s %s (%s)", e.Kind(), e.ID, reminders.Describe(e.CronExpr, e.OneShot))
		switch {
		case !e.Installed:
		case errors.Is(res.CronErr, reminders.ErrNoCrontab):
			msg += " · crontab not available, skipped"
		case res.CronErr != nil:
			env.ok(msg)
			env.warn("could not update crontab: " + res.CronErr.Error() + " (remove the `schedule run " + e.ID + "` line by hand)")
			return 0
		case res.CronRemoved > 0:
			msg += " · crontab line removed"
		default:
			msg += " · no crontab line found"
		}
		env.ok(msg)
		return 0
	}
}

// ---- coder tasks ----

func cmdTasks(args []string, workspaceRoot string) {
	os.Exit(runTasks(args, workspaceRoot, defaultCLIEnv()))
}

// resolveSessionPath maps --session (a session.json file, its
// .ergo-cli-go directory, or a workspace directory) to a file path.
// Sessions are one per workspace and are not indexed anywhere, so
// there is no session id to look up.
func resolveSessionPath(arg, workspaceRoot string) (string, error) {
	if arg == "" {
		return session.Path(workspaceRoot), nil
	}
	if strings.HasPrefix(arg, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			arg = filepath.Join(home, arg[2:])
		}
	}
	fi, err := os.Stat(arg)
	if err != nil {
		return "", fmt.Errorf("--session %s: %w", arg, err)
	}
	if !fi.IsDir() {
		return arg, nil
	}
	for _, p := range []string{filepath.Join(arg, session.FileName), session.Path(arg)} {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("--session %s: no %s in that directory or in %s/", arg, session.FileName, session.DirName)
}

func taskStyle(status string) style {
	th := theme.Current()
	switch tasks.NormalizeStatus(status) {
	case tasks.StatusDoing:
		return style{th.Primary, true}
	case tasks.StatusBlocked:
		return style{th.Bright, true}
	case tasks.StatusDone, tasks.StatusCancelled:
		return style{th.Faint, false}
	}
	return style{th.Dim, false}
}

func runTasks(args []string, workspaceRoot string, env cliEnv) int {
	sub := "list"
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	fs := flag.NewFlagSet("coder tasks "+sub, flag.ContinueOnError)
	fs.SetOutput(env.stderr)
	fs.Usage = func() { fmt.Fprint(env.stderr, tasksUsage) }
	asJSON := fs.Bool("json", false, "machine-readable output")
	sessionArg := fs.String("session", "", "session.json path, its .ergo-cli-go dir, or a workspace dir")
	statusArg := fs.String("status", "", "comma-separated statuses: todo,doing,blocked,done,cancelled")
	all := fs.Bool("all", false, "(not supported: sessions are per workspace and not indexed)")
	fs.StringVar(&workspaceRoot, "workspace-root", workspaceRoot, "workspace root")

	switch sub {
	case "help", "-h", "--help":
		fmt.Fprint(env.stdout, tasksUsage)
		return 0
	case "list", "ls", "show":
	default:
		fmt.Fprintf(env.stderr, "coder tasks: unknown command %q\n\n%s", sub, tasksUsage)
		return 2
	}
	pos, err := parseSubFlags(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *all {
		env.fail("--all is not supported: each workspace keeps its own session in <workspace>/" +
			session.DirName + "/" + session.FileName + " and they are not indexed; pass --workspace-root or --session")
		return 2
	}
	want := map[tasks.Status]bool{}
	for _, s := range strings.Split(*statusArg, ",") {
		if strings.TrimSpace(s) == "" {
			continue
		}
		if !tasks.ValidStatus(s) {
			fmt.Fprintf(env.stderr, "coder tasks: unknown status %q (want todo, doing, blocked, done or cancelled)\n", strings.TrimSpace(s))
			return 2
		}
		want[tasks.NormalizeStatus(s)] = true
	}

	path, err := resolveSessionPath(*sessionArg, workspaceRoot)
	if err != nil {
		return env.fail(err.Error())
	}
	state, exists, err := session.LoadFile(path)
	if err != nil {
		return env.fail(err.Error())
	}
	list := tasks.Load(state)

	switch sub {
	case "show":
		if len(pos) != 1 {
			fmt.Fprint(env.stderr, tasksUsage)
			return 2
		}
		id, err := resolveID(list.IDs(), pos[0])
		if err != nil {
			return env.fail(err.Error())
		}
		t, _ := list.Get(id)
		if *asJSON {
			return writeJSON(env.stdout, t)
		}
		var meta string
		if len(t.Meta) > 0 {
			b, _ := json.Marshal(t.Meta)
			meta = string(b)
		}
		env.kv([][2]string{
			{"ID", t.ID},
			{"Status", env.paint(taskStyle(t.Status), tasks.Glyph(t.Status)) + " " + t.Status},
			{"Title", t.Title},
			{"Owner", t.Owner},
			{"Priority", t.Priority},
			{"Due", localTime(t.Due, longTime)},
			{"Created", localTime(t.CreatedAt, longTime)},
			{"Updated", localTime(t.UpdatedAt, longTime)},
			{"Meta", meta},
		})
		return 0
	}

	if len(pos) != 0 {
		fmt.Fprint(env.stderr, tasksUsage)
		return 2
	}
	shown := make([]tasks.Task, 0, list.Len())
	for _, t := range list.All() {
		if len(want) == 0 || want[tasks.NormalizeStatus(t.Status)] {
			shown = append(shown, t)
		}
	}
	if *asJSON {
		return writeJSON(env.stdout, shown)
	}
	if len(shown) == 0 {
		switch {
		case !exists:
			fmt.Fprintf(env.stdout, "No session at %s yet, so no tasks.\n", path)
		case len(want) > 0 && list.Len() > 0:
			fmt.Fprintf(env.stdout, "No %s tasks (%d in total) in %s.\n", strings.ReplaceAll(*statusArg, ",", "/"), list.Len(), path)
		default:
			fmt.Fprintf(env.stdout, "No tasks in %s.\n", path)
		}
		return 0
	}
	th := theme.Current()
	rows := make([][]cell, 0, len(shown))
	for _, t := range shown {
		st := taskStyle(t.Status)
		title := style{th.Text, false}
		if s := tasks.NormalizeStatus(t.Status); s == tasks.StatusDone || s == tasks.StatusCancelled {
			title = style{th.Dim, false}
		}
		due := localTime(t.Due, shortTime)
		dueSt := style{th.Text, false}
		if due == "" {
			due, dueSt = "—", style{th.Faint, false}
		}
		if d, err := time.Parse(time.RFC3339, t.Due); err == nil && d.Before(env.now) {
			if s := tasks.NormalizeStatus(t.Status); s != tasks.StatusDone && s != tasks.StatusCancelled {
				due += " !"
				dueSt = style{th.Bright, true}
			}
		}
		rows = append(rows, []cell{
			{t.ID, style{th.Deep, false}},
			{tasks.Glyph(t.Status) + " " + t.Status, st},
			{truncate(t.Title, 60), title},
			{due, dueSt},
			{localTime(t.CreatedAt, shortTime), style{th.Dim, false}},
		})
	}
	env.table([]string{"ID", "STATUS", "TITLE", "DUE", "CREATED"}, rows)
	return 0
}
