package tui

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"github.com/tjcoder-labs/cli/internal/auth"
	"github.com/tjcoder-labs/cli/internal/client"
	"github.com/tjcoder-labs/cli/internal/providers"
	"github.com/tjcoder-labs/cli/internal/session"
)

// ---- test scaffolding ---------------------------------------------------

// fakeGateway stands in for api.tjcoder.com: device-code sign-in, logout,
// the wallet, and a chat endpoint whose reply the test chooses.
type fakeGateway struct {
	srv         *httptest.Server
	chatHits    atomic.Int32
	logoutHits  atomic.Int32
	logoutAuth  atomic.Value // last Authorization header seen by /cli/auth/logout
	chatStatus  int
	chatBody    string
	startStatus int
	startBody   string
	startGate   chan struct{} // when set, /cli/auth/start waits for it to close
}

const (
	bodyInvalidKey      = `{"error":{"code":"invalid_api_key","message":"Invalid API key"}}`
	bodyAccountInactive = `{"error":{"code":"account_inactive","message":"Account is not active"}}`
	bodySignupClosed    = `{"error":{"code":"signup_closed","message":"invite only"}}`
	testKey             = "ergo_test_abcdef123456"
)

// hubEnv isolates every store a sign-in touches (token file, identity,
// provider registry, prefs), points the gateway at a fake, and disables
// the keychain, the browser, and the card arm delay.
func hubEnv(t *testing.T) *fakeGateway {
	t.Helper()
	t.Setenv("ERGO_NO_KEYRING", "1")
	t.Setenv("ERGO_API_KEY", "")
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	noArmDelay(t)
	old := openBrowser
	openBrowser = func(string) error { return nil }
	t.Cleanup(func() { openBrowser = old })

	g := &fakeGateway{chatStatus: http.StatusForbidden, chatBody: bodyAccountInactive, startStatus: http.StatusOK}
	mux := http.NewServeMux()
	mux.HandleFunc("/cli/auth/start", func(w http.ResponseWriter, r *http.Request) {
		if g.startGate != nil {
			select {
			case <-g.startGate:
			case <-r.Context().Done():
				return
			}
		}
		if g.startStatus != http.StatusOK {
			w.WriteHeader(g.startStatus)
			_, _ = w.Write([]byte(g.startBody))
			return
		}
		_, _ = w.Write([]byte(`{"device_code":"dc","user_code":"ABCD-2345","verification_uri":"https://x.example/verify","interval":1,"expires_in":900}`))
	})
	mux.HandleFunc("/cli/auth/poll", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success","api_key":"` + testKey + `","email":"me@x.co","balance_cents":500,"welcome_granted":true}`))
	})
	mux.HandleFunc("/cli/auth/logout", func(w http.ResponseWriter, r *http.Request) {
		g.logoutHits.Add(1)
		g.logoutAuth.Store(r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{}`))
	})
	mux.HandleFunc("/v1/wallet/balance", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"balance_cents":500}`))
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		g.chatHits.Add(1)
		w.WriteHeader(g.chatStatus)
		_, _ = w.Write([]byte(g.chatBody))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNotFound) })
	g.srv = httptest.NewServer(mux)
	t.Cleanup(g.srv.Close)
	t.Setenv("ERGO_BASE_URL", g.srv.URL)
	return g
}

func identityScreen(s tcell.SimulationScreen) tcell.Screen { return s }

func startUnsignedErgo(t *testing.T, g *fakeGateway) (*App, tcell.SimulationScreen) {
	t.Helper()
	return startSimAppWith(t, client.NewErgo(g.srv.URL, "", 0), identityScreen)
}

func startSignedErgo(t *testing.T, g *fakeGateway) (*App, tcell.SimulationScreen) {
	t.Helper()
	saveSignedIn(t)
	return startSimAppWith(t, client.NewErgo(g.srv.URL, testKey, 0), identityScreen)
}

func saveSignedIn(t *testing.T) {
	t.Helper()
	if err := auth.SaveErgoToken(testKey); err != nil {
		t.Fatal(err)
	}
	if err := auth.SaveErgoIdentity(auth.Identity{Email: "me@x.co"}); err != nil {
		t.Fatal(err)
	}
}

