package tui

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rivo/tview"
	"github.com/tjcoder-labs/cli/internal/tools"
	"github.com/tjcoder-labs/cli/internal/workspace"
)

// Workspaces: /add, /dirs (alias /workspaces), /use.
//
// The Set lives on the App and is shared with the runner, so every tool
// sees an added directory at once (mid-turn too). All App-side state
// here (caches, hint items) is touched on the UI goroutine only; ssh
// work runs on goroutines that post their results back with postUI.

const (
	sshHostsTTL     = 10 * time.Second
	remoteDirsTTL   = 30 * time.Second
	wsArgMaxItems   = 60
	sshConnectLimit = 11 * time.Minute // askpass cards time out at 10m
)

// wsArg is one suggestion in the workspace-argument hint line.
type wsArg struct {
	label  string // what the palette shows
	insert string // the full argument text placed after "/cmd "
	desc   string // faint trailer for the highlighted item
}

type remoteDirEntry struct {
	dirs []string
	at   time.Time
}

// initWorkspaces creates the workspace set for root and hands it to the
// runner. Called once from NewApp, before loadSession.
func (a *App) initWorkspaces(root string) {
	a.workspaces = workspace.NewSet(root)
	a.sshFS = map[string]*workspace.SSHFS{}
	a.remoteDirs = map[string]remoteDirEntry{}
	a.remoteDirsBusy = map[string]bool{}
	if a.runner != nil {
		a.runner.Workspaces = a.workspaces
	}
}

// closeWorkspaces stops the askpass bridge. ControlMasters are left to
// ControlPersist so a quick restart reuses the authenticated connection.
func (a *App) closeWorkspaces() {
	if a.askBridge != nil {
		a.askBridge.Close()
		a.askBridge = nil
	}
}

// ensureAskBridge starts the SSH_ASKPASS bridge on first use.
func (a *App) ensureAskBridge() (*workspace.Bridge, error) {
	if a.askBridge != nil {
		return a.askBridge, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("askpass: %w", err)
	}
	b, err := workspace.NewBridge(exe, a.answerSSHPrompt)
	if err != nil {
		return nil, err
	}
	a.askBridge = b
	return b, nil
}

// sshFSFor returns the (cached) ssh backend for host, wired to the
// askpass bridge so passwords, passphrases and 2FA codes arrive through
// the secure input card.
func (a *App) sshFSFor(host string) (*workspace.SSHFS, error) {
	if fs, ok := a.sshFS[host]; ok {
		return fs, nil
	}
	b, err := a.ensureAskBridge()
	if err != nil {
		return nil, err
	}
	fs := workspace.NewSSHFS(host, b.Env(host))
	a.sshFS[host] = fs
	return fs, nil
}

// sshHostKeyOptions: the host-key card. "No" is first and is the
// default; only an explicit "Yes" answers ssh with "yes".
var sshHostKeyOptions = []string{"No, do not connect", "Yes, trust this host key and connect"}

// answerSSHPrompt is the askpass AskFunc. It runs on the bridge's
// goroutine (never the UI loop), so the blocking card calls are safe.
func (a *App) answerSSHPrompt(ctx context.Context, req workspace.AskRequest) ([]byte, bool) {
	title := "SSH · " + req.Host
	if req.IsConfirm() {
		ans, err := a.Ask(ctx, tools.PromptRequest{
			Kind:     tools.PromptAuthorization,
			Title:    title,
			Question: "Trust this host key and connect?",
			Detail:   strings.TrimSpace(req.Prompt),
			Options:  sshHostKeyOptions,
			Default:  1,
		})
		if err != nil || ans.Cancelled || ans.Index != 1 || ans.Choice != sshHostKeyOptions[1] {
			return nil, false
		}
		return []byte("yes"), true
	}
	label := strings.TrimSpace(req.Prompt)
	if label == "" {
		label = "Password"
	}
	secret, err := a.AskSecret(ctx, title, label)
	if err != nil || secret == nil {
		return nil, false
	}
	return secret, true
}

