package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

type fakeSudo struct {
	cached      bool
	validateErr error
	gotPassword string
	validated   int
	invalidated int
	pwBuf       []byte
}

func (f *fakeSudo) Path() (string, error)       { return "/usr/bin/sudo", nil }
func (f *fakeSudo) Cached(context.Context) bool { return f.cached }
func (f *fakeSudo) Invalidate(context.Context)  { f.invalidated++; f.cached = false }
func (f *fakeSudo) Validate(_ context.Context, pw []byte) error {
	f.validated++
	f.gotPassword = string(pw)
	f.pwBuf = pw
	if f.validateErr == nil {
		f.cached = true
	}
	return f.validateErr
}

type fakePrompter struct {
	answers  []PromptAnswer
	asked    []PromptRequest
	secret   []byte
	secrets  int
	recorded []string
}

func (p *fakePrompter) Ask(_ context.Context, req PromptRequest) (PromptAnswer, error) {
	p.asked = append(p.asked, req)
	if len(p.answers) == 0 {
		return PromptAnswer{Index: -1, Cancelled: true}, nil
	}
	a := p.answers[0]
	p.answers = p.answers[1:]
	return a, nil
}
func (p *fakePrompter) AskSecret(context.Context, string, string) ([]byte, error) {
	p.secrets++
	if p.secret == nil {
		return nil, nil
	}
	return append([]byte(nil), p.secret...), nil
}
func (p *fakePrompter) Record(t string) { p.recorded = append(p.recorded, t) }

func withFakeSudo(t *testing.T, f *fakeSudo) {
	t.Helper()
	old := sudoOps
	sudoOps = f
	t.Cleanup(func() { sudoOps = old })
}

func TestSudoInvocationsDetection(t *testing.T) {
	yes := []string{
		"sudo apt update", "  sudo -E make install", "cd x && sudo rm y", "echo hi | sudo tee /etc/x",
		"FOO=1 sudo ls", "env FOO=1 sudo ls", "nohup sudo x", "(sudo id)", "x; sudo y", "bash -c 'sudo id'",
		"timeout 10 sudo id", "/usr/bin/sudo id", "echo $(sudo cat /etc/shadow)",
	}
	for _, c := range yes {
		if !IsSudoCommand(c) {
			t.Errorf("expected sudo detected in %q", c)
		}
	}
	no := []string{"ls -la", "grep sudo /var/log/auth.log", "echo sudo", "cat sudoers.txt", "pseudo x", "man sudoers"}
	for _, c := range no {
		if IsSudoCommand(c) {
			t.Errorf("did not expect sudo in %q", c)
		}
	}
}

func TestSudoGateHeadlessDeniedWithoutFlag(t *testing.T) {
	withFakeSudo(t, &fakeSudo{})
	_, err := authorizeSudo(context.Background(), "sudo id", ExecEnv{})
	if !errors.Is(err, errSudoDenied) || !strings.Contains(err.Error(), "--allow-sudo") {
		t.Fatalf("headless sudo must be denied with a hint, got %v", err)
	}
}

func TestSudoGateHeadlessAllowedNeverPrompts(t *testing.T) {
	f := &fakeSudo{}
	withFakeSudo(t, f)
	g, err := authorizeSudo(context.Background(), "sudo id", ExecEnv{Sudo: &SudoPolicy{AllowNonInteractive: true}})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	if f.validated != 0 {
		t.Fatal("headless must never validate/prompt")
	}
	dir := strings.TrimPrefix(g.Env[0], "CODER_SUDO_SHIM=")
	b, err := os.ReadFile(dir + "/sudo")
	if err != nil || !strings.Contains(string(b), "' -n \"$@\"") {
		t.Fatalf("shim must force sudo -n, got %q (%v)", b, err)
	}
}

