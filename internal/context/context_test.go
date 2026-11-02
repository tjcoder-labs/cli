package context

import (
	"strings"
	"testing"
)

func TestGitTokenInterpolates(t *testing.T) {
	info := Info{Git: "feat/branch dirty"}
	if got := info.Render("- Git: {{git}}"); got != "- Git: feat/branch dirty" {
		t.Fatalf("git token not interpolated: %q", got)
	}
	// An empty Git still replaces the token (with ""), never leaving the
	// literal {{git}} in place.
	empty := Info{}
	if got := empty.Render("{{git}}"); got == "{{git}}" {
		t.Fatalf("token left literal with empty Git: %q", got)
	}
}

func TestDefaultTemplateHasGitLine(t *testing.T) {
	if !strings.Contains(DefaultEnvironmentTemplate, "- Git: {{git}}") {
		t.Fatalf("default template missing the Git line:\n%s", DefaultEnvironmentTemplate)
	}
}