// persistWorkspaces mirrors the set into the session and saves it.
func (a *App) persistWorkspaces() {
	a.sessionState.Workspaces = a.workspaces.Specs()
	a.sessionState.ActiveWorkspace = a.workspaces.ActiveName()
	_ = a.saveSession()
}

// restoreWorkspaces re-adds the session's workspaces. Local ones are
// checked now; ssh ones are added unconnected and connect (prompting if
// needed) on their first use.
func (a *App) restoreWorkspaces(specs []workspace.Spec, active string) {
	if a.workspaces == nil {
		return
	}
	var missing []string
	for _, sp := range specs {
		switch sp.Kind {
		case workspace.KindLocal:
			if st, err := os.Stat(sp.Path); err != nil || !st.IsDir() {
				missing = append(missing, sp.Path)
				continue
			}
			a.workspaces.Add(workspace.NewLocal(sp.Path, sp.Name))
		case workspace.KindSSH:
			if workspace.ValidHostAlias(sp.Host) != nil || sp.Path == "" {
				continue
			}
			fs, err := a.sshFSFor(sp.Host)
			if err != nil {
				missing = append(missing, sp.Host+":"+sp.Path)
				continue
			}
			name := sp.Name
			if name == "" {
				name = sp.Host
			}
			a.workspaces.Add(&workspace.Workspace{Name: name, Kind: workspace.KindSSH, Root: sp.Path, Host: sp.Host, FS: fs})
		}
	}
	if active != "" {
		_ = a.workspaces.SetActive(active)
	}
	if len(missing) > 0 && a.activity != nil {
		a.appendActivity("Workspaces not restored (missing): " + strings.Join(missing, ", "))
	}
}

// commandArgs returns the raw text after the command word, so paths with
// spaces survive (handleSlashCommand's fields split them).
func commandArgs(cmd string) string {
	s := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(cmd), "/"))
	if i := strings.IndexAny(s, " \t"); i != -1 {
		return strings.TrimSpace(s[i+1:])
	}
	return ""
}

func unquoteArg(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1]
	}
	return s
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}

// handleAddCommand: /add <folder> | /add ssh <host>[:<path>].
func (a *App) handleAddCommand(raw string) {
	raw = strings.TrimSpace(raw)
	switch {
	case raw == "":
		a.Record("Usage: /add <folder path>  |  /add ssh <host>[:<path>]  (hosts come from ~/.ssh/config)")
		return
	case raw == "ssh" || strings.HasPrefix(raw, "ssh ") || strings.HasPrefix(raw, "ssh\t"):
		a.addSSHWorkspace(strings.TrimSpace(strings.TrimPrefix(raw, "ssh")))
		return
	}
	p := expandHome(unquoteArg(raw))
	if !filepath.IsAbs(p) {
		base := a.workspaceRoot
		if base == "" {
			base, _ = os.Getwd()
		}
		p = filepath.Join(base, p)
	}
	st, err := os.Stat(p)
	if err != nil {
		a.Record("/add: " + err.Error())
		return
	}
	if !st.IsDir() {
		a.Record("/add: " + p + " is not a directory")
		return
	}
	w, added := a.workspaces.Add(workspace.NewLocal(p, ""))
	if !added {
		a.Record(fmt.Sprintf("Already a workspace: %s (%s)", w.Name, w.Label()))
		return
	}
	a.fileIndex = nil // the @ picker now includes this folder
	a.persistWorkspaces()
	a.refreshContextBar()
	a.Record(fmt.Sprintf("Added workspace %s → %s. Tools reach it as workspace=%s or %s:path; /use %s makes it the default.",
		w.Name, w.Label(), w.Name, w.Name, w.Name))
}