func TestSudoGateDenyAndEsc(t *testing.T) {
	withFakeSudo(t, &fakeSudo{cached: true})
	// A zero-value answer (nothing picked) and an index whose label does
	// not match must deny too: the first row is never applied by default.
	for _, ans := range []PromptAnswer{{Index: 2, Choice: "Deny"}, {Index: -1, Cancelled: true}, {}, {Index: 0, Choice: "Deny"}} {
		p := &fakePrompter{answers: []PromptAnswer{ans}}
		_, err := authorizeSudo(context.Background(), "sudo id", ExecEnv{Prompter: p, Sudo: &SudoPolicy{}})
		if !errors.Is(err, errSudoDenied) {
			t.Fatalf("answer %+v: want denial, got %v", ans, err)
		}
		if len(p.asked) != 1 || p.asked[0].Detail != "sudo id" || len(p.asked[0].Options) != 3 {
			t.Fatalf("prompt must show the exact command with 3 options: %+v", p.asked)
		}
		if len(p.recorded) != 1 || !strings.Contains(p.recorded[0], "denied") {
			t.Fatalf("decision must be recorded: %v", p.recorded)
		}
	}
}

func TestSudoGateAllowOncePrimesAndDropsTicketZeroingPassword(t *testing.T) {
	f := &fakeSudo{}
	withFakeSudo(t, f)
	pol := &SudoPolicy{}
	p := &fakePrompter{answers: []PromptAnswer{{Index: 0, Choice: "Allow once"}}, secret: []byte("hunter2")}
	g, err := authorizeSudo(context.Background(), "sudo apt update", ExecEnv{Prompter: p, Sudo: pol})
	if err != nil {
		t.Fatal(err)
	}
	if f.gotPassword != "hunter2" || f.validated != 1 {
		t.Fatalf("password must be fed to sudo -S once, got %d", f.validated)
	}
	for _, c := range f.pwBuf {
		if c != 0 {
			t.Fatal("password buffer must be zeroed after use")
		}
	}
	for _, r := range p.recorded {
		if strings.Contains(r, "hunter2") {
			t.Fatal("password leaked into the transcript record")
		}
	}
	if !strings.Contains(g.Note, "allowed once") || strings.Contains(g.Note, "hunter2") {
		t.Fatalf("note = %q", g.Note)
	}
	if !pol.TicketMayBeLive() {
		t.Fatal("primed ticket must be tracked while the command runs")
	}
	g.Close()
	if f.invalidated != 1 || pol.TicketMayBeLive() {
		t.Fatal("allow once must drop the ticket the gate primed")
	}
	if pol.SessionApproved() {
		t.Fatal("allow once must not grant session approval")
	}
}

func TestSudoGateSessionApprovalSkipsPromptAndUsesCache(t *testing.T) {
	f := &fakeSudo{}
	withFakeSudo(t, f)
	pol := &SudoPolicy{}
	p := &fakePrompter{answers: []PromptAnswer{{Index: 1, Choice: "Allow sudo for this session"}}, secret: []byte("pw")}
	g, err := authorizeSudo(context.Background(), "sudo id", ExecEnv{Prompter: p, Sudo: pol})
	if err != nil {
		t.Fatal(err)
	}
	g.Close()
	if f.invalidated != 0 || !pol.SessionApproved() {
		t.Fatal("session approval keeps sudo's timestamp ticket")
	}
	g, err = authorizeSudo(context.Background(), "sudo whoami", ExecEnv{Prompter: p, Sudo: pol})
	if err != nil {
		t.Fatal(err)
	}
	g.Close()
	if len(p.asked) != 1 || p.secrets != 1 {
		t.Fatalf("second command must not re-prompt (asked=%d secrets=%d)", len(p.asked), p.secrets)
	}
}

