package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/tjcoder-labs/cli/internal/agent"
	"github.com/tjcoder-labs/cli/internal/client"
	"github.com/tjcoder-labs/cli/internal/session"
)

// thinkBlockRe matches inline chain-of-thought blocks (including empty
// ones) that some models emit directly in message content. These are
// routed to the cognition pane live via the runner's thoughtParser, but
// the raw content stored in history retains them. We strip them when
// rendering the transcript on reload so stale (often empty)
// <think></think> markers do not appear in the conversation pane.
var thinkBlockRe = regexp.MustCompile(`(?is)<think>.*?</think>`)

// strayThinkTagRe catches unbalanced <think> / </think> tags left over
// when a block was never closed in the stored content.
var strayThinkTagRe = regexp.MustCompile(`(?i)</?think>`)

// stripThinkBlocks removes inline reasoning blocks from display content.
func stripThinkBlocks(s string) string {
	s = thinkBlockRe.ReplaceAllString(s, "")
	s = strayThinkTagRe.ReplaceAllString(s, "")
	return strings.TrimSpace(s)
}

func (a *App) sessionPath() string {
	return filepath.Join(a.workspaceRoot, ".ergo-cli-go", "session.json")
}

func (a *App) loadSession() {
	state, exists, err := session.Load(a.workspaceRoot)
	if err != nil || !exists {
		a.setTranscriptSplash()
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

	if len(state.Transcript) > 0 {
		var b strings.Builder
		for _, e := range state.Transcript {
			role := strings.ToLower(e.Role)
			switch role {
			case "user", "you":
				b.WriteString(a.renderUserMessage(e.Content))
			case "assistant":
				content := stripThinkBlocks(e.Content)
				if content == "" {
					continue
				}
				if e.Timestamp != "" {
					fmt.Fprintf(&b, "[%s]%s[-]\n", a.palette.HexDim, e.Timestamp)
				}
				fmt.Fprintf(&b, "[%s::b]%s[-:-:-]\n", a.palette.HexPurple, a.assistantLabel())
				b.WriteString(a.highlightTranscriptText(content))
				b.WriteString("\n\n")
			default:
				// Skip tool/system messages: they are part of History
				// (replayed to the model) but are not conversation turns
				// the user should see rendered as assistant replies.
			}
		}
		a.transcript.SetText(b.String())
	}
	a.reasoning.SetText(state.Reasoning)
	a.activity.SetText(state.Activity)
	if a.transcript.GetText(true) == "" {
		a.setTranscriptSplash()
	}
	a.sessionState = state
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

func (a *App) saveSession() error {
	if a.workspaceRoot == "" {
		return nil
	}
	
	// Build the transcript from real conversation turns only. Tool and
	// system messages live in History (and are replayed to the model)
	// but must not be rendered as assistant replies on reload.
	entries := []session.TranscriptEntry{}
	for _, msg := range a.history {
		role := strings.ToLower(msg.Role)
		if role != "user" && role != "assistant" {
			continue
		}
		entries = append(entries, session.TranscriptEntry{
			Role:    msg.Role,
			Content: msg.Content,
		})
	}

	newState := session.State{
		CurrentAgent:   a.currentAgent.Name,
		CurrentModel:   a.currentModel,
		ToolMax:        a.sessionState.ToolMax,
		EnabledTools:   a.enabledToolList(),
		History:        append([]client.Message(nil), a.history...),
		ContextInfo:    a.contextInfo,
		RefOrder:       append([]string(nil), a.refOrder...),
		Transcript:     entries,
		Reasoning:      a.reasoning.GetText(true),
		Activity:       a.activity.GetText(true),
		Tasks:          append([]session.Task(nil), a.sessionState.Tasks...),
		Articles:       append([]session.Article(nil), a.sessionState.Articles...),
		Memories:       append([]session.Memory(nil), a.sessionState.Memories...),
		BackgroundJobs: append([]session.BackgroundJob(nil), a.sessionState.BackgroundJobs...),
	}
	
	// Safety check: prevent overwriting a valid session with empty data.
	// If the new state has no history/tasks/reasoning AND a valid session file
	// already exists on disk, skip this save to preserve existing data
	// (this can happen on early app exit or crash before full load).
	if len(newState.History) == 0 && len(newState.Tasks) == 0 && strings.TrimSpace(newState.Reasoning) == "" {
		// Check if an existing session has content we'd be about to lose
		if existing, exists, _ := session.Load(a.workspaceRoot); exists && 
		   (len(existing.History) > 0 || len(existing.Tasks) > 0 || strings.TrimSpace(existing.Reasoning) != "") {
			// Skip save to preserve existing session
			return nil
		}
	}
	dir := filepath.Dir(a.sessionPath())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	if err := session.Save(a.workspaceRoot, newState); err != nil {
		return err
	}
	return nil
}
