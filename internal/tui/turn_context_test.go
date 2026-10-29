package tui

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rivo/tview"
	"github.com/tjcoder-labs/cli/internal/client"
	"github.com/tjcoder-labs/cli/internal/session"
	"github.com/tjcoder-labs/cli/internal/tooling"
	"github.com/tjcoder-labs/cli/internal/tools"
)

func TestWordBudget(t *testing.T) {
	cases := []struct {
		cols, lines, want int
	}{
		{100, 30, 373},   // 100/6 × 28 × 0.8 = 373.3
		{80, 24, 234},    // 80/6 × 22 × 0.8 = 234.7
		{0, 0, 234},      // unknown size → 80×24 fallback
		{20, 5, 40},      // tiny pane clamps to the floor
		{300, 120, 1500}, // huge pane clamps to the ceiling
		{120, 2, 40},     // label rows only
	}
	for _, c := range cases {
		if got := WordBudget(c.cols, c.lines); got != c.want {
			t.Errorf("WordBudget(%d,%d) = %d, want %d", c.cols, c.lines, got, c.want)
		}
	}
	// Monotonic in both dimensions.
	if WordBudget(100, 40) <= WordBudget(100, 30) || WordBudget(140, 30) <= WordBudget(100, 30) {
		t.Error("budget should grow with pane size")
	}
}

func TestFormatTurnContext(t *testing.T) {
	got := formatTurnContext(turnContextInfo{
		User: "TJ Coder", Email: "tj@tjcoder.com",
		Layout: "split (transcript + right column)", RightPanel: "canvas", CanvasFile: "main.go:L10-40",
		FocusedPane: "input", Cwd: "/work/cli",
		Cols: 100, Lines: 30, TranscriptLines: 212,
		Attachments: []fileAttachment{
			{Path: "main.go", Size: 2048, Lines: 80, Mime: "text/x-go", Lang: "go"},
			{Path: "logo.png", Size: 4096, Mime: "image/png", Binary: true},
		},
	})
	for _, want := range []string{
		"<tui_context>\n",
		"user: TJ Coder\n",
		"email: tj@tjcoder.com\n",
		"right_panel: canvas\n",
		"canvas_file: main.go:L10-40\n",
		"focused_pane: input\n",
		"cwd: /work/cli\n",
		"transcript_width_cols: 100\n",
		"transcript_visible_lines: 30\n",
		"transcript_line_count: 212\n",
		"respond in fewer than 373 words",
		"at most 100 columns",
		"attachment: path=main.go size=2.0KB lines=80 mime=text/x-go lang=go\n",
		"attachment: path=logo.png size=4.0KB mime=image/png binary=true",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("block missing %q\n%s", want, got)
		}
	}
	if !strings.HasSuffix(got, "</tui_context>") {
		t.Errorf("block not closed:\n%s", got)
	}
	// Email (and any empty field) is simply omitted.
	noEmail := formatTurnContext(turnContextInfo{User: "tj", Cols: 100, Lines: 30})
	if strings.Contains(noEmail, "email:") || strings.Contains(noEmail, "canvas_file:") {
		t.Errorf("empty fields should be omitted:\n%s", noEmail)
	}
}