func TestSudoGateBadPasswordAndCancelledPassword(t *testing.T) {
	withFakeSudo(t, &fakeSudo{validateErr: errors.New("nope")})
	p := &fakePrompter{answers: []PromptAnswer{{Index: 0, Choice: "Allow once"}}, secret: []byte("bad")}
	if _, err := authorizeSudo(context.Background(), "sudo id", ExecEnv{Prompter: p, Sudo: &SudoPolicy{}}); !errors.Is(err, errSudoDenied) {
		t.Fatalf("bad password must deny, got %v", err)
	}
	withFakeSudo(t, &fakeSudo{})
	p = &fakePrompter{answers: []PromptAnswer{{Index: 0, Choice: "Allow once"}}}
	if _, err := authorizeSudo(context.Background(), "sudo id", ExecEnv{Prompter: p, Sudo: &SudoPolicy{}}); !errors.Is(err, errSudoDenied) {
		t.Fatalf("cancelled password must deny, got %v", err)
	}
}

func TestSudoGateRejectsAbsolutePath(t *testing.T) {
	withFakeSudo(t, &fakeSudo{cached: true})
	p := &fakePrompter{answers: []PromptAnswer{{Index: 0, Choice: "Allow once"}}}
	if _, err := authorizeSudo(context.Background(), "/usr/bin/sudo id", ExecEnv{Prompter: p}); !errors.Is(err, errSudoDenied) {
		t.Fatalf("absolute sudo path must be refused, got %v", err)
	}
	if len(p.asked) != 0 {
		t.Fatal("must refuse before prompting")
	}
}

func TestRunCommandSudoDeniedHeadlessDoesNotExecute(t *testing.T) {
	withFakeSudo(t, &fakeSudo{})
	dir := t.TempDir()
	raw, _ := json.Marshal(map[string]any{"command": "sudo touch " + dir + "/ran"})
	_, err := runCommandTool{}.Execute(context.Background(), raw, ExecEnv{WorkspaceRoot: dir})
	if !errors.Is(err, errSudoDenied) {
		t.Fatalf("want denial, got %v", err)
	}
	if _, statErr := os.Stat(dir + "/ran"); statErr == nil {
		t.Fatal("denied command must not run")
	}
}

func TestRunCommandSudoUsesShim(t *testing.T) {
	withFakeSudo(t, &fakeSudo{cached: true})
	dir := t.TempDir()
	p := &fakePrompter{answers: []PromptAnswer{{Index: 0, Choice: "Allow once"}}}
	// `command -v sudo` resolves through PATH, proving the shim is first.
	raw, _ := json.Marshal(map[string]any{"command": "true || sudo id; command -v sudo"})
	res, err := runCommandTool{}.Execute(context.Background(), raw, ExecEnv{WorkspaceRoot: dir, Prompter: p, Sudo: &SudoPolicy{}})
	if err != nil {
		t.Fatalf("%v: %s", err, res.Content)
	}
	if !strings.Contains(res.Content, "coder-sudo-") || !strings.HasPrefix(res.Content, "[sudo: allowed once") {
		t.Fatalf("expected shim on PATH and decision note, got %q", res.Content)
	}
}

func TestAskUserTool(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{"question": "Deploy?", "options": []string{"Yes", "No"}, "kind": "authorization"})
	res, err := askUserTool{}.Execute(context.Background(), raw, ExecEnv{})
	if err != nil || !strings.HasPrefix(res.Content, "UNAVAILABLE") {
		t.Fatalf("headless ask_user must not block: %v %q", err, res.Content)
	}
	p := &fakePrompter{answers: []PromptAnswer{{Index: 1, Choice: "No"}}}
	res, err = askUserTool{}.Execute(context.Background(), raw, ExecEnv{Prompter: p})
	if err != nil || res.Content != "ANSWERED: No" {
		t.Fatalf("got %v %q", err, res.Content)
	}
	if p.asked[0].Kind != PromptAuthorization {
		t.Fatalf("kind = %q", p.asked[0].Kind)
	}
	p = &fakePrompter{answers: []PromptAnswer{{Index: -1, Other: "staging first"}}}
	res, _ = askUserTool{}.Execute(context.Background(), raw, ExecEnv{Prompter: p})
	if res.Content != "ANSWERED (custom): staging first" {
		t.Fatalf("other answer = %q", res.Content)
	}
}
