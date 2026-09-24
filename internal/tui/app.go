package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"github.com/tjcoder-labs/cli/internal/agent"
	"github.com/tjcoder-labs/cli/internal/client"
	ctxpkg "github.com/tjcoder-labs/cli/internal/context"
	"github.com/tjcoder-labs/cli/internal/mcp"
	"github.com/tjcoder-labs/cli/internal/memories"
	"github.com/tjcoder-labs/cli/internal/session"
	"github.com/tjcoder-labs/cli/internal/tasks"
	"github.com/tjcoder-labs/cli/internal/tooling"
	"github.com/tjcoder-labs/cli/internal/tools"
	"github.com/tjcoder-labs/cli/internal/tracking"

	"unicode"
)

// Palette holds all color scheme values for the TUI (backgrounds, text, accents, hex tags).
// Both dark and light theme variants are available via darkPalette() and lightPalette().
type Palette struct {
	BgRoot      tcell.Color
	BgReasoning tcell.Color
	BgActivity  tcell.Color
	BgInput     tcell.Color
	BgModal     tcell.Color
	BgSelect    tcell.Color
	TextMain    tcell.Color
	TextDim     tcell.Color
	TextFaint   tcell.Color
	Purple      tcell.Color
	Lavender    tcell.Color
	Violet      tcell.Color
	Orchid      tcell.Color
	HexMain     string
	HexDim      string
	HexFaint    string
	HexRoot     string
	HexPurple   string
	HexLavender string
	HexViolet   string
	HexOrchid   string
}

// darkPalette returns the original dark theme (monochromatic with a.palette.Purple accents).
func darkPalette() Palette {
	return Palette{
		BgRoot:      tcell.NewRGBColor(0, 0, 0),
		BgReasoning: tcell.NewRGBColor(10, 7, 20),
		BgActivity:  tcell.NewRGBColor(7, 5, 18),
		BgInput:     tcell.NewRGBColor(20, 14, 36),
		BgModal:     tcell.NewRGBColor(32, 23, 58),
		BgSelect:    tcell.NewRGBColor(46, 32, 78),
		TextMain:    tcell.NewRGBColor(232, 226, 245),
		TextDim:     tcell.NewRGBColor(150, 140, 178),
		TextFaint:   tcell.NewRGBColor(86, 74, 112),
		Purple:      tcell.NewRGBColor(167, 124, 248),
		Lavender:    tcell.NewRGBColor(196, 165, 255),
		Violet:      tcell.NewRGBColor(124, 58, 237),
		Orchid:      tcell.NewRGBColor(199, 60, 220),
		HexMain:     "#E8E2F5",
		HexDim:      "#968CB2",
		HexFaint:    "#564A70",
		HexRoot:     "#000000",
		HexPurple:   "#A77CF8",
		HexLavender: "#C4A5FF",
		HexViolet:   "#7C3AED",
		HexOrchid:   "#C73CDC",
	}
}

// lightPalette returns a light theme with white backgrounds and saturated a.palette.Purple accents.
func lightPalette() Palette {
	return Palette{
		BgRoot:      tcell.NewRGBColor(255, 255, 255),
		BgReasoning: tcell.NewRGBColor(248, 248, 250),
		BgActivity:  tcell.NewRGBColor(245, 245, 248),
		BgInput:     tcell.NewRGBColor(250, 250, 255),
		BgModal:     tcell.NewRGBColor(240, 240, 248),
		BgSelect:    tcell.NewRGBColor(225, 215, 245),
		TextMain:    tcell.NewRGBColor(30, 25, 50),
		TextDim:     tcell.NewRGBColor(120, 100, 150),
		TextFaint:   tcell.NewRGBColor(170, 160, 190),
		Purple:      tcell.NewRGBColor(110, 65, 200),
		Lavender:    tcell.NewRGBColor(100, 50, 190),
		Violet:      tcell.NewRGBColor(90, 30, 180),
		Orchid:      tcell.NewRGBColor(180, 30, 170),
		HexMain:     "#1E1932",
		HexDim:      "#786496",
		HexFaint:    "#AAA0BE",
		HexRoot:     "#FFFFFF",
		HexPurple:   "#6E41C8",
		HexLavender: "#6432BE",
		HexViolet:   "#5A1EB4",
		HexOrchid:   "#B41EAA",
	}
}

var spinner = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// panelGutter is the width/height of the thin negative-space gutter used
// between every panel-level surface (main columns and rows), so spacing
// reads as consistent throughout.
const panelGutter = 1

// marginX is the standard horizontal content inset used by header, footer,
// and every panel's internal padding (SetBorderPadding left/right). The
// input row's outer margin uses the same value so it lines up with the
// panels above it.
// NOTE: kept as 3 to match the conversation panel's left padding.
const marginX = 3

const modelLoadTimeout = 12 * time.Second

// slashCommand describes a single "/" command for the inline command
// palette rendered on the input hint line. This is the single source of
// truth used both by the input change handler (to build live matches)
// and by Tab/Shift+Tab cycling.
type slashCommand struct {
	cmd  string
	desc string
}

// slashCommands is the ordered set of commands offered by the inline
// palette. Keep in sync with handleSlashCommand's switch.
var slashCommands = []slashCommand{
	{"agent", "Open agent selector"},
	{"agentinfo", "View current agent prompt"},
	{"tools", "Toggle tools"},
	{"model", "Choose model"},
	{"scroll", "Scroll transcript"},
	{"clear", "Clear session"},
	{"compact", "Summarize transcript to free context"},
	{"quit", "Quit application"},
	{"trigger", "Trigger an event"},
	{"reminder", "Create a reminder"},
	{"task", "Create a task"},
	{"tasks", "Open tasks panel"},
	{"memory", "Store a memory"},
	{"config", "Edit user config (JSON)"},
	{"environment", "Edit injected environment/context template"},
	{"activity", "Open activity panel"},
	{"canvas", "Open a file/range in the canvas panel"},
	{"about", "Open the about / welcome screen"},
	{"mcp", "Manage MCP integrations"},
}

type App struct {
	tv *tview.Application
	// running is true between App.Run() entering tv.Run() and
	// tv.Run() returning. It gates which goroutine is allowed to
	// touch tview primitives that the event loop owns (focus,
	// redraw, etc.). Reading it from any goroutine is safe; the
	// only mutations happen in Run() under the caller's stack.
	running bool

	provider client.Provider
	registry *tools.Registry
	runner   *tooling.Runner
	// steeringCh carries mid-turn user steering messages from the
	// TUI's submit() handler to Runner.Steering. It is buffered so
	// a quick double-Enter doesn't drop; submit() does a non-blocking
	// send and falls back to an activity-log warning if the buffer
	// is saturated (per the contract documented on Runner.Steering).
	steeringCh   chan string
	mcpClient    *mcp.Client
	sessionState session.State
	palette      Palette

	workspaceRoot             string
	productName               string
	author                    string
	appVersion                string
	agents                    []agent.Config
	currentAgent              agent.Config
	currentModel              string
	previousAgentDefaultModel string // tracks the last agent's default model so switching agents can swap models
	models                    []client.ModelInfo
	enabledTools              map[string]bool
	history                   []client.Message
	busy                      bool
	contextInfo               string
	refSet                    map[string]struct{}
	refOrder                  []string
	spinIdx                   int
	spinStop                  chan struct{}
	assistantState            string
	assistantStamp            string
	inputPlaceholder          string
	inputPlaceholderActive    bool

	header          *tview.TextView
	transcript      *tview.TextView
	transcriptTitle *tview.TextView
	transcriptPanel *tview.Flex
	reasoning       *tview.TextView
	reasoningTitle  *tview.TextView
	tasksTitle      *tview.TextView
	memoriesTitle   *tview.TextView
	reasoningPanel  *tview.Flex
	activity        *tview.TextView
	activityTitle   *tview.TextView
	activityPanel   *tview.Flex
	// tasksList is the interactive tview.List shown in the right
	// column when the user opens /tasks. The user navigates with
	// Up/Down and presses Enter to toggle a task between done and
	// its previous open status.
	tasksList  *tview.List
	tasksPanel *tview.Flex
	// memoriesList is the interactive tview.List shown in the right
	// column when the user opens /memories. The user navigates with
	// Up/Down and presses Delete to remove a memory.
	memoriesList  *tview.List
	memoriesPanel *tview.Flex
	testView   *tview.TextView
	testPanel  *tview.Flex
	// right is the right-column Flex (Cognition stacked over the
	// active body). Cached so buildRightColumn can read its current
	// size for adaptive height calculations.
	right *tview.Flex
	// leftPane, when non-nil, replaces the Conversation column in the
	// normal layout branch. It is how modal-style commands (/config,
	// /environment, /agent, /model, /tools, form editors, etc.) render
	// in-place in the left column instead of as a floating window, so
	// the right column (cognition/activity) stays visible alongside.
	leftPane   tview.Primitive
	input      *tview.InputField
	contextBar *tview.TextView
	footer     *tview.TextView
	statusBar  *tview.TextView
	pages      *tview.Pages

	// Inline input-hint palette state. The line beneath the input
	// (contextBar) alternates between session meta (cwd/context/refs)
	// and a horizontally-scrolling command/reference palette driven by
	// the first character of the input: "" = meta, "command" (leading
	// "/"), "reference" (leading "@", stubbed for now). hintMatches is
	// the current filtered command list and hintSelected the index of
	// the highlighted entry (used for Tab cycling and marquee windowing).
	hintMode     string
	hintMatches  []slashCommand
	hintSelected int

	// multiLineInput is true while the input field contains a newline
	// (e.g. a pasted multi-line block). It suppresses the command /
	// reference palette so the hint line can show a "multi-line" cue.
	multiLineInput bool

	// Global input history (decoupled from session.json). Stored
	// in ~/.config/tjcoder/coder-cli/input_history.json so it
	// persists across sessions and workspaces. The index tracks
	// the current position when the user navigates with Up/Down;
	// -1 means "at the bottom (new input)".
	inputHistory      []string
	inputHistoryIdx   int
	inputHistoryDraft string

	startupSplashVisible   bool
	reasoningSplashVisible bool
	cognitionActive        bool

	// activePanel is the name of the right-column body currently
	// showing in the unified right-hand pane ("activity", "tasks",
	// "articles", "canvas"). It is independent of fullscreen; fullscreen
	// controls visibility of the entire right column.
	activePanel string
	// canvasPath/canvasStart/canvasEnd hold the file and optional
	// 1-based line range currently rendered in the "canvas" panel.
	canvasPath  string
	canvasStart int
	canvasEnd   int
	// fullscreen hides the right column so the transcript can use the
	// full terminal width (useful for copying long output).
	fullscreen bool
	// aboutMode hides the Conversation label and reorients the
	// transcript body to the About splash: ASCII art on the left, the
	// product intro on the right. It is toggled by the /about slash
	// command and cleared automatically when the user starts typing.
	aboutMode bool
	// aboutBody is the right-hand intro TextView used while aboutMode
	// is active. We keep it pre-built and just toggle its visibility
	// in the layout rebuild so toggling is idempotent and cheap.
	aboutBody *tview.TextView
	// aboutAscii is the left-hand ASCII TextView used while aboutMode
	// is active.
	aboutAscii *tview.TextView
	// headerRight is a small TextView rendered to the right of the
	// header showing [Alt+I] About / [Alt+F] Fullscreen.
	headerRight *tview.TextView
	// firstRun is true until the TUI has had a chance to warm up the
	// cognition pane via a non-interactive recap. After the first
	// run (or first user turn), it flips to false so we don't recap
	// on every launch.
	firstRun bool
	rootFlex tview.Primitive
	inputRow tview.Primitive

	mu sync.Mutex

	// config holds the persisted user-tunable settings loaded from
	// .ergo-cli-go/config.json. Mutated by /config and read by the
	// agent runner for the tool-step cap.
	config AppConfig

	// scheduler is the in-session schedule worker (scheduler.go).
	// Started in Run(), stopped when Run() returns; it injects due
	// schedules into the live conversation via submitScheduled.
	scheduler *scheduleWorker

	// pendingTranscript holds persisted transcript entries that have
	// not yet been rendered into the transcript TextView. It is populated
	// by loadSession() and flushed after the first draw pass (via the
	// after-draw callback installed in Run()), because user-message
	// bubbles are sized from a.transcript.GetRect(), which is 0×0 until
	// the layout is actually drawn. Rendering them before that forces
	// every reloaded message into the fixed-width fallback.
	pendingTranscript []session.TranscriptEntry
}

func New(provider client.Provider, registry *tools.Registry, workspaceRoot, modelOverride, productName, author, appVersion string) *App {
	agents := agent.AllWithWorkspace(workspaceRoot)
	current := agents[0]
	model := current.DefaultModel
	if modelOverride != "" {
		model = modelOverride
		// Persist the explicit override to the cross-workspace TUI
		// prefs so the next launch (in any workspace) starts on the
		// model the user just selected. Without this, a TUI
		// invocation that bypasses the model modal would still need
		// to re-select the model manually on every run.
		_ = session.SetLastModel(true, modelOverride)
	} else if saved, ok := session.GetLastModel(true); ok && saved != "" {
		// No explicit override on this launch: fall through to the
		// user's remembered TUI choice. This is what makes the
		// model "stick" across re-invocations and across
		// workspace switches.
		model = saved
	}
	state, _, err := session.Load(workspaceRoot)
	if err != nil {
		state = session.State{}
	}
	cfg := loadConfigFromDisk(workspaceRoot)
	app := &App{
		tv:            tview.NewApplication(),
		provider:      provider,
		registry:      registry,
		runner:        &tooling.Runner{Provider: provider, Registry: registry, WorkspaceRoot: workspaceRoot, MaxSteps: cfg.ToolMax},
		steeringCh:    make(chan string, 16),
		mcpClient:     mcp.NewClient(),
		sessionState:  state,
		workspaceRoot: workspaceRoot,
		productName:   productName,
		author:        author,
		appVersion:    appVersion,
		agents:        agents,
		currentAgent:  current,
		currentModel:  model,
		enabledTools:  map[string]bool{},
		contextInfo:   "ctx: unavailable",
		refSet:        map[string]struct{}{},
		palette:       darkPalette(),
		config:        cfg,
	}
	// Set up sinks for tool integration (highlight_code, invoke_cli_command, etc.)
	app.runner.CLICommandSink = app
	trackReg := tracking.NewRegistry()
	trackReg.Register(tracking.NewTaskTracker())
	trackReg.Register(tracking.NewMemoryTracker())
	registry.RegisterTool(tools.ManageItemsBridge{Impl: tools.NewManageItems(trackReg, &app.sessionState)})
	app.resetEnabledTools(current.ToolNames)
	app.runner.SessionState = &app.sessionState
	app.runner.PersistSession = app.saveSession
	// Wire the steering channel last: it must exist on the App
	// before we hand a reference to the runner so a submit() that
	// races with the very first turn still has somewhere to send.
	app.runner.Steering = app.steeringCh
	app.scheduler = newScheduleWorker(app)
	app.build()
	app.loadSession()
	app.loadModelsAsync()
	app.loadMCPServers()
	// Load the global input history (decoupled from session.json).
	if h, err := session.LoadInputHistory(); err == nil {
		app.inputHistory = h
	}
	app.inputHistoryIdx = -1
	// Persist defaults on first run so the file exists for the
	// in-app editor and so a config write is always recoverable.
	if workspaceRoot != "" {
		if _, err := os.Stat(app.configPath()); os.IsNotExist(err) {
			_ = app.saveConfig(cfg)
		}
	}
	return app
}

// loadMCPServers hydrates the in-memory mcpClient from the persisted
// config.MCPServers map. Each enabled entry is registered; failures
// (network errors during tools/list discovery, bad URLs) are recorded
// in the activity log so the user knows why a server didn't come up.
// It is safe to call before the TUI event loop starts — appendActivity
// just appends to a TextView and is benign if the view isn't yet
// attached to a page.
func (a *App) loadMCPServers() {
	if a.mcpClient == nil || len(a.config.MCPServers) == 0 {
		return
	}
	for name, sc := range a.config.MCPServers {
		if !sc.Enabled {
			continue
		}
		if sc.Type != "" && sc.Type != "sse" {
			a.appendActivity(fmt.Sprintf("[%s]MCP %s skipped[-]: transport %q not yet supported (only \"sse\")",
				a.palette.HexOrchid, name, sc.Type))
			continue
		}
		if sc.URL == "" {
			a.appendActivity(fmt.Sprintf("[%s]MCP %s skipped[-]: missing url", a.palette.HexOrchid, name))
			continue
		}
		if err := a.mcpClient.AddServer(name, sc.URL); err != nil {
			a.appendActivity(fmt.Sprintf("[%s]MCP %s failed[-]: %v", a.palette.HexOrchid, name, err))
			continue
		}
		tools.RegisterMCPClient(a.registry, a.mcpClient)
		a.appendActivity(fmt.Sprintf("MCP server [%s]%s[-] restored from config", a.palette.HexLavender, name))
	}
}

func (a *App) Run() error {
	defer a.saveSession()
	defer a.stopSpinner()
	a.running = true
	defer func() { a.running = false }()
	// Start the in-session schedule worker for the lifetime of the
	// event loop. Due schedules are injected into the conversation
	// as attributable user messages; the watcher stops with the app.
	if a.scheduler != nil {
		a.scheduler.start()
		defer a.scheduler.stop()
	}
	// Flush any persisted transcript entries deferred from loadSession()
	// once the layout has actually been drawn. This runs on the very
	// first draw pass, at which point a.transcript.GetRect() returns a
	// real width, so user-message bubbles size to the pane instead of
	// the fixed-width fallback. We install the callback lazily (on Run)
	// rather than in New() so the app is always fully constructed first.
	if len(a.pendingTranscript) > 0 {
		pending := a.pendingTranscript
		a.pendingTranscript = nil
		a.tv.SetAfterDrawFunc(func(screen tcell.Screen) {
			// Render once; then drop the callback so it doesn't
			// re-run on every subsequent redraw.
			a.tv.SetAfterDrawFunc(nil)
			a.renderTranscriptFromEntries(pending)
		})
	}
	// Enable bracketed paste so terminals deliver a multi-line paste as
	// a single unit to the focused primitive's PasteHandler instead of a
	// stream of keystrokes. Without this, each newline in a pasted block
	// arrives as KeyEnter and triggers submit(), splitting the paste at
	// the first line break and dropping the remainder.
	a.tv.EnablePaste(true)
	return a.tv.Run()
}

// focusOrQueue moves keyboard focus to p. tview's SetFocus is
// safe to call from any goroutine (it acquires a lock and sets a
// field; the next Draw picks it up). We do not route focus through
// QueueUpdate/QueueUpdateDraw because those send on a channel that
// the event loop drains — and when the caller is already executing
// inside a QueueUpdate closure, the inner send would block forever
// and stall the event loop. That channel-send pattern was the
// trigger for the /tasks hang. The historical reason people route
// focus through QueueUpdate is ordering with respect to other
// queued updates; for our use case (user just opened a panel via
// slash command) ordering does not matter — the next event-loop
// tick will redraw with the new focus.
func (a *App) focusOrQueue(p tview.Primitive) {
	if p == nil {
		return
	}
	a.tv.SetFocus(p)
}

