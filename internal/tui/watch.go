package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"github.com/tjcoder-labs/cli/internal/tools"
	"github.com/tjcoder-labs/cli/internal/watch"
	"github.com/tjcoder-labs/cli/internal/workspace"
)

// Watch canvas mode
//
// /watch is not a separate pane: it is a content mode of the existing
// canvas (the right-column surface ui_control panel=canvas drives). The
// canvas either renders a file (canvasModeFile, a.canvasPath) or a
// command's live output (canvasModeWatch). Both share the canvas's
// TextView (a.activity), full-height layout, CANVAS title row, Alt+C /
// /canvas toggle, Ctrl+E focus, Esc, theme and fullscreen handling;
// refreshCanvasPanel dispatches to renderWatch in watch mode.
//
// /watch <command> streams output, colourised; /watch -n <secs> <command>
// re-runs it like watch(1), swapping each run's output in at once. The
// agent opens the same canvas with ui_control panel=canvas + command.
// One watch at a time; starting another replaces it.
//
// The command runs on a PTY (unix) with FORCE_COLOR / CLICOLOR_FORCE set,
// in its own session and process group so Stop can end everything it
// spawned and it can never touch the TUI's terminal or a tty-scoped sudo
// ticket. It passes the same checks as run_command (tools.CheckWatchCommand:
// the disallowed-pattern list, and no sudo at all). Watches the agent starts
// (ui_control or invoke_cli_command) stop after watchAgentMaxDuration,
// matching background jobs.
//
// Output is kept in a watch.Buffer (5,000 lines) and redrawn at most every
// watchRedrawEvery, only when something changed and only while the canvas
// is showing the watch. Showing a file or another pane leaves the watch
// running; /watch (or Alt+C) brings it back, /watch stop ends it, and
// quitting the TUI kills it.

const (
	canvasModeFile  = ""
	canvasModeWatch = "watch"
	// watchMarkerPrefix starts the ui_control canvas marker for watch
	// mode: "panel:canvas:watch:<action>[:<interval>:<command>]".
	watchMarkerPrefix = "panel:canvas:watch:"
)

const (
	watchRedrawEvery      = 150 * time.Millisecond
	watchAgentMaxDuration = time.Hour
	watchMaxInterval      = 3600
)

type watchPane struct {
	sess      *watch.Session
	fromAgent bool
	follow    bool
	drawn     uint64
	queued    atomic.Bool
	stopTick  chan struct{}
}

// watchRequest is a parsed /watch invocation.
type watchRequest struct {
	sub      string // "", start, stop, close, restart, clear, status
	command  string
	interval int
	noPTY    bool
}

const watchUsage = "Usage: /watch [-n secs] [--no-pty] <command> · /watch stop|close|restart|clear|status · /watch toggles the pane"

// parseWatchArgs parses the text after "/watch". The command is taken
// verbatim from the original text (quoting and spacing preserved).
func parseWatchArgs(rest string) (watchRequest, error) {
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return watchRequest{}, nil
	}
	switch w := strings.ToLower(rest); w {
	case "stop", "close", "restart", "rerun", "clear", "status", "list":
		if w == "rerun" {
			w = "restart"
		}
		if w == "list" {
			w = "status"
		}
		return watchRequest{sub: w}, nil
	}
	req := watchRequest{sub: "start"}
	s := rest
	next := func() (string, string) {
		s = strings.TrimLeft(s, " \t")
		i := strings.IndexAny(s, " \t")
		if i < 0 {
			return s, ""
		}
		return s[:i], s[i:]
	}
	for {
		tok, after := next()
		switch {
		case tok == "--":
			s = after
			goto done
		case tok == "--no-pty":
			req.noPTY = true
			s = after
		case tok == "-n" || tok == "--interval":
			v, after2 := func() (string, string) { s = after; return next() }()
			n, err := strconv.Atoi(v)
			if err != nil {
				return req, fmt.Errorf("%s needs a number of seconds", tok)
			}
			req.interval = n
			s = after2
		case strings.HasPrefix(tok, "--interval="), strings.HasPrefix(tok, "-n") && len(tok) > 2 && tok[2] >= '0' && tok[2] <= '9':
			v := strings.TrimPrefix(strings.TrimPrefix(tok, "--interval="), "-n")
			n, err := strconv.Atoi(v)
			if err != nil {
				return req, fmt.Errorf("bad interval %q", v)
			}
			req.interval = n
			s = after
		default:
			goto done
		}
	}
