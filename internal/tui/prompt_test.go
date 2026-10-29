package tui

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/tjcoder-labs/cli/internal/tools"
)

func key(k tcell.Key) *tcell.EventKey { return tcell.NewEventKey(k, 0, tcell.ModNone) }
func ch(r rune) *tcell.EventKey       { return tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone) }

func TestPromptStateNumberSelects(t *testing.T) {
	s := newChoicePrompt(tools.PromptRequest{Options: []string{"a", "b", "c"}})
	if done, _ := s.handleKey(ch('9')); done {
		t.Fatal("out-of-range digit must be ignored")
	}
	done, ans := s.handleKey(ch('2'))
	if !done || ans.Index != 1 || ans.Choice != "b" {
		t.Fatalf("got %v %+v", done, ans)
	}
}

func TestPromptStateArrowsWrapAndEnter(t *testing.T) {
	s := newChoicePrompt(tools.PromptRequest{Options: []string{"a", "b"}})
	s.handleKey(key(tcell.KeyUp)) // wraps to last
	if s.selected != 1 {
		t.Fatalf("selected = %d", s.selected)
	}
	s.handleKey(key(tcell.KeyDown))
	s.handleKey(key(tcell.KeyDown))
	done, ans := s.handleKey(key(tcell.KeyEnter))
	if !done || ans.Choice != "b" {
		t.Fatalf("got %+v", ans)
	}
}

func TestPromptStateEscCancels(t *testing.T) {
	s := newChoicePrompt(tools.PromptRequest{Kind: tools.PromptAuthorization, Options: []string{"Allow", "Deny"}})
	if !strings.Contains(s.render(darkPalette(), 80), "Esc to deny") {
		t.Fatal("authorization prompt should say Esc denies")
	}
	done, ans := s.handleKey(key(tcell.KeyEscape))
	if !done || !ans.Cancelled || ans.Index != -1 {
		t.Fatalf("got %+v", ans)
	}
}

func TestPromptStateOtherFreeText(t *testing.T) {
	s := newChoicePrompt(tools.PromptRequest{Options: []string{"a"}, AllowOther: true})
	if done, _ := s.handleKey(ch('2')); done || !s.editing {
		t.Fatal("Other row should open the text line")
	}
	if done, _ := s.handleKey(key(tcell.KeyEnter)); done {
		t.Fatal("empty other must not submit")
	}
	for _, r := range "hi x" {
		s.handleKey(ch(r))
	}
	s.handleKey(key(tcell.KeyBackspace2))
	// Digits are text while editing, not selections.
	s.handleKey(ch('1'))
	done, ans := s.handleKey(key(tcell.KeyEnter))
	if !done || ans.Other != "hi 1" || ans.Index != -1 || ans.Text() != "hi 1" {
		t.Fatalf("got %+v", ans)
	}
	// Esc while editing returns to the list instead of cancelling.
	s = newChoicePrompt(tools.PromptRequest{Options: []string{"a"}, AllowOther: true})
	s.handleKey(ch('2'))
	if done, _ := s.handleKey(key(tcell.KeyEscape)); done || s.editing {
		t.Fatal("Esc in other mode should go back")
	}
}

func TestPromptStateSecretMaskedAndZeroed(t *testing.T) {
	s := newSecretPrompt("sudo", "Password")
	for _, r := range "pä55" {
		s.handleKey(ch(r))
	}
	s.handleKey(key(tcell.KeyBackspace2))
	out := s.render(darkPalette(), 80)
	if strings.Contains(out, "pä5") || !strings.Contains(out, "•••") || strings.Contains(out, "••••") {
		t.Fatalf("secret must render as one bullet per rune: %q", out)
	}
	if string(s.buf) != "pä5" {
		t.Fatalf("buf = %q", s.buf)
	}
	held := s.buf
	done, ans := s.handleKey(key(tcell.KeyEscape))
	if !done || !ans.Cancelled || s.buf != nil {
		t.Fatal("esc must cancel and drop the buffer")
	}
	for _, b := range held[:cap(held)] {
		if b != 0 {
			t.Fatal("cancelled secret must be zeroed")
		}
	}
}