func (a *App) build() {
	a.header = tview.NewTextView().
		SetDynamicColors(true).
		SetWrap(false).
		SetTextAlign(tview.AlignLeft)
	a.header.SetBackgroundColor(a.palette.BgRoot)
	a.header.SetTextColor(a.palette.TextMain)
	a.header.SetBorderPadding(1, 1, 2, 2)
	a.headerRight = tview.NewTextView().
		SetDynamicColors(true).
		SetWrap(false).
		SetTextAlign(tview.AlignRight)
	a.headerRight.SetBackgroundColor(a.palette.BgRoot)
	a.headerRight.SetTextColor(a.palette.TextDim)
	a.headerRight.SetBorderPadding(1, 1, 2, 2)

	var transcriptPanel, reasoningPanel, activityPanel *tview.Flex
	a.transcript, a.transcriptTitle, transcriptPanel = a.newPanel("Conversation", a.palette.BgRoot, 0)
	a.transcriptPanel = transcriptPanel
	a.reasoning, a.reasoningTitle, reasoningPanel = a.newPanel("COGNITION", a.palette.BgReasoning, 1)
	a.reasoningPanel = reasoningPanel
	// Use a unified background for the right-hand column so reasoning
	// and activity read as a single connected pane.
	a.activity, a.activityTitle, activityPanel = a.newPanel("ACTIVITY", a.palette.BgReasoning, 1)
	a.activityPanel = activityPanel
	a.reasoning.SetTextColor(a.palette.TextDim)

	// Interactive tasks list. The list is built once and populated
	// each time the user opens /tasks. Toggling a task (Enter)
	// rewrites the entry in place rather than rebuilding the list,
	// so the cursor stays put and the activity log gets a single
	// concise entry per toggle.
	a.tasksList = tview.NewList().
		ShowSecondaryText(false).
		SetHighlightFullLine(true)
	a.tasksList.SetBackgroundColor(a.palette.BgReasoning)
	a.tasksList.SetMainTextColor(a.palette.TextMain)
	a.tasksList.SetSelectedBackgroundColor(a.palette.BgSelect)
	a.tasksList.SetSelectedTextColor(a.palette.Lavender)
	a.tasksList.SetBorderPadding(1, 1, 3, 2)
	a.tasksTitle = tview.NewTextView().SetDynamicColors(true)
	a.tasksTitle.SetBackgroundColor(a.palette.BgReasoning)
	a.tasksTitle.SetText(fmt.Sprintf(" [%s]TASKS[-]", a.palette.HexPurple))
	a.tasksTitle.SetBorderPadding(0, 0, 2, 2)
	a.tasksPanel = tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(a.tasksTitle, 1, 0, false).
		AddItem(a.tasksList, 0, 1, true)
	a.tasksPanel.SetBackgroundColor(a.palette.BgReasoning)

	a.memoriesList = tview.NewList().
		ShowSecondaryText(true).
		SetHighlightFullLine(true)
	a.memoriesList.SetBackgroundColor(a.palette.BgReasoning)
	a.memoriesList.SetMainTextColor(a.palette.TextMain)
	a.memoriesList.SetSecondaryTextColor(a.palette.TextDim)
	a.memoriesList.SetSelectedBackgroundColor(a.palette.BgSelect)
	a.memoriesList.SetSelectedTextColor(a.palette.Lavender)
	a.memoriesList.SetBorderPadding(1, 1, 3, 2)
	a.memoriesList.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyDelete, tcell.KeyBackspace2:
			a.deleteSelectedMemory()
			return nil
		}
		return event
	})
	a.memoriesTitle = tview.NewTextView().SetDynamicColors(true)
	a.memoriesTitle.SetBackgroundColor(a.palette.BgReasoning)
	a.memoriesTitle.SetText(fmt.Sprintf(" [%s]MEMORIES[-] [%s]Delete removes · Enter clears hint[-]", a.palette.HexPurple, a.palette.HexFaint))
	a.memoriesTitle.SetBorderPadding(0, 0, 2, 2)
	a.memoriesPanel = tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(a.memoriesTitle, 1, 0, false).
		AddItem(a.memoriesList, 0, 1, true)
	a.memoriesPanel.SetBackgroundColor(a.palette.BgReasoning)

	a.testView = tview.NewTextView().
		SetDynamicColors(true).
		SetWrap(true)
	a.testView.SetBackgroundColor(a.palette.BgReasoning)
	a.testView.SetTextColor(a.palette.TextMain)
	a.testView.SetBorderPadding(1, 1, 3, 2)
	a.testPanel = tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(a.testView, 0, 1, true)
	a.testPanel.SetBackgroundColor(a.palette.BgReasoning)

	a.footer = tview.NewTextView().SetDynamicColors(true)
	a.footer.SetBackgroundColor(a.palette.BgRoot)
	a.footer.SetBorderPadding(1, 1, 2, 2)

	// Lower-left agent + state pill. Renders "agent: <name> · <state>"
	// alongside the [Ctl+A] switch hint, mirroring the same vocabulary
	// used by the per-turn assistant label.
	a.statusBar = tview.NewTextView().SetDynamicColors(true)
	a.statusBar.SetBackgroundColor(a.palette.BgRoot)
	a.statusBar.SetBorderPadding(1, 1, 2, 2)

	// The cwd/ctx/refs status line now lives inside the same card as the
	// input field (see inputCard below), sharing its background so the
	// two read as one cohesive component.
	a.contextBar = tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignLeft)
	a.contextBar.SetBackgroundColor(a.palette.BgInput)
	a.contextBar.SetTextColor(a.palette.TextDim)
	a.contextBar.SetBorderPadding(1, 1, 2, 2)

	// The prompt bar gets generous padding in its own background color so
	// it "feels larger" and unmistakably reads as the text-entry surface,
	// rather than growing an actual border. The "›" indicator lives inside
	// the field itself (as its label), with its background explicitly
	// pinned to bgInput so it always matches the field surface exactly.
	a.input = tview.NewInputField().
		SetFieldBackgroundColor(a.palette.BgInput).
		SetFieldTextColor(a.palette.TextMain)
	a.input.SetLabel("")
	a.input.SetLabelStyle(tcell.StyleDefault.Foreground(a.palette.Lavender).Background(a.palette.BgInput))
	// Use tview's native placeholder so the field remains empty while the
	// hint is shown — this keeps GetText() returning "" (so submit guards
	// don't trip on the placeholder) and lets cursor/backspace behave
	// normally without a manual SetInputCapture shim.
	a.input.SetPlaceholder("Type a command or message (use / for commands)")
	a.input.SetPlaceholderTextColor(a.palette.TextFaint)
	a.input.SetPlaceholderStyle(tcell.StyleDefault.Foreground(a.palette.TextFaint).Background(a.palette.BgInput))
	// The input hint line (contextBar) alternates between session meta
	// and an inline command/reference palette based on the input's
	// first character. updateInputHint recomputes that state and
	// refreshContextBar renders it.
	a.input.SetChangedFunc(func(text string) {
		// Any non-empty text input (including slash commands) exits
		// about mode automatically so the user can chat without
		// having to type /about again to dismiss the splash.
		if a.aboutMode && text != "" {
			a.setAboutMode(false)
		}
		// A pasted (or typed) block spanning multiple lines is kept as
		// one message rather than being split at each newline; flag it
		// so the hint line can show a "multi-line" cue instead of the
		// command/reference palette.
		a.multiLineInput = strings.Contains(text, "\n")
		a.updateInputHint(text)
		a.refreshContextBar()
	})
	a.input.SetBackgroundColor(a.palette.BgInput)
	a.input.SetBorderPadding(1, 0, 2, 2)
	a.input.SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEnter {
			a.submit()
		}
	})
	// Tab / Shift+Tab cycle the highlighted entry in the command
	// palette while it is active; Enter completes the highlighted
	// command into the input unless the typed token is already an
	// exact command (then it submits normally). Arrow keys are left
	// untouched so the text cursor behaves as usual.
	a.input.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		// Global input history navigation (Up/Down). This takes
		// priority over the command palette so the user can always
		// recall prior input regardless of what hint mode is active.
		// When hintMode is "command" (slash palette), Up/Down still
		// navigate history rather than the palette — Tab/Shift+Tab
		// remain the palette navigation keys.
		switch event.Key() {
		case tcell.KeyUp:
			a.navigateInputHistory(-1)
			return nil
		case tcell.KeyDown:
			a.navigateInputHistory(1)
			return nil
		}
		if a.hintMode != "command" || len(a.hintMatches) == 0 {
			return event
		}
		switch event.Key() {
		case tcell.KeyTab:
			a.hintSelected = (a.hintSelected + 1) % len(a.hintMatches)
			a.refreshContextBar()
			return nil
		case tcell.KeyBacktab:
			a.hintSelected = (a.hintSelected - 1 + len(a.hintMatches)) % len(a.hintMatches)
			a.refreshContextBar()
			return nil
		case tcell.KeyEnter:
			typed := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(a.input.GetText()), "/"))
			// Exact match already typed: let submit run it.
			for _, c := range a.hintMatches {
				if c.cmd == typed {
					return event
				}
			}
			// Otherwise complete to the highlighted command and stay
			// in the field so the user can add arguments / confirm.
			sel := a.hintMatches[a.hintSelected]
			a.input.SetText("/" + sel.cmd + " ")
			return nil
		}
		return event
	})

	// Pre-build the About TextViews so toggling aboutMode (via
	// /about) is just a layout swap rather than a rebuild. The ASCII
	// view is left-aligned, the intro is a wrapped body mirroring
	// the splash used by renderStartupSplash / renderAboutSplash.
	a.aboutAscii = tview.NewTextView().
		SetDynamicColors(true).
		SetWrap(false).
		SetTextAlign(tview.AlignLeft)
	a.aboutAscii.SetBackgroundColor(a.palette.BgRoot)
	a.aboutAscii.SetTextColor(a.palette.Lavender)
	a.aboutAscii.SetBorderPadding(2, 1, 4, 2)
	a.aboutAscii.SetText(a.loadAsciiArt())

	a.aboutBody = tview.NewTextView().
		SetDynamicColors(true).
		SetWrap(true)
	a.aboutBody.SetBackgroundColor(a.palette.BgRoot)
	a.aboutBody.SetTextColor(a.palette.TextMain)
	a.aboutBody.SetBorderPadding(2, 1, 4, 2)
	a.aboutBody.SetText(a.renderAboutIntro())

	vGutter := spacerBox(a.palette.BgRoot)

	// Stack reasoning and activity directly so their backgrounds connect.
	// Activity gets priority over Cognition (2:3 ratio) so longer event
	// logs don't get squeezed off the bottom of the screen. We use
	// proportion 0 (flex) for activity so it grows to fit its
	// contents up to a sensible cap, while cognition is held at a
	// minimum 3 share so the model monologue always has room to
	// stream.
	right := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(reasoningPanel, 0, 2, false).
		AddItem(activityPanel, 0, 3, false)
	right.SetBackgroundColor(a.palette.BgReasoning)

	// About layout: ASCII on the left, intro on the right. Pre-built
	// so toggling aboutMode is just a layout swap rather than a
	// rebuild of the underlying TextViews.
	aboutRow := tview.NewFlex().
		AddItem(a.aboutAscii, 0, 1, false).
		AddItem(vGutter, panelGutter, 0, false).
		AddItem(a.aboutBody, 0, 1, false)
	aboutRow.SetBackgroundColor(a.palette.BgRoot)

	// In aboutMode the "Conversation" title is hidden by using the
	// bare transcript TextView in place of the full panel, so the
	// ASCII art reads from the very top of the surface.
	var main *tview.Flex
	if a.aboutMode {
		left := tview.NewFlex().
			AddItem(a.transcript, 0, 1, false)
		left.SetBackgroundColor(a.palette.BgRoot)
		main = tview.NewFlex().
			AddItem(left, 0, 5, false).
			AddItem(vGutter, panelGutter, 0, false).
			AddItem(aboutRow, 0, 3, false)
	} else {
		main = tview.NewFlex().
			AddItem(transcriptPanel, 0, 5, false).
			AddItem(vGutter, panelGutter, 0, false).
			AddItem(right, 0, 3, false)
	}
	main.SetBackgroundColor(a.palette.BgRoot)

	// Treat the entire prompt shell as a single bgInput-backed component so
	// the input field and its status line read as one cohesive surface.
	inputSurface := a.newInputSurface()

	// Inset the prompt bar horizontally using the same standard margin as
	// the panels above (marginX), matching the earlier stable layout.
	inputRow := tview.NewFlex().
		AddItem(spacerBox(a.palette.BgRoot), marginX-1, 0, false).
		AddItem(inputSurface, 0, 1, true).
		AddItem(spacerBox(a.palette.BgRoot), marginX, 0, false)
	inputRow.SetBackgroundColor(a.palette.BgRoot)
	a.inputRow = inputRow

	headerRow := tview.NewFlex().
		AddItem(a.header, 0, 1, false).
		AddItem(a.headerRight, 0, 1, false)
	headerRow.SetBackgroundColor(a.palette.BgRoot)

	// Footer row: lower-left agent + state pill, lower-right
	// panel shortcuts. The two TextViews share the bottom row so the
	// the bottom edge of the screen stays anchored while the rest of
	// the layout flexes.
	footerRow := tview.NewFlex().
		AddItem(a.statusBar, 0, 1, false).
		AddItem(a.footer, 0, 1, false)
	footerRow.SetBackgroundColor(a.palette.BgRoot)

	layout := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(headerRow, 3, 0, false).
		AddItem(spacerBox(a.palette.BgRoot), panelGutter, 0, false).
		AddItem(main, 0, 1, false).
		AddItem(spacerBox(a.palette.BgRoot), panelGutter, 0, false).
		AddItem(inputRow, 5, 0, true).
		AddItem(spacerBox(a.palette.BgRoot), panelGutter, 0, false).
		AddItem(footerRow, 3, 0, false)
	layout.SetBackgroundColor(a.palette.BgRoot)

	a.pages = tview.NewPages().
		AddPage("main", layout, true, true)
	a.rootFlex = layout

	a.tv.SetRoot(a.pages, true)
	a.tv.SetFocus(a.input)
	a.tv.SetInputCapture(a.globalKeys)
	a.refreshHeader()
	a.refreshHeaderRight()
	a.refreshContextBar()
	a.refreshFooter()
	a.setActivePanel("activity")
	a.appendActivity("Developed by TJ Coder / AI Labs")
	a.appendActivity("Loaded provider " + a.provider.BaseURL())
	a.appendActivity("Enabled tools: " + strings.Join(a.enabledToolList(), ", "))

	// If the agent has not yet had a chance to respond (no history),
	// kick off the cognition-recap trigger so the Cognition pane warms
	// up with a one-paragraph summary of the current session state.
	a.firstRun = true
	if len(a.history) == 0 {
		a.appendActivity("[cognition] priming with a non-interactive recap...")
		go a.runCognitionRecap()
	}
}

// userMessageMaxWidth returns the widest line, in cells, that a
// user message is allowed to occupy before it is word-wrapped to
// fit. The cap derives from the live conversation-pane width so
// bubbles track the available space on any terminal size. The
// minimum guarantees the bubble is always at least 24 cells wide
// even on extremely narrow terminals so it remains readable.
//
// A "wrapped" message (one whose text must break across lines) is
// sized to fill the entire pane so every line shares one continuous
// highlight edge, like a modern messaging bubble. Short messages stay
// compact.
const (
	userMessageMinWidth = 24
	// userMessageMaxWidth is the historical hard ceiling. It is no longer
	// applied as a cap on the live pane width (bubbles now track the
	// actual conversation-pane width). It is retained only as an upper
	// bound for the explicit /config override so a bad value can't
	// overflow the screen.
	userMessageMaxWidth = 80
	// userMessagePadding is the inner horizontal inset (left + right, in
	// cells) applied to user-message bubbles so text sits comfortably
	// inside the purple highlight instead of hugging its edge.
	userMessagePadding = 2
)

func (a *App) userMessageMaxWidth() int {
	// 1. If the user pinned an explicit cap in /config, honor it
	//    (clamped to sane bounds so a bad value can't make bubbles
	//    invisible or wider than the screen).
	if a.config.UserMessageMaxWidth > 0 {
		w := a.config.UserMessageMaxWidth
		if w < userMessageMinWidth {
			return userMessageMinWidth
		}
		if w > userMessageMaxWidth {
			return userMessageMaxWidth
		}
		return w
	}
	// 2. Otherwise, derive from the live conversation body width.
	//    Read it from tview so the cap matches what the user is
	//    actually *seeing*, not a constant estimate. Falls back to
	//    a sane default (80-col terminal) during unit tests where
	//    no screen is mounted.
	cols := 0
	if a.transcript != nil {
		_, _, w, _ := a.transcript.GetRect()
		cols = w
	}
	if cols <= 0 {
		return 38
	}
	// Subtract the 3-left + 2-right border padding the body has
	// so we never overflow the visible cell area.
	body := cols - 5
	if body < userMessageMinWidth {
		return userMessageMinWidth
	}
	return body
}

