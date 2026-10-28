package main

import (
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/tjcoder-labs/cli/internal/auth"
)

// providerInputs captures everything resolveProvider needs, so the policy
// is testable without touching the network, env, or disk.
type providerInputs struct {
	Flag        string // value of --provider ("" when not given)
	Saved       string // provider saved in prefs ("" when none)
	Interactive bool   // true for the TUI; false for headless/ask/models/schedule
	ErgoReady   bool   // an ergo API key is available (env or saved by /login)
	OllamaUp    func() bool
	CloudKey    bool // OLLAMA_API_KEY is set
}

// resolveProvider picks the provider by priority: ergo, then local ollama,
// then ollama-cloud.
//
//  1. An explicit --provider always wins, then a saved provider choice.
//     The automatic default never overrides either.
//  2. Otherwise ergo is the default when it has credentials (ErgoReady).
//  3. Interactive (TUI) with no ergo credentials: probe local Ollama first;
//     if reachable, use it. Otherwise fall back to ergo so the TUI can
//     prompt /login.
//  4. Headless with no ergo credentials (nobody to prompt): fall back to
//     local ollama if reachable, then ollama-cloud if OLLAMA_API_KEY is set,
//     else local ollama (so errors point at the local host as before).
func resolveProvider(in providerInputs) string {
	if f := strings.TrimSpace(in.Flag); f != "" {
		return f
	}
	if s := strings.TrimSpace(in.Saved); s != "" {
		return s
	}
	if in.ErgoReady {
		return "ergo"
	}
	// No ergo credentials: probe local Ollama regardless of interactive/headless.
	if in.OllamaUp != nil && in.OllamaUp() {
		return "ollama"
	}
	if in.CloudKey {
		return "ollama-cloud"
	}
	// Final fallback: ergo for interactive (shows /login prompt), ollama for headless.
	if in.Interactive {
		return "ergo"
	}
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
		Interactive: interactive,
		ErgoReady:   auth.ResolveErgoToken() != "",
		OllamaUp:    func() bool { return ollamaReachable(host) },
		CloudKey:    strings.TrimSpace(os.Getenv("OLLAMA_API_KEY")) != "",
	}
}
