package telemetry

import (
	"regexp"
	"strings"
	"time"

	"github.com/getsentry/sentry-go"
)

// allowedProps is the complete set of properties each event may carry.
var allowedProps = map[string][]string{
	"app_installed":   {"method"},
	"session_started": {"provider_type", "mode"},
	"model_selected":  {"model", "provider_type"},
	"command_used":    {"command"},
	"tool_completed":  {"tool", "ok", "latency"},
	"turn_completed":  {"ok", "latency", "prompt_tokens", "output_tokens"},
	"login_result":    {"ok", "method"},
	"update_check":    {"ok", "available"},
	"error":           {"class"},
}

// Allowed filters props to the event's allow-list, dropping any value
// that is not a bool, small int, or a short identifier-like string.
func Allowed(event string, props map[string]any) map[string]any {
	out := map[string]any{}
	for _, k := range allowedProps[event] {
		v, ok := props[k]
		if !ok {
			continue
		}
		switch x := v.(type) {
		case bool, int:
			out[k] = x
		case string:
			if safeIdent(x) {
				out[k] = x
			}
		}
	}
	return out
}

var identRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/+-]{0,63}$`)
var secretRe = regexp.MustCompile(`(?i)^(sk|xai|pk|ghp|gho|github_pat|ergo|phc|key)[-_][A-Za-z0-9_-]{8,}|[A-Za-z0-9_-]{32,}`)

// safeIdent accepts short ids like "ergo/pro", "run_command", "compact"
// and rejects paths, emails, whitespace-containing text, and key-like
// strings.
func safeIdent(s string) bool {
	if !identRe.MatchString(s) || strings.Contains(s, "@") || strings.Contains(s, "..") || strings.Count(s, "/") > 1 {
		return false
	}
	return !secretRe.MatchString(s)
}

// Bucketless sanitises a free-form class name to a safe identifier.
func Bucketless(s string) string {
	if safeIdent(s) {
		return s
	}
	return "other"
}

// LatencyBucket maps a duration to a coarse bucket label.
func LatencyBucket(d time.Duration) string {
	switch {
	case d < 250*time.Millisecond:
		return "<250ms"
	case d < time.Second:
		return "<1s"
	case d < 5*time.Second:
		return "<5s"
	case d < 30*time.Second:
		return "<30s"
	case d < 2*time.Minute:
		return "<2m"
	default:
		return ">=2m"
	}
}

// TokenBucket maps a token count to a coarse bucket label.
func TokenBucket(n int) string {
	switch {
	case n <= 0:
		return "0"
	case n < 1000:
		return "<1k"
	case n < 8000:
		return "<8k"
	case n < 32000:
		return "<32k"
	case n < 128000:
		return "<128k"
	default:
		return ">=128k"
	}
}

var (
	pathRe  = regexp.MustCompile(`(?:[A-Za-z]:\\|~[/\\]|/)(?:[^\s"'():,;]+[/\\])+[^\s"'():,;]*`)
	emailRe = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)
	urlRe   = regexp.MustCompile(`https?://[^\s"']+`)
	tokenRe = regexp.MustCompile(`(?i)\b(?:sk|xai|pk|ghp|gho|github_pat|ergo|phc|bearer)[-_ ][A-Za-z0-9_\-.]{8,}|\b[A-Za-z0-9_\-]{32,}\b`)
)

// ScrubText removes paths, URLs, emails, and key-like tokens from a
// message and truncates it.
func ScrubText(s string) string {
	s = urlRe.ReplaceAllString(s, "<url>")
	s = emailRe.ReplaceAllString(s, "<email>")
	s = tokenRe.ReplaceAllString(s, "<redacted>")
	s = pathRe.ReplaceAllString(s, "<path>")
	if r := []rune(s); len(r) > 200 {
		s = string(r[:200]) + "…"
	}
	return s
}

// scrubSentryEvent is the Sentry BeforeSend hook: it strips user/host
// data, request and env context, breadcrumbs, extra data, and absolute
// paths from stack frames, and scrubs messages.
func scrubSentryEvent(ev *sentry.Event, _ *sentry.EventHint) *sentry.Event {
	if ev == nil {
		return nil
	}
	id := ev.User.ID
	ev.User = sentry.User{ID: id}
	ev.ServerName = ""
	ev.Request = nil
	ev.Breadcrumbs = nil
	ev.Modules = nil
	for k := range ev.Contexts {
		if k != "os" && k != "runtime" {
			delete(ev.Contexts, k)
		}
	}
	if osc, ok := ev.Contexts["os"]; ok {
		ev.Contexts["os"] = sentry.Context{"name": osc["name"]}
	}
	for k := range ev.Tags {
		if k != "class" {
			delete(ev.Tags, k)
		}
	}
	ev.Message = ScrubText(ev.Message)
	scrubTrace := func(st *sentry.Stacktrace) {
		if st == nil {
			return
		}
		for i := range st.Frames {
			f := &st.Frames[i]
			f.AbsPath = ""
			f.Vars = nil
			f.PreContext, f.ContextLine, f.PostContext = nil, "", nil
			f.Filename = trimToModule(f.Filename)
		}
	}
	for i := range ev.Exception {
		ev.Exception[i].Value = ScrubText(ev.Exception[i].Value)
		scrubTrace(ev.Exception[i].Stacktrace)
	}
	for i := range ev.Threads {
		scrubTrace(ev.Threads[i].Stacktrace)
	}
	return ev
}

// trimToModule keeps only the module-relative part of a source path
// (e.g. "internal/tui/app.go"), never a home directory.
func trimToModule(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	for _, marker := range []string{"tjcoder-labs/cli/", "/pkg/mod/", "/src/"} {
		if i := strings.LastIndex(p, marker); i >= 0 {
			return p[i+len(marker):]
		}
	}
	if strings.HasPrefix(p, "/") || strings.Contains(p, ":/") {
		if i := strings.LastIndex(p, "/"); i >= 0 {
			return p[i+1:]
		}
	}
	return p
}
