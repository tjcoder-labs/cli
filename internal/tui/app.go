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

type App struct {
	tv *tview.Application
	// running is true between App.Run() entering tv.Run() and
	// tv.Run() returning. It gates which goroutine is allowed to
	// touch tview primitives that the event loop owns (focus,
	// redraw, etc.). Reading it from any goroutine is safe; the
	// only mutations happen in Run() under the caller's stack.
	running bool

	provider     client.Provider
	registry     *tools.Registry
	runner       *tooling.Runner
	sessionState session.State
	palette      Palette

	workspaceRoot          string
	productName            string
	author                 string
	appVersion             string
	agents                 []agent.Config
	currentAgent           agent.Config
	currentModel           string
	models                 []client.ModelInfo
	enabledTools           map[string]bool
	history                []client.Message
	busy                   bool
	contextInfo            string
	refSet                 map[string]struct{}
	refOrder               []string
	spinIdx                int
	spinStop               chan struct{}
	assistantState         string
	assistantStamp         string
	inputPlaceholder       string
	inputPlaceholderActive bool

	header          *tview.TextView
	transcript      *tview.TextView
	transcriptTitle *tview.TextView
	transcriptPanel *tview.Flex
	reasoning       *tview.TextView
	reasoningTitle  *tview.TextView
	tasksTitle      *tview.TextView
	reasoningPanel  *tview.Flex
	activity        *tview.TextView
	activityPanel   *tview.Flex
	// tasksList is the interactive tview.List shown in the right
	// column when the user opens /tasks. The user navigates with
	// Up/Down and presses Enter to toggle a task between done and
	// its previous open status.
	tasksList  *tview.List
	tasksPanel *tview.Flex
	testView   *tview.TextView
	testPanel  *tview.Flex
	// right is the right-column Flex (Cognition stacked over the
	// active body). Cached so buildRightColumn can read its current
	// size for adaptive height calculations.
	right      *tview.Flex
	input      *tview.InputField
	contextBar *tview.TextView
	footer     *tview.TextView
	statusBar  *tview.TextView
	pages      *tview.Pages
	suggestion *tview.TextView

	startupSplashVisible   bool
	reasoningSplashVisible bool
	cognitionActive        bool

	// activePanel is the name of the right-column body currently
	// showing in the unified right-hand pane ("activity", "tasks",
	// "articles", "code"). It is independent of fullscreen; fullscreen
	// controls visibility of the entire right column.
	activePanel string
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
	app.build()
	app.loadSession()
	app.loadModelsAsync()
	// Persist defaults on first run so the file exists for the
	// in-app editor and so a config write is always recoverable.
	if workspaceRoot != "" {
		if _, err := os.Stat(app.configPath()); os.IsNotExist(err) {
			_ = app.saveConfig(cfg)
		}
	}
	return app
}

