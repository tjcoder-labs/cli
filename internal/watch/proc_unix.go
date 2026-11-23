//go:build !windows

package watch

import (
	"io"
	"os"
	"os/exec"
	"syscall"

	"github.com/creack/pty"
)

const ptySupported = true

func shellCommand(command string) *exec.Cmd {
	if bash, err := exec.LookPath("bash"); err == nil {
		return exec.Command(bash, "-lc", command)
	}
	return exec.Command("/bin/sh", "-c", command)
}

// startProcess starts cmd in its own session (and so its own process
// group, with no access to the TUI's terminal or a tty-scoped sudo
// ticket) and returns a reader over its combined output.
func startProcess(cmd *exec.Cmd, opts Options) (io.ReadCloser, error) {
	if opts.PTY {
		// pty.StartWithAttrs makes the PTY slave the new session's
		// controlling terminal (Setsid+Setctty).
		ptmx, err := pty.StartWithAttrs(cmd, &pty.Winsize{Cols: opts.Cols, Rows: opts.Rows},
			&syscall.SysProcAttr{Setsid: true, Setctty: true})
		if err != nil {
			return nil, err
		}
		return ptyReader{ptmx}, nil
	}
	pr, pw, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	cmd.Stdout = pw
	cmd.Stderr = pw
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		pr.Close()
		pw.Close()
		return nil, err
	}
	pw.Close()
	return pr, nil
}

// ptyReader maps the EIO Linux returns once the slave side is closed
// to a clean EOF.
type ptyReader struct{ f *os.File }

func (p ptyReader) Read(b []byte) (int, error) {
	n, err := p.f.Read(b)
	if err != nil && n == 0 {
		if pe, ok := err.(*os.PathError); ok && pe.Err == syscall.EIO {
			return 0, io.EOF
		}
	}
	return n, err
}

func (p ptyReader) Close() error { return p.f.Close() }

func terminateGroup(p *os.Process) {
	if p != nil {
		_ = syscall.Kill(-p.Pid, syscall.SIGTERM)
		_ = syscall.Kill(-p.Pid, syscall.SIGHUP)
	}
}

func killGroup(p *os.Process) {
	if p != nil {
		_ = syscall.Kill(-p.Pid, syscall.SIGKILL)
	}
}
