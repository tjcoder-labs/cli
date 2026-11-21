// Package convsync mirrors CLI session transcripts into the signed-in user's
// ergo-ai-server conversations (POST /cli/conversations/:sessionId/messages)
// whatever provider produced the turns. It is best-effort: syncing never
// blocks or fails a turn, unsynced turns stay in session.json and are sent
// on the next successful attempt, and secrets are masked before upload.
//
// Dedupe: each transcript item carries a per-session monotonic seq; the
// server keys messages by (conversation, seq) with insert-only upserts, so
// a retried batch never duplicates. A local cursor (conversation-sync.json)
// remembers how much of History has been sent; when History is rewritten
// (compaction) the new messages continue the same seq sequence.
package convsync

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"github.com/tjcoder-labs/cli/internal/client"
)

const (
	maxContentChars    = 100_000
	maxToolArgsChars   = 8_000
	maxToolResultChars = 4_000
	maxReasoningChars  = 8_000
)

// ToolCall is one tool invocation with its (redacted, truncated) result.
type ToolCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments,omitempty"`
	Result    string `json:"result,omitempty"`
	Status    string `json:"status,omitempty"`
}

// Usage is the provider-reported token usage of an assistant message.
type Usage struct {
	InputTokens  int `json:"input_tokens,omitempty"`
	OutputTokens int `json:"output_tokens,omitempty"`
}

// Item is one transcript entry on the wire.
type Item struct {
	Seq         int        `json:"seq"`
	Role        string     `json:"role"` // user | assistant | tool
	Content     string     `json:"content"`
	Fingerprint string     `json:"fingerprint"`
	CreatedAt   string     `json:"created_at,omitempty"`
	Provider    string     `json:"provider,omitempty"`
	Model       string     `json:"model,omitempty"`
	ToolName    string     `json:"tool_name,omitempty"`
	ToolCalls   []ToolCall `json:"tool_calls,omitempty"`
	Reasoning   string     `json:"reasoning,omitempty"`
	Usage       *Usage     `json:"usage,omitempty"`
}

