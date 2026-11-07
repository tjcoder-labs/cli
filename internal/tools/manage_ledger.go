package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/tjcoder-labs/cli/internal/client"
	"github.com/tjcoder-labs/cli/internal/ledger"
)

// manageLedgerTool exposes the generalized, open-ended event ledger to
// agents. It supersedes the social-only interaction_log with a schema-free
// (namespace, key) identity, arbitrary JSON payloads, and optional TTL.
type manageLedgerTool struct{}

func (manageLedgerTool) Definition() client.ToolDefinition {
	props := map[string]any{
		"action":    stringProp("Operation: check | record | get | list | count | delete | clear"),
		"namespace": stringProp("Free-form scope, e.g. email, social, billing, outreach, invoices"),
		"key":       stringProp("Stable dedup identity within the namespace: message-id, recipient, URL slug, hash, etc."),
		"kind":      stringProp("Optional free-form sub-type: sent, received, replied, upvote, like, paid"),
		"data":      stringProp("Optional arbitrary JSON payload to store with the entry"),
		"note":      stringProp("Human-readable snippet or subject (optional)"),
		"ttl":       numberProp("Optional expiry in seconds after record time (0 = never)"),
		"limit":     numberProp("Max rows for list (default 20)"),
	}
	return client.ToolDefinition{
		Type: "function",
		Function: client.FunctionDefinition{
			Name: "manage_ledger",
			Description: `Generalized persistent event ledger for tracking arbitrary events and enforcing deduplication (messages sent, correspondences, social actions, or any other data requiring a record). Stored in ~/.local/share/coder/ledger.json.

Each entry is identified by a (namespace, key) pair — both free strings — and may carry an arbitrary JSON "data" payload, a "kind" tag, a "note", and an optional time-to-live.

Actions:
- check(namespace, key): has this event already been recorded? Returns "recorded" or "not recorded".
- record(namespace, key, kind?, data?, note?, ttl?): mark an event as done. Returns whether it was new.
- get(namespace, key): full entry (data included) or "not found".
- list(namespace?, kind?, limit?): recent entries, newest first.
- count(namespace?, kind?): totals grouped by namespace/kind.
- delete(namespace, key): remove a single entry.
- clear(namespace): remove every entry in a namespace.

Rules of use:
- ALWAYS call "check" BEFORE performing a repeatable action (sending a message/email, posting, recording a fact). If check reports "recorded", do NOT repeat it.
- After successfully performing the action, call "record" so future sessions don't duplicate it.
- Dedup by a STABLE key (message-id, recipient+subject, URL, content hash) — never by free-form prose alone.`,
			Parameters: objectSchema([]string{"action"}, props),
		},
	}
}

