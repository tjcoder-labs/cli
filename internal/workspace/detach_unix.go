//go:build unix

package workspace

import (
	"os/exec"
	"syscall"
)

// detach starts cmd in a new session with no controlling terminal, so
// ssh can never open /dev/tty (which belongs to the TUI in raw mode)
// and must use SSH_ASKPASS for every prompt.
func detach(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setsid = true
}
