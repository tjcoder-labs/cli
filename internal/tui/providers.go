package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rivo/tview"
	"github.com/tjcoder-labs/cli/internal/client"
	"github.com/tjcoder-labs/cli/internal/providers"
	"github.com/tjcoder-labs/cli/internal/session"
)

// openProvidersModal lists the global provider registry and lets the user
// add, edit, activate, or remove OpenAI-compatible gateways. The registry is
// global (cross-workspace) and seeded with ergo-gateway + ollama, both of
// which may be removed.
func (a *App) openProvidersModal() {
	list := tview.NewList().ShowSecondaryText(true)
	a.styleModalList(list)
	a.refreshProvidersList(list)
	list.SetDoneFunc(func() { a.closeModal() })
	a.showModal("Providers", list)
}

// refreshProvidersList rebuilds the provider list from the registry. Each row
// shows name + base URL; the secondary line carries the auth flow and a
// marker for the active provider. Actions are wired to a nested sub-menu so
// the list itself stays read-only and re-renderable.
func (a *App) refreshProvidersList(list *tview.List) {
	list.Clear()
	reg := providers.Load()
	for _, p := range reg.Providers {
		p := p
		marker := " "
		if p.Name == reg.Active {
			marker = "●"
		}
		secondary := p.BaseURL
		auth := "none"
		switch p.Auth {
		case providers.AuthDeviceCode:
			auth = "device-code"
		case providers.AuthAPIKey:
			auth = "api-key"
		}
		list.AddItem(fmt.Sprintf("%s %s", marker, p.Name), fmt.Sprintf("%s · auth: %s", secondary, auth), 0, func() {
			a.openProviderDetail(p.Name)
		})
	}
	list.AddItem("＋ Add provider", "register a new OpenAI-compatible gateway", 0, func() {
		a.openProviderEditor(providers.Config{})
	})
}

// openProviderDetail shows actions for one provider: activate, edit, login,
// remove.
func (a *App) openProviderDetail(name string) {
	cfg, ok := providers.Get(name)
	if !ok {
		a.appendActivity(fmt.Sprintf("Provider %q not found", name))
		return
	}

	reg := providers.Load()
	isActive := reg.Active == name

	list := tview.NewList().ShowSecondaryText(true)
	a.styleModalList(list)
	list.AddItem("Activate", "make this the default provider", 0, func() {
		reg.Active = name
		if err := providers.Save(reg); err != nil {
			a.appendActivity(fmt.Sprintf("[red]Provider activation failed[-]: %v", err))
			return
		}
		if err := session.SetProvider(name); err != nil {
			a.appendActivity(fmt.Sprintf("[red]Provider preference save failed[-]: %v", err))
		}
		// Rebuild the live client and reload models for the new provider.
		a.switchProvider(cfg)
		a.closeModal()
	})
	list.AddItem("Edit", "change name / base URL / auth flow", 0, func() {
		a.openProviderEditor(cfg)
	})
	if cfg.Auth != providers.AuthNone {
		label := "Login"
		if cfg.Auth == providers.AuthDeviceCode {
			label = "Login (device code)"
		} else {
			label = "Login (API key)"
		}
		list.AddItem(label, "authenticate with this provider", 0, func() {
			a.beginProviderLogin(cfg)
		})
	}
	list.AddItem("Remove", "delete this provider", 0, func() {
		a.removeProvider(name)
	})
	if isActive {
		list.AddItem("(active)", "this provider is currently selected", 0, nil)
	}
	list.SetDoneFunc(func() { a.openProvidersModal() })
	a.showModal(fmt.Sprintf("Provider: %s", name), list)
}

// openProviderEditor renders a form to add or edit a provider. When editing,
// existing fields are pre-filled and the name field is read-only (renaming
// would orphan credentials; users should remove + re-add instead).
func (a *App) openProviderEditor(existing providers.Config) {
	form := tview.NewForm()
	a.styleModalForm(form)

	name := existing.Name
	baseURL := existing.BaseURL
	auth := string(existing.Auth)
	if auth == "" {
		auth = "none"
	}
	protocol := existing.Protocol
	if protocol == "" {
		protocol = "auto"
	}

	if name == "" {
		form.AddInputField("Name", "", 30, nil, func(s string) { name = strings.TrimSpace(s) })
	} else {
		form.AddInputField("Name", name, 30, nil, func(string) {})
	}
	form.AddInputField("Base URL", baseURL, 60, nil, func(s string) { baseURL = strings.TrimSpace(s) })
	form.AddDropDown("Auth", []string{"none", "api_key", "device_code"}, authIndex(auth), func(option string, _ int) {
		auth = option
	})
	form.AddDropDown("Protocol", []string{"auto", "openai", "ollama"}, protocolIndex(protocol), func(option string, _ int) {
		protocol = option
	})
	form.AddInputField("API key (optional)", existing.APIKey, 60, nil, func(s string) { existing.APIKey = strings.TrimSpace(s) })

	save := func() {
		if name == "" || baseURL == "" {
			a.appendActivity("[red]Provider name and base URL are required[-]")
			return
		}
		cfg := existing
		cfg.Name = name
		cfg.BaseURL = strings.TrimRight(baseURL, "/")
		cfg.Auth = providers.AuthFlow(auth)
		cfg.Protocol = protocolFromIndex(protocol)
		if cfg.Auth != providers.AuthAPIKey {
			cfg.APIKey = ""
		}
		if !cfg.Valid() {
			a.appendActivity("[red]Invalid provider config[-]")
			return
		}

		reg := providers.Load()
		// Replace or append.
		replaced := false
		for i, p := range reg.Providers {
			if p.Name == cfg.Name {
				reg.Providers[i] = cfg
				replaced = true
				break
			}
		}
		if !replaced {
			reg.Providers = append(reg.Providers, cfg)
		}
		// A brand-new provider becomes active if it's the only one (or first).
		if reg.Active == "" {
			reg.Active = cfg.Name
		}
		if err := providers.Save(reg); err != nil {
			a.appendActivity(fmt.Sprintf("[red]Provider save failed[-]: %v", err))
			return
		}
		a.appendActivity(fmt.Sprintf("Saved provider %s; checking integration...", cfg.Name))
		a.openProvidersModal()
		a.checkProviderIntegration(cfg)
	}
	form.AddButton("Save", save)
	form.AddButton("Cancel", func() { a.openProvidersModal() })

	a.showModal("Provider", form)
}