func screenText(sim tcell.SimulationScreen) string {
	cells, w, h := sim.GetContents()
	var b strings.Builder
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if rs := cells[y*w+x].Runes; len(rs) > 0 {
				b.WriteRune(rs[0])
			} else {
				b.WriteByte(' ')
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func waitScreen(t *testing.T, a *App, sim tcell.SimulationScreen, want string) {
	t.Helper()
	// Handlers queued from the test don't trigger a redraw the way a key
	// press does, so ask for one before each look at the screen.
	deadline := time.Now().Add(slashDeadlockTimeout)
	for {
		a.tv.Draw()
		var ok bool
		onLoop(t, a, func() { ok = strings.Contains(screenText(sim), want) })
		if ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for screen to show %q; got:\n%s", want, snap(t, a, sim))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// snap reads the screen on the event loop (the simulator's cell buffer is
// written by the draw there, so reading it from the test goroutine races).
func snap(t *testing.T, a *App, sim tcell.SimulationScreen) string {
	t.Helper()
	var txt string
	onLoop(t, a, func() { txt = screenText(sim) })
	return txt
}

// colOf returns the column (in cells) where want first appears on screen.
func colOf(t *testing.T, a *App, sim tcell.SimulationScreen, want string) int {
	t.Helper()
	for _, line := range strings.Split(snap(t, a, sim), "\n") {
		if i := strings.Index(line, want); i >= 0 {
			return len([]rune(line[:i]))
		}
	}
	return -1
}

// flatCard joins a card's wrapped rows into single-spaced text so long
// lines can be matched whole regardless of the pane width.
func flatCard(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "▎", " ")), " ")
}

// waitCard waits for a hub/prompt card containing want and returns its text.
func waitCard(t *testing.T, a *App, want string) string {
	t.Helper()
	var txt string
	waitFor(t, a, "card containing "+fmt.Sprintf("%q", want), func() bool {
		if a.livePrompt == nil {
			return false
		}
		txt = flatCard(a.livePrompt.view.GetText(true))
		return strings.Contains(txt, want)
	})
	return txt
}

func waitNoCard(t *testing.T, a *App) {
	t.Helper()
	waitFor(t, a, "card gone", func() bool { return a.livePrompt == nil })
}

// ---- buildLoginHub ------------------------------------------------------

func TestBuildLoginHubStates(t *testing.T) {
	cases := []struct {
		name    string
		st      hubState
		title   string
		labels  []string
		choices []hubChoice
		quest   string
	}{
		{"signed out", hubState{}, "Get started",
			[]string{"Sign in to TJ Coder", "Use a different provider", "Configure providers"},
			[]hubChoice{hubSignIn, hubUseProvider, hubConfigure},
			"Sign in to use TJ Coder, or switch to another provider."},
		{"signed out with a waiting message", hubState{CanRetry: true}, "Get started",
			[]string{"Sign in to TJ Coder", "Use a different provider", "Configure providers", "Try again"},
			[]hubChoice{hubSignIn, hubUseProvider, hubConfigure, hubTryAgain}, ""},
		{"signed in", hubState{Signed: true, Email: "me@x.co", Balance: "$4.32"}, "TJ Coder account",
			[]string{"Switch account", "Sign out", "Use a different provider", "Configure providers", "Open wallet"},
			[]hubChoice{hubSwitchAccount, hubSignOut, hubUseProvider, hubConfigure, hubWallet},
			"Signed in as me@x.co\nBalance: $4.32"},
		{"signed in, nothing known", hubState{Signed: true}, "TJ Coder account",
			[]string{"Switch account", "Sign out", "Use a different provider", "Configure providers", "Open wallet"},
			[]hubChoice{hubSwitchAccount, hubSignOut, hubUseProvider, hubConfigure, hubWallet},
			"You're signed in."},
		{"signed in via ERGO_API_KEY", hubState{Signed: true, Env: true}, "TJ Coder account",
			[]string{"Sign out", "Use a different provider", "Configure providers", "Open wallet"},
			[]hubChoice{hubSignOut, hubUseProvider, hubConfigure, hubWallet}, ""},
		{"rejected key", hubState{Signed: true, Rejected: true, CanRetry: true}, "Sign in again",
			[]string{"Sign in again", "Use a different provider", "Configure providers", "Try again"},
			[]hubChoice{hubSignIn, hubUseProvider, hubConfigure, hubTryAgain},
			"Your TJ Coder session is no longer valid."},
		{"rejected key from the environment", hubState{Signed: true, Rejected: true, Env: true}, "Sign in again",
			[]string{"Use a different provider", "Configure providers"},
			[]hubChoice{hubUseProvider, hubConfigure}, "ERGO_API_KEY"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req, choices := buildLoginHub(c.st)
			if req.Title != c.title {
				t.Errorf("title %q, want %q", req.Title, c.title)
			}
			if strings.Join(req.Options, "|") != strings.Join(c.labels, "|") {
				t.Errorf("options %q, want %q", req.Options, c.labels)
			}
			if fmt.Sprint(choices) != fmt.Sprint(c.choices) {
				t.Errorf("choices %v, want %v", choices, c.choices)
			}
			if len(req.Descriptions) != len(req.Options) {
				t.Errorf("every option needs a description: %d vs %d", len(req.Descriptions), len(req.Options))
			}
			if c.quest != "" && !strings.Contains(req.Question, c.quest) {
				t.Errorf("question %q missing %q", req.Question, c.quest)
			}
		})
	}
	// The credit claim lives only on the first-run option.
	req, _ := buildLoginHub(hubState{})
	if req.Descriptions[0] != "New accounts get $5 credit automatically." {
		t.Errorf("sign-in subtext = %q", req.Descriptions[0])
	}
}

func TestBuildLoginHubSignOutCopyForEnvKey(t *testing.T) {
	req, _ := buildLoginHub(hubState{Signed: true, Env: true})
	if !strings.Contains(req.Descriptions[0], "ERGO_API_KEY") || strings.Contains(req.Descriptions[0], "Revoke") {
		t.Errorf("env sign-out must not promise a revoke: %q", req.Descriptions[0])
	}
}

func TestPreviewPrompt(t *testing.T) {
	if got := previewPrompt("  hello  "); got != "hello" {
		t.Errorf("got %q", got)
	}
	if got := previewPrompt("first line\nsecond"); got != "first line…" {
		t.Errorf("multi-line: %q", got)
	}
	long := strings.Repeat("word ", 30)
	if got := previewPrompt(long); len([]rune(got)) > 62 || !strings.HasSuffix(got, "…") {
		t.Errorf("long: %q", got)
	}
}

