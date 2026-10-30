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
	AskAnswered    = "ANSWERED"
	AskDismissed   = "DISMISSED: the user dismissed the question; do not re-ask, proceed with your best judgment or stop."
	AskUnavailable = "UNAVAILABLE: no interactive user; the question was not answered and no default was applied. Proceed with best judgment or stop."
	// AskUnavailableAuth is the headless result for an authorization
	// question: nothing was approved, whatever the default says.
	AskUnavailableAuth = "UNAVAILABLE: no interactive user; authorization NOT granted. Do not proceed with the privileged action."
	AskInterrupted     = "INTERRUPTED: the user sent a new message instead of answering; do not re-ask, follow their new message."
	AskCancelled       = "CANCELLED: the turn was cancelled while waiting for the user's answer."
	askDeniedSuffix    = " Treat this as declined: do not perform the action."
)

// AskUserGuidance is appended to the system prompt whenever ask_user is
// among the enabled tools.
const AskUserGuidance = `Asking the user (ask_user):
- Call ask_user only when a decision truly needs the user (ambiguous request, a choice between materially different approaches, or explicit approval before something destructive or irreversible). Otherwise decide yourself and say what you assumed.
- Ask one question at a time and wait for the answer; never batch several questions into one call.
- Keep the question short (one sentence) and prefer 2-6 concrete options, most likely first; set default to the recommended one. The default only pre-selects that option on the card: it is NEVER applied automatically, and only an explicit user answer counts. The user can always type a custom answer unless allow_custom is false.
- Results start with ANSWERED, DISMISSED, UNAVAILABLE, INTERRUPTED or CANCELLED. Only ANSWERED is a user decision. On DISMISSED or UNAVAILABLE do not ask again: proceed with your best judgment or stop and explain. On INTERRUPTED follow the user's new message; on CANCELLED stop.
- An authorization (kind authorization) is granted only by an ANSWERED result that approves it. DISMISSED, UNAVAILABLE, INTERRUPTED and CANCELLED all mean NOT approved: do not perform the action.`

// AskUserHeadlessGuidance is added after AskUserGuidance when the
// session has no interactive user (no Prompter).
const AskUserHeadlessGuidance = `- This session is non-interactive: ask_user always returns UNAVAILABLE and no default is applied. Nothing that needs authorization is approved; do not perform it.`

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
				"Returns 'ANSWERED: <label>', 'ANSWERED (custom): <text>', 'ANSWERED (multiple): …', 'DISMISSED: …', 'INTERRUPTED: …' (the user sent a new message instead), 'CANCELLED: …' (the turn was cancelled), or 'UNAVAILABLE: …' (non-interactive session). " +
				"The default is never auto-applied: without an explicit user answer there is no answer. An authorization is granted only by an ANSWERED approval; every other outcome means not approved.",
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
				"default":      stringProp("Label (or 1-based number) of the recommended option. It only pre-selects that row on the card; it is never used as an answer (not even when no user is available)."),
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
		// Never answer on the user's behalf: a default is only the
		// card's pre-selected row, not an answer. Headless there is no
		// card, so there is no answer, and an authorization is denied.
		return Result{Content: unavailableResult(req), Preview: "ask_user: no interactive user"}, nil
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

// unavailableResult is the result when nobody can answer req.
func unavailableResult(req PromptRequest) string {
	if req.Kind == PromptAuthorization {
		return AskUnavailableAuth
	}
	return AskUnavailable
}

// emptyAnswer reports a non-cancelled answer that carries no choice and
// no text, i.e. nothing the user actually picked or typed.
func emptyAnswer(ans PromptAnswer) bool {
	return !ans.Cancelled && ans.Choice == "" && strings.TrimSpace(ans.Other) == "" && len(ans.Choices) == 0
}

// FormatAskAnswer renders ans as the structured ask_user result.
// An answer with nothing picked or typed is never reported as
// ANSWERED (that would let an empty/zero answer stand in for the
// pre-selected default); it is treated as a dismissal.
func FormatAskAnswer(req PromptRequest, ans PromptAnswer) string {
	if ans.Cancelled || emptyAnswer(ans) {
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
