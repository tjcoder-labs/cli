package tooling

import (
	"context"
	"encoding/json"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/tjcoder-labs/cli/internal/agent"
	"github.com/tjcoder-labs/cli/internal/client"
	"github.com/tjcoder-labs/cli/internal/tools"
)

// blockingPrompter blocks Ask until an answer arrives or ctx ends, the
// way the TUI card does. It returns only ctx.Err() on cancellation, so
// the tool must classify steering from the context cause.
type blockingPrompter struct {
	started chan tools.PromptRequest
	answer  chan tools.PromptAnswer
}

func newBlockingPrompter() *blockingPrompter {
	return &blockingPrompter{started: make(chan tools.PromptRequest, 1), answer: make(chan tools.PromptAnswer, 1)}
}

func (p *blockingPrompter) Ask(ctx context.Context, req tools.PromptRequest) (tools.PromptAnswer, error) {
	p.started <- req
	select {
	case a := <-p.answer:
		return a, nil
	case <-ctx.Done():
		return tools.PromptAnswer{Index: -1, Cancelled: true}, ctx.Err()
	}
}
func (p *blockingPrompter) AskSecret(context.Context, string, string) ([]byte, error) {
	return nil, nil
}
func (p *blockingPrompter) Record(string) {}

func askCall(args string) client.Message {
	return client.Message{Role: "assistant", ToolCalls: []client.ToolCall{{
		Type:     "function",
		Function: client.ToolFunctionCall{Name: "ask_user", Arguments: json.RawMessage(args)},
	}}}
}

func toolResults(history []client.Message) []string {
	var out []string
	for _, m := range history {
		if m.Role == "tool" {
			out = append(out, m.Content)
		}
	}
	return out
}

const askArgs = `{"question":"Which environment?","header":"Deploy target","options":[{"label":"staging"},{"label":"production","description":"live traffic"}],"default":"staging"}`

type runOut struct {
	history []client.Message
	err     error
}

func startRun(t *testing.T, ctx context.Context, r *Runner, enabled []string) chan runOut {
	t.Helper()
	done := make(chan runOut, 1)
	go func() {
		h, err := r.Run(ctx, nil, "deploy it", agent.Config{Name: "t", Prompt: "base"}, "m", enabled, nil)
		done <- runOut{h, err}
	}()
	return done
}

func waitRun(t *testing.T, done chan runOut) runOut {
	t.Helper()
	select {
	case o := <-done:
		return o
	case <-time.After(10 * time.Second):
		t.Fatal("runner did not finish")
	}
	return runOut{}
}

func waitAsked(t *testing.T, p *blockingPrompter) tools.PromptRequest {
	t.Helper()
	select {
	case req := <-p.started:
		return req
	case <-time.After(10 * time.Second):
		t.Fatal("ask_user never reached the prompter")
	}
	return tools.PromptRequest{}
}

func TestRunner_AskUserBlocksUntilAnswered(t *testing.T) {
	provider := &scriptedProvider{responses: []client.Message{askCall(askArgs), {Role: "assistant", Content: "deploying to production"}}}
	p := newBlockingPrompter()
	r := &Runner{Provider: provider, Registry: tools.NewRegistry(provider), WorkspaceRoot: t.TempDir(), MaxSteps: 4, Prompter: p}
	done := startRun(t, context.Background(), r, []string{"ask_user"})
	req := waitAsked(t, p)
	if req.Title != "Deploy target" || req.Default != 1 || !req.AllowOther || req.Descriptions[1] != "live traffic" {
		t.Fatalf("request = %+v", req)
	}
	select {
	case <-done:
		t.Fatal("runner must block while the question is open")
	case <-time.After(50 * time.Millisecond):
	}
	p.answer <- tools.PromptAnswer{Index: 1, Choice: "production"}
	o := waitRun(t, done)
	if o.err != nil {
		t.Fatal(o.err)
	}
	if got := toolResults(o.history); len(got) != 1 || got[0] != "ANSWERED: production" {
		t.Fatalf("tool results = %q", got)
	}
	sys := provider.requests[0].Messages[0].Content
	if !strings.Contains(sys, tools.AskUserGuidance) {
		t.Fatal("ask_user guidance missing from the system prompt")
	}
	if strings.Contains(sys, tools.AskUserHeadlessGuidance) {
		t.Fatal("interactive runs must not carry the headless ask_user note")
	}
}

func TestRunner_AskUserDismissed(t *testing.T) {
	provider := &scriptedProvider{responses: []client.Message{askCall(askArgs), {Role: "assistant", Content: "ok"}}}
	p := newBlockingPrompter()
	r := &Runner{Provider: provider, Registry: tools.NewRegistry(provider), WorkspaceRoot: t.TempDir(), MaxSteps: 4, Prompter: p}
	done := startRun(t, context.Background(), r, []string{"ask_user"})
	waitAsked(t, p)
	p.answer <- tools.PromptAnswer{Index: -1, Cancelled: true}
	o := waitRun(t, done)
	if got := toolResults(o.history); len(got) != 1 || got[0] != tools.AskDismissed {
		t.Fatalf("tool results = %q", got)
	}
}