func TestResolveLoginProvider(t *testing.T) {
	reg := providers.Defaults()
	for arg, want := range map[string]string{
		"tjcoder": "tjcoder", "TJCoder": "tjcoder", "ergo": "tjcoder", "ergo-gateway": "tjcoder",
		"ollama": "ollama", "ollama-cloud": "ollama-cloud", " Ollama-Cloud ": "ollama-cloud",
	} {
		cfg, ok := resolveLoginProvider(reg, arg)
		if !ok || cfg.Name != want {
			t.Errorf("%q -> %q, %v; want %q", arg, cfg.Name, ok, want)
		}
	}
	for _, arg := range []string{"", "nope", "openai"} {
		if _, ok := resolveLoginProvider(reg, arg); ok {
			t.Errorf("%q should not resolve", arg)
		}
	}
}

// ---- the hub, end to end -------------------------------------------------

func TestLoginSlashOpensGetStartedWhenSignedOut(t *testing.T) {
	g := hubEnv(t)
	a, sim := startSimApp(t)
	_ = g
	typeLine(sim, "/login")
	txt := waitCard(t, a, "GET STARTED")
	for _, want := range []string{
		"Sign in to use TJ Coder, or switch to another provider.",
		"Sign in to TJ Coder", "New accounts get $5 credit automatically.",
		"Use a different provider", "Configure providers",
	} {
		if !strings.Contains(txt, want) {
			t.Errorf("card missing %q:\n%s", want, txt)
		}
	}
	if strings.Contains(txt, "Try again") {
		t.Errorf("no waiting message, so no Try again:\n%s", txt)
	}
	injectKey(sim, tcell.KeyEscape, 0)
	waitNoCard(t, a)
	// The hub's bookkeeping is released right after the card goes.
	waitFor(t, a, "hub released and nothing open", func() bool { return !a.loginHubOpen && a.leftPane == nil })
}

func TestLoginSlashShowsAccountMenuWhenSignedIn(t *testing.T) {
	g := hubEnv(t)
	a, sim := startSignedErgo(t, g)
	onLoop(t, a, func() { a.setErgoBalanceCents(432) })
	typeLine(sim, "/login")
	txt := waitCard(t, a, "TJ CODER ACCOUNT")
	for _, want := range []string{"Signed in as me@x.co", "Balance: $4.32", "Switch account", "Sign out", "Open wallet", "Use a different provider", "Configure providers"} {
		if !strings.Contains(txt, want) {
			t.Errorf("card missing %q:\n%s", want, txt)
		}
	}
	if strings.Contains(txt, testKey) {
		t.Error("the key must never be rendered")
	}
}

func TestLoginHubDoesNotStack(t *testing.T) {
	g := hubEnv(t)
	a, sim := startSimApp(t)
	_ = g
	typeLine(sim, "/login")
	waitCard(t, a, "GET STARTED")
	onLoop(t, a, func() {
		a.openLoginHub(nil, false)
		a.openLoginHub(nil, false)
		if len(a.liveCards) != 1 {
			t.Errorf("%d cards mounted, want 1", len(a.liveCards))
		}
	})
}

func TestLoginHubConfigureOpensProviders(t *testing.T) {
	g := hubEnv(t)
	a, sim := startSimApp(t)
	_ = g
	typeLine(sim, "/login")
	waitCard(t, a, "GET STARTED")
	injectKey(sim, tcell.KeyRune, '3') // Configure providers
	waitFor(t, a, "providers modal", func() bool { return a.leftPane != nil })
	waitScreen(t, a, sim, "PROVIDERS")
}

func TestLoginHubSignInOpensEmailFormFlushWithHeading(t *testing.T) {
	g := hubEnv(t)
	a, sim := startSimApp(t)
	_ = g
	typeLine(sim, "/login")
	waitCard(t, a, "GET STARTED")
	injectKey(sim, tcell.KeyRune, '1') // Sign in to TJ Coder
	waitScreen(t, a, sim, "SIGN IN")
	waitScreen(t, a, sim, "Enter your email")
	txt := snap(t, a, sim)
	for _, want := range []string{
		"Enter your email", "password needed",
		"New here? Your account is created", "$5 credit",
		"Email", "you@example.com", "Send sign-in link", "Cancel",
	} {
		if !strings.Contains(txt, want) {
			t.Errorf("sign-in form missing %q:\n%s", want, txt)
		}
	}
	if strings.Contains(txt, "ERGO SIGN-IN") || strings.Contains(strings.ToLower(txt), "contacting ergo") {
		t.Errorf("old branding is gone:\n%s", txt)
	}
	// The heading and the form's first row start in the same column.
	if h, f := colOf(t, a, sim, "SIGN IN"), colOf(t, a, sim, "Email"); h < 0 || h != f {
		t.Errorf("heading at col %d, form label at col %d; want flush", h, f)
	}
}

func TestSignInFormRejectsBadEmailInline(t *testing.T) {
	g := hubEnv(t)
	a, sim := startSimApp(t)
	_ = g
	onLoop(t, a, func() { a.handleLoginCommand() })
	waitScreen(t, a, sim, "Send sign-in link")
	typeLine(sim, "nope") // type, then Enter moves to the button
	injectKey(sim, tcell.KeyEnter, 0)
	waitScreen(t, a, sim, "Enter a valid email address")
	onLoop(t, a, func() {
		if a.loginCancel != nil {
			t.Error("a bad email must not start a sign-in")
		}
	})
}

