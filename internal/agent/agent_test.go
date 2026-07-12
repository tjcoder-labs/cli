package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWorkspaceAgentPersistence(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "ergo-agent-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfgs := []Config{
		{
			Name:         "custom-agent",
			DisplayName:  "Custom Agent",
			Title:        "Custom Title",
			DefaultModel: "test-model",
			ToolNames:    []string{"read_file", "run_command"},
			Prompt:       "You are a test agent.",
		},
	}
	if err := SaveWorkspaceAgents(tmpDir, cfgs); err != nil {
		t.Fatalf("failed to save workspace agents: %v", err)
	}

	loaded, err := LoadWorkspaceAgents(tmpDir)
	if err != nil {
		t.Fatalf("failed to load workspace agents: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 loaded agent, got %d", len(loaded))
	}
	if loaded[0].Name != "custom-agent" {
		t.Fatalf("expected saved agent name 'custom-agent', got %q", loaded[0].Name)
	}
}

func TestWorkspaceAgentsPath(t *testing.T) {
	path := WorkspaceAgentsPath("/tmp/workspace")
	expected := filepath.Join("/tmp/workspace", ".ergo-cli-go", "agents.json")
	if path != expected {
		t.Fatalf("expected path %q, got %q", expected, path)
	}
}
