//go:build windows

package tools

import "os/exec"

// detachFromTerminal is a no-op on Windows (no sudo, no tty tickets).
func detachFromTerminal(*exec.Cmd, bool) {}