// wordWrap breaks s on whitespace so no output line exceeds width
// cells. Existing newlines are preserved as hard breaks. Words
// longer than width are emitted on their own line rather than
// split (we don't slice inside runes).
func wordWrap(s string, width int) []string {
	if width <= 0 {
		return strings.Split(s, "\n")
	}
	var out []string
	for _, paragraph := range strings.Split(s, "\n") {
		if paragraph == "" {
			out = append(out, "")
			continue
		}
		words := strings.Fields(paragraph)
		if len(words) == 0 {
			out = append(out, "")
			continue
		}
		var line string
		for _, w := range words {
			if utf8.RuneCountInString(w) > width {
				// Flush whatever we have, emit the oversized
				// word on its own line, then start fresh.
				if line != "" {
					out = append(out, line)
					line = ""
				}
				out = append(out, w)
				continue
			}
			candidate := w
			if line != "" {
				candidate = line + " " + w
			}
			if utf8.RuneCountInString(candidate) > width {
				out = append(out, line)
				line = w
			} else {
				line = candidate
			}
		}
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func (a *App) renderUserMessage(prompt string) string {
	maxWidth := a.userMessageMaxWidth()
	lines := wordWrap(prompt, maxWidth)
	maxLen := 0
	for _, line := range lines {
		if l := utf8.RuneCountInString(line); l > maxLen {
			maxLen = l
		}
	}
	// Inner horizontal padding around the message text.
	padding := userMessagePadding
	width := maxLen + padding*2
	// A "wrapped" message is one whose whitespace-normalized length exceeds
	// a single line, i.e. the text had to break across lines. In that case
	// the bubble takes the full conversation-pane width so that every line
	// (long and short alike) shares one continuous highlight edge — like a
	// modern messaging bubble that fills the available space when narrow.
	// Hard newlines are deliberately collapsed here so a short multi-line
	// message stays compact instead of stretching edge-to-edge.
	normalized := utf8.RuneCountInString(strings.Join(strings.Fields(prompt), " "))
	if normalized > maxWidth {
		width = maxWidth
	}
	// Use non-breaking spaces so tview doesn't trim trailing spaces and the
	// background color fills the entire bubble width.
	nbsp := "\u00A0"
	blank := strings.Repeat(nbsp, width)
	innerPad := strings.Repeat(nbsp, padding)

	// Border and content lines use explicit fg+bg color tags (rather than
	// shorthand like "[-:%s:-]") so tview's color-region tracker carries
	// the bubble's background through any SetText/GetText round trip
	// (e.g. updateAssistantTurnLabel re-stitches the whole transcript
	// when the assistant label changes). The closing tag re-states the
	// bubble's background so the colored region is properly closed
	// before the newline and adjacent lines don't bleed into each
	// other. The visible output is identical: the border lines are
	// still non-breaking spaces fully painted in HexPurple.
	purple := a.palette.HexPurple
	main := a.palette.HexMain
	root := a.palette.HexRoot
	var b strings.Builder
	fmt.Fprintf(&b, "[%[1]s:%[1]s:-]%[3]s[%[2]s:%[2]s:-]\n", purple, root, blank)
	for _, line := range lines {
		padded := innerPad + line
		// Pad with non-breaking spaces to preserve trailing space width
		extra := width - utf8.RuneCountInString(padded)
		if extra > 0 {
			padded += strings.Repeat(nbsp, extra)
		}
		fmt.Fprintf(&b, "[%[1]s:%[2]s:b]%[4]s[%[3]s:%[3]s:-]\n", main, purple, root, padded)
	}
	// Final border line followed by an explicit `[-:-:-]` reset on its
	// own so the bubble's purple background does not bleed into the
	// following assistant label / body. Without the trailing reset,
	// tview's tag parser carries the prior `bg=purple` style into the
	// next region's "no-bg-specified" tag (e.g. `[purple::b]`) and the
	// label ends up rendered as purple-on-purple, which is what the
	// user reported as "transparent lettering on a purple background".
	fmt.Fprintf(&b, "[%[1]s:%[1]s:-]%[3]s[%[2]s:%[2]s:-]\n", purple, root, blank)
	fmt.Fprint(&b, "[-:-:-]\n")
	return b.String()
}

func (a *App) restyleLegacyUserMessages(text string) string {
	if text == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	var out []string
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		// Detect old user message format (with or without the old "You" label)
		if strings.Contains(line, "["+a.palette.HexLavender+"::b]You[-:-:-]") ||
			(strings.Contains(line, "["+a.palette.HexMain+":"+a.palette.HexPurple+":b]") && i > 0 && strings.Contains(lines[i-1], "You")) {
			// Collect all lines until blank or next label
			var content []string
			for j := i + 1; j < len(lines); j++ {
				next := lines[j]
				if strings.TrimSpace(next) == "" {
					break
				}
				if strings.Contains(next, "Coder is thinking") || strings.Contains(next, "Coder replied:") || strings.Contains(next, "Coder says:") {
					break
				}
				// Extract content from styled lines
				if strings.Contains(next, "["+a.palette.HexMain+":"+a.palette.HexPurple+":b]") {
					payload := strings.TrimSpace(strings.SplitN(next, "]", 2)[1])
					payload = strings.TrimSuffix(payload, "[-:-:-]")
					content = append(content, strings.TrimSpace(payload))
				} else if strings.Contains(next, "[-:"+a.palette.HexPurple+":-]") {
					// Skip a.palette.Purple border lines
					continue
				} else {
					content = append(content, strings.TrimSpace(next))
				}
				i = j
			}
			if len(content) > 0 {
				out = append(out, a.renderUserMessage(strings.Join(content, "\n")))
			}
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// restyleLegacyAssistantMessages ensures older assistant turns include a
// consistent label, a timestamp line, and applies the same transcript
// highlighting we use for new content.
func (a *App) restyleLegacyAssistantMessages(text string) string {
	if text == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	var out []string
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		out = append(out, line)
		// If this line is an assistant label, ensure the next non-empty
		// line is a timestamp. Then restyle the following content block.
		if strings.Contains(line, "Coder is thinking") || strings.Contains(line, "Coder replied:") || strings.Contains(line, "Coder says:") {
			// Look ahead for a timestamp line; if absent, insert one.
			nextIdx := i + 1
			for nextIdx < len(lines) && strings.TrimSpace(lines[nextIdx]) == "" {
				nextIdx++
			}
			needsStamp := true
			if nextIdx < len(lines) && strings.Contains(lines[nextIdx], a.palette.HexDim) {
				needsStamp = false
			}
			if needsStamp {
				stamp := formatTimestamp(time.Now())
				out = append(out, fmt.Sprintf("[%s]%s[-]", a.palette.HexDim, stamp))
			}
			// Find content block until blank line or next label and run highlight
			contentStart := i + 1
			if !needsStamp {
				contentStart = nextIdx + 1
			}
			contentEnd := contentStart
			for contentEnd < len(lines) {
				l := strings.TrimSpace(lines[contentEnd])
				if l == "" {
					break
				}
				if strings.Contains(lines[contentEnd], "Coder is thinking") || strings.Contains(lines[contentEnd], "Coder replied:") || strings.Contains(lines[contentEnd], "Coder says:") {
					break
				}
				contentEnd++
			}
			if contentEnd > contentStart {
				block := strings.Join(lines[contentStart:contentEnd], "\n")
				highlighted := a.highlightTranscriptText(block)
				out = append(out, strings.Split(highlighted, "\n")...)
				i = contentEnd - 1
			}
		}
	}
	return strings.Join(out, "\n")
}

// spacerBox is a plain, borderless filler used to create a thin gutter of
// negative space between adjacent panels without introducing a border.
func spacerBox(bg tcell.Color) *tview.Box {
	b := tview.NewBox()
	b.SetBackgroundColor(bg)
	return b
}

func (a *App) newInputSurface() *tview.Flex {
	inner := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(a.input, 3, 0, true).
		AddItem(a.contextBar, 3, 0, false)
	inner.SetBackgroundColor(a.palette.BgInput)

	leftBeam := tview.NewBox()
	leftBeam.SetBackgroundColor(a.palette.BgInput)
	leftBeam.SetDrawFunc(func(screen tcell.Screen, x, y, width, height int) (int, int, int, int) {
		focus := false
		if a.input != nil {
			focus = a.input.HasFocus()
		}
		style := tcell.StyleDefault.Foreground(a.palette.Purple).Background(a.palette.BgInput)
		if !focus {
			style = tcell.StyleDefault.Foreground(tcell.NewRGBColor(84, 49, 124)).Background(a.palette.BgInput)
		}
		for row := 0; row < height; row++ {
			screen.SetContent(x, y+row, '│', nil, style)
		}
		return x, y, width, height
	})

	surface := tview.NewFlex().
		AddItem(leftBeam, 1, 0, false).
		AddItem(spacerBox(a.palette.BgInput), marginX-1, 0, false).
		AddItem(inner, 0, 1, true).
		AddItem(spacerBox(a.palette.BgInput), marginX, 0, false)
	surface.SetBackgroundColor(a.palette.BgInput)
	return surface
}

func (a *App) setInputPlaceholder() {
	// Native placeholder: tview renders the hint only when the field is
	// empty, so we just push the text into SetPlaceholder.
	if a.input == nil {
		return
	}
	a.input.SetPlaceholder("Type a command or message (use / for commands)")
	a.input.SetPlaceholderTextColor(a.palette.TextFaint)
	a.input.SetPlaceholderStyle(tcell.StyleDefault.Foreground(a.palette.TextFaint).Background(a.palette.BgInput))
}

// (a *App) newPanel builds a borderless content panel: a single-line label row
// (used in place of a box title) stacked above a scrollable body, both
// sharing the same subtly-elevated background so the pair reads as one
// surface without needing a border. topGap adds extra blank rows above
// the label (still within the panel's own background) for panels that
// need breathing room before their title, e.g. the two right-hand panels.
func (a *App) newPanel(title string, bg tcell.Color, topGap int) (*tview.TextView, *tview.TextView, *tview.Flex) {
	label := tview.NewTextView().SetDynamicColors(true)
	label.SetBackgroundColor(bg)
	label.SetText(fmt.Sprintf(" [%s]%s[-]", a.palette.HexPurple, strings.ToUpper(title)))
	label.SetBorderPadding(topGap, 0, 2, 2)

	body := tview.NewTextView().
		SetDynamicColors(true).
		SetWrap(true).
		SetScrollable(true)
	body.SetBackgroundColor(bg)
	body.SetTextColor(a.palette.TextMain)
	// The label text has a literal leading space before its color tag (see
	// above), so its visible text starts one column right of its own
	// padding edge. Give the body one extra column of left padding here
	// (and only here) so its content lines up directly under the title
	// instead of sitting one column to its left.
	body.SetBorderPadding(1, 1, 3, 2)

	container := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(label, 1+topGap, 0, false).
		AddItem(body, 0, 1, false)
	container.SetBackgroundColor(bg)

	return body, label, container
}

func (a *App) refreshHeader() {
	// Minimal header: program name + version. Everything else (agent,
	// model, provider, cwd, tool count, etc.) lives in the context bar
	// and footer so the top of the screen stays clean.
	title := fmt.Sprintf(
		"[%s::b] %s[-:-:-] [%s]v%s[-]",
		a.palette.HexPurple, a.productName,
		a.palette.HexDim, a.appVersion,
	)
	a.header.SetText(title)
}

func (a *App) refreshFooter() {
	// Right-aligned bottom bar: panel shortcuts only. The agent + state
	// indicator (with the [Ctl+A] hint) lives on the lower-left, so the
	// right side stays free for global Alt-shortcut hints.
	hint := func(key, label string) string {
		return fmt.Sprintf("[%s]%s[-] %s", a.palette.HexLavender, key, label)
	}
	a.footer.SetTextAlign(tview.AlignRight)
	a.footer.SetText(fmt.Sprintf(
		"  %s  %s  %s",
		hint("[Alt+A]", "Activity"),
		hint("[Alt+T]", "Tasks"),
		hint("[Alt+C]", "Code"),
	))
	a.refreshStatusIndicator()
}

// agentStateLabel returns a small word describing the agent's current
// state ("online", "thinking", or "replying") so the lower-left
// indicator can flip through them in real time.
func (a *App) agentStateLabel() string {
	if a.busy {
		// Distinguish "thinking" (model mid-stream) from "replying" (model
		// has produced its first commentary chunk). assistantState is set
		// by the runner event handlers as the response streams in.
		if a.assistantState == "replied" {
			return "replying"
		}
		return "thinking"
	}
	return "online"
}

// refreshStatusIndicator renders the lower-left agent + state pill
// ("agent: ergo · online"), plus the [Ctl+A] hint. It is called from
// refreshFooter so the two halves of the bottom row stay in sync.
func (a *App) refreshStatusIndicator() {
	if a.statusBar == nil {
		return
	}
	agentName := a.currentAgent.Name
	if agentName == "" {
		agentName = "coder"
	}
	modelName := a.currentModel
	if modelName == "" {
		modelName = "—"
	}
	// Shorten the model name for the status bar so it doesn't eat
	// horizontal space. Keep the last meaningful segment after any
	// "/" prefix.
	if idx := strings.LastIndex(modelName, "/"); idx >= 0 && idx < len(modelName)-1 {
		modelName = modelName[idx+1:]
	}
	state := a.agentStateLabel()
	stateColor := a.palette.HexLavender
	stateBold := false
	switch state {
	case "thinking":
		stateColor = a.palette.HexPurple
		stateBold = true
	case "replying":
		stateColor = a.palette.HexViolet
		stateBold = true
	}
	var stateTag string
	if stateBold {
		stateTag = fmt.Sprintf("[%s::b]%s[-:-:-]", stateColor, state)
	} else {
		stateTag = fmt.Sprintf("[%s]%s[-]", stateColor, state)
	}
	a.statusBar.SetTextAlign(tview.AlignLeft)
	a.statusBar.SetText(fmt.Sprintf(
		"[%s]agent:[-] [%s::b]%s[-:-:-] [%s]·[-] [%s]model:[-] [%s::b]%s[-:-:-] · %s   [%s][Ctl+A][-] switch agent",
		a.palette.HexDim, a.palette.HexLavender, agentName,
		a.palette.HexDim, a.palette.HexDim, a.palette.HexLavender, modelName,
		stateTag, a.palette.HexLavender,
	))
}

// refreshHeaderRight updates the right-aligned [Alt+I] / [Ctl+F] /
// [F11] hints. The "Fullscreen" label flips to "Exit Fullscreen"
// when the right column is hidden. Both Ctrl+F and F11 trigger
// fullscreen so the user can pick whichever is more natural; Ctrl+F
// is the mnemonic primary and F11 is the muscle-memory alias from
// browsers / editors.
func (a *App) refreshHeaderRight() {
	if a.headerRight == nil {
		return
	}
	fsLabel := "Fullscreen"
	if a.fullscreen {
		fsLabel = "Exit Fullscreen"
	}
	a.headerRight.SetText(fmt.Sprintf(
		"[%s][Alt+I][-] About   [%s][Ctl+F][-] %s   [%s][F11][-]",
		a.palette.HexLavender, a.palette.HexLavender, fsLabel, a.palette.HexLavender,
	))
}

func (a *App) refreshContextBar() string {
	// The hint line alternates between the inline command/reference
	// palette (while the user is typing a "/" or "@" prefix) and the
	// session meta line (cwd / context / refs) otherwise.
	var line string
	switch a.hintMode {
	case "command":
		line = a.renderCommandPalette()
	case "reference":
		line = a.renderReferencePalette()
	case "multiline":
		line = a.renderMultiLineHint()
	default:
		line = a.renderMetaBar()
	}
	if a.contextBar != nil {
		a.contextBar.SetText(line)
	}
	return line
}

// updateInputHint inspects the current input text and sets the hint-line
// mode and (for command mode) the filtered command matches. A leading
// "/" activates the command palette; a leading "@" activates the
// reference palette (stubbed pending a workspace file index); anything
// else reverts the line to session meta.
func (a *App) updateInputHint(text string) {
	switch {
	case a.multiLineInput:
		// Multi-line input (pasted block): no palette. The meta bar
		// renders a dedicated multi-line cue instead.
		a.hintMode = "multiline"
		a.hintMatches = nil
		a.hintSelected = 0
	case strings.HasPrefix(text, "/"):
		q := strings.ToLower(strings.TrimPrefix(text, "/"))
		// Match on the first token only so arguments don't disturb the
		// palette once a command has been chosen.
		if i := strings.IndexAny(q, " \t"); i != -1 {
			q = q[:i]
		}
		matches := make([]slashCommand, 0, len(slashCommands))
		for _, c := range slashCommands {
			if q == "" || strings.HasPrefix(c.cmd, q) {
				matches = append(matches, c)
			}
		}
		a.hintMode = "command"
		a.hintMatches = matches
		if a.hintSelected >= len(matches) {
			a.hintSelected = 0
		}
	case strings.HasPrefix(text, "@"):
		a.hintMode = "reference"
		a.hintMatches = nil
		a.hintSelected = 0
	default:
		a.hintMode = ""
		a.hintMatches = nil
		a.hintSelected = 0
	}
}

// paletteWidth returns the number of columns available for the hint line
// content, accounting for the contextBar's horizontal border padding.
func (a *App) paletteWidth() int {
	if a.contextBar == nil {
		return 80
	}
	_, _, w, _ := a.contextBar.GetInnerRect()
	if w <= 0 {
		w = 80
	}
	return w
}

// renderCommandPalette renders the matched "/" commands as a single
// horizontally-windowed line. The highlighted entry (hintSelected) is
// always kept within the visible window; minimal chevrons ("‹" / "›")
// flag content clipped off either edge.
func (a *App) renderCommandPalette() string {
	if len(a.hintMatches) == 0 {
		return fmt.Sprintf(" [%s]no matching commands[-] ", a.palette.HexFaint)
	}
	// Plain (untagged) label per entry, used for width accounting.
	labels := make([]string, len(a.hintMatches))
	for i, c := range a.hintMatches {
		labels[i] = "/" + c.cmd
	}
	const sep = "  "
	width := a.paletteWidth()
	// Reserve two columns for potential chevrons on each side.
	budget := width - 4
	if budget < 8 {
		budget = 8
	}

	// Grow a window around hintSelected until it no longer fits.
	start, end := a.hintSelected, a.hintSelected+1
	used := len(labels[a.hintSelected])
	for {
		grew := false
		if end < len(labels) {
			if cost := len(sep) + len(labels[end]); used+cost <= budget {
				used += cost
				end++
				grew = true
			}
		}
		if start > 0 {
			if cost := len(sep) + len(labels[start-1]); used+cost <= budget {
				used += cost
				start--
				grew = true
			}
		}
		if !grew {
			break
		}
	}

	var b strings.Builder
	b.WriteString(" ")
	if start > 0 {
		b.WriteString(fmt.Sprintf("[%s]‹[-] ", a.palette.HexPurple))
	}
	for i := start; i < end; i++ {
		if i > start {
			b.WriteString(sep)
		}
		if i == a.hintSelected {
			// Highlighted entry: bright lavender + description hint.
			b.WriteString(fmt.Sprintf("[%s::b]%s[-:-:-]", a.palette.HexLavender, labels[i]))
		} else {
			b.WriteString(fmt.Sprintf("[%s]%s[-]", a.palette.HexPurple, labels[i]))
		}
	}
	if end < len(labels) {
		b.WriteString(fmt.Sprintf(" [%s]›[-]", a.palette.HexPurple))
	}
	// Append the highlighted command's description as a faint trailer
	// when there's room, so the user knows what the selection does.
	desc := a.hintMatches[a.hintSelected].desc
	if desc != "" && used+len(desc)+3 <= budget {
		b.WriteString(fmt.Sprintf("   [%s]%s[-]", a.palette.HexFaint, desc))
	}
	b.WriteString(" ")
	return b.String()
}

// renderReferencePalette is a placeholder for the forthcoming "@" file
// reference picker. Until a workspace file index is wired up, it simply
// advertises the feature so the mode is discoverable.
func (a *App) renderReferencePalette() string {
	return fmt.Sprintf(" [%s]@ file references — coming soon[-] ", a.palette.HexFaint)
}

// renderMultiLineHint renders the hint line while the input field holds a
// multi-line block (typically a paste). It reports the line count and
// reminds the user that Enter submits the whole block as one message.
func (a *App) renderMultiLineHint() string {
	lines := 1
	if a.input != nil {
		if t := a.input.GetText(); t != "" {
			lines = strings.Count(t, "\n") + 1
		}
	}
	return fmt.Sprintf(" [%s]%d-line input[-] [%s]Enter to send as one message[-] ",
		a.palette.HexPurple, lines, a.palette.HexDim)
}

func (a *App) renderMetaBar() string {
	cwd := a.workspaceRoot
	if cwd == "" {
		cwd = "."
	}
	contextText := strings.TrimSpace(strings.TrimPrefix(a.contextInfo, "ctx:"))
	contextText = strings.TrimSpace(contextText)
	if contextText == "" {
		contextText = "unavailable"
	}
	// If contextText contains a numeric "used / total" pair, render a compact
	// progress bar in the primary tint color and replace the numeric portion
	// with the bar to keep the context line compact and live-updating.
	if contextText != "unavailable" {
		// Match numbers with optional K/M suffixes, e.g. "12K / 127K" or "3% / 2M"
		re := regexp.MustCompile(`([\d.]+)\s*([KM]?)\s*/\s*([\d.]+)\s*([KM]?)`)
		if m := re.FindStringSubmatchIndex(contextText); m != nil {
			usedStr := contextText[m[2]:m[3]]
			usedSuffix := contextText[m[4]:m[5]]
			totalStr := contextText[m[6]:m[7]]
			totalSuffix := contextText[m[8]:m[9]]
			used := parseTokenCount(usedStr, usedSuffix)
			total := parseTokenCount(totalStr, totalSuffix)
			if total > 0 {
				bar := a.renderProgressBar(used, total, 28)
				contextText = bar
			}
		}
	}

	refsText := strings.TrimSpace(strings.TrimPrefix(a.referenceSummary(), "refs:"))
	refsText = strings.TrimSpace(refsText)
	focus := false
	if a.input != nil {
		focus = a.input.HasFocus()
	}
	accent := a.palette.HexPurple
	if !focus {
		accent = "#564A70"
	}
	// Build context bar with conditional bullets:
	// - No leading bullet before cwd
	// - Skip context bullet and label if context is "unavailable"
	// - Skip refs bullet and label if refs are empty
	var line string
	if contextText == "unavailable" {
		// No context shown, no context bullet
		if refsText == "" {
			// No refs either: just progress + cwd + trailing space
			line = fmt.Sprintf("[%s]%s[-] [%s]%s[-] ",
				accent, a.progressGlyph(), a.palette.HexDim, cwd)
		} else {
			// Refs only, no context: progress + cwd + refs (no bullet before refs)
			line = fmt.Sprintf("[%s]%s[-] [%s]%s[-]   [%s]%s[-] ",
				accent, a.progressGlyph(), a.palette.HexDim, cwd, a.palette.HexDim, refsText)
		}
	} else {
		// Context is shown
		if refsText == "" {
			// Context only, no refs: progress + cwd + bullet + context (no trailing refs)
			line = fmt.Sprintf("[%s]%s[-] [%s]%s[-]   [%s]●[-] [%s]%s[-] ",
				accent, a.progressGlyph(), a.palette.HexDim, cwd, accent, a.palette.HexDim, contextText)
		} else {
			// Both context and refs: progress + cwd + bullet + context + bullet + refs
			line = fmt.Sprintf("[%s]%s[-] [%s]%s[-]   [%s]●[-] [%s]%s[-]   [%s]●[-] [%s]%s[-] ",
				accent, a.progressGlyph(), a.palette.HexDim, cwd, accent, a.palette.HexDim, contextText, accent, a.palette.HexDim, refsText)
		}
	}
	if a.contextBar != nil {
		a.contextBar.SetText(line)
	}
	return line
}

// (a *App) renderProgressBar builds a compact 12-segment progress bar with a highlighted current segment.
// Output format: [--progress--] 3% of 126,980 tokens.
func (a *App) renderProgressBar(used, total, _width int) string {
	const segments = 12
	if total <= 0 {
		return ""
	}
	pct := float64(used) / float64(total)
	if pct < 0 {
		pct = 0
	}
	if pct > 1 {
		pct = 1
	}
	// number of fully consumed segments (left of the current highlight)
	consumed := int(pct * float64(segments))
	if consumed < 0 {
		consumed = 0
	}
	if consumed > segments-1 {
		consumed = segments - 1
	}
	// current segment index (0..segments-1)
	current := consumed

	var b strings.Builder
	b.WriteString("[")
	for i := 0; i < segments; i++ {
		if i < consumed {
			// consumed segment (dim)
			b.WriteString(fmt.Sprintf("[%s]%s[-]", a.palette.HexDim, "━"))
		} else if i == current {
			// current highlighted segment (primary tint)
			b.WriteString(fmt.Sprintf("[%s]%s[-]", a.palette.HexPurple, "▮"))
		} else {
			// unconsumed segment (faint)
			b.WriteString(fmt.Sprintf("[%s]%s[-]", a.palette.HexDim, "·"))
		}
	}
	b.WriteString("]")

	percent := int(pct * 100)
	formattedTotal := formatTokensCompactTUI(total)
	return fmt.Sprintf("%s %d%% of %s tokens.", b.String(), percent, formattedTotal)
}

// formatWithCommas formats an integer with comma separators, e.g. 126980 -> "126,980"
func formatWithCommas(n int) string {
	if n < 0 {
		n = -n
		return "-" + formatWithCommas(n)
	}
	s := strconv.FormatInt(int64(n), 10)
	if len(s) <= 3 {
		return s
	}
	var parts []string
	for len(s) > 3 {
		parts = append([]string{s[len(s)-3:]}, parts...)
		s = s[:len(s)-3]
	}
	if s != "" {
		parts = append([]string{s}, parts...)
	}
	return strings.Join(parts, ",")
}

// parseTokenCount parses a numeric string with an optional K/M suffix
// into an integer token count. Used by renderMetaBar to extract
// used/total from context strings like "~12K / 127K used".
func parseTokenCount(num, suffix string) int {
	f := 0.0
	fmt.Sscanf(num, "%f", &f)
	switch strings.ToUpper(suffix) {
	case "K":
		f *= 1000
	case "M":
		f *= 1_000_000
	}
	return int(f)
}

// formatTokensCompactTUI renders a token count using K/M suffixes.
// Mirrors tooling.formatTokensCompact but lives in the tui package
// to avoid a cross-package dependency.
func formatTokensCompactTUI(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	if n < 1_000_000 {
		v := float64(n) / 1000
		if v == float64(int(v)) {
			return fmt.Sprintf("%dK", int(v))
		}
		return fmt.Sprintf("%.1fK", v)
	}
	v := float64(n) / 1_000_000
	if v == float64(int(v)) {
		return fmt.Sprintf("%dM", int(v))
	}
	return fmt.Sprintf("%.1fM", v)
}

func (a *App) progressGlyph() string {
	if !a.busy {
		// Use the larger filled bullet so it matches other separators.
		return "●"
	}
	return spinner[a.spinIdx%len(spinner)]
}

func (a *App) startSpinner() {
	a.stopSpinner()
	stop := make(chan struct{})
	a.spinStop = stop
	go func() {
		ticker := time.NewTicker(120 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				a.tv.QueueUpdateDraw(func() {
					if !a.busy {
						return
					}
					a.spinIdx = (a.spinIdx + 1) % len(spinner)
					a.refreshContextBar()
				})
			case <-stop:
				return
			}
		}
	}()
}

func (a *App) stopSpinner() {
	if a.spinStop != nil {
		close(a.spinStop)
		a.spinStop = nil
	}
}

func (a *App) globalKeys(event *tcell.EventKey) *tcell.EventKey {
	if event.Key() == tcell.KeyCtrlC {
		a.tv.Stop()
		return nil
	}
	// Alt-letter shortcuts for panel display (informational only, focus stays on input).
	if event.Modifiers()&tcell.ModAlt != 0 {
		switch unicode.ToLower(event.Rune()) {
		case 'a':
			a.showPanel("activity")
			return nil
		case 't':
			a.showPanel("tasks")
			return nil
		case 'c':
			a.showPanel("canvas")
			return nil
		case 'i':
			a.setReasoningSplash()
			return nil
		case 'f':
			a.toggleFullscreen()
			return nil
		}
	}
	if event.Key() == tcell.KeyCtrlA {
		a.openAgentModal()
		return nil
	}
	if event.Key() == tcell.KeyCtrlT {
		a.openToolModal()
		return nil
	}
	if event.Key() == tcell.KeyCtrlP || event.Key() == tcell.KeyF2 {
		a.openModelModal()
		return nil
	}
	if event.Key() == tcell.KeyCtrlL {
		a.clearSession()
		return nil
	}
	if event.Key() == tcell.KeyPgUp || event.Key() == tcell.KeyCtrlU {
		a.scrollTranscript(-12)
		return nil
	}
	if event.Key() == tcell.KeyPgDn || event.Key() == tcell.KeyCtrlD {
		a.scrollTranscript(12)
		return nil
	}
	return event
}

func (a *App) handleSlashCommand(cmd string) {
	parts := strings.Fields(strings.TrimPrefix(cmd, "/"))
	if len(parts) == 0 {
		return
	}
	command := strings.ToLower(parts[0])

	switch command {
	case "agent":
		a.openAgentModal()
	case "about":
		// /about toggles the welcome/About screen (ASCII on the
		// left, intro on the right). Typing anything automatically
		// exits about mode via the input change handler.
		a.setAboutMode(!a.aboutMode)
	case "tools":
		a.openToolModal()
	case "model":
		a.openModelModal()
	case "scroll":
		// /scroll [up|down] [lines] — scroll transcript
		dir := "down"
		lines := 12
		if len(parts) > 1 {
			dir = strings.ToLower(parts[1])
		}
		if len(parts) > 2 {
			if n, err := strconv.Atoi(parts[2]); err == nil {
				lines = n
			}
		}
		if dir == "up" {
			a.scrollTranscript(-lines)
		} else {
			a.scrollTranscript(lines)
		}
		a.appendActivity(fmt.Sprintf("Scrolled %s by %d lines", dir, lines))
	case "clear":
		a.clearSession()
	case "compact":
		a.compactHistory(parts)
	case "quit":
		a.tv.Stop()
	case "agentinfo":
		a.openAgentInfoModal()
	case "trigger", "event":
		a.openTriggerModal()
	case "reminder":
		a.openReminderModal()
	case "task":
		a.openTaskModal()
	case "memory", "memories":
		a.openMemoryModal()
	case "config":
		a.openConfigModal()
	case "environment", "env":
		a.openEnvironmentModal()
	case "tasks":
		a.showPanel("tasks")
	case "activity":
		a.showPanel("activity")
	case "test":
		a.showPanel("test")
	case "articles":
		a.showPanel("articles")
	case "canvas", "code", "reference":
		// /canvas [path] [start[-end]] — open a file (optionally a
		// line range) in the canvas panel. With no path it shows the
		// current canvas (or placeholder).
		if len(parts) > 1 {
			start, end := 0, 0
			if len(parts) > 2 {
				start, end = parseLineRange(parts[2])
			}
			a.openCanvas(parts[1], start, end)
		} else {
			a.showPanel("canvas")
		}
	case "mcp":
		a.handleMCPCommand(parts)
	default:
		a.appendActivity(fmt.Sprintf("Unknown command: /%s. Try /agent, /tools, /model, /config, /environment, /scroll, /clear, /quit, /task, /memory, /tasks", command))
	}
}

// InvokeCLICommand implements the tools.CLICommandSink interface,
// allowing tools to proactively invoke slash commands on behalf of the agent.
// This is called from a background goroutine (the runner), so we queue the
// command invocation through the event loop for thread safety.
func (a *App) InvokeCLICommand(command string) error {
	// Queue the command handler in the TUI event loop for thread safety.
	// This ensures the command is executed in the correct context without
	// race conditions or deadlocks.
	a.tv.QueueUpdateDraw(func() {
		a.handleSlashCommand("/" + command)
	})
	return nil
}

func (a *App) openTriggerModal() {
	form := tview.NewForm()
	a.styleModalForm(form)
	var name, payload string
	form.AddInputField("Event name", "", 20, nil, func(s string) { name = s })
	form.AddInputField("Payload (optional)", "", 40, nil, func(s string) { payload = s })
	form.AddButton("Trigger", func() {
		a.appendActivity(fmt.Sprintf("Triggered event: %s payload=%s", name, payload))
		a.closeModal()
	})
	form.AddButton("Cancel", func() { a.closeModal() })
	a.showModal("Trigger Event", form)
}

func (a *App) openReminderModal() {
	form := tview.NewForm()
	a.styleModalForm(form)
	var cronExpr, message string
	var install bool
	form.AddInputField("Cron (5 fields)", "", 20, nil, func(s string) { cronExpr = s })
	form.AddInputField("Message", "", 60, nil, func(s string) { message = s })
	form.AddCheckbox("Install to crontab", false, func(checked bool) { install = checked })
	form.AddButton("Save", func() {
		// Call reminder tool asynchronously could be added here.
		a.appendActivity(fmt.Sprintf("Scheduled reminder: %s -> %s (install=%v)", cronExpr, message, install))
		a.closeModal()
	})
	form.AddButton("Cancel", func() { a.closeModal() })
	a.showModal("Set Reminder", form)
}

func (a *App) openTaskModal() {
	form := tview.NewForm()
	a.styleModalForm(form)
	var title, due string
	form.AddInputField("Title", "", 40, nil, func(s string) { title = s })
	form.AddInputField("Due (optional)", "", 20, nil, func(s string) { due = s })
	form.AddButton("Create", func() {
		result, err := tasks.NewStore().Create(a.sessionState, tasks.CreateInput{Title: title, Owner: "user", Meta: map[string]any{"due": due}})
		if err != nil {
			a.appendActivity(fmt.Sprintf("Task creation failed: %v", err))
			return
		}
		a.sessionState = result.State
		a.saveSession()
		a.appendActivity(fmt.Sprintf("Created task: %s due=%s", title, due))
		a.closeModal()
	})
	form.AddButton("Cancel", func() { a.closeModal() })
	a.showModal("New Task", form)
}

func (a *App) openMemoryModal() {
	form := tview.NewForm()
	a.styleModalForm(form)
	var title, body, tags string
	form.AddInputField("Title", "", 40, nil, func(s string) { title = s })
	form.AddInputField("Body", "", 60, nil, func(s string) { body = s })
	form.AddInputField("Tags (comma separated)", "", 30, nil, func(s string) { tags = s })
	form.AddButton("Save", func() {
		parsedTags := []string{}
		for _, part := range strings.Split(tags, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				parsedTags = append(parsedTags, part)
			}
		}
		result, err := memories.NewStore().Create(a.sessionState, memories.CreateInput{Title: title, Body: body, Tags: parsedTags, Source: "local"})
		if err != nil {
			a.appendActivity(fmt.Sprintf("Memory creation failed: %v", err))
			return
		}
		a.sessionState = result.State
		a.saveSession()
		a.appendActivity(fmt.Sprintf("Stored memory: %s", title))
		a.closeModal()
	})
	form.AddButton("Cancel", func() { a.closeModal() })
	a.showModal("New Memory", form)
}

// openConfigModal shows a small JSON editor preloaded with the
// current AppConfig, plus a read-only defaults panel showing the
// live runtime settings (agent, model, provider, host, etc.).
// The user can edit values in place and hit "Save" to persist;
// "Cancel" discards changes. Validation errors are surfaced in the
// activity log rather than as a popup so the editor stays focused
// for quick corrections.
func (a *App) openConfigModal() {
	bg := a.palette.BgInput

	// Build a read-only info panel showing live runtime defaults.
	infoView := tview.NewTextView().SetDynamicColors(true)
	infoView.SetBackgroundColor(bg)
	infoView.SetTextColor(a.palette.TextDim)
	infoView.SetBorder(true)
	infoView.SetTitle(" Current Defaults ")
	infoView.SetBorderColor(a.palette.TextDim)

	agentName := a.currentAgent.Name
	if agentName == "" {
		agentName = "coder"
	}
	modelName := a.currentModel
	if modelName == "" {
		modelName = "(agent default)"
	}
	compactModel := a.config.CompactModel
	if compactModel == "" {
		compactModel = "(uses current model)"
	}
	providerURL := a.provider.BaseURL()

	infoText := fmt.Sprintf(
		" [%s]agent[-]   [%s::b]%s[-:-:-]\n"+
			" [%s]model[-]   [%s::b]%s[-:-:-]\n"+
			" [%s]compact[-] [%s::b]%s[-:-:-]\n"+
			" [%s]provider[-] [%s::b]%s[-:-:-]\n"+
			" [%s]toolMax[-] [%s::b]%d[-:-:-]\n"+
			" [%s]path[-]   [%s]%s[-]",
		a.palette.HexDim, a.palette.HexLavender, agentName,
		a.palette.HexDim, a.palette.HexLavender, modelName,
		a.palette.HexDim, a.palette.HexLavender, compactModel,
		a.palette.HexDim, a.palette.HexLavender, providerURL,
		a.palette.HexDim, a.palette.HexLavender, a.config.ToolMax,
		a.palette.HexDim, a.palette.HexDim, a.configPath(),
	)
	infoView.SetText(infoText)

	// A working copy of the config, mutated live by the form widgets
	// below. This decouples the form from a.config so the user can
	// cancel without touching the live values, and lets us save the
	// whole struct at once without re-reading individual fields.
	work := a.config

	form := tview.NewForm()
	a.styleModalForm(form)
	form.SetItemPadding(1)

	// --- Agent ---
	agentNames := make([]string, 0, len(a.agents))
	agentIdx := 0
	for i, cfg := range a.agents {
		agentNames = append(agentNames, cfg.Name)
		if cfg.Name == work.Agent {
			agentIdx = i
		}
	}
	agentNames = append(agentNames, "(use first agent)")
	form.AddDropDown("Agent (default)", agentNames, agentIdx, func(option string, _ int) {
		if option == "(use first agent)" {
			work.Agent = ""
		} else {
			work.Agent = option
		}
	})

	// --- Model ---
	modelNames := make([]string, 0, len(a.models)+1)
	modelIdx := 0
	modelNames = append(modelNames, "(agent default)")
	for i, m := range a.models {
		label := m.Name
		if m.ParameterSize != "" {
			label = fmt.Sprintf("%s (%s)", m.Name, m.ParameterSize)
		}
		modelNames = append(modelNames, label)
		if m.Name == work.Model {
			modelIdx = i + 1
		}
	}
	form.AddDropDown("Model (default)", modelNames, modelIdx, func(option string, _ int) {
		// The dropdown shows "name (size)" but the config stores the
		// bare name. Strip any " (…)" suffix before persisting.
		if option == "(agent default)" {
			work.Model = ""
			return
		}
		if idx := strings.Index(option, " ("); idx > 0 {
			option = option[:idx]
		}
		work.Model = option
	})

	// --- Compact model ---
	compactNames := make([]string, 0, len(a.models)+1)
	compactIdx := 0
	compactNames = append(compactNames, "(use current model)")
	for i, m := range a.models {
		compactNames = append(compactNames, m.Name)
		if m.Name == work.CompactModel {
			compactIdx = i + 1
		}
	}
	form.AddDropDown("Compact model", compactNames, compactIdx, func(option string, _ int) {
		if option == "(use current model)" {
			work.CompactModel = ""
		} else {
			work.CompactModel = option
		}
	})

	// --- Tool step cap ---
	form.AddInputField("Tool steps per turn", strconv.Itoa(work.ToolMax), 6,
		func(textToCheck string, _ rune) bool {
			n, err := strconv.Atoi(textToCheck)
			return err == nil && n > 0
		},
		func(text string) {
			if n, err := strconv.Atoi(text); err == nil && n > 0 {
				work.ToolMax = n
			}
		})

	// --- User bubble width ---
	form.AddInputField("User bubble width", strconv.Itoa(work.UserMessageMaxWidth), 6,
		func(textToCheck string, _ rune) bool {
			if textToCheck == "" {
				return true
			}
			n, err := strconv.Atoi(textToCheck)
			return err == nil && n >= 0
		},
		func(text string) {
			if text == "" {
				work.UserMessageMaxWidth = 0
				return
			}
			if n, err := strconv.Atoi(text); err == nil && n >= 0 {
				work.UserMessageMaxWidth = n
			}
		})

	// --- Actions ---
	save := func() {
		if err := a.saveConfig(work); err != nil {
			a.appendActivity(fmt.Sprintf("["+a.palette.HexOrchid+"::b]config save failed[-:-:-]: %v", err))
			return
		}
		a.config = work
		a.runner.MaxSteps = work.ToolMax
		a.appendActivity(fmt.Sprintf("Saved config to %s (toolMax=%d, compactModel=%s)", a.configPath(), work.ToolMax, work.CompactModel))
		a.closeModal()
	}
	form.AddButton("Save", save)
	form.AddButton("Cancel", func() { a.closeModal() })

	help := tview.NewTextView().SetDynamicColors(true)
	help.SetBackgroundColor(bg)
	help.SetTextColor(a.palette.TextDim)
	help.SetText(fmt.Sprintf(
		" [%s]tab[-] next field · [%s]enter[-] edit dropdown · [%s]0 bubble width[-] = auto (pane width)",
		a.palette.HexLavender, a.palette.HexLavender, a.palette.HexLavender))

	body := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(infoView, 7, 0, false).
		AddItem(spacerBox(bg), 1, 0, false).
		AddItem(form, 0, 1, true).
		AddItem(spacerBox(bg), 1, 0, false).
		AddItem(help, 1, 0, false)
	body.SetBackgroundColor(bg)

	a.showModal("Config", body)
}

// buildEnvironmentInfo gathers live runtime context for template
// interpolation, including the currently selected model and agent.
func (a *App) buildEnvironmentInfo() ctxpkg.Info {
	info := ctxpkg.Build(os.Args[0], a.appVersion, a.workspaceRoot)
	info.Model = a.currentModel
	info.Agent = a.currentAgent.Name
	return info
}

// renderedEnvironment returns the interpolated environment block to
// prepend to the agent system prompt, or "" when the user has cleared
// the template.
func (a *App) renderedEnvironment() string {
	tmpl := ctxpkg.LoadTemplate(a.workspaceRoot)
	return strings.TrimSpace(a.buildEnvironmentInfo().Render(tmpl))
}

// openEnvironmentModal shows an in-pane editor for the environment
// template injected into the agent's system prompt. The template
// supports {{token}} interpolation (cwd, shell, os, model, agent, …)
// resolved live at each turn. Ctrl+S saves, Esc cancels.
func (a *App) openEnvironmentModal() {
	bg := a.palette.BgInput
	editor := tview.NewTextArea()
	editor.SetBackgroundColor(bg)
	editor.SetText(ctxpkg.LoadTemplate(a.workspaceRoot), true)

	preview := tview.NewTextView().SetDynamicColors(true).SetWrap(true)
	preview.SetBackgroundColor(bg)
	preview.SetTextColor(a.palette.TextDim)
	renderPreview := func() {
		out := a.buildEnvironmentInfo().Render(editor.GetText())
		preview.SetText(fmt.Sprintf("[%s]preview (interpolated):[-]\n%s", a.palette.HexFaint, out))
	}
	renderPreview()

	hint := tview.NewTextView().SetDynamicColors(true)
	hint.SetBackgroundColor(bg)
	hint.SetTextColor(a.palette.TextDim)
	hint.SetText(fmt.Sprintf(" [%s]%s[-]   [%s]ctrl+s[-] save · [%s]esc[-] cancel",
		a.palette.HexFaint, ctxpkg.TemplatePath(a.workspaceRoot), a.palette.HexLavender, a.palette.HexLavender))

	help := tview.NewTextView().SetDynamicColors(true)
	help.SetBackgroundColor(bg)
	help.SetTextColor(a.palette.TextDim)
	help.SetText(fmt.Sprintf(" [%s]tokens:[-] {{cwd}} {{workspace}} {{shell}} {{os}} {{arch}} {{user}} {{hostname}} {{time}} {{timezone}} {{home}} {{locale}} {{model}} {{agent}} {{cli_path}} {{cli_version}}   [%s]expr:[-] {{$(command)}}",
		a.palette.HexFaint, a.palette.HexFaint))

	closeEnv := func() { a.closeModal() }
	save := func() {
		if err := ctxpkg.SaveTemplate(a.workspaceRoot, editor.GetText()); err != nil {
			a.appendActivity(fmt.Sprintf("["+a.palette.HexOrchid+"::b]environment save failed[-:-:-]: %v", err))
			return
		}
		a.appendActivity(fmt.Sprintf("Saved environment template to %s", ctxpkg.TemplatePath(a.workspaceRoot)))
		closeEnv()
	}

	editor.SetChangedFunc(renderPreview)

	body := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(hint, 1, 0, false).
		AddItem(help, 2, 0, false).
		AddItem(editor, 0, 2, true).
		AddItem(preview, 0, 1, false)
	body.SetBackgroundColor(bg)

	editor.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyEsc:
			closeEnv()
			return nil
		case tcell.KeyCtrlS:
			save()
			return nil
		}
		return event
	})

	a.showModal("Environment", body)
}

