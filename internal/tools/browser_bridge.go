package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tjcoder-labs/cli/internal/browser"
	"github.com/tjcoder-labs/cli/internal/client"
)

// browserBridgeState caches the bridge across tool calls for this process.
var (
	bridgeMu   sync.Mutex
	bridgeInst *browser.Bridge
)

// getBridge dials (once) and returns the process-wide bridge. agent names the
// owning agent for tab registry bookkeeping.
func getBridge(ctx context.Context, port int, agent string) (*browser.Bridge, error) {
	bridgeMu.Lock()
	defer bridgeMu.Unlock()
	if bridgeInst != nil {
		return bridgeInst, nil
	}
	b, err := browser.DialBridge(ctx, port, agent)
	if err != nil {
		return nil, err
	}
	bridgeInst = b
	return b, nil
}

// browserBridgeTool exposes full Chrome control over CDP.
type browserBridgeTool struct{}

func (browserBridgeTool) Definition() client.ToolDefinition {
	props := map[string]any{
		"action":        stringProp("Operation to perform. One of: list_tabs, new_tab, close_tab, claim_tab, release_tab, release_all, navigate, reload, back, forward, evaluate, click, type, press_key, wait_for, get_dom, screenshot, get_cookies, set_cookie, delete_cookie, clear_site_data, storage_get, storage_set, storage_keys, emulate, intercept_enable, intercept_disable."),
		"tab_id":        stringProp("Target tab ID. Required for most actions except list_tabs/new_tab. Use list_tabs to find IDs. Tabs you create via new_tab are auto-owned."),
		"url":           stringProp("URL for new_tab / navigate."),
		"selector":      stringProp("CSS selector, supports shadow piercing with '>>>' e.g. 'my-app >>> #inner'. Used by click/type/wait_for/get_dom."),
		"text":          stringProp("Text for 'type', expression for 'evaluate', key for 'press_key' (e.g. 'Enter', 'ctrl+a')."),
		"expression":    stringProp("JavaScript to evaluate (action=evaluate). Alias of text."),
		"key":           stringProp("Key/chord for press_key, or storage key for storage_get/set."),
		"value":         stringProp("Value for storage_set / set_cookie."),
		"kind":          stringProp("'local' or 'session' for storage_* actions."),
		"visible":       boolProp("wait_for: require the element be visible (default true)."),
		"pierce":        boolProp("get_dom: also pierce shadow roots (default true)."),
		"max_chars":     numberProp("get_dom: truncate serialized DOM to this many chars."),
		"attempts":      numberProp("wait_for: number of poll attempts (default 40)."),
		"interval_ms":   numberProp("wait_for: delay between polls in ms (default 150)."),
		"clear_first":   boolProp("type: clear the field before typing."),
		"await_promise": boolProp("evaluate: await returned promise (default true)."),
		// Cookie fields
		"name":   stringProp("Cookie/record name for set_cookie/delete_cookie."),
		"domain": stringProp("Cookie domain."),
		"path":   stringProp("Cookie path."),
		// Emulation fields
		"width":      numberProp("emulate: viewport width."),
		"height":     numberProp("emulate: viewport height."),
		"mobile":     boolProp("emulate: mobile flag."),
		"user_agent": stringProp("emulate/set_cookie: user agent override."),
		"locale":     stringProp("emulate: locale e.g. en-US."),
		"timezone":   stringProp("emulate: IANA timezone e.g. America/New_York."),
		// Control
		"port":  numberProp("CDP debug port (default 9222)."),
		"force": boolProp("claim_tab: take a tab owned by another session."),
	}
	return client.ToolDefinition{
		Type: "function",
		Function: client.FunctionDefinition{
			Name:        "browser_bridge",
			Description: "Control a locally running Chrome (started with --remote-debugging-port) over CDP. Manage tabs, navigate, run JS, click/type (trusted input), wait with shadow-DOM-piercing selectors, capture screenshots/DOM, and manage cookies/storage/emulation. Tabs created or claimed by this session are tracked so concurrent agents don't interfere with each other's or the user's tabs.",
			Parameters:  objectSchema([]string{"action"}, props),
		},
	}
}