func validateProviderIntegration(cfg providers.Config) (int, error) {
	p, err := client.NewProviderFromConfig(cfg, 8*time.Second)
	if err != nil {
		return 0, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	models, err := p.ListModels(ctx)
	if err != nil {
		return 0, err
	}
	if len(models) == 0 {
		return 0, fmt.Errorf("provider returned no models")
	}
	return len(models), nil
}

func modelFromProviderModels(current string, models []client.ModelInfo) (string, bool) {
	for _, model := range models {
		if model.Name == current {
			return current, false
		}
	}
	for _, model := range models {
		if name := strings.TrimSpace(model.Name); name != "" {
			return name, current != ""
		}
	}
	return current, false
}

func (a *App) checkProviderIntegration(cfg providers.Config) {
	go func() {
		count, err := validateProviderIntegration(cfg)
		a.postUI(func() {
			if err != nil {
				a.appendActivity(fmt.Sprintf("[yellow]Provider %s saved, but integration check failed: %v[-]", cfg.Name, err))
				return
			}
			a.appendActivity(fmt.Sprintf("[green]Provider %s integration verified (%d models)[-]", cfg.Name, count))
		})
	}()
}

func authIndex(auth string) int {
	switch auth {
	case "api_key":
		return 1
	case "device_code":
		return 2
	default:
		return 0
	}
}

func protocolIndex(protocol string) int {
	switch protocol {
	case providers.ProtocolOpenAI:
		return 1
	case providers.ProtocolOllama:
		return 2
	default:
		return 0
	}
}

func protocolFromIndex(idx string) string {
	switch idx {
	case providers.ProtocolOpenAI:
		return providers.ProtocolOpenAI
	case providers.ProtocolOllama:
		return providers.ProtocolOllama
	default:
		return ""
	}
}

// removeProvider deletes a provider from the registry. The active provider
// can be removed; Save re-selects the first remaining provider.
func (a *App) removeProvider(name string) {
	reg := providers.Load()
	wasActive := reg.Active == name
	savedProvider, _ := session.GetProvider()
	out := reg.Providers[:0]
	found := false
	for _, p := range reg.Providers {
		if p.Name == name {
			found = true
			continue
		}
		out = append(out, p)
	}
	if !found {
		a.appendActivity(fmt.Sprintf("Provider %q not found", name))
		return
	}
	reg.Providers = out
	if reg.Active == name {
		reg.Active = ""
	}
	if err := providers.Save(reg); err != nil {
		a.appendActivity(fmt.Sprintf("[red]Provider remove failed[-]: %v", err))
		return
	}
	if reg.Active != "" && (wasActive || savedProvider == name) {
		if err := session.SetProvider(reg.Active); err != nil {
			a.appendActivity(fmt.Sprintf("[red]Provider preference update failed[-]: %v", err))
			return
		}
	}
	a.appendActivity(fmt.Sprintf("Removed provider %s", name))
	a.openProvidersModal()
}

// beginProviderLogin dispatches /login <provider> to the right auth flow.
func (a *App) beginProviderLogin(cfg providers.Config) {
	switch cfg.Auth {
	case providers.AuthDeviceCode:
		a.handleLoginCommand()
	case providers.AuthAPIKey:
		a.openAPIKeyLogin(cfg)
	default:
		a.appendActivity(fmt.Sprintf("Provider %s needs no credentials", cfg.Name))
	}
}

// openAPIKeyLogin prompts for a Bearer API key and stores it in the registry.
func (a *App) openAPIKeyLogin(cfg providers.Config) {
	form := tview.NewForm()
	a.styleModalForm(form)
	var key string
	form.AddInputField("API key", "", 60, nil, func(s string) { key = strings.TrimSpace(s) })
	form.AddButton("Save", func() {
		if key == "" {
			a.appendActivity("[red]API key is required[-]")
			return
		}
		cfg.APIKey = key
		reg := providers.Load()
		for i, p := range reg.Providers {
			if p.Name == cfg.Name {
				reg.Providers[i] = cfg
				break
			}
		}
		if err := providers.Save(reg); err != nil {
			a.appendActivity(fmt.Sprintf("[red]Provider login failed[-]: %v", err))
			return
		}
		a.appendActivity(fmt.Sprintf("Stored API key for %s", cfg.Name))
		a.closeModal()
		a.checkProviderIntegration(cfg)
	})
	form.AddButton("Cancel", func() { a.closeModal() })
	a.showModal(fmt.Sprintf("Login: %s", cfg.Name), form)
}
