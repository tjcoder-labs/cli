package tui

import (
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/rivo/tview"
	"github.com/tjcoder-labs/cli/internal/auth"
	"github.com/tjcoder-labs/cli/internal/client"
	"github.com/tjcoder-labs/cli/internal/session"
)

var tagRe = regexp.MustCompile(`\[[a-zA-Z0-9#:\-]*\]`)

func plain(s string) string { return tagRe.ReplaceAllString(s, "") }

func TestLoginCodeViewIsBrandPurpleAndBoxed(t *testing.T) {
	pal := darkPalette()
	st := &auth.DeviceStart{UserCode: "ABCD-2345", VerificationURI: "https://x.example/cli/auth/verify"}
	v := loginCodeView(pal, "me@x.co", st, time.Minute, false)
	if !strings.Contains(v, fmt.Sprintf("[%s::b]A[-:-:-]", pal.HexPurple)) {
		t.Errorf("code characters must be bold in the brand purple %s:\n%s", pal.HexPurple, v)
	}
	if pal.HexPurple != "#A77CF8" && !strings.EqualFold(pal.HexPurple, "#a77cf8") {
		t.Errorf("dark theme purple = %s, want canonical #a77cf8", pal.HexPurple)
	}
	p := plain(v)
	for _, want := range []string{"│ A │ B │ C │ D │", "│ 2 │ 3 │ 4 │ 5 │", "┌───┬───┬───┬───┐", "ABCD-2345"} {
		if !strings.Contains(p, want) {
			t.Errorf("plain view missing %q:\n%s", want, p)
		}
	}
	// light theme uses its own purple token
	if lp := lightPalette(); strings.Contains(loginCodeView(lp, "me@x.co", st, time.Minute, false), pal.HexPurple) && lp.HexPurple != pal.HexPurple {
		t.Error("light view must use the light palette purple")
	}
}

func TestFriendlyLoginError(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		headline string
		next     string
	}{
		{"expired", auth.ErrExpired, "expired", "/login"},
		{"signup closed", fmt.Errorf("%w: invite only", auth.ErrSignupClosed), "closed for a@b.co", "allowlist"},
		{"rate limited", &auth.RateLimitedError{RetryAfter: 90 * time.Second}, "Too many", "1m30s"},
		{"rate limited no hint", &auth.RateLimitedError{}, "Too many", "few minutes"},
		{"denied", auth.ErrDenied, "declined", "run /login"},
		{"network", errors.New("could not reach the ergo gateway: dial tcp: refused"), "Can't reach", "connection"},
		{"other", errors.New("boom"), "didn't go through", "/login"},
	}
	for _, c := range cases {
		f := friendlyLoginError(c.err, "a@b.co")
		if !strings.Contains(f.Headline, c.headline) {
			t.Errorf("%s: headline %q missing %q", c.name, f.Headline, c.headline)
		}
		if !strings.Contains(f.Next, c.next) {
			t.Errorf("%s: next %q missing %q", c.name, f.Next, c.next)
		}
		if f.Next == "" {
			t.Errorf("%s: every failure needs a next step", c.name)
		}
	}
	v := plain(loginFailureView(darkPalette(), friendlyLoginError(auth.ErrExpired, "a@b.co")))
	if !strings.Contains(v, "✕") || !strings.Contains(v, "Next:") {
		t.Errorf("failure view: %s", v)
	}
}

func TestLoginSuccessView(t *testing.T) {
	pal := darkPalette()
	agents := client.ErgoFallbackModels()
	v := loginSuccessView(pal, "me@x.co", true, 500, agents, "ergo/flash", "the OS keychain")
	p := plain(v)
	for _, want := range []string{
		"✓ You're signed in", "me@x.co", "$5.00 welcome credit added",
		"● ergo/flash", "ergo/genius", "ergo/pro", "fast and economical",
		"Type a message to start", "/model", "/balance", "/logout",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("success view missing %q:\n%s", want, p)
		}
	}
	if strings.Contains(p, "● ergo/genius") || strings.Contains(p, "● ergo/pro") {
		t.Errorf("only the selected agent is marked:\n%s", p)
	}
	if strings.Contains(p, "ergo_") {
		t.Error("must not render key material")
	}
	// not a fresh grant -> show the balance
	p = plain(loginSuccessView(pal, "me@x.co", false, 1234, agents, "ergo/pro", "file"))
	if !strings.Contains(p, "Balance: $12.34") || strings.Contains(p, "welcome credit") {
		t.Errorf("balance line: %s", p)
	}
}

