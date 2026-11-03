package tui

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/rivo/tview"
	"github.com/tjcoder-labs/cli/internal/client"
	"github.com/tjcoder-labs/cli/internal/session"
)

// subsessionDirName is the directory under the workspace's .ergo-cli-go
// that holds named subsessions (one subdirectory per @name).
const subsessionDirName = "subsessions"

var sessionNameRe = regexp.MustCompile(`[^a-z0-9_-]+`)

// sanitizeSessionName normalizes an @name into a safe directory name.
func sanitizeSessionName(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = sessionNameRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		s = "main"
	}
	return s
}

// subsessionPath returns the session.json path for a named subsession.
func (a *App) subsessionPath(name string) string {
	return filepath.Join(a.workspaceRoot, session.DirName, subsessionDirName,
		sanitizeSessionName(name), session.FileName)
}

// providerCLIArg returns the --provider value that reconstructs the TUI's
// active provider for a headless subsession. Ollama must distinguish cloud
// from local (ProviderKey collapses both to "ollama").
func (a *App) providerCLIArg() string {
	switch p := a.provider.(type) {
	case *client.Ergo:
		return "ergo"
	case *client.Gemini:
		return "gemini"
	case *client.Ollama:
		if strings.Contains(p.BaseURL(), "ollama.com") {
			return "ollama-cloud"
		}
		return "ollama"
	case *client.OpenAICompatible:
		if n := client.ProviderKey(p); n != "" && n != "openai-compatible" {
			return n
		}
		return ""
	}
	return ""
}

// handleSubsessionCommand implements /ask and /btw: route a message to a
// named subsession, run it headlessly via `coder ask --session-path`, and
// surface the reply. "@name" selects (and auto-creates) the subsession.
func (a *App) handleSubsessionCommand(parts []string) {
	// parts is the fields-split command, e.g. ["btw","@reddit","hello","world"].
	name := ""
	rest := []string{}
	for i, p := range parts[1:] {
		if strings.HasPrefix(p, "@") {
			name = strings.TrimPrefix(p, "@")
			rest = parts[i+2:]
			break
		}
		rest = append(rest, p)
	}
	if name == "" {
		a.appendActivity("Usage: /ask @name <message>  (or /btw @name <message>) — @name is the subsession")
		return
	}
	msg := strings.TrimSpace(strings.Join(rest, " "))
	if msg == "" {
		a.appendActivity("Usage: /ask @name <message>  (or /btw @name <message>) — provide a message")
		return
	}

	sessionFile := a.subsessionPath(name)
	a.appendActivity(fmt.Sprintf("→ subsession @%s (%s): %s", name, sanitizeSessionName(name), truncateForActivity(msg)))

	exe, err := os.Executable()
	if err != nil {
		a.appendActivity(fmt.Sprintf("[%s::b]✗ subsession[-::B]: %v", a.palette.HexAlert, err))
		return
	}

	args := []string{
		"ask",
		"--workspace-root", a.workspaceRoot,
		"--session-path", sessionFile,
		"--quiet",
		"--agent", a.currentAgent.Name,
	}
	if prov := a.providerCLIArg(); prov != "" {
		args = append(args, "--provider", prov)
	}
	if a.currentModel != "" {
		args = append(args, "--model", a.currentModel)
	}
	args = append(args, msg)

	go func(name, sessionFile, msg string) {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, exe, args...)
		var out bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &out
		runErr := cmd.Run()
		reply := strings.TrimSpace(out.String())
		a.postUI(func() {
			if runErr != nil {
				a.appendActivity(fmt.Sprintf("[%s::b]✗ subsession @%s[-::B] failed: %v", a.palette.HexAlert, name, runErr))
			}
			if reply != "" {
				a.appendSubsessionReply(name, reply)
			} else {
				a.appendActivity(fmt.Sprintf("subsession @%s produced no reply", name))
			}
		})
	}(name, sessionFile, msg)
}

// appendSubsessionReply renders a subsession's answer into the transcript
// and activity stream under an @name attribution.
func (a *App) appendSubsessionReply(name, reply string) {
	if a.transcript != nil {
		a.clearStartupSplash()
		fmt.Fprintf(a.transcript, "[%s]%s[-]\n", a.palette.HexDim, formatTimestamp(time.Now()))
		fmt.Fprintf(a.transcript, "[%s::b]@%s[-:-:-]\n%s\n\n", a.palette.HexLavender, tview.Escape(name), tview.Escape(reply))
		a.transcript.ScrollToEnd()
	}
	a.appendActivity(fmt.Sprintf("subsession @%s replied", name))
}
