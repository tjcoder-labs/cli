package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestMain(m *testing.M) {
	// The askpass wrapper re-executes this test binary as the helper.
	if len(os.Args) > 1 && os.Args[1] == AskpassSubcommand {
		os.Exit(AskpassMain(os.Args[2:], os.Stdout))
	}
	os.Exit(m.Run())
}

func writeFile(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadSSHHosts(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "config"), `
# comment
Include conf.d/*.conf
Host *
  ServerAliveInterval 30
Host web1 web2 "db one"
  HostName 10.0.0.5
  User deploy
Host bastion
  HostName=bastion.example.com
  Port = 2222
  IdentityFile ~/.ssh/id_bastion
Host *.internal !secret prod-?
  User nobody
Match host web1
  User shouldnotapply
Host jumped
  ProxyJump bastion
  HostName 10.1.1.1
`)
	writeFile(t, filepath.Join(dir, "conf.d", "a.conf"), "Host included\n  HostName inc.example.com\nInclude ../config\n")
	writeFile(t, filepath.Join(dir, "conf.d", "b.conf"), "host lower-case\n")
	hosts, err := LoadSSHHosts(filepath.Join(dir, "config"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	byName := map[string]SSHHost{}
	for _, h := range hosts {
		names = append(names, h.Alias)
		byName[h.Alias] = h
	}
	want := []string{"bastion", "db one", "included", "jumped", "lower-case", "web1", "web2"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("hosts = %q, want %q", names, want)
	}
	if h := byName["web2"]; h.HostName != "10.0.0.5" || h.User != "deploy" {
		t.Errorf("web2 = %+v", h)
	}
	if h := byName["web1"]; h.User != "deploy" {
		t.Errorf("Match block leaked into web1: %+v", h)
	}
	if h := byName["bastion"]; h.HostName != "bastion.example.com" || h.Port != "2222" || h.IdentityFile != "~/.ssh/id_bastion" {
		t.Errorf("bastion = %+v", h)
	}
	if h := byName["jumped"]; h.ProxyJump != "bastion" {
		t.Errorf("jumped = %+v", h)
	}
	if h := byName["included"]; h.HostName != "inc.example.com" {
		t.Errorf("included = %+v", h)
	}
	if hosts, err := LoadSSHHosts(filepath.Join(dir, "missing")); err != nil || len(hosts) != 0 {
		t.Errorf("missing config: %v %v", hosts, err)
	}
}

func TestFilterHosts(t *testing.T) {
	hosts := []SSHHost{{Alias: "web1"}, {Alias: "web2"}, {Alias: "db", HostName: "webdb.example.com"}, {Alias: "Prod"}}
	alias := func(hs []SSHHost) (out []string) {
		for _, h := range hs {
			out = append(out, h.Alias)
		}
		return
	}
	if got := alias(FilterHosts(hosts, "we")); !reflect.DeepEqual(got, []string{"web1", "web2", "db"}) {
		t.Errorf("we -> %v", got)
	}
	if got := alias(FilterHosts(hosts, "pro")); !reflect.DeepEqual(got, []string{"Prod"}) {
		t.Errorf("pro -> %v", got)
	}
	if got := FilterHosts(hosts, ""); len(got) != 4 {
		t.Errorf("empty filter -> %v", got)
	}
}

func TestLocalResolveContainment(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	root := filepath.Join(base, "proj")
	sibling := filepath.Join(base, "proj-evil")
	writeFile(t, filepath.Join(root, "a.txt"), "a")
	writeFile(t, filepath.Join(sibling, "secret"), "s")
	if err := os.Symlink(sibling, filepath.Join(root, "out")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "a.txt"), filepath.Join(root, "in")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(sibling, "nope"), filepath.Join(root, "dangling")); err != nil {
		t.Fatal(err)
	}
	w := NewLocal(root, "")
	if w.Name != "proj" {
		t.Errorf("name = %q", w.Name)
	}
	for _, ok := range []string{"a.txt", ".", "", "new/dir/file.go", filepath.Join(root, "a.txt"), "in"} {
		if _, err := w.Resolve(ctx, ok); err != nil {
			t.Errorf("Resolve(%q): %v", ok, err)
		}
	}
	for _, bad := range []string{"../proj-evil/secret", sibling + "/secret", "out/secret", "out/newfile", "dangling", "/etc/passwd"} {
		if p, err := w.Resolve(ctx, bad); err == nil {
			t.Errorf("Resolve(%q) = %q, want refusal", bad, p)
		}
	}
	if p, _ := w.Resolve(ctx, "in"); p != filepath.Join(w.Root, "a.txt") {
		t.Errorf("in resolved to %q", p)
	}
}

