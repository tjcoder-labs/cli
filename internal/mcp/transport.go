package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// JSONRPCRequest represents a standard MCP JSON-RPC request.
type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// JSONRPCResponse represents a standard MCP JSON-RPC response.
type JSONRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error    *JSONRPCError   `json:"error,omitempty"`
}

type JSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type Transport struct {
	httpClient *http.Client
	endpoint   string
}

func NewTransport(endpoint string) *Transport {
	return &Transport{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		endpoint: endpoint,
	}
}

// SendRequest performs a synchronous JSON-RPC call over HTTP POST.
func (t *Transport) SendRequest(ctx context.Context, method string, params interface{}, responseTarget interface{}) error {
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
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("http request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server returned non-200 status: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	var jsonResp JSONRPCResponse
	if err := json.Unmarshal(body, &jsonResp); err != nil {
		return fmt.Errorf("failed to decode JSON-RPC response: %w", err)
	}

	if jsonResp.Error != nil {
		return fmt.Errorf("MCP error (%d): %s", jsonResp.Error.Code, jsonResp.Error.Message)
	}

	if err := json.Unmarshal(jsonResp.Result, responseTarget); err != nil {
		return fmt.Errorf("failed to decode result into target: %w", err)
	}

	return nil
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
