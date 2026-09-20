package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tjcoder-labs/cli/internal/client"
)

// resendBaseURL is the Resend API root.
const resendBaseURL = "https://api.resend.com"

// resendTokenPath is where a user-provided API key is persisted
// across sessions (mode 0600, per-user). It is only written via the
// "auth" action and never logged.
func resendTokenPath() string {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "coder", "resend-token")
}

// resendResolveToken returns the bearer token from, in order:
//  1. the RESEND_API_KEY environment variable, then
//  2. the persisted token file (~/.local/share/coder/resend-token).
// An empty string means "not configured".
func resendResolveToken() string {
	if t := strings.TrimSpace(os.Getenv("RESEND_API_KEY")); t != "" {
		return t
	}
	data, err := os.ReadFile(resendTokenPath())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// resendAPIError is a best-effort extraction of Resend's error JSON.
type resendAPIError struct {
	StatusCode int    `json:"statusCode"`
	Message    string `json:"message"`
	Name       string `json:"name"`
}

func (e resendAPIError) String() string {
	if e.Message != "" {
		return fmt.Sprintf("%d: %s", e.StatusCode, e.Message)
	}
	return "unknown API error"
}

// resendArgs is the decoded argument set for the resend tool.
type resendArgs struct {
	Action     string `json:"action"`
	Token      string `json:"token"`
	From       string `json:"from"`
	To         string `json:"to"`
	Subject    string `json:"subject"`
	HTML       string `json:"html"`
	Text       string `json:"text"`
	CC         string `json:"cc"`
	BCC        string `json:"bcc"`
	ReplyTo    string `json:"reply_to"`
	ScheduledAt string `json:"scheduled_at"`
	ID         string `json:"id"`
	Limit      int    `json:"limit"`
	After      string `json:"after"`
}

// resendTool provides send/list/retrieve access to emails via the
// Resend REST API. Authentication uses a bearer API key.
type resendTool struct{}

func (resendTool) Definition() client.ToolDefinition {
	return client.ToolDefinition{
		Type: "function",
		Function: client.FunctionDefinition{
			Name: "resend",
			Description: `Send and receive emails via the Resend API.

AUTHENTICATION (required before any other action):
- API key is read from RESEND_API_KEY env, or set once with action=auth (persisted to ~/.local/share/coder/resend-token, mode 0600).
- To create a key: Resend Dashboard -> API Keys -> Create API Key -> copy the key (starts with re_). Pass it to action=auth (or export RESEND_API_KEY).

ACTIONS:
- auth(token): save the API key locally and verify it with a GET /emails?limit=1 call.
- send(from, to, subject, html?, text?, cc?, bcc?, reply_to?, scheduled_at?): send an email.
- list(limit?, after?): list sent emails (id, to, subject, created_at).
- get(id): retrieve a single email by id (includes html, text, subject, from, to).

SAFETY: list/get are safe. send sends a real email; confirm the recipient and content with the user before invoking. Never print the API key.`,
			Parameters: objectSchema([]string{"action"}, map[string]any{
				"action":      stringProp("auth | send | list | get"),
				"token":       stringProp("Resend API key (only for action=auth)."),
				"from":        stringProp("Sender email (e.g. Acme <onboarding@example.dev>)."),
				"to":          stringProp("Recipient email (comma-separated for multiple)."),
				"subject":     stringProp("Email subject."),
				"html":        stringProp("HTML body of the email."),
				"text":        stringProp("Plain text body of the email."),
				"cc":          stringProp("CC recipient(s), comma-separated."),
				"bcc":         stringProp("BCC recipient(s), comma-separated."),
				"reply_to":    stringProp("Reply-to email address."),
				"scheduled_at": stringProp("Schedule email (natural language or ISO 8601)."),
				"id":          stringProp("Email id (for get)."),
				"limit":       numberProp("Max results for list (1-100, default 20)."),
				"after":       stringProp("Pagination cursor (email id)."),
			}),
		},
	}
}

func (resendTool) Execute(ctx context.Context, raw json.RawMessage, env ExecEnv) (Result, error) {
	var a resendArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return Result{}, err
	}
	a.Action = strings.ToLower(strings.TrimSpace(a.Action))
	if a.Action == "" {
		return Result{}, fmt.Errorf("action is required (auth|send|list|get)")
	}

	if a.Action == "auth" {
		return resendHandleAuth(a.Token)
	}

	token := resendResolveToken()
	if token == "" {
		return Result{}, fmt.Errorf("no Resend API key configured; set RESEND_API_KEY or use action=auth with your key")
	}

	switch a.Action {
	case "send":
		return resendSend(ctx, token, a)
	case "list":
		return resendList(ctx, token, a)
	case "get":
		return resendGet(ctx, token, a.ID)
	default:
		return Result{}, fmt.Errorf("unknown action %q (auth|send|list|get)", a.Action)
	}
}