func newModelTestApp(t *testing.T, p client.Provider) *App {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	return &App{
		provider:   p,
		palette:    darkPalette(),
		activity:   tview.NewTextView(),
		transcript: tview.NewTextView(),
	}
}

func TestSanitizeModel(t *testing.T) {
	a := newModelTestApp(t, client.NewErgo("http://x", "k", 0))
	if got := a.sanitizeModel("deepseek-v4-pro:cloud"); got != "ergo/pro" {
		t.Errorf("ollama tag on ergo -> %q, want ergo/pro", got)
	}
	if got := a.sanitizeModel("ergo/genius"); got != "ergo/genius" {
		t.Errorf("got %q", got)
	}
	a.explicitModel = "weird"
	if got := a.sanitizeModel("weird"); got != "weird" {
		t.Errorf("explicit --model must win, got %q", got)
	}
}

func TestApplyErgoModelsReplacesStaleModelAndKeepsOllamaPref(t *testing.T) {
	a := newModelTestApp(t, client.NewErgo("http://x", "k", 0))
	if err := session.SetModelForProvider("ollama", "minimax-m3:cloud"); err != nil {
		t.Fatal(err)
	}
	a.currentModel = "minimax-m3:cloud"
	note := a.applyErgoModels([]string{"ergo/flash", "ergo/pro", "xai/grok-4.3"})
	if a.currentModel != "ergo/pro" || !strings.Contains(note, "not served by ergo") {
		t.Errorf("model=%q note=%q", a.currentModel, note)
	}
	if m, _ := session.GetModelForProvider("ollama"); m != "minimax-m3:cloud" {
		t.Errorf("ollama pref clobbered: %q", m)
	}
	// remembered ergo pick is adopted when the user hasn't chosen
	// (a legacy saved id is migrated on disk)
	_ = session.SetModelForProvider("ergo", "ergo-lite")
	a.currentModel = "ergo/pro"
	if note := a.applyErgoModels([]string{"ergo/flash", "ergo/pro", "xai/grok-4.3"}); note != "" || a.currentModel != "ergo/flash" {
		t.Errorf("model=%q note=%q", a.currentModel, note)
	}
	if m, _ := session.GetModelForProvider("ergo"); m != "ergo/flash" {
		t.Errorf("legacy saved id not migrated: %q", m)
	}
	// a saved unified-catalogue pick (xai/…) survives
	_ = session.SetModelForProvider("ergo", "xai/grok-4.3")
	a.currentModel = "ergo/pro"
	if a.applyErgoModels([]string{"ergo/flash", "ergo/pro", "xai/grok-4.3"}); a.currentModel != "xai/grok-4.3" {
		t.Errorf("model=%q", a.currentModel)
	}
}

func TestHandleModelNotFoundResetsToDefaultAndListsModels(t *testing.T) {
	a := newModelTestApp(t, client.NewErgo("http://x", "k", 0))
	a.currentModel = "deepseek-v4-pro:cloud"
	full := []string{"ergo", "ergo-lite", "ergo-genius", "xai/grok-4.3", "ollama/qwen3:8b"}
	a.ergoModels = full
	err := fmt.Errorf("turn failed: %w", &client.ErgoModelNotFoundError{
		Model: "deepseek-v4-pro:cloud", Available: []string{"ergo", "ergo-lite", "ergo-genius"},
	})
	note, ok := a.handleModelNotFound(err)
	if !ok {
		t.Fatal("model_not_found must be handled")
	}
	for _, want := range []string{`"deepseek-v4-pro:cloud"`, "ergo, ergo-lite, ergo-genius", "Switched to ergo", "/model"} {
		if !strings.Contains(note, want) {
			t.Errorf("note missing %q: %s", want, note)
		}
	}
	if a.currentModel != "ergo" {
		t.Errorf("model = %q, want ergo", a.currentModel)
	}
	if !reflect.DeepEqual(a.ergoModels, full) {
		t.Errorf("404 shrank the model list to %v", a.ergoModels)
	}
	if _, ok := a.handleModelNotFound(errors.New("other")); ok {
		t.Error("other errors are not model_not_found")
	}
}

func TestSanitizeModelDropsGatewayAgentOnOtherProviders(t *testing.T) {
	a := newModelTestApp(t, client.NewOllama("http://x"))
	if got := a.sanitizeModel("ergo-lite"); got != "" {
		t.Errorf("gateway agent leaked to ollama: %q", got)
	}
	if got := a.sanitizeModel("qwen3:cloud"); got != "qwen3:cloud" {
		t.Errorf("got %q", got)
	}
}
