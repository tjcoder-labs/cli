package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/tjcoder-labs/cli/internal/auth"
	"github.com/tjcoder-labs/cli/internal/client"
	"github.com/tjcoder-labs/cli/internal/mcpserver"
	"github.com/tjcoder-labs/cli/internal/nativehost"
	"github.com/tjcoder-labs/cli/internal/session"
	"github.com/tjcoder-labs/cli/internal/tools"
	"github.com/tjcoder-labs/cli/internal/tracking"
	"github.com/tjcoder-labs/cli/internal/workspace"
	"golang.org/x/term"
)

func mcpCommand(args []string) error {
	flags := flag.NewFlagSet("mcp", flag.ContinueOnError)
	stdio := flags.Bool("stdio", false, "serve newline-delimited JSON-RPC only (no public relay)")
	root := flags.String("workspace-root", mustCWD(), "workspace exposed to the consuming agent")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("usage: coder mcp [--stdio] [--workspace-root DIR]")
	}
	abs, err := filepath.Abs(*root)
	if err != nil {
		return err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("workspace must be a directory")
	}
	key := auth.ResolveErgoToken()
	provider := client.NewErgo(client.ErgoDefaultBaseURL, key, 2*time.Minute)
	registry := tools.NewRegistry(provider)
	state, _, err := session.Load(abs)
	if err != nil {
		return err
	}
	track := tracking.NewRegistry()
	track.Register(tracking.NewTaskTracker())
	track.Register(tracking.NewMemoryTracker())
	track.Register(tracking.NewReminderTracker(abs))
	registry.RegisterTool(tools.ManageItemsBridge{Impl: tools.NewManageItems(track, &state)})
	env := tools.ExecEnv{WorkspaceRoot: abs, Provider: provider, SessionState: &state, PersistSession: func() error { return session.Save(abs, state) }, Workspaces: workspace.NewSet(abs)}
	server := &mcpserver.Server{Registry: registry, Env: env, Version: version}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer cancel()
	if *stdio || !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
		go func() { <-ctx.Done(); _ = os.Stdin.Close() }()
		return server.ServeStdio(ctx, os.Stdin, os.Stdout)
	}
	if !term.IsTerminal(int(os.Stderr.Fd())) {
		return fmt.Errorf("public MCP requires a terminal on stderr to display its credential safely; use --stdio for local consumers")
	}
	if key == "" {
		return fmt.Errorf("sign in with /login in interactive coder before publishing MCP tools")
	}
	// Foreground invocation is explicit delegation of this toolset, not a sandbox.
	fmt.Fprintf(os.Stderr, "Publishing %d local tools in %s. Anyone holding the secret URL can use them until exit/expiry. Sudo remains denied. Ctrl-C revokes.\n", len(registry.Names()), abs)
	relay := &mcpserver.Relay{BaseURL: client.ErgoDefaultBaseURL, Key: key, Server: server}
	// Own lifecycle here: the TUI browser wrapper re-raises signals, while MCP
	// needs graceful cancellation and best-effort revocation on those signals.
	exe, browserErr := os.Executable()
	if browserErr == nil {
		var browserSession *nativehost.Session
		browserSession, browserErr = nativehost.StartInteractive(exe)
		if browserErr == nil {
			defer browserSession.Close()
		}
	}
	if browserErr != nil {
		fmt.Fprintf(os.Stderr, "Browser relay unavailable (CDP unchanged): %v\n", browserErr)
	}
	return relay.Run(ctx, func(p mcpserver.Publication) {
		expiry := p.ExpiresAt
		if t, e := time.Parse(time.RFC3339, p.ExpiresAt); e == nil {
			expiry = t.Local().Format("2006-01-02 15:04:05 MST")
		}
		fmt.Fprintf(os.Stderr, "MCP URL: %s\nExpires: %s\nKeep this URL private; it alone grants tool access. No credential is saved to disk.\n", p.URL, expiry)
	})
}