func (a *App) openAgentInfoModal() {
	bg := a.palette.BgInput
	body := tview.NewTextView().SetDynamicColors(true).SetWrap(true).SetScrollable(true)
	body.SetBackgroundColor(bg)
	body.SetTextColor(a.palette.TextMain)
	prompt := strings.TrimSpace(a.currentAgent.Prompt)
	if prompt == "" {
		prompt = "<no system prompt available>"
	}
	p := a.palette.HexPurple
	body.SetText(fmt.Sprintf("[%s]Agent:[-] %s\n[%s]Title:[-] %s\n[%s]Default model:[-] %s\n[%s]Tools:[-] %s\n\n[%s]System prompt:[-]\n%s\n\n[%s]esc[-] close",
		p, a.currentAgent.Name,
		p, a.currentAgent.Title,
		p, a.currentAgent.DefaultModel,
		p, strings.Join(a.currentAgent.ToolNames, ", "),
		p, prompt,
		a.palette.HexLavender,
	))
	body.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyEsc {
			a.closeModal()
			return nil
		}
		return event
	})
	a.showModal("Agent Info", body)
}

// setActivePanel records which right-column body is currently
// showing so the panel shortcuts can highlight the active entry.
func (a *App) setActivePanel(name string) {
	a.activePanel = name
}