func (a *App) addSSHWorkspace(target string) {
	if target == "" {
		hosts := a.sshHostList()
		names := make([]string, 0, len(hosts))
		for _, h := range hosts {
			names = append(names, h.Alias)
		}
		msg := "Usage: /add ssh <host>[:<path>]  (path defaults to the remote home)"
		if len(names) > 0 {
			msg += "\nHosts in ~/.ssh/config: " + strings.Join(names, ", ")
		}
		a.Record(msg)
		return
	}
	host, rp := workspace.ParseSSHTarget(unquoteArg(target))
	if err := workspace.ValidHostAlias(host); err != nil {
		a.Record("/add ssh: " + err.Error())
		return
	}
	fs, err := a.sshFSFor(host)
	if err != nil {
		a.Record("/add ssh: " + err.Error())
		return
	}
	shown := host
	if rp != "" {
		shown += ":" + rp
	}
	a.Record("Connecting to " + shown + " …")
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), sshConnectLimit)
		defer cancel()
		w, err := workspace.NewSSH(ctx, host, rp, fs)
		a.postUI(func() {
			if err != nil {
				a.Record("Couldn't add " + shown + ": " + err.Error())
				return
			}
			w, added := a.workspaces.Add(w)
			if !added {
				a.Record(fmt.Sprintf("Already a workspace: %s (%s)", w.Name, w.Label()))
				return
			}
			a.persistWorkspaces()
			a.refreshContextBar()
			a.Record(fmt.Sprintf("Added ssh workspace %s → %s. Tools reach it as workspace=%s or %s:path; /use %s makes it the default.",
				w.Name, w.Label(), w.Name, w.Name, w.Name))
		})
	}()
}

// handleDirsCommand: /dirs lists; /dirs rm <name> removes.
func (a *App) handleDirsCommand(raw string) {
	fields := strings.Fields(raw)
	if len(fields) > 0 {
		switch strings.ToLower(fields[0]) {
		case "rm", "remove", "del", "delete":
			if len(fields) < 2 {
				a.Record("Usage: /dirs rm <name>")
				return
			}
			a.removeWorkspace(fields[1])
			return
		}
	}
	a.Record(a.workspaceListing())
}

func (a *App) workspaceListing() string {
	all := a.workspaces.All()
	active := a.workspaces.ActiveName()
	var b strings.Builder
	b.WriteString("Workspaces")
	for i, w := range all {
		mark := "  "
		if strings.EqualFold(w.Name, active) {
			mark = "▸ "
		}
		tag := string(w.Kind)
		if i == 0 {
			tag += ", primary"
		}
		fmt.Fprintf(&b, "\n%s%s  %s  (%s)", mark, w.Name, w.Label(), tag)
	}
	if len(all) == 1 {
		b.WriteString("\nAdd more with /add <folder> or /add ssh <host>[:<path>].")
	}
	return b.String()
}

func (a *App) removeWorkspace(name string) {
	w, ok := a.workspaces.Get(name)
	if !ok {
		a.Record(fmt.Sprintf("No workspace named %q. Known: %s", name, strings.Join(a.workspaces.Names(), ", ")))
		return
	}
	if err := a.workspaces.Remove(name); err != nil {
		a.Record("/dirs rm: " + err.Error())
		return
	}
	if w.Kind == workspace.KindSSH {
		inUse := false
		for _, o := range a.workspaces.All() {
			if o.Host == w.Host {
				inUse = true
			}
		}
		if fs, ok := a.sshFS[w.Host]; ok && !inUse {
			delete(a.sshFS, w.Host)
			go fs.Close()
		}
	}
	a.fileIndex = nil
	a.persistWorkspaces()
	a.refreshContextBar()
	a.Record(fmt.Sprintf("Removed workspace %s (%s).", w.Name, w.Label()))
}

// handleUseCommand: /use <name> picks the default workspace.
func (a *App) handleUseCommand(raw string) {
	name := strings.TrimSpace(raw)
	if name == "" {
		a.Record(a.workspaceListing() + "\nUsage: /use <name>")
		return
	}
	if err := a.workspaces.SetActive(name); err != nil {
		a.Record(fmt.Sprintf("No workspace named %q. Known: %s", name, strings.Join(a.workspaces.Names(), ", ")))
		return
	}
	w := a.workspaces.Active()
	a.persistWorkspaces()
	a.refreshContextBar()
	a.Record(fmt.Sprintf("Active workspace: %s (%s). Relative paths and commands now default to it.", w.Name, w.Label()))
}

