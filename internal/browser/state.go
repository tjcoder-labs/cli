package browser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
)

// Cookie mirrors CDP Network.Cookie for get/set/delete.
type Cookie struct {
	Name     string  `json:"name"`
	Value    string  `json:"value"`
	Domain   string  `json:"domain,omitempty"`
	Path     string  `json:"path,omitempty"`
	Expires  float64 `json:"expires,omitempty"`
	HTTPOnly bool    `json:"httpOnly,omitempty"`
	Secure   bool    `json:"secure,omitempty"`
	SameSite string  `json:"sameSite,omitempty"`
	URL      string  `json:"url,omitempty"`
}

// GetCookies returns cookies for the whole browser (or filtered to url).
func GetCookies(ctx context.Context, c *Client, url string) ([]Cookie, error) {
	params := map[string]any{}
	if url != "" {
		params["urls"] = []string{url}
	}
	res, err := c.Call(ctx, "Storage.getCookies", params)
	if err != nil {
		// Fall back to Network.getAllCookies on older targets.
		res, err = c.Call(ctx, "Network.getAllCookies", nil)
		if err != nil {
			return nil, err
		}
	}
	var out struct {
		Cookies []Cookie `json:"cookies"`
	}
	if err := json.Unmarshal(res, &out); err != nil {
		return nil, err
	}
	return out.Cookies, nil
}

// SetCookie sets a cookie. URL or Domain+Path is required.
func SetCookie(ctx context.Context, c *Client, ck Cookie) error {
	p := map[string]any{"name": ck.Name, "value": ck.Value}
	if ck.URL != "" {
		p["url"] = ck.URL
	}
	if ck.Domain != "" {
		p["domain"] = ck.Domain
	}
	if ck.Path != "" {
		p["path"] = ck.Path
	}
	if ck.Expires > 0 {
		p["expires"] = ck.Expires
	}
	if ck.HTTPOnly {
		p["httpOnly"] = true
	}
	if ck.Secure {
		p["secure"] = true
	}
	if ck.SameSite != "" {
		p["sameSite"] = ck.SameSite
	}
	_, err := c.Call(ctx, "Storage.setCookies", map[string]any{"cookies": []map[string]any{p}})
	return err
}

// DeleteCookies removes a cookie by name (optionally scoped by url/domain).
func DeleteCookies(ctx context.Context, c *Client, name, url, domain string) error {
	p := map[string]any{"name": name}
	if url != "" {
		p["url"] = url
	}
	if domain != "" {
		p["domain"] = domain
	}
	_, err := c.Call(ctx, "Network.deleteCookies", p)
	return err
}

// ClearSiteData wipes cookies, storage, cache for an origin.
func ClearSiteData(ctx context.Context, c *Client, origin string) error {
	_, err := c.Call(ctx, "Storage.clearDataForOrigin", map[string]any{
		"origin":       origin,
		"storageTypes": "cookies,local_storage,session_storage,indexeddb,service_workers,cache_storage",
	})
	return err
}

// storageJS reads or writes a key in localStorage/sessionStorage.
func storageJS(kind, op, key string, value *string) string {
	store := "localStorage"
	if kind == "session" {
		store = "sessionStorage"
	}
	switch op {
	case "get":
		return fmt.Sprintf(`(function(){var v=window.%s.getItem(%q);return v===null?null:v;})()`, store, key)
	case "set":
		if value == nil {
			return "null"
		}
		b, _ := json.Marshal(*value)
		return fmt.Sprintf(`(function(){window.%s.setItem(%q, %s);return true;})()`, store, key, string(b))
	case "remove":
		return fmt.Sprintf(`(function(){window.%s.removeItem(%q);return true;})()`, store, key)
	case "keys":
		return fmt.Sprintf(`(function(){var o=[];var s=window.%s;for(var i=0;i<s.length;i++){o.push(s.key(i));}return o;})()`, store)
	}
	return "null"
}

// StorageGet reads a storage key. kind: "local"|"session".
func StorageGet(ctx context.Context, c *Client, sessionID, kind, key string) (string, bool, error) {
	raw, err := Evaluate(ctx, c, sessionID, storageJS(kind, "get", key, nil), false)
	if err != nil {
		return "", false, err
	}
	s := string(raw)
	if s == "null" {
		return "", false, nil
	}
	var out string
	if err := json.Unmarshal(raw, &out); err != nil {
		return string(raw), false, nil
	}
	return out, true, nil
}

// StorageSet writes a storage key.
func StorageSet(ctx context.Context, c *Client, sessionID, kind, key, value string) error {
	_, err := Evaluate(ctx, c, sessionID, storageJS(kind, "set", key, &value), false)
	return err
}

// Emulate applies device/viewport/user-agent/locale/timezone emulation.
func Emulate(ctx context.Context, c *Client, sessionID string, width, height int, mobile bool, userAgent, locale, timezone string) error {
	if width > 0 && height > 0 {
		_, err := c.CallSession(ctx, sessionID, "Emulation.setDeviceMetricsOverride", map[string]any{
			"width": width, "height": height, "deviceScaleFactor": 1, "mobile": mobile,
		})
		if err != nil {
			return err
		}
	}
	if userAgent != "" {
		if _, err := c.CallSession(ctx, sessionID, "Emulation.setUserAgentOverride", map[string]any{"userAgent": userAgent}); err != nil {
			return err
		}
	}
	if locale != "" {
		_, _ = c.CallSession(ctx, sessionID, "Emulation.setLocaleOverride", map[string]any{"locale": locale})
	}
	if timezone != "" {
		_, _ = c.CallSession(ctx, sessionID, "Emulation.setTimezoneOverride", map[string]any{"timezoneId": timezone})
	}
	return nil
}

// Screenshot captures the tab as PNG and returns base64.
func Screenshot(ctx context.Context, c *Client, sessionID string, quality int) (string, error) {
	_, _ = c.CallSession(ctx, sessionID, "Page.enable", nil)
	res, err := c.CallSession(ctx, sessionID, "Page.captureScreenshot", map[string]any{
		"format": "png",
	})
	if err != nil {
		return "", err
	}
	var out struct {
		Data string `json:"data"`
	}
	if err := json.Unmarshal(res, &out); err != nil {
		return "", err
	}
	// Sanity check it's actually decodable base64.
	if _, err := base64.StdEncoding.DecodeString(out.Data); err != nil {
		return "", fmt.Errorf("screenshot not base64: %w", err)
	}
	return out.Data, nil
}