// toggleFullscreen hides the right column so the transcript can use
// the full terminal width. The current panel state is preserved and
// restored when fullscreen is toggled back off.
func (a *App) toggleFullscreen() {
	a.fullscreen = !a.fullscreen
	a.appendActivity(fmt.Sprintf("[%s]fullscreen[-] %s", a.palette.HexDim, map[bool]string{true: "on (right column hidden)", false: "off"}[a.fullscreen]))
	a.rebuildLayout()
	a.refreshHeaderRight()
}

// runCognitionRecap spawns a non-interactive `coder` agent in the
// background, asks it to summarize the most recent transcript and
// session state, and pipes the result into the Cognition pane. It
// is safe to call multiple times; subsequent invocations are no-ops
// because the firstRun flag flips off on first use.
func (a *App) runCognitionRecap() {
	defer func() { a.firstRun = false }()
	var prompt string
	if len(a.history) == 0 {
		prompt = fmt.Sprintf("Please review recent messages and output a one paragraph recap of the discussion which will be appended to the cognition pane of the tj coder CLI. The user has not yet sent a message; produce a brief welcome that summarises the current session context and what kinds of tasks the user might want help with. Use exactly these session facts and do not invent others: workspace=%q, model=%q, agent=%q.", a.workspaceRoot, a.currentModel, a.currentAgent.Name)
	} else {
		prompt = fmt.Sprintf("Please review recent messages and output a one paragraph recap of the discussion which will be appended to the cognition pane of the tj coder CLI. If you reference the active model or agent, use exactly these session facts and do not invent others: model=%q, agent=%q.", a.currentModel, a.currentAgent.Name)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Use the internal runner to avoid the overhead and risks of spawning a subprocess.
	// We disable tools for the recap to ensure it's a plain text summary.
	newHistory, err := a.runner.Run(ctx, a.history, prompt, a.currentAgent, a.currentModel, nil, nil)
	if err != nil {
		a.tv.QueueUpdateDraw(func() {
			fmt.Fprintf(a.reasoning, "\n[%s::b]cognition recap failed[-:-:-] %v[-]\n", a.palette.HexOrchid, err)
			a.reasoning.ScrollToEnd()
		})
		return
	}

	if len(newHistory) == 0 {
		return
	}
	recapped := newHistory[len(newHistory)-1].Content
	if recapped == "" {
		return
	}
	recapped = strings.TrimSpace(recapped)

	a.tv.QueueUpdateDraw(func() {
		fmt.Fprintf(a.reasoning, "\n\n%s\n", a.highlightTranscriptText(recapped))
		a.reasoning.ScrollToEnd()
	})
}

// showPanel switches the activity panel between 'activity', 'tasks', and 'articles'.
// Panels are display-only; focus always remains on the input field so the user
// can continue typing commands. Use Alt+[letter] to switch panels without losing input focus.
func (a *App) showPanel(name string) {
	name = strings.ToLower(strings.TrimSpace(name))
	switch name {
	case "tasks":
		a.refreshTasksList()
		a.setActivePanel("tasks")
		a.rebuildLayout()
		a.focusInput()
		return
	case "memories":
		a.refreshMemoriesList()
		a.setActivePanel("memories")
		a.rebuildLayout()
		a.focusInput()
		return
	case "articles":
		a.setActivePanel("articles")
		a.setActivityTitle("ARTICLES", "")
		a.activity.SetText("[articles] Not implemented yet\n")
		a.rebuildLayout()
		a.focusInput()
		return
	case "canvas", "code", "reference":
		a.setActivePanel("canvas")
		a.refreshCanvasPanel()
		a.rebuildLayout()
		a.focusInput()
		return
	case "test":
		a.setActivePanel("test")
		a.testView.SetText("Test Pane: Active\n\nThis is an empty test pane used for troubleshooting hangs.")
		a.rebuildLayout()
		a.focusInput()
		return
	default:
		a.setActivePanel("activity")
		a.setActivityTitle("ACTIVITY", "")
		if state, ok, err := session.Load(a.workspaceRoot); err == nil && ok {
			if state.Activity != "" {
				a.activity.SetText(state.Activity)
			}
		}
		a.rebuildLayout()
		a.focusInput()
	}
}

// refreshTasksList rebuilds the interactive tasks list from the
// current session state. Each task is rendered with its status
// glyph and (when done) a struck-through, dimmed title. The user
// navigates with Up/Down and presses Enter to toggle the task
// between done and its previous open status.
func (a *App) focusInput() {
	if a.tv != nil && a.input != nil {
		a.tv.SetFocus(a.input)
	}
}

func (a *App) refreshTasksList() {
	if a.tasksList == nil {
		return
	}
	a.tasksList.Clear()
	list := tasks.Load(a.sessionState)
	if list.Len() == 0 {
		// No tasks: render an empty-state hint as a single
		// non-interactive entry so the panel still shows something
		// useful.
		a.tasksList.AddItem(
			fmt.Sprintf("[%s]No tasks yet.[-] [%s]Use /task <title> to add one.[-]",
				a.palette.HexDim, a.palette.HexFaint),
			"", 0, nil,
		)
		return
	}
	for _, t := range list.All() {
		task := t
		line := a.formatTaskLine(task)
		a.tasksList.AddItem(line, "", 0, func() {
			a.toggleTask(task.ID)
		})
	}
}

// refreshMemoriesList rebuilds the interactive memories list from the
// current session state. Each memory is rendered with its title as the
// main line and body/tags as secondary text. The user navigates with
// Up/Down and presses Delete to remove a memory.
func (a *App) refreshMemoriesList() {
	if a.memoriesList == nil {
		return
	}
	a.memoriesList.Clear()
	list := memories.Load(a.sessionState)
	if list.Len() == 0 {
		a.memoriesList.AddItem(
			fmt.Sprintf("[%s]No memories yet.[-] [%s]Use /memory to add one.[-]",
				a.palette.HexDim, a.palette.HexFaint),
			"", 0, nil,
		)
		return
	}
	for _, m := range list.All() {
		mem := m
		secondary := ""
		if mem.Body != "" {
			secondary = mem.Body
		}
		if len(mem.Tags) > 0 {
			if secondary != "" {
				secondary += " · "
			}
			secondary += "tags: " + strings.Join(mem.Tags, ", ")
		}
		a.memoriesList.AddItem(mem.Title, secondary, 0, func() {
			a.deleteSelectedMemory()
		})
	}
}

// deleteSelectedMemory removes the currently highlighted memory from
// the session and refreshes the list in place.
func (a *App) deleteSelectedMemory() {
	if a.memoriesList == nil {
		return
	}
	idx := a.memoriesList.GetCurrentItem()
	list := memories.Load(a.sessionState)
	items := list.All()
	if idx < 0 || idx >= len(items) {
		return
	}
	mem := items[idx]
	newState, removed := memories.NewStore().Delete(a.sessionState, mem.ID)
	if !removed {
		return
	}
	a.sessionState = newState
	a.saveSession()
	a.appendActivity(fmt.Sprintf("Removed memory: %s", mem.Title))
	a.refreshMemoriesList()
}

// formatTaskLine returns the single-line display string for a task,
// including its status glyph and (for done tasks) a struck-through,
// dimmed title.
func (a *App) formatTaskLine(t tasks.Task) string {
	glyph := tasks.GlyphFor(t.Status)
	title := t.Title
	if tasks.Status(t.Status) == tasks.StatusDone {
		// Strike through the title using Unicode combining chars so
		// the dimming + strikethrough combination reads as
		// "checked off" at a glance.
		title = strikeThrough(title)
		return fmt.Sprintf("%s [%s]%s[-]", glyph, a.palette.HexFaint, title)
	}
	// Open tasks: keep the title in the main text color and use the
	// dim tint for the status glyph's surrounding bullet so the
	// user can scan the list quickly.
	_ = title
	return fmt.Sprintf("%s [%s]%s[-]", glyph, a.palette.TextMain, t.Title)
}

// strikeThrough returns s with Unicode combining long stroke overlay
// characters appended to each rune so terminals render the text as
// strikethrough. We avoid modifying whitespace runs to keep the
// output compact and grep-friendly.
func strikeThrough(s string) string {
	if s == "" {
		return s
	}
	const combining = "\u0336"
	var b strings.Builder
	b.Grow(len(s) * 2)
	for _, r := range s {
		b.WriteRune(r)
		if r == ' ' || r == '\t' {
			continue
		}
		b.WriteString(combining)
	}
	return b.String()
}

// toggleTask flips a task between done and its previous open
// status, persists the change, and re-renders the list so the
// glyph + strikethrough update in place. The activity log records
// a single concise line per toggle. This function is called from
// the tasksList item callback, which is already executing in the
// tview event loop, so list mutations are safe to perform directly
// without QueueUpdateDraw.
func (a *App) toggleTask(id string) {
	// Capture the cursor's numeric position *before* mutating the
	// list, so we can restore it after the refresh without
	// depending on a fragile string match against freshly
	// formatted (possibly strikethrough) line text.
	prevIndex := -1
	if a.tasksList != nil {
		prevIndex = a.tasksList.GetCurrentItem()
	}
	store := tasks.NewStore()
	state, t, ok, err := store.ToggleDone(a.sessionState, id)
	if err != nil {
		a.appendActivity(fmt.Sprintf("[%s]toggle failed[-]: %v", a.palette.HexOrchid, err))
		return
	}
	if !ok {
		return
	}
	a.sessionState = state
	a.saveSession()
	verb := "checked"
	if tasks.Status(t.Status) == tasks.StatusDone {
		verb = "checked"
	} else {
		verb = "reopened"
	}
	a.appendActivity(fmt.Sprintf("[%s]%s[-] %s", a.palette.HexLavender, verb, t.Title))
	// Perform list mutations directly since we're already in the event loop
	// via the item callback. No QueueUpdateDraw needed here.
	a.refreshTasksList()
	if a.tasksList == nil {
		return
	}
	count := a.tasksList.GetItemCount()
	if count == 0 {
		return
	}
	next := prevIndex
	if next < 0 {
		next = 0
	}
	if next >= count {
		next = count - 1
	}
	a.tasksList.SetCurrentItem(next)
}

// setActivityTitle updates the right-column body label (the row that
// normally reads ACTIVITY). The canvas view retitles it CANVAS, with an
// optional dim suffix (e.g. the file path and range).
func (a *App) setActivityTitle(title, suffix string) {
	if a.activityTitle == nil {
		return
	}
	text := fmt.Sprintf(" [%s]%s[-]", a.palette.HexPurple, strings.ToUpper(title))
	if suffix != "" {
		text += fmt.Sprintf("  [%s]%s[-]", a.palette.HexDim, tview.Escape(suffix))
	}
	a.activityTitle.SetText(text)
}

// refreshCanvasPanel renders the canvas view into the right-column body.
// When no file is loaded it shows a short placeholder; otherwise it
// renders a.canvasPath (optionally scrolled to a.canvasStart..canvasEnd)
// with line numbers and a highlighted range. The panel label row is
// retitled CANVAS (with the path as a dim suffix) instead of an inner
// header line.
func (a *App) refreshCanvasPanel() {
	if a.activity == nil {
		return
	}
	if strings.TrimSpace(a.canvasPath) == "" {
		a.setActivityTitle("CANVAS", "")
		a.activity.SetText(fmt.Sprintf(
			"[%s]Files the agent reads or drafts appear here. The agent uses ui_control (panel=canvas, path, start_line, end_line) to open a file and range. Use [%s]/canvas <path> [start[-end]][-] or [%s]Alt+C[-] to open one yourself.[-]",
			a.palette.HexLavender, a.palette.HexLavender, a.palette.HexLavender,
		))
		return
	}
	rangeLabel := ""
	if a.canvasStart > 0 {
		if a.canvasEnd > a.canvasStart {
			rangeLabel = fmt.Sprintf(":%d-%d", a.canvasStart, a.canvasEnd)
		} else {
			rangeLabel = fmt.Sprintf(":%d", a.canvasStart)
		}
	}
	a.setActivityTitle("CANVAS", a.canvasPath+rangeLabel)
	a.activity.SetText(a.renderCanvasBody(a.canvasPath, a.canvasStart, a.canvasEnd))
	a.activity.ScrollTo(a.canvasScrollTo(a.canvasStart), 0)
}

// canvasScrollTo returns the body row the canvas should scroll to so
// the highlighted range is comfortably in view (a couple of lines of lead-in).
func (a *App) canvasScrollTo(start int) int {
	if start <= 3 {
		return 0
	}
	return start - 3
}

// renderCanvasBody reads path and returns a tview-markup string with a
// header, line numbers, and the [start,end] range highlighted. Reads are
// capped so an accidental huge file cannot stall the UI.
func (a *App) renderCanvasBody(path string, start, end int) string {
	full := path
	if !filepath.IsAbs(full) {
		full = filepath.Join(a.workspaceRoot, path)
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return fmt.Sprintf("[%s]could not open %s: %v[-]",
			a.palette.HexOrchid, tview.Escape(path), err)
	}

	const maxBytes = 512 * 1024
	truncated := false
	if len(data) > maxBytes {
		data = data[:maxBytes]
		truncated = true
	}

	lines := strings.Split(string(data), "\n")
	const maxLines = 2000
	if len(lines) > maxLines {
		lines = lines[:maxLines]
		truncated = true
	}

	var b strings.Builder
	width := len(strconv.Itoa(len(lines)))
	for i, ln := range lines {
		n := i + 1
		gutter := fmt.Sprintf("[%s]%*d[-]", a.palette.HexDim, width, n)
		text := tview.Escape(ln)
		if start > 0 && n >= start && n <= max(start, end) {
			fmt.Fprintf(&b, "%s [%s]│[-] [%s]%s[-]\n", gutter, a.palette.HexPurple, a.palette.HexLavender, text)
		} else {
			fmt.Fprintf(&b, "%s [%s]│[-] %s\n", gutter, a.palette.HexDim, text)
		}
	}
	if truncated {
		fmt.Fprintf(&b, "\n[%s]… output truncated[-]\n", a.palette.HexDim)
	}
	return b.String()
}

// parseLineRange parses "12", "12-40" or "12:40" into (start, end).
// A bare number yields end==0 (single-line highlight); invalid input
// yields (0, 0).
func parseLineRange(s string) (int, int) {
	s = strings.TrimSpace(s)
	sep := strings.IndexAny(s, "-:")
	if sep < 0 {
		n, _ := strconv.Atoi(s)
		return n, 0
	}
	start, _ := strconv.Atoi(strings.TrimSpace(s[:sep]))
	end, _ := strconv.Atoi(strings.TrimSpace(s[sep+1:]))
	return start, end
}

// openCanvas loads a file (and optional 1-based line range) into the canvas
// panel and makes it the active right-hand panel. Callers on background
// goroutines must wrap this via QueueUpdateDraw.
func (a *App) openCanvas(path string, start, end int) {
	a.canvasPath = strings.TrimSpace(path)
	a.canvasStart = start
	a.canvasEnd = end
	a.showPanel("canvas")
}

// reloadTasksIfChanged checks if tasks were modified (e.g., by the agent),
// reloads them from the saved session, and refreshes the pane if they changed.
func (a *App) reloadTasksIfChanged() {
	// Load the current session state from disk
	saved, exists, err := session.Load(a.workspaceRoot)
	if err != nil || !exists {
		return
	}

	// Simple comparison: if task count differs, tasks changed
	if len(saved.Tasks) != len(a.sessionState.Tasks) {
		a.sessionState.Tasks = append([]session.Task(nil), saved.Tasks...)
		// Only refresh if the pane is currently visible
		if a.activePanel == "tasks" && a.tasksList != nil {
			a.refreshTasksList()
		}
		return
	}

	// Check if any task content changed
	for i, t := range saved.Tasks {
		if i >= len(a.sessionState.Tasks) ||
			t.ID != a.sessionState.Tasks[i].ID ||
			t.Title != a.sessionState.Tasks[i].Title ||
			t.Status != a.sessionState.Tasks[i].Status {
			a.sessionState.Tasks = append([]session.Task(nil), saved.Tasks...)
			// Only refresh if the pane is currently visible
			if a.activePanel == "tasks" && a.tasksList != nil {
				a.refreshTasksList()
			}
			return
		}
	}
}

// renderTasks loads session state and formats tasks for display
func (a *App) renderTasks(maxLines int) string {
	list := tasks.Load(a.sessionState)
	return tasks.FormatPromptBlock(list, maxLines)
}

func (a *App) clearSession() {
	// Reset UI panels to default state
	a.showPanel("activity")
	a.history = nil
	a.transcript.Clear()
	a.reasoning.Clear()
	a.activity.Clear()
	a.contextInfo = "ctx: unavailable"
	a.refSet = map[string]struct{}{}
	a.refOrder = nil
	a.setTranscriptSplash()
	a.setReasoningSplash()
	a.refreshContextBar()
	a.appendActivity("Session cleared.")
	a.saveSession()
}

// compactHistory summarizes the current transcript to free context
// space. The model is asked to produce a structured summary of every
// user request, every assistant decision, and any code/file changes;
// the in-memory history is then replaced with a small "compaction
// marker" pair so subsequent turns see a compact but complete
// narrative.
//
// If the transcript is already short, the operation is a no-op with
// an activity log notice. If the summarization call fails, history
// is left untouched so the user does not lose context.
// compactHistory summarizes the current transcript to free context
// space. The model is asked to produce a structured summary of every
// user request, every assistant decision, and any code/file changes;
// the in-memory history is then replaced with a small "compaction
// marker" pair so subsequent turns see a compact but complete
// narrative.
//
// The compaction runs asynchronously in a goroutine so the event
// loop is never blocked. While the model is working, an indeterminate
// progress indicator ("compacting…") is shown in the transcript and
// the context-bar spinner animates. When the summary returns, the
// transcript is updated with the result and the context bar is
// refreshed to reflect the freed tokens.
//
// /compact [model] optionally specifies the model used for the
// compaction; it defaults to the current model.
func (a *App) compactHistory(parts []string) {
	if len(a.history) < 4 {
		a.appendActivity(fmt.Sprintf("[%s]compact[-]: nothing to compact (history has %d messages)", a.palette.HexFaint, len(a.history)))
		return
	}

	model := a.currentModel
	// Honor the CompactModel config override if set — lets the user
	// use a cheaper / faster model for summarization while keeping
	// a more capable model for the main conversation.
	if a.config.CompactModel != "" {
		model = a.config.CompactModel
	}
	if len(parts) > 1 {
		model = parts[1]
	}

	preCount := len(a.history)
	preChars := 0
	for _, m := range a.history {
		preChars += len(m.Content)
	}

	prompt := strings.Join([]string{
		"Please produce a structured, compact summary of our entire conversation above. The summary will replace the prior messages in the transcript, so it must be sufficient to continue the work without re-asking any question I have already answered.",
		"",
		"Format the summary as Markdown with these sections:",
		"- **Goal**: the user's overarching objective in one or two sentences",
		"- **Decisions**: numbered list of every concrete decision the assistant made, with one-line rationale each",
		"- **Files touched**: list of file paths modified, created, or read, with one-sentence reason each",
		"- **Open questions**: any unresolved question or TODO the user or assistant flagged",
		"- **Next steps**: the most likely next thing the user will ask for",
		"",
		"Be specific (function names, command names, exact paths). Do not include pleasantries or meta-commentary. Do not exceed ~600 words.",
	}, "\n")

	// Show indeterminate progress in the transcript immediately.
	stamp := formatTimestamp(time.Now())
	fmt.Fprintf(a.transcript, "[%s]%s[-]\n", a.palette.HexDim, stamp)
	fmt.Fprintf(a.transcript, "[%s::b]Coder is compacting[-:-:-] [%s]using %s…[-]\n", a.palette.HexPurple, a.palette.HexDim, model)
	a.transcript.ScrollToEnd()

	// Set busy state and start spinner.
	a.mu.Lock()
	a.busy = true
	a.mu.Unlock()
	a.startSpinner()
	a.appendActivity(fmt.Sprintf("[%s]compacting history using model %s…[-] ", a.palette.HexLavender, model))

	// Capture the history snapshot for the goroutine.
	history := append([]client.Message(nil), a.history...)
	agentCfg := a.currentAgent
	if env := a.renderedEnvironment(); env != "" {
		agentCfg.Prompt = env + "\n\n" + agentCfg.Prompt
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		newHistory, err := a.runner.Run(ctx, history, prompt, agentCfg, model, nil, nil)

		a.tv.QueueUpdateDraw(func() {
			a.mu.Lock()
			a.busy = false
			a.mu.Unlock()
			a.stopSpinner()

			if err != nil {
				a.appendActivity(fmt.Sprintf("[%s]compact failed[-]: %v", a.palette.HexOrchid, err))
				fmt.Fprintf(a.transcript, "[%s]compact failed: %v[-]\n", a.palette.HexOrchid, err)
				a.transcript.ScrollToEnd()
				return
			}
			if len(newHistory) <= len(history) {
				a.appendActivity(fmt.Sprintf("[%s]compact failed[-]: runner produced no new messages", a.palette.HexOrchid))
				fmt.Fprintf(a.transcript, "[%s]compact failed: no summary produced[-]\n", a.palette.HexOrchid)
				a.transcript.ScrollToEnd()
				return
			}

			summary := newHistory[len(newHistory)-1].Content
			if strings.TrimSpace(summary) == "" {
				a.appendActivity(fmt.Sprintf("[%s]compact failed[-]: model returned an empty summary", a.palette.HexOrchid))
				fmt.Fprintf(a.transcript, "[%s]compact failed: empty summary[-]\n", a.palette.HexOrchid)
				a.transcript.ScrollToEnd()
				return
			}

			// Replace in-memory history with a two-message marker.
			a.history = []client.Message{
				{Role: "user", Content: "[Compaction] The prior conversation has been summarized to free context. Treat the assistant's summary below as authoritative for any earlier decision, file change, or open question."},
				{Role: "assistant", Content: summary},
			}

			// Persist immediately.
			if err := a.saveSession(); err != nil {
				a.appendActivity(fmt.Sprintf("[%s]compact saved in-memory but session persist failed[-]: %v", a.palette.HexOrchid, err))
			}

			postChars := 0
			for _, m := range a.history {
				postChars += len(m.Content)
			}
			a.appendActivity(fmt.Sprintf("[%s]compacted[-]: %d → %d messages, %d → %d chars",
				a.palette.HexLavender, preCount, len(a.history), preChars, postChars))

			// Update the transcript with the RECAP label (uppercase, primary
			// color, matching the ACTIVITY/CONVERSATION/COGNITION label style)
			// followed by the stats line and the summary body.
			fmt.Fprintf(a.transcript, "\n\n[%s]%s[-]\n", a.palette.HexDim, formatTimestamp(time.Now()))
			fmt.Fprintf(a.transcript, "[%s::b]RECAP[-:-:-]\n", a.palette.HexPurple)
			fmt.Fprintf(a.transcript, "[%s]%d → %d messages, %d → %d chars[-]\n\n", a.palette.HexDim, preCount, len(a.history), preChars, postChars)
			fmt.Fprint(a.transcript, a.highlightTranscriptText(summary))
			fmt.Fprint(a.transcript, "\n\n")
			a.transcript.ScrollToEnd()

			// Refresh the context bar to reflect freed tokens.
			a.contextInfo = "ctx: recalculating…"
			a.refreshContextBar()
		})
	}()
}

// navigateInputHistory moves through the global input history in
// response to Up/Down arrow keys. Up (-1) goes to older entries;
// Down (+1) goes to newer, eventually returning to the draft the
// user was typing before they started navigating. The current
// draft is saved when the user first presses Up so pressing Down
// all the way back restores it.
func (a *App) navigateInputHistory(dir int) {
	if len(a.inputHistory) == 0 {
		return
	}

	// On the first Up press, save the current draft text.
	if a.inputHistoryIdx == -1 && dir < 0 {
		a.inputHistoryDraft = a.input.GetText()
	}

	newIdx := a.inputHistoryIdx + dir
	if dir < 0 {
		// Up: clamp at the oldest entry (index 0).
		if newIdx < 0 {
			newIdx = 0
		}
	} else {
		// Down: past the newest entry returns to draft.
		if newIdx >= len(a.inputHistory) {
			newIdx = -1
		}
	}
	a.inputHistoryIdx = newIdx

	if newIdx == -1 {
		a.input.SetText(a.inputHistoryDraft)
	} else {
		a.input.SetText(a.inputHistory[newIdx])
	}
}

// recordInputHistory appends prompt to the global input history
// (both in-memory and on-disk) and resets the navigation index so
// the next Up press starts from the newest entry. The disk write
// is non-blocking: it runs in a goroutine so the event loop is
// never stalled by file I/O.
func (a *App) recordInputHistory(prompt string) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return
	}
	// Update in-memory copy (dedup against last entry).
	if len(a.inputHistory) > 0 && a.inputHistory[len(a.inputHistory)-1] == prompt {
		a.inputHistoryIdx = -1
		return
	}
	a.inputHistory = append(a.inputHistory, prompt)
	a.inputHistoryIdx = -1
	// Persist asynchronously.
	go func() {
		_ = session.AppendInputHistory(prompt)
	}()
}

