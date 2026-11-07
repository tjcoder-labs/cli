package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// protocolVersion is the MCP protocol revision this client
// advertises during the initialize handshake. Tavily's MCP server
// (and any spec-compliant server) echoes this back in
// initialize.result.protocolVersion.
const protocolVersion = "2024-11-05"

// clientVersion uses the running CLI version supplied by its entry point.
func clientVersion(versions []string) string {
	if len(versions) > 0 && versions[0] != "" {
		return versions[0]
	}
	return "dev"
}

func initializeParams(version string) map[string]any {
	return map[string]any{"protocolVersion": protocolVersion, "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "tjcoder-cli", "version": version}}
}

// JSONRPCRequest represents a standard MCP JSON-RPC request.
type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// JSONRPCResponse represents a standard MCP JSON-RPC response.
type JSONRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *JSONRPCError   `json:"error,omitempty"`
}

type JSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *JSONRPCError) Error() string { return fmt.Sprintf("MCP error (%d): %s", e.Code, e.Message) }

// Transport wraps a single HTTP endpoint speaking the MCP
// Streamable HTTP transport (a.k.a. MCP-over-SSE). Responses are
// framed as `event: message\r\ndata: <json>\r\n\r\n` even for
// short-lived, non-streaming requests — see the MCP spec for 2024-11-05.
type Transport struct {
	httpClient *http.Client
	endpoint   string
	version    string

	// initOnce guards the initialize/initialized handshake so it
	// only runs once per transport lifetime. Servers may reject
	// subsequent calls without a successful handshake.
	initOnce sync.Once
	initErr  error
}

// NewTransport builds a Transport pointed at endpoint. The handshake
// is deferred until the first SendRequest call so callers don't pay
// for a round-trip they may not need (e.g. on construction-only
// scenarios).
func NewTransport(endpoint string, versions ...string) *Transport {
	return &Transport{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		endpoint: endpoint,
		version:  clientVersion(versions),
	}
}

// initialize performs the MCP handshake:
//  1. POST {"method": "initialize", ...} → server returns capabilities
//  2. POST {"method": "notifications/initialized"} → ack
//
// We do (2) without expecting a body in the response. Per the spec,
// notifications have no id and receive either 202 Accepted or 200 OK
// with an empty body.
//
// The handshake uses sendRaw directly so sync.Once is never re-entered.
func (t *Transport) initialize(ctx context.Context) error {
	t.initOnce.Do(func() {
		initParams := initializeParams(t.version)
		var initResult json.RawMessage
		if err := t.sendRaw(ctx, "initialize", initParams, true, &initResult); err != nil {
			t.initErr = fmt.Errorf("initialize handshake failed: %w", err)
			return
		}
		if err := t.sendNotification(ctx, "notifications/initialized", nil); err != nil {
			t.initErr = fmt.Errorf("initialized notification failed: %w", err)
			return
		}
	})
	return t.initErr
}

