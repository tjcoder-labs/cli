package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/tjcoder-labs/cli/internal/client"
	"github.com/tjcoder-labs/cli/internal/workspace"
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
				"workspace":   workspaceProp(),
			}),
		},
	}
}

// searchHit is one matching line.
type searchHit struct {
	rel  string
	line int
	text string
}

func (searchCodeTool) Execute(ctx context.Context, raw json.RawMessage, env ExecEnv) (Result, error) {
	var args struct {
		Pattern    string `json:"pattern"`
		Path       string `json:"path"`
		Glob       string `json:"glob"`
		OutputMode string `json:"output_mode"`
		Limit      int    `json:"limit"`
		Workspace  string `json:"workspace"`
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
	ws, root, err := env.target(ctx, args.Workspace, args.Path)
	if err != nil {
		return Result{}, err
	}
	re, err := regexp.Compile(args.Pattern)
	if err != nil {
		return Result{}, err
	}
	var hits []string
	counts := map[string]int{}
	files := map[string]bool{}
	// add records one hit; false stops the search.
	add := func(h searchHit) bool {
		counts[h.rel]++
		switch args.OutputMode {
		case "files_only":
			if !files[h.rel] {
				files[h.rel] = true
				hits = append(hits, h.rel)
			}
		case "lines_with_context":
			hits = append(hits, fmt.Sprintf("%s:%d: %s", h.rel, h.line, strings.TrimSpace(h.text)))
		}
		return args.OutputMode == "count" || len(hits) < args.Limit
	}
	if ws.Kind == workspace.KindSSH {
		if err := remoteSearch(ctx, ws, root, args.Pattern, args.Glob, re, add); err != nil {
			return Result{}, err
		}
	} else if err := localSearch(root, args.Glob, re, add); err != nil {
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

func globMatch(glob, rel string) bool {
	if glob == "" {
		return true
	}
	if m, err := filepath.Match(glob, rel); err == nil && m {
		return true
	}
	m, _ := filepath.Match(glob, filepath.Base(rel))
	return m
}

func localSearch(root, glob string, re *regexp.Regexp, add func(searchHit) bool) error {
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
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
		if !globMatch(glob, rel) {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		for idx, line := range strings.Split(string(data), "\n") {
			if re.MatchString(line) && !add(searchHit{rel: rel, line: idx + 1, text: line}) {
				return filepath.SkipAll
			}
		}
		return nil
	})
	if err == filepath.SkipAll {
		return nil
	}
	return err
}

// remoteSearch greps on the host (rg when installed, else grep -E),
// then re-checks every line with the Go regexp so results match the
// local semantics. Hidden directories, .git and node_modules are
// skipped as locally.
func remoteSearch(ctx context.Context, ws *workspace.Workspace, root, pattern, glob string, re *regexp.Regexp, add func(searchHit) bool) error {
	q := workspace.Quote
	rg := "rg -n --no-heading --color=never --no-messages -g '!node_modules' -e " + q(pattern)
	grep := "grep -rnIE --exclude-dir=.git --exclude-dir=node_modules --exclude-dir='.?*' -e " + q(pattern)
	if glob != "" {
		rg += " -g " + q(glob)
		grep += " --include=" + q(filepath.Base(glob))
	}
	script := "if command -v rg >/dev/null 2>&1; then " + rg + " . ; else " + grep + " . ; fi | head -n 5000"
	cmd, err := ws.FS.Command(ctx, root, script, nil)
	if err != nil {
		return err
	}
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		// exit 1 = no matches for both tools
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 1 {
			return nil
		}
		return fmt.Errorf("remote search on %s: %w", ws.Host, err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		file, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		num, text, ok := strings.Cut(rest, ":")
		if !ok {
			continue
		}
		n, err := strconv.Atoi(num)
		if err != nil || !re.MatchString(text) {
			continue
		}
		rel := strings.TrimPrefix(file, "./")
		if !globMatch(glob, rel) {
			continue
		}
		if !add(searchHit{rel: rel, line: n, text: text}) {
			break
		}
	}
	return nil
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
				"workspace":  workspaceProp(),
			}),
		},
	}
}