// workspaceStatus is the subtle status-bar segment: empty with only
// the primary; otherwise the active name and how many there are.
func (a *App) workspaceStatus() string {
	if a.workspaces == nil {
		return ""
	}
	n := len(a.workspaces.All())
	if n < 2 {
		return ""
	}
	return fmt.Sprintf("⌂ %s · %d", a.workspaces.ActiveName(), n)
}

// ---- hints -------------------------------------------------------------

func (a *App) sshHostList() []workspace.SSHHost {
	if a.sshHosts != nil && time.Since(a.sshHostsAt) < sshHostsTTL {
		return a.sshHosts
	}
	hosts, _ := workspace.LoadSSHHosts(a.sshConfigPath())
	if hosts == nil {
		hosts = []workspace.SSHHost{}
	}
	a.sshHosts, a.sshHostsAt = hosts, time.Now()
	return hosts
}

func (a *App) sshConfigPath() string {
	if a.sshConfigOverride != "" {
		return a.sshConfigOverride
	}
	return workspace.DefaultSSHConfigPath()
}

// workspaceArgHints computes the argument hint for /add, /use and /dirs
// once the command word is complete. ok is false for any other input.
func (a *App) workspaceArgHints(text string) (cmd, title string, items []wsArg, ok bool) {
	if a.workspaces == nil || !strings.HasPrefix(text, "/") || strings.Contains(text, "\n") {
		return "", "", nil, false
	}
	body := strings.TrimPrefix(text, "/")
	i := strings.IndexAny(body, " \t")
	if i == -1 {
		return "", "", nil, false
	}
	cmd = strings.ToLower(body[:i])
	rest := strings.TrimLeft(body[i+1:], " \t")
	switch cmd {
	case "add", "add-dir":
		title, items = a.addArgHints(rest)
	case "use":
		title = "use workspace:"
		items = a.workspaceNameArgs(rest, true, "")
	case "dirs", "workspaces":
		if r, found := cutWord(rest, "rm"); found {
			title = "remove workspace:"
			items = a.workspaceNameArgs(r, false, "rm ")
		} else if strings.Contains(rest, " ") {
			return "", "", nil, false
		} else {
			title = "dirs:"
			if strings.HasPrefix("rm", strings.ToLower(rest)) {
				items = []wsArg{{label: "rm <name>", insert: "rm ", desc: "remove an added workspace"}}
			}
		}
	default:
		return "", "", nil, false
	}
	return cmd, title, items, true
}

// cutWord returns what follows "word " at the start of s.
func cutWord(s, word string) (string, bool) {
	if len(s) > len(word) && strings.EqualFold(s[:len(word)], word) && (s[len(word)] == ' ' || s[len(word)] == '\t') {
		return strings.TrimLeft(s[len(word)+1:], " \t"), true
	}
	return "", false
}

func (a *App) addArgHints(rest string) (string, []wsArg) {
	if rest == "" {
		return "add:", []wsArg{
			{label: "folder path", insert: "./", desc: "a local folder: ./  ../  ~/  or /absolute"},
			{label: "ssh <host>", insert: "ssh ", desc: "a remote folder over ssh (hosts from ~/.ssh/config)"},
		}
	}
	if rest == "ssh" {
		return "ssh host:", a.sshHostArgs("")
	}
	if target, found := cutWord(rest, "ssh"); found {
		if host, p, hasPath := strings.Cut(target, ":"); hasPath {
			return host + ":", a.remoteDirArgs(host, p)
		}
		if strings.ContainsAny(target, " \t") {
			return "ssh host:", nil
		}
		return "ssh host:", a.sshHostArgs(target)
	}
	return "folder:", a.localDirArgs(rest)
}

func (a *App) sshHostArgs(prefix string) []wsArg {
	var out []wsArg
	for _, h := range workspace.FilterHosts(a.sshHostList(), prefix) {
		desc := h.HostName
		if h.User != "" && desc != "" {
			desc = h.User + "@" + desc
		}
		if h.Port != "" && desc != "" {
			desc += ":" + h.Port
		}
		out = append(out, wsArg{label: h.Alias, insert: "ssh " + h.Alias, desc: desc})
		if len(out) >= wsArgMaxItems {
			break
		}
	}
	return out
}

