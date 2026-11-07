// Package adopt maps an existing project's folders to Framework directory
// keys, as R's and Python's adopt() and structure_import() do. It looks at
// folder names only, never file names or contents. The rules and the
// algorithm are shared: rules.json is copied from the R package, and the
// conformance cases in internal/spec/testdata/conformance/adopt check them.
package adopt

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

//go:embed rules.json
var rulesJSON []byte

// Rules is rules.json.
type Rules struct {
	MaxDepth int      `json:"max_depth"`
	Skip     []string `json:"skip"`
	Rules    []Rule   `json:"rules"`
}

// Rule gives a key to the first folder that equals one of its patterns.
type Rule struct {
	Key       string   `json:"key"`
	Patterns  []string `json:"patterns"`
	Languages []string `json:"languages,omitempty"`
}

// applies reports whether the rule is for language. A rule without
// languages is for every language.
func (r Rule) applies(language string) bool {
	if len(r.Languages) == 0 {
		return true
	}
	for _, l := range r.Languages {
		if l == language {
			return true
		}
	}
	return false
}

// Shared returns the rules fw ships with.
func Shared() Rules {
	var r Rules
	if err := json.Unmarshal(rulesJSON, &r); err != nil {
		panic(fmt.Sprintf("adopt: rules.json: %v", err))
	}
	return r
}

// Extra is a folder no rule matched.
type Extra struct {
	Label string `json:"label"`
	Path  string `json:"path"`
}

// Warning flags a folder, such as one whose path contains "private".
type Warning struct {
	Code string `json:"code"`
	Path string `json:"path"`
}

// Text is the warning as the R package words it.
func (w Warning) Text() string {
	return fmt.Sprintf("'%s' looks private. Keep it out of git and away from AI assistants.", w.Path)
}

// Mapping is the proposed layout of a project.
type Mapping struct {
	Directories      map[string]string `json:"directories"`
	ExtraDirectories map[string]Extra  `json:"extra_directories"`
	Warnings         []Warning         `json:"warnings"`
}

// Empty reports whether no folder was mapped.
func (m Mapping) Empty() bool {
	return len(m.Directories)+len(m.ExtraDirectories) == 0
}

func (r Rules) skipped(segment string) bool {
	if strings.HasPrefix(segment, ".") {
		return true
	}
	for _, s := range r.Skip {
		if strings.EqualFold(s, segment) {
			return true
		}
	}
	return false
}

// Scan lists the folders under root as paths relative to it, with forward
// slashes, at most MaxDepth levels deep, without entering skipped or hidden
// folders or following links.
func (r Rules) Scan(root string) ([]string, error) {
	var found []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == root {
				return err
			}
			return nil // an unreadable folder is left out
		}
		if p == root || !d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if r.skipped(d.Name()) || strings.Count(rel, "/")+1 > r.MaxDepth {
			return filepath.SkipDir
		}
		found = append(found, rel)
		return nil
	})
	sort.Strings(found)
	return found, err
}

var notKeyChars = regexp.MustCompile(`[^a-z0-9]+`)

// extraKey turns a folder path into a directory key.
func extraKey(p string) string {
	key := strings.Trim(notKeyChars.ReplaceAllString(strings.ToLower(p), "_"), "_")
	if key == "" || key[0] < 'a' || key[0] > 'z' {
		key = "dir_" + key
	}
	return key
}

func parent(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[:i]
	}
	return ""
}

// Map proposes directory keys for folders in a project of the given
// language, following the shared algorithm.
func (r Rules) Map(folders []string, language string) Mapping {
	// 1. Keep folders within the depth without skipped or hidden segments.
	seen := map[string]bool{}
	var kept []string
	for _, f := range folders {
		f = strings.TrimRight(f, "/")
		if f == "" || seen[f] {
			continue
		}
		seen[f] = true
		parts := strings.Split(f, "/")
		ok := len(parts) <= r.MaxDepth
		for _, s := range parts {
			ok = ok && !r.skipped(s)
		}
		if ok {
			kept = append(kept, f)
		}
	}
	sort.Strings(kept)

	// 2. Each rule's first matching pattern gives an unassigned folder its key.
	m := Mapping{Directories: map[string]string{}, ExtraDirectories: map[string]Extra{}, Warnings: []Warning{}}
	assigned := map[string]bool{}
	taken := map[string]bool{}
	for _, rule := range r.Rules {
		if !rule.applies(language) {
			continue
		}
		taken[rule.Key] = true
		if _, done := m.Directories[rule.Key]; done {
			continue
		}
	patterns:
		for _, pattern := range rule.Patterns {
			for _, f := range kept {
				if !assigned[f] && strings.EqualFold(f, pattern) {
					m.Directories[rule.Key] = f
					assigned[f] = true
					break patterns
				}
			}
		}
	}

	// 3 and 4. Folders inside an assigned folder are covered. Of the rest,
	// one with no assigned folder inside it is an extra directory unless
	// its parent is one too.
	within := func(f, root string) bool { return strings.HasPrefix(f, root+"/") }
	pure := map[string]bool{}
	var pureList []string
	for _, f := range kept {
		if assigned[f] {
			continue
		}
		covered, holds := false, false
		for a := range assigned {
			covered = covered || within(f, a)
			holds = holds || within(a, f)
		}
		if !covered && !holds {
			pure[f] = true
			pureList = append(pureList, f)
		}
	}
	// 5. Keys from paths, numbered when taken.
	for _, f := range pureList {
		if pure[parent(f)] {
			continue
		}
		base := extraKey(f)
		key := base
		for i := 2; taken[key]; i++ {
			key = fmt.Sprintf("%s_%d", base, i)
		}
		taken[key] = true
		m.ExtraDirectories[key] = Extra{Label: path.Base(f), Path: f}
	}

	for _, f := range kept {
		if strings.Contains(strings.ToLower(f), "private") {
			m.Warnings = append(m.Warnings, Warning{Code: "private_path", Path: f})
		}
	}
	return m
}