func (browserBridgeTool) Execute(ctx context.Context, raw json.RawMessage, env ExecEnv) (Result, error) {
	var a struct {
		Action       string `json:"action"`
		TabID        string `json:"tab_id"`
		URL          string `json:"url"`
		Selector     string `json:"selector"`
		Text         string `json:"text"`
		Expression   string `json:"expression"`
		Key          string `json:"key"`
		Value        string `json:"value"`
		Kind         string `json:"kind"`
		Visible      *bool  `json:"visible"`
		Pierce       *bool  `json:"pierce"`
		MaxChars     int    `json:"max_chars"`
		Attempts     int    `json:"attempts"`
		IntervalMs   int    `json:"interval_ms"`
		ClearFirst   bool   `json:"clear_first"`
		AwaitPromise *bool  `json:"await_promise"`
		Name         string `json:"name"`
		Domain       string `json:"domain"`
		Path         string `json:"path"`
		Width        int    `json:"width"`
		Height       int    `json:"height"`
		Mobile       bool   `json:"mobile"`
		UserAgent    string `json:"user_agent"`
		Locale       string `json:"locale"`
		Timezone     string `json:"timezone"`
		Port         int    `json:"port"`
		Force        bool   `json:"force"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return Result{}, err
	}
	a.Action = strings.TrimSpace(strings.ToLower(a.Action))
	if a.Action == "" {
		return Result{}, fmt.Errorf("action is required")
	}

	agent := "coder"
	if env.SessionState != nil && env.SessionState.CurrentAgent != "" {
		agent = env.SessionState.CurrentAgent
	}
	b, err := getBridge(ctx, a.Port, agent)
	if err != nil {
		return Result{}, err
	}
	// Keep registry consistent with reality.
	b.GCRegistry(ctx)

	// Helper to resolve a session for an owned tab.
	sessionFor := func(tabID string) (string, error) {
		if err := b.Registry.RequireOwned(tabID); err != nil {
			return "", err
		}
		return b.SessionFor(ctx, tabID)
	}

	switch a.Action {
	case "list_tabs":
		targets, err := b.Client.ListTargets(ctx)
		if err != nil {
			return Result{}, err
		}
		var sb strings.Builder
		for _, t := range targets {
			if t.Type != "page" {
				continue // skip iframes/service workers in the listing
			}
			owner := b.Registry.OwnershipOf(t.ID)
			fmt.Fprintf(&sb, "%s\t[%s]\t%s\t%s\n", t.ID, owner, t.Title, t.URL)
		}
		out := sb.String()
		if out == "" {
			out = "(no tabs)"
		}
		return Result{Content: out, Preview: preview(out)}, nil

	case "new_tab":
		id, err := b.Client.NewTab(ctx, a.URL)
		if err != nil {
			return Result{}, err
		}
		_ = b.Registry.Register(id, agent, false)
		msg := fmt.Sprintf("created tab %s (owned by this session)", id)
		return Result{Content: msg, Preview: msg}, nil

	case "claim_tab":
		if a.TabID == "" {
			return Result{}, fmt.Errorf("tab_id required")
		}
		if err := b.Registry.Claim(a.TabID, agent, a.Force); err != nil {
			return Result{}, err
		}
		msg := fmt.Sprintf("claimed tab %s", a.TabID)
		return Result{Content: msg, Preview: msg}, nil

	case "release_tab":
		if a.TabID == "" {
			return Result{}, fmt.Errorf("tab_id required")
		}
		if err := b.Registry.Release(a.TabID); err != nil {
			return Result{}, err
		}
		return Result{Content: "released " + a.TabID, Preview: "released"}, nil

	case "release_all":
		if err := b.ReleaseAll(ctx); err != nil {
			return Result{}, err
		}
		return Result{Content: "released all owned tabs", Preview: "released all"}, nil

	case "close_tab":
		if a.TabID == "" {
			return Result{}, fmt.Errorf("tab_id required")
		}
		if err := b.Registry.RequireOwned(a.TabID); err != nil {
			return Result{}, err
		}
		if err := b.Client.CloseTab(ctx, a.TabID); err != nil {
			return Result{}, err
		}
		_ = b.Registry.Release(a.TabID)
		return Result{Content: "closed " + a.TabID, Preview: "closed"}, nil

	case "navigate":
		if a.TabID == "" || a.URL == "" {
			return Result{}, fmt.Errorf("tab_id and url required")
		}
		sid, err := sessionFor(a.TabID)
		if err != nil {
			return Result{}, err
		}
		if err := browser.Navigate(ctx, b.Client, sid, a.URL); err != nil {
			return Result{}, err
		}
		return Result{Content: "navigated to " + a.URL, Preview: "navigated"}, nil

	case "reload", "back", "forward":
		sid, err := sessionFor(a.TabID)
		if err != nil {
			return Result{}, err
		}
		var method string
		switch a.Action {
		case "reload":
			method = "Page.reload"
		case "back", "forward":
			// Use history navigation via JS (simplest reliable path).
			delta := "-1"
			if a.Action == "forward" {
				delta = "1"
			}
			if _, err := browser.Evaluate(ctx, b.Client, sid, "history.go("+delta+")", false); err != nil {
				return Result{}, err
			}
			return Result{Content: a.Action + " ok", Preview: a.Action}, nil
		}
		if _, err := b.Client.CallSession(ctx, sid, method, map[string]any{"ignoreCache": true}); err != nil {
			return Result{}, err
		}
		return Result{Content: a.Action + " ok", Preview: a.Action}, nil

	case "evaluate":
		expr := a.Expression
		if expr == "" {
			expr = a.Text
		}
		if expr == "" {
			return Result{}, fmt.Errorf("expression required")
		}
		sid, err := sessionFor(a.TabID)
		if err != nil {
			return Result{}, err
		}
		// Default awaitPromise to true.
		await := true
		if a.AwaitPromise != nil {
			await = *a.AwaitPromise
		}
		out, err := browser.Evaluate(ctx, b.Client, sid, expr, await)
		if err != nil {
			return Result{}, err
		}
		return Result{Content: string(out), Preview: preview(string(out))}, nil

	case "click":
		if a.Selector == "" {
			return Result{}, fmt.Errorf("selector required")
		}
		sid, err := sessionFor(a.TabID)
		if err != nil {
			return Result{}, err
		}
		rr, err := browser.Click(ctx, b.Client, sid, a.Selector)
		if err != nil {
			return Result{}, err
		}
		msg := fmt.Sprintf("clicked %q (%s, %.0fx%.0f)", a.Selector, rr.Tag, rr.W, rr.H)
		return Result{Content: msg, Preview: msg}, nil

	case "type":
		if a.Selector == "" {
			return Result{}, fmt.Errorf("selector required")
		}
		sid, err := sessionFor(a.TabID)
		if err != nil {
			return Result{}, err
		}
		if err := browser.Type(ctx, b.Client, sid, a.Selector, a.Text, a.ClearFirst); err != nil {
			return Result{}, err
		}
		return Result{Content: fmt.Sprintf("typed %d chars into %q", len(a.Text), a.Selector), Preview: "typed"}, nil

	case "press_key":
		key := a.Key
		if key == "" {
			key = a.Text
		}
		if key == "" {
			return Result{}, fmt.Errorf("key required")
		}
		sid, err := sessionFor(a.TabID)
		if err != nil {
			return Result{}, err
		}
		if err := browser.PressKey(ctx, b.Client, sid, key); err != nil {
			return Result{}, err
		}
		return Result{Content: "pressed " + key, Preview: "pressed " + key}, nil

	case "wait_for":
		if a.Selector == "" {
			return Result{}, fmt.Errorf("selector required")
		}
		sid, err := sessionFor(a.TabID)
		if err != nil {
			return Result{}, err
		}
		visible := true
		if a.Visible != nil {
			visible = *a.Visible
		}
		rr, err := browser.WaitFor(ctx, b.Client, sid, a.Selector, visible, a.Attempts, a.IntervalMs)
		if err != nil {
			return Result{}, err
		}
		msg := fmt.Sprintf("found %q (%s %v)", a.Selector, rr.Tag, rr.Visible)
		return Result{Content: msg, Preview: msg}, nil

	case "get_dom":
		sid, err := sessionFor(a.TabID)
		if err != nil {
			return Result{}, err
		}
		pierce := true
		if a.Pierce != nil {
			pierce = *a.Pierce
		}
		max := a.MaxChars
		if max <= 0 {
			max = 20000
		}
		out, err := browser.GetDOM(ctx, b.Client, sid, a.Selector, pierce, max)
		if err != nil {
			return Result{}, err
		}
		return Result{Content: out, Preview: preview(out)}, nil

	case "screenshot":
		sid, err := sessionFor(a.TabID)
		if err != nil {
			return Result{}, err
		}
		data, err := browser.Screenshot(ctx, b.Client, sid, 0)
		if err != nil {
			return Result{}, err
		}
		// Write the PNG to the research dir rather than emitting base64
		// inline: a 1920x1080 PNG is ~600-900kB of base64, which (a)
		// floods the model context window (~200-300k tokens per shot)
		// and (b) permanently inflates session history. The agent can
		// read_or_open the file via read_file/research_note if it needs
		// to inspect the pixels, or just report the path to the user.
		dir := DefaultResearchDir()
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return Result{}, fmt.Errorf("mkdir screenshots: %w", err)
		}
		fname := filepath.Join(dir, fmt.Sprintf("screenshot-%d.png", time.Now().UnixNano()))
		raw, err := base64.StdEncoding.DecodeString(data)
		if err != nil {
			return Result{}, fmt.Errorf("decode screenshot: %w", err)
		}
		if err := os.WriteFile(fname, raw, 0o600); err != nil {
			return Result{}, fmt.Errorf("write screenshot: %w", err)
		}
		summary := fmt.Sprintf("screenshot saved to %s (%d bytes PNG; base64 length %d)", fname, len(raw), len(data))
		return Result{Content: summary, Preview: summary}, nil

	case "get_cookies":
		cks, err := browser.GetCookies(ctx, b.Client, a.URL)
		if err != nil {
			return Result{}, err
		}
		buf, _ := json.MarshalIndent(cks, "", "  ")
		return Result{Content: string(buf), Preview: preview(string(buf))}, nil

	case "set_cookie":
		if a.Name == "" {
			return Result{}, fmt.Errorf("name required")
		}
		ck := browser.Cookie{Name: a.Name, Value: a.Value, Domain: a.Domain, Path: a.Path, URL: a.URL}
		if err := browser.SetCookie(ctx, b.Client, ck); err != nil {
			return Result{}, err
		}
		return Result{Content: "cookie set", Preview: "cookie set"}, nil

	case "delete_cookie":
		if a.Name == "" {
			return Result{}, fmt.Errorf("name required")
		}
		if err := browser.DeleteCookies(ctx, b.Client, a.Name, a.URL, a.Domain); err != nil {
			return Result{}, err
		}
		return Result{Content: "cookie deleted", Preview: "cookie deleted"}, nil

	case "clear_site_data":
		if a.URL == "" {
			return Result{}, fmt.Errorf("url (origin) required")
		}
		if err := browser.ClearSiteData(ctx, b.Client, a.URL); err != nil {
			return Result{}, err
		}
		return Result{Content: "site data cleared for " + a.URL, Preview: "cleared"}, nil

	case "storage_get":
		sid, err := sessionFor(a.TabID)
		if err != nil {
			return Result{}, err
		}
		v, found, err := browser.StorageGet(ctx, b.Client, sid, a.Kind, a.Key)
		if err != nil {
			return Result{}, err
		}
		if !found {
			return Result{Content: "(not found)", Preview: "(not found)"}, nil
		}
		return Result{Content: v, Preview: preview(v)}, nil

	case "storage_set":
		sid, err := sessionFor(a.TabID)
		if err != nil {
			return Result{}, err
		}
		if err := browser.StorageSet(ctx, b.Client, sid, a.Kind, a.Key, a.Value); err != nil {
			return Result{}, err
		}
		return Result{Content: "storage set", Preview: "storage set"}, nil

	case "emulate":
		sid, err := sessionFor(a.TabID)
		if err != nil {
			return Result{}, err
		}
		if err := browser.Emulate(ctx, b.Client, sid, a.Width, a.Height, a.Mobile, a.UserAgent, a.Locale, a.Timezone); err != nil {
			return Result{}, err
		}
		return Result{Content: "emulation applied", Preview: "emulated"}, nil

	default:
		return Result{}, fmt.Errorf("unknown browser_bridge action %q", a.Action)
	}
}
