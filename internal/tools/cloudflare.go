package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tjcoder-labs/cli/internal/client"
)

// cloudflareBaseURL is the Cloudflare API v4 root.
const cloudflareBaseURL = "https://api.cloudflare.com/client/v4"

// cloudflareTokenPath is where a user-provided API token is persisted
// across sessions (mode 0600, per-user). It is only written via the
// "auth" action and never logged.
func cloudflareTokenPath() string {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "coder", "cloudflare-token")
}

// cloudflareResolveToken returns the bearer token from, in order:
//   1. the CLOUDFLARE_API_TOKEN environment variable, then
//   2. the persisted token file (~/.local/share/coder/cloudflare-token).
// An empty string means "not configured".
func cloudflareResolveToken() string {
	if t := strings.TrimSpace(os.Getenv("CLOUDFLARE_API_TOKEN")); t != "" {
		return t
	}
	data, err := os.ReadFile(cloudflareTokenPath())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// cloudflareAPIError is a best-effort extraction of Cloudflare's error
// JSON so the agent/user sees actionable messages instead of a raw blob.
type cloudflareAPIError struct {
	Errors []struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
}

func (e cloudflareAPIError) String() string {
	parts := make([]string, 0, len(e.Errors))
	for _, er := range e.Errors {
		parts = append(parts, fmt.Sprintf("%d: %s", er.Code, er.Message))
	}
	if len(parts) == 0 {
		return "unknown API error"
	}
	return strings.Join(parts, "; ")
}

// cloudflareArgs is the decoded argument set for the cloudflare tool.
type cloudflareArgs struct {
	Action   string `json:"action"`
	Token    string `json:"token"`
	Zone     string `json:"zone"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	Content  string `json:"content"`
	TTL      int    `json:"ttl"`
	Proxied  bool   `json:"proxied"`
	Priority int    `json:"priority"`
	ID       string `json:"id"`
}

// cloudflareTool provides read/write access to Cloudflare zones and DNS
// records via the v4 REST API. Authentication uses a bearer API token
// (not email + global key). See cloudflareResolveToken for sourcing.
type cloudflareTool struct{}

func (cloudflareTool) Definition() client.ToolDefinition {
	return client.ToolDefinition{
		Type: "function",
		Function: client.FunctionDefinition{
			Name: "cloudflare",
			Description: `Manage Cloudflare zones and DNS records via the REST API v4.

AUTHENTICATION (required before any other action):
- Token is read from CLOUDFLARE_API_TOKEN env, or set once with action=auth (persisted to ~/.local/share/coder/cloudflare-token, mode 0600).
- To create a token: Cloudflare Dashboard -> My Profile -> API Tokens -> Create Token -> "Edit zone DNS" template -> pick zone -> Create. Copy the token and pass it to action=auth (or export CLOUDFLARE_API_TOKEN).

ACTIONS:
- auth(token): save the token locally and verify it with a /user/tokens/verify call.
- zones(): list zones (id + name + status).
- zone_id(name): resolve a zone name to its id (helper for the actions below).
- dns_list(zone, name?, type?): list DNS records for a zone.
- dns_create(zone, name, type, content, ttl?, proxied?, priority?): create a record.
- dns_update(zone, id, name?, content?, ttl?, proxied?): update a record.
- dns_delete(zone, id): delete a record.

SAFETY: list/read actions are safe. dns_create/dns_update/dns_delete mutate DNS; show the exact intent and confirm with the user before invoking. Never print the token.`,
			Parameters: objectSchema([]string{"action"}, map[string]any{
				"action":   stringProp("auth | zones | zone_id | dns_list | dns_create | dns_update | dns_delete"),
				"token":    stringProp("Bearer API token (only for action=auth)."),
				"zone":     stringProp("Zone name or id (e.g. example.com)."),
				"name":     stringProp("DNS record name (e.g. www or www.example.com)."),
				"type":     stringProp("DNS record type (A, AAAA, CNAME, TXT, MX, ...)."),
				"content":  stringProp("DNS record content (IP, target hostname, text)."),
				"ttl":      numberProp("TTL in seconds (1 = auto)."),
				"proxied":  boolProp("Route through Cloudflare proxy (orange cloud)."),
				"priority": numberProp("MX/SRV priority."),
				"id":       stringProp("Record id (for dns_update/dns_delete)."),
			}),
		},
	}
}

func (cloudflareTool) Execute(ctx context.Context, raw json.RawMessage, env ExecEnv) (Result, error) {
	var a cloudflareArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return Result{}, err
	}
	a.Action = strings.ToLower(strings.TrimSpace(a.Action))
	if a.Action == "" {
		return Result{}, fmt.Errorf("action is required (auth|zones|zone_id|dns_list|dns_create|dns_update|dns_delete)")
	}

	// auth is special: it persists the token before any API call.
	if a.Action == "auth" {
		return cloudflareHandleAuth(a.Token)
	}

	token := cloudflareResolveToken()
	if token == "" {
		return Result{}, fmt.Errorf("no Cloudflare API token configured; set CLOUDFLARE_API_TOKEN or use action=auth with your token")
	}

	switch a.Action {
	case "zones":
		body, err := cloudflareCall(ctx, token, "GET", "/zones", nil)
		if err != nil {
			return Result{}, err
		}
		return Result{Content: body, Preview: preview(body)}, nil
	case "zone_id":
		id, err := cloudflareResolveZoneID(ctx, token, a.Zone)
		if err != nil {
			return Result{}, err
		}
		out := fmt.Sprintf("zone %s -> %s", a.Zone, id)
		return Result{Content: out, Preview: out}, nil
	case "dns_list":
		return cloudflareDNSList(ctx, token, a.Zone, a.Name, a.Type)
	case "dns_create":
		return cloudflareDNSCreate(ctx, token, a)
	case "dns_update":
		return cloudflareDNSUpdate(ctx, token, a)
	case "dns_delete":
		return cloudflareDNSDelete(ctx, token, a.Zone, a.ID)
	default:
		return Result{}, fmt.Errorf("unknown action %q (auth|zones|zone_id|dns_list|dns_create|dns_update|dns_delete)", a.Action)
	}
}

// cloudflareHandleAuth persists the token and verifies it.
func cloudflareHandleAuth(token string) (Result, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return Result{}, fmt.Errorf("token is required for action=auth")
	}
	path := cloudflareTokenPath()
	if path == "" {
		return Result{}, fmt.Errorf("could not resolve token path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return Result{}, fmt.Errorf("create token dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		return Result{}, fmt.Errorf("write token: %w", err)
	}
	// Verify immediately so the user knows the token is valid.
	_, err := cloudflareCall(context.Background(), token, "GET", "/user/tokens/verify", nil)
	if err != nil {
		return Result{}, fmt.Errorf("token saved but verification failed: %w", err)
	}
	out := fmt.Sprintf("Cloudflare token saved to %s and verified", path)
	return Result{Content: out, Preview: "token verified"}, nil
}

// cloudflareResolveZoneID returns the zone id for a zone name. If the
// input already looks like a 32-hex id it is returned as-is.
func cloudflareResolveZoneID(ctx context.Context, token, zone string) (string, error) {
	zone = strings.TrimSpace(zone)
	if zone == "" {
		return "", fmt.Errorf("zone is required")
	}
	if len(zone) == 32 && isHex(zone) {
		return zone, nil
	}
	body, err := cloudflareCall(ctx, token, "GET", "/zones?name="+url.QueryEscape(zone)+"&status=active", nil)
	if err != nil {
		return "", err
	}
	var resp struct {
		Result []struct {
			ID     string `json:"id"`
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		return "", fmt.Errorf("parse zone response: %w", err)
	}
	for _, z := range resp.Result {
		if strings.EqualFold(z.Name, zone) {
			return z.ID, nil
		}
	}
	return "", fmt.Errorf("zone %q not found (or token lacks Zone:Read on it)", zone)
}

// cloudflareDNSList lists DNS records for a zone, optionally filtered
// by name and/or type.
func cloudflareDNSList(ctx context.Context, token, zone, name, recType string) (Result, error) {
	id, err := cloudflareResolveZoneID(ctx, token, zone)
	if err != nil {
		return Result{}, err
	}
	q := url.Values{}
	if name != "" {
		q.Set("name", name)
	}
	if recType != "" {
		q.Set("type", strings.ToUpper(recType))
	}
	endpoint := "/zones/" + id + "/dns_records"
	if len(q) > 0 {
		endpoint += "?" + q.Encode()
	}
	body, err := cloudflareCall(ctx, token, "GET", endpoint, nil)
	if err != nil {
		return Result{}, err
	}
	return Result{Content: body, Preview: preview(body)}, nil
}

// cloudflareDNSCreate creates a DNS record.
func cloudflareDNSCreate(ctx context.Context, token string, a cloudflareArgs) (Result, error) {
	id, err := cloudflareResolveZoneID(ctx, token, a.Zone)
	if err != nil {
		return Result{}, err
	}
	if a.Name == "" || a.Type == "" || a.Content == "" {
		return Result{}, fmt.Errorf("name, type, and content are required for dns_create")
	}
	payload := map[string]any{
		"name":    a.Name,
		"type":    strings.ToUpper(a.Type),
		"content": a.Content,
	}
	if a.TTL > 0 {
		payload["ttl"] = a.TTL
	} else {
		payload["ttl"] = 1
	}
	payload["proxied"] = a.Proxied
	if a.Priority > 0 {
		payload["priority"] = a.Priority
	}
	raw, _ := json.Marshal(payload)
	body, err := cloudflareCall(ctx, token, "POST", "/zones/"+id+"/dns_records", raw)
	if err != nil {
		return Result{}, err
	}
	return Result{Content: body, Preview: preview(body)}, nil
}

// cloudflareDNSUpdate updates an existing DNS record by id.
func cloudflareDNSUpdate(ctx context.Context, token string, a cloudflareArgs) (Result, error) {
	id, err := cloudflareResolveZoneID(ctx, token, a.Zone)
	if err != nil {
		return Result{}, err
	}
	if a.ID == "" {
		return Result{}, fmt.Errorf("record id is required for dns_update")
	}
	payload := map[string]any{}
	if a.Name != "" {
		payload["name"] = a.Name
	}
	if a.Type != "" {
		payload["type"] = strings.ToUpper(a.Type)
	}
	if a.Content != "" {
		payload["content"] = a.Content
	}
	if a.TTL > 0 {
		payload["ttl"] = a.TTL
	}
	payload["proxied"] = a.Proxied
	if a.Priority > 0 {
		payload["priority"] = a.Priority
	}
	raw, _ := json.Marshal(payload)
	body, err := cloudflareCall(ctx, token, "PUT", "/zones/"+id+"/dns_records/"+a.ID, raw)
	if err != nil {
		return Result{}, err
	}
	return Result{Content: body, Preview: preview(body)}, nil
}

// cloudflareDNSDelete deletes a DNS record by id.
func cloudflareDNSDelete(ctx context.Context, token, zone, recID string) (Result, error) {
	id, err := cloudflareResolveZoneID(ctx, token, zone)
	if err != nil {
		return Result{}, err
	}
	if recID == "" {
		return Result{}, fmt.Errorf("record id is required for dns_delete")
	}
	body, err := cloudflareCall(ctx, token, "DELETE", "/zones/"+id+"/dns_records/"+recID, nil)
	if err != nil {
		return Result{}, err
	}
	return Result{Content: body, Preview: preview(body)}, nil
}

// cloudflareCall performs an authenticated request and returns the body.
// On a non-2xx it extracts Cloudflare's error object for a clean message.
func cloudflareCall(ctx context.Context, token, method, endpoint string, body []byte) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, cloudflareBaseURL+endpoint, reader)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	hc := &http.Client{Timeout: 30 * time.Second}
	resp, err := hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("cloudflare request failed: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if err != nil {
		return "", fmt.Errorf("read cloudflare response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var apiErr cloudflareAPIError
		if json.Unmarshal(data, &apiErr) == nil && len(apiErr.Errors) > 0 {
			return "", fmt.Errorf("cloudflare API error (%d): %s", resp.StatusCode, apiErr.String())
		}
		return "", fmt.Errorf("cloudflare API error (%d): %s", resp.StatusCode, truncateOutput(string(data)))
	}
	return string(data), nil
}

func isHex(s string) bool {
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F') {
			return false
		}
	}
	return len(s) > 0
}