// Rows lists the mapping for review: Framework keys in rule order, then the
// extra directories by path.
func (r Rules) Rows(m Mapping) [][2]string {
	var rows [][2]string
	done := map[string]bool{}
	for _, rule := range r.Rules {
		if p, ok := m.Directories[rule.Key]; ok && !done[rule.Key] {
			rows = append(rows, [2]string{p, rule.Key})
			done[rule.Key] = true
		}
	}
	extras := make([]Extra, 0, len(m.ExtraDirectories))
	for _, e := range m.ExtraDirectories {
		extras = append(extras, e)
	}
	sort.Slice(extras, func(i, j int) bool { return extras[i].Path < extras[j].Path })
	for _, e := range extras {
		rows = append(rows, [2]string{e.Path, "(other folder)"})
	}
	return rows
}

// SetRole gives the folder at p a new role: a Framework key, "other" for an
// extra directory, or "leave" to leave it out.
func (r Rules) SetRole(m Mapping, p, role string) Mapping {
	for k, v := range m.Directories {
		if v == p {
			delete(m.Directories, k)
		}
	}
	for k, e := range m.ExtraDirectories {
		if e.Path == p {
			delete(m.ExtraDirectories, k)
		}
	}
	switch role {
	case "leave":
	case "other":
		base := extraKey(p)
		key := base
		for i := 2; ; i++ {
			_, extra := m.ExtraDirectories[key]
			_, dir := m.Directories[key]
			if !extra && !dir {
				break
			}
			key = fmt.Sprintf("%s_%d", base, i)
		}
		m.ExtraDirectories[key] = Extra{Label: path.Base(p), Path: p}
	default:
		m.Directories[role] = p
	}
	return m
}

// Keys lists the Framework keys a project of the language can use.
func (r Rules) Keys(language string) []string {
	var keys []string
	for _, rule := range r.Rules {
		if rule.applies(language) {
			keys = append(keys, rule.Key)
		}
	}
	return keys
}

// DetectLanguage guesses a project's language from the files at its top
// level and returns it with the file that decided it, or "" when it cannot
// tell. Project files (pyproject.toml; renv.lock, DESCRIPTION, or an .Rproj;
// .do files) decide first. Without them, weaker signs do: setup.py,
// setup.cfg, requirements*.txt, or notebooks for Python; .R or .Rmd files
// for R; and a .qmd file, whose code chunks say which. Two languages at the
// same strength are a tie, and fw cannot tell.
func DetectLanguage(root string) (language, source string) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", ""
	}
	strong := map[string]string{}
	weak := map[string]string{}
	note := func(m map[string]string, lang, name string) {
		if _, ok := m[lang]; !ok {
			m[lang] = name
		}
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		lower := strings.ToLower(name)
		switch {
		case name == "pyproject.toml":
			note(strong, "python", name)
		case name == "renv.lock" || name == "DESCRIPTION" || strings.HasSuffix(name, ".Rproj"):
			note(strong, "r", name)
		case strings.HasSuffix(lower, ".do"):
			note(strong, "stata", name)
		case name == "setup.py" || name == "setup.cfg" || strings.HasSuffix(lower, ".ipynb") ||
			strings.HasPrefix(lower, "requirements") && strings.HasSuffix(lower, ".txt"):
			note(weak, "python", name)
		case strings.HasSuffix(lower, ".r") || strings.HasSuffix(lower, ".rmd"):
			note(weak, "r", name)
		case strings.HasSuffix(lower, ".qmd"):
			note(weak, qmdLanguage(filepath.Join(root, name)), name)
		}
	}
	for _, found := range []map[string]string{strong, weak} {
		switch len(found) {
		case 0:
			continue
		case 1:
			for l, src := range found {
				return l, src
			}
		}
		return "", ""
	}
	return "", ""
}

var (
	pythonChunk = regexp.MustCompile("(?m)^```\\{python")
	rChunk      = regexp.MustCompile("(?m)^```\\{r[ ,}]")
)

// qmdLanguage reads a Quarto document's code chunks: python when it has
// Python chunks, otherwise r, Quarto's knitr engine. Only the first 64 KB
// are read.
func qmdLanguage(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return "r"
	}
	defer f.Close()
	buf := make([]byte, 64<<10)
	n, _ := f.Read(buf)
	if pythonChunk.Match(buf[:n]) && !rChunk.Match(buf[:n]) {
		return "python"
	}
	return "r"
}
