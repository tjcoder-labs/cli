package providers

import "testing"

func TestResolveProtocol(t *testing.T) {
	for _, tt := range []struct {
		name     string
		cfg      Config
		want     string
	}{
		{"ollama name auto", Config{Name: "ollama"}, ProtocolOllama},
		{"ollama-cloud name auto", Config{Name: "ollama-cloud"}, ProtocolOllama},
		{"ollama_cloud alias auto", Config{Name: "ollama_cloud"}, ProtocolOllama},
		{"generic name auto", Config{Name: "my-gateway"}, ProtocolOpenAI},
		{"ergo name auto", Config{Name: "ergo-gateway"}, ProtocolOpenAI},
		{"explicit ollama overrides name", Config{Name: "my-gateway", Protocol: "ollama"}, ProtocolOllama},
		{"explicit openai overrides ollama name", Config{Name: "ollama", Protocol: "openai"}, ProtocolOpenAI},
		{"explicit ollama case-insensitive", Config{Name: "x", Protocol: "OLLAMA"}, ProtocolOllama},
		{"empty protocol auto", Config{Name: "ollama-cloud", Protocol: ""}, ProtocolOllama},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.ResolveProtocol(); got != tt.want {
				t.Fatalf("ResolveProtocol() = %q, want %q", got, tt.want)
			}
		})
	}
}