func (a *App) Run() error {
	defer a.saveSession()
	defer a.stopSpinner()
	a.running = true
	defer func() { a.running = false }()
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
	a.activity, _, activityPanel = a.newPanel("ACTIVITY", a.palette.BgReasoning, 1)
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
	// Keep slash suggestions lightweight and non-focusable so the input
	// stays fully usable while still showing matching commands inline.
	a.input.SetChangedFunc(func(text string) {
		// Any non-empty text input (including slash commands) exits
		// about mode automatically so the user can chat without
		// having to type /about again to dismiss the splash.
		if a.aboutMode && text != "" {
			a.setAboutMode(false)
		}
		if a.suggestion == nil {
			return
		}
		if !strings.HasPrefix(text, "/") {
			a.suggestion.SetText("")
			return
		}
		cmds := []struct{ cmd, desc string }{
			{"agent", "Open agent selector"},
			{"agentinfo", "View current agent prompt"},
			{"tools", "Toggle tools"},
			{"model", "Choose model"},
			{"scroll", "Scroll transcript"},
			{"clear", "Clear session"},
			{"quit", "Quit application"},
			{"trigger", "Trigger an event"},
			{"event", "Alias for trigger"},
			{"reminder", "Create a reminder"},
			{"task", "Create a task"},
			{"config", "Edit user config (JSON)"},
			{"about", "Open the about / welcome screen"},
		}
		q := strings.ToLower(strings.TrimPrefix(text, "/"))
		var matches []string
		for _, c := range cmds {
			if q == "" || strings.HasPrefix(c.cmd, q) {
				matches = append(matches, fmt.Sprintf("[%s]/%s[-]", a.palette.HexPurple, c.cmd))
			}
		}
		if len(matches) == 0 {
			a.suggestion.SetText("")
			return
		}
		a.suggestion.SetText(strings.Join(matches, "  "))
	})
	a.input.SetBackgroundColor(a.palette.BgInput)
	a.input.SetBorderPadding(1, 0, 2, 2)
	a.input.SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEnter {
			a.submit()
		}
	})

	a.suggestion = tview.NewTextView().SetDynamicColors(true)
	a.suggestion.SetBackgroundColor(a.palette.BgInput)
	a.suggestion.SetTextColor(a.palette.TextDim)
	a.suggestion.SetWrap(false)
	a.suggestion.SetText("")

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
// fit. The cap scales gently with the current terminal width so
// wider screens get visibly wider bubbles (up to a hard maximum),
// and shorter screens still get a usable bubble. The minimum
// guarantees the bubble is always at least 24 cells wide even on
// extremely narrow terminals so it remains readable.
//
// Numbers are tuned for the TUI's 5:3 conversation/right-column
// split with marginX insets: on an 80-col terminal the
// conversation body has ~41 cells of usable text, so a cap of
// 38 leaves 3 cells of breathing room (1 left + 2 right padding).
const (
	userMessageMinWidth = 24
	userMessageMaxWidth = 80
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
	if body > userMessageMaxWidth {
		return userMessageMaxWidth
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
	// One-line/column padding around the message text.
	padding := 1
	width := maxLen + padding*2
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
		AddItem(a.input, 2, 0, true).
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
		"[%s]agent:[-] [%s::b]%s[-:-:-] · %s   [%s][Ctl+A][-] switch agent",
		a.palette.HexDim, a.palette.HexLavender, agentName,
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
		re := regexp.MustCompile(`(\d+)\s*/\s*(\d+)`)
		if m := re.FindStringSubmatchIndex(contextText); m != nil {
			// m[0], m[1] are the matched span; m[2], m[3] are group 1 span; m[4], m[5] group 2
			usedStr := contextText[m[2]:m[3]]
			totalStr := contextText[m[4]:m[5]]
			used := 0
			total := 0
			fmt.Sscanf(usedStr, "%d", &used)
			fmt.Sscanf(totalStr, "%d", &total)
			if total > 0 {
				bar := a.renderProgressBar(used, total, 28)
				// Do not include any other context/token data after the tokens phrase.
				// The progress bar already renders the percent and "of <total> tokens." phrase.
				contextText = bar
			} else {
				// leave as-is on parse failure
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
	// Format total with comma separators for human readability
	formattedTotal := formatWithCommas(total)
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
			a.showPanel("code")
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
	case "tasks":
		a.showPanel("tasks")
	case "activity":
		a.showPanel("activity")
	case "test":
		a.showPanel("test")
	case "articles":
		a.showPanel("articles")
	default:
		a.appendActivity(fmt.Sprintf("Unknown command: /%s. Try /agent, /tools, /model, /config, /scroll, /clear, /quit, /task, /memory, /tasks", command))
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

func (a *App) closeSuggestions() {
	if a.suggestion != nil {
		a.suggestion.SetText("")
	}
	a.tv.SetFocus(a.input)
}

func (a *App) openTriggerModal() {
	form := tview.NewForm()
	var name, payload string
	form.AddInputField("Event name", "", 20, nil, func(s string) { name = s })
	form.AddInputField("Payload (optional)", "", 40, nil, func(s string) { payload = s })
	form.AddButton("Trigger", func() {
		a.appendActivity(fmt.Sprintf("Triggered event: %s payload=%s", name, payload))
		a.pages.RemovePage("modal")
		a.tv.SetFocus(a.input)
	})
	form.AddButton("Cancel", func() { a.pages.RemovePage("modal"); a.tv.SetFocus(a.input) })
	form.SetButtonsAlign(tview.AlignRight)
	a.showModal("Trigger Event", form)
}

func (a *App) openReminderModal() {
	form := tview.NewForm()
	var cronExpr, message string
	var install bool
	form.AddInputField("Cron (5 fields)", "", 20, nil, func(s string) { cronExpr = s })
	form.AddInputField("Message", "", 60, nil, func(s string) { message = s })
	form.AddCheckbox("Install to crontab", false, func(checked bool) { install = checked })
	form.AddButton("Save", func() {
		// Call reminder tool asynchronously could be added here.
		a.appendActivity(fmt.Sprintf("Scheduled reminder: %s -> %s (install=%v)", cronExpr, message, install))
		a.pages.RemovePage("modal")
		a.tv.SetFocus(a.input)
	})
	form.AddButton("Cancel", func() { a.pages.RemovePage("modal"); a.tv.SetFocus(a.input) })
	form.SetButtonsAlign(tview.AlignRight)
	a.showModal("Set Reminder", form)
}

func (a *App) openTaskModal() {
	form := tview.NewForm()
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
		a.pages.RemovePage("modal")
		a.tv.SetFocus(a.input)
	})
	form.AddButton("Cancel", func() { a.pages.RemovePage("modal"); a.tv.SetFocus(a.input) })
	form.SetButtonsAlign(tview.AlignRight)
	a.showModal("New Task", form)
}

func (a *App) openMemoryModal() {
	form := tview.NewForm()
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
		a.pages.RemovePage("modal")
		a.tv.SetFocus(a.input)
	})
	form.AddButton("Cancel", func() { a.pages.RemovePage("modal"); a.tv.SetFocus(a.input) })
	form.SetButtonsAlign(tview.AlignRight)
	a.showModal("New Memory", form)
}

