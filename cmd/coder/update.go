package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/tjcoder-labs/cli/internal/appdir"
	"github.com/tjcoder-labs/cli/internal/tui"
	"github.com/tjcoder-labs/cli/internal/update"
)

// buildMethod is stamped to "binary" by scripts/build-release.sh so a
// downloaded release can update itself in place; empty means a source
// build (go build / make build), which only gets instructions.
var buildMethod string

func init() { tui.BuildMethod = buildMethod }

// cmdUpdate implements `coder update [--check] [-y] [check-on|check-off|auto [on|off]]`.
func cmdUpdate(args []string) {
	fs := flag.NewFlagSet("update", flag.ExitOnError)
	checkOnly := fs.Bool("check", false, "only report whether an update is available")
	yes := fs.Bool("y", false, "do not ask for confirmation")
	_ = fs.Parse(args)
	// `coder update check-on|check-off` toggles the background check.
	if a := fs.Arg(0); a == "check-on" || a == "check-off" {
		on := a == "check-on"
		if _, err := appdir.Update(func(s *appdir.Settings) { s.UpdateCheck = &on }); err != nil {
			fatal("update: %v", err)
		}
		fmt.Printf("Background update check %s.\n", map[bool]string{true: "enabled", false: "disabled"}[on])
		return
	}

	// `coder update auto on|off` toggles automatic background installs.
	if fs.Arg(0) == "auto" {
		switch fs.Arg(1) {
		case "on", "off":
			on := fs.Arg(1) == "on"
			if _, err := appdir.Update(func(s *appdir.Settings) { s.AutoUpdate = &on }); err != nil {
				fatal("update: %v", err)
			}
			fmt.Printf("Automatic updates %s.\n", map[bool]string{true: "enabled", false: "disabled"}[on])
			if ok, why := update.AutoUpdateEnabled(); on && !ok && why == update.AutoUpdateEnv {
				fmt.Println("Note: " + update.AutoUpdateEnv + "=0 is set and still keeps them off.")
			}
		case "":
			ok, why := update.AutoUpdateEnabled()
			state := map[bool]string{true: "on", false: "off"}[ok]
			if why != "" {
				state += " (" + why + ")"
			}
			fmt.Println("Automatic updates: " + state)
		default:
			fatal("usage: coder update auto [on|off]")
		}
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	c := update.NewChecker()
	c.HTTP.Timeout = 30 * time.Second
	res, err := c.Check(ctx, version)
	if err != nil {
		fatal("update check failed: %v", err)
	}
	if !res.Available() {
		fmt.Printf("coder v%s is up to date (latest v%s)\n", version, res.Latest)
		return
	}
	exe, _ := os.Executable()
	method := update.DetectMethod(exe, buildMethod)
	fmt.Printf("Update available: v%s → v%s (%s install)\n", version, res.Latest, method)
	if *checkOnly {
		return
	}
	plan, err := c.Prepare(ctx, method, version, res.Latest, exe)
	if errors.Is(err, update.ErrNoBinaryRelease) {
		fmt.Println(err)
		return
	} else if err != nil {
		fatal("update: %v", err)
	}
	switch method {
	case update.MethodSource:
		fmt.Println("Built from source; update with:\n  " + plan.Command)
		return
	case update.MethodNPM:
		fmt.Println("Will run: " + plan.Command)
	case update.MethodBinary:
		fmt.Printf("Will replace %s with v%s from %s\n", plan.Target, plan.Version, plan.Asset)
	}
	if !*yes && !confirm("Proceed? [y/N] ") {
		fmt.Println("Cancelled.")
		return
	}
	if err := c.Apply(ctx, plan, os.Stdout); err != nil {
		fatal("update failed: %v", err)
	}
	fmt.Printf("Updated to v%s.\n", plan.Version)
}

func confirm(q string) bool {
	fmt.Print(q)
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	a := strings.ToLower(strings.TrimSpace(line))
	return a == "y" || a == "yes"
}