func (a *App) submit() {
	a.mu.Lock()
	if a.busy {
		// Mid-turn: route the prompt into the runner's steering
		// channel instead of dropping it. Non-blocking send: if the
		// channel buffer is saturated (the runner is mid-drain or
		// the user pasted a wall of text faster than we can keep
		// up), surface a warning in the activity log so the user
		// knows to retry, instead of silently swallowing the input.
		prompt := strings.TrimSpace(a.input.GetText())
		if prompt == "" {
			a.mu.Unlock()
			return
		}
		// Record in global input history (non-blocking best-effort).
		a.recordInputHistory(prompt)
		select {
		case a.steeringCh <- prompt:
			a.appendActivity(fmt.Sprintf("[%s]steering[-]: queued %q for the in-flight turn", a.palette.HexOrchid, truncateForActivity(prompt)))
		default:
			a.appendActivity(fmt.Sprintf("[%s]steering[-]: queue full, message dropped; wait for the current turn to settle and try again", a.palette.HexOrchid))
		}
		a.input.SetText("")
		a.setInputPlaceholder()
		a.refreshContextBar()
		a.mu.Unlock()
		return
	}
	prompt := strings.TrimSpace(a.input.GetText())
	if prompt == "" {
		a.mu.Unlock()
		return
	}

	// Record in global input history (non-blocking best-effort).
	a.recordInputHistory(prompt)

	// Handle slash commands. A multi-line block is treated as a plain
	// message even if it begins with "/" (a pasted snippet shouldn't be
	// misinterpreted as a command), and an exact single-line "/cmd" is
	// routed to the command dispatcher.
	if strings.HasPrefix(prompt, "/") && !strings.Contains(prompt, "\n") {
		a.mu.Unlock()
		a.input.SetText("")
		a.setInputPlaceholder()
		a.handleSlashCommand(prompt)
		return
	}

	a.busy = true
	a.spinIdx = 0
	a.assistantState = "thinking"
	a.assistantStamp = ""
	a.mu.Unlock()

	a.startSpinner()
	a.input.SetText("")
	a.setInputPlaceholder()
	a.refreshFooter()
	a.refreshContextBar()
	a.appendUserMessage(prompt)
	a.appendAssistantTurnLabel()
	a.transcript.ScrollToEnd()
	a.clearReasoningSplash()
	a.cognitionActive = true
	// New cognition entries are appended inline; no per-turn divider is
	// rendered here so the pane does not accumulate blank header lines
	// on a quiet session.
	a.addReferencesFromText(prompt)
	a.refreshContextBar()

	// CRITICAL: Save the session immediately and synchronously after rendering
	// the user message. This must complete before we start the runner goroutine,
	// so that if the user quits immediately, the message is already persisted.
	// We hold the mutex during save to prevent concurrent modifications.
	a.mu.Lock()
	if err := a.saveSession(); err != nil {
		a.mu.Unlock()
		a.appendActivity(fmt.Sprintf("["+a.palette.HexOrchid+"::b]warning[-:-:-]: failed to save session: %v", err))
	} else {
		a.mu.Unlock()
	}

	a.launchTurn(prompt, "")
}

// launchTurn runs one agent turn as a background goroutine against a
// snapshot of the current history, then merges the result back onto
// the UI thread. agentNameOverride selects a different agent persona
// for this turn only (scheduled runs); "" uses the current agent.
//
// History is shared with the live conversation, so whatever the
// scheduled turn does is visible to (and remembered by) the main
// agent on subsequent turns — the user never has to restate context.
func (a *App) launchTurn(prompt, agentNameOverride string) {
	enabled := a.enabledToolList()
	history := append([]client.Message(nil), a.history...)
	agentCfg := a.currentAgent
	if agentNameOverride != "" && agentNameOverride != agentCfg.Name {
		if alt, ok := agent.FindWithWorkspace(agentNameOverride, a.workspaceRoot); ok {
			agentCfg = alt
		} else {
			a.appendActivity(fmt.Sprintf("["+a.palette.HexOrchid+"::b]warning[-:-:-]: schedule agent %q not found; using %s", agentNameOverride, agentCfg.Name))
		}
	}
	// Inject the (interpolated) environment/context block ahead of the
	// agent's own system prompt, mirroring headless mode. Editable via
	// /environment. Empty template => no injection.
	if env := a.renderedEnvironment(); env != "" {
		agentCfg.Prompt = env + "\n\n" + agentCfg.Prompt
	}
	model := a.currentModel
	if agentCfg.DefaultModel != "" && agentNameOverride != "" {
		// A scheduled turn naming a different agent runs on that
		// agent's preferred model rather than whichever model the
		// interactive session happens to have selected.
		model = agentCfg.DefaultModel
	}

	go func() {
		// Use a timeout context to prevent indefinite hangs on API calls or tool execution.
		// Timeout is set to 15 minutes to accommodate long-running tasks while preventing
		// infinite hangs that require shell termination.
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		nextHistory, err := a.runner.Run(ctx, history, prompt, agentCfg, model, enabled, a.handleEvent)
		a.tv.QueueUpdateDraw(func() {
			if err != nil {
				errText := fmt.Sprintf("error: %v", err)
				if isReasoningRelated(errText) {
					fmt.Fprintf(a.reasoning, "\n["+a.palette.HexOrchid+"::b]%s[-:-:-]\n", errText)
					a.reasoning.ScrollToEnd()
				} else {
					a.appendActivity(fmt.Sprintf("["+a.palette.HexOrchid+"::b]error[-:-:-]: %v", err))
					fmt.Fprintf(a.transcript, "\n["+a.palette.HexOrchid+"::b]error[-:-:-]: %v\n", err)
				}
			} else {
				a.assistantState = "replied"
				a.assistantStamp = formatTimestamp(time.Now())
				a.updateAssistantTurnLabel()
				fmt.Fprint(a.transcript, "\n\n")
			}
			if len(nextHistory) > 0 {
				a.history = nextHistory
			}
			a.busy = false
			a.stopSpinner()
			a.refreshContextBar()
			a.refreshFooter()
			a.tv.SetFocus(a.input)
			a.saveSession()

			// Auto-refresh tasks pane if visible and tasks were modified by the agent
			a.reloadTasksIfChanged()
		})
	}()
}

// submitScheduled injects a fired schedule's prompt into the live
// conversation. It is invoked from the scheduler worker goroutine, so
// all UI mutation is marshalled onto the event loop.
//
// When a turn is already in flight the prompt is delivered via the
// steering channel instead: the schedule doesn't pile up a second
// turn (single-flight), and the in-flight agent receives the
// schedule's instruction as user steering.
func (a *App) submitScheduled(agentName, prompt string) {
	a.tv.QueueUpdateDraw(func() {
		a.mu.Lock()
		if a.busy {
			select {
			case a.steeringCh <- prompt:
				a.appendActivity(fmt.Sprintf("[%s]schedule[-]: turn in flight; prompt steered into the running turn",
					a.palette.HexOrchid))
			default:
				a.appendActivity("[" + a.palette.HexOrchid + "::b]warning[-:-:-]: schedule fired while busy and steering queue is full; prompt dropped")
			}
			a.mu.Unlock()
			return
		}
		a.busy = true
		a.spinIdx = 0
		a.assistantState = "thinking"
		a.assistantStamp = ""
		a.mu.Unlock()

		a.startSpinner()
		a.refreshFooter()
		a.refreshContextBar()
		a.appendScheduledMessage(agentName, prompt)
		a.appendAssistantTurnLabel()
		a.transcript.ScrollToEnd()
		a.clearReasoningSplash()
		a.cognitionActive = true
		a.addReferencesFromText(prompt)
		a.refreshContextBar()

		a.mu.Lock()
		if err := a.saveSession(); err != nil {
			a.mu.Unlock()
			a.appendActivity(fmt.Sprintf("["+a.palette.HexOrchid+"::b]warning[-:-:-]: failed to save session: %v", err))
		} else {
			a.mu.Unlock()
		}

		a.launchTurn(prompt, agentName)
	})
}

// appendScheduledMessage renders the fired schedule's prompt as a
// user-style message, visually attributed to the schedule + agent so
// it is distinguishable from something the user typed:
//
//	17:50  ⚙ schedule …512af0 → social-researcher (*/10 * * * *)
//	> <prompt body>
func (a *App) appendScheduledMessage(agentName, prompt string) {
	stamp := formatTimestamp(time.Now())
	label := "⚙ schedule"
	if agentName != "" {
		label += " → " + agentName
	}
	fmt.Fprintf(a.transcript, "[%s]%s[-]\n", a.palette.HexDim, stamp)
	fmt.Fprintf(a.transcript, "[%s::b]%s[-:-:-]\n", a.palette.HexViolet, label)
	fmt.Fprintf(a.transcript, "[%s]%s[-]\n\n", a.palette.TextDim, prompt)
}

func (a *App) handleEvent(event tooling.Event) {
	a.tv.QueueUpdateDraw(func() {
		switch event.Type {
		case tooling.EventReasoning:
			a.assistantState = "thinking"
			a.clearReasoningSplash()
			a.cognitionActive = true
			fmt.Fprint(a.reasoning, a.highlightTranscriptText(event.Text))
			a.reasoning.ScrollToEnd()
			a.addReferencesFromText(event.Text)
		case tooling.EventCommentary:
			// Only set timestamp and update label on the first reply transition;
			// subsequent commentary chunks just append text to avoid repeat timestamps.
			if a.assistantState != "replied" {
				a.assistantState = "replied"
				a.assistantStamp = formatTimestamp(time.Now())
				a.updateAssistantTurnLabel()
			}
			fmt.Fprint(a.transcript, a.highlightTranscriptText(event.Text))
			a.transcript.ScrollToEnd()
			a.addReferencesFromText(event.Text)
		case tooling.EventToolStart:
			a.appendActivity(fmt.Sprintf("["+a.palette.HexViolet+"]→ %s[-] %s", event.ToolName, event.Text))
			a.addReferencesFromText(event.Text)
		case tooling.EventToolResult:
			// ui_control tool: event.Text contains a marker 'panel:<name>:<action>'
			if event.ToolName == "ui_control" && strings.HasPrefix(strings.TrimSpace(event.Text), "panel:") {
				marker := strings.TrimSpace(event.Text)
				parts := strings.SplitN(marker, ":", 6)
				if len(parts) >= 3 {
					panel := parts[1]
					action := parts[2]
					// canvas markers carry an optional
					// start:end:path tail (parts[3..5]).
					if panel == "canvas" && len(parts) >= 6 {
						start, _ := strconv.Atoi(parts[3])
						end, _ := strconv.Atoi(parts[4])
						path := parts[5]
						// Log BEFORE switching: canvas reuses the
						// activity TextView, so logging after would
						// append onto the rendered file and wreck
						// the scroll position.
						a.appendActivity(fmt.Sprintf("[%s]ui_control: canvas %s %s", a.palette.HexLavender, action, path))
						switch strings.ToLower(action) {
						case "show", "toggle":
							a.openCanvas(path, start, end)
						case "hide":
							a.showPanel("activity")
						}
						return
					}
					// Log BEFORE switching panels for the same reason.
					a.appendActivity(fmt.Sprintf("[%s]ui_control: %s %s", a.palette.HexLavender, panel, action))
					switch strings.ToLower(action) {
					case "show", "toggle":
						a.showPanel(panel)
					case "hide":
						a.showPanel("activity")
					}
					return
				}
			}
			a.appendActivity(fmt.Sprintf("["+a.palette.HexLavender+"::b]✓ %s[-:-:-] %s", event.ToolName, event.Text))
			a.addReferencesFromText(event.Text)
		case tooling.EventError:
			if isReasoningRelated(event.Text) {
				fmt.Fprintf(a.reasoning, "\n["+a.palette.HexOrchid+"::b]✗ %s[-:-:-] %s\n", event.ToolName, event.Text)
				a.reasoning.ScrollToEnd()
			} else {
				a.appendActivity(fmt.Sprintf("["+a.palette.HexOrchid+"::b]✗ %s[-:-:-] %s", event.ToolName, event.Text))
			}
		case tooling.EventContext:
			a.contextInfo = event.Text
			a.refreshContextBar()
		case tooling.EventSteering:
			// Mid-turn user steering. Render as a transcript line
			// distinct from a fresh-turn user message: same body
			// styling but a "steered:" prefix and a dimmed
			// "mid-turn" tag so the user can see exactly what was
			// injected into the in-flight conversation. The
			// activity log already records the queue event from
			// submit(), so we don't double-log here.
			stamp := formatTimestamp(time.Now())
			fmt.Fprintf(a.transcript, "[%s]%s[-]\n", a.palette.HexDim, stamp)
			fmt.Fprintf(a.transcript, "[%s::b]you steered[-:-:-] %s\n", a.palette.HexLavender, event.Text)
			a.transcript.ScrollToEnd()
		}
	})
}

func (a *App) assistantLabel() string {
	if a.assistantState == "thinking" {
		return "Coder is thinking..."
	}
	if a.assistantState == "replied" {
		return "Coder replied:"
	}
	return "Coder says:"
}

func (a *App) appendAssistantTurnLabel() {
	if a.transcript == nil {
		return
	}
	// Place timestamp above the label for clarity
	if a.assistantState == "replied" && a.assistantStamp != "" {
		fmt.Fprintf(a.transcript, "[%s]%s[-]\n", a.palette.HexDim, a.assistantStamp)
	}
	fmt.Fprintf(a.transcript, "[%s::b]%s[-:-:-]\n", a.palette.HexPurple, a.assistantLabel())
}

func (a *App) updateAssistantTurnLabel() {
	if a.transcript == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	// IMPORTANT: pass `false` here so the raw color tags are preserved.
	// GetText(true) strips all style/region tags from the buffer, which
	// was the source of the "colorization shifts after the assistant
	// responds" symptom: the round-trip through SetText would discard
	// the user bubble's purple-background tags and the rest of the
	// transcript would re-render with a different style stack.
	text := a.transcript.GetText(false)
	if text == "" {
		return
	}
	lines := strings.Split(text, "\n")
	idx := -1
	for i := len(lines) - 1; i >= 0; i-- {
		line := lines[i]
		if strings.Contains(line, "Coder is thinking") || strings.Contains(line, "Coder replied:") || strings.Contains(line, "Coder says:") {
			idx = i
			break
		}
	}
	if idx < 0 || idx >= len(lines) {
		// Defensive: a label could not be found, or the index is somehow
		// out of range relative to the current text (e.g. after a
		// concurrent setText). Fall back to appending a fresh label.
		a.appendAssistantTurnLabel()
		return
	}
	// The timestamp is rendered on the line *above* the label (see
	// appendAssistantTurnLabel). When this function is called more than
	// once per turn — first on the initial commentary transition, then
	// again when the turn completes — we must remove the previously
	// inserted timestamp line so it is not duplicated. Start the
	// replacement at the preceding dim timestamp line when present.
	start := idx
	if start > 0 && strings.Contains(lines[start-1], a.palette.HexDim) && timestampLineRe.MatchString(lines[start-1]) {
		start--
	}
	var replacement []string
	// Timestamp first, then label. The label uses an explicit
	// `bg=root` for the same reason as appendAssistantTurnLabel: to
	// guard against a leaked `bg=purple` from the user bubble.
	if a.assistantState == "replied" && a.assistantStamp != "" {
		replacement = append(replacement, fmt.Sprintf("[%s]%s[-]", a.palette.HexDim, a.assistantStamp))
	}
	replacement = append(replacement, fmt.Sprintf("[%s:%s:b]%s[-:-:-]", a.palette.HexPurple, a.palette.HexRoot, a.assistantLabel()))
	newLines := append([]string{}, lines[:start]...)
	newLines = append(newLines, replacement...)
	if idx+1 < len(lines) {
		newLines = append(newLines, lines[idx+1:]...)
	}
	a.transcript.SetText(strings.Join(newLines, "\n"))
}

// timestampLineRe matches a rendered clock stamp such as "5:04 PM" or
// "11:15 AM" inside a transcript line. It is used to detect and
// replace a previously inserted timestamp line so per-turn timestamps
// are never duplicated.
var timestampLineRe = regexp.MustCompile(`\d{1,2}:\d{2}`)