// openConfigModal shows a small JSON editor preloaded with the
// current AppConfig. The user can edit values in place and hit
// "Save" to persist; "Cancel" discards changes. Validation errors
// are surfaced in the activity log rather than as a popup so the
// editor stays focused for quick corrections.
func (a *App) openConfigModal() {
	editor := tview.NewTextArea()
	editor.SetBackgroundColor(a.palette.BgModal)
	editor.SetBorder(true).
		SetTitle(" config.json — edit values, Ctrl+S to save, Esc to cancel ").
		SetTitleAlign(tview.AlignLeft)
	editor.SetText(formatConfigJSON(a.config), true)

	help := tview.NewTextView().SetDynamicColors(true)
	help.SetBackgroundColor(a.palette.BgModal)
	help.SetTextColor(a.palette.TextDim)
	help.SetText(fmt.Sprintf(" [%s]editing:[-] %s   [%s]hint:[-] toolMax caps tool steps per turn",
		a.palette.HexFaint, a.configPath(), a.palette.HexFaint))

	buttons := tview.NewFlex()
	buttons.SetBackgroundColor(a.palette.BgModal)

	// capture by value so Cancel always closes over the editor it
	// opened, even after a future Save replaces a.config.
	closeModal := func() {
		a.pages.RemovePage("modal")
		a.tv.SetFocus(a.input)
	}
	save := func() {
		parsed, err := parseConfigJSON(editor.GetText())
		if err != nil {
			a.appendActivity(fmt.Sprintf("["+a.palette.HexOrchid+"::b]config invalid[-:-:-]: %v", err))
			return
		}
		if err := a.saveConfig(parsed); err != nil {
			a.appendActivity(fmt.Sprintf("["+a.palette.HexOrchid+"::b]config save failed[-:-:-]: %v", err))
			return
		}
		a.config = parsed
		a.runner.MaxSteps = parsed.ToolMax
		a.appendActivity(fmt.Sprintf("Saved config to %s (toolMax=%d)", a.configPath(), parsed.ToolMax))
		closeModal()
	}

	saveBtn := tview.NewButton("Save").SetSelectedFunc(save)
	cancelBtn := tview.NewButton("Cancel").SetSelectedFunc(closeModal)
	for _, b := range []*tview.Button{saveBtn, cancelBtn} {
		b.SetBackgroundColor(a.palette.BgModal)
		b.SetLabelColor(a.palette.TextMain)
	}
	buttons.AddItem(saveBtn, 0, 1, false)
	buttons.AddItem(cancelBtn, 0, 1, false)

	body := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(help, 1, 0, false).
		AddItem(editor, 0, 1, true).
		AddItem(buttons, 1, 0, false)
	body.SetBackgroundColor(a.palette.BgModal)

	// Ctrl+S saves, Esc cancels. We wire these on the TextArea's
	// input capture so they work regardless of focus.
	editor.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyEsc:
			closeModal()
			return nil
		case tcell.KeyCtrlS:
			save()
			return nil
		}
		return event
	})

	a.showModal("Config", body)
}

