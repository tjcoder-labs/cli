//go:build !windows

package watch

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func waitDone(t *testing.T, s *Session) {
	t.Helper()
	select {
	case <-s.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("watch did not finish")
	}
}

func TestSessionStreamsColourAndExitCode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix shell")
	}
	for _, usePTY := range []bool{false, true} {
		s, err := Start(Options{Command: `printf '\033[31mred\033[0m\n'; exit 3`, Dir: t.TempDir(), PTY: usePTY}, nil)
		if err != nil {
			t.Fatal(err)
		}
		waitDone(t, s)
		st := s.Status()
		if st.Active || st.Running || st.ExitCode != 3 || st.Stopped || st.Run != 1 {
			t.Fatalf("pty=%v status = %+v", usePTY, st)
		}
		lines := s.Buffer().Lines()
		if len(lines) == 0 || !strings.Contains(lines[0], "[maroon") || !strings.Contains(lines[0], "red") {
			t.Fatalf("pty=%v lines = %q", usePTY, lines)
		}
	}
}

func TestSessionColourEnv(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix shell")
	}
	t.Setenv("NO_COLOR", "1")
	s, err := Start(Options{Command: `echo "fc=$FORCE_COLOR cf=$CLICOLOR_FORCE nc=${NO_COLOR-unset}"`}, nil)
	if err != nil {
		t.Fatal(err)
	}
	waitDone(t, s)
	if got := s.Buffer().Text(); got != "fc=1 cf=1 nc=unset" {
		t.Fatalf("env = %q", got)
	}
}

func TestSessionStopKillsProcessGroup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process groups")
	}
	for _, usePTY := range []bool{false, true} {
		dir := t.TempDir()
		pidFile := filepath.Join(dir, "child.pid")
		// A background grandchild that ignores nothing special; the
		// whole group must die on Stop.
		cmd := `sleep 300 & echo $! > child.pid; echo started; wait`
		s, err := Start(Options{Command: cmd, Dir: dir, PTY: usePTY}, nil)
		if err != nil {
			t.Fatal(err)
		}
		var child int
		deadline := time.Now().Add(5 * time.Second)
		for child == 0 && time.Now().Before(deadline) {
			if b, err := os.ReadFile(pidFile); err == nil {
				child, _ = strconv.Atoi(strings.TrimSpace(string(b)))
			}
			time.Sleep(20 * time.Millisecond)
		}
		if child == 0 {
			t.Fatal("child never started")
		}
		if st := s.Status(); !st.Running || st.PID == 0 {
			t.Fatalf("not running: %+v", st)
		}
		s.Stop()
		waitDone(t, s)
		st := s.Status()
		if !st.Stopped || st.Running || st.ExitCode != -1 {
			t.Fatalf("pty=%v status after stop = %+v", usePTY, st)
		}
		gone := false
		for i := 0; i < 100 && !gone; i++ {
			gone = syscall.Kill(child, 0) == syscall.ESRCH || zombie(child)
			time.Sleep(20 * time.Millisecond)
		}
		if !gone {
			t.Fatalf("pty=%v grandchild %d survived Stop", usePTY, child)
		}
		s.Stop() // idempotent
	}
}

// zombie reports a process that has exited but not been reaped by its
// (re)parent yet, which counts as killed.
func zombie(pid int) bool {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	f := strings.Fields(string(b))
	return len(f) > 2 && f[2] == "Z"
}

func TestSessionIntervalReruns(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix shell")
	}
	dir := t.TempDir()
	s, err := Start(Options{Command: `echo x >> runs; wc -l < runs`, Dir: dir, Interval: 50 * time.Millisecond}, nil)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for s.Status().Run < 3 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	s.Stop()
	waitDone(t, s)
	st := s.Status()
	if st.Run < 3 || !st.Stopped {
		t.Fatalf("status = %+v", st)
	}
	// Each run replaces the screen: one line, a count >= 2.
	lines := s.Buffer().Lines()
	if len(lines) != 1 {
		t.Fatalf("interval output should be the last run only: %q", lines)
	}
	if n, _ := strconv.Atoi(strings.TrimSpace(lines[0])); n < 2 {
		t.Fatalf("last run output = %q", lines[0])
	}
}

func TestSessionMaxDuration(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix shell")
	}
	s, err := Start(Options{Command: "sleep 30", MaxDuration: 200 * time.Millisecond}, nil)
	if err != nil {
		t.Fatal(err)
	}
	waitDone(t, s)
	if st := s.Status(); !st.Stopped {
		t.Fatalf("status = %+v", st)
	}
}

func TestStartRejectsEmptyAndBadDir(t *testing.T) {
	if _, err := Start(Options{Command: "  "}, nil); err == nil {
		t.Fatal("empty command accepted")
	}
	if _, err := Start(Options{Command: "true", Dir: "/definitely/not/here"}, nil); err == nil {
		t.Fatal("bad dir accepted")
	}
}
