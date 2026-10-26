package tui

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tjcoder-labs/cli/internal/auth"
	"github.com/tjcoder-labs/cli/internal/client"
)

func TestPlausibleEmail(t *testing.T) {
	for _, ok := range []string{"a@b.co", "tj@tjcoder.com"} {
		if !plausibleEmail(ok) {
			t.Errorf("%q should pass", ok)
		}
	}
	for _, bad := range []string{"", "nope", "@b.co", "a@b", "a b@c.com", "a@b.c<o"} {
		if plausibleEmail(bad) {
			t.Errorf("%q should fail", bad)
		}
	}
}

func TestLoginCodeViewShowsCodeAndURL(t *testing.T) {
	st := &auth.DeviceStart{UserCode: "ABCD-2345", VerificationURI: "https://x.example/cli/auth/verify", ExpiresIn: 900}
	v := loginCodeView(darkPalette(), "me@x.co", st, 14*time.Minute+5*time.Second, true)
	for _, want := range []string{"ABCD-2345", "https://x.example/cli/auth/verify", "me@x.co", "14:05", "opened in your browser"} {
		if !strings.Contains(v, want) {
			t.Errorf("view missing %q:\n%s", want, v)
		}
	}
}

func TestDefaultOpenBrowserRejectsNonHTTP(t *testing.T) {
	for _, u := range []string{"file:///etc/passwd", "javascript:alert(1)", "--flag"} {
		if err := defaultOpenBrowser(u); err == nil {
			t.Errorf("%q must be refused", u)
		}
	}
}

func TestFetchErgoBalanceCents(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/wallet/balance" || r.Header.Get("Authorization") != "Bearer k1" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"balance_cents":432}`))
	}))
	defer srv.Close()
	t.Setenv("ERGO_BASE_URL", srv.URL)
	if ergoBaseURL() != srv.URL {
		t.Skipf("ergoBaseURL not overridable via env in this build (%s)", ergoBaseURL())
	}
	got, err := fetchErgoBalance(context.Background(), "k1")
	if err != nil || got != "$4.32" {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := fetchErgoBalanceCents(context.Background(), "wrong"); err == nil || !strings.Contains(err.Error(), "/login") {
		t.Fatalf("expected re-login hint, got %v", err)
	}
}

func TestErgoRunErrorNote(t *testing.T) {
	a := &App{}
	// 401/403 → /login hint
	if n := a.ergoRunErrorNote(&client.ErgoAuthError{}); !strings.Contains(n, "/login") {
		t.Errorf("auth note: %q", n)
	}
	if n := a.ergoRunErrorNote(errors.New("boom")); n != "" {
		t.Errorf("unrelated error got note %q", n)
	}
}