func (a *App) openAgentInfoModal() {
	body := tview.NewTextView().SetDynamicColors(true).SetWrap(true)
	body.SetBackgroundColor(a.palette.BgModal)
	body.SetTextColor(a.palette.TextMain)
	prompt := strings.TrimSpace(a.currentAgent.Prompt)
	if prompt == "" {
		prompt = "<no system prompt available>"
	}
	body.SetText(fmt.Sprintf("[yellow]Agent:[-] %s\n[yellow]Title:[-] %s\n[yellow]Default model:[-] %s\n[yellow]Tools:[-] %s\n\n[yellow]System prompt:[-]\n%s",
		a.currentAgent.Name,
		a.currentAgent.Title,
		a.currentAgent.DefaultModel,
		strings.Join(a.currentAgent.ToolNames, ", "),
		prompt,
	))

	closeButton := tview.NewButton("Close").SetSelectedFunc(func() {
		a.pages.RemovePage("modal")
		a.tv.SetFocus(a.input)
	})
	closeButton.SetBackgroundColor(a.palette.BgModal)
	closeButton.SetLabelColor(a.palette.TextMain)

	container := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(body, 0, 1, false).
		AddItem(closeButton, 1, 0, true)
	container.SetBackgroundColor(a.palette.BgModal)
	a.showModal("Agent Info", container)
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
		prompt = "Please review recent messages and output a one paragraph recap of the discussion which will be appended to the cognition pane of the tj coder CLI. The user has not yet sent a message; produce a brief welcome that summarises the current session context (workspace, model, agent) and what kinds of tasks the user might want help with."
	} else {
		prompt = "Please review recent messages and output a one paragraph recap of the discussion which will be appended to the cognition pane of the tj coder CLI."
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
		fmt.Fprintf(a.reasoning, "\n\n [%s]RECAP[-]\n", a.palette.HexPurple)
		fmt.Fprintf(a.reasoning, "[%s]%s[-]\n", a.palette.TextMain, recapped)
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
	case "articles":
		a.setActivePanel("articles")
		a.activity.SetText("[articles] Not implemented yet\n")
		a.rebuildLayout()
		a.focusInput()
		return
	case "code":
		a.setActivePanel("code")
		a.refreshCodePanel()
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

// refreshCodePanel renders the placeholder "code" view into the
// right-column body. It is intentionally minimal until the dedicated
// code-reference feature lands.
func (a *App) refreshCodePanel() {
	if a.activity == nil {
		return
	}
	a.activity.SetText(fmt.Sprintf(
		"[%s::b] CODE[-:-:-]\n\n[%s]Code references will appear here. The agent can call /ui_control to open files and line ranges in this panel. Use [%s]Alt+T[-] to switch back to tasks.[-]",
		a.palette.HexPurple,
		a.palette.HexLavender, a.palette.HexLavender,
	))
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

func (a *App) submit() {
	a.mu.Lock()
	if a.busy {
		a.mu.Unlock()
		return
	}
	prompt := strings.TrimSpace(a.input.GetText())
	if prompt == "" {
		a.mu.Unlock()
		return
	}

	// Handle slash commands
	if strings.HasPrefix(prompt, "/") {
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
	// on a quiet session. The recap header (the RECAP label) remains the
	// sole visual marker for fresh cognition content.
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

	enabled := a.enabledToolList()
	history := append([]client.Message(nil), a.history...)
	agentCfg := a.currentAgent
	model := a.currentModel

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

func (a *App) handleEvent(event tooling.Event) {
	a.tv.QueueUpdateDraw(func() {
		switch event.Type {
		case tooling.EventReasoning:
			a.assistantState = "thinking"
			a.clearReasoningSplash()
			a.cognitionActive = true
			fmt.Fprint(a.reasoning, event.Text)
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
				parts := strings.Split(strings.TrimSpace(event.Text), ":")
				if len(parts) >= 3 {
					panel := parts[1]
					action := parts[2]
					switch strings.ToLower(action) {
					case "show", "toggle":
						a.showPanel(panel)
					case "hide":
						a.showPanel("activity")
					}
					// Log the UI action succinctly
					a.appendActivity(fmt.Sprintf("[%s]ui_control: %s %s", a.palette.HexLavender, panel, action))
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
	return t.Format("3:04:05 PM")
}

func (a *App) highlightTranscriptText(text string) string {
	if text == "" {
		return ""
	}
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

func (a *App) appendActivity(line string) {
	stamp := formatTimestamp(time.Now())
	fmt.Fprintf(a.activity, "["+a.palette.HexFaint+"]%s[-] %s\n", stamp, line)
	a.activity.ScrollToEnd()
}

// appendUserMessage renders the user's prompt as a filled ergo-a.palette.Purple
// rectangular bubble in the conversation panel, using the same padding
// standard as the rest of the TUI.
func (a *App) appendUserMessage(prompt string) {
	if a.transcript == nil {
		return
	}
	a.clearStartupSplash()
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
	case "test":
		bodyPrim = a.testPanel
	case "articles", "code":
		// Articles and code currently reuse the activity TextView
		// for their (placeholder) content, so the active body
		// stays the same primitive.
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
		mainFlex = tview.NewFlex().
			AddItem(a.transcriptPanel, 0, 5, false).
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
			if a.currentModel == "" {
				a.currentModel = agentCfg.DefaultModel
			}
			a.refreshHeader()
			a.appendActivity("Selected agent: " + agentCfg.Name)
			a.appendActivity("Enabled tools: " + strings.Join(a.enabledToolList(), ", "))
			a.saveSession()
			a.pages.RemovePage("modal")
			a.tv.SetFocus(a.input)
		})
	}
	list.SetDoneFunc(func() {
		a.pages.RemovePage("modal")
		a.tv.SetFocus(a.input)
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
			a.refreshHeader()
			a.appendActivity("Selected model: " + item.Name)
			// Remember the pick as the shared cross-mode model so it
			// persists across program sessions and workspaces (and is
			// honored by headless runs too).
			_ = session.SetLastModel(true, item.Name)
			a.saveSession()
			a.pages.RemovePage("modal")
			a.tv.SetFocus(a.input)
		})
	}
	list.SetDoneFunc(func() {
		a.pages.RemovePage("modal")
		a.tv.SetFocus(a.input)
	})
	a.showModal("Select Model", list)
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
		a.pages.RemovePage("modal")
		a.tv.SetFocus(a.input)
	})
	a.showModal("Toggle Tools (Enter toggles, Esc closes)", list)
}

func (a *App) showModal(title string, primitive tview.Primitive) {
	label := tview.NewTextView().SetDynamicColors(true)
	label.SetBackgroundColor(a.palette.BgModal)
	label.SetText(fmt.Sprintf(" [%s]%s[-]", a.palette.HexFaint, strings.ToUpper(title)))
	label.SetBorderPadding(0, 0, 2, 2)

	panel := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(label, 1, 0, false).
		AddItem(primitive, 0, 1, true)
	panel.SetBackgroundColor(a.palette.BgModal)

	modal := tview.NewFlex().
		AddItem(nil, 0, 1, false).
		AddItem(panel, 0, 2, true).
		AddItem(nil, 0, 1, false)
	wrapper := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(nil, 0, 1, false).
		AddItem(modal, 0, 2, true).
		AddItem(nil, 0, 1, false)
	wrapper.SetBackgroundColor(a.palette.BgRoot)
	a.pages.AddAndSwitchToPage("modal", wrapper, true)
	a.tv.SetFocus(primitive)
}

func (a *App) styleModalList(list *tview.List) {
	list.SetBackgroundColor(a.palette.BgModal)
	list.SetBorderPadding(1, 1, 2, 2)
	list.SetMainTextColor(a.palette.TextMain)
	list.SetSecondaryTextColor(a.palette.TextDim)
	list.SetSelectedBackgroundColor(a.palette.BgSelect)
	list.SetSelectedTextColor(a.palette.Lavender)
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