// splitDirPrefix splits "a/b/pre" into ("a/b/", "pre").
func splitDirPrefix(p string) (dir, prefix string) {
	if i := strings.LastIndex(p, "/"); i != -1 {
		return p[:i+1], p[i+1:]
	}
	return "", p
}

func matchDirNames(names []string, prefix, dirPart, insertPrefix string) []wsArg {
	sort.Strings(names)
	lp := strings.ToLower(prefix)
	var out []wsArg
	for _, n := range names {
		if strings.HasPrefix(n, ".") && !strings.HasPrefix(prefix, ".") {
			continue
		}
		if n == "." || n == ".." || !strings.HasPrefix(strings.ToLower(n), lp) {
			continue
		}
		out = append(out, wsArg{label: n + "/", insert: insertPrefix + dirPart + n + "/"})
		if len(out) >= wsArgMaxItems {
			break
		}
	}
	return out
}

// localDirArgs completes local folders for /add.
func (a *App) localDirArgs(arg string) []wsArg {
	arg = strings.TrimPrefix(arg, "\"")
	dirPart, prefix := splitDirPrefix(arg)
	search := expandHome(strings.TrimSuffix(dirPart, "/"))
	if dirPart == "/" {
		search = "/"
	}
	if dirPart == "~/" {
		search = expandHome("~")
	}
	if search == "" {
		search = "."
	}
	if !filepath.IsAbs(search) {
		base := a.workspaceRoot
		if base == "" {
			base, _ = os.Getwd()
		}
		search = filepath.Join(base, search)
	}
	entries, err := os.ReadDir(search)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
			continue
		}
		if e.Type()&os.ModeSymlink != 0 {
			if st, err := os.Stat(filepath.Join(search, e.Name())); err == nil && st.IsDir() {
				names = append(names, e.Name())
			}
		}
	}
	return matchDirNames(names, prefix, dirPart, "")
}

// remoteDirArgs completes remote folders for "/add ssh host:path". It
// only lists over an already-running ControlMaster (never prompts);
// results arrive asynchronously and refresh the hint line.
func (a *App) remoteDirArgs(host, p string) []wsArg {
	if workspace.ValidHostAlias(host) != nil {
		return nil
	}
	dirPart, prefix := splitDirPrefix(p)
	dir := strings.TrimSuffix(dirPart, "/")
	switch {
	case dirPart == "/":
		dir = "/"
	case dirPart == "" || dirPart == "~/":
		dir = "." // ssh starts in the remote $HOME
	case strings.HasPrefix(dirPart, "~/"):
		dir = strings.TrimSuffix(strings.TrimPrefix(dirPart, "~/"), "/")
	}
	key := host + "\x00" + path.Clean(dir)
	if e, ok := a.remoteDirs[key]; ok && time.Since(e.at) < remoteDirsTTL {
		return matchDirNames(append([]string(nil), e.dirs...), prefix, dirPart, "ssh "+host+":")
	}
	fs, ok := a.sshFS[host]
	if !ok || a.remoteDirsBusy[key] {
		return nil
	}
	a.remoteDirsBusy[key] = true
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		var dirs []string
		if fs.MasterUp(ctx) {
			dirs, _ = fs.ListDirs(ctx, dir)
		}
		a.postUI(func() {
			delete(a.remoteDirsBusy, key)
			a.remoteDirs[key] = remoteDirEntry{dirs: dirs, at: time.Now()}
			if a.hintMode == "wsargs" && a.input != nil {
				a.updateInputHint(a.input.GetText())
				a.refreshContextBar()
			}
		})
	}()
	return nil
}

func (a *App) workspaceNameArgs(prefix string, includePrimary bool, insertPrefix string) []wsArg {
	lp := strings.ToLower(strings.TrimSpace(prefix))
	active := a.workspaces.ActiveName()
	var out []wsArg
	for i, w := range a.workspaces.All() {
		if i == 0 && !includePrimary {
			continue
		}
		if !strings.HasPrefix(strings.ToLower(w.Name), lp) {
			continue
		}
		desc := w.Label()
		if strings.EqualFold(w.Name, active) {
			desc += " (active)"
		}
		out = append(out, wsArg{label: w.Name, insert: insertPrefix + w.Name, desc: desc})
	}
	return out
}