done:
	req.command = strings.TrimSpace(s)
	if req.interval < 0 || req.interval > watchMaxInterval {
		return req, fmt.Errorf("interval must be 1-%d seconds", watchMaxInterval)
	}
	if req.command == "" {
		return req, fmt.Errorf("missing command. %s", watchUsage)
	}
	return req, nil
}

// slashRest is the text after the command word of a slash command.
func slashRest(cmd, word string) string {
	body := strings.TrimPrefix(strings.TrimSpace(cmd), "/")
	if len(body) >= len(word) && strings.EqualFold(body[:len(word)], word) {
		return strings.TrimSpace(body[len(word):])
	}
	return ""
}

// watchCanvasShowing reports whether the canvas is up in watch mode.
func (a *App) watchCanvasShowing() bool {
	return a.canvasMode == canvasModeWatch && panelKey(a.activePanel) == "canvas"
}

// openWatchCanvas switches the canvas to watch mode and shows it.
func (a *App) openWatchCanvas() {
	a.canvasMode = canvasModeWatch
	if panelKey(a.activePanel) == "canvas" {
		a.refreshCanvasPanel()
		return
	}
	a.showPanel("canvas")
}

// watchKey handles keys while the canvas has focus (Ctrl+E) in watch mode.
func (a *App) watchKey(ev *tcell.EventKey) *tcell.EventKey {
	switch ev.Key() {
	case tcell.KeyEscape:
		a.restoreDefaultFocus()
		return nil
	case tcell.KeyUp, tcell.KeyPgUp, tcell.KeyHome:
		a.watch.follow = false
		a.renderWatchTitle()
		return ev
	case tcell.KeyEnd:
		a.watch.follow = true
		a.renderWatchTitle()
		return ev
	case tcell.KeyRune:
		switch ev.Rune() {
		case 'f', 'F':
			a.watch.follow = !a.watch.follow
			if a.watch.follow {
				a.activity.ScrollToEnd()
			}
			a.renderWatchTitle()
			return nil
		case 'G':
			a.watch.follow = true
			a.activity.ScrollToEnd()
			a.renderWatchTitle()
			return nil
		case 'k', 'g':
			a.watch.follow = false
			a.renderWatchTitle()
			return ev
		case 's':
			a.stopWatch()
			return nil
		case 'r':
			a.restartWatch()
			return nil
		case 'c':
			if a.watch.sess != nil {
				a.watch.sess.Buffer().Reset()
				a.renderWatch()
			}
			return nil
		case 'q':
			a.showPanel("activity")
			return nil
		}
	}
	return ev
}

// handleWatchCommand runs /watch …; rest is the text after "/watch".
func (a *App) handleWatchCommand(rest string) {
	req, err := parseWatchArgs(rest)
	if err != nil {
		a.appendActivity(fmt.Sprintf("[%s::b]✗ watch[-::B] %s", a.palette.HexAlert, tview.Escape(err.Error())))
		return
	}
	switch req.sub {
	case "":
		if a.watch.sess == nil {
			a.appendActivity(tview.Escape(watchUsage))
			return
		}
		if a.watchCanvasShowing() {
			a.showPanel("activity")
		} else {
			a.openWatchCanvas()
		}
	case "stop":
		if !a.stopWatch() {
			a.appendActivity("No watch is running.")
		}
	case "close":
		a.stopWatch()
		if a.watchCanvasShowing() {
			a.showPanel("activity")
		}
	case "restart":
		a.restartWatch()
	case "clear":
		if a.watch.sess != nil {
			a.watch.sess.Buffer().Reset()
			a.renderWatch()
		}
	case "status":
		a.appendActivity(a.watchStatusLine())
	case "start":
		opts := watch.Options{Command: req.command, PTY: !req.noPTY, Interval: time.Duration(req.interval) * time.Second}
		if err := a.startWatch(opts, a.slashFromAgent); err != nil {
			a.appendActivity(fmt.Sprintf("[%s::b]✗ watch[-::B] %s", a.palette.HexAlert, tview.Escape(err.Error())))
		}
	}
}

