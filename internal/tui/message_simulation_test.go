package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rivo/tview"
	"github.com/tjcoder-labs/cli/internal/client"
	"github.com/tjcoder-labs/cli/internal/session"
)

// TestUserMessagePersistenceSimulation simulates the exact user flow:
// 1. Render a user message
// 2. Render the assistant label
// 3. Save the session
// 4. Verify the message is in the saved session file
func TestUserMessagePersistenceSimulation(t *testing.T) {
	tmpDir := t.TempDir()

	// Simulate user sends "Hello" and app immediately saves
	{
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

		// Simulate what submit() does:
		// 0. Add message to history (source of truth)
		app.history = append(app.history, client.Message{Role: "user", Content: "Hello"})
		
		// 1. Render user message
		app.appendUserMessage("Hello")
		app.appendAssistantTurnLabel()
		
		// 2. Save session (simulating the QueueUpdateDraw callback)
		if err := app.saveSession(); err != nil {
			t.Fatalf("save failed: %v", err)
		}

		// Verify file was created
		sessionPath := filepath.Join(tmpDir, session.DirName, session.FileName)
		if _, err := os.Stat(sessionPath); err != nil {
			t.Fatalf("session file not created: %v", err)
		}

		// Load the session file and verify the message is there
		loaded, exists, err := session.Load(tmpDir)
		if err != nil || !exists {
			t.Fatalf("failed to load session: %v", err)
		}

		// The key test: does the transcript contain our user message?
		if len(loaded.Transcript) == 0 {
			// Debug: print the raw transcript from the app
			rawTranscript := app.transcript.GetText(true)
			t.Logf("Raw transcript from app:\n%q\n", rawTranscript)
			t.Fatalf("transcript entries are empty after save")
		}

		// Check if any entry contains "Hello"
		found := false
		for _, entry := range loaded.Transcript {
			t.Logf("Loaded entry: role=%q, content=%q, timestamp=%q", entry.Role, entry.Content, entry.Timestamp)
			if entry.Role == "user" && strings.Contains(entry.Content, "Hello") {
				found = true
				break
			}
		}
		
		if !found {
			t.Logf("ERROR: user message 'Hello' not found in transcript entries")
			t.Logf("Loaded transcript entries: %+v", loaded.Transcript)
			// But don't fail yet - let's see what's actually being saved
		}
	}

	// Now reload and verify
	{
		time.Sleep(100 * time.Millisecond) // Ensure file is flushed
		
		// Just load the file, don't reconstruct the full app
		loaded2, exists, err := session.Load(tmpDir)
		if err != nil || !exists {
			t.Fatalf("failed to load session on second attempt: %v", err)
		}

		if len(loaded2.Transcript) == 0 {
			t.Fatalf("transcript entries are empty on reload")
		}

		found := false
		for _, entry := range loaded2.Transcript {
			if entry.Role == "user" && strings.Contains(entry.Content, "Hello") {
				found = true
				break
			}
		}
		
		if !found {
			t.Fatalf("user message 'Hello' not persisted across reload. Transcript: %+v", loaded2.Transcript)
		}
	}
}
