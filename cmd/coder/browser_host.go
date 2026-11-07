package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/tjcoder-labs/cli/internal/nativehost"
)

func browserHostCommand(args []string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if len(args) != 1 {
		return fmt.Errorf("usage: coder browser-host register|unregister (interactive coder starts the relay automatically)")
	}
	switch args[0] {
	case "register":
		paths, err := nativehost.Register(exe)
		if err == nil {
			for _, p := range paths {
				fmt.Println(p)
			}
		}
		return err
	case "unregister":
		return nativehost.Unregister(exe)
	default:
		return fmt.Errorf("usage: coder browser-host register|unregister")
	}
}

// This wrapper is called only after all headless dispatch has returned.
func runInteractiveBrowser(run func() error) error {
	exe, err := os.Executable()
	var relay *nativehost.Session
	if err == nil {
		relay, err = nativehost.StartInteractive(exe)
	}
	if err != nil {
		// A missing extension or a second session must not break CDP or the TUI.
		fmt.Fprintf(os.Stderr, "Browser relay unavailable (CDP unchanged): %v\n", err)
		return run()
	}
	defer relay.Close()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	done := make(chan struct{})
	defer close(done)
	defer signal.Stop(signals)
	go func() {
		select {
		case sig := <-signals:
			relay.Close()
			signal.Reset(sig)
			if p, e := os.FindProcess(os.Getpid()); e == nil {
				_ = p.Signal(sig)
			}
		case <-done:
		}
	}()
	return run()
}