// watchDir is the active local workspace root.
func (a *App) watchDir() (string, error) {
	if a.workspaces != nil {
		if ws := a.workspaces.Active(); ws != nil {
			if ws.Kind == workspace.KindSSH {
				return "", fmt.Errorf("watch runs locally; %s is an ssh workspace (use /use to pick a local one, or run_command for remote work)", ws.Name)
			}
			if ws.Root != "" {
				return ws.Root, nil
			}
		}
	}
	return a.workspaceRoot, nil
}

// startWatch validates and starts a watch, replacing any current one,
// and shows the pane. UI goroutine.
func (a *App) startWatch(opts watch.Options, fromAgent bool) error {
	if err := tools.CheckWatchCommand(opts.Command); err != nil {
		return err
	}
	dir, err := a.watchDir()
	if err != nil {
		return err
	}
	opts.Dir = dir
	opts.MaxLines = watch.DefaultMaxLines
	if fromAgent {
		opts.MaxDuration = watchAgentMaxDuration
	}
	if a.activity != nil {
		if _, _, w, h := a.activity.GetInnerRect(); w > 20 && h > 5 {
			opts.Cols, opts.Rows = uint16(w-3), uint16(h)
		}
	}
	a.stopWatchAsync()
	sess, err := watch.Start(opts, nil)
	if err != nil {
		return err
	}
	a.watch.sess = sess
	a.watch.fromAgent = fromAgent
	a.watch.follow = true
	a.watch.drawn = 0
	who := ""
	if fromAgent {
		who = " (agent)"
	}
	a.appendActivity(fmt.Sprintf("[%s]watch%s:[-] %s", a.palette.HexLavender, who, tview.Escape(opts.Command)))
	a.startWatchTicker(sess)
	a.openWatchCanvas()
	return nil
}

func (a *App) restartWatch() {
	if a.watch.sess == nil {
		a.appendActivity("No watch to restart. " + tview.Escape(watchUsage))
		return
	}
	opts := a.watch.sess.Options()
	if err := a.startWatch(opts, a.watch.fromAgent); err != nil {
		a.appendActivity(fmt.Sprintf("[%s::b]✗ watch[-::B] %s", a.palette.HexAlert, tview.Escape(err.Error())))
	}
}

// stopWatch ends the running watch, keeping its output on screen. It
// reports whether there was an active watch. Never blocks the UI.
func (a *App) stopWatch() bool {
	s := a.watch.sess
	if s == nil || !s.Status().Active {
		return false
	}
	go s.Stop()
	return true
}

// stopWatchAsync ends the current watch (if any) and detaches its ticker.
func (a *App) stopWatchAsync() {
	if a.watch.stopTick != nil {
		close(a.watch.stopTick)
		a.watch.stopTick = nil
	}
	if s := a.watch.sess; s != nil {
		go s.Stop()
	}
}

// shutdownWatch kills the watch synchronously; used when the TUI exits.
func (a *App) shutdownWatch() {
	if a.watch.stopTick != nil {
		close(a.watch.stopTick)
		a.watch.stopTick = nil
	}
	if s := a.watch.sess; s != nil {
		s.Stop()
	}
}

// startWatchTicker posts throttled redraws while sess lives, then a
// final one with an exit marker.
func (a *App) startWatchTicker(sess *watch.Session) {
	stop := make(chan struct{})
	a.watch.stopTick = stop
	post := func() {
		if !a.watch.queued.CompareAndSwap(false, true) {
			return
		}
		a.postUI(func() {
			a.watch.queued.Store(false)
			if a.watch.sess == sess {
				a.renderWatch()
			}
		})
	}
	go func() {
		t := time.NewTicker(watchRedrawEvery)
		defer t.Stop()
		var lastVer uint64
		var lastSec int64
		for {
			select {
			case <-stop:
				return
			case <-sess.Done():
				st := sess.Status()
				if st.Interval == 0 || st.Stopped {
					sess.Buffer().AppendNote("["+a.palette.HexDim+"]", "── "+watchEndText(st)+" ──")
				}
				post()
				return
			case now := <-t.C:
				v := sess.Buffer().Version()
				sec := now.Unix()
				if v != lastVer || sec != lastSec {
					lastVer, lastSec = v, sec
					post()
				}
			}
		}
	}()
}

