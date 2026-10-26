package client

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
)

// ErgoDefaultModel is the agent used when nothing valid is remembered.
const ErgoDefaultModel = "ergo"

// ErgoAgent describes one of the gateway's models ("agents").
type ErgoAgent struct {
	Name string
	Desc string
}

// ErgoAgents is the hardcoded fallback set (and the source of the
// one-line descriptions) used when /v1/models is unavailable.
var ErgoAgents = []ErgoAgent{
	{Name: "ergo", Desc: "balanced everyday agent (default)"},
	{Name: "ergo-lite", Desc: "fast and economical"},
	{Name: "ergo-genius", Desc: "deepest reasoning for hard problems"},
}

// ErgoFallbackModels returns the names of ErgoAgents.
func ErgoFallbackModels() []string {
	out := make([]string, 0, len(ErgoAgents))
	for _, a := range ErgoAgents {
		out = append(out, a.Name)
	}
	return out
}

// ErgoAgentDesc returns the one-line description for a known agent ("" if unknown).
func ErgoAgentDesc(name string) string {
	for _, a := range ErgoAgents {
		if a.Name == name {
			return a.Desc
		}
	}
	return ""
}

// ErgoModelAllowed reports whether name is in the available set.
func ErgoModelAllowed(name string, available []string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	for _, m := range available {
		if m == name {
			return true
		}
	}
	return false
}

// ResolveErgoModel picks the model to use for the ergo provider. Priority:
// an explicit choice (returned as-is — the gateway is the authority and the
// user asked for it), then the saved value if it is a gateway model, then
// the default ("ergo", or the first available model if "ergo" is not served).
func ResolveErgoModel(explicit, saved string, available []string) string {
	if e := strings.TrimSpace(explicit); e != "" {
		return e
	}
	if ErgoModelAllowed(saved, available) {
		return strings.TrimSpace(saved)
	}
	if len(available) == 0 || ErgoModelAllowed(ErgoDefaultModel, available) {
		return ErgoDefaultModel
	}
	return available[0]
}

// ErgoAvailableModels returns the gateway's model names, fetched from
// GET /v1/models, falling back to the hardcoded set on any failure or an
// empty/blank list. It never returns an empty slice.
func ErgoAvailableModels(ctx context.Context, p Provider) []string {
	e, ok := p.(*Ergo)
	if !ok || e == nil || !e.Authenticated() {
		return ErgoFallbackModels()
	}
	infos, err := e.ListModels(ctx)
	if err != nil {
		return ErgoFallbackModels()
	}
	var out []string
	for _, m := range infos {
		if n := strings.TrimSpace(m.Name); n != "" {
			out = append(out, n)
		}
	}
	if len(out) == 0 {
		return ErgoFallbackModels()
	}
	return out
}

// ErgoModelNotFoundError is returned when the gateway rejects the requested
// model (404 / code "model_not_found").
type ErgoModelNotFoundError struct {
	Model     string
	Available []string
	Message   string
}

func (e *ErgoModelNotFoundError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return "unknown ergo model " + e.Model
}

// IsModelNotFound reports whether err is an ergo model_not_found.
func IsModelNotFound(err error) bool {
	var m *ErgoModelNotFoundError
	return errors.As(err, &m)
}

var availableListRe = regexp.MustCompile(`(?i)available:\s*([^"\n]+)`)

// parseErgoModelNotFound recognises the gateway's model_not_found body
// {"error":{"code":"model_not_found","message":"Unknown model \"x\". Available: a, b."}}.
func parseErgoModelNotFound(status int, body []byte, requested string) *ErgoModelNotFoundError {
	var parsed struct {
		Error struct {
			Code    any    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil
	}
	code, _ := parsed.Error.Code.(string)
	if code != "model_not_found" {
		return nil
	}
	out := &ErgoModelNotFoundError{Model: requested, Message: parsed.Error.Message}
	if m := availableListRe.FindStringSubmatch(parsed.Error.Message); m != nil {
		for _, name := range strings.Split(m[1], ",") {
			name = strings.Trim(strings.TrimSpace(name), `.`)
			if name != "" {
				out.Available = append(out.Available, name)
			}
		}
	}
	return out
}

// ProviderKey returns the prefs key for a provider instance: "ergo",
// "gemini", or "ollama" (local and hosted Ollama share Ollama-style names).
func ProviderKey(p Provider) string {
	switch p.(type) {
	case *Ergo:
		return "ergo"
	case *Gemini:
		return "gemini"
	default:
		return "ollama"
	}
}
