package main

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestCmdAskEmptyReplyExitsNonZero runs cmdAsk in a child process (it
// calls os.Exit) against a mock Ollama server whose reply is empty, and
// checks the run fails with a clear message on stderr and nothing on
// stdout.
func TestCmdAskEmptyReplyExitsNonZero(t *testing.T) {
	if host := os.Getenv("CODER_TEST_EMPTY_REPLY_HOST"); host != "" {
		cmdAsk(askOptions{
			Host:          host,
			Provider:      "ollama",
			WorkspaceRoot: os.Getenv("CODER_TEST_EMPTY_REPLY_WS"),
			Model:         "test-model",
			Timeout:       5 * time.Second,
			Agent:         "software-engineer",
			Prompt:        "Reply with the word OK",
			Quiet:         true,
			Session:       false,
			NoTools:       true,
			ExplicitModel: true,
			ExplicitAgent: true,
		})
		os.Exit(0)
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/chat":
			w.Header().Set("Content-Type", "application/x-ndjson")
			_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":""},"done":true}` + "\n"))
		case "/api/tags":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"models":[{"name":"test-model"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	cmd := exec.Command(os.Args[0], "-test.run=^TestCmdAskEmptyReplyExitsNonZero$")
	cmd.Env = append(os.Environ(),
		"CODER_TEST_EMPTY_REPLY_HOST="+ts.URL,
		"CODER_TEST_EMPTY_REPLY_WS="+t.TempDir(),
		"XDG_CONFIG_HOME="+t.TempDir(),
		"HOME="+t.TempDir(),
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() == 0 {
		t.Fatalf("empty reply: err = %v, want non-zero exit\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "empty reply") {
		t.Fatalf("stderr %q does not explain the empty reply", stderr.String())
	}
	if strings.TrimSpace(stdout.String()) != "" {
		t.Fatalf("stdout = %q, want nothing", stdout.String())
	}
}