func watchEndText(st watch.Status) string {
	el := watchElapsed(st)
	switch {
	case st.Stopped:
		return "stopped after " + el
	case st.Err != "":
		return "failed: " + st.Err
	default:
		return fmt.Sprintf("exit %d after %s", st.ExitCode, el)
	}
}

func watchElapsed(st watch.Status) string {
	end := st.Ended
	if end.IsZero() {
		end = time.Now()
	}
	d := end.Sub(st.Started)
	if d < 10*time.Second {
		return d.Round(100 * time.Millisecond).String()
	}
	return d.Round(time.Second).String()
}

func (a *App) watchStatusLine() string {
	s := a.watch.sess
	if s == nil {
		return "No watch. " + tview.Escape(watchUsage)
	}
	st := s.Status()
	state := "running"
	if !st.Active {
		state = watchEndText(st)
	}
	return fmt.Sprintf("watch: %s · %s · %s · %d lines", tview.Escape(st.Command), state, watchElapsed(st), s.Buffer().Len())
}

// renderWatch repaints the canvas title row and body when the canvas
// is showing the watch. UI goroutine.
func (a *App) renderWatch() {
	if a.activity == nil || !a.watchCanvasShowing() {
		return
	}
	a.renderWatchTitle()
	s := a.watch.sess
	if s == nil {
		a.watch.drawn = 0
		a.activity.SetText(fmt.Sprintf("[%s]No command is being watched.\n\n/watch <command>          stream output live (dev servers, logs, tests)\n/watch -n 2 <command>     re-run every 2 seconds, like watch(1)\n/watch --no-pty <cmd>     use pipes instead of a terminal\n\nCtrl+E focuses the canvas: f follow · s stop · r rerun · c clear · Esc back.\nThe agent opens one with ui_control (panel=canvas, command).[-]", a.palette.HexDim))
		return
	}
	if v := s.Buffer().Version(); v != a.watch.drawn {
		a.watch.drawn = v
		a.activity.SetText(s.Buffer().Text())
		if a.watch.follow {
			a.activity.ScrollToEnd()
		}
	}
}

// renderWatchTitle writes the canvas title row for watch mode: CANVAS,
// the watch state, the command and dim metadata.
func (a *App) renderWatchTitle() {
	if a.activityTitle == nil || !a.watchCanvasShowing() {
		return
	}
	p := a.palette
	s := a.watch.sess
	if s == nil {
		a.activityTitle.SetText(fmt.Sprintf(" [%s]CANVAS[-]  [%s]watch · /watch <command>[-]", p.HexPurple, p.HexDim))
		return
	}
	st := s.Status()
	var state string
	switch {
	case st.Running:
		state = fmt.Sprintf("[%s]● running[-]", p.HexLavender)
	case st.Active:
		state = fmt.Sprintf("[%s]◌ waiting[-]", p.HexDim)
	case st.Stopped:
		state = fmt.Sprintf("[%s]■ stopped[-]", p.HexDim)
	case st.Err != "" || st.ExitCode != 0:
		state = fmt.Sprintf("[%s::b]✗ exit %d[-::B]", p.HexAlert, st.ExitCode)
	default:
		state = fmt.Sprintf("[%s]✓ exit 0[-]", p.HexLavender)
	}
	cmd := st.Command
	if r := []rune(cmd); len(r) > 60 {
		cmd = string(r[:59]) + "…"
	}
	meta := []string{tildePath(st.Dir), watchElapsed(st)}
	if st.Interval > 0 {
		meta = append(meta, fmt.Sprintf("every %s · run %d", st.Interval, st.Run))
		if st.Active && !st.Running && st.ExitCode != 0 {
			meta = append(meta, fmt.Sprintf("last exit %d", st.ExitCode))
		}
	}
	if st.PTY {
		meta = append(meta, "pty")
	}
	if d := s.Buffer().Dropped(); d > 0 {
		meta = append(meta, fmt.Sprintf("%d older lines dropped", d))
	}
	if !a.watch.follow {
		meta = append(meta, "paused (f follows)")
	}
	a.activityTitle.SetText(fmt.Sprintf(" [%s]CANVAS[-]  [%s]watch[-] %s [%s]%s[-]  [%s]%s[-]",
		p.HexPurple, p.HexDim, state, p.HexMain, tview.Escape(cmd),
		p.HexDim, tview.Escape(strings.Join(meta, " · "))))
}

