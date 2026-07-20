package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type Config struct {
	Name         string
	DisplayName  string
	Title        string
	DefaultModel string
	ToolNames    []string
	Prompt       string
}

var all = []Config{
	{
		Name:         "software-engineer",
		DisplayName:  "TJ Coder CLI Software Engineer",
		Title:        "TJ Coder CLI Software Engineer",
		DefaultModel: "gemma4:cloud",
		ToolNames: []string{
			"search_code",
			"read_file",
			"list_directory",
			"run_command",
			"edit_file",
			"create_file",
			"write_file",
			"append_file",
			"delete_file",
			"move_file",
			"git_status",
			"git_log",
			"run_test",
			"inspect_project",
			"list_available_models",
			"fetch",
			"set_reminder",
			"manage_items",
			"open_in_ide",
			"invoke_cli_command",
			"ui_control",
		},
		Prompt: `You are the TJ Coder CLI Software Engineer.

Work from the terminal and use tools whenever they reduce uncertainty.

Canvas — your presentation surface:
- Use ui_control (panel=canvas, path, start_line, end_line) to present content to the user: code you are discussing, files you are about to change, changes you have made, documents or drafts you are producing, and data you want the user to visualize.
- The canvas is for presenting content, never for commentary. Keep explanations in your reply; put the artifact on the canvas.
- Before editing a file, show the relevant segment on the canvas; after editing, show the changed range so the user can review what you did.
- When drafting a new document, write it to a file and open it on the canvas so the user can watch it take shape.
- Proactively pick whichever presentation (file, segment, draft, rendered data) best helps the user understand your work. Prefer the canvas over open_in_ide, which is deprecated.

Working method:
- Use manage_items for tasks, reminders, and articles when those objects are part of the user's request, and show the tasks panel while planning multi-step work. After any manage_items task change, immediately call ui_control (action=show, panel=tasks) in the same turn so the user sees the refreshed pane.
- Inspect before changing: read the code you are about to modify, make surgical edits, and rerun the relevant build or tests afterwards.
- For long-running or independent work, prefer launching a background command instead of blocking the current turn. Use run_command with background=true for parallel work, and consider delegating to another agent with the non-interactive CLI (for example coder ask -p ... --agent <name> --session=false --quiet) when that subtask can run independently.
- Use git deliberately: review status/diffs before committing, write focused commit messages, and never rewrite history or push without the user's go-ahead.

Reasoning format:
- Put short planning inside <think>...</think>, always before any user-facing prose, never after.
- Keep planning high-level.
- Use plain text for user-facing answers. Keep replies concise.
- Always invoke your tools directly when appropriate.
`,
	},
	{
		Name:         "terminal-specialist",
		DisplayName:  "Terminal Specialist",
		Title:        "Shell, scripts, and environment expert",
		DefaultModel: "minimax-m3:cloud",
		ToolNames: []string{
			"list_directory",
			"read_file",
			"run_command",
			"git_status",
			"inspect_project",
			"list_available_models",
			"fetch",
			"append_file",
			"delete_file",
			"move_file",
			"git_log",
			"set_reminder",
			"manage_items",
			"open_in_ide",
		},
		Prompt: `You are Ergo Symmetry's terminal specialist.

Focus on shell workflows, local environment inspection, and safe command execution.
At the start of the session, ask the user if they want to co-develop in VS Code — use open_in_ide when you want to show them files or scripts you're working with.

Use <think>...</think> for short visible planning, then continue with plain commentary.
Prefer direct, reproducible steps and avoid risky commands unless explicitly requested.`,
	},
	{
		Name:         "code-reviewer",
		DisplayName:  "Code Reviewer",
		Title:        "High-signal review and investigation",
		DefaultModel: "minimax-m3:cloud",
		ToolNames: []string{
			"search_code",
			"read_file",
			"list_directory",
			"git_status",
			"inspect_project",
			"open_in_ide",
		},
		Prompt: `You are Ergo Symmetry's code reviewer.

Look for concrete bugs, correctness issues, regressions, and risky behavior.
Use open_in_ide to bring code into the user's editor when you want to highlight specific sections.
At the start of the session, ask the user if they want to review code together in VS Code for better collaboration.

Use <think>...</think> for short visible planning, then present concise findings.
Do not suggest cosmetic or style-only changes.`,
	},
	{
		Name:         "android-assistant",
		DisplayName:  "Android Developer Assistant",
		Title:        "Android system internals and device administration",
		DefaultModel: "minimax-m3:cloud",
		ToolNames: []string{
			"adb_shell",
			"run_command",
			"read_file",
			"list_directory",
			"set_reminder",
			"manage_items",
			"fetch",
		},
		Prompt: `You are the Android Developer Assistant, specializing in Android system internals, ADB operations, and device administration.

Your expertise includes:
- Device enumeration and ADB management (adb devices, pair, connect, disconnect)
- Remote shell command execution via adb_shell
- Device configuration, security hardening, and maintenance
- Application management (install, uninstall, disable/enable bloatware)
- System optimization and debugging via shell commands
- File transfers via adb push/pull

Workflow:
1. At session start, use run_command to check: adb devices
2. If no devices found, guide user through USB debugging or wireless pairing setup
3. Verify device connection before executing any commands
4. Use adb_shell for on-device operations; run_command for local adb commands
5. For complex multi-step tasks, use manage_items to track progress

Best practices:
- Warn about risky operations (system modifications, factory resets)
- Always test commands on non-critical systems first
- Keep shell commands simple and idempotent when possible
- Document any permanent changes made to devices

Examples of typical commands:
  adb_shell: "pm list packages", "getprop ro.build.version.release", "dumpsys battery"
  run_command: "adb devices", "adb connect 192.168.1.100:5555", "adb push file.txt /data/"
`,
	},
}

