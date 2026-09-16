package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/tjcoder-labs/cli/internal/browser"
	"github.com/tjcoder-labs/cli/internal/client"
)

// interactionLogTool exposes the social-interaction dedup ledger to agents.
type interactionLogTool struct{}

func (interactionLogTool) Definition() client.ToolDefinition {
	props := map[string]any{
		"action":   stringProp("Operation: check | record | list | count"),
		"platform": stringProp("reddit | linkedin | gemini | google | x | other"),
		"kind":     stringProp("upvote | like | comment | dm | save | other"),
		"target":   stringProp("Stable identifier: reddit fullname (t3_xxxxx), linkedin activity URN, gemini conversation id, or canonical URL slug"),
		"url":      stringProp("Canonical URL (optional, for record)"),
		"note":     stringProp("Human-readable snippet or subject (optional, for record)"),
		"limit":    numberProp("Max rows for list (default 20)"),
	}
	return client.ToolDefinition{
		Type: "function",
		Function: client.FunctionDefinition{
			Name: "interaction_log",
			Description: `Persistent dedup ledger for social actions (upvote, like, comment, DM) performed via browser_bridge. Stored in ~/.local/share/coder/interactions.json.

Actions:
- check(platform, kind, target): has this action already been done? Returns the prior record or "not recorded".
- record(platform, kind, target, url?, note?): mark an action as done. Returns whether it was new.
- list(platform?, kind?, limit?): recent interactions, newest first.
- count(platform?, kind?): totals grouped by platform/kind.

Rules of use:
- ALWAYS call "check" BEFORE performing any upvote, like, comment, or DM. If check reports the interaction exists, do NOT re-perform it.
- After successfully performing the action in the browser, call "record" so future sessions don't repeat it.`,
			Parameters: objectSchema([]string{"action"}, props),
		},
	}
}

func (interactionLogTool) Execute(ctx context.Context, raw json.RawMessage, env ExecEnv) (Result, error) {
	_ = ctx
	_ = env
	var a struct {
		Action   string `json:"action"`
		Platform string `json:"platform"`
		Kind     string `json:"kind"`
		Target   string `json:"target"`
		URL      string `json:"url"`
		Note     string `json:"note"`
		Limit    int    `json:"limit"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return Result{}, err
	}
	a.Action = strings.ToLower(strings.TrimSpace(a.Action))

	log, err := browser.LoadInteractionLog(browser.DefaultInteractionLogPath())
	if err != nil {
		return Result{}, fmt.Errorf("failed to load interaction log: %w", err)
	}
	kind := browser.InteractionKind(strings.ToLower(a.Kind))

	switch a.Action {
	case "check":
		if a.Platform == "" || kind == "" || a.Target == "" {
			return Result{}, fmt.Errorf("check requires platform, kind, target")
		}
		if prior, ok := log.Has(a.Platform, kind, a.Target); ok {
			out := fmt.Sprintf("ALREADY DONE — %s %s on %s at %s%s",
				prior.Platform, prior.Kind, prior.Target,
				prior.At.Format("2006-01-02 15:04Z"), noteSuffix(prior.Note))
			return Result{Content: out, Preview: "already done"}, nil
		}
		out := fmt.Sprintf("not recorded: %s %s %s — safe to perform", a.Platform, kind, a.Target)
		return Result{Content: out, Preview: "not recorded"}, nil

	case "record":
		if a.Platform == "" || kind == "" || a.Target == "" {
			return Result{}, fmt.Errorf("record requires platform, kind, target")
		}
		isNew, err := log.Record(browser.Interaction{
			Platform: a.Platform, Kind: kind, Target: a.Target,
			URL: a.URL, Note: a.Note,
		})
		if err != nil {
			return Result{}, fmt.Errorf("record failed: %w", err)
		}
		verb := "recorded"
		if !isNew {
			verb = "updated (duplicate replaced)"
		}
		out := fmt.Sprintf("%s %s %s on %s", verb, kind, a.Platform, a.Target)
		return Result{Content: out, Preview: out}, nil

	case "list":
		limit := a.Limit
		if limit <= 0 {
			limit = 20
		}
		recent := log.ListRecent(0)
		filtered := recent[:0]
		for _, i := range recent {
			if a.Platform != "" && !strings.EqualFold(i.Platform, a.Platform) {
				continue
			}
			if a.Kind != "" && !strings.EqualFold(string(i.Kind), a.Kind) {
				continue
			}
			filtered = append(filtered, i)
		}
		if len(filtered) > limit {
			filtered = filtered[:limit]
		}
		if len(filtered) == 0 {
			return Result{Content: "no interactions recorded", Preview: "none"}, nil
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%-9s %-7s %-28s %-16s %s\n", "PLATFORM", "KIND", "TARGET", "AT", "NOTE")
		for _, i := range filtered {
			fmt.Fprintf(&b, "%-9s %-7s %-28s %-16s %s\n",
				i.Platform, i.Kind, trunc(i.Target, 28),
				i.At.Format("01-02 15:04Z"), trunc(i.Note, 60))
		}
		out := b.String()
		return Result{Content: out, Preview: preview(out)}, nil

	case "count":
		counts := map[string]int{}
		for _, i := range log.RecentList() {
			if a.Platform != "" && !strings.EqualFold(i.Platform, a.Platform) {
				continue
			}
			if a.Kind != "" && !strings.EqualFold(string(i.Kind), a.Kind) {
				continue
			}
			key := i.Platform + "/" + string(i.Kind)
			counts[key]++
		}
		if len(counts) == 0 {
			return Result{Content: "no interactions recorded", Preview: "none"}, nil
		}
		keys := make([]string, 0, len(counts))
		for k := range counts {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var b strings.Builder
		b.WriteString("interaction counts:\n")
		for _, k := range keys {
			fmt.Fprintf(&b, "  %-24s %d\n", k, counts[k])
		}
		out := b.String()
		return Result{Content: out, Preview: preview(out)}, nil

	default:
		return Result{}, fmt.Errorf("unknown action %q (check|record|list|count)", a.Action)
	}
}

func noteSuffix(s string) string {
	if s == "" {
		return ""
	}
	return " — " + s
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "…"
}