func TestLocalFSOps(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	fs := LocalFS{}
	p := filepath.Join(root, "d", "f.txt")
	if err := fs.WriteFile(ctx, p, []byte("one")); err != nil {
		t.Fatal(err)
	}
	os.Chmod(p, 0o600)
	if err := fs.WriteFile(ctx, p, []byte("two")); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(p); st.Mode().Perm() != 0o600 {
		t.Errorf("mode not kept: %v", st.Mode())
	}
	if err := fs.CreateFile(ctx, p, []byte("x")); !errors.Is(err, os.ErrExist) {
		t.Errorf("CreateFile over existing: %v", err)
	}
	fs.AppendFile(ctx, p, []byte("+"))
	if b, _ := fs.ReadFile(ctx, p); string(b) != "two+" {
		t.Errorf("content %q", b)
	}
	writeFile(t, filepath.Join(root, ".git", "HEAD"), "x")
	writeFile(t, filepath.Join(root, "d", "e", "deep.txt"), "x")
	ents, _ := fs.Walk(ctx, root, 2)
	var rels []string
	for _, e := range ents {
		rels = append(rels, e.Rel)
	}
	if strings.Contains(strings.Join(rels, ","), ".git") || strings.Contains(strings.Join(rels, ","), "deep") {
		t.Errorf("walk = %v", rels)
	}
}

func TestSetTarget(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	a, b := filepath.Join(base, "app"), filepath.Join(base, "lib")
	writeFile(t, filepath.Join(a, "main.go"), "a")
	writeFile(t, filepath.Join(b, "lib.go"), "b")
	s := NewSet(a)
	w, added := s.Add(NewLocal(b, ""))
	if !added || w.Name != "lib" {
		t.Fatalf("add lib: %v %q", added, w.Name)
	}
	if _, again := s.Add(NewLocal(b, "")); again {
		t.Error("duplicate root added twice")
	}
	dup, _ := s.Add(NewLocal(filepath.Join(base, "x", "app"), "app"))
	if dup.Name != "app-2" {
		t.Errorf("unique name = %q", dup.Name)
	}
	cases := []struct{ ws, p, wantWS, wantPath string }{
		{"", "main.go", "app", filepath.Join(a, "main.go")},
		{"", "lib:lib.go", "lib", filepath.Join(b, "lib.go")},
		{"lib", "lib.go", "lib", filepath.Join(b, "lib.go")},
		{"lib", "lib:lib.go", "lib", filepath.Join(b, "lib.go")},
		{"", filepath.Join(b, "lib.go"), "lib", filepath.Join(b, "lib.go")},
		{"", "nosuch:file", "app", filepath.Join(a, "nosuch:file")},
	}
	for _, c := range cases {
		w, p, err := s.Target(ctx, c.ws, c.p)
		if err != nil || w.Name != c.wantWS || p != c.wantPath {
			t.Errorf("Target(%q,%q) = %v %q %v; want %s %s", c.ws, c.p, w, p, err, c.wantWS, c.wantPath)
		}
	}
	if _, _, err := s.Target(ctx, "nope", "x"); err == nil {
		t.Error("unknown workspace accepted")
	}
	if _, _, err := s.Target(ctx, "lib", "../app/main.go"); err == nil {
		t.Error("cross-workspace escape accepted")
	}
	if err := s.SetActive("lib"); err != nil {
		t.Fatal(err)
	}
	if w, p, _ := s.Target(ctx, "", "lib.go"); w.Name != "lib" || p != filepath.Join(b, "lib.go") {
		t.Errorf("active target = %s %s", w.Name, p)
	}
	if !strings.Contains(s.PromptBlock(), "- lib [local, active] "+b) {
		t.Errorf("prompt block:\n%s", s.PromptBlock())
	}
	if err := s.Remove("app"); err == nil {
		t.Error("primary removed")
	}
	if err := s.Remove("lib"); err != nil || s.ActiveName() != "app" {
		t.Errorf("remove active: %v active=%s", err, s.ActiveName())
	}
	if NewSet(a).PromptBlock() != "" {
		t.Error("single workspace should add no prompt block")
	}
}

func TestSetConcurrentAccess(t *testing.T) {
	s := NewSet(t.TempDir())
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); s.Add(NewLocal(t.TempDir(), "")) }()
		go func() { defer wg.Done(); _ = s.PromptBlock(); _ = s.Active() }()
	}
	wg.Wait()
}

func TestSSHVersionParse(t *testing.T) {
	for v, want := range map[string]bool{
		"OpenSSH_10.0p2 Debian": true, "OpenSSH_8.4p1": true, "OpenSSH_8.3p1": false,
		"OpenSSH_7.9": false, "garbage": false,
	} {
		if got := sshVersionAtLeast(v, 8, 4); got != want {
			t.Errorf("%q: %v", v, got)
		}
	}
}

func TestQuote(t *testing.T) {
	if got := Quote(`it's $HOME`); got != `'it'\''s $HOME'` {
		t.Errorf("Quote = %s", got)
	}
}
