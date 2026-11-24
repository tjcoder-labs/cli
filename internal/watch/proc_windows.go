//go:build windows

package watch

import (
	"io"
	"os"
	"os/exec"
)

// No ConPTY support yet: Windows watches use pipes and rely on
// FORCE_COLOR / CLICOLOR_FORCE for colour.
const ptySupported = false

func shellCommand(command string) *exec.Cmd {
	return exec.Command("cmd", "/C", command)
}

func startProcess(cmd *exec.Cmd, _ Options) (io.ReadCloser, error) {
	pr, pw, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	cmd.Stdout = pw
	cmd.Stderr = pw
	if err := cmd.Start(); err != nil {
		pr.Close()
		pw.Close()
		return nil, err
	}
	pw.Close()
	return pr, nil
}

func terminateGroup(p *os.Process) {
	if p != nil {
		_ = p.Kill()
	}
}

func killGroup(p *os.Process) {
	if p != nil {
		_ = p.Kill()
	}
}
