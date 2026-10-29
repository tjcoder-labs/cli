package main

import (
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/tjcoder-labs/cli/internal/auth"
	"github.com/tjcoder-labs/cli/internal/providers"
)

// providerInputs captures everything resolveProvider needs, so the policy
// is testable without touching the network, env, or disk.
type providerInputs struct {
	Flag        string // value of --provider ("" when not given)
	Saved       string // provider saved in prefs ("" when none)
	Registry    string // active/default entry in providers.json ("" when unset)
	Interactive bool   // true for the TUI; false for headless/ask/models/schedule
	ErgoReady   bool   // an ergo API key is available (env or saved by /login)
	OllamaUp    func() bool
	CloudKey    bool // OLLAMA_API_KEY is set
}

// resolveProvider picks the provider by priority: ergo, then local ollama,
// then ollama-cloud.
//
//  1. An explicit --provider always wins, then a saved provider choice,
//     then the provider registry's active/default entry (providers.json).
//     The automatic default never overrides any of these. The built-in
//     "ergo-gateway" entry defers to the automatic policy below so a
//     signed-out headless run can still fall back to local Ollama.
//  2. Otherwise ergo is the default when it has credentials (ErgoReady).
//  3. Interactive (TUI) with no ergo credentials: fall back to ergo so
//     the TUI can show the /login prompt. The user is present to act on it
//     (or switch to local Ollama with /provider).
//  4. Headless with no ergo credentials (nobody to prompt): probe local
//     Ollama, then ollama-cloud if OLLAMA_API_KEY is set, else local ollama.
func resolveProvider(in providerInputs) string {
	if f := strings.TrimSpace(in.Flag); f != "" {
		return f
	}
	if s := strings.TrimSpace(in.Saved); s != "" {
		return s
	}
	if r := strings.TrimSpace(in.Registry); r != "" && r != "ergo-gateway" && r != "ergo" {
		return r
	}
	if in.ErgoReady {
		return "ergo"
	}
	// No ergo credentials. For the interactive TUI, return ergo so the /login
	// prompt is shown (the user is present to act on it). For headless callers
	// there is nobody to prompt, so fall through to local Ollama / cloud.
	if in.Interactive {
		return "ergo"
	}
	// Headless: probe local Ollama, then cloud, then local as last resort.
	if in.OllamaUp != nil && in.OllamaUp() {
		return "ollama"
	}
	if in.CloudKey {
		return "ollama-cloud"
	}
	// Final fallback: ollama (errors point at the local host as before).
	return "ollama"
}

// ollamaReachable does a short probe of the local Ollama host.
func ollamaReachable(host string) bool {
	c := &http.Client{Timeout: 700 * time.Millisecond}
	resp, err := c.Get(strings.TrimRight(host, "/") + "/api/tags")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// autoProviderInputs fills the environment-derived fields of providerInputs.
func autoProviderInputs(flagVal, saved, host string, interactive bool) providerInputs {
	return providerInputs{
		Flag:        flagVal,
		Saved:       saved,
		Registry:    providers.ConfiguredActive(),
		Interactive: interactive,
		ErgoReady:   auth.ResolveErgoToken() != "",
		OllamaUp:    func() bool { return ollamaReachable(host) },
		CloudKey:    strings.TrimSpace(os.Getenv("OLLAMA_API_KEY")) != "",
	}
}
