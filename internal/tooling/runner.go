package tooling

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tjcoder-labs/cli/internal/agent"
	"github.com/tjcoder-labs/cli/internal/client"
	"github.com/tjcoder-labs/cli/internal/memories"
	"github.com/tjcoder-labs/cli/internal/session"
	"github.com/tjcoder-labs/cli/internal/tools"
)

type EventType string

const (
	EventReasoning  EventType = "reasoning"
	EventCommentary EventType = "commentary"
	// EventCommentaryCorrection is emitted after the runner has
	// post-processed the assistant's final message and stripped any
	// fallback tool-call wrappers (e.g. ‹tool_call›...‹/tool_call› or
	// <tool_call>...</tool_call>) from the commentary text. The Text
	// field carries the *cleaned* commentary. UIs should replace the
	// last rendered commentary block with this content so the
	// transcript does not show the wrapper text the user could
	// otherwise mistake for a missed tool invocation.
	EventCommentaryCorrection EventType = "commentary_correction"
	EventToolStart            EventType = "tool_start"
	EventToolResult           EventType = "tool_result"
	EventContext              EventType = "context"
	EventError                EventType = "error"
)

type Event struct {
	Type     EventType
	ToolName string
	Text     string
}

type Runner struct {
	Provider      client.Provider
	Registry      *tools.Registry
	WorkspaceRoot string
	MaxSteps      int
	// Sinks for TUI integration. Set by the app before calling Run if
	// running in TUI mode. nil in headless mode.
	HighlightSink  tools.HighlightSink
	CLICommandSink tools.CLICommandSink
	SessionState   *session.State
	PersistSession func() error
}

const defaultMaxToolSteps = 8

type thoughtParser struct {
	buf        string
	inThink    bool
	inToolCall bool
}

// Streaming control-token variants. `<think>` reasoning is routed to the
// cognition pane; tool-call wrappers are suppressed entirely so leaked tool
// markup never reaches the live transcript. Models vary the delimiter shape
// (plain `<tool_call>`, pipe-delimited `<|tool_call|>` / `<tool_call|>`, and
// the single-angle-quote `‹tool_call›` form used by minimax-m3), so we treat
// every recognized delimiter as a toggle: the first one opens a suppressed
// span and the next closes it. This mirrors the post-hoc cleaning done by
// scrubMessage/ExtractFallbackToolCall for stored history.
var toolCallDelims = []string{
	"<|/tool_call|>", "<|tool_call|>",
	"<|/tool_call>", "<|tool_call>",
	"</tool_call|>", "<tool_call|>",
	"</tool_call>", "<tool_call>",
	"\u2039/tool_call\u203a", "\u2039tool_call\u203a",
}

// partialSuffixLen checks if the end of string 's' contains an incomplete
// prefix of the 'target' string (e.g., if s ends with "<thi" and target is "<think>")
func partialSuffixLen(s, target string) int {
	maxMatch := len(s)
	if len(target)-1 < maxMatch {
		maxMatch = len(target) - 1
	}
	for i := maxMatch; i > 0; i-- {
		if strings.HasSuffix(s, target[:i]) {
			return i
		}
	}
	return 0
}

// earliestToken returns the byte index and matched token for the earliest
// occurrence of any of tokens within s, or (-1, "") if none are present.
func earliestToken(s string, tokens []string) (int, string) {
	bestIdx, best := -1, ""
	for _, t := range tokens {
		if i := strings.Index(s, t); i != -1 && (bestIdx == -1 || i < bestIdx) {
			bestIdx, best = i, t
		}
	}
	return bestIdx, best
}

// maxPartialSuffix returns the largest partial-suffix length across tokens so
// a control token split across streaming chunks is held back until complete.
func maxPartialSuffix(s string, tokens []string) int {
	max := 0
	for _, t := range tokens {
		if n := partialSuffixLen(s, t); n > max {
			max = n
		}
	}
	return max
}

