package context

import (
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// Info holds runtime environment metadata for injection into agent prompts.
type Info struct {
	CWD           string // Current working directory
	Shell         string // $SHELL or shell being used
	Time          string // Current time (RFC3339)
	TimeZone      string // Timezone name (e.g., UTC, America/Los_Angeles)
	Hostname      string // Machine hostname
	OS            string // Operating system (linux, darwin, windows, etc.)
	Arch          string // CPU architecture (amd64, arm64, etc.)
	User          string // Current username
	HomeDir       string // Home directory
	CLIPath       string // Absolute path to coder CLI binary
	CLIVersion    string // CLI version (injected at build time)
	Locale        string // LANG environment variable
	WorkspaceRoot string // Working directory root (can differ from CWD in session mode)
	Model         string // Currently selected model (set by caller)
	Agent         string // Currently selected agent name (set by caller)
}

// DefaultEnvironmentTemplate is the built-in environment/context block
// injected into the agent's system prompt. It is used verbatim when the
// user has not supplied a custom template via `/environment`. Tokens in
// the form {{name}} are interpolated at runtime from the live Info.
const DefaultEnvironmentTemplate = `[execution-context]
- CLI: {{cli_path}} (v{{cli_version}})
- User: {{user}}@{{hostname}}
- OS: {{os}}/{{arch}}
- CWD: {{cwd}}
- Workspace: {{workspace}}
- Shell: {{shell}}
- Time: {{time}} ({{timezone}})
- Home: {{home}}
- Locale: {{locale}}
- Model: {{model}}
- Agent: {{agent}}

[co-development]
- The user can co-develop with you in VS Code using the open_in_ide tool.
- When you need to view or edit code, invoke open_in_ide to open files at specific line numbers.
- At the start of your session, ask the user if they want to co-develop in VS Code.
- This enables the user to see exactly what code you're referencing and collaborate in real-time.
`

// Build gathers runtime context with fault tolerance.
// Missing or unavailable fields are set to sensible defaults.
func Build(cliPath, cliVersion, workspaceRoot string) Info {
	info := Info{
		CLIPath:       getOrDefault(cliPath, "unknown"),
		CLIVersion:    getOrDefault(cliVersion, "dev"),
		WorkspaceRoot: getOrDefault(workspaceRoot, getCWD()),
	}

	// Current working directory
	info.CWD = getCWD()

	// Shell
	info.Shell = getEnv("SHELL", getDefaultShell())

	// Time and timezone
	now := time.Now()
	info.Time = now.Format(time.RFC3339)
	info.TimeZone = now.Location().String()

	// Hostname
	info.Hostname = getHostname()

	// OS and architecture
	info.OS = runtime.GOOS
	info.Arch = runtime.GOARCH

	// Current user
	info.User = getCurrentUser()

	// Home directory
	info.HomeDir = getHomeDir()

	// Locale
	info.Locale = getEnv("LANG", "C")

	return info
}

// tokens returns the interpolation map used by Render. Keys correspond
// to the {{name}} placeholders supported in environment templates.
func (i Info) tokens() map[string]string {
	return map[string]string{
		"cwd":         i.CWD,
		"workspace":   i.WorkspaceRoot,
		"shell":       i.Shell,
		"os":          i.OS,
		"arch":        i.Arch,
		"user":        i.User,
		"hostname":    i.Hostname,
		"time":        i.Time,
		"timezone":    i.TimeZone,
		"home":        i.HomeDir,
		"locale":      i.Locale,
		"model":       i.Model,
		"agent":       i.Agent,
		"cli_path":    i.CLIPath,
		"cli_version": i.CLIVersion,
	}
}

// tokenRe matches {{name}} placeholders (optionally padded with spaces).
var tokenRe = regexp.MustCompile(`\{\{\s*([a-zA-Z_][a-zA-Z0-9_]*)\s*\}\}`)

// Render interpolates {{name}} placeholders in tmpl using the live Info
// values. Unknown tokens are left untouched so authors can spot typos.
func (i Info) Render(tmpl string) string {
	vals := i.tokens()
	return tokenRe.ReplaceAllStringFunc(tmpl, func(match string) string {
		key := strings.ToLower(strings.TrimSpace(tokenRe.FindStringSubmatch(match)[1]))
		if v, ok := vals[key]; ok {
			return v
		}
		return match
	})
}

// FormatPrompt returns the default environment block with the live Info
// interpolated. It is retained for callers that want the built-in block
// without loading a user template.
func (i Info) FormatPrompt() string {
	return i.Render(DefaultEnvironmentTemplate)
}

// TemplatePath returns the on-disk location of the user's environment
// template for the given workspace.
func TemplatePath(workspaceRoot string) string {
	return filepath.Join(workspaceRoot, ".ergo-cli-go", "environment.tmpl")
}

// LoadTemplate returns the user's custom environment template for the
// workspace, or DefaultEnvironmentTemplate when none has been saved.
func LoadTemplate(workspaceRoot string) string {
	if workspaceRoot == "" {
		return DefaultEnvironmentTemplate
	}
	data, err := os.ReadFile(TemplatePath(workspaceRoot))
	if err != nil {
		return DefaultEnvironmentTemplate
	}
	return string(data)
}

// SaveTemplate persists a custom environment template for the workspace.
// Passing content equal to DefaultEnvironmentTemplate removes the file so
// the built-in default is used going forward.
func SaveTemplate(workspaceRoot, content string) error {
	path := TemplatePath(workspaceRoot)
	if strings.TrimSpace(content) == strings.TrimSpace(DefaultEnvironmentTemplate) {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o600)
}

// --- Fault-tolerant helpers ---

func getCWD() string {
	cwd, err := os.Getwd()
	if err != nil {
		return "unknown"
	}
	return cwd
}

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

func getOrDefault(s, fallback string) string {
	if s != "" {
		return s
	}
	return fallback
}

func getDefaultShell() string {
	// Fallback based on OS
	if runtime.GOOS == "windows" {
		return "cmd.exe"
	}
	return "/bin/bash"
}

func getHostname() string {
	hostname, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return hostname
}

func getCurrentUser() string {
	u, err := user.Current()
	if err != nil {
		return getEnv("USER", "unknown")
	}
	return u.Username
}

func getHomeDir() string {
	u, err := user.Current()
	if err != nil {
		return getEnv("HOME", "~")
	}
	return u.HomeDir
}

// ExpandPath resolves ~ to home directory for paths read from config.
// If resolution fails, returns the original path.
func ExpandPath(p string) string {
	if !strings.HasPrefix(p, "~") {
		return p
	}
	home := getHomeDir()
	if home == "~" {
		return p // Can't expand, return as-is
	}
	if p == "~" {
		return home
	}
	return filepath.Join(home, strings.TrimPrefix(p, "~/"))
}
