//go:build !windows

package tools

import (
	"os/exec"
	"syscall"
)

// detachFromTerminal starts cmd in a new session with no controlling
// terminal, so it cannot use a tty-scoped sudo ticket or prompt on the
// TUI's terminal. With ctty set (PTY mode) the PTY slave on stdin becomes
// the new session's terminal instead.
func detachFromTerminal(cmd *exec.Cmd, ctty bool) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setsid = true
	if ctty {
		cmd.SysProcAttr.Setctty = true
		cmd.SysProcAttr.Ctty = 0
	}
}
