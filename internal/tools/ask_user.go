package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/tjcoder-labs/cli/internal/client"
)

// askUserMaxOptions caps the numbered choices on an ask_user card so
// every option has a single-digit hotkey and the card stays short.
const askUserMaxOptions = 6

// Result strings returned to the model. They start with a fixed
// upper-case status word so the outcome is unambiguous.
const (
	AskAnswered     = "ANSWERED"
	AskDismissed    = "DISMISSED: the user dismissed the question; do not re-ask, proceed with your best judgment or stop."
	AskUnavailable  = "UNAVAILABLE: no interactive user; proceed with best judgment."
	AskInterrupted  = "INTERRUPTED: the user sent a new message instead of answering; do not re-ask, follow their new message."
	AskCancelled    = "CANCELLED: the turn was cancelled while waiting for the user's answer."
	askDeniedSuffix = " Treat this as declined: do not perform the action."
)

// AskUserGuidance is appended to the system prompt whenever ask_user is
// among the enabled tools.
const AskUserGuidance = `Asking the user (ask_user):
- Call ask_user only when a decision truly needs the user (ambiguous request, a choice between materially different approaches, or explicit approval before something destructive or irreversible). Otherwise decide yourself and say what you assumed.
- Ask one question at a time and wait for the answer; never batch several questions into one call.
- Keep the question short (one sentence) and prefer 2-6 concrete options, most likely first; set default to the recommended one. The user can always type a custom answer unless allow_custom is false.
- Results start with ANSWERED, DISMISSED, UNAVAILABLE, INTERRUPTED or CANCELLED. On DISMISSED or UNAVAILABLE do not ask again: proceed with your best judgment (or the default) or stop and explain.`

// ErrSteered is returned by a Prompter when a prompt was abandoned
// because the user sent a new (steering) message instead of answering.
var ErrSteered = errors.New("prompt interrupted by a new user message")

// askOption accepts either "label" or {"label": …, "description": …}.
type askOption struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}

func (o *askOption) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		o.Label = s
		return nil
	}
	type plain askOption
	var p plain
	if err := json.Unmarshal(b, &p); err != nil {
		return fmt.Errorf("option must be a string or {label, description}: %w", err)
	}
	*o = askOption(p)
	return nil
}

type askUserArgs struct {
	Question    string          `json:"question"`
	Header      string          `json:"header"`
	Title       string          `json:"title"` // alias for header
	Options     []askOption     `json:"options"`
	AllowCustom *bool           `json:"allow_custom"`
	AllowOther  *bool           `json:"allow_other"` // legacy alias
	MultiSelect bool            `json:"multi_select"`
	Default     json.RawMessage `json:"default"`
	Kind        string          `json:"kind"`
	Detail      string          `json:"detail"`
}

// askUserTool lets an agent ask the user a question (clarification,
// authorization, or "what next?") on the same in-transcript card as
// the sudo gate, and get a structured answer back.
type askUserTool struct{}

func (askUserTool) Definition() client.ToolDefinition {
	return client.ToolDefinition{
		Type: "function",
		Function: client.FunctionDefinition{
			Name: "ask_user",
			Description: "Ask the user ONE short question on an inline card in the transcript and wait for the answer. " +
				"Offer up to 6 numbered options (the user picks with a number key or arrows+Enter); a free-text answer row is added unless allow_custom is false; " +
				"with no options it is a free-text question. Esc dismisses. Use only when a decision truly needs the user. " +
				"Returns 'ANSWERED: <label>', 'ANSWERED (custom): <text>', 'ANSWERED (multiple): …', 'DISMISSED: …', or 'UNAVAILABLE: …' (non-interactive session).",
			Parameters: objectSchema([]string{"question"}, map[string]any{
				"question": stringProp("The question, one short sentence."),
				"header":   stringProp("Optional short card title (2-4 words), e.g. 'Deploy target'. Defaults to QUESTION."),
				"options": map[string]any{
					"type":        "array",
					"maxItems":    askUserMaxOptions,
					"description": "Up to 6 choices, most likely first. Omit for a free-text question.",
					"items": map[string]any{
						"type":     "object",
						"required": []string{"label"},
						"properties": map[string]any{
							"label":       stringProp("Short option text (a few words)."),
							"description": stringProp("Optional one-line explanation shown under the option."),
						},
					},
				},
				"allow_custom": boolProp("Offer a free-text answer row (default true)."),
				"multi_select": boolProp("Let the user choose several options (default false)."),
				"default":      stringProp("Label (or 1-based number) of the recommended option: the cursor starts there, and it is used when no user is available."),
				"kind":         stringProp("One of question (default), authorization, next_steps. For authorization, a dismissal means denied."),
				"detail":       stringProp("Optional detail shown under the question, e.g. the exact command or file affected."),
			}),
		},
	}
}

