package tools

import "testing"

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