func TestBuildTurnContextToggles(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	app := &App{palette: darkPalette(), workspaceRoot: "/tmp/ws", activePanel: "tasks", fullscreen: true}
	t.Setenv("CODER_TURN_CONTEXT", "0")
	if got := app.buildTurnContext(nil); got != "" {
		t.Fatalf("CODER_TURN_CONTEXT=0 should disable the block, got %q", got)
	}
	t.Setenv("CODER_TURN_CONTEXT", "")
	got := app.buildTurnContext(nil)
	for _, want := range []string{"cwd: /tmp/ws", "right_panel: tasks", "layout: fullscreen", "respond in fewer than 234 words"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	if strings.Contains(got, "email:") {
		t.Errorf("no identity on disk, email must be omitted:\n%s", got)
	}
}

// recordingProvider captures requests and replies "ok".
type recordingProvider struct{ requests []client.ChatRequest }

func (p *recordingProvider) Chat(_ context.Context, req client.ChatRequest, _ func(client.StreamEvent) error) (client.Message, error) {
	p.requests = append(p.requests, req)
	return client.Message{Role: "assistant", Content: "ok"}, nil
}
func (p *recordingProvider) ListModels(context.Context) ([]client.ModelInfo, error) { return nil, nil }
func (p *recordingProvider) ContextWindow(context.Context, string) (int, error)     { return 8192, nil }
func (p *recordingProvider) BaseURL() string                                        { return "http://localhost:11434" }

// End to end: the model sees the <tui_context> block on the latest user
// message, but the history the TUI keeps — and session.json on disk —
// holds only the user's text plus attachments.
func TestSessionJSONNeverContainsTurnContext(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "main.go", []byte("package main\n"))
	app := &App{
		workspaceRoot: root,
		transcript:    tview.NewTextView(),
		reasoning:     tview.NewTextView(),
		activity:      tview.NewTextView(),
		palette:       darkPalette(),
		currentAgent:  mockAgent(),
		currentModel:  "test-model",
		enabledTools:  map[string]bool{},
		history:       []client.Message{{Role: "user", Content: "earlier"}, {Role: "assistant", Content: "earlier reply"}},
		sessionState:  session.State{},
	}
	prompt := "explain @main.go"
	atts := loadAttachments(root, mentionedPaths(prompt), attachMaxFileBytes, attachMaxTotalBytes)
	modelPrompt := prompt + "\n\n" + formatAttachmentsBlock(atts)
	block := formatTurnContext(turnContextInfo{User: "tj", Email: "tj@tjcoder.com", Cols: 100, Lines: 30, Attachments: atts})

	provider := &recordingProvider{}
	runner := &tooling.Runner{Provider: provider, Registry: tools.NewRegistry(provider), WorkspaceRoot: root, MaxSteps: 1}
	next, err := runner.Run(tooling.WithTurnContext(context.Background(), block), app.history, modelPrompt, app.currentAgent, "test-model", nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	sent := provider.requests[0].Messages
	if last := sent[len(sent)-1]; last.Role != "user" || !strings.HasSuffix(last.Content, block) || !strings.Contains(last.Content, "<attached_files>") {
		t.Fatalf("latest user message on the request = %q", last.Content)
	}
	app.history = next
	if err := app.saveSession(); err != nil {
		t.Fatalf("saveSession: %v", err)
	}
	var raw []byte
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".json") {
			b, _ := os.ReadFile(p)
			raw = append(raw, b...)
		}
		return nil
	})
	if len(raw) == 0 {
		t.Fatal("no session json written")
	}
	if strings.Contains(string(raw), "tui_context") || strings.Contains(string(raw), "tj@tjcoder.com") {
		t.Fatal("session json contains turn metadata")
	}
	if !strings.Contains(string(raw), "attached_files") {
		t.Fatal("attachments should persist with the user message")
	}
}

func TestFilterFileMatchesRanking(t *testing.T) {
	files := []string{
		"README.md",
		"cmd/coder/main.go",
		"internal/tui/app.go",
		"internal/tui/app_test.go",
		"internal/tui/attachments.go",
		"docs/mapping.md",
	}
	got := filterFileMatches(files, "app", 10)
	want := []string{"internal/tui/app.go", "internal/tui/app_test.go", "docs/mapping.md"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("filter(app) = %v, want %v", got, want)
	}
	// Fuzzy subsequence.
	if got := filterFileMatches(files, "atchmt", 10); len(got) != 1 || got[0] != "internal/tui/attachments.go" {
		t.Fatalf("fuzzy match = %v", got)
	}
	// Case-insensitive, capped.
	if got := filterFileMatches(files, "READ", 10); len(got) != 1 || got[0] != "README.md" {
		t.Fatalf("case-insensitive = %v", got)
	}
	if got := filterFileMatches(files, "", 3); len(got) != 3 {
		t.Fatalf("empty query should list up to the cap, got %v", got)
	}
}

