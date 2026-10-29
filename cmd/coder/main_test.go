package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tjcoder-labs/cli/internal/client"
	"github.com/tjcoder-labs/cli/internal/providers"
	"github.com/tjcoder-labs/cli/internal/session"
)

func TestCmdAsk_SessionRetention(t *testing.T) {
	// 1. Setup a test workspace directory
	tmpDir, err := os.MkdirTemp("", "ergo-cli-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// 2. Setup a mock Ollama server
	var chatRequests []client.ChatRequest
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/chat" {
			var chatReq client.ChatRequest
			if err := json.NewDecoder(r.Body).Decode(&chatReq); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			chatRequests = append(chatRequests, chatReq)

			// Respond with a stream chunk
			w.Header().Set("Content-Type", "application/x-ndjson")
			w.WriteHeader(http.StatusOK)

			chunk := map[string]any{
				"message": map[string]any{
					"role":    "assistant",
					"content": "Hello, I am response for: " + chatReq.Messages[len(chatReq.Messages)-1].Content,
				},
				"done": true,
			}
			data, _ := json.Marshal(chunk)
			w.Write(data)
			w.Write([]byte("\n"))
			return
		}
		if r.URL.Path == "/api/tags" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"models":[]}`))
			return
		}
	}))
	defer ts.Close()

	// 3. First turn: user asks a question
	opts := askOptions{
		Host:          ts.URL,
		Provider:      "ollama",
		WorkspaceRoot: tmpDir,
		Model:         "test-model",
		Timeout:       5 * time.Second,
		Agent:         "software-engineer",
		Prompt:        "Turn 1 Prompt",
		Session:       true,
		ExplicitModel: true,
		ExplicitAgent: true,
	}

	cmdAsk(opts)

	// Check if session.json was created
	sessionPath := filepath.Join(tmpDir, ".ergo-cli-go", "session.json")
	if _, err := os.Stat(sessionPath); os.IsNotExist(err) {
		t.Fatalf("expected session file at %s to exist", sessionPath)
	}

	// Read session file and verify
	data, err := os.ReadFile(sessionPath)
	if err != nil {
		t.Fatalf("failed to read session: %v", err)
	}
	var state session.State
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("failed to unmarshal session: %v", err)
	}

	if state.CurrentAgent != "software-engineer" {
		t.Errorf("expected agent 'software-engineer', got %q", state.CurrentAgent)
	}
	if state.CurrentModel != "test-model" {
		t.Errorf("expected model 'test-model', got %q", state.CurrentModel)
	}
	if len(state.History) != 2 {
		t.Errorf("expected history length 2, got %d", len(state.History))
	}

	// 4. Second turn: should retain context (load history and append new prompt)
	opts2 := askOptions{
		Host:          ts.URL,
		Provider:      "ollama",
		WorkspaceRoot: tmpDir,
		Timeout:       5 * time.Second,
		Prompt:        "Turn 2 Prompt",
		Session:       true,
		// ExplicitAgent/ExplicitModel are false, so it should load from session
	}

	cmdAsk(opts2)

	// Verify second request payload sent to the mock Ollama server
	if len(chatRequests) != 2 {
		t.Fatalf("expected 2 chat requests, got %d", len(chatRequests))
	}

	// The second request's messages should contain:
	// 0: system prompt (inserted by runner)
	// 1: Turn 1 Prompt (user)
	// 2: Hello, I am response for: Turn 1 Prompt (assistant)
	// 3: Turn 2 Prompt (user)
	req2 := chatRequests[1]
	if len(req2.Messages) != 4 {
		t.Fatalf("expected 4 messages in second chat request, got %d", len(req2.Messages))
	}
	if req2.Messages[1].Content != "Turn 1 Prompt" {
		t.Errorf("expected message 1 content 'Turn 1 Prompt', got %q", req2.Messages[1].Content)
	}
	if req2.Messages[2].Content != "Hello, I am response for: Turn 1 Prompt" {
		t.Errorf("expected message 2 content 'Hello, I am response for: Turn 1 Prompt', got %q", req2.Messages[2].Content)
	}
	if req2.Messages[3].Content != "Turn 2 Prompt" {
		t.Errorf("expected message 3 content 'Turn 2 Prompt', got %q", req2.Messages[3].Content)
	}

	// Also verify that the agent and model were restored from the session since they were not explicitly overridden
	if req2.Model != "test-model" {
		t.Errorf("expected model restored from session to be 'test-model', got %q", req2.Model)
	}
}

func TestModelFitsProvider(t *testing.T) {
	avail := client.ErgoFallbackModels()
	if modelFitsProvider(true, "deepseek-v4-pro:cloud", avail) {
		t.Error("ollama tag must not fit the ergo gateway")
	}
	if !modelFitsProvider(true, "ergo-genius", avail) {
		t.Error("gateway agent must fit ergo")
	}
	if modelFitsProvider(false, "ergo-lite", avail) {
		t.Error("gateway agent must not be sent to ollama")
	}
	if !modelFitsProvider(false, "minimax-m3:cloud", avail) {
		t.Error("ollama tag must fit ollama")
	}
}

func TestResolveAvailableModel(t *testing.T) {
	available := []client.ModelInfo{{Name: "custom-a"}, {Name: "custom-b"}}
	for _, tt := range []struct {
		name      string
		preferred string
		fallback  string
		models    []client.ModelInfo
		want      string
	}{
		{"remembered model wins", "custom-b", "custom-a", available, "custom-b"},
		{"agent default is used when available", "stale", "custom-a", available, "custom-a"},
		{"first provider model is fallback", "stale", "also-stale", available, "custom-a"},
		{"empty catalog has no selection", "stale", "also-stale", nil, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveAvailableModel(tt.preferred, tt.fallback, tt.models); got != tt.want {
				t.Fatalf("resolveAvailableModel() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCmdAskRoutesToAvailableOpenAICompatibleModel(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	var requestedModel string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"served-model"}]}`))
		case "/v1/chat/completions":
			var request struct {
				Model string `json:"model"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			requestedModel = request.Model
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n"))
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	if err := providers.Save(providers.Registry{
		Active: "test-gateway",
		Providers: []providers.Config{{
			Name: "test-gateway", BaseURL: server.URL,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	cmdAsk(askOptions{
		Provider:      "test-gateway",
		WorkspaceRoot: workspace,
		Timeout:       5 * time.Second,
		Agent:         "software-engineer",
		Quiet:         true,
		Prompt:        "hello",
		Session:       false,
		ExplicitAgent: true,
	})
	if requestedModel != "served-model" {
		t.Fatalf("chat used model %q, want served-model", requestedModel)
	}
}

func TestErgoModelNotFoundMessage(t *testing.T) {
	e := &client.ErgoModelNotFoundError{Model: "foo:cloud", Available: []string{"ergo", "ergo-lite"}}
	m := ergoModelNotFoundMessage(e, nil, false)
	for _, want := range []string{`"foo:cloud"`, "ergo, ergo-lite", "default (ergo/pro)"} {
		if !strings.Contains(m, want) {
			t.Errorf("message missing %q: %s", want, m)
		}
	}
	if m := ergoModelNotFoundMessage(&client.ErgoModelNotFoundError{}, nil, true); !strings.Contains(m, "--model") || !strings.Contains(m, "ergo/genius") {
		t.Errorf("explicit/fallback message: %s", m)
	}
}
