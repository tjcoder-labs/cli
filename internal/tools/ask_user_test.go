package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestAskUserParseValidation(t *testing.T) {
	cases := []struct {
		name, args, wantErr string
	}{
		{"missing question", `{"options":["a"]}`, "question is required"},
		{"blank question", `{"question":"  "}`, "question is required"},
		{"too many options", `{"question":"q","options":["1","2","3","4","5","6","7"]}`, "at most 6"},
		{"no options no custom", `{"question":"q","allow_custom":false}`, "provide options"},
		{"bad default", `{"question":"q","options":["a","b"],"default":"c"}`, "does not match"},
		{"bad json", `{"question":`, "invalid ask_user arguments"},
	}
	for _, c := range cases {
		_, err := askUserTool{}.parse(json.RawMessage(c.args))
		if err == nil || !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("%s: err = %v, want %q", c.name, err, c.wantErr)
		}
	}

	req, err := askUserTool{}.parse(json.RawMessage(`{"question":"Pick","header":"Target","options":["a",{"label":"b","description":"second"}],"default":2,"multi_select":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if req.Title != "Target" || len(req.Options) != 2 || req.Descriptions[1] != "second" || req.Default != 2 || !req.MultiSelect || !req.AllowOther {
		t.Fatalf("parsed = %+v", req)
	}
	free, err := askUserTool{}.parse(json.RawMessage(`{"question":"Name?"}`))
	if err != nil || len(free.Options) != 0 || !free.AllowOther || free.MultiSelect {
		t.Fatalf("free text = %+v %v", free, err)
	}
	noCustom, _ := askUserTool{}.parse(json.RawMessage(`{"question":"q","options":["a"],"allow_custom":false}`))
	if noCustom.AllowOther {
		t.Fatal("allow_custom:false must hide the custom row")
	}
	byLabel, _ := askUserTool{}.parse(json.RawMessage(`{"question":"q","options":["Alpha","Beta"],"default":"beta"}`))
	if byLabel.Default != 2 {
		t.Fatalf("default by label = %d", byLabel.Default)
	}
	def := askUserTool{}.Definition()
	if def.Function.Name != "ask_user" {
		t.Fatal("tool must be named ask_user")
	}
}

func TestAskUserResultStrings(t *testing.T) {
	req := PromptRequest{Options: []string{"a", "b"}, AllowOther: true}
	if got := FormatAskAnswer(req, PromptAnswer{Index: 1, Choice: "b"}); got != "ANSWERED: b" {
		t.Fatal(got)
	}
	if got := FormatAskAnswer(req, PromptAnswer{Index: -1, Other: "c"}); got != "ANSWERED (custom): c" {
		t.Fatal(got)
	}
	if got := FormatAskAnswer(req, PromptAnswer{Index: -1, Cancelled: true}); got != AskDismissed {
		t.Fatal(got)
	}
	auth := req
	auth.Kind = PromptAuthorization
	if got := FormatAskAnswer(auth, PromptAnswer{Index: -1, Cancelled: true}); !strings.HasPrefix(got, "DISMISSED") || !strings.Contains(got, "declined") {
		t.Fatal(got)
	}
	multi := req
	multi.MultiSelect = true
	got := FormatAskAnswer(multi, PromptAnswer{Index: 0, Choices: []string{"a", "b"}, Other: "z"})
	if got != "ANSWERED (multiple):\n- a\n- b\n- (custom) z" {
		t.Fatalf("multi = %q", got)
	}
}

func TestAskUserHeadlessAndCancel(t *testing.T) {
	raw := json.RawMessage(`{"question":"q","options":["a","b"]}`)
	res, err := askUserTool{}.Execute(context.Background(), raw, ExecEnv{})
	if err != nil || res.Content != AskUnavailable {
		t.Fatalf("headless = %q %v", res.Content, err)
	}
	res, _ = askUserTool{}.Execute(context.Background(), json.RawMessage(`{"question":"q","options":["a","b"],"default":"b"}`), ExecEnv{})
	if res.Content != "UNAVAILABLE: no interactive user; using the default option: b" {
		t.Fatalf("headless default = %q", res.Content)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, err = askUserTool{}.Execute(ctx, raw, ExecEnv{Prompter: ctxPrompter{}})
	if err != nil || res.Content != AskCancelled {
		t.Fatalf("cancelled = %q %v", res.Content, err)
	}
	sctx, scancel := context.WithCancelCause(context.Background())
	scancel(ErrSteered)
	res, err = askUserTool{}.Execute(sctx, raw, ExecEnv{Prompter: ctxPrompter{}})
	if err != nil || res.Content != AskInterrupted {
		t.Fatalf("steered = %q %v", res.Content, err)
	}
}

// ctxPrompter returns as soon as ctx is done.
type ctxPrompter struct{}

func (ctxPrompter) Ask(ctx context.Context, _ PromptRequest) (PromptAnswer, error) {
	<-ctx.Done()
	return PromptAnswer{Index: -1, Cancelled: true}, ctx.Err()
}
func (ctxPrompter) AskSecret(context.Context, string, string) ([]byte, error) { return nil, nil }
func (ctxPrompter) Record(string)                                             {}
