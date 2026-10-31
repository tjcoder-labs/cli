package tui

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"html"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// ---- "@" file mention picker + attachments ----
//
// Typing "@" in the chat input opens an inline completion palette of
// workspace files (fileIndex + filterFileMatches). Every "@path" token that
// names a regular file under the workspace when the message is sent is
// attached: its contents are appended to the user message in an
// <attached_files> block (persisted in history so follow-up turns can still
// see the file), while the transcript shows a compact 📎 chip instead.

const (
	// attachMaxFileBytes caps the contents attached for one file.
	attachMaxFileBytes = 200 * 1024
	// attachMaxTotalBytes caps the contents attached for one message.
	attachMaxTotalBytes = 500 * 1024
	// attachMaxFiles caps how many files a single message may attach.
	attachMaxFiles = 20
	// fileIndexMaxFiles caps the picker's workspace index.
	fileIndexMaxFiles = 20000
	// fileIndexMaxSize skips very large files from the picker listing.
	fileIndexMaxSize = 20 * 1024 * 1024
	// fileMatchCap caps the number of picker results.
	fileMatchCap = 50
	// fileIndexTTL is how long a built index is reused before rescanning.
	fileIndexTTL = 15 * time.Second
)

// skippedDirs are never listed, whether files come from git or the walk.
var skippedDirs = map[string]bool{
	".git": true, ".hg": true, ".svn": true, "node_modules": true,
	".ergo-cli-go": true,
}

// walkSkippedDirs are additionally pruned by the fallback walker (no git):
// typical build/dependency output that a .gitignore would normally cover.
var walkSkippedDirs = map[string]bool{
	"vendor": true, "dist": true, "build": true, "target": true,
	".venv": true, "venv": true, "__pycache__": true, ".next": true,
	".cache": true, ".idea": true, ".gradle": true, "obj": true,
	"coverage": true,
}

// binaryExts are listed nowhere in the picker; when one is still
// mentioned by hand it is attached as metadata only.
var binaryExts = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true,
	".ico": true, ".bmp": true, ".tiff": true, ".pdf": true, ".zip": true,
	".gz": true, ".tgz": true, ".bz2": true, ".xz": true, ".7z": true,
	".rar": true, ".tar": true, ".exe": true, ".dll": true, ".so": true,
	".dylib": true, ".a": true, ".o": true, ".class": true, ".jar": true,
	".wasm": true, ".bin": true, ".dat": true, ".db": true, ".sqlite": true,
	".mp3": true, ".mp4": true, ".mov": true, ".avi": true, ".wav": true,
	".flac": true, ".ogg": true, ".woff": true, ".woff2": true, ".ttf": true,
	".otf": true, ".eot": true, ".pyc": true, ".ds_store": true,
}

// skipIndexedPath reports whether a slash-separated relative path should
// be hidden from the picker (inside a skipped dir, or a binary extension).
func skipIndexedPath(rel string) bool {
	parts := strings.Split(rel, "/")
	for _, p := range parts[:len(parts)-1] {
		if skippedDirs[p] {
			return true
		}
	}
	return binaryExts[strings.ToLower(path.Ext(rel))]
}

// gitignoreMatcher is a small, dependency-free subset of .gitignore
// semantics used only when `git ls-files` is unavailable: comments, blank
// lines, negation (!), directory-only patterns (trailing /), anchored
// patterns (leading or inner /), and shell globs via path.Match. "**" is
// treated as "any number of path segments".
type gitignoreMatcher struct {
	rules []gitignoreRule
}

type gitignoreRule struct {
	pattern  string
	negate   bool
	dirOnly  bool
	anchored bool
}

func parseGitignore(text string) *gitignoreMatcher {
	m := &gitignoreMatcher{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimRight(line, " ")
		r := gitignoreRule{}
		if strings.HasPrefix(line, "!") {
			r.negate = true
			line = line[1:]
		}
		if strings.HasSuffix(line, "/") {
			r.dirOnly = true
			line = strings.TrimSuffix(line, "/")
		}
		if strings.HasPrefix(line, "/") {
			r.anchored = true
			line = strings.TrimPrefix(line, "/")
		} else if strings.Contains(line, "/") {
			r.anchored = true
		}
		if line == "" {
			continue
		}
		r.pattern = line
		m.rules = append(m.rules, r)
	}
	return m
}