// sendNotification posts a JSON-RPC notification without an id.
// We don't decode a response — the server
// replies with 202 Accepted and an empty body, which the SSE parser
// would otherwise fail on.
func (t *Transport) sendNotification(ctx context.Context, method string, params interface{}) error {
	payload, err := json.Marshal(JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      nil,
		Method:  method,
		Params:  encodeParams(params),
	})
	if err != nil {
		return fmt.Errorf("failed to marshal notification: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, "POST", t.endpoint, bytes.NewBuffer(payload))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	t.setCommonHeaders(req)
	resp, err := t.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("http request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("notification %q rejected: status %d, body %s", method, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

// setCommonHeaders applies the headers every MCP request needs:
//   - Content-Type: application/json (we send JSON-RPC bodies)
//   - Accept: application/json, text/event-stream (we'll take either
//     framing — the spec says servers may respond with either)
//   - MCP-Protocol-Version: pin to the revision we initialized with
//   - User-Agent: identify ourselves in server logs
func (t *Transport) setCommonHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", protocolVersion)
	req.Header.Set("User-Agent", "tjcoder-cli/"+t.version+" (+https://tjcoder.com)")
}

// SendRequest performs a synchronous JSON-RPC call. If this is the
// first call on the transport it also runs the initialize handshake.
// Responses are parsed as SSE (`event: message\ndata: <json>`); if
// the server returns plain JSON instead, the body is unmarshaled
// directly as a fallback.
func (t *Transport) SendRequest(ctx context.Context, method string, params interface{}, responseTarget interface{}) error {
	if err := t.initialize(ctx); err != nil {
		return err
	}
	return t.sendRaw(ctx, method, params, true, responseTarget)
}

// sendRaw is the underlying HTTP/JSON-RPC transport: marshal the
// request, POST it, parse the response (SSE or JSON), and unmarshal
// the result. It does not run the initialize handshake — callers
// that want a handshake should use SendRequest.
//
// The responseExpected flag is true for normal request/response
// methods (where we expect a JSON-RPC envelope back) and false for
// notifications (where the server replies 202 with no body).
func (t *Transport) sendRaw(ctx context.Context, method string, params interface{}, responseExpected bool, responseTarget interface{}) error {
	payload, err := json.Marshal(JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      time.Now().UnixNano(),
		Method:  method,
		Params:  encodeParams(params),
	})
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", t.endpoint, bytes.NewBuffer(payload))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	t.setCommonHeaders(req)

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("http request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("server returned 404: not an MCP endpoint? (check the URL)")
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("server returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	if !responseExpected {
		// Notifications receive 202 with no body — drain and discard
		// so the connection can be reused.
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}

	// Decode the response. Prefer SSE framing; fall back to a raw
	// JSON body for servers (or test fixtures) that skip SSE.
	jsonResp, err := decodeResponse(resp)
	if err != nil {
		return err
	}

	if jsonResp.Error != nil {
		return jsonResp.Error
	}

	if err := json.Unmarshal(jsonResp.Result, responseTarget); err != nil {
		return fmt.Errorf("failed to decode result into target: %w", err)
	}

	return nil
}

// decodeResponse reads resp.Body and returns a JSONRPCResponse,
// handling both `text/event-stream` (the MCP Streamable HTTP
// transport) and plain `application/json` (some servers / test
// fixtures skip the SSE framing).
func decodeResponse(resp *http.Response) (*JSONRPCResponse, error) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	ct := resp.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "text/event-stream") {
		data, err := extractSSEData(body)
		if err != nil {
			return nil, err
		}
		body = data
	}

	var jsonResp JSONRPCResponse
	if err := json.Unmarshal(body, &jsonResp); err != nil {
		return nil, fmt.Errorf("failed to decode JSON-RPC response: %w (body=%s)", err, strings.TrimSpace(string(body)))
	}
	return &jsonResp, nil
}

// extractSSEData scans a `text/event-stream` body for `data:` lines
// and returns the concatenation of every data payload. The MCP
// Streamable HTTP transport emits exactly one `event: message` frame
// per request, but we tolerate multiple to be defensive against
// keep-alive pings interleaved with the response.
func extractSSEData(body []byte) ([]byte, error) {
	var collected []byte
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "data:"):
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if len(collected) > 0 {
				collected = append(collected, '\n')
			}
			collected = append(collected, payload...)
		case line == "":
			// blank line terminates an SSE event; if we already
			// have data, we're done.
			if len(collected) > 0 {
				return collected, nil
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to scan SSE body: %w", err)
	}
	if len(collected) == 0 {
		return nil, fmt.Errorf("no data: lines in SSE response")
	}
	return collected, nil
}

func encodeParams(params interface{}) json.RawMessage {
	if p, ok := params.(json.RawMessage); ok {
		return p
	}
	b, err := json.Marshal(params)
	if err != nil {
		return nil
	}
	return b
}

// Close releases pooled HTTP connections.
func (t *Transport) Close() error { t.httpClient.CloseIdleConnections(); return nil }

// Err reports terminal transport errors (HTTP errors are tracked per call).
func (t *Transport) Err() error { return nil }
