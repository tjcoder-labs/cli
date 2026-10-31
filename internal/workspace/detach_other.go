//go:build !unix

package workspace

import "os/exec"

func detach(*exec.Cmd) {}