// Ignored reports whether rel (slash-separated, relative to the repo root)
// is ignored. isDir says whether rel itself is a directory. Parent
// directories are checked too, so a file under an ignored dir is ignored.
func (m *gitignoreMatcher) Ignored(rel string, isDir bool) bool {
	if m == nil || len(m.rules) == 0 {
		return false
	}
	parts := strings.Split(rel, "/")
	for i := 1; i <= len(parts); i++ {
		sub := strings.Join(parts[:i], "/")
		dir := i < len(parts) || isDir
		if m.match(sub, dir) {
			return true
		}
	}
	return false
}

func (m *gitignoreMatcher) match(rel string, isDir bool) bool {
	ignored := false
	base := path.Base(rel)
	for _, r := range m.rules {
		if r.dirOnly && !isDir {
			continue
		}
		var ok bool
		if r.anchored {
			ok = globMatch(r.pattern, rel)
		} else {
			ok = globMatch(r.pattern, base)
		}
		if ok {
			ignored = !r.negate
		}
	}
	return ignored
}

// globMatch extends path.Match with "**" spanning any number of segments.
func globMatch(pattern, name string) bool {
	if !strings.Contains(pattern, "**") {
		ok, _ := path.Match(pattern, name)
		return ok
	}
	pp := strings.Split(pattern, "/")
	np := strings.Split(name, "/")
	var rec func(i, j int) bool
	rec = func(i, j int) bool {
		if i == len(pp) {
			return j == len(np)
		}
		if pp[i] == "**" {
			for k := j; k <= len(np); k++ {
				if rec(i+1, k) {
					return true
				}
			}
			return false
		}
		if j == len(np) {
			return false
		}
		if ok, _ := path.Match(pp[i], np[j]); !ok {
			return false
		}
		return rec(i+1, j+1)
	}
	return rec(0, 0)
}

// listWorkspaceFiles returns slash-separated relative paths of files under
// root suitable for the picker: `git ls-files` (tracked + untracked, minus
// .gitignore'd) when root is in a git work tree, otherwise a bounded walk
// honoring the root .gitignore. Both paths drop skipped dirs and binaries.
func listWorkspaceFiles(root string) []string {
	if files, ok := gitListFiles(root); ok {
		return capAndFilterIndex(files)
	}
	return walkWorkspaceFiles(root)
}