// setWorkspaceArgHint switches the hint line to the argument palette.
func (a *App) setWorkspaceArgHint(cmd, title string, items []wsArg) {
	changed := a.hintMode != "wsargs" || a.wsArgCmd != cmd || a.wsArgTitle != title || len(items) != len(a.wsArgs)
	a.hintMode = "wsargs"
	a.hintMatches = nil
	a.wsArgCmd, a.wsArgTitle, a.wsArgs = cmd, title, items
	if changed || a.wsArgSel >= len(items) {
		a.wsArgSel = 0
	}
}

// completeWorkspaceArg puts the highlighted suggestion into the input.
func (a *App) completeWorkspaceArg() {
	if len(a.wsArgs) == 0 || a.input == nil {
		return
	}
	a.input.SetText("/" + a.wsArgCmd + " " + a.wsArgs[a.wsArgSel].insert)
}

// renderWorkspaceArgPalette renders the argument hint line (same
// windowing and colours as the command palette).
func (a *App) renderWorkspaceArgPalette() string {
	var b strings.Builder
	b.WriteString(" ")
	b.WriteString(fmt.Sprintf("[%s]%s[-] ", a.palette.HexDim, tview.Escape(a.wsArgTitle)))
	if len(a.wsArgs) == 0 {
		msg := "no matches"
		switch {
		case strings.HasSuffix(a.wsArgTitle, ":") && a.wsArgCmd == "add" && a.wsArgTitle != "folder:" && a.wsArgTitle != "ssh host:":
			msg = "type a path (completes once connected)"
		case a.wsArgTitle == "ssh host:":
			msg = "type a host or user@host (no matching ~/.ssh/config host)"
		}
		b.WriteString(fmt.Sprintf("[%s]%s[-] ", a.palette.HexFaint, msg))
		return b.String()
	}
	labels := make([]string, len(a.wsArgs))
	for i, it := range a.wsArgs {
		labels[i] = it.label
	}
	const sep = "  "
	budget := a.paletteWidth() - 4 - len([]rune(a.wsArgTitle)) - 1
	if budget < 8 {
		budget = 8
	}
	start, end := a.wsArgSel, a.wsArgSel+1
	used := len([]rune(labels[a.wsArgSel]))
	for {
		grew := false
		if end < len(labels) {
			if cost := len(sep) + len([]rune(labels[end])); used+cost <= budget {
				used += cost
				end++
				grew = true
			}
		}
		if start > 0 {
			if cost := len(sep) + len([]rune(labels[start-1])); used+cost <= budget {
				used += cost
				start--
				grew = true
			}
		}
		if !grew {
			break
		}
	}
	if start > 0 {
		b.WriteString(fmt.Sprintf("[%s]‹[-] ", a.palette.HexPurple))
	}
	for i := start; i < end; i++ {
		if i > start {
			b.WriteString(sep)
		}
		if i == a.wsArgSel {
			b.WriteString(fmt.Sprintf("[%s::b]%s[-:-:-]", a.palette.HexLavender, tview.Escape(labels[i])))
		} else {
			b.WriteString(fmt.Sprintf("[%s]%s[-]", a.palette.HexPurple, tview.Escape(labels[i])))
		}
	}
	if end < len(labels) {
		b.WriteString(fmt.Sprintf(" [%s]›[-]", a.palette.HexPurple))
	}
	trailer := a.wsArgs[a.wsArgSel].desc
	if trailer == "" {
		trailer = "Tab complete · ⇧Tab next"
	}
	if used+len([]rune(trailer))+3 <= budget {
		b.WriteString(fmt.Sprintf("   [%s]%s[-]", a.palette.HexFaint, tview.Escape(trailer)))
	}
	b.WriteString(" ")
	return b.String()
}

// wsArgHint sets the argument palette for text when it applies.
func (a *App) wsArgHint(text string) bool {
	cmd, title, items, ok := a.workspaceArgHints(text)
	if !ok {
		return false
	}
	a.setWorkspaceArgHint(cmd, title, items)
	return true
}
