package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Device-code login against the ergo gateway:
//
//	POST /cli/auth/start  {email}        → device_code, user_code, verification_uri, interval, expires_in
//	POST /cli/auth/poll   {device_code}  → {status: pending|slow_down|expired|denied|success, …}
//	POST /cli/auth/logout (Bearer key)   → revokes the key server-side
//
// The user approves in a browser (emailed link + the code shown in the
// terminal); the CLI only ever polls. The key is returned exactly once.

// Sentinel errors a UI can branch on.
var (
	ErrSignupClosed = errors.New("sign-ups are closed for this email address")
	ErrExpired      = errors.New("the sign-in request expired — run /login again")
	ErrDenied       = errors.New("the sign-in request was denied")
)

// RateLimitedError carries the server's Retry-After hint (0 if unknown).
type RateLimitedError struct{ RetryAfter time.Duration }

func (e *RateLimitedError) Error() string {
	if e.RetryAfter > 0 {
		return fmt.Sprintf("too many sign-in attempts — try again in %s", e.RetryAfter.Round(time.Second))
	}
	return "too many sign-in attempts — try again later"
}

// DeviceStart is the response of /cli/auth/start.
type DeviceStart struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	Interval                int    `json:"interval"`
	ExpiresIn               int    `json:"expires_in"`
	Message                 string `json:"message"`
}

// DeviceResult is returned by a successful login. APIKey must never be
// logged or printed.
type DeviceResult struct {
	APIKey         string `json:"api_key"`
	Email          string `json:"email"`
	Name           string `json:"name,omitempty"`
	ExpiresAt      string `json:"expires_at"`
	BalanceCents   int64  `json:"balance_cents"`
	WelcomeGranted bool   `json:"welcome_granted"`
}

type pollResponse struct {
	Status   string `json:"status"`
	Interval int    `json:"interval"`
	DeviceResult
}

// DeviceClient talks to the gateway's device-auth endpoints. Zero values
// for HTTP/Sleep/Now are filled with sensible defaults.
type DeviceClient struct {
	BaseURL    string
	HTTP       *http.Client
	ClientName string
	// Sleep waits d or returns ctx.Err(); replaced in tests to run instantly.
	Sleep func(ctx context.Context, d time.Duration) error
	// Now is replaced in tests to control the expiry deadline.
	Now func() time.Time
}

func (c *DeviceClient) httpc() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 15 * time.Second}
}

func (c *DeviceClient) sleep(ctx context.Context, d time.Duration) error {
	if c.Sleep != nil {
		return c.Sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (c *DeviceClient) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

type serverError struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	RetryAfter int `json:"retryAfter"`
}

func (c *DeviceClient) post(ctx context.Context, path string, body any, bearer string) (*http.Response, []byte, error) {
	buf, err := json.Marshal(body)
	if err != nil {
		return nil, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.BaseURL, "/")+path, bytes.NewReader(buf))
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := c.httpc().Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp, raw, nil
}

