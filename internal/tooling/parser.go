package tooling

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/tjcoder-labs/cli/internal/client"
	"github.com/tjcoder-labs/cli/internal/tools"
)

// ExtractFallbackToolCall intercepts and parses raw JSON objects representing tool calls
// when the model fails to utilize the native ToolCalls API.
//
// This function handles several formatting scenarios, including:
//   - Naked JSON objects appearing on a line by themselves (e.g., {"name": "..."}).
//   - JSON objects wrapped in standard markdown code blocks (e.g., ```json { ... } ```).
//   - Function signatures in markdown (e.g., ```json {"name":"...", "arguments":{...}}```).
//   - XML-style tool definitions (e.g., <function=name>...).
//   - Sequences of multiple tool calls emitted in a single message.
//   - Variations in indentation or leading/trailing whitespace around the JSON objects.
//
// The function validates the "name" field against the registered tools to prevent
// incorrect parsing of non-tool JSON data. It returns the successfully parsed
// ToolCalls and the original content string with the consumed JSON blocks removed.
func ExtractFallbackToolCall(content string, registry *tools.Registry) ([]client.ToolCall, string) {
	var toolCalls []client.ToolCall
	cleanedContent := content

	// Pre-clean: remove reasoning blocks and HTML comments often emitted by
	// models (e.g., <think>...</think> or <!-- ... -->). This prevents
	// interference with JSON/fenced-block parsing and improves robustness.
	thinkRe := regexp.MustCompile(`(?is)<think>.*?</think>`)
	cleanedContent = thinkRe.ReplaceAllString(cleanedContent, "")
	commentRe := regexp.MustCompile(`(?is)<!--.*?-->`)
	cleanedContent = commentRe.ReplaceAllString(cleanedContent, "")

	// 0. Regex for escaped/angle-bracket tool_call wrappers emitted inside
	// commentary by some models (e.g. minimax-m3). These appear as
	// \u2039tool_call\u2039...\u2039/tool_call\u2039 (single angle quotes),
	// <tool_call>...</tool_call> (literal angle brackets), or pipe-delimited
	// forms such as <|tool_call|>...<|/tool_call|> / <tool_call|>...<tool_call|>,
	// sometimes with stray backslashes from over-eager escaping. We strip the
	// wrapper, recover the inner JSON, and dispatch through the same validation
	// path as the markdown-fenced extractor.
	escapedToolCallRe := regexp.MustCompile(`(?is)[‹<]\|?/?\s*tool_call\|?[›>]([\s\S]*?)[‹<]\|?/?\s*tool_call\|?[›>]`)
	escapedMatches := escapedToolCallRe.FindAllStringSubmatch(cleanedContent, -1)
	for _, match := range escapedMatches {
		inner := match[1]
		// Unwrap any inner markdown fence to get to the raw JSON object.
		innerJSONRe := regexp.MustCompile(`(?s)\x60\x60\x60(?:json)?\s*(\{.*?\})\s*\x60\x60\x60`)
		innerStrippedRe := regexp.MustCompile(`(?s)\x60\x60\x60(?:json)?`)
		if m := innerJSONRe.FindStringSubmatch(inner); m != nil {
			inner = m[1]
		} else {
			inner = innerStrippedRe.ReplaceAllString(inner, "")
		}
		inner = strings.TrimSpace(inner)
		var tc struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal([]byte(inner), &tc); err == nil && tc.Name != "" {
			if isValidToolName(tc.Name, registry) {
				toolCalls = append(toolCalls, client.ToolCall{
					Type:     "function",
					Function: client.ToolFunctionCall{Name: tc.Name, Arguments: tc.Arguments},
				})
				cleanedContent = strings.Replace(cleanedContent, match[0], "", 1)
			}
		}
	}

	// 1. Regex for markdown JSON code blocks (matches ```json {...}``` patterns)
	// This handles both formatted and condensed JSON with or without type hint
	markdownJsonRe := regexp.MustCompile(`(?s)\x60\x60\x60(?:json)?\s*(\{[^\x60]*?"name"\s*:\s*"[^"]*?"[^\x60]*?"arguments"[^\x60]*?\})\s*\x60\x60\x60`)
	markdownMatches := markdownJsonRe.FindAllStringSubmatch(cleanedContent, -1)

	for _, match := range markdownMatches {
		var tc struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal([]byte(match[1]), &tc); err == nil && tc.Name != "" {
			if isValidToolName(tc.Name, registry) {
				toolCalls = append(toolCalls, client.ToolCall{
					Type:     "function",
					Function: client.ToolFunctionCall{Name: tc.Name, Arguments: tc.Arguments},
				})
				cleanedContent = strings.Replace(cleanedContent, match[0], "", 1)
			}
		}
	}

	// 2. Regex for XML-style output
	xmlRe := regexp.MustCompile(`(?s)<function=(.*?)>(.*?)</function>`)
	xmlMatches := xmlRe.FindAllStringSubmatch(cleanedContent, -1)

	for _, match := range xmlMatches {
		funcName := strings.TrimSpace(match[1])
		if !isValidToolName(funcName, registry) {
			continue
		}
		paramsContent := match[2]
		paramRe := regexp.MustCompile(`(?s)<parameter=(.*?)>(.*?)</parameter>`)
		paramMatches := paramRe.FindAllStringSubmatch(paramsContent, -1)

		args := make(map[string]interface{})
		for _, pm := range paramMatches {
			args[strings.TrimSpace(pm[1])] = strings.TrimSpace(pm[2])
		}
		argsJSON, _ := json.Marshal(args)

		toolCalls = append(toolCalls, client.ToolCall{
			Type:     "function",
			Function: client.ToolFunctionCall{Name: funcName, Arguments: json.RawMessage(argsJSON)},
		})
		cleanedContent = strings.Replace(cleanedContent, match[0], "", 1)
	}

	// 3. Extract any JSON object tool calls that may appear anywhere in the
	// message, including indented or embedded comment lines.
	for {
		removed := false
		for _, obj := range findBalancedJSONObjects(cleanedContent) {
			candidate := strings.TrimSpace(cleanedContent[obj.start:obj.end])
			var tc struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			}
			if err := json.Unmarshal([]byte(candidate), &tc); err == nil && tc.Name != "" {
				if isValidToolName(tc.Name, registry) {
					toolCalls = append(toolCalls, client.ToolCall{
						Type:     "function",
						Function: client.ToolFunctionCall{Name: tc.Name, Arguments: tc.Arguments},
					})
					cleanedContent = strings.TrimSpace(cleanedContent[:obj.start] + cleanedContent[obj.end:])
					removed = true
					break
				}
			}
		}
		if !removed {
			break
		}
	}

	// As a last-resort: try to find a final top-level JSON object and, if it
	// looks like a single tool call, extract it. This helps when models emit
	// a final JSON payload instead of fenced blocks or native tool call objects.
	if len(toolCalls) == 0 {
		if j, start, end, ok := findLastJSONObject(cleanedContent); ok {
			var tc struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			}
			if err := json.Unmarshal([]byte(j), &tc); err == nil && tc.Name != "" {
				if isValidToolName(tc.Name, registry) {
					toolCalls = append(toolCalls, client.ToolCall{
						Type:     "function",
						Function: client.ToolFunctionCall{Name: tc.Name, Arguments: tc.Arguments},
					})
					// remove the consumed JSON fragment from cleanedContent
					cleanedContent = strings.TrimSpace(cleanedContent[:start] + cleanedContent[end:])
				}
			}
		}
	}

	return toolCalls, strings.TrimSpace(cleanedContent)
}

