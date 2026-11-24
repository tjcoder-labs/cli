package tui

import (
	"os"
	"strings"
	"testing"

	"github.com/rivo/tview"
)

func TestReinvokeHandoffConsumed(t *testing.T) {
	t.Setenv(reinvokePromptEnv, "continue after restart")
	a := &App{input: tview.NewInputField()}
	a.submitAfterReinvoke()
	if got := os.Getenv(reinvokePromptEnv); got != "" {
		t.Fatalf("one-shot flag remained set: %q", got)
	}
	if !strings.Contains(reinvokeContinuePrompt, "Do not run /reinvoke again") {
		t.Fatal("handoff must prevent an automatic rebuild loop")
	}
}

func TestReinvokeNoHandoffIsNoop(t *testing.T) {
	t.Setenv(reinvokePromptEnv, "")
	a := &App{}
	a.submitAfterReinvoke()
}
