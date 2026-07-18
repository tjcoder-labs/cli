package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tjcoder-labs/cli/internal/client"
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
	var state persistedSession
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