// formatTimestamp renders a wall-clock stamp in the user's local time
// using a 12-hour format with seconds (e.g. "5:04:04 PM"). It is the
// single source of truth for timestamp rendering in the TUI: the
// cognition pane, the activity log, and the per-turn transcript labels
// all share this helper so the format stays consistent across panels.
func formatTimestamp(t time.Time) string {
	return t.Format("3:04 PM")
}

func (a *App) highlightTranscriptText(text string) string {
	if text == "" {
		return ""
	}
	// Expand markdown tables into box-drawn tables before line-level highlighting.
	text = a.renderMarkdownTables(text)
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			lines[i] = fmt.Sprintf("[%s]%s[-]", a.palette.HexPurple, line)
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			lines[i] = fmt.Sprintf("[%s::b]%s[-:-:-]", a.palette.HexPurple, line)
			continue
		}
		if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") || strings.HasPrefix(trimmed, "+ ") {
			lines[i] = fmt.Sprintf("[%s]• %s[-]", a.palette.HexPurple, strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(trimmed, "- "), "* "), "+ ")))
			continue
		}
		if codeSpanRe.MatchString(line) {
			lines[i] = codeSpanRe.ReplaceAllStringFunc(line, func(match string) string {
				return fmt.Sprintf("[%s]%s[-]", a.palette.HexPurple, match)
			})
		}
	}
	return strings.Join(lines, "\n")
}

// renderMarkdownTables finds GFM-style pipe tables in the input and
// re-renders them as box-drawn tables with tview color markup. A table
// block is:
//
//	| col1 | col2 |       <- header row (required)
//	| ---- | :--- |  ...  <- separator row (required)
//	| a    | b    |       <- zero or more body rows
//
// Header row is bold-tinted with HexPurple; separator and borders use
// HexFaint box-drawing runes. Column widths are derived from the widest
// visible cell in each column (color tags stripped). Empty cells are
// padded to width. The original lines are replaced in-place.
func (a *App) renderMarkdownTables(text string) string {
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines)+8)
	i := 0
	for i < len(lines) {
		// Look for a header row: starts and ends with '|', has at least one inner '|'.
		if !isTableRow(lines[i]) || i+1 >= len(lines) || !isTableSeparator(lines[i+1]) {
			out = append(out, lines[i])
			i++
			continue
		}
		// Collect rows: header at i, separator at i+1, then consecutive table rows.
		header := parseTableRow(lines[i])
		sepCells := parseTableRow(lines[i+1])
		// Sanity: column counts should match (or be tolerant).
		if len(header) == 0 {
			out = append(out, lines[i])
			i++
			continue
		}
		ncols := len(header)
		if len(sepCells) > ncols {
			ncols = len(sepCells)
		}
		bodyRows := [][]string{}
		j := i + 2
		for j < len(lines) && isTableRow(lines[j]) {
			cells := parseTableRow(lines[j])
			// Pad/truncate to ncols so rendering is uniform.
			if len(cells) < ncols {
				padded := make([]string, ncols)
				copy(padded, cells)
				cells = padded
			} else if len(cells) > ncols {
				// Merge overflow into the last column with ' | ' — preserves content.
				merged := append([]string{}, cells[:ncols-1]...)
				merged = append(merged, strings.Join(cells[ncols-1:], " | "))
				cells = merged
			}
			bodyRows = append(bodyRows, cells)
			j++
		}
		// Header may also be short — normalize.
		if len(header) < ncols {
			padded := make([]string, ncols)
			copy(padded, header)
			header = padded
		}
		// Compute column widths from visible rune lengths.
		widths := make([]int, ncols)
		measure := func(cells []string) {
			for k, c := range cells {
				w := runeDisplayWidth(stripTviewTags(c))
				if w > widths[k] {
					widths[k] = w
				}
			}
		}
		measure(header)
		for _, r := range bodyRows {
			measure(r)
		}
		// Minimum column width 3 so borders don't collapse.
		for k := range widths {
			if widths[k] < 3 {
				widths[k] = 3
			}
		}
		// Render rows.
		faint := a.palette.HexFaint
		prim := a.palette.HexPurple
		renderRow := func(cells []string, bold bool) string {
			var b strings.Builder
			b.WriteString(fmt.Sprintf("[%s]│[-]", faint))
			for k := 0; k < ncols; k++ {
				cell := ""
				if k < len(cells) {
					cell = cells[k]
				}
				pad := widths[k] - runeDisplayWidth(stripTviewTags(cell))
				if pad < 0 {
					pad = 0
				}
				padding := strings.Repeat(" ", pad)
				content := cell
				if bold {
					content = fmt.Sprintf("[%s::b]%s%s[-:-:-]", prim, cell, padding)
				} else {
					content = cell + padding
				}
				b.WriteString(" " + content + " ")
				b.WriteString(fmt.Sprintf("[%s]│[-]", faint))
			}
			return b.String()
		}
		renderBorder := func(left, mid, right string) string {
			var b strings.Builder
			b.WriteString(fmt.Sprintf("[%s]%s[-]", faint, left))
			for k := 0; k < ncols; k++ {
				b.WriteString(fmt.Sprintf("[%s]%s[-]", faint, strings.Repeat("─", widths[k]+2)))
				if k < ncols-1 {
					b.WriteString(fmt.Sprintf("[%s]%s[-]", faint, mid))
				} else {
					b.WriteString(fmt.Sprintf("[%s]%s[-]", faint, right))
				}
			}
			return b.String()
		}
		out = append(out, renderBorder("┌", "┬", "┐"))
		out = append(out, renderRow(header, true))
		out = append(out, renderBorder("├", "┼", "┤"))
		for _, r := range bodyRows {
			out = append(out, renderRow(r, false))
		}
		out = append(out, renderBorder("└", "┴", "┘"))
		i = j
	}
	return strings.Join(out, "\n")
}

// isTableRow reports whether a line looks like a markdown pipe-table row:
// starts with '|', has at least one more '|', and isn't the separator line.
func isTableRow(line string) bool {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "|") {
		return false
	}
	if strings.Count(trimmed, "|") < 2 {
		return false
	}
	return !isTableSeparator(trimmed)
}

// isTableSeparator matches the |---|---|:---:| divider line under the header.
func isTableSeparator(line string) bool {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "|") {
		return false
	}
	inner := strings.Trim(trimmed, "|")
	if inner == "" {
		return false
	}
	for _, cell := range strings.Split(inner, "|") {
		c := strings.TrimSpace(cell)
		if c == "" {
			continue
		}
		// Allow ":" and "-" only.
		for _, r := range c {
			if r != '-' && r != ':' {
				return false
			}
		}
	}
	return true
}

// parseTableRow splits a pipe row into cells, trimming spaces and dropping
// an empty leading/trailing cell from the outer pipes.
func parseTableRow(line string) []string {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return nil
	}
	// Strip outer pipes if present.
	if strings.HasPrefix(trimmed, "|") {
		trimmed = trimmed[1:]
	}
	if strings.HasSuffix(trimmed, "|") {
		trimmed = trimmed[:len(trimmed)-1]
	}
	parts := strings.Split(trimmed, "|")
	out := make([]string, len(parts))
	for i, p := range parts {
		out[i] = strings.TrimSpace(p)
	}
	return out
}

// runeDisplayWidth counts runes (not bytes) — sufficient for monospace
// table alignment since tview cell widths are rune-based for non-wide
// CJK. Wide CJK chars are treated as width 2; combining runes as 0.
func runeDisplayWidth(s string) int {
	w := 0
	for _, r := range s {
		if r == '\u0300' || r == '\u0301' || r == '\u0302' || r == '\u0303' ||
			r == '\u0304' || r == '\u0336' || r == '\u200B' {
			continue
		}
		if r >= 0x1100 && (r <= 0x115F || r == 0x2329 || r == 0x232A ||
			(r >= 0x2E80 && r <= 0xA4CF) || (r >= 0xAC00 && r <= 0xD7A3) ||
			(r >= 0xF900 && r <= 0xFAFF) || (r >= 0xFE30 && r <= 0xFE4F) ||
			(r >= 0xFF00 && r <= 0xFF60) || (r >= 0xFFE0 && r <= 0xFFE6)) {
			w += 2
			continue
		}
		w++
	}
	return w
}

// stripTviewTags removes tview color/region tags so widths measure visible
// characters only. Tags look like [#A77CF8], [-], [-:-:-], [-:bg:b],
// [region-id]. The regex requires the opening bracket be followed by '#',
// '-', or an alphanumeric region-id — and crucially does NOT span across
// a ']' to reach another '[', which would let it swallow text content
// between two adjacent tags (the original "any chars in brackets" pattern
// ate the literal string "Name" between "[#A77CF8::b]" and "[-:-:-]").
var tviewTagRe = regexp.MustCompile(`\[(?:#[0-9a-fA-F]{3,8}|-(?::[^\[\]-]*){0,3}|[A-Za-z_][A-Za-z0-9_]*)\]`)

func stripTviewTags(s string) string {
	return tviewTagRe.ReplaceAllString(s, "")
}

func (a *App) appendActivity(line string) {
	stamp := formatTimestamp(time.Now())
	fmt.Fprintf(a.activity, "["+a.palette.HexFaint+"]%s[-] %s\n", stamp, line)
	a.activity.ScrollToEnd()
}

// truncateForActivity shortens a user-typed prompt so that activity
// log entries (which appear in a narrow right column on most
// terminals) stay on a single line. Long prompts are clipped with
// an ellipsis at 60 runes; short prompts are returned verbatim.
func truncateForActivity(s string) string {
	const max = 60
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max-1]) + "…"
}

// appendUserMessage renders the user's prompt as a filled ergo-a.palette.Purple
// rectangular bubble in the conversation panel, using the same padding
// standard as the rest of the TUI. A timestamp line is rendered above
// the bubble so both user and assistant messages carry a consistent
// attribution header.
func (a *App) appendUserMessage(prompt string) {
	if a.transcript == nil {
		return
	}
	a.clearStartupSplash()
	// Timestamp above the user attribution, matching the assistant label style.
	fmt.Fprintf(a.transcript, "[%s]%s[-]\n", a.palette.HexDim, formatTimestamp(time.Now()))
	fmt.Fprint(a.transcript, a.renderUserMessage(prompt))
}

func (a *App) clearStartupSplash() {
	if !a.startupSplashVisible || a.transcript == nil {
		return
	}
	a.transcript.Clear()
	a.startupSplashVisible = false
}

func (a *App) setTranscriptSplash() {
	if a.transcript == nil {
		return
	}
	a.transcript.SetText(a.renderStartupSplash())
	a.startupSplashVisible = true
}

// setReasoningSplash puts the Cognition/ABOUT pane back into its empty
// idle state, renaming the panel title to ABOUT and rendering the intro
// copy in the body.

// rightColumnHeights returns the (cognition, body) row allocations for
// the right column given the panel sizes available in the parent Flex.
// The model is intentionally simple: the active body (activity, tasks,
// articles, code) gets a generous floor so it never collapses to a
// useless sliver, while cognition gets enough room to stream a few
// paragraphs but never enough to crowd out the body.
//
// Heuristics (rows = terminal rows the right column will have):
//   - bodyMin       = max(6, rows * 0.30)        // at least 6, or 30%
//   - bodyMax       = max(bodyMin, rows * 0.75)  // never more than 75%
//   - cogMin        = max(3, rows * 0.15)        // at least 3, or 15%
//   - cogMax        = max(cogMin, rows - bodyMin)
//   - finalBody     = clamp(bodyTarget, bodyMin, bodyMax)
//   - finalCog      = clamp(cogTarget,  cogMin,  cogMax)
//
// When the user is focused on a non-activity body (tasks, articles,
// code) cognition is also capped harder (40% of rows) since the body
// is the primary surface the user is reading.
func (a *App) rightColumnHeights(rows int) (cog, body int) {
	if rows <= 0 {
		rows = 24 // sensible default before the first Draw gives us a real size
	}
	// Target heights before clamping. Cognition starts at 8 rows
	// (enough for a couple of paragraphs of streaming model output)
	// and grows with rows; body starts at 10 and grows faster.
	cogTarget := 8
	bodyTarget := 10
	if rows >= 30 {
		cogTarget = 10
		bodyTarget = rows - cogTarget
	} else if rows >= 20 {
		cogTarget = 7
		bodyTarget = rows - cogTarget
	}
	// Compute the floors / caps.
	bodyMin := 6
	if rows*30/100 > bodyMin {
		bodyMin = rows * 30 / 100
	}
	bodyMax := bodyMin
	if rows*75/100 > bodyMax {
		bodyMax = rows * 75 / 100
	}
	cogMin := 3
	if rows*15/100 > cogMin {
		cogMin = rows * 15 / 100
	}
	// Hard cap on cognition. When the user is looking at a panel
	// other than activity (tasks, articles, code, memories), shrink
	// the cognition ceiling further so the focused body has more
	// room.
	cogCeiling := rows - bodyMin
	if a.activePanel != "" && a.activePanel != "activity" {
		if rows*40/100 < cogCeiling {
			cogCeiling = rows * 40 / 100
		}
	}
	if cogCeiling < cogMin {
		cogCeiling = cogMin
	}
	// Apply clamps.
	body = bodyTarget
	if body < bodyMin {
		body = bodyMin
	}
	if body > bodyMax {
		body = bodyMax
	}
	// Make sure we never exceed the available rows. If the body
	// alone would overflow, trim it down so cognition keeps its
	// minimum.
	if body+cogMin > rows {
		body = rows - cogMin
		if body < bodyMin {
			body = bodyMin
		}
	}
	cog = rows - body
	if cog < cogMin {
		cog = cogMin
	}
	if cog > cogCeiling {
		cog = cogCeiling
	}
	// Final safety: ensure we never return negative heights. If
	// rows is absurdly small (e.g. 2) split the leftover evenly.
	if body < 1 {
		body = 1
	}
	if cog < 1 {
		cog = 1
	}
	if body+cog > rows {
		overflow := body + cog - rows
		if body-overflow >= 1 {
			body -= overflow
		} else if cog-overflow >= 1 {
			cog -= overflow
		}
	}
	return cog, body
}

// buildRightColumn assembles the right-hand Flex (Cognition stacked
// above the active body) using adaptive heights from
// rightColumnHeights. The function is cheap and idempotent so it can
// be called whenever the active panel changes, cognition state
// changes, or the parent Flex resizes.
//
// The active body is whichever panel the user has selected via
// /tasks, /articles, /code, or the default activity stream. The
// activity panel is the only "log"-style surface; the others
// buildRightColumn constructs the right-hand column with adaptive, content-aware
// height allocation. The reasoning (cognition) and body (activity/tasks/code) panes
// use proportional heights that adapt to content, preventing background bleed-through.
// (tasks, articles, code) are navigable / interactive views and
// are managed by their own primitives.
func (a *App) buildRightColumn() *tview.Flex {
	// Determine the proportion of space for cognition vs body based on active panel
	// and content. Using flex proportions (1:2 or 2:3) instead of fixed heights
	// allows content to adapt without leaving empty space.
	cogProp := 1  // Cognition gets 1 part
	bodyProp := 2 // Body gets 2 parts by default (2:3 ratio)

	// When viewing a non-activity panel, give more space to the body since it's
	// the primary focus (1:1.5 ratio instead of 1:2)
	if a.activePanel != "" && a.activePanel != "activity" {
		bodyProp = 1
		cogProp = 1
	}

	// Pick the body primitive that matches the active panel.
	// Defaults to the activity TextView so the right column still
	// works even if a panel name is unknown.
	var bodyPrim tview.Primitive = a.activityPanel
	switch a.activePanel {
	case "tasks":
		bodyPrim = a.tasksPanel
	case "memories":
		bodyPrim = a.memoriesPanel
	case "test":
		bodyPrim = a.testPanel
	case "articles", "code", "canvas", "reference":
		// Articles reuse the activity TextView placeholder; canvas
		// (and its /code, /reference aliases) renders file content
		// into that same TextView, so the body stays this primitive.
		bodyPrim = a.activityPanel
	}
	right := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(a.reasoningPanel, 0, cogProp, false).
		AddItem(bodyPrim, 0, bodyProp, false)
	right.SetBackgroundColor(a.palette.BgReasoning)
	a.right = right
	return right
}

// rebuildLayout reconstructs the page tree, hiding or showing the
// right column based on a.fullscreen. We rebuild the layout from
// scratch so fullscreen is fully reversible and idempotent.
func (a *App) rebuildLayout() {
	if a.tv == nil || a.pages == nil {
		return
	}
	vGutter := spacerBox(a.palette.BgRoot)
	var mainFlex *tview.Flex
	if a.fullscreen {
		// Fullscreen: hide the right column, the gutter, AND the
		// "Conversation" title row so the transcript is the only
		// thing on screen. The bare a.transcript TextView is used
		// (rather than a.transcriptPanel) so the user can copy a
		// contiguous block of text without also pulling in the
		// "Conversation" label or any title-row padding. The same
		// marginX insets are kept so the left edge still lines up
		// with the input surface below.
		transcriptOnly := tview.NewFlex().
			AddItem(spacerBox(a.palette.BgRoot), marginX-1, 0, false).
			AddItem(a.transcript, 0, 1, false).
			AddItem(spacerBox(a.palette.BgRoot), marginX, 0, false)
		transcriptOnly.SetBackgroundColor(a.palette.BgRoot)
		mainFlex = tview.NewFlex().
			AddItem(transcriptOnly, 0, 1, false)
		// a.right is now stale; clear it so buildRightColumn
		// recomputes from scratch the next time it is called.
		a.right = nil
	} else if a.aboutMode {
		// About mode: ASCII on the left, intro on the right, no
		// "Conversation" title bar.
		aboutRow := tview.NewFlex().
			AddItem(a.aboutAscii, 0, 1, false).
			AddItem(vGutter, panelGutter, 0, false).
			AddItem(a.aboutBody, 0, 1, false)
		aboutRow.SetBackgroundColor(a.palette.BgRoot)
		left := tview.NewFlex().
			AddItem(a.transcript, 0, 1, false)
		left.SetBackgroundColor(a.palette.BgRoot)
		mainFlex = tview.NewFlex().
			AddItem(left, 0, 5, false).
			AddItem(vGutter, panelGutter, 0, false).
			AddItem(aboutRow, 0, 3, false)
	} else {
		// Delegate to buildRightColumn so the active body primitive
		// tracks a.activePanel (tasks, articles, code, activity)
		// rather than being hardcoded to the activity stream.
		// Without this, showPanel("tasks") would build a tasksList
		// that is never attached to the page tree, and focusing it
		// would crash the TUI.
		right := a.buildRightColumn()
		leftPrim := tview.Primitive(a.transcriptPanel)
		if a.leftPane != nil {
			leftPrim = a.leftPane
		}
		mainFlex = tview.NewFlex().
			AddItem(leftPrim, 0, 5, false).
			AddItem(vGutter, panelGutter, 0, false).
			AddItem(right, 0, 3, false)
	}
	mainFlex.SetBackgroundColor(a.palette.BgRoot)

	headerRow := tview.NewFlex().
		AddItem(a.header, 0, 1, false).
		AddItem(a.headerRight, 0, 1, false)
	headerRow.SetBackgroundColor(a.palette.BgRoot)

	footerRow := tview.NewFlex().
		AddItem(a.statusBar, 0, 1, false).
		AddItem(a.footer, 0, 1, false)
	footerRow.SetBackgroundColor(a.palette.BgRoot)

	layout := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(headerRow, 3, 0, false).
		AddItem(spacerBox(a.palette.BgRoot), panelGutter, 0, false).
		AddItem(mainFlex, 0, 1, false).
		AddItem(spacerBox(a.palette.BgRoot), panelGutter, 0, false).
		AddItem(a.inputRow, 5, 0, true).
		AddItem(spacerBox(a.palette.BgRoot), panelGutter, 0, false).
		AddItem(footerRow, 3, 0, false)
	layout.SetBackgroundColor(a.palette.BgRoot)

	// Remove the old "main" page if it exists, then add the new layout.
	// This ensures the page stack stays clean and focus updates work properly.
	a.pages.RemovePage("main")
	a.pages.AddPage("main", layout, true, true)
	a.rootFlex = layout
}

func (a *App) setReasoningSplash() {
	if a.reasoning == nil {
		return
	}
	a.cognitionActive = false
	if a.reasoningTitle != nil {
		a.reasoningTitle.SetText(fmt.Sprintf(" [%s]%s[-]", a.palette.HexPurple, "ABOUT"))
	}
	a.reasoning.Clear()
	fmt.Fprint(a.reasoning, a.renderAboutSplash())
	a.reasoningSplashVisible = true
}

