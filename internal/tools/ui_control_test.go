package tools

import (
	"context"
	"encoding/json"
	"testing"
)

func TestUIControlMarkerBasic(t *testing.T) {
	raw := json.RawMessage(`{"action":"show","panel":"tasks"}`)
	res, err := uiControlTool{}.Execute(context.Background(), raw, ExecEnv{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Content != "panel:tasks:show" {
		t.Fatalf("got marker %q, want panel:tasks:show", res.Content)
	}
}

func TestUIControlMarkerCanvasWithRange(t *testing.T) {
	raw := json.RawMessage(`{"action":"show","panel":"canvas","path":"internal/tui/app.go","start_line":12,"end_line":40}`)
	res, err := uiControlTool{}.Execute(context.Background(), raw, ExecEnv{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "panel:canvas:show:12:40:internal/tui/app.go"
	if res.Content != want {
		t.Fatalf("got marker %q, want %q", res.Content, want)
	}
}

func TestUIControlMarkerCanvasNoPathFallsBackToBase(t *testing.T) {
	raw := json.RawMessage(`{"action":"show","panel":"canvas"}`)
	res, err := uiControlTool{}.Execute(context.Background(), raw, ExecEnv{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Content != "panel:canvas:show" {
		t.Fatalf("got marker %q, want panel:canvas:show", res.Content)
	}
}

func TestUIControlRequiresActionAndPanel(t *testing.T) {
	raw := json.RawMessage(`{"action":"show"}`)
	if _, err := (uiControlTool{}).Execute(context.Background(), raw, ExecEnv{}); err == nil {
		t.Fatal("expected error when panel is missing")
	}
}
