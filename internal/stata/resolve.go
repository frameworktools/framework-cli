// Package stata scaffolds Stata projects. fw builds them itself, because
// Stata has no Framework package. It writes the folder layout, master.do,
// settings.do with path globals, do-file templates, .gitignore, and the AI
// files, and nothing beyond scaffolding.
package stata

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/frameworktools/framework-cli/internal/catalog"
	"github.com/frameworktools/framework-cli/internal/skillsync"
	"github.com/frameworktools/framework-cli/internal/spec"
)

// Types names fw's built-in Stata structures, used when the layers bring no
// directories of their own. type is otherwise only a label: behavior follows
// directories, security.sensitive, and core_files.
var Types = []string{"project", "project_sensitive", "bare"}

// defaults is layer 0 for a Stata project of the given type.
func defaults(kind string) map[string]any {
	var dirs map[string]any
	switch kind {
	case "project":
		dirs = map[string]any{
			"do":                  "do",
			"inputs_raw":          "inputs/raw",
			"inputs_intermediate": "inputs/intermediate",
			"inputs_final":        "inputs/final",
			"outputs_tables":      "outputs/tables",
			"outputs_figures":     "outputs/figures",
			"docs":                "docs",
			"scratch":             "scratch",
			"logs":                "logs",
		}
	case "project_sensitive":
		dirs = map[string]any{
			"do":                          "do",
			"inputs_private_raw":          "inputs/private/raw",
			"inputs_private_intermediate": "inputs/private/intermediate",
			"inputs_private_final":        "inputs/private/final",
			"inputs_public_raw":           "inputs/public/raw",
			"inputs_public_intermediate":  "inputs/public/intermediate",
			"inputs_public_final":         "inputs/public/final",
			"outputs_private_tables":      "outputs/private/tables",
			"outputs_public_tables":       "outputs/public/tables",
			"outputs_private_figures":     "outputs/private/figures",
			"outputs_public_figures":      "outputs/public/figures",
			"docs":                        "docs",
			"scratch":                     "scratch",
			"logs":                        "logs",
		}
	default:
		dirs = map[string]any{}
	}
	skills := map[string]any{}
	if kind != "bare" {
		skills["framework-stata"] = map[string]any{"package": true}
	}
	return map[string]any{
		"author":            map[string]any{"name": "", "email": "", "affiliation": ""},
		"directories":       dirs,
		"extra_directories": map[string]any{},
		"git":               map[string]any{"use_git": true, "hooks": map[string]any{}},
		"ai":                map[string]any{"enabled": true, "assistants": map[string]any{"claude": true}, "agents_md": "stub"},
		"security":          map[string]any{"sensitive": kind == "project_sensitive"},
		"env":               map[string]any{},
		"data_hooks":        map[string]any{},
		"files":             map[string]any{},
		"skills":            skills,
		"scaffold": map[string]any{
			"seed_on_scaffold": false, "seed": "",
			"workflow": "scripts", "notebook_format": "script",
		},
	}
}

// Resolved is a resolved Stata spec: the full document for
// .framework/spec.json and a typed view for scaffolding.
type Resolved struct {
	Doc       map[string]any
	View      View
	Conflicts []spec.Conflict
}

// View is the part of the spec that Stata scaffolding uses.
type View struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	Author struct {
		Name        string `json:"name"`
		Email       string `json:"email"`
		Affiliation string `json:"affiliation"`
	} `json:"author"`
	Directories      map[string]string `json:"directories"`
	ExtraDirectories map[string]struct {
		Label string `json:"label"`
		Path  string `json:"path"`
	} `json:"extra_directories"`
	Git struct {
		UseGit           bool   `json:"use_git"`
		GitignoreContent string `json:"gitignore_content"`
	} `json:"git"`
	AI struct {
		Enabled    bool            `json:"enabled"`
		Assistants map[string]bool `json:"assistants"`
		AgentsMD   string          `json:"agents_md"`
	} `json:"ai"`
	Security struct {
		Sensitive bool `json:"sensitive"`
	} `json:"security"`
	Tools    map[string]bool `json:"tools"`
	Scaffold struct {
		Workflow       string `json:"workflow"`
		NotebookFormat string `json:"notebook_format"`
	} `json:"scaffold"`
	Env   map[string]any `json:"env"`
	Files map[string]struct {
		Content  string `json:"content"`
		Template bool   `json:"template"`
	} `json:"files"`
	Skills map[string]struct {
		Package bool              `json:"package"`
		Content string            `json:"content"`
		Files   map[string]string `json:"files"`
		Bundle  []skillsync.File  `json:"bundle"`
		Sync    *struct {
			ID      string `json:"skill_id"`
			Version int    `json:"version"`
			Hash    string `json:"hash"`
			Variant string `json:"variant"`
			Server  string `json:"server"`
		} `json:"sync"`
	} `json:"skills"`

	// CoreFiles is false for a bare structure: no starter .do files and no
	// .env.
	CoreFiles bool `json:"-"`
}

