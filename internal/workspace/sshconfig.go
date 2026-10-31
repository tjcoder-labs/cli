package workspace

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// SSHHost is one concrete Host alias from ~/.ssh/config. Only the alias
// is ever handed to ssh; the other fields are informational (tip line,
// /dirs) because the system ssh binary applies the real config,
// including Match blocks, ProxyJump and IdentityFile.
type SSHHost struct {
	Alias        string
	HostName     string
	User         string
	Port         string
	IdentityFile string
	ProxyJump    string
}

// maxIncludeDepth bounds Include recursion (ssh itself allows 16).
const maxIncludeDepth = 16

// DefaultSSHConfigPath is ~/.ssh/config ("" when HOME is unknown).
func DefaultSSHConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".ssh", "config")
}

// LoadSSHHosts parses path (and its Includes) and returns every concrete
// host alias, sorted. Patterns (*, ?, negations) are skipped: they are
// defaults, not hosts. A missing file yields no hosts and no error.
func LoadSSHHosts(path string) ([]SSHHost, error) {
	if path == "" {
		return nil, nil
	}
	p := &sshParser{byAlias: map[string]*SSHHost{}, seen: map[string]bool{}, sshDir: filepath.Dir(path)}
	if err := p.parseFile(path, 0); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	out := make([]SSHHost, 0, len(p.order))
	for _, a := range p.order {
		out = append(out, *p.byAlias[a])
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Alias < out[j].Alias })
	return out, nil
}

type sshParser struct {
	byAlias map[string]*SSHHost
	order   []string
	seen    map[string]bool // files already parsed (Include loops)
	sshDir  string
}

func isPattern(h string) bool { return strings.ContainsAny(h, "*?!") }

// splitKV splits "Key value", "Key=value" and "Key = value".
func splitKV(line string) (string, string) {
	line = strings.TrimSpace(line)
	i := strings.IndexAny(line, " \t=")
	if i < 0 {
		return line, ""
	}
	key := line[:i]
	rest := strings.TrimLeft(line[i:], " \t")
	rest = strings.TrimPrefix(rest, "=")
	return key, strings.TrimSpace(rest)
}

// fields splits on whitespace, honouring double quotes.
func fields(s string) []string {
	var out []string
	var cur strings.Builder
	inQ, have := false, false
	for _, r := range s {
		switch {
		case r == '"':
			inQ = !inQ
			have = true
		case (r == ' ' || r == '\t') && !inQ:
			if have {
				out = append(out, cur.String())
				cur.Reset()
				have = false
			}
		default:
			cur.WriteRune(r)
			have = true
		}
	}
	if have {
		out = append(out, cur.String())
	}
	return out
}

func (p *sshParser) parseFile(path string, depth int) error {
	if depth > maxIncludeDepth {
		return nil
	}
	abs, _ := filepath.Abs(path)
	if p.seen[abs] {
		return nil
	}
	p.seen[abs] = true
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	var current []*SSHHost // hosts the current block applies to
	inMatch := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val := splitKV(line)
		switch strings.ToLower(key) {
		case "host":
			current, inMatch = nil, false
			for _, h := range fields(val) {
				if isPattern(h) {
					continue
				}
				e, ok := p.byAlias[h]
				if !ok {
					e = &SSHHost{Alias: h}
					p.byAlias[h] = e
					p.order = append(p.order, h)
				}
				current = append(current, e)
			}
		case "match":
			current, inMatch = nil, true
		case "include":
			for _, pat := range fields(val) {
				pat = expandHome(pat)
				if !filepath.IsAbs(pat) {
					pat = filepath.Join(p.sshDir, pat)
				}
				matches, _ := filepath.Glob(pat)
				sort.Strings(matches)
				for _, m := range matches {
					_ = p.parseFile(m, depth+1)
				}
			}
		default:
			if inMatch {
				continue
			}
			// First value wins, as in ssh.
			for _, e := range current {
				switch strings.ToLower(key) {
				case "hostname":
					setOnce(&e.HostName, val)
				case "user":
					setOnce(&e.User, val)
				case "port":
					setOnce(&e.Port, val)
				case "identityfile":
					setOnce(&e.IdentityFile, val)
				case "proxyjump":
					setOnce(&e.ProxyJump, val)
				}
			}
		}
	}
	return sc.Err()
}

func setOnce(dst *string, v string) {
	if *dst == "" {
		*dst = v
	}
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}

// FilterHosts returns the hosts whose alias (or HostName) starts with
// prefix, then those containing it, case-insensitively.
func FilterHosts(hosts []SSHHost, prefix string) []SSHHost {
	q := strings.ToLower(prefix)
	var starts, contains []SSHHost
	for _, h := range hosts {
		a := strings.ToLower(h.Alias)
		switch {
		case strings.HasPrefix(a, q):
			starts = append(starts, h)
		case q != "" && (strings.Contains(a, q) || strings.Contains(strings.ToLower(h.HostName), q)):
			contains = append(contains, h)
		}
	}
	return append(starts, contains...)
}
