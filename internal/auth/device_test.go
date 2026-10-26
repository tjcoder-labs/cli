package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func noSleep(slept *[]time.Duration) func(context.Context, time.Duration) error {
	return func(ctx context.Context, d time.Duration) error {
		*slept = append(*slept, d)
		return ctx.Err()
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func startOK(w http.ResponseWriter) {
	writeJSON(w, 200, map[string]any{
		"device_code": "edc_abcdefghijklmnop", "user_code": "ABCD-2345",
		"verification_uri": "https://x/cli/auth/verify", "interval": 5, "expires_in": 900,
	})
}

func TestStartSuccessAndErrors(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   any
		hdr    string
		check  func(error) bool
	}{
		{"signup closed", 403, map[string]any{"error": map[string]any{"code": "signup_closed", "message": "closed"}}, "", func(e error) bool { return errors.Is(e, ErrSignupClosed) }},
		{"rate limited", 429, map[string]any{"error": map[string]any{"code": "rate_limited"}, "retryAfter": 42}, "", func(e error) bool {
			var r *RateLimitedError
			return errors.As(e, &r) && r.RetryAfter == 42*time.Second
		}},
		{"email failed", 502, map[string]any{"error": map[string]any{"code": "email_failed", "message": "Could not send the sign-in email."}}, "", func(e error) bool { return e != nil && e.Error() == "Could not send the sign-in email." }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { writeJSON(w, tc.status, tc.body) }))
			defer srv.Close()
			_, err := (&DeviceClient{BaseURL: srv.URL}).Start(context.Background(), "a@b.co")
			if !tc.check(err) {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in map[string]string
		_ = json.NewDecoder(r.Body).Decode(&in)
		if r.URL.Path != "/cli/auth/start" || in["email"] != "a@b.co" {
			t.Errorf("bad request %s %v", r.URL.Path, in)
		}
		startOK(w)
	}))
	defer srv.Close()
	s, err := (&DeviceClient{BaseURL: srv.URL}).Start(context.Background(), " a@b.co ")
	if err != nil || s.UserCode != "ABCD-2345" || s.Interval != 5 {
		t.Fatalf("start: %v %+v", err, s)
	}
}

func TestWaitPendingSlowDownThenSuccess(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch atomic.AddInt32(&n, 1) {
		case 1:
			writeJSON(w, 200, map[string]any{"status": "pending", "interval": 5})
		case 2:
			writeJSON(w, 200, map[string]any{"status": "slow_down", "interval": 10})
		default:
			writeJSON(w, 200, map[string]any{"status": "success", "api_key": "ek_ergo-cli_x", "email": "a@b.co", "balance_cents": 500, "welcome_granted": true})
		}
	}))
	defer srv.Close()
	var slept []time.Duration
	c := &DeviceClient{BaseURL: srv.URL, Sleep: noSleep(&slept)}
	res, err := c.Wait(context.Background(), &DeviceStart{DeviceCode: "edc_abcdefghijklmnop", Interval: 5, ExpiresIn: 900}, nil)
	if err != nil || res.APIKey != "ek_ergo-cli_x" || res.BalanceCents != 500 || !res.WelcomeGranted {
		t.Fatalf("wait: %v %+v", err, res)
	}
	want := []time.Duration{5 * time.Second, 5 * time.Second, 10 * time.Second}
	if len(slept) != 3 || slept[0] != want[0] || slept[1] != want[1] || slept[2] != want[2] {
		t.Fatalf("interval handling wrong: %v", slept)
	}
}

func TestWaitTerminalStatuses(t *testing.T) {
	for status, want := range map[string]error{"expired": ErrExpired, "denied": ErrDenied} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]any{"status": status}) }))
		var slept []time.Duration
		_, err := (&DeviceClient{BaseURL: srv.URL, Sleep: noSleep(&slept)}).Wait(context.Background(), &DeviceStart{DeviceCode: "x", Interval: 1, ExpiresIn: 60}, nil)
		srv.Close()
		if !errors.Is(err, want) {
			t.Fatalf("%s: got %v", status, err)
		}
	}
}

func TestWaitHonorsDeadlineAndContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]any{"status": "pending"}) }))
	defer srv.Close()
	clock := time.Unix(1000, 0)
	c := &DeviceClient{
		BaseURL: srv.URL,
		Now:     func() time.Time { return clock },
		Sleep:   func(ctx context.Context, d time.Duration) error { clock = clock.Add(d); return nil },
	}
	if _, err := c.Wait(context.Background(), &DeviceStart{DeviceCode: "x", Interval: 5, ExpiresIn: 20}, nil); !errors.Is(err, ErrExpired) {
		t.Fatalf("expected local expiry, got %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (&DeviceClient{BaseURL: srv.URL}).Wait(ctx, &DeviceStart{DeviceCode: "x", Interval: 1, ExpiresIn: 60}, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancel, got %v", err)
	}
}

func TestWaitToleratesTransientFailuresThenGivesUp(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&n, 1) <= 2 {
			w.WriteHeader(503)
			return
		}
		writeJSON(w, 200, map[string]any{"status": "success", "api_key": "k"})
	}))
	var slept []time.Duration
	res, err := (&DeviceClient{BaseURL: srv.URL, Sleep: noSleep(&slept)}).Wait(context.Background(), &DeviceStart{DeviceCode: "x", Interval: 2, ExpiresIn: 900}, nil)
	srv.Close()
	if err != nil || res.APIKey != "k" {
		t.Fatalf("expected recovery, got %v", err)
	}
	if slept[1] <= slept[0] {
		t.Fatalf("expected backoff growth: %v", slept)
	}

	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	defer down.Close()
	if _, err := (&DeviceClient{BaseURL: down.URL, Sleep: noSleep(&slept)}).Wait(context.Background(), &DeviceStart{DeviceCode: "x", Interval: 1, ExpiresIn: 9999}, nil); err == nil {
		t.Fatal("expected give-up error")
	}
}

func TestLogoutSendsBearerAndTreats401AsDone(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		writeJSON(w, 200, map[string]any{"revoked": true})
	}))
	if err := (&DeviceClient{BaseURL: srv.URL}).Logout(context.Background(), "ek_k"); err != nil || got != "Bearer ek_k" {
		t.Fatalf("logout: %v %q", err, got)
	}
	srv.Close()
	gone := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) }))
	defer gone.Close()
	if err := (&DeviceClient{BaseURL: gone.URL}).Logout(context.Background(), "ek_k"); err != nil {
		t.Fatalf("401 should count as done: %v", err)
	}
	if err := (&DeviceClient{BaseURL: "http://127.0.0.1:1"}).Logout(context.Background(), "ek_k"); err == nil {
		t.Fatal("network failure should surface")
	}
}
