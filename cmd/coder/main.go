package main

import (
	"bufio"
	"context"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/alpha-tjcoder/coder-cli/internal/agent"
	"github.com/alpha-tjcoder/coder-cli/internal/client"
	ctxpkg "github.com/alpha-tjcoder/coder-cli/internal/context"
	"github.com/alpha-tjcoder/coder-cli/internal/session"
	"github.com/alpha-tjcoder/coder-cli/internal/tooling"
	"github.com/alpha-tjcoder/coder-cli/internal/tools"
	"github.com/alpha-tjcoder/coder-cli/internal/tracking"
	"github.com/alpha-tjcoder/coder-cli/internal/tui"
)

const (
	defaultHost        = "http://localhost:11434"
	defaultGeminiModel = "gemini-2.5-flash"
	defaultProvider    = "ollama"
)

// packageJSON is the package.json shipped with the binary, embedded at
// compile time. It backs the fallback values for version/productName/author
// when those have not been injected via -ldflags (e.g. plain `go run` or
// an ad-hoc `go build`).
//
//go:embed package.json
var packageJSON embed.FS

// packageMeta is a minimal view of package.json used to seed ldflag
// fallbacks.
type packageMeta struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Author  string `json:"author"`
}

func loadPackageMeta() packageMeta {
	data, err := packageJSON.ReadFile("package.json")
	if err != nil {
		return packageMeta{}
	}
	var m packageMeta
	_ = json.Unmarshal(data, &m) // best-effort: empty struct on failure
	return m
}

// These are injected at build time from package.json via -ldflags
// (see the Makefile). When unset, they fall back to the embedded copy of
// package.json so `go run` and ad-hoc builds still report a useful version.
var (
	version = func() string {
		if v := loadPackageMeta().Version; v != "" {
			return v
		}
		return "dev"
	}()

	productName = func() string {
		if n := loadPackageMeta().Name; n != "" {
			return n
		}
		return "TJ Coder CLI"
	}()

	author = func() string {
		if a := loadPackageMeta().Author; a != "" {
			return a
		}
		return "TJ Coder AI Labs"
	}()
)

