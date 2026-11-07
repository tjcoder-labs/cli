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
	"golang.org/x/term"
)

func mcpCommand(args []string) error {
	tunnel := len(args) > 0 && args[0] == "tunnel"
	if tunnel {
		args = args[1:]
	}
	flags := flag.NewFlagSet("mcp", flag.ContinueOnError)
	flags.Usage = func() { fmt.Fprintln(flags.Output(), mcpUsage); flags.PrintDefaults() }
	stdio := flags.Bool("stdio", false, "serve newline-delimited JSON-RPC only (no public relay)")
	root := flags.String("workspace-root", mustCWD(), "workspace exposed to the consuming agent")
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("%s", mcpUsage)
	}
	local, err := mcpLocalMode(tunnel, *stdio, term.IsTerminal(int(os.Stdin.Fd())), term.IsTerminal(int(os.Stdout.Fd())))
	if err != nil {
		return err
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
	server, err := mcpserver.NewLocalServer(abs, key, version)
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer cancel()
	if local {
		go func() { <-ctx.Done(); _ = os.Stdin.Close() }()
		return server.ServeStdio(ctx, os.Stdin, os.Stdout)
	}
	if !term.IsTerminal(int(os.Stderr.Fd())) {
		return fmt.Errorf("public MCP requires a terminal on stderr to display its credential safely; use --stdio for local consumers")
	}
	if key == "" {
		return fmt.Errorf("sign in with /login in interactive coder before starting an MCP tunnel")
	}
	// Operator-only activity goes to terminal stderr, never the MCP stream.
	printer := &mcpToolPrinter{env: cliEnv{stderr: os.Stderr, color: colorEnabled(os.Stderr)}}
	server.OnToolCall = printer.started
	// Foreground invocation is explicit delegation of this toolset, not a sandbox.
	fmt.Fprintf(os.Stderr, "Tunneling %d local tools in %s. Anyone holding the secret URL can use them until exit/expiry. Sudo remains denied. Ctrl-C revokes.\n", len(server.Registry.Names()), abs)
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

const mcpUsage = "Usage: coder mcp tunnel [--workspace-root DIR] (public secret URL; /login required; sudo denied)\n       coder mcp --stdio [--workspace-root DIR] (local MCP server)\nPiped stdin or stdout always selects local stdio; bare coder mcp on a terminal shows this help.\nPublic tunnels print themed, secret-safe tool activity to stderr; NO_COLOR disables ANSI."

func mcpLocalMode(tunnel, stdio, stdinTTY, stdoutTTY bool) (bool, error) {
	if tunnel && stdio {
		return false, fmt.Errorf("tunnel and --stdio cannot be combined; %s", mcpUsage)
	}
	if stdio || !stdinTTY || !stdoutTTY {
		return true, nil
	}
	if !tunnel {
		return false, fmt.Errorf("%s", mcpUsage)
	}
	return false, nil
}
