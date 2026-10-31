package tui

import (
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/tjcoder-labs/cli/internal/tools"
	"unicode/utf8"
)

var updateGolden = flag.Bool("update-golden", false, "rewrite testdata golden files")

var tagRE = regexp.MustCompile(`\[[^\[\]]*\]`)

// plainRows strips tview style tags (render.Markup escapes literal
// brackets as "[]" pairs, which the regexp also removes harmlessly for
// these fixtures).
func plainRows(card string) []string {
	return strings.Split(tagRE.ReplaceAllString(card, ""), "\n")
}

func sudoRequest() tools.PromptRequest {
	return tools.PromptRequest{
		Kind: tools.PromptAuthorization, Title: "sudo",
		Question: "The agent wants to run a command with sudo.",
		Detail:   "apt-get install -y ripgrep",
		Options:  []string{"Allow once", "Allow sudo for this session", "Deny"},
	}
}

func askRequest() tools.PromptRequest {
	return tools.PromptRequest{
		Kind: tools.PromptQuestion, Title: "Deploy target",
		Question:     "Which environment should I deploy to?",
		Options:      []string{"staging", "production"},
		Descriptions: []string{"", "live traffic"},
		AllowOther:   true, Default: 1,
	}
}

// TestCardSudoAndAskShareRenderer is the golden guard that sudo and
// ask_user use one card: same frame, width, accent and fill, live and
// answered.
func TestCardSudoAndAskShareRenderer(t *testing.T) {
	p := darkPalette()
	const width = 72
	cards := map[string]string{
		"sudo_live":     newChoicePrompt(sudoRequest()).render(p, width),
		"ask_live":      newChoicePrompt(askRequest()).render(p, width),
		"sudo_answered": renderCard(p.Theme, answeredContent(p.Theme, sudoRequest(), tools.PromptAnswer{Index: 0, Choice: "Allow once"}, outcomeAnswered), width, ""),
		"ask_answered":  renderCard(p.Theme, answeredContent(p.Theme, askRequest(), tools.PromptAnswer{Index: -1, Other: "staging first"}, outcomeAnswered), width, ""),
		"ask_dismissed": renderCard(p.Theme, answeredContent(p.Theme, askRequest(), tools.PromptAnswer{Index: -1, Cancelled: true}, outcomeDismissed), width, ""),
		"sudo_denied":   renderCard(p.Theme, answeredContent(p.Theme, sudoRequest(), tools.PromptAnswer{Index: -1, Cancelled: true}, outcomeDismissed), width, ""),
	}
	accent := "[" + p.Theme.Primary
	for name, card := range cards {
		rows := plainRows(card)
		for i, row := range rows {
			if w := utf8.RuneCountInString(row); w != width {
				t.Errorf("%s row %d width %d, want %d: %q", name, i, w, width, row)
			}
			if !strings.HasPrefix(row, cardAccent) {
				t.Errorf("%s row %d lacks the accent: %q", name, i, row)
			}
		}
		for i, raw := range strings.Split(card, "\n") {
			if !strings.Contains(raw, ":"+p.Theme.BgInput) || !strings.Contains(raw, accent) {
				t.Errorf("%s row %d not on the shared accent/BgInput fill: %q", name, i, raw)
			}
		}
		if strings.TrimSpace(strings.TrimPrefix(rows[0], cardAccent)) != "" || strings.TrimSpace(strings.TrimPrefix(rows[len(rows)-1], cardAccent)) != "" {
			t.Errorf("%s must open and close with a blank padded row", name)
		}
	}

	var golden strings.Builder
	for _, name := range []string{"sudo_live", "ask_live", "sudo_answered", "ask_answered", "ask_dismissed", "sudo_denied"} {
		golden.WriteString("== " + name + "\n")
		for _, r := range plainRows(cards[name]) {
			golden.WriteString(strings.TrimRight(r, " ") + "\n")
		}
	}
	path := filepath.Join("testdata", "prompt_cards.golden")
	if *updateGolden {
		if err := os.WriteFile(path, []byte(golden.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (run with -update-golden): %v", err)
	}
	if golden.String() != string(want) {
		t.Fatalf("cards differ from %s:\n%s", path, golden.String())
	}
}

func TestCardWrapsLongQuestionWithinWidth(t *testing.T) {
	p := darkPalette()
	req := askRequest()
	req.Question = strings.Repeat("very long question text ", 12)
	for _, r := range plainRows(newChoicePrompt(req).render(p, 50)) {
		if w := utf8.RuneCountInString(r); w != 50 {
			t.Fatalf("row width %d: %q", w, r)
		}
	}
}

func TestPromptStateDefaultMultiAndFreeText(t *testing.T) {
	s := newChoicePrompt(tools.PromptRequest{Options: []string{"a", "b", "c"}, Default: 3})
	if done, ans := s.handleKey(key(tcell.KeyEnter)); !done || ans.Choice != "c" {
		t.Fatalf("default not preselected: %+v", ans)
	}

	m := newChoicePrompt(tools.PromptRequest{Options: []string{"a", "b", "c"}, AllowOther: true, MultiSelect: true})
	if done, _ := m.handleKey(ch('1')); done {
		t.Fatal("a digit toggles in multi-select")
	}
	m.handleKey(ch('3'))
	m.handleKey(ch('3'))
	m.handleKey(ch('2'))
	if !strings.Contains(strings.Join(plainRows(m.render(darkPalette(), 60)), "\n"), "● a") {
		t.Fatal("ticked option should show ●")
	}
	m.handleKey(ch('4')) // Other row opens the text line
	for _, r := range "zz" {
		m.handleKey(ch(r))
	}
	done, ans := m.handleKey(key(tcell.KeyEnter))
	if !done || strings.Join(ans.Choices, ",") != "a,b" || ans.Other != "zz" || ans.Text() != "a, b, zz" {
		t.Fatalf("multi = %+v", ans)
	}
	m = newChoicePrompt(tools.PromptRequest{Options: []string{"a", "b"}, MultiSelect: true})
	m.handleKey(key(tcell.KeyDown))
	if done, ans := m.handleKey(key(tcell.KeyEnter)); !done || strings.Join(ans.Choices, ",") != "b" {
		t.Fatalf("Enter with nothing ticked takes the highlighted option: %+v", ans)
	}

	f := newChoicePrompt(tools.PromptRequest{AllowOther: true, OtherLabel: "Type your answer…"})
	if !f.editing {
		t.Fatal("free-text question starts in the text line")
	}
	for _, r := range "blue" {
		f.handleKey(ch(r))
	}
	f.handleKey(key(tcell.KeyCtrlU))
	for _, r := range "red" {
		f.handleKey(ch(r))
	}
	if done, ans := f.handleKey(key(tcell.KeyEnter)); !done || ans.Other != "red" {
		t.Fatalf("free text = %+v", ans)
	}
	f = newChoicePrompt(tools.PromptRequest{AllowOther: true})
	if done, ans := f.handleKey(key(tcell.KeyEscape)); !done || !ans.Cancelled {
		t.Fatal("Esc dismisses a free-text question")
	}
}
