//go:build unix

package browser

import (
	"os/exec"
	"syscall"
)

// setSysProcAttr detaches the child process into its own session so the
// agent process can exit without killing Chrome. Only available on Unix.
func setSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}