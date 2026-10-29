package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tjcoder-labs/cli/internal/client"
)

// PromptKind classifies an interactive transcript prompt so the TUI can
// title and colour it consistently.
type PromptKind string

const (
	PromptQuestion      PromptKind = "question"
	PromptAuthorization PromptKind = "authorization"
	PromptNextSteps     PromptKind = "next_steps"
)

// PromptRequest describes a numbered-choice prompt rendered inline in
// the transcript. Options are shown as "1. …", "2. …"; when AllowOther
// is set an extra free-text "Other…" entry is appended.
type PromptRequest struct {
	Kind       PromptKind
	Title      string
	Question   string
	Detail     string // e.g. the exact command an authorization covers
	Options    []string
	AllowOther bool
}

// PromptAnswer is the user's response. Index is the 0-based option
// chosen, or -1 when the user typed a free-text answer (Other) or
// cancelled (Cancelled, via Esc).
type PromptAnswer struct {
	Index     int
	Choice    string
	Other     string
	Cancelled bool
}

// Text returns the answer as plain text for tool results.
func (a PromptAnswer) Text() string {
	switch {
	case a.Cancelled:
		return "(cancelled)"
	case a.Index < 0:
		return a.Other
	default:
		return a.Choice
	}
}

// Prompter is implemented by the TUI so tools can ask the user
// questions inline in the transcript. nil in headless mode.
//
// Ask and AskSecret block until the user answers or ctx is done; they
// are called from the runner goroutine, never the UI goroutine.
// AskSecret returns the typed bytes (masked on screen, never echoed to
// the transcript); the caller owns the slice and must zero it after use.
// Record appends a one-line note to the transcript (e.g. an
// authorization decision); it must never be given secret material.
type Prompter interface {
	Ask(ctx context.Context, req PromptRequest) (PromptAnswer, error)
	AskSecret(ctx context.Context, title, label string) ([]byte, error)
	Record(text string)
}

// askUserTool lets an agent ask the user a question (clarification,
// authorization, or "what next?") and get the chosen option back.
type askUserTool struct{}

func (askUserTool) Definition() client.ToolDefinition {
	return client.ToolDefinition{
		Type: "function",
		Function: client.FunctionDefinition{
			Name: "ask_user",
			Description: "Ask the user a question in the transcript and wait for their answer. Offer 2-9 short numbered options; " +
				"the user picks one with a number key or arrows+Enter, or presses Esc to decline. Use it to clarify ambiguous requests, " +
				"to get explicit authorization before destructive or irreversible actions, and to suggest next steps. " +
				"Returns the chosen option text (or the user's free-text answer when allow_other is true).",
			Parameters: objectSchema([]string{"question", "options"}, map[string]any{
				"question": stringProp("The question to show, one or two sentences."),
				"options": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Choices to offer, most likely first (max 9).",
				},
				"kind":        stringProp("One of question (default), authorization, next_steps."),
				"detail":      stringProp("Optional detail shown under the question, e.g. the exact command or file affected."),
				"allow_other": boolProp("If true, add an 'Other…' option that lets the user type a free-text answer."),
			}),
		},
	}
}

func (askUserTool) Execute(ctx context.Context, raw json.RawMessage, env ExecEnv) (Result, error) {
	var args struct {
		Question   string   `json:"question"`
		Options    []string `json:"options"`
		Kind       string   `json:"kind"`
		Detail     string   `json:"detail"`
		AllowOther bool     `json:"allow_other"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return Result{}, err
	}
	args.Question = strings.TrimSpace(args.Question)
	if args.Question == "" {
		return Result{}, fmt.Errorf("question is required")
	}
	opts := make([]string, 0, len(args.Options))
	for _, o := range args.Options {
		if o = strings.TrimSpace(o); o != "" {
			opts = append(opts, o)
		}
	}
	if len(opts) > 9 {
		opts = opts[:9]
	}
	if len(opts) == 0 && !args.AllowOther {
		return Result{}, fmt.Errorf("at least one option is required (or set allow_other)")
	}
	if env.Prompter == nil {
		msg := "The user is not available to answer (non-interactive session). Do not wait for an answer: proceed with the safest reasonable default, or stop and explain what you need."
		return Result{Content: msg, Preview: "ask_user unavailable (headless)"}, nil
	}
	kind := PromptKind(strings.TrimSpace(args.Kind))
	switch kind {
	case PromptQuestion, PromptAuthorization, PromptNextSteps:
	default:
		kind = PromptQuestion
	}
	ans, err := env.Prompter.Ask(ctx, PromptRequest{
		Kind:       kind,
		Question:   args.Question,
		Detail:     strings.TrimSpace(args.Detail),
		Options:    opts,
		AllowOther: args.AllowOther,
	})
	if err != nil {
		return Result{}, err
	}
	var content string
	switch {
	case ans.Cancelled:
		content = "The user dismissed the question without choosing (treat as no / declined)."
	case ans.Index < 0:
		content = "The user answered: " + ans.Other
	default:
		content = fmt.Sprintf("The user chose option %d: %s", ans.Index+1, ans.Choice)
	}
	return Result{Content: content, Preview: preview(content)}, nil
}
