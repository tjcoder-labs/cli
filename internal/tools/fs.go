package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/tjcoder-labs/cli/internal/client"
)

func resolvePath(root, p string) string {
	if p == "" {
		return root
	}
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(root, p)
}

type searchCodeTool struct{}

func (searchCodeTool) Definition() client.ToolDefinition {
	return client.ToolDefinition{
		Type: "function",
		Function: client.FunctionDefinition{
			Name:        "search_code",
			Description: "Search source files with a regular expression. Useful for symbols, imports, TODOs, handlers, and config keys.",
			Parameters: objectSchema([]string{"pattern"}, map[string]any{
				"pattern":     stringProp("Regular expression to search for."),
				"path":        stringProp("Optional path relative to the workspace root."),
				"glob":        stringProp("Optional glob filter such as *.go or **/*.ts."),
				"output_mode": stringProp("files_only, lines_with_context, or count."),
				"limit":       numberProp("Maximum results to return. Default 50."),
			}),
		},
	}
}

func (searchCodeTool) Execute(_ context.Context, raw json.RawMessage, env ExecEnv) (Result, error) {
	var args struct {
		Pattern    string `json:"pattern"`
		Path       string `json:"path"`
		Glob       string `json:"glob"`
		OutputMode string `json:"output_mode"`
		Limit      int    `json:"limit"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return Result{}, err
	}
	if args.Pattern == "" {
		return Result{}, fmt.Errorf("pattern is required")
	}
	if args.Limit <= 0 {
		args.Limit = 50
	}
	if args.OutputMode == "" {
		args.OutputMode = "files_only"
	}
	root := resolvePath(env.WorkspaceRoot, args.Path)
	re, err := regexp.Compile(args.Pattern)
	if err != nil {
		return Result{}, err
	}
	var hits []string
	counts := map[string]int{}
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			base := filepath.Base(path)
			if base == ".git" || base == "node_modules" || strings.HasPrefix(base, ".") && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		if args.Glob != "" {
			match, err := filepath.Match(args.Glob, rel)
			if err != nil || !match {
				matchBase, _ := filepath.Match(args.Glob, filepath.Base(rel))
				if !matchBase {
					return nil
				}
			}
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		lines := strings.Split(string(data), "\n")
		fileMatched := false
		for idx, line := range lines {
			if !re.MatchString(line) {
				continue
			}
			counts[rel]++
			if !fileMatched && args.OutputMode == "files_only" {
				hits = append(hits, rel)
				fileMatched = true
				if len(hits) >= args.Limit {
					return filepath.SkipAll
				}
				continue
			}
			if args.OutputMode == "lines_with_context" {
				hits = append(hits, fmt.Sprintf("%s:%d: %s", rel, idx+1, strings.TrimSpace(line)))
				if len(hits) >= args.Limit {
					return filepath.SkipAll
				}
			}
		}
		return nil
	})
	if err != nil && err != filepath.SkipAll {
		return Result{}, err
	}
	if args.OutputMode == "count" {
		keys := make([]string, 0, len(counts))
		for key := range counts {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			hits = append(hits, fmt.Sprintf("%s: %d", key, counts[key]))
		}
		if len(hits) > args.Limit {
			hits = hits[:args.Limit]
		}
	}
	out := strings.Join(hits, "\n")
	if out == "" {
		out = "no matches"
	}
	return Result{Content: out, Preview: preview(out)}, nil
}

type readFileTool struct{}

func (readFileTool) Definition() client.ToolDefinition {
	return client.ToolDefinition{
		Type: "function",
		Function: client.FunctionDefinition{
			Name:        "read_file",
			Description: "Read a file from disk, optionally limited to a line range.",
			Parameters: objectSchema([]string{"path"}, map[string]any{
				"path":       stringProp("File path relative to the workspace root or absolute."),
				"start_line": numberProp("Optional 1-based start line."),
				"end_line":   numberProp("Optional inclusive end line."),
				"max_chars":  numberProp("Optional max characters. Default 50000."),
			}),
		},
	}
}

func (readFileTool) Execute(_ context.Context, raw json.RawMessage, env ExecEnv) (Result, error) {
	var args struct {
		Path      string `json:"path"`
		StartLine int    `json:"start_line"`
		EndLine   int    `json:"end_line"`
		MaxChars  int    `json:"max_chars"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return Result{}, err
	}
	path := resolvePath(env.WorkspaceRoot, args.Path)
	data, err := os.ReadFile(path)
	if err != nil {
		return Result{}, err
	}
	text := string(data)
	if args.StartLine > 0 || args.EndLine > 0 {
		lines := strings.Split(text, "\n")
		start := max(1, args.StartLine)
		end := len(lines)
		if args.EndLine > 0 && args.EndLine < end {
			end = args.EndLine
		}
		if start > len(lines) {
			text = ""
		} else {
			text = strings.Join(lines[start-1:end], "\n")
		}
	}
	if args.MaxChars <= 0 {
		args.MaxChars = 50000
	}
	if len(text) > args.MaxChars {
		text = text[:args.MaxChars] + "\n...[truncated]"
	}
	return Result{Content: text, Preview: preview(text)}, nil
}