// clearReasoningSplash removes the ABOUT splash from the Cognition pane
// (without re-rendering it) and flips the panel title back to COGNITION
// so the inner monologue can take over.
func (a *App) clearReasoningSplash() {
	if !a.reasoningSplashVisible || a.reasoning == nil {
		// Even if the splash was already cleared, make sure the title is
		// correct in case we re-enter cognition mid-session.
		if !a.reasoningSplashVisible && a.cognitionActive && a.reasoningTitle != nil {
			a.reasoningTitle.SetText(fmt.Sprintf(" [%s]%s[-]", a.palette.HexPurple, "COGNITION"))
		}
		return
	}
	a.reasoning.Clear()
	a.reasoningSplashVisible = false
	if a.reasoningTitle != nil {
		a.reasoningTitle.SetText(fmt.Sprintf(" [%s]%s[-]", a.palette.HexPurple, "COGNITION"))
	}
}

func (a *App) renderStartupSplash() string {
	ascii := strings.TrimRight(a.loadAsciiArt(), "\n")

	var b strings.Builder
	if ascii != "" {
		b.WriteString(ascii)
		b.WriteString("\n\n")
	}
	b.WriteString(fmt.Sprintf("[%s]Made with love in Las Vegas by TJ Coder AI Labs.  %s[-]",
		a.palette.HexFaint, "@tjcoder/cli"))
	return b.String()
}

// renderAboutIntro is the body copy shown on the right half of the
// About screen. It re-uses the same voice as renderAboutSplash but
// keeps things short so the ASCII art on the left has room to
// breathe.
func (a *App) renderAboutIntro() string {
	product := a.productName
	if product == "" {
		product = "Coder CLI"
	}
	handle := "@tjcoder/cli"
	var b strings.Builder
	fmt.Fprintf(&b, "[%s::b]%s[-:-:-]\n", a.palette.HexLavender, handle)
	fmt.Fprintf(&b, "[%s]%s[-]\n\n", a.palette.HexFaint, product)
	fmt.Fprintf(&b, "[%s]A terminal-first coding TUI that talks to local and remote[-]\n", a.palette.HexDim)
	fmt.Fprintf(&b, "[%s]models, streams its thinking in a side panel, and routes tool[-]\n", a.palette.HexDim)
	fmt.Fprintf(&b, "[%s]calls through a curated terminal/code toolkit.[-]\n\n", a.palette.HexDim)
	fmt.Fprintf(&b, "[%s]Made with love in Las Vegas by TJ Coder AI Labs.[-]\n\n", a.palette.HexFaint)
	if a.appVersion != "" {
		fmt.Fprintf(&b, "[%s]version  %s[-]\n", a.palette.HexPurple, a.appVersion)
	} else {
		fmt.Fprintf(&b, "[%s]version  dev[-]\n", a.palette.HexPurple)
	}
	fmt.Fprintf(&b, "[%s]%s[-]", a.palette.HexFaint, "Type to start chatting. /about toggles this view.")
	return b.String()
}

// setAboutMode flips aboutMode on/off and rebuilds the layout so the
// transcript title is hidden and the right column is swapped for the
// About split (ASCII on the left, intro on the right). When
// re-entering the chat surface, the previous transcript state is
// restored via setTranscriptSplash / clearReasoningSplash.
func (a *App) setAboutMode(on bool) {
	if a.aboutMode == on {
		return
	}
	a.aboutMode = on
	if on {
		// Seed the transcript body with the startup splash so the
		// left half reads as the ASCII art context rather than
		// whatever chat history the user was looking at.
		a.transcript.SetText(a.renderStartupSplash())
		a.startupSplashVisible = true
		a.appendActivity("Opened /about screen")
	} else {
		a.appendActivity("Closed /about screen")
	}
	a.rebuildLayout()
}

// renderAboutSplash is shown in the Cognition/ABOUT pane while the
// model is idle. It reintroduces the product and surfaces the running
// version. (Kept for compatibility with the splash path.)
func (a *App) renderAboutSplash() string {
	product := a.productName
	if product == "" {
		product = "Coder CLI"
	}
	handle := "@tjcoder/cli"
	var b strings.Builder
	fmt.Fprintf(&b, "[%s::b]Welcome to %s[-:-:-]\n", a.palette.HexLavender, handle)
	fmt.Fprintf(&b, "[%s]%s[-]\n\n", a.palette.HexFaint, product)
	fmt.Fprintf(&b, "[%s]A terminal-first coding TUI that talks to local and remote models,[-]\n", a.palette.HexDim)
	fmt.Fprintf(&b, "[%s]streams its thinking in a side panel, and routes tool calls through a[-]\n", a.palette.HexDim)
	fmt.Fprintf(&b, "[%s]curated terminal/code toolkit.[-]\n\n", a.palette.HexDim)
	if a.appVersion != "" {
		fmt.Fprintf(&b, "[%s]version  %s[-]", a.palette.HexPurple, a.appVersion)
	} else {
		fmt.Fprintf(&b, "[%s]version  dev[-]", a.palette.HexPurple)
	}
	return b.String()
}

func (a *App) loadAsciiArt() string {
	paths := []string{
		filepath.Join(a.workspaceRoot, "ergo-ascii.txt"),
		"ergo-ascii.txt",
	}
	for _, path := range paths {
		if path == "" {
			continue
		}
		if data, err := os.ReadFile(path); err == nil {
			return string(data)
		}
	}
	return ""
}

func (a *App) resetEnabledTools(names []string) {
	a.enabledTools = map[string]bool{}
	for _, name := range names {
		a.enabledTools[name] = true
	}
}

func (a *App) enabledToolList() []string {
	names := make([]string, 0, len(a.enabledTools))
	for _, name := range a.currentAgent.ToolNames {
		if a.enabledTools[name] {
			names = append(names, name)
		}
	}
	return names
}

func (a *App) loadModels() {
	ctx, cancel := context.WithTimeout(context.Background(), modelLoadTimeout)
	defer cancel()
	models, err := a.provider.ListModels(ctx)
	if err != nil {
		a.appendActivity(fmt.Sprintf("["+a.palette.HexOrchid+"::b]model load failed[-:-:-]: %v", err))
		return
	}
	a.models = models
	if a.currentModel == "" && len(models) > 0 {
		a.currentModel = models[0].Name
	}
	a.refreshHeader()
}

func (a *App) loadModelsAsync() {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), modelLoadTimeout)
		defer cancel()
		models, err := a.provider.ListModels(ctx)
		a.tv.QueueUpdateDraw(func() {
			if err != nil {
				a.appendActivity(fmt.Sprintf("["+a.palette.HexViolet+"]model load deferred[-]: %v", err))
				return
			}
			a.models = models
			if a.currentModel == "" && len(models) > 0 {
				a.currentModel = models[0].Name
			}
			a.refreshHeader()
		})
	}()
}

func (a *App) openAgentModal() {
	list := tview.NewList().ShowSecondaryText(true)
	a.styleModalList(list)
	for _, cfg := range a.agents {
		agentCfg := cfg
		list.AddItem(agentCfg.Name, agentCfg.Title, 0, func() {
			a.currentAgent = agentCfg
			a.resetEnabledTools(agentCfg.ToolNames)
			// If no explicit model was chosen by the user, adopt the
			// agent's DefaultModel so switching agents also switches
			// to the right model family.
			if agentCfg.DefaultModel != "" && (a.currentModel == "" || a.currentModel == a.previousAgentDefaultModel) {
				a.currentModel = agentCfg.DefaultModel
			}
			a.previousAgentDefaultModel = agentCfg.DefaultModel
			a.refreshFooter()
			a.appendActivity("Selected agent: " + agentCfg.Name)
			a.appendActivity("Enabled tools: " + strings.Join(a.enabledToolList(), ", "))
			a.saveSession()
			a.closeModal()
			// Agent switch may have implicitly switched the model
			// (to the agent's default). Recompute the token bar so
			// the user sees the new model's context window at once.
			a.refreshContextForModel(a.currentModel)
		})
	}
	list.SetDoneFunc(func() {
		a.closeModal()
	})
	a.showModal("Select Agent", list)
}

func (a *App) openModelModal() {
	if len(a.models) == 0 {
		a.loadModels()
	}
	list := tview.NewList().ShowSecondaryText(true)
	a.styleModalList(list)
	for _, model := range a.models {
		item := model
		label := item.Name
		if item.ParameterSize != "" {
			label = fmt.Sprintf("%s (%s)", item.Name, item.ParameterSize)
		}
		list.AddItem(label, item.Family, 0, func() {
			a.currentModel = item.Name
			a.refreshFooter()
			a.appendActivity("Selected model: " + item.Name)
			// Remember the pick as the shared cross-mode model so it
			// persists across program sessions and workspaces (and is
			// honored by headless runs too).
			_ = session.SetLastModel(true, item.Name)
			a.saveSession()
			a.closeModal()
			// Different model → different context window. Recompute
			// the token indicator immediately so the user sees the
			// bar move to the new model's total, not the old one.
			a.refreshContextForModel(item.Name)
		})
	}
	list.SetDoneFunc(func() {
		a.closeModal()
	})
	a.showModal("Select Model", list)
}

// refreshContextForModel re-estimates the token consumption indicator
// against the context window of `model` and re-renders the context bar
// right away. Use this immediately after switching agent or model so
// the bar reflects the new model's window without waiting for the
// next model response. Each model family has a different context
// window (e.g. 8K for some Gemini variants vs. 128K+ for others), so
// the "X% of Y tokens" total genuinely changes on a switch. If the
// provider can't resolve a window or there's no history yet, the bar
// falls back to "ctx: unavailable" so the user sees a clear state.
func (a *App) refreshContextForModel(model string) {
	if a.provider == nil || model == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	window, err := a.provider.ContextWindow(ctx, model)
	if err != nil || window <= 0 {
		// Unknown window — keep the previous bar but mark it so the
		// user at least sees the model name is in effect. We
		// intentionally don't blank the indicator; the next model
		// response will refresh it with authoritative counts.
		a.appendActivity(fmt.Sprintf("[%s]token indicator[-]: context window unknown for %s", a.palette.HexDim, model))
		return
	}
	estChars := 0
	for _, m := range a.history {
		estChars += len(m.Role) + len(m.Content) + len(m.Thinking) + len(m.ToolName)
		for _, tc := range m.ToolCalls {
			estChars += len(tc.Function.Name) + len(tc.Function.Arguments)
		}
	}
	estTok := estChars / 4
	if estTok > window {
		estTok = window
	}
	remaining := window - estTok
	if remaining < 0 {
		remaining = 0
	}
	pctUsed := float64(estTok) / float64(window) * 100.0
	a.contextInfo = fmt.Sprintf("ctx: ~%s / %s used (%.1f%%, est), remaining=%s",
		formatTokensCompactTUI(estTok), formatTokensCompactTUI(window), pctUsed, formatTokensCompactTUI(remaining))
	a.refreshContextBar()
}

func (a *App) openToolModal() {
	list := tview.NewList().ShowSecondaryText(false)
	a.styleModalList(list)
	var rebuild func()
	rebuild = func() {
		list.Clear()
		for _, name := range a.currentAgent.ToolNames {
			toolName := name
			prefix := "[ ]"
			if a.enabledTools[toolName] {
				prefix = "[x]"
			}
			list.AddItem(prefix+" "+toolName, "", 0, func() {
				a.enabledTools[toolName] = !a.enabledTools[toolName]
				rebuild()
				a.appendActivity("Enabled tools: " + strings.Join(a.enabledToolList(), ", "))
				a.saveSession()
			})
		}
	}
	rebuild()
	list.SetDoneFunc(func() {
		a.closeModal()
	})
	a.showModal("Toggle Tools (Enter toggles, Esc closes)", list)
}

// showModal renders a modal-style primitive (editor, form, or selector
// list) in place of the Conversation column, keeping the right column
// visible alongside. This replaces the old floating-window presentation
// so the UI adapts consistently for every applicable command. The modal
// borrows the borderless design language of the prompt bar
// (newInputSurface): a subtly elevated bgInput surface fronted by a
// single left accent beam instead of a box border, with an uppercase
// purple title label matching the panels. Use closeModal to dismiss and
// restore the transcript.
func (a *App) showModal(title string, primitive tview.Primitive) {
	bg := a.palette.BgInput

	label := tview.NewTextView().SetDynamicColors(true)
	label.SetBackgroundColor(bg)
	label.SetText(fmt.Sprintf(" [%s]%s[-]", a.palette.HexPurple, strings.ToUpper(title)))
	label.SetBorderPadding(1, 0, 2, 2)

	inner := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(label, 2, 0, false).
		AddItem(primitive, 0, 1, true)
	inner.SetBackgroundColor(bg)

	// Signature left accent beam (mirrors newInputSurface): a single
	// vertical rule that lights up in purple while the modal owns focus
	// and dims when it does not, standing in for a box border.
	beam := tview.NewBox()
	beam.SetBackgroundColor(bg)
	beam.SetDrawFunc(func(screen tcell.Screen, x, y, width, height int) (int, int, int, int) {
		style := tcell.StyleDefault.Foreground(a.palette.Purple).Background(bg)
		if !primitive.HasFocus() {
			style = tcell.StyleDefault.Foreground(tcell.NewRGBColor(84, 49, 124)).Background(bg)
		}
		for row := 0; row < height; row++ {
			screen.SetContent(x, y+row, '│', nil, style)
		}
		return x, y, width, height
	})

	panel := tview.NewFlex().
		AddItem(beam, 1, 0, false).
		AddItem(spacerBox(bg), marginX-1, 0, false).
		AddItem(inner, 0, 1, true).
		AddItem(spacerBox(bg), marginX, 0, false)
	panel.SetBackgroundColor(bg)

	// About and fullscreen modes own the left column too; clear them so
	// the modal pane is what actually renders.
	a.aboutMode = false
	a.fullscreen = false
	a.leftPane = panel
	a.rebuildLayout()
	a.tv.SetFocus(primitive)
}

// closeModal dismisses an in-pane modal and restores the Conversation
// column, returning focus to the input field.
func (a *App) closeModal() {
	a.leftPane = nil
	a.rebuildLayout()
	a.tv.SetFocus(a.input)
}

func (a *App) styleModalList(list *tview.List) {
	list.SetBackgroundColor(a.palette.BgInput)
	list.SetBorderPadding(1, 1, 2, 2)
	list.SetMainTextColor(a.palette.TextMain)
	list.SetSecondaryTextColor(a.palette.TextDim)
	list.SetSelectedBackgroundColor(a.palette.BgSelect)
	list.SetSelectedTextColor(a.palette.Lavender)
}

// styleModalForm applies the borderless modal surface palette to a form
// so input fields and buttons read as part of the same bgInput surface
// as the rest of the modal rather than tview's default black widgets.
func (a *App) styleModalForm(form *tview.Form) {
	form.SetBackgroundColor(a.palette.BgInput)
	form.SetFieldBackgroundColor(a.palette.BgSelect)
	form.SetFieldTextColor(a.palette.TextMain)
	form.SetLabelColor(a.palette.Lavender)
	form.SetButtonBackgroundColor(a.palette.BgSelect)
	form.SetButtonTextColor(a.palette.TextMain)
	form.SetButtonsAlign(tview.AlignRight)
}

func (a *App) ToolNames() []string {
	names := a.enabledToolList()
	sort.Strings(names)
	return names
}

func (a *App) scrollTranscript(delta int) {
	row, col := a.transcript.GetScrollOffset()
	row += delta
	if row < 0 {
		row = 0
	}
	a.transcript.ScrollTo(row, col)
}

func isReasoningRelated(text string) bool {
	lower := strings.ToLower(text)
	return strings.Contains(lower, "think") ||
		strings.Contains(lower, "reasoning") ||
		strings.Contains(lower, "<think>") ||
		strings.Contains(lower, "model note")
}

var pathRefRe = regexp.MustCompile(`(?i)([a-z0-9_./~\-]+\.(?:md|markdown|txt|go|ts|tsx|js|jsx|py|json|ya?ml|toml|sh|sql|conf))`)
var codeSpanRe = regexp.MustCompile("`([^`]+)`")

func (a *App) addReferencesFromText(text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	matches := pathRefRe.FindAllString(text, -1)
	for _, match := range matches {
		ref := strings.TrimSpace(strings.Trim(match, "[](){}<>\"'`.,;:!?"))
		if ref == "" {
			continue
		}
		if strings.Contains(ref, "://") {
			continue
		}
		ref = strings.TrimPrefix(ref, "./")
		if filepath.IsAbs(ref) {
			if rel, err := filepath.Rel(a.workspaceRoot, ref); err == nil && rel != ".." && !strings.HasPrefix(rel, "../") {
				ref = rel
			}
		}
		ref = filepath.Clean(ref)
		if _, exists := a.refSet[ref]; exists {
			continue
		}
		a.refSet[ref] = struct{}{}
		a.refOrder = append(a.refOrder, ref)
	}
	a.refreshContextBar()
}

func (a *App) referenceSummary() string {
	if len(a.refOrder) == 0 {
		return "refs: -"
	}
	mdRefs := make([]string, 0, len(a.refOrder))
	otherRefs := make([]string, 0, len(a.refOrder))
	for _, ref := range a.refOrder {
		if strings.HasSuffix(strings.ToLower(ref), ".md") || strings.HasSuffix(strings.ToLower(ref), ".markdown") {
			mdRefs = append(mdRefs, ref)
		} else {
			otherRefs = append(otherRefs, ref)
		}
	}
	ordered := append(mdRefs, otherRefs...)
	maxShown := 5
	if len(ordered) <= maxShown {
		return "refs: " + strings.Join(ordered, ", ")
	}
	return fmt.Sprintf("refs: %s (+%d)", strings.Join(ordered[:maxShown], ", "), len(ordered)-maxShown)
}

func (a *App) handleMCPCommand(parts []string) {
	if len(parts) < 2 {
		a.appendActivity("Usage: /mcp [add|list|remove] [args...]")
		return
	}

	action := strings.ToLower(parts[1])
	switch action {
	case "add":
		if len(parts) < 4 {
			a.appendActivity("Usage: /mcp add <name> <url>")
			return
		}
		name := parts[2]
		url := parts[3]
		err := a.mcpClient.AddServer(name, url)
		if err != nil {
			a.appendActivity(fmt.Sprintf("[%s]MCP add failed[-]: %v", a.palette.HexOrchid, err))
			return
		}
		// Register the tools in the global registry
		tools.RegisterMCPClient(a.registry, a.mcpClient)
		// Persist to config so the integration survives restarts and
		// shows up in /config's JSON editor.
		if a.config.MCPServers == nil {
			a.config.MCPServers = make(map[string]MCPServerConfig)
		}
		a.config.MCPServers[name] = MCPServerConfig{
			Type:    "sse",
			URL:     url,
			Enabled: true,
		}
		if saveErr := a.saveConfig(a.config); saveErr != nil {
			a.appendActivity(fmt.Sprintf("[%s]MCP %s added in-memory but config save failed[-]: %v",
				a.palette.HexOrchid, name, saveErr))
			return
		}
		a.appendActivity(fmt.Sprintf("Successfully added MCP server [%s]%s[-] and registered tools", a.palette.HexLavender, name))

	case "list":
		servers := a.mcpClient.GetServers()
		if len(servers) == 0 {
			a.appendActivity("No MCP servers configured.")
			return
		}
		var b strings.Builder
		b.WriteString("Active MCP Servers:\n")
		for name, s := range servers {
			status := "enabled"
			if !s.IsEnabled {
				status = "disabled"
			}
			b.WriteString(fmt.Sprintf("- %s (%s) [%s]\n", name, status, s.URL))
		}
		a.appendActivity(b.String())

	case "remove":
		if len(parts) < 3 {
			a.appendActivity("Usage: /mcp remove <name>")
			return
		}
		name := parts[2]
		if err := a.mcpClient.RemoveServer(name); err != nil {
			a.appendActivity(fmt.Sprintf("[%s]MCP remove failed[-]: %v", a.palette.HexOrchid, err))
			return
		}
		// Drop the mcp_{name}_* tools from the agent's tool set so
		// they stop being offered as available functions.
		tools.UnregisterMCPServer(a.registry, name)
		// Drop from persisted config.
		if _, ok := a.config.MCPServers[name]; ok {
			delete(a.config.MCPServers, name)
			if saveErr := a.saveConfig(a.config); saveErr != nil {
				a.appendActivity(fmt.Sprintf("[%s]MCP %s removed in-memory but config save failed[-]: %v",
					a.palette.HexOrchid, name, saveErr))
				return
			}
		}
		a.appendActivity(fmt.Sprintf("Removed MCP server [%s]%s[-]", a.palette.HexLavender, name))

	default:
		a.appendActivity("Unknown MCP action. Use add, list, or remove.")
	}
}
