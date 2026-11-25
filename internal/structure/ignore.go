package structure

import (
	"bytes"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Rule is the ignore rule that hides a path: its pattern, the file it comes
// from (relative to the root, "global", or "info/exclude"), and its line.
type Rule struct {
	Rule   string `json:"rule"`
	Source string `json:"source,omitempty"`
	Line   int    `json:"line,omitempty"`
}

// ignorer answers whether a path, relative to the walked folder, is ignored.
type ignorer interface {
	dir(rel string) *Rule
	file(rel string) *Rule
}

// noIgnore ignores nothing: no git and no .gitignore.
type noIgnore struct{}

func (noIgnore) dir(string) *Rule  { return nil }
func (noIgnore) file(string) *Rule { return nil }

// pattern is one line of a .gitignore file.
type pattern struct {
	text    string
	line    int
	negate  bool
	dirOnly bool
	re      *regexp.Regexp
}

// parsePatterns reads gitignore lines: blank lines and # comments are
// skipped, ! negates, a trailing / matches folders only, and a leading or
// middle / anchors the pattern to the root. *, ?, ** and [...] are globs.
func parsePatterns(text string) []pattern {
	var out []pattern
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r")
		if !strings.HasSuffix(line, `\ `) {
			line = strings.TrimRight(line, " \t")
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if p, ok := compilePattern(line); ok {
			p.line = i + 1
			out = append(out, p)
		}
	}
	return out
}

func compilePattern(text string) (pattern, bool) {
	p := pattern{text: text}
	body := text
	if strings.HasPrefix(body, "!") {
		p.negate = true
		body = body[1:]
	} else if strings.HasPrefix(body, `\!`) || strings.HasPrefix(body, `\#`) {
		body = body[1:]
	}
	if strings.HasSuffix(body, "/") {
		p.dirOnly = true
		body = strings.TrimRight(body, "/")
	}
	anchored := strings.Contains(body, "/")
	body = strings.TrimPrefix(body, "/")
	if body == "" {
		return p, false
	}
	glob := globRegexp(body)
	if anchored {
		glob = "^" + glob + "$"
	} else {
		glob = "^(?:.*/)?" + glob + "$"
	}
	re, err := regexp.Compile(glob)
	if err != nil {
		return p, false
	}
	p.re = re
	return p, true
}

// globRegexp turns a gitignore glob into a regular expression body.
func globRegexp(glob string) string {
	var b strings.Builder
	for i := 0; i < len(glob); i++ {
		c := glob[i]
		switch {
		case strings.HasPrefix(glob[i:], "**/") && (i == 0 || glob[i-1] == '/'):
			b.WriteString("(?:.*/)?")
			i += 2
		case strings.HasPrefix(glob[i:], "**") && i+2 == len(glob) && i > 0 && glob[i-1] == '/':
			b.WriteString(".+")
			i++
		case strings.HasPrefix(glob[i:], "**"):
			b.WriteString(".*")
			i++
		case c == '*':
			b.WriteString("[^/]*")
		case c == '?':
			b.WriteString("[^/]")
		case c == '[':
			end := strings.IndexByte(glob[i+1:], ']')
			if end < 0 {
				b.WriteString(`\[`)
				continue
			}
			class := glob[i+1 : i+1+end]
			if strings.HasPrefix(class, "!") {
				class = "^" + class[1:]
			}
			b.WriteString("[" + strings.ReplaceAll(class, `\`, `\\`) + "]")
			i += end + 1
		case c == '\\' && i+1 < len(glob):
			i++
			b.WriteString(regexp.QuoteMeta(string(glob[i])))
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	return b.String()
}

// match returns the last pattern that matches rel, or nil when none does or
// the last one is a negation.
func match(patterns []pattern, rel string, isDir bool) *pattern {
	var found *pattern
	for i := range patterns {
		p := &patterns[i]
		if p.dirOnly && !isDir {
			continue
		}
		if p.re.MatchString(rel) {
			found = p
		}
	}
	if found == nil || found.negate {
		return nil
	}
	return found
}

// matchesAny reports whether any pattern matches rel or a folder above it.
// Exclude globs use it, so excluding a folder excludes what it holds.
func matchesAny(patterns []pattern, rel string, isDir bool) bool {
	for dir := parent(rel); dir != ""; dir = parent(dir) {
		if match(patterns, dir, true) != nil {
			return true
		}
	}
	return match(patterns, rel, isDir) != nil
}

// fileIgnore reads only the project root's .gitignore, for when git is
// missing or the folder isn't in a work tree. prefix is the walked folder
// relative to the project root.
type fileIgnore struct {
	patterns []pattern
	prefix   string
}

func (f fileIgnore) lookup(rel string, isDir bool) *Rule {
	full := join(f.prefix, rel)
	for dir := parent(full); dir != ""; dir = parent(dir) {
		if p := match(f.patterns, dir, true); p != nil {
			return &Rule{Rule: p.text, Source: ".gitignore", Line: p.line}
		}
	}
	if p := match(f.patterns, full, isDir); p != nil {
		return &Rule{Rule: p.text, Source: ".gitignore", Line: p.line}
	}
	return nil
}

func (f fileIgnore) dir(rel string) *Rule  { return f.lookup(rel, true) }
func (f fileIgnore) file(rel string) *Rule { return f.lookup(rel, false) }

// gitIgnore holds what git reports as ignored under the walked folder.
type gitIgnore struct {
	dirs, files map[string]*Rule
}

func (g gitIgnore) lookup(rel string) *Rule {
	for dir := parent(rel); dir != ""; dir = parent(dir) {
		if r := g.dirs[dir]; r != nil {
			return r
		}
	}
	return nil
}

func (g gitIgnore) dir(rel string) *Rule {
	if r := g.dirs[rel]; r != nil {
		return r
	}
	return g.lookup(rel)
}

func (g gitIgnore) file(rel string) *Rule {
	if r := g.files[rel]; r != nil {
		return r
	}
	return g.lookup(rel)
}

// gitCommand runs git in dir with input on stdin. A missing git is an error.
var gitCommand = func(dir string, input []byte, args ...string) ([]byte, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...) // #nosec G204 -- fixed git subcommands
	if input != nil {
		cmd.Stdin = bytes.NewReader(input)
	}
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	return cmd.Output()
}

// newGitIgnore asks git which paths under walkRoot it ignores, and why.
// It reports false when git is missing or walkRoot isn't in a work tree.
func newGitIgnore(walkRoot, projectRoot string) (gitIgnore, bool) {
	out, err := gitCommand(walkRoot, nil, "rev-parse", "--is-inside-work-tree", "--show-toplevel", "--absolute-git-dir")
	if err != nil {
		return gitIgnore{}, false
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 3 || strings.TrimSpace(lines[0]) != "true" {
		return gitIgnore{}, false
	}
	top, gitDir := resolved(strings.TrimSpace(lines[1])), resolved(strings.TrimSpace(lines[2]))
	listed, err := gitCommand(walkRoot, nil, "ls-files", "--others", "--ignored", "--exclude-standard", "--directory", "-z")
	if err != nil {
		return gitIgnore{}, false
	}
	g := gitIgnore{dirs: map[string]*Rule{}, files: map[string]*Rule{}}
	entries := splitZ(listed)
	rules := checkIgnore(walkRoot, entries, top, gitDir, projectRoot)
	var unmatched []string
	for _, e := range entries {
		rel := strings.TrimSuffix(e, "/")
		if rel == "" || rel == "." {
			continue
		}
		r := rules[e]
		switch {
		case r != nil && strings.HasSuffix(e, "/"):
			g.dirs[rel] = r
		case r != nil:
			g.files[rel] = r
		case strings.HasSuffix(e, "/"):
			// git lists a folder whose untracked files are all ignored by
			// rules on the files, not the folder. Newer git also lists the
			// files; older git needs asking for them.
			if !hasDescendant(entries, e) {
				unmatched = append(unmatched, rel)
			}
		}
	}
	if len(unmatched) > 0 {
		more, err := gitCommand(walkRoot, nil, append([]string{"ls-files", "--others", "--ignored", "--exclude-standard", "-z", "--"}, unmatched...)...)
		if err == nil {
			files := splitZ(more)
			for p, r := range checkIgnore(walkRoot, files, top, gitDir, projectRoot) {
				if !strings.HasSuffix(p, "/") {
					g.files[p] = r
				}
			}
		}
	}
	return g, true
}

func hasDescendant(entries []string, dir string) bool {
	for _, e := range entries {
		if e != dir && strings.HasPrefix(e, dir) {
			return true
		}
	}
	return false
}

func splitZ(b []byte) []string {
	var out []string
	for _, s := range strings.Split(string(b), "\x00") {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// checkIgnore asks git check-ignore for each path's rule. Negated rules
// don't count.
func checkIgnore(walkRoot string, paths []string, top, gitDir, projectRoot string) map[string]*Rule {
	rules := map[string]*Rule{}
	if len(paths) == 0 {
		return rules
	}
	input := []byte(strings.Join(paths, "\x00") + "\x00")
	// check-ignore exits 1 when nothing matched; its output still counts.
	out, _ := gitCommand(walkRoot, input, "check-ignore", "-v", "-z", "--stdin")
	fields := strings.Split(string(out), "\x00")
	for i := 0; i+3 < len(fields); i += 4 {
		source, line, rule, p := fields[i], fields[i+1], fields[i+2], fields[i+3]
		if rule == "" || strings.HasPrefix(rule, "!") {
			continue
		}
		n, _ := strconv.Atoi(line)
		rules[p] = &Rule{Rule: rule, Source: ruleSource(source, top, gitDir, walkRoot, projectRoot), Line: n}
	}
	return rules
}

// ruleSource names a rule's file without an absolute path: relative to the
// walked folder, the project, or the work tree, whichever holds it first;
// "info/exclude" for the repository's own excludes; "global" for any file
// outside the work tree.
func ruleSource(source, top, gitDir, walkRoot, projectRoot string) string {
	if strings.HasPrefix(source, "/") && !filepath.IsAbs(source) {
		// A POSIX path on Windows, such as a global excludes file git
		// reports from an MSYS home. It is outside the work tree.
		return "global"
	}
	abs := source
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(top, filepath.FromSlash(source))
	}
	abs = resolved(abs)
	if abs == filepath.Join(gitDir, "info", "exclude") {
		return "info/exclude"
	}
	for _, base := range []string{walkRoot, projectRoot, top} {
		if rel, ok := inside(base, abs); ok {
			if strings.HasPrefix(rel, ".git/") || rel == ".git" {
				return "info/exclude"
			}
			return rel
		}
	}
	return "global"
}

// inside returns p relative to base when p is inside it.
func inside(base, p string) (string, bool) {
	rel, err := filepath.Rel(base, p)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// resolved is p with symbolic links resolved, so macOS's /var and
// /private/var compare equal.
func resolved(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	// A file that doesn't exist: resolve its folder.
	if r, err := filepath.EvalSymlinks(filepath.Dir(p)); err == nil {
		return filepath.Join(r, filepath.Base(p))
	}
	return filepath.Clean(p)
}

func parent(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[:i]
	}
	return ""
}

func join(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	}
	return path.Join(a, b)
}
