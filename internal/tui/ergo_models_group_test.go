package tui

import (
	"reflect"
	"testing"

	"github.com/tjcoder-labs/cli/internal/client"
)

func TestGroupErgoModels(t *testing.T) {
	in := []client.ModelInfo{
		{Name: "xai/grok-4.3"}, {Name: "ergo/genius"}, {Name: "zeta/m"},
		{Name: "ollama/qwen3:8b"}, {Name: "ergo/flash"}, {Name: "ergo/pro"}, {Name: "google/gemini-3"},
	}
	var names, groups []string
	for _, g := range groupErgoModels(in) {
		groups = append(groups, g.Name)
		for _, m := range g.Models {
			names = append(names, m.Name)
		}
	}
	if !reflect.DeepEqual(groups, []string{"ergo", "xai", "ollama", "google", "zeta"}) {
		t.Fatalf("groups %v", groups)
	}
	if !reflect.DeepEqual(names[:3], []string{"ergo/flash", "ergo/pro", "ergo/genius"}) {
		t.Fatalf("tiers first in order: %v", names)
	}
}