func TestSignInFormEscClosesAndRefocusesInput(t *testing.T) {
	g := hubEnv(t)
	a, sim := startSimApp(t)
	_ = g
	onLoop(t, a, func() { a.handleLoginCommand() })
	waitFor(t, a, "form open", func() bool { return a.leftPane != nil && a.tv.GetFocus() != a.input })
	injectKey(sim, tcell.KeyRune, 'q')
	injectKey(sim, tcell.KeyEscape, 0)
	waitFor(t, a, "closed", func() bool { return a.leftPane == nil && a.tv.GetFocus() == a.input })
}

func TestAPIKeyFormHeadingIsFlush(t *testing.T) {
	a, sim := startSimApp(t)
	onLoop(t, a, func() { a.openAPIKeyLogin(providers.Config{Name: "x"}) })
	waitScreen(t, a, sim, "LOGIN: X")
	if h, f := colOf(t, a, sim, "LOGIN: X"), colOf(t, a, sim, "API key"); h < 0 || h != f {
		t.Errorf("heading at col %d, form label at col %d; want flush", h, f)
	}
}

// ---- provider pick -------------------------------------------------------

func TestLoginHubUseDifferentProviderSwitchesAndPersists(t *testing.T) {
	g := hubEnv(t)
	a, sim := startSimApp(t)
	_ = g
	typeLine(sim, "/login")
	waitCard(t, a, "GET STARTED")
	injectKey(sim, tcell.KeyRune, '2') // Use a different provider
	txt := waitCard(t, a, "USE A DIFFERENT PROVIDER")
	for _, want := range []string{"ollama", "ollama-cloud", "needs an API key", "no key needed", "Back"} {
		if !strings.Contains(txt, want) {
			t.Errorf("pick card missing %q:\n%s", want, txt)
		}
	}
	if strings.Contains(txt, "tjcoder") {
		t.Errorf("the account provider is not a 'different' provider:\n%s", txt)
	}
	injectKey(sim, tcell.KeyRune, '1') // ollama
	waitFor(t, a, "ollama active", func() bool { return providers.Load().Active == "ollama" })
	if p, ok := session.GetProvider(); !ok || p != "ollama" {
		t.Errorf("saved provider preference = %q, %v", p, ok)
	}
	onLoop(t, a, func() {
		if _, ok := a.provider.(*client.Ergo); ok {
			t.Error("live provider was not switched")
		}
	})
}

func TestLoginHubProviderPickBackReturnsToHub(t *testing.T) {
	g := hubEnv(t)
	a, sim := startSimApp(t)
	_ = g
	typeLine(sim, "/login")
	waitCard(t, a, "GET STARTED")
	injectKey(sim, tcell.KeyRune, '2')
	waitCard(t, a, "USE A DIFFERENT PROVIDER")
	injectKey(sim, tcell.KeyRune, '3') // Back
	waitFor(t, a, "hub is back", func() bool {
		return a.livePrompt != nil && strings.Contains(flatCard(a.livePrompt.view.GetText(true)), "GET STARTED") && len(a.liveCards) == 1
	})
}

func TestLoginHubAPIKeyProviderAsksForKeyFirst(t *testing.T) {
	g := hubEnv(t)
	a, sim := startSimApp(t)
	_ = g
	typeLine(sim, "/login")
	waitCard(t, a, "GET STARTED")
	injectKey(sim, tcell.KeyRune, '2')
	waitCard(t, a, "USE A DIFFERENT PROVIDER")
	injectKey(sim, tcell.KeyRune, '2') // ollama-cloud, no key saved
	waitScreen(t, a, sim, "LOGIN: OLLAMA-CLOUD")
	if got := providers.Load().Active; got == "ollama-cloud" {
		t.Error("must not activate before the key is saved")
	}
}

// ---- /login <provider> ---------------------------------------------------

func TestLoginProviderShortcuts(t *testing.T) {
	g := hubEnv(t)
	a, sim := startUnsignedErgo(t, g)
	typeLine(sim, "/login ollama")
	waitFor(t, a, "ollama active", func() bool { return providers.Load().Active == "ollama" })

	// An unknown name explains itself and lists the real ones.
	typeLine(sim, "/login nosuch")
	waitForText(t, a, "Unknown provider", func() string { return a.activity.GetText(true) })
	for _, name := range []string{"tjcoder", "ollama", "ollama-cloud"} {
		if txt := a.activity.GetText(true); !strings.Contains(txt, name+",") && !strings.Contains(txt, name+".") {
			t.Errorf("activity should list %s:\n%s", name, txt)
		}
	}

	// The key-based hosted provider asks for its key.
	typeLine(sim, "/login ollama-cloud")
	waitScreen(t, a, sim, "LOGIN: OLLAMA-CLOUD")
	injectKey(sim, tcell.KeyEscape, 0)
	waitFor(t, a, "closed", func() bool { return a.leftPane == nil })

	// "ergo" is the old name for the account.
	typeLine(sim, "/login ergo")
	waitScreen(t, a, sim, "Enter your email")
}

