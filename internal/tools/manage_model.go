package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/tjcoder-labs/cli/internal/client"
)

// ManageModelArgs are the parameters for the manage_model tool.
type ManageModelArgs struct {
	// Model: the exact model name to switch to (e.g. "llama3.2",
	// "gemma3:cloud", "tjcoder/ergo-pro:cloud"). When set, it wins over
	// Direction. Works with any provider.
	Model string `json:"model,omitempty"`
	// Direction: "up" (more reasoning), "down" (faster), "auto" (let the
	// provider pick the best tier). Only meaningful for tjcoder/ergo-*
	// tiers; ignored when Model is set.
	Direction string `json:"direction,omitempty"`
	// Think: "on" (force reasoning), "off" (disable), "auto" (provider default)
	Think string `json:"think,omitempty"`
	// AutoTier: let the provider pick the best tier for the task
	AutoTier bool `json:"auto_tier,omitempty"`
	// Persist: save choice to prefs (default true)
	Persist bool `json:"persist,omitempty"`
}

// ManageModelResult is what the tool returns.
type ManageModelResult struct {
	Model    string `json:"model"`
	Think    string `json:"think"`
	AutoTier bool   `json:"auto_tier"`
	Note     string `json:"note"`
}

// manageModelTool switches the active model. It works with any provider:
// ergo tiers (via Direction), or an explicit Model for Ollama local,
// Ollama Cloud, Gemini, or any OpenAI-compatible gateway.
type manageModelTool struct{}

func (manageModelTool) Definition() client.ToolDefinition {
	return client.ToolDefinition{
		Type: "function",
		Function: client.FunctionDefinition{
			Name:        "manage_model",
			Description: "Switch the active model (or ergo tier) and control thinking. Pass `model` with an exact name to switch to any model served by the current provider (Ollama local, Ollama Cloud, Gemini, ergo tiers, etc.); pass `direction` up/down to move between ergo tiers. `think` toggles reasoning.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"model": map[string]any{
						"type":        "string",
						"description": "Exact model name to switch to (e.g. llama3.2, gemma3:cloud, tjcoder/ergo-pro:cloud). Wins over direction. Any provider.",
					},
					"direction": map[string]any{
						"type":        "string",
						"description": "Tier direction: 'up' for more reasoning (flash→pro→genius), 'down' for speed, 'auto' to let provider pick. Ergo tiers only; ignored when model is set.",
						"enum":        []string{"up", "down", "auto"},
						"default":     "auto",
					},
					"think": map[string]any{
						"type":        "string",
						"description": "Thinking mode: 'on' forces reasoning stream, 'off' disables reasoning, 'auto' uses provider default",
						"enum":        []string{"on", "off", "auto"},
						"default":     "auto",
					},
					"auto_tier": map[string]any{
						"type":        "boolean",
						"description": "If true, provider picks the best tier for the task (overrides direction)",
						"default":     false,
					},
					"persist": map[string]any{
						"type":        "boolean",
						"description": "Save the choice to preferences (survives restart)",
						"default":     true,
					},
				},
				"required": []string{},
			},
		},
	}
}

func (manageModelTool) Execute(ctx context.Context, args json.RawMessage, env ExecEnv) (Result, error) {
	var params ManageModelArgs
	if len(args) > 0 {
		if err := json.Unmarshal(args, &params); err != nil {
			return Result{}, fmt.Errorf("invalid args: %w", err)
		}
	}

	// Defaults
	if params.Direction == "" {
		params.Direction = "auto"
	}
	if params.Think == "" {
		params.Think = "auto"
	}
	if !params.Persist {
		params.Persist = true // default true
	}
	validThink := map[string]bool{"on": true, "off": true, "auto": true}
	if !validThink[params.Think] {
		params.Think = "auto"
	}

	currentModel := ""
	if env.SessionState != nil && env.SessionState.CurrentModel != "" {
		currentModel = env.SessionState.CurrentModel
	}

	// Enumerate the models the active provider serves (best-effort; a
	// provider without a working /tags or /models endpoint yields nil).
	available := listAvailableModels(ctx, env.Provider)

	// Determine the target model.
	targetModel, note := resolveTarget(params, currentModel, available)

	// An explicit model must be in the provider's catalogue when that
	// catalogue is known; otherwise we'd let the agent pick a non-existent
	// model and the next chat would fail with model_not_found.
	if m := strings.TrimSpace(params.Model); m != "" && len(available) > 0 && !contains(available, m) {
		return Result{}, fmt.Errorf("model %q is not served by this provider; available: %s", m, strings.Join(available, ", "))
	}

	// Persist if requested. In TUI mode, route the switch through the CLI
	// command sink (/model <name>) so the live app's currentModel and
	// context window update immediately; the sink is absent headless, so
	// fall back to mutating the session state directly.
	if params.Persist {
		if sink := env.CLICommandSink(); sink != nil && targetModel != "" && targetModel != currentModel {
			if err := sink.InvokeCLICommand("model " + targetModel); err != nil {
				return Result{}, fmt.Errorf("failed to switch model: %w", err)
			}
		} else if env.SessionState != nil {
			env.SessionState.CurrentModel = targetModel
			env.SessionState.ThinkMode = params.Think
			env.SessionState.AutoTier = params.AutoTier
			if env.PersistSession != nil {
				_ = env.PersistSession()
			}
		}
	}

	res := ManageModelResult{
		Model:    targetModel,
		Think:    params.Think,
		AutoTier: params.AutoTier,
		Note:     note,
	}

	out, _ := json.Marshal(res)
	return Result{Content: string(out), Preview: note}, nil
}

