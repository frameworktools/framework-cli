// Package structure builds a project structure artifact: a folder tree with
// optional notes, where ignored and private paths publish as stubs and
// groups instead of their names. R, Python, and Stata build the same tree;
// see framework-cloud's docs/plans/2026-11-25-structure-artifacts.md.
package structure

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

// Limits Framework Cloud enforces.
const (
	MaxHidden = 10000
	MaxNodes  = 5000
	MaxBytes  = 1 << 20
	MaxLevels = 32
	MaxNote   = 500
)

// Entry types.
const (
	Dir     = "dir"
	File    = "file"
	Ignored = "ignored"
)

// Options say what to build.
type Options struct {
	// ProjectRoot is the project's folder.
	ProjectRoot string
	// Paths are folders relative to ProjectRoot, with / separators. None,
	// or ".", is the whole project.
	Paths []string
	// Depth is how many levels to list; 0 lists all.
	Depth int
	// Exclude globs leave paths out, with no stub.
	Exclude []string
	// HideIgnored leaves ignored paths out instead of showing their rules.
	HideIgnored bool
	// PrivateDirs are settings folders marked private, relative to
	// ProjectRoot.
	PrivateDirs []string
	// Local builds a preview that never uploads, so the folder itself may
	// be ignored or private.
	Local bool
}

// Entry is a folder, a file, or a group of ignored files in a folder.
type Entry struct {
	Name, Path, Type string
	Note             string
	Ignored          *Rule
	Private, Depth   bool
	// Stub folders list no contents; Hidden counts their files. For a
	// group, Hidden counts the files its rule hides.
	Stub     bool
	Hidden   int
	Children []*Entry
	// Names are a group's file names. They stay on this computer: a
	// preview prints them, and an upload never holds them.
	Names []string
}

// Tree is a built project structure.
type Tree struct {
	Root, Base, Method string
	Top                *Entry
	index              map[string]*Entry
}

type builder struct {
	walkRoot    string
	prefix      string
	opts        Options
	exclude     []pattern
	ign         ignorer
	privateDirs []string
	index       map[string]*Entry
}

