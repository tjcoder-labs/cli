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

func TestUIControlWatchMarkerAndGating(t *testing.T) {
	ok := []struct{ raw, want string }{
		{`{"action":"show","panel":"canvas","command":"go test ./... -run 'A:B'"}`, "panel:canvas:watch:show:0:go test ./... -run 'A:B'"},
		{`{"action":"show","panel":"canvas","command":"git status","interval_seconds":2}`, "panel:canvas:watch:show:2:git status"},
		{`{"action":"stop","panel":"canvas"}`, "panel:canvas:watch:stop"},
		{`{"action":"show","panel":"watch"}`, "panel:canvas:watch:show"},
		{`{"action":"show","panel":"canvas","path":"a.go"}`, "panel:canvas:show:0:0:a.go"},
	}
	for _, c := range ok {
		res, err := uiControlTool{}.Execute(context.Background(), json.RawMessage(c.raw), ExecEnv{})
		if err != nil || res.Content != c.want || res.Preview != c.want {
			t.Errorf("%s: got %q, %v; want %q", c.raw, res.Content, err, c.want)
		}
	}
	for _, raw := range []string{
		`{"action":"show","panel":"canvas","command":"sudo tail -f /var/log/syslog"}`,
		`{"action":"show","panel":"canvas","command":"cd / && rm -rf /"}`,
		`{"action":"show","panel":"canvas","command":"date","interval_seconds":-1}`,
		`{"action":"explode","panel":"watch"}`,
	} {
		if _, err := (uiControlTool{}).Execute(context.Background(), json.RawMessage(raw), ExecEnv{}); err == nil {
			t.Errorf("%s: expected rejection", raw)
		}
	}
}
