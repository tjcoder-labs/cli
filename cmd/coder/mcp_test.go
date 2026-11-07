package main

import (
	"strings"
	"testing"
)

func TestMCPModeSelection(t *testing.T) {
	for _, tc := range []struct {
		name                                         string
		tunnel, stdio, inTTY, outTTY, local, wantErr bool
	}{
		{name: "terminal help", inTTY: true, outTTY: true, wantErr: true},
		{name: "public tunnel", tunnel: true, inTTY: true, outTTY: true},
		{name: "explicit stdio", stdio: true, inTTY: true, outTTY: true, local: true},
		{name: "piped stdin", outTTY: true, local: true},
		{name: "piped stdout", inTTY: true, local: true},
		{name: "tunnel pipe fails local", tunnel: true, local: true},
		{name: "contradictory modes", tunnel: true, stdio: true, inTTY: true, outTTY: true, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			local, err := mcpLocalMode(tc.tunnel, tc.stdio, tc.inTTY, tc.outTTY)
			if (err != nil) != tc.wantErr || local != tc.local {
				t.Fatalf("local=%v err=%v", local, err)
			}
		})
	}
	for _, text := range []string{"coder mcp tunnel", "--stdio", "/login", "sudo denied"} {
		if !strings.Contains(mcpUsage, text) {
			t.Errorf("usage missing %q", text)
		}
	}
}

func TestMCPRejectsUnknownSubcommands(t *testing.T) {
	if err := mcpCommand([]string{"publish"}); err == nil || !strings.Contains(err.Error(), "coder mcp tunnel") {
		t.Fatalf("err=%v", err)
	}
}