// Resolve merges the envelope's layers over the Stata defaults and finalizes
// the spec, as R's spec_resolve() does for R projects. fwVersion is recorded
// in source.fw_version.
func Resolve(env spec.Envelope, fwVersion string) (Resolved, error) {
	if lang := env.Language(); lang != "stata" {
		return Resolved{}, fmt.Errorf("this layers file describes a %s project, not a Stata project", lang)
	}
	layers, err := env.LabeledLayers()
	if err != nil {
		return Resolved{}, err
	}
	kind := "project"
	for _, l := range layers {
		if t, ok := l.Layer["type"].(string); ok {
			kind = t
		}
	}
	merged, err := spec.Merge(append([]spec.Labeled{{Label: spec.DefaultsLabel, Layer: defaults(kind)}}, layers...))
	if err != nil {
		return Resolved{}, err
	}
	doc := merged.Spec

	// core_files: false marks a bare structure. Layers from before it
	// existed say so with the type label alone.
	coreFiles, explicit := doc["core_files"].(bool)
	if !explicit {
		coreFiles = kind != "bare"
	}
	if !coreFiles {
		doc["core_files"] = false
	} else {
		delete(doc, "core_files")
	}
	if !contains(Types, kind) && coreFiles && len(asMap(doc["directories"])) == 0 {
		return Resolved{}, fmt.Errorf("fw has no Stata structure for type %q, and the layers bring no directories. Built-in types: %s", kind, strings.Join(Types, ", "))
	}

	doc["spec_version"] = 1
	doc["name"] = env.Name
	doc["type"] = kind
	doc["language"] = "stata"
	if len(env.Author) > 0 {
		author, err := spec.DecodeObject(env.Author)
		if err != nil {
			return Resolved{}, fmt.Errorf("author: %w", err)
		}
		doc["author"] = mergeAuthor(asMap(doc["author"]), author)
	}
	if len(env.Source) > 0 {
		source, err := spec.DecodeObject(env.Source)
		if err != nil {
			return Resolved{}, fmt.Errorf("source: %w", err)
		}
		doc["source"] = source
	} else {
		doc["source"] = map[string]any{"origin": "cli"}
	}
	if fwVersion != "" {
		asMap(doc["source"])["fw_version"] = fwVersion
	}
	legacyBare := kind == "bare" && !explicit
	finalize(doc, legacyBare, merged.Gitignore)

	view, err := prepare(doc, false)
	if err != nil {
		return Resolved{}, err
	}
	return Resolved{Doc: doc, View: view, Conflicts: merged.Conflicts}, nil
}

// FromSpec prepares an already resolved Stata spec for scaffolding, such as
// the input of a create/ conformance case.
func FromSpec(doc map[string]any) (Resolved, error) {
	if lang, _ := doc["language"].(string); lang != "stata" {
		return Resolved{}, fmt.Errorf("this spec describes a %q project, not a Stata project", lang)
	}
	view, err := prepare(doc, true)
	if err != nil {
		return Resolved{}, err
	}
	return Resolved{Doc: doc, View: view}, nil
}

// prepare validates a resolved spec and returns its view. existing allows
// what a project fw adopted or upgraded may record, such as tools.quarto
// for Quarto files it found.
func prepare(doc map[string]any, existing bool) (View, error) {
	if err := spec.ValidateResolved(doc); err != nil {
		return View{}, err
	}
	var view View
	data, err := json.Marshal(doc)
	if err != nil {
		return View{}, err
	}
	if err := json.Unmarshal(data, &view); err != nil {
		return View{}, err
	}
	if err := checkStata(view, existing); err != nil {
		return View{}, err
	}
	if data, ok := doc["data"].(map[string]any); ok {
		if problems := catalog.Problems(data); len(problems) > 0 {
			return View{}, fmt.Errorf("the layers' data catalog: %s", strings.Join(problems, "; "))
		}
	}
	view.CoreFiles = doc["core_files"] != false
	return view, nil
}