func main() {
	versionFlag := flag.Bool("version", false, "print version and exit")
	flag.BoolVar(versionFlag, "v", false, "print version and exit (shorthand)")
	host := flag.String("host", defaultHost, "Ollama base URL (ignored for gemini provider)")
	geminiKey := flag.String("gemini-api-key", os.Getenv("GEMINI_API_KEY"), "Gemini API key (or set GEMINI_API_KEY)")
	model := flag.String("model", "", "override the default model")
	timeout := flag.Duration("timeout", defaultTimeout(), "HTTP request timeout (e.g. 30s, 10m, 0 to disable)")
	workspaceRoot := flag.String("workspace-root", mustCWD(), "workspace root")
	providerName := flag.String("provider", defaultProvider, "override the default provider")

	// Headless (non-interactive) flags. Setting any of these — or providing a
	// positional "ask" subcommand — routes execution through cmdAsk instead of
	// the tview TUI, so the binary can be driven from scripts, CI, and shells
	// without a real terminal.
	prompt := flag.String("p", "", "one-shot prompt (headless; -p \"...\", or use `ask` subcommand, or pipe stdin)")
	flag.StringVar(prompt, "prompt", "", "one-shot prompt (alias for -p)")
	askAgent := flag.String("agent", "software-engineer", "agent name to use in headless ask mode")
	askNoTools := flag.Bool("no-tools", false, "headless ask mode: disable all tools (plain chat)")
	askAllTools := flag.Bool("all-tools", false, "headless ask mode: enable every tool in the registry (ignores agent default)")
	askShowReasoning := flag.Bool("show-reasoning", false, "headless ask mode: also print <think>...</think> blocks to stderr")
	askQuiet := flag.Bool("quiet", false, "headless ask mode: suppress tool activity on stderr")
	askSystem := flag.String("system", "", "headless ask mode: extra text prepended to the system prompt")
	askSession := flag.Bool("session", true, "headless ask mode: load and save session history from/to the workspace session file")
	askFormat := flag.String("format", "text", "headless ask mode: coerce model output — one of {text, json, xml}. For json/xml the response is extracted, validated, and pretty-printed; tool activity remains enabled unless --no-tools is also set.")
	flag.StringVar(askFormat, "output-format", "", "alias for --format (overrides --format when set)")

	flag.Parse()

	var explicitAgent, explicitModel, explicitNoTools, explicitAllTools bool
	flag.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "agent":
			explicitAgent = true
		case "model":
			explicitModel = true
		case "no-tools":
			explicitNoTools = true
		case "all-tools":
			explicitAllTools = true
		}
	})

	if *versionFlag {
		fmt.Printf("%s %s\n", productName, version)
		fmt.Printf("Developed by %s\n", author)
		return
	}

	if flag.NArg() > 0 {
		switch flag.Arg(0) {
		case "version", "-v", "--version":
			fmt.Printf("%s %s\n", productName, version)
			fmt.Printf("Developed by %s\n", author)
			return
		case "models":
			cmdModels(flag.Args()[1:], *providerName, *host, *geminiKey, *timeout)
			return
		case "agent":
			cmdAgent(flag.Args()[1:], *workspaceRoot)
			return
		case "ask":
			// `coder ask [text...]` — text after "ask" is the prompt.
			// If no text is given, read from stdin.
			text := strings.TrimSpace(strings.Join(flag.Args()[1:], " "))
			if text == "" {
				stdin, err := readStdin()
				if err != nil {
					fatal("read stdin: %v", err)
				}
				text = strings.TrimSpace(stdin)
			}
			if text == "" {
				fatal("ask: empty prompt (provide text after `ask` or pipe via stdin)")
			}
			cmdAsk(askOptions{
				Host:          *host,
				Provider:      *providerName,
				GeminiKey:     *geminiKey,
				WorkspaceRoot: *workspaceRoot,
				Model:         *model,
				Timeout:       *timeout,
				Agent:         *askAgent,
				NoTools:       *askNoTools,
				AllTools:      *askAllTools,
				ShowReasoning: *askShowReasoning,
				Quiet:         *askQuiet,
				System:        *askSystem,
				Prompt:        text,
				Session:       *askSession,
				Format:        resolveFormatFlag(*askFormat),
				ExplicitAgent: explicitAgent,
				ExplicitModel: explicitModel,
				ExplicitTools: explicitNoTools || explicitAllTools,
			})
			return
		default:
			fatal("unknown command %q", flag.Arg(0))
		}
	}

	// If a one-shot prompt was provided via -p/--prompt (or stdin is piped
	// and non-empty), run headless ask mode even with no subcommand. This
	// makes `echo hi | coder` and `coder -p "hi"` work as you'd expect.
	if *prompt != "" || isStdinPiped() {
		text := strings.TrimSpace(*prompt)
		if text == "" {
			stdin, err := readStdin()
			if err != nil {
				fatal("read stdin: %v", err)
			}
			text = strings.TrimSpace(stdin)
		}
		if text == "" {
			fatal("empty prompt (provide -p \"...\" or pipe via stdin)")
		}
		cmdAsk(askOptions{
			Host:          *host,
			Provider:      *providerName,
			GeminiKey:     *geminiKey,
			WorkspaceRoot: *workspaceRoot,
			Model:         *model,
			Timeout:       *timeout,
			Agent:         *askAgent,
			NoTools:       *askNoTools,
			AllTools:      *askAllTools,
			ShowReasoning: *askShowReasoning,
			Quiet:         *askQuiet,
			System:        *askSystem,
			Prompt:        text,
			Session:       *askSession,
			Format:        resolveFormatFlag(*askFormat),
			ExplicitAgent: explicitAgent,
			ExplicitModel: explicitModel,
			ExplicitTools: explicitNoTools || explicitAllTools,
		})
		return
	}

	provider, err := client.NewProvider(*providerName, *host, *geminiKey, *timeout)
	if err != nil {
		fatal("%v", err)
	}

	// For Gemini, default to a sensible chat model when none is specified.
	modelOverride := *model
	if *providerName == "gemini" && modelOverride == "" {
		modelOverride = defaultGeminiModel
	}

	registry := tools.NewRegistry(provider)
	app := tui.New(provider, registry, filepath.Clean(*workspaceRoot), modelOverride, productName, author, version)
	if err := app.Run(); err != nil {
		fatal("%v", err)
	}
}