func TestLoginTJCoderWhenSignedInJustSwitches(t *testing.T) {
	g := hubEnv(t)
	a, sim := startSignedErgo(t, g)
	typeLine(sim, "/login ollama")
	waitFor(t, a, "ollama active", func() bool { return providers.Load().Active == "ollama" })
	typeLine(sim, "/login tjcoder")
	waitFor(t, a, "tjcoder active", func() bool { return providers.Load().Active == "tjcoder" })
	waitForText(t, a, "Already signed in", func() string { return a.activity.GetText(true) })
	onLoop(t, a, func() {
		if a.leftPane != nil {
			t.Error("no sign-in form when already signed in")
		}
	})
}

func TestLoginArgumentPalette(t *testing.T) {
	a, _ := startSimApp(t)
	onLoop(t, a, func() {
		a.input.SetText("/login ")
		a.updateInputHint("/login ")
		if a.hintMode != "wsargs" || a.wsArgCmd != "login" {
			t.Fatalf("mode %q cmd %q", a.hintMode, a.wsArgCmd)
		}
		if got := strings.Join(wsLabels(a), "|"); got != "tjcoder|ollama|ollama-cloud" {
			t.Fatal(got)
		}
		a.updateInputHint("/login ol")
		if got := strings.Join(wsLabels(a), "|"); got != "ollama|ollama-cloud" {
			t.Fatal(got)
		}
		a.updateInputHint("/login ollama-c")
		if got := strings.Join(wsLabels(a), "|"); got != "ollama-cloud" {
			t.Fatal(got)
		}
		a.input.SetText("/login ollama-c")
		a.completeWorkspaceArg()
		if a.input.GetText() != "/login ollama-cloud" {
			t.Fatalf("tab: %q", a.input.GetText())
		}
		a.updateInputHint("/login zzz")
		if a.hintMode == "wsargs" {
			t.Error("no matches should drop the palette")
		}
		if _, _, ok := a.loginArgHints("/login a b"); ok {
			t.Error("a second word is free-form")
		}
		if _, _, ok := a.loginArgHints("/logout "); ok {
			t.Error("only /login completes providers")
		}
	})
}

func TestHelpAndPaletteMentionLoginProvider(t *testing.T) {
	found := false
	for _, c := range slashCommands {
		if c.cmd == "login" {
			found = true
			if !strings.Contains(c.desc, "/login [provider]") {
				t.Errorf("palette entry: %q", c.desc)
			}
		}
	}
	if !found {
		t.Fatal("login missing from the palette")
	}
}

// ---- first message while signed out -------------------------------------

func TestSignedOutStartupIsASoftHint(t *testing.T) {
	g := hubEnv(t)
	a, _ := startUnsignedErgo(t, g)
	onLoop(t, a, func() {
		act := a.activity.GetText(true)
		if !strings.Contains(act, "Not signed in — type /login to sign in or pick another provider") {
			t.Errorf("missing hint:\n%s", act)
		}
		if strings.Contains(act, "Starting ergo sign-in") || strings.Contains(act, "cognition") {
			t.Errorf("startup must not open a login or run the recap while signed out:\n%s", act)
		}
		if a.leftPane != nil || a.livePrompt != nil {
			t.Error("nothing should be open at startup")
		}
	})
}

func TestFirstMessageWhileSignedOutOffersTheHubAndKeepsTheMessage(t *testing.T) {
	g := hubEnv(t)
	a, sim := startUnsignedErgo(t, g)
	typeLine(sim, "hello there")
	txt := waitCard(t, a, "GET STARTED")
	if !strings.Contains(txt, "Try again") {
		t.Errorf("a waiting message gets Try again:\n%s", txt)
	}
	onLoop(t, a, func() {
		if got := a.input.GetText(); got != "hello there" {
			t.Errorf("the message must stay in the input, got %q", got)
		}
		if a.busy {
			t.Error("nothing may be sent")
		}
	})
	if n := g.chatHits.Load(); n != 0 {
		t.Errorf("%d chat requests while signed out", n)
	}
	// Try again while still signed out just brings the hub back.
	injectKey(sim, tcell.KeyRune, '4')
	waitFor(t, a, "hub again", func() bool {
		return a.livePrompt != nil && strings.Contains(flatCard(a.livePrompt.view.GetText(true)), "GET STARTED")
	})
}

// A full sign-in: the waiting message is offered, never auto-sent.
func TestSignInOffersSendMyMessageAndDoesNotAutoSend(t *testing.T) {
	g := hubEnv(t)
	a, sim := startUnsignedErgo(t, g)
	typeLine(sim, "hello there")
	waitCard(t, a, "GET STARTED")
	injectKey(sim, tcell.KeyRune, '1') // Sign in to TJ Coder
	waitScreen(t, a, sim, "Send sign-in link")
	for _, r := range "me@x.co" {
		injectKey(sim, tcell.KeyRune, r)
	}
	injectKey(sim, tcell.KeyEnter, 0) // field -> Send sign-in link
	injectKey(sim, tcell.KeyEnter, 0) // press it
	waitScreen(t, a, sim, "You're signed in")
	txt := snap(t, a, sim)
	for _, want := range []string{"$5.00 welcome credit added", "Your message is waiting:", "hello there", "Send my message", "Start chatting"} {
		if !strings.Contains(txt, want) {
			t.Errorf("success screen missing %q:\n%s", want, txt)
		}
	}
	if strings.Contains(txt, testKey) {
		t.Error("the key must never be rendered")
	}
	if n := g.chatHits.Load(); n != 0 {
		t.Fatalf("message was sent without being asked (%d chat requests)", n)
	}
	if got := providers.Load().Active; got != "tjcoder" {
		t.Errorf("active provider = %q after signing in", got)
	}
	if auth.TokenStore() == "" {
		t.Error("key was not stored")
	}
	// Choose "Send my message" (the first button).
	injectKey(sim, tcell.KeyEnter, 0)
	waitFor(t, a, "message sent", func() bool { return g.chatHits.Load() >= 1 })
	// The fake gateway answers 403 account_inactive, which mounts the red
	// retry card; wait for it so no goroutine outlives the test.
	waitCard(t, a, "ERROR")
	onLoop(t, a, func() {
		if a.leftPane != nil {
			t.Error("success screen should have closed")
		}
		if !strings.Contains(a.transcript.GetText(true), "hello there") {
			t.Error("the sent message is in the transcript")
		}
	})
}

