package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/tjcoder-labs/cli/internal/client"
)

// adbShellTool executes a shell command on the device
type adbShellTool struct{}

func (t adbShellTool) Definition() client.ToolDefinition {
	return client.ToolDefinition{
		Type: "function",
		Function: client.FunctionDefinition{
			Name:        "adb_shell",
			Description: "Execute a shell command on a connected Android device via adb",
			Parameters: objectSchema(
				[]string{"command"},
				map[string]any{
					"command": stringProp("Shell command to execute on device"),
					"device":  stringProp("Device serial or address (uses default device if omitted)"),
				},
			),
		},
	}
}

func (t adbShellTool) Execute(ctx context.Context, args json.RawMessage, env ExecEnv) (Result, error) {
	var params struct {
		Command string `json:"command"`
		Device  string `json:"device"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return Result{}, fmt.Errorf("invalid arguments: %w", err)
	}

	cmdArgs := []string{}
	if params.Device != "" {
		cmdArgs = append(cmdArgs, "-s", params.Device)
	}
	cmdArgs = append(cmdArgs, "shell", params.Command)

	cmd := exec.CommandContext(ctx, "adb", cmdArgs...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Shell commands may return non-zero but still be valid; include output
		return Result{
			Content: string(output),
			Preview: fmt.Sprintf("Command exit: %v", err),
		}, nil
	}

	return Result{
		Content: truncateOutput(string(output)),
		Preview: preview(truncateOutput(string(output))),
	}, nil
}

// --- Helper functions ---

func parseAdbDevices(output string, includeOffline bool) ([]struct{ Serial, State string }, error) {
	var devices []struct{ Serial, State string }
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || line == "List of attached devices" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		serial := parts[0]
		state := parts[1]

		if state == "offline" && !includeOffline {
			continue
		}

		devices = append(devices, struct{ Serial, State string }{serial, state})
	}
	return devices, nil
}

// RegisterAndroidTools registers Android ADB tools with the registry
func RegisterAndroidTools(r *Registry) {
	r.RegisterTool(adbShellTool{})
}