func cmdModels(args []string, providerName, host, geminiKey string, timeout time.Duration) {
	// Subcommand dispatch: `coder models` lists, `coder models info <name>`
	// prints provenance (modelfile, adapters, parameters, template, license)
	// for a single model. Default behavior is unchanged: a bare
	// `coder models` continues to print the list of available models.
	if len(args) == 0 {
		cmdModelsList(providerName, host, geminiKey, timeout)
		return
	}
	switch args[0] {
	case "list":
		cmdModelsList(providerName, host, geminiKey, timeout)
	case "info":
		cmdModelsInfo(args[1:], providerName, host, geminiKey, timeout)
	default:
		fmt.Fprintf(os.Stderr, "usage: coder models [list|info <name>]\n")
		os.Exit(2)
	}
}

func cmdModelsList(providerName, host, geminiKey string, timeout time.Duration) {
	provider, err := client.NewProvider(providerName, host, geminiKey, timeout)
	if err != nil {
		fatal("%v", err)
	}
	models, err := provider.ListModels(nil)
	if err != nil {
		fatal("list models: %v", err)
	}
	for _, m := range models {
		fmt.Printf("%-36s  %s\n", m.Name, m.ParameterSize)
	}
}

// cmdModelsInfo prints the provenance / configuration of a single model
// so the user can identify its base architecture, any adapters applied,
// and the chat template / parameters it's running with. This is the
// command to run when the user wants to know "is this model a fine-tune
// of an open-source base, and if so which one?".
func cmdModelsInfo(args []string, providerName, host, geminiKey string, timeout time.Duration) {
	if len(args) != 1 {
		fmt.Fprintf(os.Stderr, "usage: coder models info <model-name>\n")
		os.Exit(2)
	}
	name := args[0]
	provider, err := client.NewProvider(providerName, host, geminiKey, timeout)
	if err != nil {
		fatal("%v", err)
	}
	// ShowModel is an optional capability — only some providers
	// (Ollama and most Ollama-compatible services) can report
	// modelfile provenance. Type-assert instead of requiring every
	// Provider to implement it.
	pv, ok := provider.(client.ProvenanceProvider)
	if !ok {
		fmt.Fprintf(os.Stderr, "%s: provider %q does not support model provenance lookup\n", productName, providerName)
		os.Exit(2)
	}
	details, err := pv.ShowModel(nil, name)
	if err != nil {
		fatal("show model %q: %v", name, err)
	}
	// Output is human-readable by default so the user can scan
	// modelfile lines quickly. We deliberately keep it plain text;
	// JSON / YAML can be added later if anyone needs to parse it.
	fmt.Printf("name:       %s\n", details.Name)
	if len(details.Adapters) > 0 {
		fmt.Printf("adapters:   %s\n", strings.Join(details.Adapters, ", "))
	}
	if v := details.Details["family"]; v != "" {
		fmt.Printf("family:     %s\n", v)
	}
	if v := details.Details["parent_model"]; v != "" {
		fmt.Printf("parent:     %s\n", v)
	}
	if v := details.Details["parameter_size"]; v != "" {
		fmt.Printf("size:       %s\n", v)
	}
	if v := details.Details["quantization_level"]; v != "" {
		fmt.Printf("quant:      %s\n", v)
	}
	if v := details.Details["context_window"]; v != "" {
		fmt.Printf("ctx_window: %s\n", v)
	}
	if details.License != "" {
		fmt.Printf("license:    %s\n", details.License)
	}
	if details.Modelfile != "" {
		fmt.Printf("\nmodelfile:\n%s\n", details.Modelfile)
	}
	if details.Parameters != "" {
		fmt.Printf("\nparameters:\n%s\n", details.Parameters)
	}
	if details.Template != "" {
		fmt.Printf("\ntemplate:\n%s\n", details.Template)
	}
}

