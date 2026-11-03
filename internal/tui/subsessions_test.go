package tui

import (
	"strings"
	"testing"

	"github.com/tjcoder-labs/cli/internal/client"
)

func TestSanitizeSessionName(t *testing.T) {
	cases := map[string]string{
		"reddit":      "reddit",
		"Reddit Feed": "reddit-feed",
		"@reddit":     "reddit",
		"  ":          "main",
		"a/b*c":       "a-b-c",
	}
	for in, want := range cases {
		if got := sanitizeSessionName(in); got != want {
			t.Errorf("sanitizeSessionName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSubsessionPath(t *testing.T) {
	a := &App{workspaceRoot: "/tmp/ws"}
	p := a.subsessionPath("Reddit Feed")
	if !strings.HasSuffix(p, "/.ergo-cli-go/subsessions/reddit-feed/session.json") {
		t.Fatalf("unexpected path: %s", p)
	}
}

func TestProviderCLIArg(t *testing.T) {
	a := &App{}
	a.provider = client.NewOllamaWithTimeout("http://localhost:11434", 0)
	if got := a.providerCLIArg(); got != "ollama" {
		t.Errorf("local ollama = %q, want ollama", got)
	}
	a.provider = client.NewOllamaCloud(client.OllamaCloudHost, "k", 0)
	if got := a.providerCLIArg(); got != "ollama-cloud" {
		t.Errorf("cloud ollama = %q, want ollama-cloud", got)
	}
	a.provider = client.NewGemini("k", 0)
	if got := a.providerCLIArg(); got != "gemini" {
		t.Errorf("gemini = %q, want gemini", got)
	}
}
