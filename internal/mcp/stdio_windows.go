package mcp

import "os/exec"

func configureStdioProcess(cmd *exec.Cmd) {}
func terminateStdioProcess(cmd *exec.Cmd) { _ = cmd.Process.Kill() }
func killStdioProcess(cmd *exec.Cmd)      { _ = cmd.Process.Kill() }