func defaultTimeout() time.Duration {
	raw := os.Getenv("ERGO_HTTP_TIMEOUT")
	if raw == "" {
		return client.DefaultRequestTimeout
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		fatal("invalid ERGO_HTTP_TIMEOUT %q: %v", raw, err)
	}
	return d
}

func mustCWD() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, productName+": "+format+"\n", args...)
	os.Exit(1)
}

// askOptions bundles the headless ask-mode inputs so cmdAsk's signature
// stays readable as we add flags.
type askOptions struct {
	Host          string
	Provider      string
	GeminiKey     string
	WorkspaceRoot string
	Model         string
	Timeout       time.Duration
	Agent         string
	NoTools       bool
	AllTools      bool
	ShowReasoning bool
	Quiet         bool
	System        string
	Prompt        string
	Session       bool
	ExplicitAgent bool
	ExplicitModel bool
	ExplicitTools bool
	// Format coerces model output to a specific representation. Valid
	// values are "text" (default, no coercion), "json" (extract the
	// first balanced JSON value, validate, and pretty-print), and
	// "xml" (extract the first balanced XML element, validate via
	// encoding/xml, and pretty-print). Coercion is achieved via
	// system-prompt instructions and post-processing rather than
	// per-provider "format" parameters so it works uniformly across
	// Ollama, Gemini, and any future provider.
	Format string
}

type persistedSession struct {
	CurrentAgent string           `json:"current_agent"`
	CurrentModel string           `json:"current_model"`
	EnabledTools []string         `json:"enabled_tools"`
	History      []client.Message `json:"history"`
	ContextInfo  string           `json:"context_info"`
	RefOrder     []string         `json:"ref_order"`
	Transcript   string           `json:"transcript"`
	Reasoning    string           `json:"reasoning"`
	Activity     string           `json:"activity"`
}

// cmdAsk runs a single prompt end-to-end without launching the tview TUI.
// The final assistant reply is written to stdout. Tool activity (and
// optionally reasoning) is streamed to stderr so it doesn't pollute the
// response that scripts are trying to capture.
func isReasoningRelated(text string) bool {
	lower := strings.ToLower(text)
	return strings.Contains(lower, "think") ||
		strings.Contains(lower, "reasoning") ||
		strings.Contains(lower, "<think>") ||
		strings.Contains(lower, "model note")
}

