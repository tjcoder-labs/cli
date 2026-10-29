package tooling

import (
	"context"
	"strings"

	"github.com/tjcoder-labs/cli/internal/client"
)

// turnContextKey carries the ephemeral per-turn metadata block (see
// WithTurnContext) through Runner.Run without widening its signature.
type turnContextKey struct{}

// WithTurnContext returns a context that makes Runner attach block to the
// most recent user message of every request it builds during this Run.
// The block is applied to a request-time copy only: it is never written
// into the history slice Run returns, so it is never persisted and older
// user turns are always replayed clean. An empty block is a no-op.
func WithTurnContext(ctx context.Context, block string) context.Context {
	block = strings.TrimSpace(block)
	if block == "" {
		return ctx
	}
	return context.WithValue(ctx, turnContextKey{}, block)
}

// TurnContextFrom returns the block installed by WithTurnContext, or "".
func TurnContextFrom(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	s, _ := ctx.Value(turnContextKey{}).(string)
	return s
}

// ApplyTurnContext returns a copy of messages in which block is appended to
// the content of the LAST user-role message only. The input slice and its
// elements are not modified. With an empty block or no user message the
// copy is returned unchanged.
func ApplyTurnContext(messages []client.Message, block string) []client.Message {
	out := make([]client.Message, len(messages))
	copy(out, messages)
	block = strings.TrimSpace(block)
	if block == "" {
		return out
	}
	for i := len(out) - 1; i >= 0; i-- {
		if out[i].Role != "user" {
			continue
		}
		if strings.TrimSpace(out[i].Content) == "" {
			out[i].Content = block
		} else {
			out[i].Content = out[i].Content + "\n\n" + block
		}
		break
	}
	return out
}