func TestRunner_AskUserSteeringWhileWaiting(t *testing.T) {
	before := runtime.NumGoroutine()
	provider := &scriptedProvider{responses: []client.Message{askCall(askArgs), {Role: "assistant", Content: "switching to docs"}}}
	p := newBlockingPrompter()
	steer := make(chan string, 1)
	r := &Runner{Provider: provider, Registry: tools.NewRegistry(provider), WorkspaceRoot: t.TempDir(), MaxSteps: 4, Prompter: p, Steering: steer}
	done := startRun(t, context.Background(), r, []string{"ask_user"})
	waitAsked(t, p)
	steer <- "never mind, update the docs instead"
	o := waitRun(t, done)
	if o.err != nil {
		t.Fatal(o.err)
	}
	if got := toolResults(o.history); len(got) != 1 || got[0] != tools.AskInterrupted {
		t.Fatalf("tool results = %q", got)
	}
	// The steering message reaches the next provider call.
	found := false
	for _, m := range provider.requests[1].Messages {
		if m.Role == "user" && strings.Contains(m.Content, "update the docs") {
			found = true
		}
	}
	if !found {
		t.Fatal("steering message missing from the follow-up request")
	}
	time.Sleep(20 * time.Millisecond)
	if after := runtime.NumGoroutine(); after > before+1 {
		t.Fatalf("goroutine leak: %d before, %d after", before, after)
	}
}

func TestRunner_AskUserCancelWhileWaiting(t *testing.T) {
	provider := &scriptedProvider{responses: []client.Message{askCall(askArgs)}}
	p := newBlockingPrompter()
	r := &Runner{Provider: provider, Registry: tools.NewRegistry(provider), WorkspaceRoot: t.TempDir(), MaxSteps: 4, Prompter: p, Steering: make(chan string, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	done := startRun(t, ctx, r, []string{"ask_user"})
	waitAsked(t, p)
	cancel()
	o := waitRun(t, done) // must unblock; an error from the cancelled turn is fine
	for _, c := range toolResults(o.history) {
		if c != tools.AskCancelled {
			t.Fatalf("tool result = %q", c)
		}
	}
}

func TestRunner_AskUserNotEnabled(t *testing.T) {
	provider := &scriptedProvider{responses: []client.Message{askCall(askArgs), {Role: "assistant", Content: "ok"}}}
	p := newBlockingPrompter()
	r := &Runner{Provider: provider, Registry: tools.NewRegistry(provider), WorkspaceRoot: t.TempDir(), MaxSteps: 4, Prompter: p}
	done := startRun(t, context.Background(), r, []string{"list_directory"})
	o := waitRun(t, done)
	select {
	case <-p.started:
		t.Fatal("a disabled ask_user must not reach the prompter")
	default:
	}
	got := toolResults(o.history)
	if len(got) != 1 || !strings.Contains(got[0], "not enabled") {
		t.Fatalf("tool results = %q", got)
	}
	if strings.Contains(provider.requests[0].Messages[0].Content, "Asking the user (ask_user)") {
		t.Fatal("guidance must not be injected when ask_user is disabled")
	}
}

func TestRunner_ToolLessRunNeverPrompts(t *testing.T) {
	provider := &scriptedProvider{responses: []client.Message{askCall(askArgs), {Role: "assistant", Content: "recap"}}}
	p := newBlockingPrompter()
	r := &Runner{Provider: provider, Registry: tools.NewRegistry(provider), WorkspaceRoot: t.TempDir(), MaxSteps: 4, Prompter: p}
	o := waitRun(t, startRun(t, context.Background(), r, nil))
	select {
	case <-p.started:
		t.Fatal("a tool-less run (recap/compaction) must not prompt")
	default:
	}
	if got := toolResults(o.history); len(got) != 1 || !strings.Contains(got[0], "not enabled") {
		t.Fatalf("tool results = %q", got)
	}
}

// TestRunner_HeadlessAuthorizationNotApproved: a turn whose ask_user is
// an authorization with a "Yes, proceed" default, run with no prompter
// (headless), gets the explicit not-approved result, never the default.
func TestRunner_HeadlessAuthorizationNotApproved(t *testing.T) {
	args := `{"question":"Wipe the cache directory?","kind":"authorization","options":["Yes, proceed","No"],"default":"Yes, proceed","detail":"rm -rf ~/.cache/coder"}`
	provider := &scriptedProvider{responses: []client.Message{askCall(args), {Role: "assistant", Content: "not wiping"}}}
	r := &Runner{Provider: provider, Registry: tools.NewRegistry(provider), WorkspaceRoot: t.TempDir(), MaxSteps: 4}
	o := waitRun(t, startRun(t, context.Background(), r, []string{"ask_user"}))
	if o.err != nil {
		t.Fatal(o.err)
	}
	got := toolResults(o.history)
	if len(got) != 1 || got[0] != tools.AskUnavailableAuth {
		t.Fatalf("tool results = %q", got)
	}
	if strings.Contains(got[0], "Yes, proceed") || strings.HasPrefix(got[0], tools.AskAnswered) {
		t.Fatalf("headless authorization must not be approved: %q", got[0])
	}
	sys := provider.requests[0].Messages[0].Content
	if !strings.Contains(sys, tools.AskUserGuidance) || !strings.Contains(sys, tools.AskUserHeadlessGuidance) {
		t.Fatal("headless runs must carry the non-interactive ask_user guidance")
	}
}
