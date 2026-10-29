package telemetry

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

// RunnerObserver maps agent-loop callbacks to tool_completed and
// turn_completed events. It sends names, outcomes and buckets only.
type RunnerObserver struct{}

// ToolDone implements tooling.Observer.
func (RunnerObserver) ToolDone(name string, err error, d time.Duration) {
	Track("tool_completed", map[string]any{"tool": ToolClass(name), "ok": err == nil, "latency": LatencyBucket(d)})
}

// TurnDone implements tooling.Observer.
func (RunnerObserver) TurnDone(d time.Duration, promptTokens, outputTokens int, err error) {
	if errors.Is(err, context.Canceled) {
		return // user interrupt, not a failure
	}
	Track("turn_completed", map[string]any{
		"ok": err == nil, "latency": LatencyBucket(d),
		"prompt_tokens": TokenBucket(promptTokens), "output_tokens": TokenBucket(outputTokens),
	})
	if err != nil {
		Error(ErrorClass(err), err)
	}
}

// ToolClass collapses user-defined MCP tool names (which can reveal
// private servers) to "mcp".
func ToolClass(name string) string {
	if strings.HasPrefix(name, "mcp_") || strings.HasPrefix(name, "mcp__") {
		return "mcp"
	}
	return Bucketless(name)
}

// ErrorClass reduces an error to a coarse, content-free class.
func ErrorClass(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	var ne net.Error
	if errors.As(err, &ne) {
		if ne.Timeout() {
			return "timeout"
		}
		return "network"
	}
	// Innermost wrapped error's Go type, e.g. "client.ErgoAuthError".
	for {
		u := errors.Unwrap(err)
		if u == nil {
			break
		}
		err = u
	}
	t := strings.TrimLeft(fmt.Sprintf("%T", err), "*")
	switch t {
	case "errors.errorString", "fmt.wrapError", "fmt.wrapErrors":
		return "other"
	}
	return Bucketless(t)
}