func TestSkipIndexedPath(t *testing.T) {
	for p, want := range map[string]bool{
		"main.go":                 false,
		"node_modules/x/index.js": true,
		".git/config":             true,
		"web/node_modules/a.js":   true,
		"assets/logo.png":         true,
		"bin/run.sh":              false, // tracked scripts stay visible in git mode
		"docs/guide.md":           false,
		"pkg/thing.exe":           true,
	} {
		if got := skipIndexedPath(p); got != want {
			t.Errorf("skipIndexedPath(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestGitignoreMatcher(t *testing.T) {
	m := parseGitignore("# comment\n\n*.log\n/build/\ntmp/\n!keep.log\ndocs/**/*.pdf\nsecret.txt\n")
	cases := []struct {
		path  string
		isDir bool
		want  bool
	}{
		{"app.log", false, true},
		{"sub/app.log", false, true},
		{"keep.log", false, false},
		{"build", true, true},
		{"src/build", true, false}, // anchored
		{"tmp", true, true},
		{"a/tmp", true, true},
		{"tmp", false, false}, // dir-only pattern
		{"docs/a/b/c.pdf", false, true},
		{"secret.txt", false, true},
		{"main.go", false, false},
	}
	for _, c := range cases {
		if got := m.Ignored(c.path, c.isDir); got != c.want {
			t.Errorf("Ignored(%q,%v) = %v, want %v", c.path, c.isDir, got, c.want)
		}
	}
}

func writeFile(t *testing.T, root, rel string, data []byte) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestListWorkspaceFilesRespectsIgnores(t *testing.T) {
	for _, useGit := range []bool{false, true} {
		root := t.TempDir()
		writeFile(t, root, ".gitignore", []byte("*.log\nout/\n"))
		writeFile(t, root, "main.go", []byte("package main\n"))
		writeFile(t, root, "pkg/util.go", []byte("package pkg\n"))
		writeFile(t, root, "debug.log", []byte("x"))
		writeFile(t, root, "out/gen.go", []byte("x"))
		writeFile(t, root, "node_modules/lib/index.js", []byte("x"))
		writeFile(t, root, "img/logo.png", []byte("\x89PNG"))
		if useGit {
			if _, err := exec.LookPath("git"); err != nil {
				continue
			}
			if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
				t.Fatalf("git init: %v %s", err, out)
			}
		}
		got := listWorkspaceFiles(root)
		want := []string{".gitignore", "main.go", "pkg/util.go"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("git=%v: listWorkspaceFiles = %v, want %v", useGit, got, want)
		}
	}
}

func TestMentionTokens(t *testing.T) {
	if q, ok := activeMentionToken("look at @int"); !ok || q != "int" {
		t.Fatalf("active token = %q %v", q, ok)
	}
	if _, ok := activeMentionToken("look at @int "); ok {
		t.Fatal("trailing space should end the mention")
	}
	if _, ok := activeMentionToken("mail tj@tjcoder.com"); ok {
		t.Fatal("an email address is not a mention")
	}
	if q, ok := activeMentionToken("@"); !ok || q != "" {
		t.Fatal("bare @ should open the picker with an empty query")
	}
	if got := replaceMentionToken("look at @int", "internal/tui/app.go"); got != "look at @internal/tui/app.go " {
		t.Fatalf("replace = %q", got)
	}
	got := mentionedPaths("compare @a.go and @b/c.go, plus tj@x.com and @a.go.")
	if !reflect.DeepEqual(got, []string{"a.go", "b/c.go"}) {
		t.Fatalf("mentionedPaths = %v", got)
	}
}

func TestLoadAttachmentsCaps(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "small.go", []byte("package x\n\nfunc A() {}\n"))
	writeFile(t, root, "big.txt", []byte(strings.Repeat("0123456789\n", 30000))) // 330,000 bytes
	writeFile(t, root, "blob.dat", []byte{0x00, 0x01, 0x02, 'a'})
	writeFile(t, root, "img.png", []byte("not really a png"))

	atts := loadAttachments(root, []string{"small.go", "big.txt", "blob.dat", "img.png", "missing.go", "../escape.go", "small.go"}, attachMaxFileBytes, attachMaxTotalBytes)
	if len(atts) != 4 {
		t.Fatalf("expected 4 attachments (missing/escaping/duplicate dropped), got %d: %+v", len(atts), atts)
	}
	small, big, blob, img := atts[0], atts[1], atts[2], atts[3]
	if small.Lines != 3 || small.Lang != "go" || small.Truncated || small.Content != "package x\n\nfunc A() {}\n" {
		t.Errorf("small = %+v", small)
	}
	if !big.Truncated || len(big.Content) != attachMaxFileBytes || big.Lines != 30000 || big.Size != 330000 {
		t.Errorf("big: truncated=%v len=%d lines=%d size=%d", big.Truncated, len(big.Content), big.Lines, big.Size)
	}
	if !blob.Binary || blob.Content != "" || !img.Binary {
		t.Errorf("binary detection: blob=%+v img=%+v", blob, img)
	}

	block := formatAttachmentsBlock(atts)
	if !strings.Contains(block, "[truncated: showing first 204800 of 330000 bytes]") {
		t.Error("truncation note missing")
	}
	if !strings.Contains(block, `<file path="blob.dat" size="4"`) || !strings.Contains(block, "[binary file: contents not attached]") {
		t.Error("binary metadata missing")
	}

	// Total cap: the second large file is cut to what remains, the third
	// is omitted (metadata only).
	writeFile(t, root, "big2.txt", []byte(strings.Repeat("a", 150000)))
	writeFile(t, root, "big3.txt", []byte(strings.Repeat("b", 150000)))
	atts = loadAttachments(root, []string{"big.txt", "big2.txt", "big3.txt"}, 200_000, 300_000)
	if len(atts[0].Content) != 200_000 || len(atts[1].Content) != 100_000 || !atts[1].Truncated || !atts[2].Omitted || atts[2].Content != "" {
		t.Fatalf("total cap: %d/%v %d/%v omitted=%v", len(atts[0].Content), atts[0].Truncated, len(atts[1].Content), atts[1].Truncated, atts[2].Omitted)
	}
	total := 0
	for _, a := range atts {
		total += len(a.Content)
	}
	if total > 300_000 {
		t.Fatalf("total %d exceeds cap", total)
	}
}

func TestTruncationKeepsUTF8Valid(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "u.txt", []byte(strings.Repeat("é", 100))) // 2 bytes each
	atts := loadAttachments(root, []string{"u.txt"}, 51, 1000)
	if len(atts) != 1 || len(atts[0].Content) != 50 || !atts[0].Truncated {
		t.Fatalf("utf8 cut: %+v", atts)
	}
}

