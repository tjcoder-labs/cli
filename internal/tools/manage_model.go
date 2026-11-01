package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tjcoder-labs/cli/internal/client"
)

// ergoProvider is the subset of the ergo client the manage_model tool needs.
type ergoProvider interface {
	Authenticated() bool
}

// ManageModelArgs are the parameters for the manage_model tool.
type ManageModelArgs struct {
	// Direction: "up" (more reasoning), "down" (faster), "auto" (let gateway pick best)
	Direction string `json:"direction,omitempty"`
	// Think: "on" (force reasoning), "off" (disable), "auto" (tier default)
	Think string `json:"think,omitempty"`
	// AutoTier: let gateway pick the best tier for the task
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

// manageModelTool implements tier switching for tjcoder/ergo-* models.
type manageModelTool struct{}

func (manageModelTool) Definition() client.ToolDefinition {
	return client.ToolDefinition{
		Type: "function",
		Function: client.FunctionDefinition{
			Name:        "manage_model",
			Description: "Switch between ergo tiers (flash/pro/genius) and control thinking. Use when the current tier is too slow (go down) or needs more reasoning (go up). Only works with tjcoder/ergo-* models.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"direction": map[string]any{
						"type":        "string",
						"description": "Tier direction: 'up' for more reasoning (flash→pro→genius), 'down' for speed (genius→pro→flash), 'auto' to let gateway pick best tier",
						"enum":        []string{"up", "down", "auto"},
						"default":     "auto",
					},
					"think": map[string]any{
						"type":        "string",
						"description": "Thinking mode: 'on' forces reasoning stream, 'off' disables reasoning, 'auto' uses tier default",
						"enum":        []string{"on", "off", "auto"},
						"default":     "auto",
					},
					"auto_tier": map[string]any{
						"type":        "boolean",
						"description": "If true, gateway picks the best tier for the task (overrides direction)",
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

	// Validate we're using an ergo provider AND it's authenticated
	e, ok := env.Provider.(ergoProvider)
	if !ok {
		return Result{}, fmt.Errorf("manage_model only works with the ergo (tjcoder) provider")
	}
	if !e.Authenticated() {
		return Result{}, fmt.Errorf("manage_model requires an authenticated ergo gateway session (run /login)")
	}

	// Get current model from env or session
	currentModel := ""
	if env.SessionState != nil && env.SessionState.CurrentModel != "" {
		currentModel = env.SessionState.CurrentModel
	}
	if currentModel == "" {
		// fallback to default
		currentModel = client.ErgoDefaultModel
	}

	// Normalize current model
	currentModel = client.NormalizeErgoModel(currentModel)

	// Check if current model is a tjcoder tier
	if !strings.HasPrefix(currentModel, "tjcoder/ergo-") && !strings.HasPrefix(currentModel, "tjcoder/deepseek") {
		return Result{}, fmt.Errorf("manage_model only works with tjcoder/ergo-* tiers, current model: %s", currentModel)
	}

	// Define tier order
	tiers := []string{
		"tjcoder/ergo-flash:cloud",
		"tjcoder/ergo-pro:cloud",
		"tjcoder/ergo-genius:cloud",
	}

	// Find current tier index
	currentIdx := -1
	for i, t := range tiers {
		if t == currentModel {
			currentIdx = i
			break
		}
	}

	// Determine target model
	var targetModel string
	switch params.Direction {
	case "up":
		if currentIdx >= 0 && currentIdx < len(tiers)-1 {
			targetModel = tiers[currentIdx+1]
		} else {
			targetModel = tiers[len(tiers)-1] // genius
		}
	case "down":
		if currentIdx > 0 {
			targetModel = tiers[currentIdx-1]
		} else {
			targetModel = tiers[0] // flash
		}
	case "auto":
		fallthrough
	default:
		// auto: keep current model, let auto_tier header handle it
		targetModel = currentModel
		params.AutoTier = true
	}

	// Validate think value
	validThink := map[string]bool{"on": true, "off": true, "auto": true}
	if !validThink[params.Think] {
		params.Think = "auto"
	}

	// Persist if requested
	if params.Persist && env.SessionState != nil {
		env.SessionState.CurrentModel = targetModel
		if env.PersistSession != nil {
			_ = env.PersistSession()
		}
	}

	// Build note
	note := fmt.Sprintf("Switched to %s", targetModel)
	if params.AutoTier {
		note += " (auto-tier enabled)"
	}
	if params.Think != "auto" {
		note += fmt.Sprintf(", think=%s", params.Think)
	}
	if currentIdx >= 0 && targetModel != currentModel {
		dir := "up"
		if currentIdx > 0 && targetModel == tiers[currentIdx-1] {
			dir = "down"
		}
		note = fmt.Sprintf("Tier %s: %s → %s", dir, currentModel, targetModel)
		if params.AutoTier {
			note += " (auto-tier enabled)"
		}
		if params.Think != "auto" {
			note += fmt.Sprintf(", think=%s", params.Think)
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