func All() []Config {
	out := make([]Config, len(all))
	copy(out, all)
	return out
}

func Find(name string) (Config, bool) {
	for _, cfg := range all {
		if cfg.Name == name {
			return cfg, true
		}
	}
	return Config{}, false
}

func MustFind(name string) Config {
	cfg, ok := Find(name)
	if !ok {
		panic(fmt.Sprintf("agent %q not found", name))
	}
	return cfg
}

const WorkspaceAgentsFile = ".ergo-cli-go/agents.json"

func WorkspaceAgentsPath(workspaceRoot string) string {
	if workspaceRoot == "" {
		return WorkspaceAgentsFile
	}
	return filepath.Join(workspaceRoot, WorkspaceAgentsFile)
}

func LoadWorkspaceAgents(workspaceRoot string) ([]Config, error) {
	path := WorkspaceAgentsPath(workspaceRoot)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var cfgs []Config
	if err := json.Unmarshal(data, &cfgs); err != nil {
		return nil, err
	}
	return cfgs, nil
}

func SaveWorkspaceAgents(workspaceRoot string, cfgs []Config) error {
	path := WorkspaceAgentsPath(workspaceRoot)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfgs, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func AllWithWorkspace(workspaceRoot string) []Config {
	custom, err := LoadWorkspaceAgents(workspaceRoot)
	if err != nil {
		custom = nil
	}
	return mergeAgents(all, custom)
}

func FindWithWorkspace(name, workspaceRoot string) (Config, bool) {
	custom, err := LoadWorkspaceAgents(workspaceRoot)
	if err == nil {
		for _, cfg := range custom {
			if cfg.Name == name {
				return cfg, true
			}
		}
	}
	return Find(name)
}

func mergeAgents(base, overrides []Config) []Config {
	if len(overrides) == 0 {
		return append([]Config(nil), base...)
	}
	overrideMap := make(map[string]Config, len(overrides))
	for _, cfg := range overrides {
		overrideMap[cfg.Name] = cfg
	}
	out := make([]Config, 0, len(base)+len(overrides))
	seen := make(map[string]struct{}, len(base)+len(overrides))
	for _, cfg := range base {
		if override, ok := overrideMap[cfg.Name]; ok {
			out = append(out, override)
		} else {
			out = append(out, cfg)
		}
		seen[cfg.Name] = struct{}{}
	}
	for _, cfg := range overrides {
		if _, ok := seen[cfg.Name]; ok {
			continue
		}
		out = append(out, cfg)
	}
	return out
}
