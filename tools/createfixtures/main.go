// Command createfixtures writes the Stata cases of the shared create/
// conformance fixtures. The R repository holds them in
// inst/conformance/create/stata/; fw generates them because it is the Stata
// engine. Each case is a resolved spec and every path creating it writes.
//
//	go run ./tools/createfixtures --out ../framework/inst/conformance/create/stata
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/frameworktools/framework-cli/internal/spec"
	"github.com/frameworktools/framework-cli/internal/stata"
)

// entry is one layer as the case records it.
type entry struct {
	Kind  string         `json:"kind"`
	Key   string         `json:"key"`
	Layer map[string]any `json:"layer"`
}

// Case is one file in create/stata/.
type Case struct {
	Description string         `json:"description"`
	Language    string         `json:"language"`
	Layers      []entry        `json:"layers"`
	Spec        map[string]any `json:"spec"`
	Expected    struct {
		Paths []string `json:"paths"`
	} `json:"expected"`
}

// scripts is the Scripts-only workflow's layer, from R's
// inst/workflows/scripts.json.
var scripts = entry{Kind: "facet", Key: "framework/workflow-scripts", Layer: map[string]any{
	"tools":    map[string]any{"quarto": false, "jupyter": false},
	"scaffold": map[string]any{"workflow": "scripts", "notebook_format": "script"},
}}

func base(kind string) entry {
	layer := map[string]any{"type": kind}
	if kind == "bare" {
		layer["core_files"] = false
	}
	return entry{Kind: "base", Key: kind, Layer: layer}
}

func choices(extra map[string]any) entry {
	layer := map[string]any{"language": "stata", "git": map[string]any{"use_git": false}}
	for k, v := range extra {
		layer[k] = v
	}
	return entry{Kind: "project", Key: "choices", Layer: layer}
}

var cases = []struct {
	name, description string
	layers            []entry
}{
	{"project-scripts", "Standard structure, Scripts only, with Claude Code.",
		[]entry{base("project"), scripts, choices(nil)}},
	{"project_sensitive-scripts", "Privacy-sensitive structure: private and public folders and Claude Code deny rules.",
		[]entry{base("project_sensitive"), scripts, choices(nil)}},
	{"bare-scripts", "Bare structure: folders, settings.do, and settings.yml only, no do-files, .env, or AI files.",
		[]entry{base("bare"), scripts, choices(nil)}},
	{"project-scripts-no-agents", "Standard structure with AI agents off.",
		[]entry{base("project"), scripts, choices(map[string]any{"ai": map[string]any{"enabled": false}})}},
	{"project-scripts-codex-copilot", "Standard structure with Codex and Copilot instead of Claude Code.",
		[]entry{base("project"), scripts, choices(map[string]any{"ai": map[string]any{"assistants": map[string]any{"claude": false, "codex": true, "copilot": true}}})}},
}

func main() {
	out := flag.String("out", "", "folder to write the cases to")
	flag.Parse()
	if *out == "" {
		fmt.Fprintln(os.Stderr, "usage: createfixtures --out DIR")
		os.Exit(2)
	}
	if err := run(*out); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(out string) error {
	if err := os.MkdirAll(out, 0o755); err != nil { // #nosec G301 -- fixtures are public
		return err
	}
	for _, c := range cases {
		got, err := build(c.description, c.layers)
		if err != nil {
			return fmt.Errorf("%s: %w", c.name, err)
		}
		data, err := json.MarshalIndent(got, "", "  ")
		if err != nil {
			return err
		}
		path := filepath.Join(out, c.name+".json")
		if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil { // #nosec G306 -- fixtures are public
			return err
		}
		fmt.Println(path)
	}
	return nil
}

func build(description string, layers []entry) (Case, error) {
	c := Case{Description: description, Language: "stata", Layers: layers}
	env := spec.Envelope{SpecVersion: 1, Name: "Fixture Study",
		Author: json.RawMessage(`{"name":"Ada Analyst","email":"ada@example.org","affiliation":""}`)}
	for _, l := range layers {
		data, err := json.Marshal(l.Layer)
		if err != nil {
			return c, err
		}
		env.Layers = append(env.Layers, spec.Entry{Kind: l.Kind, Key: l.Key, Layer: data})
	}
	r, err := stata.Resolve(env, "")
	if err != nil {
		return c, err
	}
	// The source names the client that resolved the spec, which is not part
	// of creation.
	delete(r.Doc, "source")
	c.Spec = r.Doc
	paths, err := Create(c.Spec)
	c.Expected.Paths = paths
	return c, err
}

// Create builds a project from a resolved spec in a new temporary folder
// and returns the sorted paths it wrote, folders ending in "/". Tests use
// it to check the cases.
func Create(doc map[string]any) ([]string, error) {
	r, err := stata.FromSpec(doc)
	if err != nil {
		return nil, err
	}
	tmp, err := os.MkdirTemp("", "fw-create-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	dir := filepath.Join(tmp, "fixture-study")
	if _, err := stata.Scaffold(r, stata.Options{Dir: dir, Now: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)}); err != nil {
		return nil, err
	}
	return Paths(dir)
}

// Paths lists every file and folder under dir as the create/ cases do.
func Paths(dir string) ([]string, error) {
	var paths []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || path == dir {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == ".git" {
			return filepath.SkipDir
		}
		if d.IsDir() {
			rel += "/"
		}
		paths = append(paths, rel)
		return nil
	})
	sort.Strings(paths)
	return paths, err
}