func tildePath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if rel, err := filepath.Rel(home, p); err == nil && !strings.HasPrefix(rel, "..") {
			if rel == "." {
				return "~"
			}
			return "~/" + rel
		}
	}
	return p
}

// handleWatchMarker applies a ui_control canvas watch marker:
// "panel:canvas:watch:<action>[:<interval>:<command>]".
func (a *App) handleWatchMarker(marker string) {
	parts := strings.SplitN(strings.TrimPrefix(marker, watchMarkerPrefix), ":", 3)
	action := strings.ToLower(parts[0])
	if len(parts) == 3 && strings.TrimSpace(parts[2]) != "" && (action == "show" || action == "toggle") {
		interval, _ := strconv.Atoi(parts[1])
		a.appendActivity(fmt.Sprintf("[%s]ui_control: canvas watch %s[-]", a.palette.HexLavender, tview.Escape(parts[2])))
		opts := watch.Options{Command: parts[2], PTY: true, Interval: time.Duration(interval) * time.Second}
		if err := a.startWatch(opts, true); err != nil {
			a.appendActivity(fmt.Sprintf("[%s::b]✗ watch[-::B] %s", a.palette.HexAlert, tview.Escape(err.Error())))
		}
		return
	}
	a.appendActivity(fmt.Sprintf("[%s]ui_control: canvas watch %s[-]", a.palette.HexLavender, tview.Escape(action)))
	switch action {
	case "show":
		a.openWatchCanvas()
	case "toggle":
		if a.watchCanvasShowing() {
			a.showPanel("activity")
		} else {
			a.openWatchCanvas()
		}
	case "hide":
		if a.watchCanvasShowing() {
			a.showPanel("activity")
		}
	case "stop":
		a.stopWatch()
	}
}

// watchArgHints offers /watch subcommands in the argument palette until
// the input stops matching one (then it is a free-form command).
func (a *App) watchArgHints(text string) (string, []wsArg, bool) {
	if !strings.HasPrefix(text, "/") || strings.Contains(text, "\n") {
		return "", nil, false
	}
	body := strings.TrimPrefix(text, "/")
	i := strings.IndexAny(body, " \t")
	if i == -1 || !strings.EqualFold(body[:i], "watch") {
		return "", nil, false
	}
	rest := strings.ToLower(strings.TrimLeft(body[i+1:], " \t"))
	if strings.ContainsAny(rest, " \t") {
		return "", nil, false
	}
	all := []wsArg{
		{label: "<command>", insert: "", desc: "stream a command's colorized output live"},
		{label: "-n <secs> <command>", insert: "-n 2 ", desc: "re-run every N seconds, like watch(1)"},
		{label: "stop", insert: "stop", desc: "end the running watch, keep its output"},
		{label: "restart", insert: "restart", desc: "run the same command again"},
		{label: "clear", insert: "clear", desc: "clear the scrollback"},
		{label: "close", insert: "close", desc: "stop the watch and close the pane"},
		{label: "status", insert: "status", desc: "log the watch's state to activity"},
	}
	var items []wsArg
	for _, it := range all {
		if rest == "" || (it.insert != "" && strings.HasPrefix(it.insert, rest)) {
			items = append(items, it)
		}
	}
	if len(items) == 0 {
		return "", nil, false
	}
	return "watch:", items, true
}