// Cursor is the persisted sync position for one session.
type Cursor struct {
	SessionID       string `json:"session_id"`
	ConversationID  string `json:"conversation_id,omitempty"`
	SyncedLen       int    `json:"synced_len"`
	LastFingerprint string `json:"last_fingerprint,omitempty"`
	NextSeq         int    `json:"next_seq"`
	// Recovered is set when the position came from the server (local cursor
	// lost); the fingerprint then names the last synced assistant message.
	Recovered bool   `json:"-"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

// Fingerprint is a short content hash of a history message (computed on the
// redacted form, so it never encodes a raw secret).
func Fingerprint(m client.Message) string {
	h := sha256.New()
	h.Write([]byte(strings.ToLower(m.Role)))
	h.Write([]byte{0})
	h.Write([]byte(m.ToolName))
	h.Write([]byte{0})
	h.Write([]byte(Redact(m.Content)))
	for _, tc := range m.ToolCalls {
		h.Write([]byte{0})
		h.Write([]byte(tc.Function.Name))
		h.Write([]byte{1})
		h.Write([]byte(Redact(string(tc.Function.Arguments))))
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// resolveStart finds the first History index not yet synced.
func resolveStart(h []client.Message, cur Cursor) int {
	if cur.SyncedLen > 0 && cur.SyncedLen <= len(h) && Fingerprint(h[cur.SyncedLen-1]) == cur.LastFingerprint {
		return cur.SyncedLen
	}
	if cur.LastFingerprint != "" {
		for i := len(h) - 1; i >= 0; i-- {
			if Fingerprint(h[i]) == cur.LastFingerprint {
				start := i + 1
				if cur.Recovered {
					// The server's last item is the assistant call; its tool
					// results were folded into it, so skip them.
					for start < len(h) && strings.EqualFold(h[start].Role, "tool") {
						start++
					}
				}
				return start
			}
		}
	}
	// Nothing synced yet, or History was rewritten (compaction / edit):
	// send what is there now as new items continuing NextSeq.
	return 0
}

// Meta describes the session for the conversation record.
type Meta struct {
	Provider   string `json:"provider,omitempty"`
	Model      string `json:"model,omitempty"`
	Agent      string `json:"agent,omitempty"`
	Cwd        string `json:"cwd,omitempty"`
	CLIVersion string `json:"cli_version,omitempty"`
	Platform   string `json:"platform,omitempty"`
	Title      string `json:"title,omitempty"`
}

// Plan returns the items to send for History given the cursor, plus the
// cursor to persist once they are acknowledged. An assistant message whose
// tool calls have not all returned yet (end of History) is held back so the
// call and its results always travel together.
func Plan(h []client.Message, cur Cursor, meta Meta, now time.Time) ([]Item, Cursor) {
	start := resolveStart(h, cur)
	seq := cur.NextSeq
	var items []Item
	i := start
	stamp := func() string {
		// Monotonic per item so createdAt ordering matches seq ordering.
		return now.Add(time.Duration(len(items)) * time.Millisecond).UTC().Format("2006-01-02T15:04:05.000Z07:00")
	}
loop:
	for i < len(h) {
		m := h[i]
		switch strings.ToLower(m.Role) {
		case "user":
			items = append(items, Item{Seq: seq, Role: "user", Content: clip(Redact(m.Content), maxContentChars), Fingerprint: Fingerprint(m), CreatedAt: stamp()})
			seq++
			i++
		case "assistant":
			it := Item{
				Seq: seq, Role: "assistant", Content: clip(Redact(m.Content), maxContentChars), Fingerprint: Fingerprint(m),
				CreatedAt: stamp(), Provider: meta.Provider, Model: meta.Model,
			}
			if strings.TrimSpace(m.Thinking) != "" {
				it.Reasoning = clip(Redact(m.Thinking), maxReasoningChars)
			}
			if m.PromptEvalCount > 0 || m.EvalCount > 0 {
				it.Usage = &Usage{InputTokens: m.PromptEvalCount, OutputTokens: m.EvalCount}
			}
			next := i + 1
			if len(m.ToolCalls) > 0 {
				j := i + 1
				for j < len(h) && strings.EqualFold(h[j].Role, "tool") {
					j++
				}
				if j == len(h) && j-(i+1) < len(m.ToolCalls) {
					break loop // tool calls still running: wait for the results
				}
				it.ToolCalls = foldToolResults(m.ToolCalls, h[i+1:j])
				next = j
			}
			items = append(items, it)
			seq++
			i = next
		case "tool":
			items = append(items, Item{
				Seq: seq, Role: "tool", ToolName: m.ToolName, Content: clip(Redact(m.Content), maxToolResultChars),
				Fingerprint: Fingerprint(m), CreatedAt: stamp(), Provider: meta.Provider, Model: meta.Model,
			})
			seq++
			i++
		default: // system / internal notices are not part of the conversation
			i++
		}
	}
	next := cur
	next.NextSeq = seq
	next.Recovered = false
	if i > 0 && i <= len(h) {
		next.SyncedLen = i
		next.LastFingerprint = Fingerprint(h[i-1])
	} else {
		next.SyncedLen = 0
	}
	return items, next
}

func foldToolResults(calls []client.ToolCall, results []client.Message) []ToolCall {
	out := make([]ToolCall, len(calls))
	for k, c := range calls {
		out[k] = ToolCall{Name: c.Function.Name, Arguments: clip(Redact(rawArgs(c.Function.Arguments)), maxToolArgsChars), Status: "success"}
	}
	used := make([]bool, len(calls))
	for _, r := range results {
		idx := -1
		for k := range calls {
			if !used[k] && calls[k].Function.Name == r.ToolName {
				idx = k
				break
			}
		}
		if idx < 0 {
			for k := range calls {
				if !used[k] {
					idx = k
					break
				}
			}
		}
		if idx < 0 {
			break
		}
		used[idx] = true
		out[idx].Result = clip(Redact(r.Content), maxToolResultChars)
		low := strings.ToLower(strings.TrimSpace(r.Content))
		if strings.HasPrefix(low, "error") || strings.HasPrefix(low, "tool error") {
			out[idx].Status = "error"
		}
	}
	return out
}

func rawArgs(a json.RawMessage) string {
	s := strings.TrimSpace(string(a))
	if s == "" || s == "null" {
		return ""
	}
	// Arguments sometimes arrive as a JSON string holding JSON.
	var inner string
	if strings.HasPrefix(s, `"`) && json.Unmarshal(a, &inner) == nil {
		return inner
	}
	return s
}

func clip(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8Start(s[cut]) {
		cut--
	}
	return s[:cut] + "\n…[truncated]"
}

func utf8Start(b byte) bool { return b&0xC0 != 0x80 }

// TitleFrom returns a short title from the first user message.
func TitleFrom(h []client.Message) string {
	for _, m := range h {
		if strings.EqualFold(m.Role, "user") && strings.TrimSpace(m.Content) != "" {
			t := strings.Join(strings.Fields(Redact(m.Content)), " ")
			if len(t) > 80 {
				t = clip(t, 80)
				t = strings.TrimSuffix(t, "\n…[truncated]") + "…"
			}
			return t
		}
	}
	return ""
}
