package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestBrowserTransportBackwardsCompatibility(t *testing.T) {
	for _, v := range []string{"", "cdp"} {
		got, e := browserTransport(v)
		if e != nil || got != "raw_cdp" {
			t.Fatalf("%q changed old CDP default: %q %v", v, got, e)
		}
	}
	if got, e := browserTransport("stealth"); e != nil || got != "stealth" {
		t.Fatal(got, e)
	}
	if _, e := browserTransport("bogus"); e == nil {
		t.Fatal("invalid transport accepted")
	}
}

func TestPrivateTabNeverSilentlyUsesNormalCDP(t *testing.T) {
	for _, raw := range []string{`{"action":"new_tab","incognito":true}`, `{"action":"new_tab","incognito":true,"transport":"cdp"}`} {
		_, e := (browserBridgeTool{}).Execute(context.Background(), json.RawMessage(raw), ExecEnv{})
		if e == nil || !strings.Contains(e.Error(), "requires transport=stealth") {
			t.Fatalf("private URL did not fail closed: %v", e)
		}
	}
}
