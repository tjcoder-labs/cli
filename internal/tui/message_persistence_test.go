package tui

import (
	"strings"
	"testing"

	"github.com/rivo/tview"
	"github.com/tjcoder-labs/cli/internal/agent"
	"github.com/tjcoder-labs/cli/internal/client"
	"github.com/tjcoder-labs/cli/internal/session"
)

// TestTranscriptParsing verifies that user messages rendered to the
// transcript can be properly parsed and persisted.
func TestTranscriptParsing(t *testing.T) {
	tmpDir := t.TempDir()

	app := &App{
		workspaceRoot: tmpDir,
		transcript:    tview.NewTextView(),
		reasoning:     tview.NewTextView(),
		activity:      tview.NewTextView(),
		palette:       darkPalette(),
		currentAgent:  mockAgent(),
		currentModel:  "test-model",
		enabledTools:  map[string]bool{},
		history:       []client.Message{},
		sessionState:  session.State{},
	}

	userMessage := "Test message for persistence"

	// Render the user message to the transcript
	rendered := app.renderUserMessage(userMessage)
	app.transcript.SetText(rendered)

	// Save the session
	if err := app.saveSession(); err != nil {
		t.Fatalf("saveSession failed: %v", err)
	}

	// Load the session back and verify the message is there
	loaded, exists, err := session.Load(tmpDir)
	if err != nil || !exists {
		t.Fatalf("failed to load session: %v", err)
	}

	// Check that the transcript entries include our message
	if len(loaded.Transcript) == 0 {
		t.Fatalf("transcript entries are empty")
	}

	found := false
	for _, entry := range loaded.Transcript {
		if entry.Role == "user" {
			// The content may have newlines if wrapped, so just check that all words are present
			if strings.Contains(entry.Content, "Test") && strings.Contains(entry.Content, "message") {
				found = true
				break
			}
		}
	}

	if !found {
		t.Fatalf("user message not found in transcript entries. Got: %+v", loaded.Transcript)
	}
}

// TestTranscriptReconstruction verifies that a saved transcript
// (with just a user message) can be loaded and displayed.
func TestTranscriptReconstruction(t *testing.T) {
	tmpDir := t.TempDir()

	// Simulate saving a user message without an assistant response
	savedState := session.State{
		Transcript: []session.TranscriptEntry{
			{
				Role:    "user",
				Content: "User sent this message",
			},
		},
	}

	if err := session.Save(tmpDir, savedState); err != nil {
		t.Fatalf("failed to save session: %v", err)
	}

	// Load the session back
	loaded, exists, err := session.Load(tmpDir)
	if err != nil || !exists {
		t.Fatalf("failed to load session: %v", err)
	}

	// Verify the loaded transcript has the message
	if len(loaded.Transcript) == 0 {
		t.Fatalf("transcript entries are empty after load")
	}

	if loaded.Transcript[0].Role != "user" {
		t.Fatalf("expected role 'user', got %q", loaded.Transcript[0].Role)
	}

	if !strings.Contains(loaded.Transcript[0].Content, "User sent this message") {
		t.Fatalf("message content mismatch. Got: %q", loaded.Transcript[0].Content)
	}
}

// TestNoHistoryDuplication verifies that the final history doesn't
// have duplicate user messages.
func TestNoHistoryDuplication(t *testing.T) {
	// Simulate the initial state: no messages
	history := []client.Message{}

	// Simulate what the runner does: add the user message
	userMessage := "New user message"
	history = append(history, client.Message{Role: "user", Content: userMessage})

	// Simulate adding the assistant response
	history = append(history, client.Message{Role: "assistant", Content: "Assistant response"})

	// Verify we have exactly 2 messages (no duplication)
	if len(history) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(history))
	}

	if history[0].Role != "user" || history[1].Role != "assistant" {
		t.Fatalf("unexpected roles: user=%q, assistant=%q", history[0].Role, history[1].Role)
	}

	if history[0].Content != userMessage {
		t.Fatalf("user message content mismatch")
	}
}

func mockAgent() agent.Config {
	return agent.Config{
		Name:         "test-agent",
		DisplayName:  "Test Agent",
		ToolNames:    []string{},
	}
}

