package watch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Options configures one watch.
type Options struct {
	// Command is run through the platform shell (bash -lc / sh -c,
	// cmd /C on Windows), like run_command.
	Command string
	// Dir is the working directory.
	Dir string
	// Interval > 0 re-runs Command that long after each run finishes
	// (like watch -n), showing each run's output in one swap. Zero
	// streams a single run as it happens (like tail -f).
	Interval time.Duration
	// PTY runs the command on a pseudo-terminal so tools that only
	// colour a tty (ls, git, grep, …) still do. Ignored on Windows.
	PTY bool
	// Cols/Rows size the PTY (defaults 120x40).
	Cols, Rows uint16
	// MaxDuration stops the watch after this long (0 = no limit).
	MaxDuration time.Duration
	// MaxLines caps the scrollback (DefaultMaxLines when 0).
	MaxLines int
}

// Status is a snapshot of a watch.
type Status struct {
	Command  string
	Dir      string
	Interval time.Duration
	PTY      bool
	Running  bool // a process is alive right now
	Active   bool // the watch has not finished (interval mode idles between runs)
	Run      int  // 1-based run counter
	ExitCode int  // last finished run; -1 when killed or not started
	Stopped  bool // ended by Stop (or MaxDuration)
	Err      string
	Started  time.Time // start of the watch
	RunStart time.Time // start of the current/last run
	Ended    time.Time // end of the watch (zero while active)
	PID      int
}

// Session is one running watch.
type Session struct {
	opts Options
	buf  *Buffer

	mu      sync.Mutex
	st      Status
	proc    *os.Process
	stopped bool
	cancel  context.CancelFunc
	done    chan struct{}
}

// colorEnv nudges CLIs into colour even without a tty (pipe mode) and
// tells PTY-aware ones what the terminal can do.
var colorEnv = []string{
	"FORCE_COLOR=1",
	"CLICOLOR=1",
	"CLICOLOR_FORCE=1",
	"COLORTERM=truecolor",
	"TERM=xterm-256color",
	"GIT_PAGER=cat",
	"PAGER=cat",
}

// Environ is os.Environ with NO_COLOR removed and colorEnv applied.
func Environ() []string {
	keys := map[string]bool{"NO_COLOR": true}
	for _, kv := range colorEnv {
		keys[kv[:strings.IndexByte(kv, '=')]] = true
	}
	out := make([]string, 0, len(os.Environ())+len(colorEnv))
	for _, kv := range os.Environ() {
		if i := strings.IndexByte(kv, '='); i > 0 && keys[kv[:i]] {
			continue
		}
		out = append(out, kv)
	}
	return append(out, colorEnv...)
}

// Start launches the watch and returns once the first run has started
// (or failed to start). Output goes to buf.
func Start(opts Options, buf *Buffer) (*Session, error) {
	if strings.TrimSpace(opts.Command) == "" {
		return nil, errors.New("watch: empty command")
	}
	if opts.Cols == 0 {
		opts.Cols = 120
	}
	if opts.Rows == 0 {
		opts.Rows = 40
	}
	if buf == nil {
		buf = NewBuffer(opts.MaxLines)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if opts.MaxDuration > 0 {
		ctx, cancel = context.WithTimeout(context.Background(), opts.MaxDuration)
	}
	now := time.Now()
	s := &Session{
		opts:   opts,
		buf:    buf,
		cancel: cancel,
		done:   make(chan struct{}),
		st: Status{
			Command: opts.Command, Dir: opts.Dir, Interval: opts.Interval,
			PTY: opts.PTY && ptySupported, Active: true, ExitCode: -1, Started: now,
		},
	}
	first := make(chan error, 1)
	go s.loop(ctx, first)
	if err := <-first; err != nil {
		<-s.done
		return nil, err
	}
	return s, nil
}

// Buffer is the watch's scrollback.
func (s *Session) Buffer() *Buffer { return s.buf }

// Options returns the options the watch was started with.
func (s *Session) Options() Options { return s.opts }

// Done is closed when the watch has fully finished.
func (s *Session) Done() <-chan struct{} { return s.done }

// Status returns a snapshot.
func (s *Session) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.st
}

// Stop ends the watch: the running process group gets SIGTERM, then
// SIGKILL after a grace period. It blocks until the watch is done (at
// most a few seconds) and is safe to call more than once.
func (s *Session) Stop() {
	s.mu.Lock()
	s.stopped = true
	s.mu.Unlock()
	s.cancel()
	select {
	case <-s.done:
	case <-time.After(5 * time.Second):
	}
}

func (s *Session) loop(ctx context.Context, first chan<- error) {
	defer close(s.done)
	defer s.cancel()
	for run := 1; ; run++ {
		target := s.buf
		if s.opts.Interval > 0 {
			target = NewBuffer(s.buf.Max())
		}
		code, err := s.runOnce(ctx, run, target, first)
		first = nil
		if s.opts.Interval > 0 && (err == nil || code >= 0) {
			s.buf.Replace(target)
		}
		s.mu.Lock()
		s.st.Running = false
		s.st.PID = 0
		s.st.ExitCode = code
		if err != nil {
			s.st.Err = err.Error()
		} else {
			s.st.Err = ""
		}
		stopped := s.stopped || ctx.Err() != nil
		s.mu.Unlock()
		if stopped || s.opts.Interval <= 0 || (err != nil && code < 0) {
			s.finish(stopped)
			return
		}
		select {
		case <-ctx.Done():
			s.finish(true)
			return
		case <-time.After(s.opts.Interval):
		}
	}
}

func (s *Session) finish(stopped bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.st.Active = false
	s.st.Stopped = stopped
	s.st.Ended = time.Now()
}

// runOnce runs the command once and returns its exit code (-1 when it
// was killed or never started). first, when non-nil, receives the
// start result.
func (s *Session) runOnce(ctx context.Context, run int, out io.Writer, first chan<- error) (int, error) {
	report := func(err error) {
		if first != nil {
			first <- err
		}
	}
	cmd := shellCommand(s.opts.Command)
	cmd.Dir = s.opts.Dir
	cmd.Env = Environ()
	cmd.Stdin = nil
	r, err := startProcess(cmd, s.opts)
	if err != nil {
		report(fmt.Errorf("watch: start: %w", err))
		return -1, err
	}
	s.mu.Lock()
	s.proc = cmd.Process
	s.st.Running = true
	s.st.Run = run
	s.st.RunStart = time.Now()
	s.st.PID = cmd.Process.Pid
	s.mu.Unlock()
	report(nil)

	copied := make(chan struct{})
	go func() {
		defer close(copied)
		_, _ = io.Copy(out, r)
	}()
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()

	var waitErr error
	killed := false
	select {
	case waitErr = <-waited:
	case <-ctx.Done():
		killed = true
		terminateGroup(cmd.Process)
		select {
		case waitErr = <-waited:
		case <-time.After(1500 * time.Millisecond):
			killGroup(cmd.Process)
			waitErr = <-waited
		}
	}
	// Anything the command left behind in its process group would keep
	// the output pipe open; the watch owns the group, so end it.
	killGroup(cmd.Process)
	select {
	case <-copied:
	case <-time.After(500 * time.Millisecond):
	}
	_ = r.Close()
	<-copied

	code := -1
	if cmd.ProcessState != nil {
		code = cmd.ProcessState.ExitCode()
	}
	if killed {
		return -1, nil
	}
	var ee *exec.ExitError
	if waitErr != nil && !errors.As(waitErr, &ee) {
		return code, waitErr
	}
	return code, nil
}
