package context

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
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
}

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

// FormatPrompt returns a formatted block suitable for injecting into system prompts.
func (i Info) FormatPrompt() string {
	var b strings.Builder
	b.WriteString("[execution-context]\n")
	b.WriteString(fmt.Sprintf("- CLI: %s (v%s)\n", i.CLIPath, i.CLIVersion))
	b.WriteString(fmt.Sprintf("- User: %s@%s\n", i.User, i.Hostname))
	b.WriteString(fmt.Sprintf("- OS: %s/%s\n", i.OS, i.Arch))
	b.WriteString(fmt.Sprintf("- CWD: %s\n", i.CWD))
	b.WriteString(fmt.Sprintf("- Workspace: %s\n", i.WorkspaceRoot))
	b.WriteString(fmt.Sprintf("- Shell: %s\n", i.Shell))
	b.WriteString(fmt.Sprintf("- Time: %s (%s)\n", i.Time, i.TimeZone))
	b.WriteString(fmt.Sprintf("- Home: %s\n", i.HomeDir))
	b.WriteString(fmt.Sprintf("- Locale: %s\n", i.Locale))
	b.WriteString("\n[co-development]\n")
	b.WriteString("- The user can co-develop with you in VS Code using the open_in_ide tool.\n")
	b.WriteString("- When you need to view or edit code, invoke open_in_ide to open files at specific line numbers.\n")
	b.WriteString("- At the start of your session, ask the user if they want to co-develop in VS Code.\n")
	b.WriteString("- This enables the user to see exactly what code you're referencing and collaborate in real-time.\n")
	return b.String()
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
