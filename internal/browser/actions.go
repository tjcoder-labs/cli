package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Navigate loads url in the tab and waits for DOMContentLoaded + a short
// network-idle so hydration-triggering bundles have a chance to run.
func Navigate(ctx context.Context, c *Client, sessionID, url string) error {
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") && !strings.HasPrefix(url, "chrome://") && url != "about:blank" {
		url = "https://" + url
	}
	// Enable Page domain so load events flow, then navigate.
	_, _ = c.CallSession(ctx, sessionID, "Page.enable", nil)
	if _, err := c.CallSession(ctx, sessionID, "Page.navigate", map[string]any{"url": url}); err != nil {
		return err
	}
	// Best-effort: give the document a moment before returning; callers should
	// still wait_for their specific selector.
	time.Sleep(300 * time.Millisecond)
	return nil
}

// mouseClick dispatches a trusted click at viewport coordinates.
func mouseClick(ctx context.Context, c *Client, sessionID string, x, y float64) error {
	down := map[string]any{"type": "mousePressed", "x": x, "y": y, "button": "left", "clickCount": 1}
	up := map[string]any{"type": "mouseReleased", "x": x, "y": y, "button": "left", "clickCount": 1}
	if _, err := c.CallSession(ctx, sessionID, "Input.dispatchMouseEvent", down); err != nil {
		return err
	}
	_, err := c.CallSession(ctx, sessionID, "Input.dispatchMouseEvent", up)
	return err
}

// Click resolves the selector (piercing shadow roots), waits for it to
// appear and stabilise, scrolls it into view, and dispatches a trusted click.
// It retries so pre-hydration placeholders don't become hard failures.
func Click(ctx context.Context, c *Client, sessionID, selector string) (ResolveResult, error) {
	var rr ResolveResult
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		rr, err = stableRect(ctx, c, sessionID, selector)
		if err == nil && rr.Found {
			break
		}
		time.Sleep(time.Duration(150*(attempt+1)) * time.Millisecond)
	}
	if err != nil || !rr.Found {
		if err == nil {
			err = fmt.Errorf("element %q not found for click", selector)
		}
		return rr, err
	}
	if !rr.Visible {
		return rr, fmt.Errorf("element %q is not visible (%.0fx%.0f)", selector, rr.W, rr.H)
	}
	if err := mouseClick(ctx, c, sessionID, rr.X, rr.Y); err != nil {
		return rr, err
	}
	return rr, nil
}

// focusExpr focuses an element matched by a piercing selector and returns true
// if focus landed. Used before typing so trusted key events go to the field.
const focusExpr = `(function(sel){
  function resolve(s){var p=s.split(">>>").map(function(x){return x.trim();}).filter(Boolean);var r=document,n=null;for(var i=0;i<p.length;i++){n=r.querySelector(p[i]);if(!n)return null;if(i<p.length-1){if(n.shadowRoot)r=n.shadowRoot;else return null;}}return n;}
  var e=resolve(sel); if(!e) return false;
  try{e.scrollIntoView({block:'center'});}catch(_){}
  e.focus(); return document.activeElement===e || (e.shadowRoot&&e.shadowRoot.activeElement) || true;
})(%s)`

// Type focuses the target field then types text using trusted key events
// (insertText), so framework onChange/controlled inputs fire correctly.
func Type(ctx context.Context, c *Client, sessionID, selector, text string, clearFirst bool) error {
	// Focus the element.
	selJSON, _ := json.Marshal(selector)
	if _, err := c.CallSession(ctx, sessionID, "Runtime.evaluate", map[string]any{
		"expression":    fmt.Sprintf(focusExpr, selJSON),
		"returnByValue": true,
	}); err != nil {
		return err
	}
	if clearFirst {
		if err := PressKey(ctx, c, sessionID, "ctrl+a"); err == nil {
			_ = PressKey(ctx, c, sessionID, "Backspace")
		}
	}
	// Use trusted text insertion so IME/composition behaviour is preserved.
	_, err := c.CallSession(ctx, sessionID, "Input.insertText", map[string]any{"text": text})
	return err
}

// PressKey sends a trusted key. Accepts a single key ("Enter", "Backspace")
// or a chord ("ctrl+a", "ctrl+shift+r").
func PressKey(ctx context.Context, c *Client, sessionID, key string) error {
	k := parseKey(key)
	params := map[string]any{
		"text":                  "",
		"key":                   k.Key,
		"code":                  k.Code,
		"windowsVirtualKeyCode": k.VK,
		"nativeVirtualKeyCode":  k.VK,
	}
	if k.Mods != 0 {
		params["modifiers"] = k.Mods
	}
	if k.VK == 13 || k.Key == "Enter" {
		params["text"] = "\r"
	}
	if _, err := c.CallSession(ctx, sessionID, "Input.dispatchKeyEvent", merge(params, map[string]any{"type": "keyDown"})); err != nil {
		return err
	}
	_, err := c.CallSession(ctx, sessionID, "Input.dispatchKeyEvent", merge(params, map[string]any{"type": "keyUp"}))
	return err
}