func (manageLedgerTool) Execute(ctx context.Context, raw json.RawMessage, env ExecEnv) (Result, error) {
	_ = ctx
	_ = env
	var a struct {
		Action    string          `json:"action"`
		Namespace string          `json:"namespace"`
		Key       string          `json:"key"`
		Kind      string          `json:"kind"`
		Data      json.RawMessage `json:"data"`
		Note      string          `json:"note"`
		TTL       float64         `json:"ttl"`
		Limit     int             `json:"limit"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return Result{}, err
	}
	a.Action = strings.ToLower(strings.TrimSpace(a.Action))
	ns := strings.TrimSpace(a.Namespace)
	key := strings.TrimSpace(a.Key)

	l, err := ledger.Load(ledger.DefaultPath())
	if err != nil {
		return Result{}, fmt.Errorf("failed to load ledger: %w", err)
	}

	switch a.Action {
	case "check":
		if ns == "" || key == "" {
			return Result{}, fmt.Errorf("check requires namespace, key")
		}
		if e, ok := l.Has(ns, key); ok {
			out := fmt.Sprintf("RECORDED — %s:%s (%s) at %s%s",
				e.Namespace, e.Key, e.Kind, e.At.Format("2006-01-02 15:04Z"), noteSuffix(e.Note))
			return Result{Content: out, Preview: "recorded"}, nil
		}
		return Result{Content: fmt.Sprintf("not recorded: %s:%s — safe to proceed", ns, key), Preview: "not recorded"}, nil

	case "record":
		if ns == "" || key == "" {
			return Result{}, fmt.Errorf("record requires namespace, key")
		}
		e := ledger.Entry{Namespace: ns, Key: key, Kind: strings.TrimSpace(a.Kind), Note: a.Note}
		if len(a.Data) > 0 {
			// Normalize: accept a raw JSON object or a JSON string.
			if a.Data[0] == '"' {
				var s string
				if err := json.Unmarshal(a.Data, &s); err == nil {
					e.Data = json.RawMessage(s)
				} else {
					e.Data = a.Data
				}
			} else {
				e.Data = a.Data
			}
		}
		if a.TTL > 0 {
			e.TTL = time.Duration(a.TTL * float64(time.Second))
		}
		isNew, err := l.Record(e)
		if err != nil {
			return Result{}, fmt.Errorf("record failed: %w", err)
		}
		verb := "recorded"
		if !isNew {
			verb = "updated (duplicate replaced)"
		}
		return Result{Content: fmt.Sprintf("%s %s:%s (%s)", verb, ns, key, e.Kind), Preview: fmt.Sprintf("%s %s:%s", verb, ns, key)}, nil

	case "get":
		if ns == "" || key == "" {
			return Result{}, fmt.Errorf("get requires namespace, key")
		}
		e := l.Get(ns, key)
		if e == nil {
			return Result{Content: fmt.Sprintf("not found: %s:%s", ns, key), Preview: "not found"}, nil
		}
		out := fmt.Sprintf("%s:%s  kind=%s  at=%s\nnote=%s\ndata=%s",
			e.Namespace, e.Key, e.Kind, e.At.Format("2006-01-02 15:04Z"),
			e.Note, dataOrNone(e.Data))
		return Result{Content: out, Preview: "found " + ns + ":" + key}, nil

	case "list":
		limit := a.Limit
		if limit <= 0 {
			limit = 20
		}
		entries := l.ListRecent(limit, ns, strings.TrimSpace(a.Kind))
		if len(entries) == 0 {
			return Result{Content: "no entries recorded", Preview: "none"}, nil
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%-12s %-24s %-10s %-16s %s\n", "NAMESPACE", "KEY", "KIND", "AT", "NOTE")
		for _, e := range entries {
			fmt.Fprintf(&b, "%-12s %-24s %-10s %-16s %s\n",
				trunc(e.Namespace, 12), trunc(e.Key, 24), trunc(e.Kind, 10),
				e.At.Format("01-02 15:04Z"), trunc(e.Note, 60))
		}
		out := b.String()
		return Result{Content: out, Preview: preview(out)}, nil

	case "count":
		type kc struct{ ns, kind string }
		counts := map[kc]int{}
		for _, e := range l.Recent {
			if e.Expired(time.Now()) {
				continue
			}
			if ns != "" && !strings.EqualFold(e.Namespace, ns) {
				continue
			}
			if a.Kind != "" && !strings.EqualFold(e.Kind, strings.TrimSpace(a.Kind)) {
				continue
			}
			counts[kc{e.Namespace, e.Kind}]++
		}
		if len(counts) == 0 {
			return Result{Content: "no entries recorded", Preview: "none"}, nil
		}
		keys := make([]string, 0, len(counts))
		byKey := map[string]int{}
		for k, v := range counts {
			s := k.ns + "/" + k.kind
			keys = append(keys, s)
			byKey[s] = v
		}
		sort.Strings(keys)
		var b strings.Builder
		b.WriteString("ledger counts:\n")
		for _, k := range keys {
			fmt.Fprintf(&b, "  %-24s %d\n", k, byKey[k])
		}
		out := b.String()
		return Result{Content: out, Preview: preview(out)}, nil

	case "delete":
		if ns == "" || key == "" {
			return Result{}, fmt.Errorf("delete requires namespace, key")
		}
		if l.Delete(ns, key) {
			if err := l.Save(); err != nil {
				return Result{}, fmt.Errorf("delete failed: %w", err)
			}
			return Result{Content: fmt.Sprintf("deleted %s:%s", ns, key), Preview: "deleted"}, nil
		}
		return Result{Content: fmt.Sprintf("not found: %s:%s", ns, key), Preview: "not found"}, nil

	case "clear":
		if ns == "" {
			return Result{}, fmt.Errorf("clear requires namespace")
		}
		n := l.Clear(ns)
		if err := l.Save(); err != nil {
			return Result{}, fmt.Errorf("clear failed: %w", err)
		}
		return Result{Content: fmt.Sprintf("cleared %d entries in %s", n, ns), Preview: fmt.Sprintf("cleared %d", n)}, nil

	default:
		return Result{}, fmt.Errorf("unknown action %q (check|record|get|list|count|delete|clear)", a.Action)
	}
}

func dataOrNone(d json.RawMessage) string {
	if len(d) == 0 {
		return "<none>"
	}
	return string(d)
}
