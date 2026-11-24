package tui

import (
	"context"
	"strings"
	"time"

	"github.com/tjcoder-labs/cli/internal/client"
)

const voiceSynthesisPrompt = `Rewrite the assistant response as a brief spoken update to a teammate in a meeting. Use at most two short sentences. Sound natural, direct, and useful; preserve the result, important decision, or needed action. Never mention that you are rewriting, never use Markdown, code, headings, or bullet points. If details remain in the written response, naturally say that more context is on screen. Do not invent information.`

// synthesizeVoice asks the configured fast model for a true spoken
// summary. The transcript is untouched. Failures return an error so the
// caller can fall back to the existing local shortener.
func (a *App) synthesizeVoice(ctx context.Context, raw string) (string, error) {
	if a.runner == nil || a.runner.Provider == nil {
		return "", context.Canceled
	}
	model := a.voiceModel()
	if model == "" || model == "off" {
		return "", context.Canceled
	}
	msg, err := a.runner.Provider.Chat(ctx, client.ChatRequest{
		Model: model,
		Messages: []client.Message{
			{Role: "system", Content: voiceSynthesisPrompt},
			{Role: "user", Content: raw},
		},
		Stream: false,
	}, nil)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(msg.Content), nil
}

// voiceModel prefers an explicitly configured model. Otherwise it uses a
// cheap tier available from the current provider, avoiding an unknown
// provider-specific tag by falling back to the active model.
func (a *App) voiceModel() string {
	if m := strings.TrimSpace(a.config.VoiceModel); m != "" {
		return m
	}
	if a.isErgoProvider() {
		return "ergo-flash"
	}
	return a.currentModel
}

func (a *App) speakSynthesized(raw string) {
	// Short replies need no extra network round trip.
	if len(raw) < 260 {
		a.speak(a.optimizeForVoice(raw))
		return
	}
	model := a.voiceModel()
	if model == "off" || a.runner == nil || a.runner.Provider == nil {
		a.speak(a.optimizeForVoice(raw))
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	spoken, err := a.synthesizeVoice(ctx, raw)
	if err != nil || strings.TrimSpace(spoken) == "" {
		a.appendActivity("Voice synthesis unavailable; using local summary")
		spoken = a.optimizeForVoice(raw)
	}
	a.speak(spoken)
}