// findLastJSONObject attempts to locate the last balanced JSON object within
// the input string. Returns the JSON substring and its start/end indices
// (end is exclusive) when found.
func findLastJSONObject(s string) (string, int, int, bool) {
	lastClose := strings.LastIndex(s, "}")
	if lastClose == -1 {
		return "", 0, 0, false
	}
	bestStart, bestEnd := -1, -1
	for start := strings.LastIndex(s[:lastClose+1], "{"); start >= 0; start = strings.LastIndex(s[:start], "{") {
		depth := 0
		end := -1
		for i := start; i < len(s); i++ {
			ch := s[i]
			if ch == '{' {
				depth++
			} else if ch == '}' {
				depth--
				if depth == 0 {
					end = i
					break
				}
			}
		}
		if end != -1 {
			candidate := strings.TrimSpace(s[start : end+1])
			var tmp any
			if json.Unmarshal([]byte(candidate), &tmp) == nil {
				// prefer the candidate with the largest end (closest to tail)
				if end > bestEnd {
					bestStart = start
					bestEnd = end + 1
				}
			}
		}
	}
	if bestStart >= 0 {
		return s[bestStart:bestEnd], bestStart, bestEnd, true
	}
	return "", 0, 0, false
}

func findBalancedJSONObjects(s string) []struct{ start, end int } {
	var matches []struct{ start, end int }
	for start := 0; start < len(s); start++ {
		if s[start] != '{' {
			continue
		}
		depth := 0
		for i := start; i < len(s); i++ {
			ch := s[i]
			if ch == '{' {
				depth++
			} else if ch == '}' {
				depth--
				if depth == 0 {
					candidate := strings.TrimSpace(s[start : i+1])
					var tmp any
					if json.Unmarshal([]byte(candidate), &tmp) == nil {
						matches = append(matches, struct{ start, end int }{start, i + 1})
					}
					break
				}
			}
		}
	}
	return matches
}

// isValidToolName checks if a tool name is registered in the provided registry.
// If registry is nil, it returns true to allow parsing without validation.
func isValidToolName(name string, registry *tools.Registry) bool {
	if registry == nil {
		return name != ""
	}
	return registry.Has(name)
}
