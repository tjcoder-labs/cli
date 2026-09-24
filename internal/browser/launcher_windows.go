//go:build windows

package browser

import "os/exec"

// setSysProcAttr is a no-op on Windows; CREATE_NEW_PROCESS_GROUP could be
// used via syscall.SysProcAttr{CreationFlags}, but for now we rely on
// exec.Command not tying the child to a cancellable context.
func setSysProcAttr(cmd *exec.Cmd) {}