func (p *thoughtParser) Add(text string) (reasoning, commentary string) {
	p.buf += text
	for len(p.buf) > 0 {
		switch {
		case p.inThink:
			idx := strings.Index(p.buf, "</think>")
			if idx != -1 {
				reasoning += p.buf[:idx]
				p.buf = p.buf[idx+len("</think>"):]
				p.inThink = false
				continue
			}
			// If the buffer ends with a partial closing tag, hold it back.
			partial := partialSuffixLen(p.buf, "</think>")
			if partial > 0 {
				reasoning += p.buf[:len(p.buf)-partial]
				p.buf = p.buf[len(p.buf)-partial:]
				return reasoning, commentary
			}
			reasoning += p.buf
			p.buf = ""
			return reasoning, commentary

		case p.inToolCall:
			// Suppress everything until the next delimiter (which closes the
			// span); emit nothing.
			idx, tok := earliestToken(p.buf, toolCallDelims)
			if idx != -1 {
				p.buf = p.buf[idx+len(tok):]
				p.inToolCall = false
				continue
			}
			// Hold back a partial delimiter; discard the rest.
			partial := maxPartialSuffix(p.buf, toolCallDelims)
			p.buf = p.buf[len(p.buf)-partial:]
			return reasoning, commentary

		default:
			thinkIdx := strings.Index(p.buf, "<think>")
			toolIdx, toolTok := earliestToken(p.buf, toolCallDelims)

			// Pick whichever opener appears first in the buffer.
			nextIdx, kind := -1, ""
			if thinkIdx != -1 {
				nextIdx, kind = thinkIdx, "think"
			}
			if toolIdx != -1 && (nextIdx == -1 || toolIdx < nextIdx) {
				nextIdx, kind = toolIdx, "tool"
			}
			if nextIdx != -1 {
				commentary += p.buf[:nextIdx]
				if kind == "think" {
					p.buf = p.buf[nextIdx+len("<think>"):]
					p.inThink = true
				} else {
					p.buf = p.buf[nextIdx+len(toolTok):]
					p.inToolCall = true
				}
				continue
			}

			// No complete opener: hold back a partial opener of any kind.
			openTokens := append([]string{"<think>"}, toolCallDelims...)
			partial := maxPartialSuffix(p.buf, openTokens)
			if partial > 0 {
				commentary += p.buf[:len(p.buf)-partial]
				p.buf = p.buf[len(p.buf)-partial:]
				return reasoning, commentary
			}
			commentary += p.buf
			p.buf = ""
			return reasoning, commentary
		}
	}
	return reasoning, commentary
}

func (p *thoughtParser) Flush() (reasoning, commentary string) {
	switch {
	case p.inThink:
		reasoning = p.buf
	case p.inToolCall:
		// Discard incomplete/unclosed tool-call markup entirely.
	default:
		commentary = p.buf
	}
	p.buf = ""
	return reasoning, commentary
}