func cmdAsk(opts askOptions) {
	sessionPath := filepath.Join(opts.WorkspaceRoot, ".ergo-cli-go", "session.json")
	var sessionState persistedSession
	sessionExists := false

	if opts.Session {
		data, err := os.ReadFile(sessionPath)
		if err == nil {
			if err := json.Unmarshal(data, &sessionState); err == nil {
				sessionExists = true
			}
		}
	}

	agentName := opts.Agent
	if !opts.ExplicitAgent && sessionExists && sessionState.CurrentAgent != "" {
		agentName = sessionState.CurrentAgent
	}

	agentCfg, ok := agent.FindWithWorkspace(agentName, opts.WorkspaceRoot)
	if !ok {
		fatal("unknown agent %q (available: %s)", agentName, agentNameList(opts.WorkspaceRoot))
	}

	// Allow -system to override the agent's prompt entirely (useful for
	// ad-hoc system instructions in CI). Otherwise layer it on top.
	if strings.TrimSpace(opts.System) != "" {
		agentCfg.Prompt = opts.System
	} else if opts.System != "" {
		// Empty but non-blank override → no agent prompt at all.
		agentCfg.Prompt = ""
	}

	// --format injection. We append a short, model-agnostic instruction
	// to the system prompt asking for output in the requested shape.
	// Doing this in the prompt (rather than via per-provider "format"
	// fields) means the same flag works for any provider and any model
	// without us having to branch on Gemini's responseMimeType vs
	// Ollama's format field vs whatever comes next. Post-processing
	// below extracts and validates the payload before we hand it to
	// stdout, so a sloppy model that adds prose around the JSON/XML
	// doesn't break scripts that consume the output.
	formatInstruction := formatInstructionFor(opts.Format)
	if formatInstruction != "" {
		if agentCfg.Prompt != "" {
			agentCfg.Prompt = agentCfg.Prompt + "\n\n" + formatInstruction
		} else {
			agentCfg.Prompt = formatInstruction
		}
	}

	// Inject execution context at the top of the agent's prompt
	if agentCfg.Prompt != "" {
		ctx := ctxpkg.Build(os.Args[0], version, opts.WorkspaceRoot)
		agentCfg.Prompt = ctx.FormatPrompt() + "\n" + agentCfg.Prompt
	}

	provider, err := client.NewProvider(opts.Provider, opts.Host, opts.GeminiKey, opts.Timeout)
	if err != nil {
		fatal("%v", err)
	}

	model := opts.Model
	if !opts.ExplicitModel && sessionExists && sessionState.CurrentModel != "" {
		model = sessionState.CurrentModel
	}
	if model == "" {
		model = agentCfg.DefaultModel
	}
	if opts.Provider == "gemini" && model == agentCfg.DefaultModel && agentCfg.DefaultModel != defaultGeminiModel {
		// Only fall back to the Gemini default when the user didn't pick
		// something explicit and the agent's default isn't already a Gemini
		// model name. The reference agents all use Ollama-style names, so
		// this is the common case.
		model = defaultGeminiModel
	}

	registry := tools.NewRegistry(provider)

	// Load full session state for tools that mutate session (tasks, articles).
	fullState, _, fullErr := session.Load(opts.WorkspaceRoot)
	if fullErr != nil {
		// Non-fatal: continue with a zero state; manage_items will persist later
		fullState = session.State{}
	}

	// Register tracking-backed tools (task manager) with access to session state.
	trackReg := tracking.NewRegistry()
	trackReg.Register(tracking.NewTaskTracker())
	manageItems := tools.NewManageItems(trackReg, &fullState)
	registry.RegisterTool(tools.ManageItemsBridge{Impl: manageItems})

	enabled := agentCfg.ToolNames
	if !opts.ExplicitTools && sessionExists && len(sessionState.EnabledTools) > 0 {
		enabled = sessionState.EnabledTools
	} else {
		switch {
		case opts.NoTools:
			enabled = nil
		case opts.AllTools:
			enabled = registry.Names()
		}
	}

	runner := &tooling.Runner{
		Provider:      provider,
		Registry:      registry,
		WorkspaceRoot: filepath.Clean(opts.WorkspaceRoot),
		MaxSteps:      8,
	}

	if !opts.Quiet {
		fmt.Fprintf(os.Stderr, "%s ask: agent=%s model=%s provider=%s workspace=%s tools=%d\n",
			productName, agentCfg.Name, model, opts.Provider, opts.WorkspaceRoot, len(enabled))
	}

	// Stream everything except the final assistant text to stderr. The
	// final answer is captured separately so we can write it as a single
	// block to stdout (no interleaving with tool noise).
	var (
		stdoutWriter  io.Writer = os.Stdout
		stderrWriter  io.Writer = os.Stderr
		commentaryBuf strings.Builder
		reasoningBuf  strings.Builder
		activityBuf   strings.Builder
	)
	_ = stdoutWriter

	onEvent := func(ev tooling.Event) {
		switch ev.Type {
		case tooling.EventReasoning:
			if ev.Text != "" {
				reasoningBuf.WriteString(ev.Text)
				if opts.ShowReasoning {
					fmt.Fprint(stderrWriter, ev.Text)
				}
			}
		case tooling.EventCommentary:
			// Skip mid-stream commentary to keep stdout clean. The final
			// assistant message is appended to history by the runner and
			// emitted as the last "message" with no tool calls — we print
			// its content below from history.
			commentaryBuf.WriteString(ev.Text)
		case tooling.EventToolStart:
			line := fmt.Sprintf("[#7C3AED]→ %s[-] %s\n", ev.ToolName, ev.Text)
			activityBuf.WriteString(line)
			if !opts.Quiet {
				fmt.Fprintf(stderrWriter, "→ %s %s\n", ev.ToolName, strings.TrimSpace(ev.Text))
			}
		case tooling.EventToolResult:
			line := fmt.Sprintf("[#C4A5FF::b]✓ %s[-:-:-] %s\n", ev.ToolName, ev.Text)
			activityBuf.WriteString(line)
			if !opts.Quiet {
				fmt.Fprintf(stderrWriter, "✓ %s %s\n", ev.ToolName, ev.Text)
			}
		case tooling.EventError:
			var line string
			if isReasoningRelated(ev.Text) {
				line = fmt.Sprintf("\n[#C73CDC::b]✗ %s[-:-:-] %s\n", ev.ToolName, ev.Text)
				reasoningBuf.WriteString(line)
			} else {
				line = fmt.Sprintf("[#C73CDC::bu]✗ %s[-:-:-] %s\n", ev.ToolName, ev.Text)
				activityBuf.WriteString(line)
			}
			if !opts.Quiet {
				fmt.Fprintf(stderrWriter, "✗ %s %s\n", ev.ToolName, ev.Text)
			}
		case tooling.EventContext:
			if !opts.Quiet {
				fmt.Fprintf(stderrWriter, "%s\n", ev.Text)
			}
		}
	}

	var history []client.Message
	if sessionExists {
		history = sessionState.History
	}

	newHistory, err := runner.Run(context.Background(), history, opts.Prompt, agentCfg, model, enabled, onEvent)

	if opts.Session && len(newHistory) > 0 {
		sessionState.CurrentAgent = agentCfg.Name
		sessionState.CurrentModel = model
		sessionState.EnabledTools = enabled
		sessionState.History = newHistory

		if sessionState.Transcript != "" && !strings.HasSuffix(sessionState.Transcript, "\n") {
			sessionState.Transcript += "\n"
		}
		sessionState.Transcript += fmt.Sprintf("[#C4A5FF::b]You[-:-:-]\n%s\n\n[#A77CF8::b]Assistant[-:-:-]\n%s\n\n", opts.Prompt, commentaryBuf.String())

		sessionState.Reasoning += reasoningBuf.String()
		sessionState.Activity += activityBuf.String()

		dir := filepath.Dir(sessionPath)
		_ = os.MkdirAll(dir, 0o700)
		if data, jsonErr := json.MarshalIndent(sessionState, "", "  "); jsonErr == nil {
			_ = os.WriteFile(sessionPath, data, 0o600)
		}
	}

	if err != nil {
		fatal("ask: %v", err)
	}

	// The last message in history is the final assistant reply (the runner
	// only returns when msg.ToolCalls is empty).
	final := lastAssistantMessage(newHistory)
	if final == "" {
		return
	}
	emitFormatted(stdoutWriter, stderrWriter, opts.Format, final, opts.Quiet)
}

