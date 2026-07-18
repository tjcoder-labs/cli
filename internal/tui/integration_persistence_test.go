package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rivo/tview"
	"github.com/tjcoder-labs/cli/internal/client"
	"github.com/tjcoder-labs/cli/internal/session"
)

// TestMessagePersistenceToJSON verifies that messages are actually written
// to the session.json file on disk with correct structure.
func TestMessagePersistenceToJSON(t *testing.T) {
	tmpDir := t.TempDir()

	// Step 1: Create app and send message
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
		assistantState: "thinking",
	}

	// Add message to history (source of truth for persistence)
	app.history = append(app.history, client.Message{Role: "user", Content: "Hello"})

	// Render message like submit() does
	app.appendUserMessage("Hello")
	app.appendAssistantTurnLabel()

	// Save the session
	if err := app.saveSession(); err != nil {
		t.Fatalf("saveSession failed: %v", err)
	}

	// Wait for any background goroutines
	time.Sleep(200 * time.Millisecond)

	// Step 2: Read the raw JSON file and parse it
	sessionPath := filepath.Join(tmpDir, session.DirName, session.FileName)
	data, err := os.ReadFile(sessionPath)
	if err != nil {
		t.Fatalf("failed to read session.json: %v", err)
	}

	t.Logf("Session file contents:\n%s\n", string(data))

	// Parse the JSON
	var rawState map[string]interface{}
	if err := json.Unmarshal(data, &rawState); err != nil {
		t.Fatalf("failed to unmarshal session.json: %v", err)
	}

	// Check transcript array
	transcriptRaw, ok := rawState["transcript"]
	if !ok {
		t.Fatalf("no 'transcript' field in session.json. Keys: %v", keys(rawState))
	}

	transcriptArray, ok := transcriptRaw.([]interface{})
	if !ok {
		t.Fatalf("transcript is not an array: %T", transcriptRaw)
	}

	t.Logf("Transcript entries in JSON: %d", len(transcriptArray))

	// Verify "Hello" is in the transcript
	found := false
	for i, entry := range transcriptArray {
		entryMap, ok := entry.(map[string]interface{})
		if !ok {
			t.Logf("Entry %d is not a map: %T", i, entry)
			continue
		}

		role, _ := entryMap["role"].(string)
		content, _ := entryMap["content"].(string)

		t.Logf("Entry %d: role=%q, content=%q", i, role, content)

		if role == "user" && strings.Contains(content, "Hello") {
			found = true
		}
	}

	if !found {
		t.Fatalf("user message 'Hello' not found in session.json transcript")
	}

	// Step 3: Simulate app restart and reload
	time.Sleep(100 * time.Millisecond)

	// Load fresh and verify
	loaded, exists, err := session.Load(tmpDir)
	if err != nil || !exists {
		t.Fatalf("failed to load session on reload: %v", err)
	}

	if len(loaded.Transcript) == 0 {
		t.Fatalf("transcript is empty after reload")
	}

	found = false
	for _, entry := range loaded.Transcript {
		if entry.Role == "user" && strings.Contains(entry.Content, "Hello") {
			found = true
			break
		}
	}

	if !found {
		t.Fatalf("user message 'Hello' not found after reload. Entries: %+v", loaded.Transcript)
	}

	t.Logf("✓ Message persisted successfully across app restart")
}

// Helper to get keys from a map for debugging
func keys(m map[string]interface{}) []string {
	var k []string
	for key := range m {
		k = append(k, key)
	}
	return k
}