func (readFileTool) Execute(ctx context.Context, raw json.RawMessage, env ExecEnv) (Result, error) {
	var args struct {
		Path      string `json:"path"`
		StartLine int    `json:"start_line"`
		EndLine   int    `json:"end_line"`
		MaxChars  int    `json:"max_chars"`
		Workspace string `json:"workspace"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return Result{}, err
	}
	ws, path, err := env.target(ctx, args.Workspace, args.Path)
	if err != nil {
		return Result{}, err
	}
	data, err := ws.FS.ReadFile(ctx, path)
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
		if start > len(lines) || start > end {
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
				"path":      stringProp("Directory path relative to the workspace root."),
				"depth":     numberProp("Maximum depth. Default 2."),
				"glob":      stringProp("Optional glob filter."),
				"workspace": workspaceProp(),
			}),
		},
	}
}

func (listDirectoryTool) Execute(ctx context.Context, raw json.RawMessage, env ExecEnv) (Result, error) {
	var args struct {
		Path      string `json:"path"`
		Depth     int    `json:"depth"`
		Glob      string `json:"glob"`
		Workspace string `json:"workspace"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return Result{}, err
	}
	if args.Depth <= 0 {
		args.Depth = 2
	}
	ws, root, err := env.target(ctx, args.Workspace, args.Path)
	if err != nil {
		return Result{}, err
	}
	walked, err := ws.FS.Walk(ctx, root, args.Depth)
	if err != nil {
		return Result{}, err
	}
	sort.Slice(walked, func(i, j int) bool { return walked[i].Rel < walked[j].Rel })
	entries := []string{"."}
	for _, e := range walked {
		if args.Glob != "" {
			if match, _ := filepath.Match(args.Glob, filepath.Base(e.Rel)); !match {
				continue
			}
		}
		label := e.Rel
		if e.IsDir {
			label += "/"
		}
		entries = append(entries, label)
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
				"path":      stringProp("File path."),
				"old_str":   stringProp("Exact text to replace."),
				"new_str":   stringProp("Replacement text."),
				"workspace": workspaceProp(),
			}),
		},
	}
}

func (editFileTool) Execute(ctx context.Context, raw json.RawMessage, env ExecEnv) (Result, error) {
	var args struct {
		Path      string `json:"path"`
		OldStr    string `json:"old_str"`
		NewStr    string `json:"new_str"`
		Workspace string `json:"workspace"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return Result{}, err
	}
	ws, path, err := env.target(ctx, args.Workspace, args.Path)
	if err != nil {
		return Result{}, err
	}
	data, err := ws.FS.ReadFile(ctx, path)
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
	if err := ws.FS.WriteFile(ctx, path, []byte(updated)); err != nil {
		return Result{}, err
	}
	msg := fmt.Sprintf("updated %s", shown(ws, path))
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
				"path":      stringProp("File path."),
				"content":   stringProp("File content."),
				"workspace": workspaceProp(),
			}),
		},
	}
}

func (createFileTool) Execute(ctx context.Context, raw json.RawMessage, env ExecEnv) (Result, error) {
	var args struct {
		Path      string `json:"path"`
		Content   string `json:"content"`
		Workspace string `json:"workspace"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return Result{}, err
	}
	ws, path, err := env.target(ctx, args.Workspace, args.Path)
	if err != nil {
		return Result{}, err
	}
	if err := ws.FS.CreateFile(ctx, path, []byte(args.Content)); err != nil {
		if errors.Is(err, os.ErrExist) {
			return Result{}, fmt.Errorf("file already exists")
		}
		return Result{}, err
	}
	msg := fmt.Sprintf("created %s", shown(ws, path))
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
				"path":      stringProp("File path."),
				"content":   stringProp("New file content."),
				"workspace": workspaceProp(),
			}),
		},
	}
}

func (writeFileTool) Execute(ctx context.Context, raw json.RawMessage, env ExecEnv) (Result, error) {
	var args struct {
		Path      string `json:"path"`
		Content   string `json:"content"`
		Workspace string `json:"workspace"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return Result{}, err
	}
	ws, path, err := env.target(ctx, args.Workspace, args.Path)
	if err != nil {
		return Result{}, err
	}
	if err := ws.FS.WriteFile(ctx, path, []byte(args.Content)); err != nil {
		return Result{}, err
	}
	msg := fmt.Sprintf("wrote %s", shown(ws, path))
	return Result{Content: msg, Preview: msg}, nil
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