func TestSignInWithoutWaitingMessageHasNoSendButton(t *testing.T) {
	g := hubEnv(t)
	a, sim := startUnsignedErgo(t, g)
	onLoop(t, a, func() { a.beginDeviceLogin("me@x.co") })
	waitScreen(t, a, sim, "You're signed in")
	if txt := snap(t, a, sim); strings.Contains(txt, "Send my message") || !strings.Contains(txt, "Start chatting") {
		t.Errorf("buttons:\n%s", txt)
	}
}

func TestSignInShowsContactingLineThenCancels(t *testing.T) {
	g := hubEnv(t)
	g.startGate = make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-g.startGate:
		default:
			close(g.startGate)
		}
	})
	a, sim := startUnsignedErgo(t, g)
	onLoop(t, a, func() { a.beginDeviceLogin("me@x.co") })
	waitScreen(t, a, sim, "Contacting TJ Coder AI Labs…")
	if strings.Contains(strings.ToLower(snap(t, a, sim)), "contacting ergo") {
		t.Error("old wording")
	}
	injectKey(sim, tcell.KeyEscape, 0)
	waitFor(t, a, "cancelled", func() bool { return a.leftPane == nil && a.loginCancel == nil })
	waitForText(t, a, "Sign-in cancelled", func() string { return a.activity.GetText(true) })
}

func TestSignInShowsCodeScreen(t *testing.T) {
	g := hubEnv(t)
	a, sim := startUnsignedErgo(t, g)
	onLoop(t, a, func() { a.beginDeviceLogin("me@x.co") })
	waitScreen(t, a, sim, "Check your email")
	txt := snap(t, a, sim)
	for _, want := range []string{"ABCD-2345", "me@x.co", "https://x.example/verify", "Esc to cancel"} {
		if !strings.Contains(txt, want) {
			t.Errorf("code screen missing %q:\n%s", want, txt)
		}
	}
	injectKey(sim, tcell.KeyEscape, 0)
	waitFor(t, a, "cancelled", func() bool { return a.leftPane == nil && a.loginCancel == nil })
}

// ---- failure screens -----------------------------------------------------

func TestSignInFailureOffersTryAgainAndBack(t *testing.T) {
	g := hubEnv(t)
	g.startStatus, g.startBody = http.StatusForbidden, bodySignupClosed
	a, sim := startUnsignedErgo(t, g)
	onLoop(t, a, func() { a.beginDeviceLogin("me@x.co") })
	waitScreen(t, a, sim, "Sign-ups are closed for me@x.co")
	txt := snap(t, a, sim)
	for _, want := range []string{"Try again", "Back", "Try the email you signed up with,", "use a different provider."} {
		if !strings.Contains(txt, want) {
			t.Errorf("failure screen missing %q:\n%s", want, txt)
		}
	}
	if low := strings.ToLower(txt); strings.Contains(low, "request access") || strings.Contains(low, "allowlist") || strings.Contains(low, "admin") {
		t.Errorf("no request-access pointer on signup-closed:\n%s", txt)
	}
	// Try again -> the email form, prefilled.
	injectKey(sim, tcell.KeyEnter, 0)
	waitScreen(t, a, sim, "Send sign-in link")
	if !strings.Contains(snap(t, a, sim), "me@x.co") {
		t.Errorf("email should be prefilled:\n%s", snap(t, a, sim))
	}
}

func TestSignInFailureBackReturnsToHub(t *testing.T) {
	g := hubEnv(t)
	g.startStatus, g.startBody = http.StatusForbidden, bodySignupClosed
	a, sim := startUnsignedErgo(t, g)
	onLoop(t, a, func() { a.beginDeviceLogin("me@x.co") })
	waitScreen(t, a, sim, "Sign-ups are closed")
	injectKey(sim, tcell.KeyTab, 0) // Try again -> Back
	injectKey(sim, tcell.KeyEnter, 0)
	waitCard(t, a, "GET STARTED")
	onLoop(t, a, func() {
		if a.leftPane != nil {
			t.Error("Back closes the failure screen")
		}
	})
}

func TestSignInFailureEscJustCloses(t *testing.T) {
	g := hubEnv(t)
	g.startStatus, g.startBody = http.StatusForbidden, bodySignupClosed
	a, sim := startUnsignedErgo(t, g)
	onLoop(t, a, func() { a.beginDeviceLogin("me@x.co") })
	waitScreen(t, a, sim, "Sign-ups are closed")
	injectKey(sim, tcell.KeyEscape, 0)
	waitFor(t, a, "closed", func() bool { return a.leftPane == nil && a.livePrompt == nil })
}

