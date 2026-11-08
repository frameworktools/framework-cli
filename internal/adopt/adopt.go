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
	"slices"
	"sort"
	"strings"
)

//go:embed rules.json
var rulesJSON []byte

// Rules is rules.json.
type Rules struct {
	MaxDepth   int      `json:"max_depth"`
	Skip       []string `json:"skip"`
	Types      Types    `json:"types"`
	Rules      []Rule   `json:"rules"`
	RenderDirs []Rule   `json:"render_dirs"`
}

// Types says how a project's type is inferred: a course from enough
// top-level course folders, and a sensitive project from a private or
// public key.
type Types struct {
	Course struct {
		Signals []string `json:"signals"`
		Min     int      `json:"min"`
	} `json:"course"`
	ProjectSensitive struct {
		KeyPattern string `json:"key_pattern"`
	} `json:"project_sensitive"`
}

// Rule gives a key to the first folder that equals one of its patterns.
type Rule struct {
	Key       string   `json:"key"`
	Patterns  []string `json:"patterns"`
	Languages []string `json:"languages,omitempty"`
	Types     []string `json:"types,omitempty"`
}

// applies reports whether the rule is for language and the project type.
// A rule without languages or types is for all of them.
func (r Rule) applies(language, kind string) bool {
	return (len(r.Languages) == 0 || slices.Contains(r.Languages, language)) &&
		(len(r.Types) == 0 || slices.Contains(r.Types, kind))
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

// Mapping is the proposed layout of a project: its type, Framework keys,
// other folders, and folders that hold rendered notebooks.
type Mapping struct {
	Type             string            `json:"type"`
	Directories      map[string]string `json:"directories"`
	ExtraDirectories map[string]Extra  `json:"extra_directories"`
	RenderDirs       map[string]string `json:"render_dirs"`
	Warnings         []Warning         `json:"warnings"`
	// MarkedPrivate lists the folders marked private (MarkPrivate), which
	// are gitignored like the folders that look private.
	MarkedPrivate []string `json:"marked_private,omitempty"`
}

// Empty reports whether no folder was mapped.
func (m Mapping) Empty() bool {
	return len(m.Directories)+len(m.ExtraDirectories)+len(m.RenderDirs) == 0
}

func (r Rules) skipped(segment string) bool {
	if strings.HasPrefix(segment, ".") {
		return true
	}
	for _, s := range r.Skip {
		if strings.EqualFold(s, segment) || strings.Contains(s, "*") && globMatch(s, segment) {
			return true
		}
	}
	return false
}

// globMatch matches a skip entry in which * stands for any characters
// within one folder name, ignoring case. No other character is special.
func globMatch(glob, name string) bool {
	parts := strings.Split(strings.ToLower(glob), "*")
	for i, p := range parts {
		parts[i] = regexp.QuoteMeta(p)
	}
	re, err := regexp.Compile("^" + strings.Join(parts, ".*") + "$")
	return err == nil && re.MatchString(strings.ToLower(name))
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

// Map proposes a type and directory keys for folders in a project of the
// given language, following the shared algorithm (conformance README,
// adopt/). kind is the project type the caller gives, or "" to infer it.
func (r Rules) Map(folders []string, language, kind string) Mapping {
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

	// 2. A course has enough course folders at the top level.
	top := map[string]bool{}
	for _, f := range kept {
		if !strings.Contains(f, "/") {
			top[strings.ToLower(f)] = true
		}
	}
	signals := 0
	for _, s := range r.Types.Course.Signals {
		if top[strings.ToLower(s)] {
			signals++
		}
	}
	minSignals := r.Types.Course.Min
	if minSignals == 0 {
		minSignals = 2
	}
	course := signals >= minSignals
	ruleType := kind
	if ruleType == "" {
		ruleType = "project"
		if course {
			ruleType = "course"
		}
	}

	// 3 and 4. Each rule's first pattern that matches a folder neither
	// assigned nor inside an assigned folder gives it the rule's key; then
	// the render_dirs rules, the same way.
	m := Mapping{Directories: map[string]string{}, ExtraDirectories: map[string]Extra{}, RenderDirs: map[string]string{}, Warnings: []Warning{}}
	assigned := map[string]bool{}
	within := func(f, root string) bool { return strings.HasPrefix(f, root+"/") }
	free := func(f string) bool {
		if assigned[f] {
			return false
		}
		for a := range assigned {
			if within(f, a) {
				return false
			}
		}
		return true
	}
	assign := func(rules []Rule, into map[string]string, check bool) {
		for _, rule := range rules {
			if check && !rule.applies(language, ruleType) {
				continue
			}
			if _, done := into[rule.Key]; done {
				continue
			}
		patterns:
			for _, pattern := range rule.Patterns {
				for _, f := range kept {
					if strings.EqualFold(f, pattern) && free(f) {
						into[rule.Key] = f
						assigned[f] = true
						break patterns
					}
				}
			}
		}
	}
	assign(r.Rules, m.Directories, true)
	assign(r.RenderDirs, m.RenderDirs, false)

	// 5. Folders inside an assigned folder are covered. Of the rest, one
	// with no assigned folder inside it is an extra directory unless its
	// parent is one too.
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
	// 6. Keys from paths, numbered when a rule for this language and type,
	// or an earlier extra, has them.
	taken := map[string]bool{}
	for k := range m.Directories {
		taken[k] = true
	}
	for _, rule := range r.Rules {
		if rule.applies(language, ruleType) {
			taken[rule.Key] = true
		}
	}
	for _, f := range pureList {
		if pure[parent(f)] {
			continue
		}
		key := numbered(extraKey(f), taken)
		taken[key] = true
		m.ExtraDirectories[key] = Extra{Label: path.Base(f), Path: f}
	}

	for _, f := range kept {
		if strings.Contains(strings.ToLower(f), "private") {
			m.Warnings = append(m.Warnings, Warning{Code: "private_path", Path: f})
		}
	}

	// 7. The type: given, else course, project_sensitive, project, or bare.
	m.Type = kind
	if m.Type == "" {
		m.Type = r.inferType(course, m.Directories)
	}
	return m
}

// inferType is step 7 of the algorithm.
func (r Rules) inferType(course bool, directories map[string]string) string {
	if course {
		return "course"
	}
	if pattern := r.Types.ProjectSensitive.KeyPattern; pattern != "" {
		if re, err := regexp.Compile(pattern); err == nil {
			for k := range directories {
				if re.MatchString(k) {
					return "project_sensitive"
				}
			}
		}
	}
	if len(directories) > 0 {
		return "project"
	}
	return "bare"
}

// numbered returns base, or base_2, base_3, ... when taken has it.
func numbered(base string, taken map[string]bool) string {
	key := base
	for i := 2; taken[key]; i++ {
		key = fmt.Sprintf("%s_%d", base, i)
	}
	return key
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
	for _, rule := range r.RenderDirs {
		if p, ok := m.RenderDirs[rule.Key]; ok {
			rows = append(rows, [2]string{p, "render_dirs." + rule.Key})
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
// extra directory, or "leave" to leave it out. An extra directory's key is
// numbered when a rule, a directory, or another extra has it, as in R.
func (r Rules) SetRole(m Mapping, p, role string) Mapping {
	for k, v := range m.Directories {
		if v == p {
			delete(m.Directories, k)
		}
	}
	for k, v := range m.RenderDirs {
		if v == p {
			delete(m.RenderDirs, k)
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
		taken := map[string]bool{}
		for k := range m.Directories {
			taken[k] = true
		}
		for k := range m.ExtraDirectories {
			taken[k] = true
		}
		for _, rule := range r.Rules {
			taken[rule.Key] = true
		}
		m.ExtraDirectories[numbered(extraKey(p), taken)] = Extra{Label: path.Base(p), Path: p}
	default:
		m.Directories[role] = p
	}
	return m
}

// Keys lists the Framework keys a project of the language and type can
// use, once each.
func (r Rules) Keys(language, kind string) []string {
	var keys []string
	for _, rule := range r.Rules {
		if rule.applies(language, kind) && !slices.Contains(keys, rule.Key) {
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

// DetectQuarto reports whether the project at root uses Quarto: the root or
// a folder Scan lists (at most MaxDepth deep, not hidden or skipped) has a
// _quarto.yml, a _quarto.yaml, or a .qmd file. R: .adopt_detect_quarto().
func (r Rules) DetectQuarto(root string) bool {
	folders, err := r.Scan(root)
	if err != nil {
		return false
	}
	for _, folder := range append([]string{""}, folders...) {
		entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(folder)))
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := strings.ToLower(e.Name())
			if strings.HasPrefix(name, ".") {
				continue
			}
			if name == "_quarto.yml" || name == "_quarto.yaml" || strings.HasSuffix(name, ".qmd") {
				return true
			}
		}
	}
	return false
}
