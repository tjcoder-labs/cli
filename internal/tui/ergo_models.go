package tui

import (
	"errors"
	"fmt"
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
// once loaded, otherwise the hardcoded ergo / ergo-lite / ergo-genius.
func (a *App) ergoAvailable() []string {
	if len(a.ergoModels) == 0 {
		return client.ErgoFallbackModels()
	}
	return a.ergoModels
}

// sanitizeModel returns a model that is valid for the active provider. With
// ergo, anything outside the gateway's set (an Ollama tag from an old
// session/prefs, an agent default) becomes the default "ergo". With any other
// provider, a gateway agent name left over from an ergo session is dropped
// (returns "" so callers fall back to their own default).
func (a *App) sanitizeModel(m string) string {
	if a.isErgoProvider() {
		if client.ErgoModelAllowed(m, a.ergoAvailable()) || (m != "" && m == a.explicitModel) {
			return m
		}
		return client.ResolveErgoModel("", "", a.ergoAvailable())
	}
	if client.ErgoModelAllowed(m, client.ErgoFallbackModels()) {
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
		saved, _ := session.GetModelForProvider("ergo")
		a.currentModel = client.ResolveErgoModel("", saved, names)
		return fmt.Sprintf("model %q is not served by ergo — using %s", old, a.currentModel)
	}
	if !a.modelChosen {
		if saved, ok := session.GetModelForProvider("ergo"); ok && saved != a.currentModel && client.ErgoModelAllowed(saved, names) {
			a.currentModel = saved
		}
	}
	return ""
}

// selectModel switches the active model and remembers it for this provider.
func (a *App) selectModel(name string) {
	a.currentModel = name
	a.modelChosen = true
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
	avail := a.ergoAvailable()
	if len(mnf.Available) > 0 {
		avail = mnf.Available
		a.ergoModels = mnf.Available
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
		a.appendActivity(fmt.Sprintf("[%s]unknown ergo model %q — available: %s[-]", a.palette.HexOrchid, name, strings.Join(a.ergoAvailable(), ", ")))
		return
	}
	a.selectModel(name)
	a.refreshFooter()
	a.appendActivity("Selected model: " + name)
	a.saveSession()
	a.refreshContextForModel(name)
}