func (r *Runner) Run(ctx context.Context, history []client.Message, prompt string, agentCfg agent.Config, model string, enabledTools []string, onEvent func(Event)) ([]client.Message, error) {
	if r.MaxSteps <= 0 {
		r.MaxSteps = defaultMaxToolSteps
	}
	history = append(history, client.Message{Role: "user", Content: prompt})
	defs := r.Registry.Definitions(enabledTools)
	systemPrompt := agentCfg.Prompt
	if injected := loadAgentsPrompt(r.WorkspaceRoot); injected != "" {
		systemPrompt += "\n\nRepository instructions from AGENTS.md (auto-injected):\n" + injected
	}
	contextWindow, _ := r.Provider.ContextWindow(ctx, model)

	if r.SessionState != nil {
		memBlock := memories.FormatPromptBlock(memories.Load(*r.SessionState), 10)
		if strings.TrimSpace(memBlock) != "" {
			systemPrompt = memBlock + "\n\n" + systemPrompt
		}
	}

	coderPrompt := loadCoderPrompt(r.WorkspaceRoot)
	if coderPrompt != "" {
		systemPrompt = "Repository instructions from CODER.md (auto-injected):\n" + coderPrompt + "\n\n" + systemPrompt
	}

	for step := 0; step < r.MaxSteps; step++ {

		// 1. The model generates its response for the current turn
		msg, err := r.runChatTurn(ctx, model, systemPrompt, history, defs, onEvent)
		if err != nil {
			return history, err
		}

		// --- HALLUCINATION INTERCEPTOR ---
		// Apply the same scrub to every assistant turn (including the
		// budget-reached checkpoint below) so models that emit
		// `‹tool_call›…` or `<tool_call>…</tool_call>` wrappers in their
		// prose — even when no native tool defs were sent — never leak
		// the markup into history or the user's transcript.
		msg, hadFallbackCalls := r.scrubMessage(msg)
		// ---------------------------------

		// 2. The sanitized message is appended to the conversation history
		history = append(history, msg)
		if onEvent != nil {
			onEvent(Event{
				Type: EventContext,
				Text: formatContextUsage(msg, contextWindow),
			})
		}

		// 3. If no tools were called (natively or via fallback), the turn ends
		if len(msg.ToolCalls) == 0 {
			return history, nil
		}

		// 3a. If fallback calls were extracted, inject a synthetic tool-acknowledged
		// message to help lower-end models understand their fallback format was parsed.
		// This ensures continuity in the SSE token stream and prevents models from
		// losing context after embedded tool calls.
		if hadFallbackCalls {
			numTools := len(msg.ToolCalls)
			toolNames := make([]string, numTools)
			for i, call := range msg.ToolCalls {
				toolNames[i] = call.Function.Name
			}
			ackMsg := fmt.Sprintf("[Intercepted fallback tool call(s): %v. Processing %d tool(s)...]", toolNames, numTools)
			history = append(history, client.Message{
				Role:    "system",
				Content: ackMsg,
			})
		}

		for _, call := range msg.ToolCalls {
			if onEvent != nil {
				onEvent(Event{
					Type:     EventToolStart,
					ToolName: call.Function.Name,
					Text:     strings.TrimSpace(string(call.Function.Arguments)),
				})
			}
			result, err := r.Registry.Execute(ctx, call.Function.Name, call.Function.Arguments, tools.ExecEnv{
				WorkspaceRoot:  r.WorkspaceRoot,
				Provider:       r.Provider,
				Sink:           r.HighlightSink,
				CommandSink:    r.CLICommandSink,
				SessionState:   r.SessionState,
				PersistSession: r.PersistSession,
			})
			if err != nil {
				errText := fmt.Sprintf("tool %s failed: %v", call.Function.Name, err)
				if onEvent != nil {
					onEvent(Event{Type: EventError, ToolName: call.Function.Name, Text: errText})
				}
				history = append(history, client.Message{
					Role:     "tool",
					ToolName: call.Function.Name,
					Content:  errText,
				})
				continue
			}
			history = append(history, client.Message{
				Role:     "tool",
				ToolName: call.Function.Name,
				Content:  result.Content,
			})
			if onEvent != nil {
				onEvent(Event{
					Type:     EventToolResult,
					ToolName: call.Function.Name,
					Text:     result.Preview,
				})
			}
		}
	}

	if onEvent != nil {
		onEvent(Event{
			Type:     EventError,
			ToolName: "runner",
			Text:     fmt.Sprintf("reached max tool steps (%d); asking model for a checkpoint response", r.MaxSteps),
		})
		onEvent(Event{
			Type: EventCommentary,
			Text: "\n[#C73CDC::b]Tool-call budget reached for this turn. Generating a checkpoint response without additional tools.[-:-:-]\n",
		})
	}

	checkpointPrompt := systemPrompt + "\n\nRuntime instruction: The tool-call budget for this assistant turn has been reached. Do not call tools in your next response. Summarize completed work, list remaining uncertainty, and explicitly ask the user whether to continue with another turn if additional tool calls are needed."
	msg, err := r.runChatTurn(ctx, model, checkpointPrompt, history, nil, onEvent)
	if err != nil {
		return history, fmt.Errorf("reached max tool steps and failed to generate checkpoint response: %w", err)
	}
	// T4: the model may still emit `‹tool_call›…` / `<tool_call>…</tool_call>`
	// wrappers in its prose even though no native tool defs were sent.
	// Run the same scrub as the main loop so the user does not see the
	// leaked markup in the transcript. We deliberately do NOT promote
	// any extracted fallback calls here: the budget guard's contract is
	// "no more tool calls this turn", so we discard them.
	msg, _ = r.scrubMessage(msg)
	if len(msg.ToolCalls) > 0 {
		msg.ToolCalls = nil
		if onEvent != nil {
			onEvent(Event{
				Type:     EventError,
				ToolName: "runner",
				Text:     "model attempted additional tool calls after tool budget guard; ignored tool calls in checkpoint response",
			})
		}
	}
	history = append(history, msg)
	if onEvent != nil {
		onEvent(Event{
			Type: EventContext,
			Text: formatContextUsage(msg, contextWindow),
		})
	}
	return history, nil
}

