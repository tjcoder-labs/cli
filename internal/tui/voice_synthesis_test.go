package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/tjcoder-labs/cli/internal/client"
	"github.com/tjcoder-labs/cli/internal/tooling"
)

type voiceTestProvider struct {
	request client.ChatRequest
}

func (p *voiceTestProvider) Chat(_ context.Context, req client.ChatRequest, _ func(client.StreamEvent) error) (client.Message, error) {
	p.request = req
	return client.Message{Role: "assistant", Content: "Build passed. More detail is on screen."}, nil
}
func (*voiceTestProvider) ListModels(context.Context) ([]client.ModelInfo, error) { return nil, nil }
func (*voiceTestProvider) ContextWindow(context.Context, string) (int, error)     { return 0, nil }
func (*voiceTestProvider) BaseURL() string                                        { return "https://ollama.test" }

func TestVoiceSynthesisUsesIndependentModel(t *testing.T) {
	p := &voiceTestProvider{}
	a := &App{
		runner:       &tooling.Runner{Provider: p},
		currentModel: "main-model",
		config:       AppConfig{VoiceModel: "fast-model"},
	}
	got, err := a.synthesizeVoice(context.Background(), "The tests passed after a detailed fix.")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "on screen") {
		t.Fatalf("spoken reply lacks screen cue: %q", got)
	}
	if p.request.Model != "fast-model" || p.request.Stream {
		t.Fatalf("unexpected request: %+v", p.request)
	}
	if !strings.Contains(p.request.Messages[0].Content, "teammate") {
		t.Fatal("missing spoken-style instruction")
	}
}

func TestVoiceModelDisabled(t *testing.T) {
	a := &App{config: AppConfig{VoiceModel: "off"}, runner: &tooling.Runner{Provider: &voiceTestProvider{}}}
	if _, err := a.synthesizeVoice(context.Background(), "text"); err == nil {
		t.Fatal("expected synthesis to be disabled")
	}
}
