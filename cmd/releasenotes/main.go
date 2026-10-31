// Command releasenotes validates release-notes/releases.json and
// regenerates CHANGELOG.md from it. Run it from anywhere in the repo:
//
//	go run ./cmd/releasenotes          # validate and write CHANGELOG.md
//	go run ./cmd/releasenotes -check   # validate; fail if CHANGELOG.md is stale
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tjcoder-labs/cli/internal/releasenotes"
)

func main() {
	check := flag.Bool("check", false, "do not write; exit 1 if CHANGELOG.md is out of date")
	flag.Parse()
	if err := run(*check); err != nil {
		fmt.Fprintln(os.Stderr, "releasenotes:", err)
		os.Exit(1)
	}
}

func run(check bool) error {
	root, err := repoRoot()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(root, "release-notes", "releases.json"))
	if err != nil {
		return err
	}
	if errs := releasenotes.Validate(data); len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintln(os.Stderr, "  -", e)
		}
		return fmt.Errorf("%d problem(s) in release-notes/releases.json", len(errs))
	}
	n, err := releasenotes.Parse(data)
	if err != nil {
		return err
	}
	want := []byte(releasenotes.Markdown(n))
	path := filepath.Join(root, "CHANGELOG.md")
	if check {
		got, _ := os.ReadFile(path)
		if !bytes.Equal(got, want) {
			return errors.New("CHANGELOG.md is out of date; run `go run ./cmd/releasenotes` and commit it")
		}
		return nil
	}
	if err := os.WriteFile(path, want, 0o644); err != nil {
		return err
	}
	fmt.Println("wrote", path)
	return nil
}

// repoRoot walks up from the working directory to the go.mod.
func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("go.mod not found; run from inside the repository")
		}
		dir = parent
	}
}