// emitFormatted writes the model's final assistant message to stdout
// in the shape the user requested. For "text" (the default) it just
// prints the raw reply. For "json" or "xml" it tries to extract the
// first balanced value, validates it, pretty-prints it, and warns on
// stderr if extraction failed (so the user knows the model didn't
// comply strictly — but the raw text is still surfaced for debugging).
func emitFormatted(stdoutWriter, stderrWriter io.Writer, format, text string, quiet bool) {
	format = strings.ToLower(strings.TrimSpace(format))
	switch format {
	case "", "text":
		fmt.Fprintln(stdoutWriter, strings.TrimRight(text, "\n"))
	case "json":
		pretty, ok := extractAndPrettyJSON(text)
		if !ok {
			if !quiet {
				fmt.Fprintf(stderrWriter, "warning: --format json requested but no valid JSON value was found in the model output; emitting raw text\n")
			}
			fmt.Fprintln(stdoutWriter, strings.TrimRight(text, "\n"))
			return
		}
		fmt.Fprintln(stdoutWriter, pretty)
	case "xml":
		pretty, ok := extractAndPrettyXML(text)
		if !ok {
			if !quiet {
				fmt.Fprintf(stderrWriter, "warning: --format xml requested but no well-formed XML element was found in the model output; emitting raw text\n")
			}
			fmt.Fprintln(stdoutWriter, strings.TrimRight(text, "\n"))
			return
		}
		fmt.Fprintln(stdoutWriter, pretty)
	default:
		// Should be impossible: resolveFormatFlag rejects unknowns.
		fmt.Fprintln(stdoutWriter, strings.TrimRight(text, "\n"))
	}
}