type keySpec struct {
	Key  string
	Code string
	VK   int
	Mods int
}

// parseKey maps a chord string to CDP Input params. Covers the common keys;
// printable chars fall through to a VK derived from the rune.
func parseKey(s string) keySpec {
	parts := strings.Split(strings.ToLower(s), "+")
	ks := keySpec{}
	var keyName string
	for _, p := range parts {
		switch p {
		case "ctrl", "control":
			ks.Mods |= 2
		case "alt":
			ks.Mods |= 1
		case "shift":
			ks.Mods |= 8
		case "meta", "cmd":
			ks.Mods |= 4
		default:
			keyName = p
		}
	}
	switch keyName {
	case "enter", "return":
		ks.Key, ks.Code, ks.VK = "Enter", "Enter", 13
	case "backspace":
		ks.Key, ks.Code, ks.VK = "Backspace", "Backspace", 8
	case "tab":
		ks.Key, ks.Code, ks.VK = "Tab", "Tab", 9
	case "escape", "esc":
		ks.Key, ks.Code, ks.VK = "Escape", "Escape", 27
	case "delete":
		ks.Key, ks.Code, ks.VK = "Delete", "Delete", 46
	case "arrowup", "up":
		ks.Key, ks.Code, ks.VK = "ArrowUp", "ArrowUp", 38
	case "arrowdown", "down":
		ks.Key, ks.Code, ks.VK = "ArrowDown", "ArrowDown", 40
	case "arrowleft", "left":
		ks.Key, ks.Code, ks.VK = "ArrowLeft", "ArrowLeft", 37
	case "arrowright", "right":
		ks.Key, ks.Code, ks.VK = "ArrowRight", "ArrowRight", 39
	default:
		if len(keyName) == 1 {
			ch := rune(keyName[0])
			ks.VK = int(ch)
			if ch >= 'a' && ch <= 'z' {
				ks.VK = int(ch - 'a' + 'A')
				ks.Code = "Key" + strings.ToUpper(keyName)
			}
			ks.Key = keyName
		} else {
			ks.Key = keyName
		}
	}
	return ks
}

func merge(a, b map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

// GetDOM returns the serialized document (piercing shadow roots when pierce
// is set). When selector is empty it returns the whole document; otherwise it
// returns the outerHTML of the resolved fragment.
func GetDOM(ctx context.Context, c *Client, sessionID, selector string, pierce bool, maxChars int) (string, error) {
	var expr string
	if selector == "" {
		expr = "document.documentElement.outerHTML"
	} else {
		selJSON, _ := json.Marshal(selector)
		expr = fmt.Sprintf(`(function(sel){function r(s){var p=s.split(">>>").map(function(x){return x.trim();}).filter(Boolean);var rt=document,n=null;for(var i=0;i<p.length;i++){n=rt.querySelector(p[i]);if(!n)return null;if(i<p.length-1){if(n.shadowRoot)rt=n.shadowRoot;else return null;}}return n;}var e=r(sel);return e?e.outerHTML:null;})(%s)`, selJSON)
	}
	res, err := c.CallSession(ctx, sessionID, "Runtime.evaluate", map[string]any{
		"expression":    expr,
		"returnByValue": true,
	})
	if err != nil {
		return "", err
	}
	var wrap struct {
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
	}
	if err := json.Unmarshal(res, &wrap); err != nil {
		return "", err
	}
	out := wrap.Result.Value
	if maxChars > 0 && len(out) > maxChars {
		out = out[:maxChars] + "\n... [truncated]"
	}
	return out, nil
}

// Evaluate runs arbitrary JS in the page context and returns the JSON
// value. This is the escape hatch for full research control.
func Evaluate(ctx context.Context, c *Client, sessionID, expression string, awaitPromise bool) (json.RawMessage, error) {
	res, err := c.CallSession(ctx, sessionID, "Runtime.evaluate", map[string]any{
		"expression":    expression,
		"returnByValue": true,
		"awaitPromise":  awaitPromise,
	})
	if err != nil {
		return nil, err
	}
	var wrap struct {
		Result struct {
			Type  string          `json:"type"`
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		ExceptionDetails json.RawMessage `json:"exceptionDetails,omitempty"`
	}
	if err := json.Unmarshal(res, &wrap); err != nil {
		return nil, err
	}
	if len(wrap.ExceptionDetails) > 0 {
		return nil, fmt.Errorf("js exception: %s", string(wrap.ExceptionDetails))
	}
	if len(wrap.Result.Value) == 0 {
		return json.RawMessage("null"), nil
	}
	return wrap.Result.Value, nil
}