// ---- rejected key --------------------------------------------------------

func TestRejectedKeyOpensSignInAgainInsteadOfRedError(t *testing.T) {
	g := hubEnv(t)
	g.chatStatus, g.chatBody = http.StatusUnauthorized, bodyInvalidKey
	a, sim := startSignedErgo(t, g)
	typeLine(sim, "hello")
	txt := waitCard(t, a, "SIGN IN AGAIN")
	for _, want := range []string{"Your TJ Coder session is no longer valid.", "Sign in again", "Use a different provider", "Configure providers", "Try again"} {
		if !strings.Contains(txt, want) {
			t.Errorf("card missing %q:\n%s", want, txt)
		}
	}
	if strings.Contains(txt, "API key invalid") || strings.Contains(txt, "ERROR") {
		t.Errorf("the red error is replaced:\n%s", txt)
	}
	onLoop(t, a, func() {
		if !a.ergoKeyRejected {
			t.Error("the rejection should be remembered")
		}
	})
	// Try again resends the turn through the same gateway.
	first := g.chatHits.Load()
	t.Logf("chat hits before Try again: %d", first)
	injectKey(sim, tcell.KeyRune, '4')
	waitFor(t, a, "turn resent", func() bool { return g.chatHits.Load() > first })
	waitCard(t, a, "SIGN IN AGAIN")
}

func TestLoginAfterRejectionShowsSignInAgain(t *testing.T) {
	g := hubEnv(t)
	a, sim := startSignedErgo(t, g)
	onLoop(t, a, func() { a.ergoKeyRejected = true })
	typeLine(sim, "/login")
	waitCard(t, a, "SIGN IN AGAIN")
}

func TestAccountInactiveStaysAPlainError(t *testing.T) {
	g := hubEnv(t) // chat answers 403 account_inactive by default
	a, sim := startSignedErgo(t, g)
	typeLine(sim, "hello")
	txt := waitCard(t, a, "ERROR")
	if strings.Contains(txt, "SIGN IN AGAIN") || !strings.Contains(txt, "Retry") {
		t.Errorf("account_inactive keeps the red retry card:\n%s", txt)
	}
	onLoop(t, a, func() {
		if a.ergoKeyRejected {
			t.Error("a suspended account is not a rejected key")
		}
		if n := a.ergoRunErrorNote(&client.ErgoAuthError{Code: "account_inactive", Status: 403}); n != "" {
			t.Errorf("no re-login advice for a suspended account: %q", n)
		}
	})
}

func TestOutOfCreditsStaysAPlainError(t *testing.T) {
	g := hubEnv(t)
	g.chatStatus, g.chatBody = http.StatusPaymentRequired, `{"error":{"code":"insufficient_credits","message":"Out of credits"},"balance_cents":0}`
	a, sim := startSignedErgo(t, g)
	typeLine(sim, "hello")
	txt := waitCard(t, a, "ERROR")
	if strings.Contains(txt, "SIGN IN AGAIN") {
		t.Errorf("402 is not an auth problem:\n%s", txt)
	}
}

// ---- switching and signing out ---------------------------------------------

func TestSwitchAccountSignsOutFirstThenAsksForEmail(t *testing.T) {
	g := hubEnv(t)
	a, sim := startSignedErgo(t, g)
	typeLine(sim, "/login")
	waitCard(t, a, "TJ CODER ACCOUNT")
	injectKey(sim, tcell.KeyRune, '1') // Switch account
	waitScreen(t, a, sim, "Send sign-in link")
	if n := g.logoutHits.Load(); n != 1 {
		t.Errorf("the old key must be revoked first (logout hits = %d)", n)
	}
	if got, _ := g.logoutAuth.Load().(string); got != "Bearer "+testKey {
		t.Errorf("logout used the saved key, got auth header of length %d", len(got))
	}
	if auth.TokenStore() != "" {
		t.Error("the old key must be gone from storage")
	}
}

func TestSignOutFromHubShowsGetStarted(t *testing.T) {
	g := hubEnv(t)
	a, sim := startSignedErgo(t, g)
	typeLine(sim, "/login")
	waitCard(t, a, "TJ CODER ACCOUNT")
	injectKey(sim, tcell.KeyRune, '2') // Sign out
	waitFor(t, a, "get started hub", func() bool {
		return a.livePrompt != nil && strings.Contains(flatCard(a.livePrompt.view.GetText(true)), "GET STARTED")
	})
	if g.logoutHits.Load() != 1 || auth.TokenStore() != "" {
		t.Errorf("sign-out must revoke and clear (hits=%d store=%q)", g.logoutHits.Load(), auth.TokenStore())
	}
}

func TestSignOutWithEnvKeyDoesNotPretendAndHidesSwitch(t *testing.T) {
	g := hubEnv(t)
	t.Setenv("ERGO_API_KEY", testKey)
	a, sim := startSimAppWith(t, client.NewErgo(g.srv.URL, testKey, 0), identityScreen)
	typeLine(sim, "/login")
	txt := waitCard(t, a, "TJ CODER ACCOUNT")
	if strings.Contains(txt, "Switch account") {
		t.Errorf("an environment key cannot be switched:\n%s", txt)
	}
	injectKey(sim, tcell.KeyRune, '1') // Sign out
	waitForText(t, a, "ERGO_API_KEY is set in your environment", func() string { return a.activity.GetText(true) })
	if g.logoutHits.Load() != 0 {
		t.Error("an environment key must not be revoked")
	}
	onLoop(t, a, func() {
		if a.livePrompt != nil {
			t.Error("no hub should reappear while the environment still signs us in")
		}
	})
}