func lastAssistantMessage(history []client.Message) string {
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role == "assistant" {
			return history[i].Content
		}
	}
	return ""
}

// resolveFormatFlag normalises the user-supplied --format value to one
// of the canonical lower-case tokens we recognise. Empty input
// defaults to "text". An unknown value is a fatal error so a typo
// doesn't silently fall through to plain-text output (which would
// defeat the entire point of --format for a script consumer).
func resolveFormatFlag(raw string) string {
	v := strings.ToLower(strings.TrimSpace(raw))
	switch v {
	case "", "text", "plain":
		return "text"
	case "json":
		return "json"
	case "xml":
		return "xml"
	default:
		fatal("invalid --format %q (expected one of: text, json, xml)", raw)
		return ""
	}
}

// formatInstructionFor returns the system-prompt snippet that
// instructs the model to respond in the requested shape. Returning
// "" for "text" preserves existing behavior exactly. The instructions
// are deliberately short and explicit so a small local model (the
// common case for this CLI) can follow them reliably.
func formatInstructionFor(format string) string {
	switch format {
	case "json":
		return "Output format: Respond with a single, valid JSON value (object or array) and nothing else. No prose, no markdown fences, no commentary before or after the JSON. If you must explain, embed the explanation as a string field inside the JSON."
	case "xml":
		return "Output format: Respond with a single, well-formed XML element (with an explicit root tag) and nothing else. No prose, no markdown fences, no commentary before or after the XML. If you must explain, embed it as a child element or text node inside the root element."
	default:
		return ""
	}
}

func agentNameList(workspaceRoot string) string {
	all := agent.AllWithWorkspace(workspaceRoot)
	names := make([]string, 0, len(all))
	for _, cfg := range all {
		names = append(names, cfg.Name)
	}
	return strings.Join(names, ", ")
}

func cmdAgent(args []string, workspaceRoot string) {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "usage: coder agent <list|show|add|remove> [options]\n")
		os.Exit(2)
	}

	switch args[0] {
	case "list":
		cmdAgentList(workspaceRoot)
	case "show":
		cmdAgentShow(args[1:], workspaceRoot)
	case "add":
		cmdAgentAdd(args[1:], workspaceRoot)
	case "remove":
		cmdAgentRemove(args[1:], workspaceRoot)
	default:
		fatal("unknown agent subcommand %q", args[0])
	}
}

func cmdAgentList(workspaceRoot string) {
	all := agent.AllWithWorkspace(workspaceRoot)
	for _, cfg := range all {
		fmt.Printf("%s\t%s\n", cfg.Name, cfg.DisplayName)
	}
}

func cmdAgentShow(args []string, workspaceRoot string) {
	fs := flag.NewFlagSet("coder agent show", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: coder agent show <name>\n")
	}
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	if fs.NArg() != 1 {
		fs.Usage()
		os.Exit(2)
	}
	name := fs.Arg(0)
	cfg, ok := agent.FindWithWorkspace(name, workspaceRoot)
	if !ok {
		fatal("unknown agent %q", name)
	}
	fmt.Printf("Name: %s\n", cfg.Name)
	fmt.Printf("DisplayName: %s\n", cfg.DisplayName)
	fmt.Printf("Title: %s\n", cfg.Title)
	fmt.Printf("DefaultModel: %s\n", cfg.DefaultModel)
	fmt.Printf("ToolNames: %s\n", strings.Join(cfg.ToolNames, ","))
	fmt.Printf("Prompt:\n%s\n", cfg.Prompt)
}