// mergeAuthor applies the envelope's author over the merged one: non-empty
// fields win, and affiliations replace the whole list, as in R.
func mergeAuthor(base, over map[string]any) map[string]any {
	for k, v := range over {
		if s, ok := v.(string); ok && s == "" {
			continue
		}
		base[k] = v
	}
	return base
}

// finalize fills settings the layers left out. legacyBare is a bare
// project described only by its type label, which also has no AI setup.
func finalize(doc map[string]any, legacyBare bool, blocks []spec.Block) {
	security := asMap(doc["security"])
	sensitive, _ := security["sensitive"].(bool)
	security["sensitive"] = sensitive
	doc["security"] = security

	skills := asMap(doc["skills"])
	if sensitive && !legacyBare {
		if _, ok := skills["framework-sensitive-data"]; !ok {
			skills["framework-sensitive-data"] = map[string]any{"package": true}
		}
	}
	doc["skills"] = skills

	ai := asMap(doc["ai"])
	kept := map[string]any{}
	for name, on := range asMap(ai["assistants"]) {
		if on == true {
			kept[name] = true
		}
	}
	ai["assistants"] = kept
	if _, ok := ai["agents_md"]; !ok {
		ai["agents_md"] = "stub"
	}
	if _, ok := ai["enabled"]; !ok {
		ai["enabled"] = true
	}
	doc["ai"] = ai

	git := asMap(doc["git"])
	git["gitignore_content"] = gitignore(sensitive, blocks)
	if _, ok := git["hooks"]; !ok {
		git["hooks"] = map[string]any{}
	}
	if _, ok := git["use_git"]; !ok {
		git["use_git"] = true
	}
	doc["git"] = git

	// Sections the merge may have left as empty arrays become empty objects
	for _, key := range []string{"directories", "extra_directories", "env", "data_hooks", "files", "skills"} {
		if m, ok := doc[key].(map[string]any); ok {
			doc[key] = m
		} else {
			doc[key] = map[string]any{}
		}
	}
}

func gitignore(sensitive bool, blocks []spec.Block) string {
	var b strings.Builder
	if err := templates.ExecuteTemplate(&b, "gitignore.tmpl", map[string]any{"Sensitive": sensitive}); err != nil {
		panic(err) // embedded template
	}
	for _, block := range blocks {
		fmt.Fprintf(&b, "\n# --- from %s ---\n%s\n", block.Label, block.Text)
	}
	return b.String()
}

var stataName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,31}$`)

// checkStata rejects what the schema allows but Stata cannot use: tools,
// workflows other than Scripts only, global names Stata does not accept,
// and paths with characters that Stata expands inside quotes. In an
// existing project, tools.quarto may be on: fw adopt and fw project upgrade
// record it when the folder has Quarto files, and fw writes nothing for it.
func checkStata(v View, existing bool) error {
	var on []string
	for id, enabled := range v.Tools {
		if enabled && (!existing || id != "quarto") {
			on = append(on, id)
		}
	}
	if len(on) > 0 {
		sort.Strings(on)
		return fmt.Errorf("the layers turn on %s, but Stata projects have no tools", strings.Join(on, ", "))
	}
	if w := v.Scaffold.Workflow; w != "" && w != "scripts" {
		return fmt.Errorf("the layers choose the %s workflow, but Stata projects support only the Scripts-only workflow (scripts)", w)
	}
	if f := v.Scaffold.NotebookFormat; f != "" && f != "script" {
		return fmt.Errorf("the layers choose the %s notebook format, but Stata projects use scripts only (script)", f)
	}
	keys := make([]string, 0, len(v.Directories)+len(v.ExtraDirectories))
	paths := map[string]string{}
	for k, p := range v.Directories {
		keys = append(keys, k)
		paths[k] = p
	}
	for k, e := range v.ExtraDirectories {
		if _, dup := paths[k]; dup {
			return fmt.Errorf("extra_directories.%s: the name is already a directory", k)
		}
		keys = append(keys, k)
		paths[k] = e.Path
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !stataName.MatchString(k) || k == "root" {
			return fmt.Errorf("directory name %q cannot be a Stata global (at most 32 letters, digits, or underscores, and not \"root\")", k)
		}
		if strings.ContainsAny(paths[k], "`$\"'") {
			return fmt.Errorf("directory %s: path %q contains a quote, backtick, or $, which Stata would expand", k, paths[k])
		}
	}
	return nil
}

func asMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