// parse validates raw arguments into a PromptRequest.
func (askUserTool) parse(raw json.RawMessage) (PromptRequest, error) {
	var args askUserArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return PromptRequest{}, fmt.Errorf("invalid ask_user arguments: %w", err)
	}
	req := PromptRequest{Kind: PromptQuestion}
	req.Question = strings.TrimSpace(args.Question)
	if req.Question == "" {
		return req, fmt.Errorf("question is required")
	}
	req.Title = strings.TrimSpace(args.Header)
	if req.Title == "" {
		req.Title = strings.TrimSpace(args.Title)
	}
	if r := []rune(req.Title); len(r) > 40 {
		req.Title = string(r[:39]) + "…"
	}
	for _, o := range args.Options {
		label := strings.TrimSpace(o.Label)
		if label == "" {
			continue
		}
		req.Options = append(req.Options, label)
		req.Descriptions = append(req.Descriptions, strings.TrimSpace(o.Description))
	}
	if len(req.Options) > askUserMaxOptions {
		return req, fmt.Errorf("at most %d options are allowed (got %d); ask a narrower question", askUserMaxOptions, len(req.Options))
	}
	req.AllowOther = true
	if args.AllowOther != nil {
		req.AllowOther = *args.AllowOther
	}
	if args.AllowCustom != nil {
		req.AllowOther = *args.AllowCustom
	}
	if len(req.Options) == 0 && !req.AllowOther {
		return req, fmt.Errorf("provide options, or allow_custom for a free-text question")
	}
	if len(req.Options) == 0 {
		req.OtherLabel = "Type your answer…"
	}
	req.MultiSelect = args.MultiSelect && len(req.Options) > 0
	if d := strings.TrimSpace(strings.Trim(strings.TrimSpace(string(args.Default)), `"`)); d != "" && d != "null" {
		idx := 0
		if n, err := strconv.Atoi(d); err == nil && n >= 1 && n <= len(req.Options) {
			idx = n
		} else {
			for i, o := range req.Options {
				if strings.EqualFold(o, d) {
					idx = i + 1
					break
				}
			}
		}
		if idx == 0 {
			return req, fmt.Errorf("default %q does not match any option label", d)
		}
		req.Default = idx
	}
	switch k := PromptKind(strings.TrimSpace(args.Kind)); k {
	case PromptAuthorization, PromptNextSteps:
		req.Kind = k
	}
	req.Detail = strings.TrimSpace(args.Detail)
	return req, nil
}

func (t askUserTool) Execute(ctx context.Context, raw json.RawMessage, env ExecEnv) (Result, error) {
	req, err := t.parse(raw)
	if err != nil {
		return Result{}, err
	}
	if env.Prompter == nil {
		content := AskUnavailable
		if req.Default > 0 {
			content = "UNAVAILABLE: no interactive user; using the default option: " + req.Options[req.Default-1]
		}
		return Result{Content: content, Preview: "ask_user: no interactive user"}, nil
	}
	ans, err := env.Prompter.Ask(ctx, req)
	switch {
	case errors.Is(err, ErrSteered), err != nil && errors.Is(context.Cause(ctx), ErrSteered):
		return Result{Content: AskInterrupted, Preview: "ask_user: interrupted by a new message"}, nil
	case err != nil && ctx.Err() != nil:
		return Result{Content: AskCancelled, Preview: "ask_user: cancelled"}, nil
	case err != nil:
		return Result{}, err
	}
	content := FormatAskAnswer(req, ans)
	return Result{Content: content, Preview: preview(content)}, nil
}

// FormatAskAnswer renders ans as the structured ask_user result.
func FormatAskAnswer(req PromptRequest, ans PromptAnswer) string {
	if ans.Cancelled {
		if req.Kind == PromptAuthorization {
			return AskDismissed + askDeniedSuffix
		}
		return AskDismissed
	}
	if req.MultiSelect {
		var b strings.Builder
		b.WriteString(AskAnswered + " (multiple):")
		for _, c := range ans.Choices {
			b.WriteString("\n- " + c)
		}
		if ans.Other != "" {
			b.WriteString("\n- (custom) " + ans.Other)
		}
		return b.String()
	}
	if ans.Index < 0 {
		return AskAnswered + " (custom): " + ans.Other
	}
	return AskAnswered + ": " + ans.Choice
}