func cmdAgentAdd(args []string, workspaceRoot string) {
	fs := flag.NewFlagSet("coder agent add", flag.ExitOnError)
	name := fs.String("name", "", "agent name (required)")
	displayName := fs.String("display-name", "", "agent display name")
	title := fs.String("title", "", "agent title")
	defaultModel := fs.String("default-model", "", "agent default model")
	toolNames := fs.String("tools", "", "comma-separated tool names")
	promptText := fs.String("prompt", "", "agent prompt text")
	promptFile := fs.String("prompt-file", "", "path to file containing agent prompt")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	if *name == "" {
		fs.Usage()
		os.Exit(2)
	}
	if *promptText == "" && *promptFile == "" {
		fatal("either --prompt or --prompt-file is required")
	}
	prompt := *promptText
	if prompt == "" {
		data, err := os.ReadFile(*promptFile)
		if err != nil {
			fatal("read prompt file: %v", err)
		}
		prompt = string(data)
	}
	custom, err := agent.LoadWorkspaceAgents(workspaceRoot)
	if err != nil {
		fatal("load agents: %v", err)
	}
	if custom == nil {
		custom = []agent.Config{}
	}
	for i, cfg := range custom {
		if cfg.Name == *name {
			custom[i].DisplayName = coalesce(*displayName, cfg.DisplayName)
			custom[i].Title = coalesce(*title, cfg.Title)
			custom[i].DefaultModel = coalesce(*defaultModel, cfg.DefaultModel)
			custom[i].Prompt = prompt
			if *toolNames != "" {
				custom[i].ToolNames = splitTools(*toolNames)
			}
			if err := agent.SaveWorkspaceAgents(workspaceRoot, custom); err != nil {
				fatal("save agents: %v", err)
			}
			return
		}
	}
	cfg := agent.Config{
		Name:         *name,
		DisplayName:  coalesce(*displayName, *name),
		Title:        coalesce(*title, *name),
		DefaultModel: *defaultModel,
		ToolNames:    splitTools(*toolNames),
		Prompt:       prompt,
	}
	custom = append(custom, cfg)
	if err := agent.SaveWorkspaceAgents(workspaceRoot, custom); err != nil {
		fatal("save agents: %v", err)
	}
}

func cmdAgentRemove(args []string, workspaceRoot string) {
	fs := flag.NewFlagSet("coder agent remove", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: coder agent remove <name>\n")
	}
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	if fs.NArg() != 1 {
		fs.Usage()
		os.Exit(2)
	}
	name := fs.Arg(0)
	custom, err := agent.LoadWorkspaceAgents(workspaceRoot)
	if err != nil {
		fatal("load agents: %v", err)
	}
	if len(custom) == 0 {
		fatal("agent %q not found", name)
	}
	found := false
	out := make([]agent.Config, 0, len(custom))
	for _, cfg := range custom {
		if cfg.Name == name {
			found = true
			continue
		}
		out = append(out, cfg)
	}
	if !found {
		fatal("agent %q not found", name)
	}
	if err := agent.SaveWorkspaceAgents(workspaceRoot, out); err != nil {
		fatal("save agents: %v", err)
	}
}

func splitTools(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func coalesce(first, second string) string {
	if first != "" {
		return first
	}
	return second
}

// isStdinPiped reports whether stdin is connected to something other than a
// terminal (a pipe or a redirect). When true, the CLI will read the prompt
// from stdin if -p wasn't provided.
func isStdinPiped() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) == 0
}

// readStdin drains stdin until EOF and returns its contents. It returns an
// empty string (not an error) for an immediate EOF, so callers can treat
// "empty pipe" and "no pipe" the same.
func readStdin() (string, error) {
	if !isStdinPiped() {
		return "", nil
	}
	var b strings.Builder
	scanner := bufio.NewScanner(os.Stdin)
	// Allow long inputs (e.g. pasted file contents).
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		b.WriteString(scanner.Text())
		b.WriteByte('\n')
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return b.String(), nil
}