func TestLogoutCommandStillWorks(t *testing.T) {
	g := hubEnv(t)
	a, sim := startSignedErgo(t, g)
	typeLine(sim, "/logout")
	waitForText(t, a, "Signed out of TJ Coder (key revoked)", func() string { return a.activity.GetText(true) })
	if auth.TokenStore() != "" {
		t.Error("key not cleared")
	}
	onLoop(t, a, func() {
		if a.livePrompt != nil || a.leftPane != nil {
			t.Error("/logout itself opens nothing")
		}
	})
}

// ---- views -------------------------------------------------------------

func TestLoginCodeViewCompactOnNarrowPanes(t *testing.T) {
	pal := darkPalette()
	st := &auth.DeviceStart{UserCode: "ABCD-2345", VerificationURI: "https://x.example/verify"}
	wide := plain(loginCodeViewW(pal, 80, 30, "me@x.co", st, time.Minute, false))
	if !strings.Contains(wide, "┌───┬") || !strings.Contains(wide, "Check your email") {
		t.Errorf("wide view:\n%s", wide)
	}
	narrow := plain(loginCodeViewW(pal, 38, 30, "me@x.co", st, time.Minute, false))
	if strings.ContainsAny(narrow, "┌│└") {
		t.Errorf("narrow view must not draw boxes:\n%s", narrow)
	}
	for _, want := range []string{"ABCD-2345", "me@x.co", "https://x.example/verify", "1:00"} {
		if !strings.Contains(narrow, want) {
			t.Errorf("narrow view missing %q:\n%s", want, narrow)
		}
	}
}

func TestLoginViewsFitShortPanes(t *testing.T) {
	pal := darkPalette()
	st := &auth.DeviceStart{UserCode: "ABCD-2345", VerificationURI: "https://x.example/verify"}
	// 80 wide but only 12 rows: no boxes, no spacer rows, everything that
	// matters still there.
	short := plain(loginCodeViewW(pal, 80, 12, "me@x.co", st, time.Minute, true))
	if strings.ContainsAny(short, "┌│└") || strings.Contains(short, "\n\n") {
		t.Errorf("short pane layout:\n%s", short)
	}
	for _, want := range []string{"me@x.co", "ABCD-2345", "https://x.example/verify", "opened in your browser", "Esc to cancel"} {
		if !strings.Contains(short, want) {
			t.Errorf("short view missing %q:\n%s", want, short)
		}
	}
	if n := strings.Count(strings.TrimRight(short, "\n"), "\n") + 1; n > 5 {
		t.Errorf("short view uses %d rows, want <= 5:\n%s", n, short)
	}
	agents := client.ErgoFallbackModels()
	ok := plain(loginSuccessViewW(pal, 80, 12, "me@x.co", true, 500, agents, agents[0], "file"))
	if strings.Contains(ok, "Key stored in") || strings.Contains(ok, "Your agents") || strings.Contains(ok, "\n\n") {
		t.Errorf("short success view:\n%s", ok)
	}
	for _, want := range []string{"✓ You're signed in", "$5.00 welcome credit added", agents[0]} {
		if !strings.Contains(ok, want) {
			t.Errorf("short success view missing %q:\n%s", want, ok)
		}
	}
}

func TestLoginSuccessViewAlignsAgents(t *testing.T) {
	pal := darkPalette()
	agents := client.ErgoFallbackModels()
	sel := agents[0]
	wide := plain(loginSuccessViewW(pal, 100, 30, "me@x.co", false, 1234, agents, sel, "file"))
	cols := map[int]bool{}
	for _, name := range agents {
		d := client.ErgoAgentDesc(name)
		if d == "" {
			continue
		}
		for _, line := range strings.Split(wide, "\n") {
			if strings.Contains(line, name) && strings.Contains(line, d) {
				cols[len([]rune(line[:strings.Index(line, d)]))] = true
			}
		}
	}
	if len(cols) != 1 {
		t.Errorf("descriptions should share one column, got %v:\n%s", cols, wide)
	}
	if !strings.Contains(wide, "/login account menu") {
		t.Errorf("footer should point at the account menu:\n%s", wide)
	}
	narrow := plain(loginSuccessViewW(pal, 40, 30, "me@x.co", false, 1234, agents, sel, "file"))
	for _, name := range agents {
		if d := client.ErgoAgentDesc(name); d != "" && strings.Contains(narrow, d) {
			t.Errorf("narrow view drops descriptions rather than misaligning them:\n%s", narrow)
			break
		}
	}
}

func TestShowModalHeadingIsFlushWithBody(t *testing.T) {
	a, sim := startSimApp(t)
	onLoop(t, a, func() {
		tv := tview.NewTextView().SetDynamicColors(true)
		tv.SetBorderPadding(0, 0, 2, 0) // list-style content inset
		tv.SetText("body")
		a.showModal("Sign in", tv)
	})
	waitScreen(t, a, sim, "SIGN IN")
	if h, b := colOf(t, a, sim, "SIGN IN"), colOf(t, a, sim, "body"); h < 0 || h != b {
		t.Errorf("heading col %d, content col %d; want flush", h, b)
	}
}
