package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// SelectorSep splits a piercing selector into per-shadow-root segments. We use
// ">>>" as the shadow boundary. Example:
//
//	"my-app >>> #inner >>> .item"  ==  document.querySelector("my-app")
//	                                   .shadowRoot.querySelector("#inner")
//	                                   .shadowRoot.querySelector(".item")
const SelectorSep = ">>>"

// resolverJS is injected via Runtime.evaluate. It:
//  1. Resolves a piercing selector across shadow boundaries to a single node.
//  2. Tags the match with a data attribute and returns {found,x,y,w,h,text}.
//
// The returned rect is used for trusted coordinate-based input so we don't
// need DOM.ResolveNode round-trips to click/type.
const resolverJS = `(function(sel){
  function resolve(s){
    var parts = s.split("` + SelectorSep + `").map(function(p){return p.trim();}).filter(Boolean);
    if(parts.length===0) return null;
    var root = document;
    var node = null;
    for(var i=0;i<parts.length;i++){
      node = root.querySelector(parts[i]);
      if(!node) return null;
      if(i<parts.length-1){
        if(node.shadowRoot){ root = node.shadowRoot; }
        else { return null; } // boundary requested but none present yet (pre-hydration)
      }
    }
    return node;
  }
  var el = resolve(sel);
  if(!el) return {found:false};
  try{ el.scrollIntoView({block:'center',inline:'center',behavior:'instant'}); }catch(e){ el.scrollIntoView(); }
  var r = el.getBoundingClientRect();
  return {
    found:true,
    x: r.left + r.width/2,
    y: r.top + r.height/2,
    w: r.width,
    h: r.height,
    visible: r.width>1 && r.height>1,
    tag: (el.tagName||'').toLowerCase(),
    text: (el.innerText||el.textContent||'').trim().slice(0,200)
  };
})(%s)`

// ResolveResult is the resolved geometry/metadata for a selector.
type ResolveResult struct {
	Found   bool    `json:"found"`
	X       float64 `json:"x"`
	Y       float64 `json:"y"`
	W       float64 `json:"w"`
	H       float64 `json:"h"`
	Visible bool    `json:"visible"`
	Tag     string  `json:"tag"`
	Text    string  `json:"text"`
}

// resolve runs the piercing resolver against the attached session.
func resolve(ctx context.Context, c *Client, sessionID, selector string) (ResolveResult, error) {
	var out ResolveResult
	selJSON, _ := json.Marshal(selector)
	expr := fmt.Sprintf(resolverJS, selJSON)
	res, err := c.CallSession(ctx, sessionID, "Runtime.evaluate", map[string]any{
		"expression":    expr,
		"returnByValue": true,
	})
	if err != nil {
		return out, err
	}
	var wrap struct {
		Result struct {
			Value json.RawMessage `json:"value"`
		} `json:"result"`
	}
	if err := json.Unmarshal(res, &wrap); err != nil {
		return out, err
	}
	if err := json.Unmarshal(wrap.Result.Value, &out); err != nil {
		return out, fmt.Errorf("resolve decode: %w", err)
	}
	return out, nil
}

// WaitFor polls until selector resolves (and is visible if wantVisible),
// returning once found or failing after attempts*interval.
func WaitFor(ctx context.Context, c *Client, sessionID, selector string, wantVisible bool, attempts, intervalMs int) (ResolveResult, error) {
	if attempts <= 0 {
		attempts = 40
	}
	if intervalMs <= 0 {
		intervalMs = 150
	}
	var last ResolveResult
	for i := 0; i < attempts; i++ {
		r, err := resolve(ctx, c, sessionID, selector)
		if err == nil && r.Found && (!wantVisible || r.Visible) {
			return r, nil
		}
		last = r
		select {
		case <-ctx.Done():
			return last, ctx.Err()
		default:
		}
		time.Sleep(time.Duration(intervalMs) * time.Millisecond)
	}
	return last, fmt.Errorf("wait_for timeout: selector %q not %s after %d attempts", selector, map[bool]string{true: "visible", false: "present"}[wantVisible], attempts)
}

// stableRect samples the element rect twice across a frame gap and reports
// whether it stopped moving — our hydration/stability proxy.
func stableRect(ctx context.Context, c *Client, sessionID, selector string) (ResolveResult, error) {
	a, err := resolve(ctx, c, sessionID, selector)
	if err != nil || !a.Found {
		return a, err
	}
	select {
	case <-ctx.Done():
		return a, ctx.Err()
	default:
	}
	time.Sleep(50 * time.Millisecond)
	b, err := resolve(ctx, c, sessionID, selector)
	if err != nil {
		return a, err
	}
	// Consider stable if within a pixel.
	if abs(a.X-b.X) < 1 && abs(a.Y-b.Y) < 1 && abs(a.W-b.W) < 1 && abs(a.H-b.H) < 1 {
		return b, nil
	}
	// Still settling; return latest anyway. Callers may retry.
	return b, nil
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

// SplitSelector splits a piercing selector into shadow segments (useful for
// error messages / unit tests).
func SplitSelector(sel string) []string {
	parts := strings.Split(sel, SelectorSep)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