// Build walks the chosen folders and returns their tree.
func Build(o Options) (*Tree, error) {
	if o.Depth < 0 {
		return nil, errors.New("depth must be 0 (no limit) or more")
	}
	projectRoot := resolved(o.ProjectRoot)
	paths, err := cleanPaths(projectRoot, o.Paths)
	if err != nil {
		return nil, err
	}
	t := &Tree{Root: filepath.Base(projectRoot), Top: &Entry{Type: Dir}}
	walkRoot := projectRoot
	var chosen []string
	switch {
	case len(paths) == 1:
		t.Base = paths[0]
		t.Root = path.Base(paths[0])
		walkRoot = filepath.Join(projectRoot, filepath.FromSlash(paths[0]))
	case len(paths) > 1:
		chosen = paths
	}
	t.Top.Name = t.Root
	b := &builder{walkRoot: walkRoot, prefix: t.Base, opts: o, index: map[string]*Entry{}}
	for _, g := range o.Exclude {
		g = strings.TrimSpace(g)
		if g == "" {
			continue
		}
		p, ok := compilePattern(strings.TrimPrefix(g, "!"))
		if !ok {
			return nil, fmt.Errorf("--exclude %q is not a glob fw can read", g)
		}
		b.exclude = append(b.exclude, p)
	}
	for _, d := range o.PrivateDirs {
		d = strings.Trim(path.Clean(strings.ReplaceAll(d, `\`, "/")), "/")
		if d != "" && d != "." && !strings.HasPrefix(d, "../") && d != ".." {
			b.privateDirs = append(b.privateDirs, d)
		}
	}

	if g, ok := newGitIgnore(walkRoot, projectRoot); ok {
		b.ign, t.Method = g, "git"
	} else if data, err := os.ReadFile(filepath.Join(projectRoot, ".gitignore")); err == nil {
		b.ign, t.Method = fileIgnore{patterns: parsePatterns(string(data)), prefix: t.Base}, "gitignore"
	} else {
		b.ign, t.Method = noIgnore{}, "none"
	}

	if t.Base != "" && !o.Local {
		if b.isPrivate("") {
			return nil, fmt.Errorf("%s is a private folder, so its contents don't publish", t.Base)
		}
		if b.rootIgnored(projectRoot) {
			return nil, fmt.Errorf("%s is ignored by git, so its contents don't publish", t.Base)
		}
	}

	if chosen == nil {
		if err := b.list(t.Top, "", 0); err != nil {
			return nil, err
		}
	} else {
		for _, c := range chosen {
			parentEntry := t.Top
			parts := strings.Split(c, "/")
			for i := 1; i < len(parts); i++ {
				if parentEntry = b.ensureDir(parentEntry, strings.Join(parts[:i], "/")); parentEntry == nil || parentEntry.Stub {
					break
				}
			}
			if parentEntry == nil || parentEntry.Stub {
				continue
			}
			info, err := os.Lstat(filepath.Join(walkRoot, filepath.FromSlash(c)))
			if err != nil {
				return nil, err
			}
			if err := b.add(parentEntry, c, fs.FileInfoToDirEntry(info), 1); err != nil {
				return nil, err
			}
		}
		sortAll(t.Top)
	}
	t.index = b.index
	return t, nil
}

// cleanPaths checks each path is a folder in the project and drops paths
// inside another chosen path.
func cleanPaths(projectRoot string, paths []string) ([]string, error) {
	var out []string
	for _, p := range paths {
		p = filepath.ToSlash(filepath.Clean(filepath.FromSlash(strings.TrimSpace(p))))
		p = strings.Trim(p, "/")
		if p == "." || p == "" {
			return nil, nil
		}
		if p == ".." || strings.HasPrefix(p, "../") || filepath.IsAbs(p) {
			return nil, fmt.Errorf("%s is outside the project", p)
		}
		if p == ".git" || strings.HasPrefix(p, ".git/") {
			return nil, errors.New(".git is always left out of a project structure")
		}
		info, err := os.Stat(filepath.Join(projectRoot, filepath.FromSlash(p)))
		if err != nil {
			return nil, fmt.Errorf("%s: no such folder in the project", p)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("%s is a file; name folders", p)
		}
		out = append(out, p)
	}
	sort.Strings(out)
	var kept []string
	for _, p := range out {
		if len(kept) > 0 {
			last := kept[len(kept)-1]
			if p == last || strings.HasPrefix(p, last+"/") {
				continue
			}
		}
		kept = append(kept, p)
	}
	return kept, nil
}

// rootIgnored reports whether git or the root .gitignore ignores the
// published folder itself.
func (b *builder) rootIgnored(projectRoot string) bool {
	if _, ok := b.ign.(gitIgnore); ok {
		out, err := gitCommand(projectRoot, []byte(b.prefix+"/\x00"), "check-ignore", "-z", "--stdin")
		return err == nil && len(out) > 0
	}
	if f, ok := b.ign.(fileIgnore); ok {
		return fileIgnore{patterns: f.patterns}.dir(b.prefix) != nil
	}
	return false
}

// ensureDir returns the folder entry at rel under p, a folder above a
// chosen path, making it when needed. An ignored or private folder is a
// stub, and with HideIgnored an ignored one is nil.
func (b *builder) ensureDir(p *Entry, rel string) *Entry {
	for _, c := range p.Children {
		if c.Path == rel && c.Type == Dir {
			return c
		}
	}
	rule := b.ign.dir(rel)
	if rule != nil && b.opts.HideIgnored {
		return nil
	}
	e := &Entry{Name: path.Base(rel), Path: rel, Type: Dir, Ignored: rule, Private: b.isPrivate(rel)}
	if rule != nil || e.Private {
		e.Stub, e.Hidden = true, b.count(rel)
	}
	p.Children = append(p.Children, e)
	b.index[rel] = e
	return e
}

// list adds rel's contents to e. level is rel's depth: 0 for the top.
func (b *builder) list(e *Entry, rel string, level int) error {
	entries, err := os.ReadDir(filepath.Join(b.walkRoot, filepath.FromSlash(rel)))
	if err != nil {
		return err
	}
	for _, d := range entries {
		if err := b.add(e, join(rel, d.Name()), d, level+1); err != nil {
			return err
		}
	}
	sortTree(e)
	return nil
}

// add adds one path to its parent folder's entry.
func (b *builder) add(parentEntry *Entry, rel string, d fs.DirEntry, level int) error {
	name := path.Base(rel)
	isDir := d.IsDir() && d.Type()&fs.ModeSymlink == 0
	if name == ".git" || matchesAny(b.exclude, rel, isDir) {
		return nil
	}
	if !isDir {
		// Symbolic links are files and are never followed.
		if rule := b.ign.file(rel); rule != nil {
			if b.opts.HideIgnored {
				return nil
			}
			group := b.group(parentEntry, rule)
			group.Names = append(group.Names, name)
			group.Hidden = min(len(group.Names), MaxHidden)
			return nil
		}
		e := &Entry{Name: name, Path: rel, Type: File}
		parentEntry.Children = append(parentEntry.Children, e)
		b.index[rel] = e
		return nil
	}
	rule := b.ign.dir(rel)
	if rule != nil && b.opts.HideIgnored {
		return nil
	}
	e := &Entry{Name: name, Path: rel, Type: Dir, Ignored: rule, Private: b.isPrivate(rel)}
	parentEntry.Children = append(parentEntry.Children, e)
	b.index[rel] = e
	levels := strings.Count(join(b.prefix, rel), "/") + 1
	switch {
	case rule != nil || e.Private:
		e.Stub = true
	case b.opts.Depth > 0 && level >= b.opts.Depth, levels >= MaxLevels:
		e.Stub, e.Depth = true, true
	}
	if e.Stub {
		e.Hidden = b.count(rel)
		return nil
	}
	return b.list(e, rel, level)
}

// group returns e's group of files ignored by rule, making it when needed.
func (b *builder) group(e *Entry, rule *Rule) *Entry {
	name := strings.TrimRight(rule.Rule, "/")
	for _, c := range e.Children {
		if c.Type == Ignored && c.Name == name {
			return c
		}
	}
	g := &Entry{Name: name, Path: e.Path, Type: Ignored, Ignored: rule, Stub: true}
	e.Children = append(e.Children, g)
	return g
}

// isPrivate reports whether rel, relative to the walked folder, is a
// private folder: named private or ending in _private, in any case, or a
// settings folder marked private. rel "" is the walked folder itself.
func (b *builder) isPrivate(rel string) bool {
	full := join(b.prefix, rel)
	if full == "" {
		return false
	}
	if rel == "" {
		// The walked folder counts when any folder down to it is private.
		for _, part := range strings.Split(full, "/") {
			if privateName(part) {
				return true
			}
		}
	} else if privateName(path.Base(rel)) {
		return true
	}
	for _, d := range b.privateDirs {
		if full == d || strings.HasPrefix(full, d+"/") {
			return true
		}
	}
	return false
}

func privateName(name string) bool {
	lower := strings.ToLower(name)
	return lower == "private" || strings.HasSuffix(lower, "_private")
}

// count counts the files under rel, up to MaxHidden. Excluded paths and
// .git folders don't count, and symbolic links count as files.
func (b *builder) count(rel string) int {
	n := 0
	start := filepath.Join(b.walkRoot, filepath.FromSlash(rel))
	_ = filepath.WalkDir(start, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if p == start {
			return nil
		}
		sub, _ := filepath.Rel(b.walkRoot, p)
		sub = filepath.ToSlash(sub)
		isDir := d.IsDir()
		if d.Name() == ".git" || matchesAny(b.exclude, sub, isDir) {
			if isDir {
				return filepath.SkipDir
			}
			return nil
		}
		if !isDir {
			n++
			if n >= MaxHidden {
				return filepath.SkipAll
			}
		}
		return nil
	})
	return n
}

// sortTree orders a folder's entries as Framework Cloud does: folders, then
// files, then ignored groups, each by name ignoring case.
func sortTree(e *Entry) {
	rank := map[string]int{Dir: 0, File: 1, Ignored: 2}
	sort.SliceStable(e.Children, func(i, j int) bool {
		a, c := e.Children[i], e.Children[j]
		if rank[a.Type] != rank[c.Type] {
			return rank[a.Type] < rank[c.Type]
		}
		if la, lc := strings.ToLower(a.Name), strings.ToLower(c.Name); la != lc {
			return la < lc
		}
		return a.Name < c.Name
	})
	for _, c := range e.Children {
		sort.Strings(c.Names)
	}
}

func sortAll(e *Entry) {
	sortTree(e)
	for _, c := range e.Children {
		sortAll(c)
	}
}

// Find returns the folder or file at p, a path in the tree. A path that
// starts with the published folder's own path also finds it.
func (t *Tree) Find(p string) *Entry {
	p = strings.Trim(filepath.ToSlash(strings.TrimSpace(p)), "/")
	p = strings.TrimPrefix(p, "./")
	if e := t.index[p]; e != nil {
		return e
	}
	if t.Base != "" {
		if rest, ok := strings.CutPrefix(p, t.Base+"/"); ok {
			return t.index[rest]
		}
	}
	return nil
}

// SetNotes adds notes, by path. A path not in the tree is an error.
func (t *Tree) SetNotes(notes map[string]string) error {
	keys := make([]string, 0, len(notes))
	for k := range notes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		e := t.Find(k)
		if e == nil {
			return fmt.Errorf("the note for %s names a path that isn't in the tree", k)
		}
		note := strings.Join(strings.Fields(notes[k]), " ")
		if utf8.RuneCountInString(note) > MaxNote {
			return fmt.Errorf("the note for %s is longer than %d characters", k, MaxNote)
		}
		e.Note = note
	}
	return nil
}

// Document is the upload: framework.artifact/v1 with kind tree.
type Document struct {
	Schema    string   `json:"schema"`
	Kind      string   `json:"kind"`
	Source    Source   `json:"source"`
	Root      string   `json:"root"`
	Base      string   `json:"base"`
	Method    string   `json:"method"`
	Nodes     []Node   `json:"nodes"`
	Open      []string `json:"open,omitempty"`
	Highlight []string `json:"highlight,omitempty"`
}

// Source names the client language.
type Source struct {
	Language string `json:"language"`
}

// Node is one path in the upload.
type Node struct {
	Path    string `json:"path"`
	Type    string `json:"type"`
	Note    string `json:"note,omitempty"`
	Ignored *Rule  `json:"ignored,omitempty"`
	Private bool   `json:"private,omitempty"`
	Depth   bool   `json:"depth,omitempty"`
	Hidden  *int   `json:"hidden,omitempty"`
}

// Document returns the upload. open names folders that start open in the
// embed and highlight names paths drawn highlighted; each must be in the
// tree.
func (t *Tree) Document(language string, open, highlight []string) (*Document, error) {
	doc := &Document{Schema: "framework.artifact/v1", Kind: "tree", Source: Source{Language: language}, Root: t.Root, Base: t.Base, Method: t.Method}
	var walk func(e *Entry)
	walk = func(e *Entry) {
		for _, c := range e.Children {
			n := Node{Path: c.Path, Type: c.Type, Note: c.Note, Ignored: c.Ignored, Private: c.Private, Depth: c.Depth}
			if c.Stub {
				hidden := min(c.Hidden, MaxHidden)
				n.Hidden = &hidden
			}
			doc.Nodes = append(doc.Nodes, n)
			if c.Type == Dir && !c.Stub {
				walk(c)
			}
		}
	}
	walk(t.Top)
	if len(doc.Nodes) == 0 {
		return nil, errors.New("the folder is empty, so there is no structure to publish")
	}
	if len(doc.Nodes) > MaxNodes {
		return nil, fmt.Errorf("the tree has %d paths, more than the %d Framework Cloud takes. List fewer levels with --depth, or leave folders out with --exclude", len(doc.Nodes), MaxNodes)
	}
	for _, p := range open {
		e := t.Find(p)
		if e == nil || e.Type != Dir {
			return nil, fmt.Errorf("open names %s, which isn't a folder in the tree", p)
		}
		doc.Open = appendNew(doc.Open, e.Path)
	}
	for _, p := range highlight {
		e := t.Find(p)
		if e == nil {
			return nil, fmt.Errorf("highlight names %s, which isn't in the tree", p)
		}
		doc.Highlight = appendNew(doc.Highlight, e.Path)
	}
	data, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	if len(data) > MaxBytes {
		return nil, errors.New("the tree is larger than the 1 MB Framework Cloud takes. List fewer levels with --depth, or leave folders out with --exclude")
	}
	return doc, nil
}

func appendNew(list []string, s string) []string {
	for _, x := range list {
		if x == s {
			return list
		}
	}
	return append(list, s)
}

// Slug is the default slug: structure, or structure-<base> for a subfolder.
func (t *Tree) Slug(slugify func(string) string) string {
	if t.Base == "" {
		return "structure"
	}
	return "structure-" + slugify(t.Base)
}

// Title is the default title: "<root> structure".
func (t *Tree) Title() string {
	return t.Root + " structure"
}