func gitListFiles(root string) ([]string, bool) {
	if _, err := exec.LookPath("git"); err != nil {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", root, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	out, err := cmd.Output()
	if err != nil {
		return nil, false
	}
	var files []string
	for _, f := range bytes.Split(out, []byte{0}) {
		if len(f) > 0 {
			files = append(files, string(f))
		}
	}
	return files, true
}

func capAndFilterIndex(files []string) []string {
	out := make([]string, 0, len(files))
	seen := map[string]bool{}
	for _, f := range files {
		f = filepath.ToSlash(f)
		if seen[f] || skipIndexedPath(f) {
			continue
		}
		seen[f] = true
		out = append(out, f)
		if len(out) >= fileIndexMaxFiles {
			break
		}
	}
	sort.Strings(out)
	return out
}

func walkWorkspaceFiles(root string) []string {
	var ign *gitignoreMatcher
	if data, err := os.ReadFile(filepath.Join(root, ".gitignore")); err == nil {
		ign = parseGitignore(string(data))
	}
	var out []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if p == root {
			return nil
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if skippedDirs[d.Name()] || walkSkippedDirs[d.Name()] || ign.Ignored(rel, true) {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || ign.Ignored(rel, false) || skipIndexedPath(rel) {
			return nil
		}
		if info, ierr := d.Info(); ierr == nil && info.Size() > fileIndexMaxSize {
			return nil
		}
		out = append(out, rel)
		if len(out) >= fileIndexMaxFiles {
			return fs.SkipAll
		}
		return nil
	})
	sort.Strings(out)
	return out
}

// filterFileMatches ranks files against query (case-insensitive) and
// returns at most limit results. Ranking: basename prefix, basename
// substring, path substring, then fuzzy subsequence; shorter paths first
// within a tier. An empty query returns the first limit files.
func filterFileMatches(files []string, query string, limit int) []string {
	if limit <= 0 {
		limit = fileMatchCap
	}
	q := strings.ToLower(strings.TrimSpace(query))
	type scored struct {
		path string
		tier int
	}
	var hits []scored
	for _, f := range files {
		lf := strings.ToLower(f)
		base := path.Base(lf)
		tier := -1
		switch {
		case q == "":
			tier = 0
		case strings.HasPrefix(base, q):
			tier = 0
		case strings.Contains(base, q):
			tier = 1
		case strings.Contains(lf, q):
			tier = 2
		case isSubsequence(q, lf):
			tier = 3
		}
		if tier >= 0 {
			hits = append(hits, scored{f, tier})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].tier != hits[j].tier {
			return hits[i].tier < hits[j].tier
		}
		if len(hits[i].path) != len(hits[j].path) {
			return len(hits[i].path) < len(hits[j].path)
		}
		return hits[i].path < hits[j].path
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.path
	}
	return out
}

func isSubsequence(needle, hay string) bool {
	if needle == "" {
		return true
	}
	nr := []rune(needle)
	i := 0
	for _, r := range hay {
		if r == nr[i] {
			i++
			if i == len(nr) {
				return true
			}
		}
	}
	return false
}

// activeMentionToken returns the "@query" token being typed at the end of
// text (query excludes the "@"), and whether one is active. A mention must
// start the text or follow whitespace; a trailing space ends it.
func activeMentionToken(text string) (string, bool) {
	if text == "" || strings.HasSuffix(text, " ") || strings.HasSuffix(text, "\t") || strings.HasSuffix(text, "\n") {
		return "", false
	}
	i := strings.LastIndexAny(text, " \t\n")
	tok := text[i+1:]
	if !strings.HasPrefix(tok, "@") {
		return "", false
	}
	return tok[1:], true
}

// replaceMentionToken swaps the trailing "@query" token for "@path ".
func replaceMentionToken(text, rel string) string {
	i := strings.LastIndexAny(text, " \t\n")
	return text[:i+1] + "@" + rel + " "
}

// mentionRe finds "@path" mentions (start of text or after whitespace).
var mentionRe = regexp.MustCompile(`(?:^|\s)@([^\s@]+)`)

// mentionedPaths returns the unique "@path" mentions in prompt, in order,
// with trailing sentence punctuation trimmed.
func mentionedPaths(prompt string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range mentionRe.FindAllStringSubmatch(prompt, -1) {
		p := strings.TrimRight(m[1], ".,;:!?)]}\"'`")
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

// fileAttachment is one file attached to a user message.
type fileAttachment struct {
	Path      string // slash-separated, relative to the workspace root
	Size      int64  // full file size in bytes
	Lines     int    // full line count (0 for binary)
	Mime      string
	Lang      string
	Binary    bool
	Truncated bool
	Omitted   bool   // contents dropped because the total cap was reached
	Content   string // attached (possibly truncated) text; "" for binary
}

var langByExt = map[string]string{
	".go": "go", ".ts": "typescript", ".tsx": "tsx", ".js": "javascript",
	".jsx": "jsx", ".mjs": "javascript", ".cjs": "javascript", ".py": "python",
	".rb": "ruby", ".rs": "rust", ".java": "java", ".kt": "kotlin",
	".swift": "swift", ".c": "c", ".h": "c", ".cc": "cpp", ".cpp": "cpp",
	".hpp": "cpp", ".cs": "csharp", ".php": "php", ".sh": "bash",
	".bash": "bash", ".zsh": "zsh", ".md": "markdown", ".markdown": "markdown",
	".json": "json", ".yaml": "yaml", ".yml": "yaml", ".toml": "toml",
	".sql": "sql", ".html": "html", ".css": "css", ".scss": "scss",
	".xml": "xml", ".txt": "text", ".mod": "go-mod", ".sum": "text",
	".dockerfile": "dockerfile", ".proto": "protobuf", ".lua": "lua",
}

func fileLang(rel string) string {
	base := strings.ToLower(path.Base(rel))
	switch base {
	case "dockerfile":
		return "dockerfile"
	case "makefile":
		return "makefile"
	}
	return langByExt[strings.ToLower(path.Ext(rel))]
}

// resolveAttachmentPath maps a mention to an absolute path inside root,
// rejecting anything that escapes it. ok is false when the mention is not
// an existing regular file.
func resolveAttachmentPath(root, mention string) (abs, rel string, ok bool) {
	if root == "" || mention == "" {
		return "", "", false
	}
	p := mention
	if !filepath.IsAbs(p) {
		p = filepath.Join(root, filepath.FromSlash(p))
	}
	p = filepath.Clean(p)
	r, err := filepath.Rel(root, p)
	if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return "", "", false
	}
	info, err := os.Stat(p)
	if err != nil || !info.Mode().IsRegular() {
		return "", "", false
	}
	// A symlink inside the workspace must not smuggle in a file from
	// outside it.
	if realRoot, err := filepath.EvalSymlinks(root); err == nil {
		if realP, err := filepath.EvalSymlinks(p); err == nil {
			rr, err := filepath.Rel(realRoot, realP)
			if err != nil || rr == ".." || strings.HasPrefix(rr, ".."+string(filepath.Separator)) {
				return "", "", false
			}
		}
	}
	return p, filepath.ToSlash(r), true
}

// loadAttachments reads every mentioned file under root, applying the
// per-file and total caps. Binary files carry metadata only.
func loadAttachments(root string, mentions []string, perFile, total int) []fileAttachment {
	return loadAttachmentsMulti(root, nil, mentions, perFile, total)
}

// loadAttachmentsMulti is loadAttachments across workspaces: a mention
// "name:rel/path" whose name is an added local workspace (extra maps
// lower-cased names to roots) resolves inside that root, confined the
// same way, and keeps the "name:" prefix in the attachment path.
func loadAttachmentsMulti(root string, extra map[string]string, mentions []string, perFile, total int) []fileAttachment {
	var out []fileAttachment
	remaining := total
	seen := map[string]bool{}
	for _, m := range mentions {
		if len(out) >= attachMaxFiles {
			break
		}
		base, mention, prefix := root, m, ""
		if name, rest, ok := strings.Cut(m, ":"); ok && rest != "" {
			if r, found := extra[strings.ToLower(name)]; found {
				base, mention, prefix = r, rest, name+":"
			}
		}
		abs, rel, ok := resolveAttachmentPath(base, mention)
		rel = prefix + rel
		if !ok || seen[rel] {
			continue
		}
		seen[rel] = true
		att, err := readAttachment(abs, rel, perFile, remaining)
		if err != nil {
			continue
		}
		remaining -= len(att.Content)
		out = append(out, att)
	}
	return out
}

func readAttachment(abs, rel string, perFile, remaining int) (fileAttachment, error) {
	att := fileAttachment{Path: rel, Lang: fileLang(rel)}
	f, err := os.Open(abs)
	if err != nil {
		return att, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return att, err
	}
	att.Size = info.Size()

	head := make([]byte, 8192)
	n, _ := io.ReadFull(f, head)
	head = head[:n]
	att.Mime = mime.TypeByExtension(path.Ext(rel))
	if att.Mime == "" {
		att.Mime = http.DetectContentType(head)
	}
	if binaryExts[strings.ToLower(path.Ext(rel))] || looksBinary(head) {
		att.Binary = true
		return att, nil
	}

	// Full line count (streamed; cheap even for large text files).
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return att, err
	}
	att.Lines = countLines(f)

	limit := perFile
	if remaining < limit {
		limit = remaining
	}
	if limit <= 0 {
		att.Omitted = true
		return att, nil
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return att, err
	}
	buf, err := io.ReadAll(io.LimitReader(f, int64(limit)))
	if err != nil {
		return att, err
	}
	if int64(len(buf)) < att.Size {
		att.Truncated = true
		buf = trimPartialRune(buf)
	}
	att.Content = string(buf)
	return att, nil
}

// trimPartialRune drops an incomplete UTF-8 sequence (at most 3 bytes)
// left at the end of buf by a byte-limit cut.
func trimPartialRune(buf []byte) []byte {
	for i := 1; i <= 3 && i <= len(buf); i++ {
		b := buf[len(buf)-i]
		if b < 0x80 {
			return buf // ASCII: no partial sequence
		}
		if utf8.RuneStart(b) {
			if !utf8.FullRune(buf[len(buf)-i:]) {
				return buf[:len(buf)-i]
			}
			return buf
		}
	}
	return buf
}

func looksBinary(head []byte) bool {
	if bytes.IndexByte(head, 0) >= 0 {
		return true
	}
	// Allow a multi-byte rune cut at the 8 KiB boundary.
	trimmed := head
	for i := 0; i < 3 && len(trimmed) > 0 && !utf8.Valid(trimmed); i++ {
		trimmed = trimmed[:len(trimmed)-1]
	}
	return !utf8.Valid(trimmed)
}

func countLines(r io.Reader) int {
	br := bufio.NewReaderSize(r, 64*1024)
	lines, last := 0, byte('\n')
	buf := make([]byte, 64*1024)
	for {
		n, err := br.Read(buf)
		if n > 0 {
			lines += bytes.Count(buf[:n], []byte{'\n'})
			last = buf[n-1]
		}
		if err != nil {
			break
		}
	}
	if last != '\n' {
		lines++
	}
	return lines
}

const (
	attachBlockOpen  = "<attached_files>"
	attachBlockClose = "</attached_files>"
)

// formatAttachmentsBlock renders atts as the delimited block appended to
// the user message content (and persisted with it in history).
func formatAttachmentsBlock(atts []fileAttachment) string {
	if len(atts) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(attachBlockOpen + "\n")
	for _, a := range atts {
		fmt.Fprintf(&b, "<file path=\"%s\" size=\"%d\"", html.EscapeString(a.Path), a.Size)
		if !a.Binary {
			fmt.Fprintf(&b, " lines=\"%d\"", a.Lines)
		}
		if a.Mime != "" {
			fmt.Fprintf(&b, " mime=\"%s\"", html.EscapeString(a.Mime))
		}
		if a.Lang != "" {
			fmt.Fprintf(&b, " lang=\"%s\"", a.Lang)
		}
		switch {
		case a.Binary:
			b.WriteString(" binary=\"true\">\n[binary file: contents not attached]\n</file>\n")
			continue
		case a.Omitted:
			b.WriteString(" omitted=\"true\">\n[contents omitted: total attachment limit reached]\n</file>\n")
			continue
		case a.Truncated:
			b.WriteString(" truncated=\"true\"")
		}
		b.WriteString(">\n")
		b.WriteString(a.Content)
		if !strings.HasSuffix(a.Content, "\n") {
			b.WriteString("\n")
		}
		if a.Truncated {
			fmt.Fprintf(&b, "[truncated: showing first %d of %d bytes]\n", len(a.Content), a.Size)
		}
		b.WriteString("</file>\n")
	}
	b.WriteString(attachBlockClose)
	return b.String()
}

// attachmentChip is the display summary of one attached file.
type attachmentChip struct {
	Path   string
	Lines  int
	Size   int64
	Binary bool
}

var fileTagRe = regexp.MustCompile(`(?m)^<file path="([^"]*)"([^>\n]*)>$`)
var attrRe = regexp.MustCompile(`(\w+)="([^"]*)"`)

// splitAttachments separates the user's typed text from a trailing
// <attached_files> block and returns chips describing the attachments.
// Content without a block is returned unchanged with no chips.
func splitAttachments(content string) (string, []attachmentChip) {
	start := strings.Index(content, "\n\n"+attachBlockOpen+"\n")
	skip := 2
	if start < 0 {
		if strings.HasPrefix(content, attachBlockOpen+"\n") {
			start, skip = 0, 0
		} else {
			return content, nil
		}
	}
	end := strings.LastIndex(content, attachBlockClose)
	if end < start {
		return content, nil
	}
	block := content[start+skip : end]
	var chips []attachmentChip
	for _, m := range fileTagRe.FindAllStringSubmatch(block, -1) {
		c := attachmentChip{Path: html.UnescapeString(m[1])}
		for _, kv := range attrRe.FindAllStringSubmatch(m[2], -1) {
			switch kv[1] {
			case "lines":
				c.Lines, _ = strconv.Atoi(kv[2])
			case "size":
				c.Size, _ = strconv.ParseInt(kv[2], 10, 64)
			case "binary":
				c.Binary = kv[2] == "true"
			}
		}
		chips = append(chips, c)
	}
	text := content[:start] + content[end+len(attachBlockClose):]
	return strings.TrimRight(text, "\n"), chips
}

// humanBytes formats n as B / KB / MB.
func humanBytes(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%dB", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1fKB", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1fMB", float64(n)/(1024*1024))
	}
}
