package tui

import (
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rivo/tview"

	"github.com/tjcoder-labs/cli/internal/workspace"
)

func newWorkspaceTestApp(t *testing.T) *App {
	t.Helper()
	app := newTestApp(t)
	app.transcript = tview.NewTextView()
	app.input = tview.NewInputField()
	app.reasoning = tview.NewTextView()
	app.markUIGoroutine()
	app.initWorkspaces(app.workspaceRoot)
	cfg := filepath.Join(t.TempDir(), "ssh_config")
	os.WriteFile(cfg, []byte("Host alpha\n  HostName 10.0.0.1\n  User me\nHost beta prod-db\n  HostName db.example\nHost *\n  ServerAliveInterval 30\n"), 0o600)
	app.sshConfigOverride = cfg
	return app
}

func wsLabels(app *App) []string {
	var out []string
	for _, it := range app.wsArgs {
		out = append(out, it.label)
	}
	return out
}

func TestWorkspaceArgHints(t *testing.T) {
	app := newWorkspaceTestApp(t)
	root := app.workspaceRoot
	for _, d := range []string{"src", "docs", ".hidden", "src/inner"} {
		os.MkdirAll(filepath.Join(root, d), 0o755)
	}
	os.WriteFile(filepath.Join(root, "file.txt"), []byte("x"), 0o644)

	app.updateInputHint("/add ")
	if app.hintMode != "wsargs" || strings.Join(wsLabels(app), "|") != "folder path|ssh <host>" {
		t.Fatalf("/add tip: mode %q items %v", app.hintMode, wsLabels(app))
	}
	if txt := stripTags(app.renderWorkspaceArgPalette()); !strings.Contains(txt, "folder path") || !strings.Contains(txt, "ssh <host>") {
		t.Fatalf("render: %q", txt)
	}

	app.updateInputHint("/add ssh ")
	if got := strings.Join(wsLabels(app), "|"); got != "alpha|beta|prod-db" {
		t.Fatalf("ssh hosts: %q", got)
	}
	if app.wsArgs[0].desc != "me@10.0.0.1" {
		t.Fatalf("host desc %q", app.wsArgs[0].desc)
	}
	app.updateInputHint("/add ssh pr")
	if got := strings.Join(wsLabels(app), "|"); got != "prod-db" {
		t.Fatalf("filtered hosts: %q", got)
	}
	app.completeWorkspaceArg()
	if app.input.GetText() != "/add ssh prod-db" {
		t.Fatalf("tab completion: %q", app.input.GetText())
	}
	app.updateInputHint("/add ssh zz")
	if len(app.wsArgs) != 0 || !strings.Contains(stripTags(app.renderWorkspaceArgPalette()), "type a host") {
		t.Fatalf("no-match host tip: %v", wsLabels(app))
	}

	app.updateInputHint("/add ./")
	if got := strings.Join(wsLabels(app), "|"); got != "docs/|src/" {
		t.Fatalf("local dirs: %q", got)
	}
	app.updateInputHint("/add ./s")
	app.completeWorkspaceArg()
	if app.input.GetText() != "/add ./src/" {
		t.Fatalf("local completion: %q", app.input.GetText())
	}
	app.updateInputHint(app.input.GetText())
	if got := strings.Join(wsLabels(app), "|"); got != "inner/" {
		t.Fatalf("drill-down: %q", got)
	}
	app.updateInputHint("/add ./.h")
	if got := strings.Join(wsLabels(app), "|"); got != ".hidden/" {
		t.Fatalf("hidden with dot prefix: %q", got)
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	os.MkdirAll(filepath.Join(home, "projects"), 0o755)
	app.updateInputHint("/add ~/")
	if got := strings.Join(wsLabels(app), "|"); got != "projects/" || app.wsArgs[0].insert != "~/projects/" {
		t.Fatalf("home dirs: %q %+v", got, app.wsArgs)
	}

	// Remote completion only reads the cache (filled asynchronously
	// over a live master); nothing is listed without one.
	app.updateInputHint("/add ssh alpha:")
	if len(app.wsArgs) != 0 {
		t.Fatalf("remote without connection should be empty: %v", wsLabels(app))
	}
	app.remoteDirs["alpha\x00"+path.Clean(".")] = remoteDirEntry{dirs: []string{"code", "notes", ".cache"}, at: time.Now()}
	app.updateInputHint("/add ssh alpha:c")
	if got := strings.Join(wsLabels(app), "|"); got != "code/" || app.wsArgs[0].insert != "ssh alpha:code/" {
		t.Fatalf("remote dirs: %q %+v", got, app.wsArgs)
	}

	// /use and /dirs rm complete names; the primary cannot be removed.
	other := t.TempDir()
	app.handleAddCommand(other)
	name := filepath.Base(other)
	app.updateInputHint("/use ")
	if got := wsLabels(app); len(got) != 2 {
		t.Fatalf("/use names: %v", got)
	}
	app.updateInputHint("/dirs ")
	if got := strings.Join(wsLabels(app), "|"); got != "rm <name>" {
		t.Fatalf("/dirs tip: %q", got)
	}
	app.updateInputHint("/dirs rm ")
	if got := strings.Join(wsLabels(app), "|"); got != name {
		t.Fatalf("/dirs rm names: %q", got)
	}

	// Other commands keep the normal palette.
	app.updateInputHint("/model x")
	if app.hintMode != "command" {
		t.Fatalf("/model args mode %q", app.hintMode)
	}
	app.updateInputHint("/add")
	if app.hintMode != "command" {
		t.Fatalf("/add without space should stay in command mode, got %q", app.hintMode)
	}
}

func TestWorkspaceCommands(t *testing.T) {
	app := newWorkspaceTestApp(t)
	other := t.TempDir()
	os.WriteFile(filepath.Join(other, "notes.md"), []byte("hello from other"), 0o644)
	name := filepath.Base(other)

	if app.workspaceStatus() != "" {
		t.Fatal("status segment should be empty with only the primary")
	}
	app.handleSlashCommand("/add " + other)
	if _, ok := app.workspaces.Get(name); !ok {
		t.Fatalf("not added: %v", app.workspaces.Names())
	}
	app.handleSlashCommand("/add " + other)
	if !strings.Contains(app.transcript.GetText(true), "Already a workspace") {
		t.Fatal("duplicate not reported")
	}
	app.handleSlashCommand("/add " + filepath.Join(other, "notes.md"))
	if !strings.Contains(app.transcript.GetText(true), "is not a directory") {
		t.Fatal("file accepted as workspace")
	}
	if !strings.Contains(stripTags(app.renderMetaBar()), "⌂ ") {
		t.Fatalf("status bar: %q", stripTags(app.renderMetaBar()))
	}

	app.handleSlashCommand("/use " + name)
	if app.workspaces.ActiveName() != name || !strings.Contains(app.workspaceStatus(), name) {
		t.Fatalf("active %q status %q", app.workspaces.ActiveName(), app.workspaceStatus())
	}
	app.handleSlashCommand("/use nope")
	if app.workspaces.ActiveName() != name {
		t.Fatal("unknown /use changed the active workspace")
	}
	app.handleSlashCommand("/workspaces")
	if !strings.Contains(app.transcript.GetText(true), "▸ "+name) {
		t.Fatalf("listing: %q", app.transcript.GetText(true))
	}

	// Persisted in the session and restored by a fresh app.
	if len(app.sessionState.Workspaces) != 1 || app.sessionState.ActiveWorkspace != name {
		t.Fatalf("session: %+v %q", app.sessionState.Workspaces, app.sessionState.ActiveWorkspace)
	}
	fresh := newWorkspaceTestApp(t)
	fresh.restoreWorkspaces(append(app.sessionState.Workspaces, workspace.Spec{Kind: workspace.KindLocal, Path: filepath.Join(other, "gone")}), name)
	if fresh.workspaces.ActiveName() != name || len(fresh.workspaces.All()) != 2 {
		t.Fatalf("restore: %v active %q", fresh.workspaces.Names(), fresh.workspaces.ActiveName())
	}

	// The @ picker lists the added folder's files as name:path, and
	// attachments resolve that form, confined to that root.
	idx := strings.Join(app.workspaceFileIndex(), "\n")
	if !strings.Contains(idx, name+":notes.md") {
		t.Fatalf("index: %q", idx)
	}
	outside := filepath.Join(filepath.Dir(other), "secret.txt")
	os.WriteFile(outside, []byte("nope"), 0o644)
	atts := loadAttachmentsMulti(app.workspaceRoot, app.localWorkspaceRoots(),
		[]string{name + ":notes.md", name + ":../secret.txt"}, attachMaxFileBytes, attachMaxTotalBytes)
	if len(atts) != 1 || atts[0].Path != name+":notes.md" || !strings.Contains(atts[0].Content, "hello from other") {
		t.Fatalf("attachments: %+v", atts)
	}

	app.handleSlashCommand("/dirs rm " + app.workspaces.Primary().Name)
	if len(app.workspaces.All()) != 2 {
		t.Fatal("primary removed")
	}
	app.handleSlashCommand("/dirs rm " + name)
	if len(app.workspaces.All()) != 1 || app.workspaces.ActiveName() != app.workspaces.Primary().Name {
		t.Fatalf("after rm: %v active %q", app.workspaces.Names(), app.workspaces.ActiveName())
	}
	if len(app.sessionState.Workspaces) != 0 {
		t.Fatalf("session not updated: %+v", app.sessionState.Workspaces)
	}

	app.handleSlashCommand("/add ssh -oProxyCommand=evil")
	if !strings.Contains(app.transcript.GetText(true), "invalid ssh host") || len(app.workspaces.All()) != 1 {
		t.Fatal("option-like ssh host accepted")
	}
}

func TestCommandArgsKeepsSpaces(t *testing.T) {
	if got := commandArgs("/add  ~/My Projects/app "); got != "~/My Projects/app" {
		t.Fatalf("%q", got)
	}
	if got := unquoteArg(`"/tmp/a b"`); got != "/tmp/a b" {
		t.Fatalf("%q", got)
	}
}
