package tools

import (
	"context"
	"strings"
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
// the transcript as a card (the same component for sudo, updates and
// ask_user). Options are shown as "1. …", "2. …"; when AllowOther is
// set an extra free-text row is appended. With no Options and
// AllowOther the card is a plain free-text question.
type PromptRequest struct {
	Kind     PromptKind
	Title    string
	Question string
	Detail   string // e.g. the exact command an authorization covers
	Options  []string
	// Descriptions, when set, holds one optional dim line per option
	// (same index as Options).
	Descriptions []string
	AllowOther   bool
	// OtherLabel overrides the free-text row label ("Other…").
	OtherLabel string
	// MultiSelect lets the user tick several options (Space or a
	// number toggles, Enter confirms).
	MultiSelect bool
	// Default is the 1-based option the cursor starts on (0 = first).
	Default int
}

// PromptAnswer is the user's response. Index is the 0-based option
// chosen, or -1 when the user typed a free-text answer (Other) or
// cancelled (Cancelled, via Esc). Multi-select prompts fill Indices /
// Choices (in option order) and Other when a custom answer was typed;
// Index is then the first chosen option, or -1.
type PromptAnswer struct {
	Index     int
	Choice    string
	Other     string
	Cancelled bool
	Indices   []int
	Choices   []string
}

// Text returns the answer as plain text for tool results and the
// answered card.
func (a PromptAnswer) Text() string {
	switch {
	case a.Cancelled:
		return "(cancelled)"
	case len(a.Choices) > 0 || (a.Indices != nil && a.Other != ""):
		parts := append([]string(nil), a.Choices...)
		if a.Other != "" {
			parts = append(parts, a.Other)
		}
		return strings.Join(parts, ", ")
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