// Start requests a device code and makes the server email the approval link.
func (c *DeviceClient) Start(ctx context.Context, email string) (*DeviceStart, error) {
	name := c.ClientName
	if name == "" {
		name = "ergo-cli"
	}
	resp, raw, err := c.post(ctx, "/cli/auth/start", map[string]string{"email": strings.TrimSpace(email), "client_name": name}, "")
	if err != nil {
		return nil, fmt.Errorf("could not reach the ergo gateway: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, startError(resp, raw)
	}
	var out DeviceStart
	if err := json.Unmarshal(raw, &out); err != nil || out.DeviceCode == "" || out.UserCode == "" {
		return nil, fmt.Errorf("unexpected response from the ergo gateway")
	}
	if out.Interval <= 0 {
		out.Interval = 5
	}
	if out.ExpiresIn <= 0 {
		out.ExpiresIn = 900
	}
	return &out, nil
}

func startError(resp *http.Response, raw []byte) error {
	var se serverError
	_ = json.Unmarshal(raw, &se)
	switch {
	case resp.StatusCode == http.StatusForbidden && se.Error.Code == "signup_closed":
		if se.Error.Message != "" {
			return fmt.Errorf("%w: %s", ErrSignupClosed, se.Error.Message)
		}
		return ErrSignupClosed
	case resp.StatusCode == http.StatusTooManyRequests:
		ra := time.Duration(se.RetryAfter) * time.Second
		if h, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && ra == 0 {
			ra = time.Duration(h) * time.Second
		}
		return &RateLimitedError{RetryAfter: ra}
	case se.Error.Message != "":
		return errors.New(se.Error.Message)
	}
	return fmt.Errorf("sign-in failed: %s", resp.Status)
}

// pollOnce performs one /poll call. A nil result with nil error never happens.
func (c *DeviceClient) pollOnce(ctx context.Context, deviceCode string) (*pollResponse, error) {
	resp, raw, err := c.post(ctx, "/cli/auth/poll", map[string]string{"device_code": deviceCode}, "")
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return &pollResponse{Status: "slow_down"}, nil
	}
	if resp.StatusCode >= 500 {
		return nil, fmt.Errorf("gateway error: %s", resp.Status)
	}
	if resp.StatusCode != http.StatusOK {
		var se serverError
		_ = json.Unmarshal(raw, &se)
		if se.Error.Message != "" {
			return nil, &fatalPollError{errors.New(se.Error.Message)}
		}
		return nil, &fatalPollError{fmt.Errorf("sign-in failed: %s", resp.Status)}
	}
	var pr pollResponse
	if err := json.Unmarshal(raw, &pr); err != nil {
		return nil, fmt.Errorf("unexpected poll response")
	}
	return &pr, nil
}

type fatalPollError struct{ error }

// maxTransientPollFailures is how many consecutive network/5xx failures
// Wait tolerates before giving up.
const maxTransientPollFailures = 6

// Wait polls until the user approves, honoring the server interval,
// slow_down (+5s if no interval is supplied), transient-failure backoff, and
// the expires_in deadline. onStatus (optional) is called after every poll with
// "pending" or "slow_down" so a UI can show liveness.
func (c *DeviceClient) Wait(ctx context.Context, start *DeviceStart, onStatus func(status string)) (*DeviceResult, error) {
	interval := time.Duration(start.Interval) * time.Second
	if interval < time.Second {
		interval = time.Second
	}
	deadline := c.now().Add(time.Duration(start.ExpiresIn) * time.Second)
	failures := 0
	for {
		if err := c.sleep(ctx, interval); err != nil {
			return nil, err
		}
		if !c.now().Before(deadline) {
			return nil, ErrExpired
		}
		pr, err := c.pollOnce(ctx, start.DeviceCode)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			var fatal *fatalPollError
			if errors.As(err, &fatal) {
				return nil, fatal.error
			}
			failures++
			if failures >= maxTransientPollFailures {
				return nil, fmt.Errorf("lost contact with the ergo gateway: %w", err)
			}
			// exponential backoff on top of the interval, capped at 30s
			interval = minDuration(interval*2, 30*time.Second)
			continue
		}
		failures = 0
		switch pr.Status {
		case "success":
			if pr.APIKey == "" {
				return nil, fmt.Errorf("gateway returned success without a key")
			}
			res := pr.DeviceResult
			return &res, nil
		case "pending":
			if pr.Interval > 0 {
				interval = time.Duration(pr.Interval) * time.Second
			}
		case "slow_down":
			if pr.Interval > 0 {
				interval = time.Duration(pr.Interval) * time.Second
			} else {
				interval += 5 * time.Second
			}
		case "expired":
			return nil, ErrExpired
		case "denied":
			return nil, ErrDenied
		default:
			return nil, fmt.Errorf("unexpected sign-in status %q", pr.Status)
		}
		if onStatus != nil {
			onStatus(pr.Status)
		}
	}
}

// Logout revokes key server-side. A 401/403 (already revoked/expired) counts
// as success; network errors are returned so the caller can warn that the
// key may still be live remotely.
func (c *DeviceClient) Logout(ctx context.Context, key string) error {
	if key == "" {
		return nil
	}
	resp, _, err := c.post(ctx, "/cli/auth/logout", map[string]string{}, key)
	if err != nil {
		return err
	}
	switch {
	case resp.StatusCode == http.StatusOK, resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
		return nil
	}
	return fmt.Errorf("logout: %s", resp.Status)
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
