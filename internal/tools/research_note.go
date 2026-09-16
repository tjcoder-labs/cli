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

// DefaultResearchDir is where the social-researcher writes artifacts by
// default. Lives under the user's coder data dir so it survives workspace
// changes (articles/reports are usually cross-project).
func DefaultResearchDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".local", "share", "coder", "research")
}

// safeRelPath cleans a user-supplied name and prevents escaping the
// research root. Strips slashes, nulls, and parent refs.
func safeRelPath(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("name required")
	}
	name = strings.ReplaceAll(name, "\\", "/")
	parts := []string{}
	for _, p := range strings.Split(name, "/") {
		p = strings.TrimSpace(p)
		if p == "" || p == "." {
			continue
		}
		if p == ".." {
			return "", fmt.Errorf("name may not contain '..'")
		}
		parts = append(parts, p)
	}
	if len(parts) == 0 {
		return "", fmt.Errorf("invalid name")
	}
	return strings.Join(parts, "/"), nil
}

// researchNoteTool provides scoped read/write/append/list for the
// social-researcher's artifacts. Stored in ~/.local/share/coder/research/.
type researchNoteTool struct{}

func (researchNoteTool) Definition() client.ToolDefinition {
	props := map[string]any{
		"action": stringProp("Operation: list | read | write | append | delete | stat"),
		"name":   stringProp("Relative path inside the research dir, e.g. 'threads/rust-async.md' or 'profiles/jdoe.txt'"),
		"content": stringProp("Body text for write/append"),
		"prefix": stringProp("Optional subdirectory filter for list (e.g. 'threads/')"),
		"max_chars": numberProp("Max chars to return for read (default 20000; file is truncated with a marker)"),
	}
	return client.ToolDefinition{
		Type: "function",
		Function: client.FunctionDefinition{
			Name: "research_note",
			Description: `Scoped file store for research artifacts. All paths are rooted at ~/.local/share/coder/research/ — you cannot escape it.

Actions:
- list(prefix?) — enumerate files, sizes, mtimes. Default prefix "" lists everything.
- read(name, max_chars?) — return file contents (UTF-8). Files larger than max_chars are truncated with a clear marker.
- write(name, content) — create or overwrite (atomic via temp-and-rename). Intermediate dirs are created.
- append(name, content) — append to existing file; creates it if missing.
- delete(name) — remove a file.
- stat(name) — metadata only (size, mtime, first-line preview).

Use it to save scraped threads, profiles, conversation transcripts, engagement reports, and notes. Name things descriptively ("reddit/t3_abc-thread.md", "linkedin/company-acme.md") so list() is browsable.`,
			Parameters: objectSchema([]string{"action"}, props),
		},
	}
}

func (researchNoteTool) Execute(ctx context.Context, raw json.RawMessage, env ExecEnv) (Result, error) {
	_ = ctx
	_ = env
	var a struct {
		Action   string `json:"action"`
		Name     string `json:"name"`
		Content  string `json:"content"`
		Prefix   string `json:"prefix"`
		MaxChars int    `json:"max_chars"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return Result{}, err
	}
	a.Action = strings.ToLower(strings.TrimSpace(a.Action))

	root := DefaultResearchDir()
	fullPath := func() (string, error) {
		rel, err := safeRelPath(a.Name)
		if err != nil {
			return "", err
		}
		return filepath.Join(root, filepath.FromSlash(rel)), nil
	}

	switch a.Action {
	case "list":
		var prefix string
		if a.Prefix != "" {
			p, err := safeRelPath(a.Prefix)
			if err != nil {
				return Result{}, err
			}
			prefix = p
		}
		if err := os.MkdirAll(root, 0o700); err != nil {
			return Result{}, err
		}
		type ent struct {
			path  string
			size  int64
			mtime string
		}
		var entries []ent
		err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(root, p)
			if err != nil {
				return nil
			}
			rel = filepath.ToSlash(rel)
			if prefix != "" && !strings.HasPrefix(rel, prefix+"/") && rel != prefix {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			entries = append(entries, ent{path: rel, size: info.Size(), mtime: info.ModTime().Format("2006-01-02 15:04")})
			return nil
		})
		if err != nil {
			return Result{}, err
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].path < entries[j].path })
		if len(entries) == 0 {
			return Result{Content: "no research notes yet", Preview: "empty"}, nil
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%-60s %10s  %s\n", "PATH", "BYTES", "MODIFIED")
		for _, e := range entries {
			fmt.Fprintf(&b, "%-60s %10d  %s\n", trunc(e.path, 60), e.size, e.mtime)
		}
		out := b.String()
		return Result{Content: out, Preview: preview(out)}, nil

	case "read":
		full, err := fullPath()
		if err != nil {
			return Result{}, err
		}
		data, err := os.ReadFile(full)
		if err != nil {
			return Result{}, fmt.Errorf("read %q: %w", a.Name, err)
		}
		max := a.MaxChars
		if max <= 0 {
			max = 20000
		}
		out := string(data)
		truncated := false
		if len(out) > max {
			out = out[:max]
			truncated = true
		}
		if truncated {
			out += fmt.Sprintf("\n\n[... truncated at %d chars; total %d bytes ...]", max, len(data))
		}
		return Result{Content: out, Preview: preview(out)}, nil

	case "write":
		if a.Content == "" {
			return Result{}, fmt.Errorf("content required for write")
		}
		full, err := fullPath()
		if err != nil {
			return Result{}, err
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			return Result{}, err
		}
		tmp := full + ".tmp"
		if err := os.WriteFile(tmp, []byte(a.Content), 0o600); err != nil {
			return Result{}, err
		}
		if err := os.Rename(tmp, full); err != nil {
			return Result{}, err
		}
		out := fmt.Sprintf("wrote %d bytes to %s", len(a.Content), a.Name)
		return Result{Content: out, Preview: out}, nil

	case "append":
		if a.Content == "" {
			return Result{}, fmt.Errorf("content required for append")
		}
		full, err := fullPath()
		if err != nil {
			return Result{}, err
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			return Result{}, err
		}
		f, err := os.OpenFile(full, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return Result{}, err
		}
		defer f.Close()
		if _, err := f.WriteString(a.Content); err != nil {
			return Result{}, err
		}
		out := fmt.Sprintf("appended %d bytes to %s", len(a.Content), a.Name)
		return Result{Content: out, Preview: out}, nil

	case "delete":
		full, err := fullPath()
		if err != nil {
			return Result{}, err
		}
		if err := os.Remove(full); err != nil {
			return Result{}, fmt.Errorf("delete %q: %w", a.Name, err)
		}
		return Result{Content: "deleted " + a.Name, Preview: "deleted"}, nil

	case "stat":
		full, err := fullPath()
		if err != nil {
			return Result{}, err
		}
		info, err := os.Stat(full)
		if err != nil {
			return Result{}, fmt.Errorf("stat %q: %w", a.Name, err)
		}
		// Sniff first line for preview.
		var firstLine string
		if data, err := os.ReadFile(full); err == nil {
			firstLine = trunc(strings.SplitN(string(data), "\n", 2)[0], 80)
		}
		out := fmt.Sprintf("%s — %d bytes, modified %s\nfirst line: %s",
			a.Name, info.Size(), info.ModTime().Format("2006-01-02 15:04:05"), firstLine)
		return Result{Content: out, Preview: out}, nil

	default:
		return Result{}, fmt.Errorf("unknown action %q (list|read|write|append|delete|stat)", a.Action)
	}
}