// listAvailableModels returns the model names the provider serves, or nil
// if they cannot be determined. It is best-effort: a network failure does
// not abort the switch. For the ergo gateway it uses the full unified
// catalogue (ListAllModels: ergo tiers plus xai/, ollama/, google/, …)
// rather than the narrower /v1/models tier list, so an agent can pick any
// model the gateway actually routes to.
func listAvailableModels(ctx context.Context, p client.Provider) []string {
	if p == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := p.(*client.Ergo); ok {
		return client.ErgoAvailableModels(ctx, p)
	}
	infos, err := p.ListModels(ctx)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(infos))
	for _, m := range infos {
		if m.Name != "" {
			names = append(names, m.Name)
		}
	}
	sort.Strings(names)
	return names
}

// ergoTiers is the ordered ergo tier list used for up/down direction.
var ergoTiers = []string{
	"tjcoder/ergo-flash:cloud",
	"tjcoder/ergo-pro:cloud",
	"tjcoder/ergo-genius:cloud",
}

// resolveTarget computes the model to switch to and a human note. Explicit
// Model wins. Direction up/down walks the ergo tier ladder. auto keeps the
// current model and enables auto-tier.
func resolveTarget(params ManageModelArgs, currentModel string, available []string) (string, string) {
	// Explicit model: switch directly. The caller has already validated it
	// against the available set when one is known.
	if m := strings.TrimSpace(params.Model); m != "" {
		note := fmt.Sprintf("Switched to %s", m)
		if params.Think != "auto" {
			note += fmt.Sprintf(", think=%s", params.Think)
		}
		return m, note
	}

	// No explicit model: direction up/down only applies to ergo tiers.
	if isErgoTier(currentModel) {
		idx := tierIndex(currentModel)
		var target string
		var dir string
		switch params.Direction {
		case "up":
			if idx >= 0 && idx < len(ergoTiers)-1 {
				target = ergoTiers[idx+1]
			} else {
				target = ergoTiers[len(ergoTiers)-1]
			}
			dir = "up"
		case "down":
			if idx > 0 {
				target = ergoTiers[idx-1]
			} else {
				target = ergoTiers[0]
			}
			dir = "down"
		default:
			params.AutoTier = true
			note := fmt.Sprintf("Keeping %s (auto-tier enabled)", currentModel)
			if params.Think != "auto" {
				note += fmt.Sprintf(", think=%s", params.Think)
			}
			return currentModel, note
		}
		note := fmt.Sprintf("Tier %s: %s → %s", dir, currentModel, target)
		if params.Think != "auto" {
			note += fmt.Sprintf(", think=%s", params.Think)
		}
		return target, note
	}

	// Non-ergo provider with no explicit model and no direction: keep the
	// current model. Direction up/down has no meaning without a tier ladder.
	note := fmt.Sprintf("Kept %s (no explicit model given)", currentModel)
	if params.Think != "auto" {
		note += fmt.Sprintf(", think=%s", params.Think)
	}
	return currentModel, note
}

func isErgoTier(m string) bool {
	return strings.HasPrefix(m, "tjcoder/ergo-") || strings.HasPrefix(m, "tjcoder/deepseek")
}

func tierIndex(m string) int {
	for i, t := range ergoTiers {
		if t == m {
			return i
		}
	}
	return -1
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}
