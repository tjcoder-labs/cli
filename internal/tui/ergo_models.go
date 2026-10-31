package tui

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/tjcoder-labs/cli/internal/client"
	"github.com/tjcoder-labs/cli/internal/session"
)

// isErgoProvider reports whether the active provider is the ergo gateway.
func (a *App) isErgoProvider() bool {
	_, ok := a.provider.(*client.Ergo)
	return ok
}

// ergoAvailable returns the gateway's model set: whatever /v1/models returned
// once loaded, otherwise the hardcoded ergo/flash, ergo/pro, ergo/genius.
func (a *App) ergoAvailable() []string {
	if len(a.ergoModels) == 0 {
		return client.ErgoFallbackModels()
	}
	return a.ergoModels
}

// sanitizeModel returns a model that is valid for the active provider. With
// ergo, anything outside the gateway's set (an Ollama tag from an old
// session/prefs, an agent default) becomes the default ergo/pro. With any other
// provider, a gateway agent name left over from an ergo session is dropped
// (returns "" so callers fall back to their own default).
func (a *App) sanitizeModel(m string) string {
	if a.isErgoProvider() {
		if client.ErgoModelAllowed(m, a.ergoAvailable()) || (m != "" && m == a.explicitModel) {
			return m
		}
		return client.ResolveErgoModel("", "", a.ergoAvailable())
	}
	if client.IsErgoTier(m) {
		return ""
	}
	return m
}

// applyErgoModels records the gateway's model set and re-validates the
// current model against it. A remembered ergo pick that only the live list
// knows about is adopted when the user has not chosen a model this run.
// Returns a user-facing note when the model had to change ("" otherwise).
func (a *App) applyErgoModels(names []string) string {
	if len(names) == 0 {
		return ""
	}
	a.ergoModels = names
	if !client.ErgoModelAllowed(a.currentModel, names) && a.currentModel != a.explicitModel {
		old := a.currentModel
		a.currentModel = client.ResolveErgoModel("", savedErgoModel(), names)
		return fmt.Sprintf("model %q is not served by ergo — using %s", old, a.currentModel)
	}
	if !a.modelChosen {
		if saved := savedErgoModel(); saved != "" && saved != a.currentModel && client.ErgoModelAllowed(saved, names) {
			a.currentModel = saved
		}
	}
	return ""
}

// savedErgoModel returns the remembered ergo model, migrating a legacy id
// (ergo, ergo-lite, ergo-genius, …) to ergo/flash|pro|genius on disk.
func savedErgoModel() string {
	saved, ok := session.GetModelForProvider("ergo")
	if !ok || saved == "" {
		return ""
	}
	if norm := client.NormalizeErgoModel(saved); norm != saved {
		_ = session.SetModelForProvider("ergo", norm)
		return norm
	}
	return saved
}

// selectModel switches the active model and remembers it for this provider.
func (a *App) selectModel(name string) {
	a.currentModel = name
	a.modelChosen = true
	emit("model_selected", map[string]any{"model": name, "provider_type": ProviderType(a.provider)})
	if !a.isErgoProvider() || client.ErgoModelAllowed(name, a.ergoAvailable()) {
		_ = session.SetModelForProvider(client.ProviderKey(a.provider), name)
	}
}

// modelNotFoundNote is the friendly message for an ergo model_not_found.
func modelNotFoundNote(err *client.ErgoModelNotFoundError, available []string, fallbackTo string) string {
	if len(err.Available) > 0 {
		available = err.Available
	}
	bad := strings.TrimSpace(err.Model)
	if bad == "" {
		bad = "that model"
	}
	return fmt.Sprintf("ergo doesn't have a model called %q. Available: %s. Switched to %s — use /model to pick another.",
		bad, strings.Join(available, ", "), fallbackTo)
}

// handleModelNotFound turns an ergo model_not_found into a friendly note,
// resets the model to the default (the Ollama-side saved value is untouched)
// and returns the note. ok is false when err is something else.
func (a *App) handleModelNotFound(err error) (note string, ok bool) {
	var mnf *client.ErgoModelNotFoundError
	if !errors.As(err, &mnf) {
		return "", false
	}
	// Pick the fallback from the 404's list when present, but keep the full
	// unified catalogue in a.ergoModels: the error body may list only the
	// ergo tiers, and shrinking the picker to those would hide xai/, ollama/…
	avail := a.ergoAvailable()
	if len(mnf.Available) > 0 {
		avail = mnf.Available
	}
	def := client.ResolveErgoModel("", "", avail)
	a.currentModel = def
	a.explicitModel = ""
	a.modelChosen = false
	_ = session.SetModelForProvider("ergo", def)
	return modelNotFoundNote(mnf, avail, def), true
}

// selectErgoAgentByName handles `/model <name>` for the ergo provider.
func (a *App) selectErgoAgentByName(name string) {
	name = strings.ToLower(strings.TrimSpace(name))
	if !client.ErgoModelAllowed(name, a.ergoAvailable()) {
		a.appendActivity(fmt.Sprintf("[%s::b]✗ unknown ergo model %q — available: %s[-::B]", a.palette.HexAlert, name, strings.Join(a.ergoAvailable(), ", ")))
		return
	}
	a.selectModel(name)
	a.refreshFooter()
	a.appendActivity("Selected model: " + name)
	a.saveSession()
	a.refreshContextForModel(name)
}

// modelGroup is one provider section of the /model picker.
type modelGroup struct {
	Name   string
	Models []client.ModelInfo
}

// groupErgoModels presents the ergo gateway catalogue as one catalog.
// Ergo tiers come first (flash/pro/genius). Ollama cloud models served by
// the same gateway stay in that same catalog rather than a separate
// provider section. Any other prefix is dropped.
func groupErgoModels(models []client.ModelInfo) []modelGroup {
	var tiers, cloud []client.ModelInfo
	for _, m := range models {
		switch client.ErgoModelGroup(m.Name) {
		case "tjcoder":
			tiers = append(tiers, m)
		}
	}
	rank := func(name string) int {
		for i, a := range client.ErgoAgents {
			if a.Name == name {
				return i
			}
		}
		return len(client.ErgoAgents)
	}
	slices.SortStableFunc(tiers, func(x, y client.ModelInfo) int { return rank(x.Name) - rank(y.Name) })
	all := append(tiers, cloud...)
	if len(all) == 0 {
		return nil
	}
	return []modelGroup{{Name: "tjcoder", Models: all}}
}