type listDirectoryTool struct{}

func (listDirectoryTool) Definition() client.ToolDefinition {
	return client.ToolDefinition{
		Type: "function",
		Function: client.FunctionDefinition{
			Name:        "list_directory",
			Description: "List files and directories under a path with optional depth and glob filtering.",
			Parameters: objectSchema(nil, map[string]any{
				"path":  stringProp("Directory path relative to the workspace root."),
				"depth": numberProp("Maximum depth. Default 2."),
				"glob":  stringProp("Optional glob filter."),
			}),
		},
	}
}

func (listDirectoryTool) Execute(_ context.Context, raw json.RawMessage, env ExecEnv) (Result, error) {
	var args struct {
		Path  string `json:"path"`
		Depth int    `json:"depth"`
		Glob  string `json:"glob"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return Result{}, err
	}
	if args.Depth <= 0 {
		args.Depth = 2
	}
	root := resolvePath(env.WorkspaceRoot, args.Path)
	var entries []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		if rel == "." {
			entries = append(entries, ".")
			return nil
		}
		if depth := strings.Count(rel, string(filepath.Separator)) + 1; depth > args.Depth {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if args.Glob != "" {
			match, _ := filepath.Match(args.Glob, filepath.Base(rel))
			if !match {
				return nil
			}
		}
		label := rel
		if d.IsDir() {
			label += "/"
		}
		entries = append(entries, label)
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	if len(entries) > 200 {
		entries = append(entries[:200], "...[truncated]")
	}
	out := strings.Join(entries, "\n")
	return Result{Content: out, Preview: preview(out)}, nil
}

type editFileTool struct{}

func (editFileTool) Definition() client.ToolDefinition {
	return client.ToolDefinition{
		Type: "function",
		Function: client.FunctionDefinition{
			Name:        "edit_file",
			Description: "Make one exact string replacement in an existing file.",
			Parameters: objectSchema([]string{"path", "old_str", "new_str"}, map[string]any{
				"path":    stringProp("File path."),
				"old_str": stringProp("Exact text to replace."),
				"new_str": stringProp("Replacement text."),
			}),
		},
	}
}

func (editFileTool) Execute(_ context.Context, raw json.RawMessage, env ExecEnv) (Result, error) {
	var args struct {
		Path   string `json:"path"`
		OldStr string `json:"old_str"`
		NewStr string `json:"new_str"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return Result{}, err
	}
	path := resolvePath(env.WorkspaceRoot, args.Path)
	data, err := os.ReadFile(path)
	if err != nil {
		return Result{}, err
	}
	text := string(data)
	count := strings.Count(text, args.OldStr)
	if count == 0 {
		return Result{}, fmt.Errorf("old_str not found")
	}
	if count > 1 {
		return Result{}, fmt.Errorf("old_str not unique (%d occurrences)", count)
	}
	updated := strings.Replace(text, args.OldStr, args.NewStr, 1)
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		return Result{}, err
	}
	msg := fmt.Sprintf("updated %s", path)
	return Result{Content: msg, Preview: msg}, nil
}

type createFileTool struct{}

func (createFileTool) Definition() client.ToolDefinition {
	return client.ToolDefinition{
		Type: "function",
		Function: client.FunctionDefinition{
			Name:        "create_file",
			Description: "Create a new file. Fails if the file already exists.",
			Parameters: objectSchema([]string{"path", "content"}, map[string]any{
				"path":    stringProp("File path."),
				"content": stringProp("File content."),
			}),
		},
	}
}

func (createFileTool) Execute(_ context.Context, raw json.RawMessage, env ExecEnv) (Result, error) {
	var args struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return Result{}, err
	}
	path := resolvePath(env.WorkspaceRoot, args.Path)
	if _, err := os.Stat(path); err == nil {
		return Result{}, fmt.Errorf("file already exists")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Result{}, err
	}
	if err := os.WriteFile(path, []byte(args.Content), 0o644); err != nil {
		return Result{}, err
	}
	msg := fmt.Sprintf("created %s", path)
	return Result{Content: msg, Preview: msg}, nil
}

type writeFileTool struct{}

func (writeFileTool) Definition() client.ToolDefinition {
	return client.ToolDefinition{
		Type: "function",
		Function: client.FunctionDefinition{
			Name:        "write_file",
			Description: "Replace the full content of a file, creating parent directories if needed.",
			Parameters: objectSchema([]string{"path", "content"}, map[string]any{
				"path":    stringProp("File path."),
				"content": stringProp("New file content."),
			}),
		},
	}
}

func (writeFileTool) Execute(_ context.Context, raw json.RawMessage, env ExecEnv) (Result, error) {
	var args struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return Result{}, err
	}
	path := resolvePath(env.WorkspaceRoot, args.Path)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Result{}, err
	}
	if err := os.WriteFile(path, []byte(args.Content), 0o644); err != nil {
		return Result{}, err
	}
	msg := fmt.Sprintf("wrote %s", path)
	return Result{Content: msg, Preview: msg}, nil
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