func (r *Runner) runChatTurn(ctx context.Context, model, systemPrompt string, history []client.Message, defs []client.ToolDefinition, onEvent func(Event)) (client.Message, error) {
	parser := &thoughtParser{}
	// Strip prior-turn chain-of-thought before replaying history to the
	// model. Thinking is retained in stored history for display, but
	// re-sending it bloats the context window and can destabilize
	// smaller models by feeding their own reasoning back as input.
	sanitized := make([]client.Message, len(history))
	for i, m := range history {
		m.Thinking = ""
		sanitized[i] = m
	}
	req := client.ChatRequest{
		Model: model,
		Messages: append([]client.Message{
			{Role: "system", Content: systemPrompt},
		}, sanitized...),
		Tools: defs,
		Think: true,
	}
	msg, err := r.Provider.Chat(ctx, req, func(stream client.StreamEvent) error {
		if stream.Reasoning != "" && onEvent != nil {
			onEvent(Event{Type: EventReasoning, Text: stream.Reasoning})
		}
		if stream.Commentary != "" && onEvent != nil {
			reasoning, commentary := parser.Add(stream.Commentary)
			if reasoning != "" {
				onEvent(Event{Type: EventReasoning, Text: reasoning})
			}
			if commentary != "" {
				onEvent(Event{Type: EventCommentary, Text: commentary})
			}
		}
		return nil
	})
	if err != nil {
		return client.Message{}, err
	}
	if onEvent != nil {
		reasoning, commentary := parser.Flush()
		if reasoning != "" {
			onEvent(Event{Type: EventReasoning, Text: reasoning})
		}
		if commentary != "" {
			onEvent(Event{Type: EventCommentary, Text: commentary})
		}
	}
	return msg, nil
}

// scrubMessage runs the hallucination interceptor on a finished
// assistant message. It strips `‹tool_call›…` / `<tool_call>…</tool_call>`
// wrappers and embedded ```json / ``` fenced tool definitions from
// the message content, and (when the wrapper contained a valid tool
// name) promotes them into msg.ToolCalls. The second return value is
// true if at least one fallback call was promoted, which the runner
// uses to decide whether to inject the synthetic "Intercepted fallback
// tool call(s)" acknowledgement into history. This is the single
// shared code path used by both the main turn loop and the
// budget-reached checkpoint branch so a model that emits embedded
// tool markup in its prose can never leak that markup into the
// transcript.
func (r *Runner) scrubMessage(msg client.Message) (client.Message, bool) {
	fallbackCalls, cleanedContent := ExtractFallbackToolCall(msg.Content, r.Registry)
	// Always adopt the cleaned content: even when no fallback tool call
	// was recovered, ExtractFallbackToolCall strips <think>...</think>
	// blocks and HTML comments that must never persist into the stored
	// transcript/history (reasoning is routed to Cognition separately).
	msg.Content = cleanedContent
	if len(fallbackCalls) > 0 {
		msg.ToolCalls = append(msg.ToolCalls, fallbackCalls...)
	}
	// The bool strictly means "fallback tool calls were promoted" — the
	// caller uses it to inject a synthetic acknowledgement message, so it
	// must NOT fire when only think-blocks/comments were stripped.
	return msg, len(fallbackCalls) > 0
}

func EncodeArgs(v any) string {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return ""
	}
	return string(data)
}

func loadAgentsPrompt(workspaceRoot string) string {
	path := filepath.Join(workspaceRoot, "AGENTS.md")
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	text := strings.TrimSpace(string(data))
	if len(text) > 12000 {
		text = text[:12000] + "\n...[truncated]"
	}
	return text
}

func loadCoderPrompt(workspaceRoot string) string {
	path := filepath.Join(workspaceRoot, "CODER.md")
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	text := strings.TrimSpace(string(data))
	if len(text) > 12000 {
		text = text[:12000] + "\n...[truncated]"
	}
	return text
}

func formatContextUsage(msg client.Message, contextWindow int) string {
	if msg.PromptEvalCount <= 0 && msg.EvalCount <= 0 {
		if contextWindow > 0 {
			return fmt.Sprintf("ctx: ? / %d (usage unavailable)", contextWindow)
		}
		return "ctx: unavailable"
	}
	if contextWindow <= 0 {
		return fmt.Sprintf("ctx: used≈%d tok, output=%d tok (window unknown)", msg.PromptEvalCount, msg.EvalCount)
	}
	used := msg.PromptEvalCount
	remaining := contextWindow - used
	if remaining < 0 {
		remaining = 0
	}
	pctUsed := float64(used) / float64(contextWindow) * 100.0
	// Provide a compact human-readable context string. The UI expects the
	// leading numeric "used / total" pair so it can render a progress bar.
	return fmt.Sprintf("ctx: %d / %d used (%.1f%%), remaining=%d, output=%d", used, contextWindow, pctUsed, remaining, msg.EvalCount)
}