// resendHandleAuth persists the API key and verifies it.
func resendHandleAuth(token string) (Result, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return Result{}, fmt.Errorf("token is required for action=auth")
	}
	path := resendTokenPath()
	if path == "" {
		return Result{}, fmt.Errorf("could not resolve token path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return Result{}, fmt.Errorf("create token dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		return Result{}, fmt.Errorf("write token: %w", err)
	}
	// Verify immediately so the user knows the key is valid.
	_, err := resendCall(context.Background(), token, "GET", "/emails?limit=1", nil)
	if err != nil {
		return Result{}, fmt.Errorf("key saved but verification failed: %w", err)
	}
	out := fmt.Sprintf("Resend API key saved to %s and verified", path)
	return Result{Content: out, Preview: "key verified"}, nil
}

// resendSend sends an email.
func resendSend(ctx context.Context, token string, a resendArgs) (Result, error) {
	if a.From == "" || a.To == "" || a.Subject == "" {
		return Result{}, fmt.Errorf("from, to, and subject are required for send")
	}
	if a.HTML == "" && a.Text == "" {
		return Result{}, fmt.Errorf("either html or text is required for send")
	}

	toList := splitCommaList(a.To)
	payload := map[string]any{
		"from":    a.From,
		"to":      toList,
		"subject": a.Subject,
	}
	if a.HTML != "" {
		payload["html"] = a.HTML
	}
	if a.Text != "" {
		payload["text"] = a.Text
	}
	if a.CC != "" {
		payload["cc"] = splitCommaList(a.CC)
	}
	if a.BCC != "" {
		payload["bcc"] = splitCommaList(a.BCC)
	}
	if a.ReplyTo != "" {
		payload["reply_to"] = splitCommaList(a.ReplyTo)
	}
	if a.ScheduledAt != "" {
		payload["scheduled_at"] = a.ScheduledAt
	}

	raw, _ := json.Marshal(payload)
	body, err := resendCall(ctx, token, "POST", "/emails", raw)
	if err != nil {
		return Result{}, err
	}
	return Result{Content: body, Preview: preview(body)}, nil
}

// resendList lists sent emails.
func resendList(ctx context.Context, token string, a resendArgs) (Result, error) {
	endpoint := "/emails"
	params := []string{}
	if a.Limit > 0 {
		params = append(params, fmt.Sprintf("limit=%d", a.Limit))
	} else {
		params = append(params, "limit=20")
	}
	if a.After != "" {
		params = append(params, "after="+a.After)
	}
	if len(params) > 0 {
		endpoint += "?" + strings.Join(params, "&")
	}
	body, err := resendCall(ctx, token, "GET", endpoint, nil)
	if err != nil {
		return Result{}, err
	}
	return Result{Content: body, Preview: preview(body)}, nil
}

// resendGet retrieves a single email by id.
func resendGet(ctx context.Context, token, id string) (Result, error) {
	if id == "" {
		return Result{}, fmt.Errorf("email id is required for get")
	}
	body, err := resendCall(ctx, token, "GET", "/emails/"+id, nil)
	if err != nil {
		return Result{}, err
	}
	return Result{Content: body, Preview: preview(body)}, nil
}

// resendCall performs an authenticated request and returns the body.
// On a non-2xx it extracts Resend's error object for a clean message.
func resendCall(ctx context.Context, token, method, endpoint string, body []byte) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, resendBaseURL+endpoint, reader)
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
		return "", fmt.Errorf("resend request failed: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if err != nil {
		return "", fmt.Errorf("read resend response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var apiErr resendAPIError
		if json.Unmarshal(data, &apiErr) == nil && apiErr.Message != "" {
			return "", fmt.Errorf("resend API error (%d): %s", resp.StatusCode, apiErr.String())
		}
		return "", fmt.Errorf("resend API error (%d): %s", resp.StatusCode, truncateOutput(string(data)))
	}
	return string(data), nil
}

// splitCommaList splits a comma-separated string into a slice of
// trimmed strings, dropping empties.
func splitCommaList(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}