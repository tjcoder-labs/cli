package main

import "testing"

func TestResolveProvider(t *testing.T) {
	up := func() bool { return true }
	down := func() bool { return false }
	cases := []struct {
		name string
		in   providerInputs
		want string
	}{
		{"fresh TUI, no creds -> ergo (login prompt)", providerInputs{Interactive: true, OllamaUp: up}, "ergo"},
		{"fresh TUI, logged in -> ergo", providerInputs{Interactive: true, ErgoReady: true}, "ergo"},
		{"headless, ergo ready -> ergo", providerInputs{ErgoReady: true, OllamaUp: up}, "ergo"},
		{"headless, no ergo, ollama up -> ollama", providerInputs{OllamaUp: up, CloudKey: true}, "ollama"},
		{"headless, no ergo, ollama down, cloud key -> ollama-cloud", providerInputs{OllamaUp: down, CloudKey: true}, "ollama-cloud"},
		{"headless, nothing -> ollama", providerInputs{OllamaUp: down}, "ollama"},
		{"explicit flag wins", providerInputs{Flag: "ollama", Interactive: true, ErgoReady: true}, "ollama"},
		{"saved choice wins over ergo", providerInputs{Saved: "ollama-cloud", Interactive: true, ErgoReady: true}, "ollama-cloud"},
		{"flag wins over saved", providerInputs{Flag: "gemini", Saved: "ollama"}, "gemini"},
	}
	for _, c := range cases {
		if got := resolveProvider(c.in); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}
