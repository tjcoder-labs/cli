package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/alpha-tjcoder/coder-cli/internal/agent"
	"github.com/alpha-tjcoder/coder-cli/internal/client"
	"github.com/alpha-tjcoder/coder-cli/internal/session"
)

type persistedTranscriptEntry struct {
	Role      string `json:"role"`
	Content   string `json:"content"`
	Timestamp string `json:"timestamp,omitempty"`
	Author    string `json:"author,omitempty"`
}

type persistedSession struct {
	CurrentAgent string           `json:"current_agent"`
	CurrentModel string           `json:"current_model"`
	EnabledTools []string         `json:"enabled_tools"`
	History      []client.Message `json:"history"`
	ContextInfo  string           `json:"context_info"`
	RefOrder     []string         `json:"ref_order"`
	Transcript   json.RawMessage  `json:"transcript"`
	Reasoning    string           `json:"reasoning"`
	Activity     string           `json:"activity"`
	Tasks        []session.Task   `json:"tasks,omitempty"`
}

func (a *App) sessionPath() string {
	return filepath.Join(a.workspaceRoot, ".ergo-cli-go", "session.json")
}

func (a *App) loadSession() {
	data, err := os.ReadFile(a.sessionPath())
	if err != nil {
		a.setTranscriptSplash()
		return
	}
	var state persistedSession
	if err := json.Unmarshal(data, &state); err != nil {
		return
	}

	if cfg, ok := agent.FindWithWorkspace(state.CurrentAgent, a.workspaceRoot); ok {
		a.currentAgent = cfg
	}
	if state.CurrentModel != "" {
		a.currentModel = state.CurrentModel
	}
	a.resetEnabledTools(a.currentAgent.ToolNames)
	if len(state.EnabledTools) > 0 {
		a.enabledTools = map[string]bool{}
		for _, name := range state.EnabledTools {
			a.enabledTools[name] = true
		}
	}

	a.history = append([]client.Message(nil), state.History...)
	if state.ContextInfo != "" {
		a.contextInfo = state.ContextInfo
	}
	a.refSet = map[string]struct{}{}
	a.refOrder = nil
	for _, ref := range state.RefOrder {
		if ref == "" {
			continue
		}
		if _, ok := a.refSet[ref]; ok {
			continue
		}
		a.refSet[ref] = struct{}{}
		a.refOrder = append(a.refOrder, ref)
	}

	// Rehydrate transcript: prefer structured entries, but accept the
	// legacy flat-string transcript for backward compatibility.
	if len(state.Transcript) > 0 {
		var entries []persistedTranscriptEntry
		if err := json.Unmarshal(state.Transcript, &entries); err == nil {
			var b strings.Builder
			for _, e := range entries {
				if strings.ToLower(e.Role) == "user" || strings.ToLower(e.Role) == "you" {
					b.WriteString(a.renderUserMessage(e.Content))
				} else {
					fmt.Fprintf(&b, "[%s::b]%s[-:-:-]\n", a.palette.HexPurple, a.assistantLabel())
					if e.Timestamp != "" {
						fmt.Fprintf(&b, "[%s]%s[-]\n", a.palette.HexDim, e.Timestamp)
					}
					b.WriteString(a.highlightTranscriptText(e.Content))
					b.WriteString("\n\n")
				}
			}
			a.transcript.SetText(b.String())
		} else {
			// Fall back to legacy behavior: transcript stored as a string.
			var legacy string
			if err2 := json.Unmarshal(state.Transcript, &legacy); err2 == nil {
				transcript := a.restyleLegacyUserMessages(legacy)
				transcript = a.restyleLegacyAssistantMessages(transcript)
				a.transcript.SetText(transcript)
			}
		}
	}
	a.reasoning.SetText(state.Reasoning)
	a.activity.SetText(state.Activity)
	if a.transcript.GetText(true) == "" {
		a.setTranscriptSplash()
	}
	a.sessionState.Tasks = state.Tasks
	// Header / footer / status pill are built once in build() with
	// default values, so we need to re-render them now that we've
	// restored the last-used agent + model from disk. Without this
	// the user would have to open the agent or model modal once
	// before the chosen values would show up in the UI.
	a.refreshHeader()
	a.refreshFooter()
	a.refreshContextBar()
	a.refreshStatusIndicator()
	a.appendActivity("Loaded saved session from " + a.sessionPath())
}

func (a *App) saveSession() {
	if a.workspaceRoot == "" {
		return
	}
	dir := filepath.Dir(a.sessionPath())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}

	// Build structured transcript entries by parsing the visible transcript
	// view so we can preserve role, content and timestamps for later
	// restoration.
	raw := a.transcript.GetText(true)
	entries := []persistedTranscriptEntry{}
	stripRe := regexp.MustCompile(`\[[^\]]+\]`)
	lines := strings.Split(raw, "\n")
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if strings.Contains(line, fmt.Sprintf("[%s:%s:b]", a.palette.HexMain, a.palette.HexPurple)) {
			// user bubble content: collect until blank line
			var content []string
			for j := i; j < len(lines); j++ {
				l := lines[j]
				if strings.TrimSpace(l) == "" {
					i = j
					break
				}
				// strip color tags
				cleaned := stripRe.ReplaceAllString(l, "")
				cleaned = strings.TrimSpace(cleaned)
				if cleaned != "" {
					content = append(content, cleaned)
				}
			}
			if len(content) > 0 {
				entries = append(entries, persistedTranscriptEntry{Role: "user", Content: strings.Join(content, "\n")})
			}
			continue
		}
		// assistant label
		if strings.Contains(line, "Coder is thinking") || strings.Contains(line, "Coder replied:") || strings.Contains(line, "Coder says:") {
			// collect possible timestamp on next non-empty line
			ts := ""
			next := i + 1
			for next < len(lines) && strings.TrimSpace(lines[next]) == "" {
				next++
			}
			if next < len(lines) {
				if regexp.MustCompile(`\d{1,2}:\d{2}`).MatchString(lines[next]) {
					ts = stripRe.ReplaceAllString(lines[next], "")
					i = next
				}
			}
			// collect content until blank line
			var content []string
			for j := i + 1; j < len(lines); j++ {
				if strings.TrimSpace(lines[j]) == "" {
					i = j
					break
				}
				cleaned := stripRe.ReplaceAllString(lines[j], "")
				cleaned = strings.TrimSpace(cleaned)
				if cleaned != "" {
					content = append(content, cleaned)
				}
				if j == len(lines)-1 {
					i = j
				}
			}
			entries = append(entries, persistedTranscriptEntry{Role: "assistant", Content: strings.Join(content, "\n"), Timestamp: ts})
		}
	}

	transcriptRaw, _ := json.Marshal(entries)
	state := persistedSession{
		CurrentAgent: a.currentAgent.Name,
		CurrentModel: a.currentModel,
		EnabledTools: a.enabledToolList(),
		History:      append([]client.Message(nil), a.history...),
		ContextInfo:  a.contextInfo,
		RefOrder:     append([]string(nil), a.refOrder...),
		Transcript:   transcriptRaw,
		Reasoning:    a.reasoning.GetText(true),
		Activity:     a.activity.GetText(true),
		Tasks:        append([]session.Task(nil), a.sessionState.Tasks...),
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(a.sessionPath(), data, 0o600)
}
