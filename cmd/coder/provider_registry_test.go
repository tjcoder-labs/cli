package main

import "testing"

func TestResolveProviderHonorsRegistryActive(t *testing.T) {
	cases := []struct {
		in   providerInputs
		want string
	}{
		{providerInputs{Registry: "my-vllm", ErgoReady: true}, "my-vllm"},
		{providerInputs{Flag: "ollama", Registry: "my-vllm"}, "ollama"},
		{providerInputs{Saved: "gemini", Registry: "my-vllm"}, "gemini"},
		// built-in ergo-gateway defers to the automatic policy
		{providerInputs{Registry: "ergo-gateway", CloudKey: true}, "ollama-cloud"},
		{providerInputs{Registry: "ergo-gateway", ErgoReady: true}, "ergo"},
		// a saved pre-rename slug must not be returned as an unknown provider
		{providerInputs{Saved: "ergo-gateway", ErgoReady: true}, "ergo"},
		{providerInputs{Flag: "ergo-gateway"}, "ergo"},
	}
	for _, c := range cases {
		if got := resolveProvider(c.in); got != c.want {
			t.Errorf("resolveProvider(%+v) = %q, want %q", c.in, got, c.want)
		}
	}
}
