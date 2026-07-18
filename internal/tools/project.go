package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tjcoder-labs/cli/internal/client"
)

type inspectProjectTool struct{}

func (inspectProjectTool) Definition() client.ToolDefinition {
	return client.ToolDefinition{
		Type: "function",
		Function: client.FunctionDefinition{
			Name:        "inspect_project",
			Description: "Detect the current project stack and important files.",
			Parameters: objectSchema(nil, map[string]any{
				"path": stringProp("Optional project path."),
			}),
		},
	}
}

func (inspectProjectTool) Execute(_ context.Context, raw json.RawMessage, env ExecEnv) (Result, error) {
	var args struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return Result{}, err
	}
	root := resolvePath(env.WorkspaceRoot, args.Path)
	detected := []string{}
	for _, candidate := range []struct {
		file string
		name string
	}{
		{"go.mod", "Go"},
		{"package.json", "Node.js"},
		{"tsconfig.json", "TypeScript"},
		{"Cargo.toml", "Rust"},
		{"pyproject.toml", "Python"},
		{"requirements.txt", "Python"},
		{"Dockerfile", "Docker"},
	} {
		if _, err := os.Stat(filepath.Join(root, candidate.file)); err == nil {
			detected = append(detected, candidate.name)
		}
	}
	sort.Strings(detected)
	files := []string{}
	entries, _ := os.ReadDir(root)
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if entry.IsDir() {
			files = append(files, name+"/")
		} else {
			files = append(files, name)
		}
		if len(files) >= 20 {
			break
		}
	}
	out := fmt.Sprintf("root: %s\nstack: %s\nentries:\n- %s", root, strings.Join(detected, ", "), strings.Join(files, "\n- "))
	return Result{Content: out, Preview: preview(out)}, nil
}

type listAvailableModelsTool struct{}

func (listAvailableModelsTool) Definition() client.ToolDefinition {
	return client.ToolDefinition{
		Type: "function",
		Function: client.FunctionDefinition{
			Name:        "list_available_models",
			Description: "List the models available on the configured local Ollama provider.",
			Parameters:  objectSchema(nil, map[string]any{}),
		},
	}
}

func (listAvailableModelsTool) Execute(ctx context.Context, _ json.RawMessage, env ExecEnv) (Result, error) {
	if env.Provider == nil {
		return Result{}, fmt.Errorf("provider unavailable")
	}
	models, err := env.Provider.ListModels(ctx)
	if err != nil {
		return Result{}, err
	}
	lines := make([]string, 0, len(models))
	for _, model := range models {
		size := strings.TrimSpace(model.ParameterSize)
		if size == "" {
			size = strings.TrimSpace(model.Family)
		}
		lines = append(lines, fmt.Sprintf("%-24s %s", model.Name, size))
	}
	out := strings.Join(lines, "\n")
	return Result{Content: out, Preview: preview(out)}, nil
}