func TestSplitAttachmentsRoundTrip(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a.go", []byte("package a\n"))
	writeFile(t, root, "b.bin", []byte{0, 1, 2})
	atts := loadAttachments(root, []string{"a.go", "b.bin"}, attachMaxFileBytes, attachMaxTotalBytes)
	content := "please review @a.go\n\n" + formatAttachmentsBlock(atts)
	text, chips := splitAttachments(content)
	if text != "please review @a.go" {
		t.Fatalf("text = %q", text)
	}
	want := []attachmentChip{{Path: "a.go", Lines: 1, Size: 10}, {Path: "b.bin", Size: 3, Binary: true}}
	if !reflect.DeepEqual(chips, want) {
		t.Fatalf("chips = %+v, want %+v", chips, want)
	}
	if text, chips := splitAttachments("no files here"); text != "no files here" || chips != nil {
		t.Fatal("plain content should pass through")
	}
}

func TestRenderUserTurnShowsChipsNotContents(t *testing.T) {
	app := &App{palette: darkPalette()}
	content := "see @a.go\n\n<attached_files>\n<file path=\"a.go\" size=\"10\" lines=\"1\" lang=\"go\">\nSECRET_BODY\n</file>\n</attached_files>"
	out := stripTags(app.renderUserTurn(content))
	if strings.Contains(out, "SECRET_BODY") || strings.Contains(out, "attached_files") {
		t.Fatalf("file contents leaked into transcript: %q", out)
	}
	if !strings.Contains(out, "📎 a.go (1 lines)") {
		t.Fatalf("chip missing: %q", out)
	}
}

func TestReferencePickerSelection(t *testing.T) {
	app := &App{palette: darkPalette()}
	app.fileIndex = []string{"internal/tui/app.go", "internal/tui/attachments.go", "main.go"}
	app.fileIndexAt = timeNowForTest()
	app.updateInputHint("explain @att")
	if app.hintMode != "reference" || len(app.refMatches) != 1 || app.refMatches[0] != "internal/tui/attachments.go" {
		t.Fatalf("picker state: mode=%q matches=%v", app.hintMode, app.refMatches)
	}
	if !strings.Contains(stripTags(app.renderReferencePalette()), "@internal/tui/attachments.go") {
		t.Fatal("palette should list the match")
	}
	app.updateInputHint("explain @zzzz")
	if app.hintMode != "reference" || len(app.refMatches) != 0 || !strings.Contains(stripTags(app.renderReferencePalette()), "no matching files") {
		t.Fatal("empty matches should still show the palette with a note")
	}
	// Dismissed token stays hidden until it changes.
	app.refDismissed = "zzzz"
	app.updateInputHint("explain @zzzz")
	if app.hintMode == "reference" {
		t.Fatal("dismissed token should not reopen the picker")
	}
	app.updateInputHint("explain @zzz")
	if app.hintMode != "reference" {
		t.Fatal("editing the token should reopen the picker")
	}
}

func timeNowForTest() time.Time { return time.Now() }
