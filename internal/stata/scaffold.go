package stata

import (
	"bytes"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"text/template"
	"time"

	"github.com/frameworktools/framework-cli/internal/adopt"
	"github.com/frameworktools/framework-cli/internal/project"
	"github.com/frameworktools/framework-cli/internal/skillsync"
	"gopkg.in/yaml.v3"
)

//go:embed templates/*.tmpl
var templateFiles embed.FS

//go:embed all:skills
var skillFiles embed.FS

var templates = template.Must(template.New("").Funcs(template.FuncMap{"yaml": yamlScalar, "indent": func(n int) string { return strings.Repeat("  ", n) }}).ParseFS(templateFiles, "templates/*.tmpl"))

// yamlScalar renders s as a YAML scalar, quoted only when it must be.
func yamlScalar(s string) string {
	data, err := yaml.Marshal(s)
	if err != nil {
		panic(err) // a string always marshals
	}
	return strings.TrimSuffix(string(data), "\n")
}

// checkoutAllowlist is what headless creation accepts in the target folder,
// matching the R and Python packages.
var checkoutAllowlist = map[string]bool{".git": true, ".github": true, ".framework": true, "README.md": true, "LICENSE": true}

// assistantTable maps each assistant to its skills folder and stub file, as
// in the R package.
var assistantTable = map[string]struct{ skillsDir, stub string }{
	"claude":  {".claude/skills", "CLAUDE.md"},
	"codex":   {".agents/skills", ""},
	"copilot": {".agents/skills", ".github/copilot-instructions.md"},
}

// Options control scaffolding.
type Options struct {
	Dir      string // absolute project folder
	Headless bool   // no git, and only into a fresh checkout
	Now      time.Time
}

// Result lists what scaffolding wrote and anything the user should know.
type Result struct {
	Created []string
	Notes   []string
}

type scaffolder struct {
	r   Resolved
	o   Options
	res Result
}

// Scaffold writes the project.
func Scaffold(r Resolved, o Options) (Result, error) {
	s := &scaffolder{r: r, o: o}
	if err := s.checkTarget(); err != nil {
		return Result{}, err
	}
	if err := os.MkdirAll(o.Dir, 0o755); err != nil {
		return Result{}, err
	}
	v := r.View

	steps := []func() error{s.directories, s.settingsDo, s.settingsYml}
	if v.CoreFiles {
		steps = append(steps, s.masterDo, s.envFile)
	}
	steps = append(steps, s.gitignore)
	if v.CoreFiles && v.AI.Enabled {
		steps = append(steps, s.ai)
	}
	steps = append(steps, s.specFiles, s.record)
	if !o.Headless && v.Git.UseGit {
		steps = append(steps, s.git)
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return s.res, err
		}
	}
	return s.res, nil
}

func (s *scaffolder) checkTarget() error {
	entries, err := os.ReadDir(s.o.Dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if s.o.Headless {
		var unexpected []string
		for _, e := range entries {
			if !checkoutAllowlist[e.Name()] {
				unexpected = append(unexpected, e.Name())
			}
		}
		if len(unexpected) > 0 {
			return fmt.Errorf("refusing to create a project in %s: it already contains %s. Headless creation only runs in a fresh checkout (allowed: .git, .github, .framework, README.md, LICENSE)",
				s.o.Dir, strings.Join(unexpected, ", "))
		}
		for rel := range s.r.View.Files {
			if strings.HasPrefix(rel, ".github/workflows/") {
				return fmt.Errorf("headless creation cannot write workflow files (GitHub rejects them from GITHUB_TOKEN pushes): %s", rel)
			}
		}
		return nil
	}
	if len(entries) > 0 {
		return fmt.Errorf("%s already exists and is not empty", s.o.Dir)
	}
	return nil
}

// write creates rel inside the project folder.
func (s *scaffolder) write(rel, content string, mode os.FileMode) error {
	clean := path.Clean(rel)
	if path.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") {
		return fmt.Errorf("refusing to write outside the project: %s", rel)
	}
	target := filepath.Join(s.o.Dir, filepath.FromSlash(clean))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(target, []byte(content), mode); err != nil {
		return fmt.Errorf("writing %s: %w", rel, err)
	}
	s.res.Created = append(s.res.Created, clean)
	return nil
}

func (s *scaffolder) render(name string, data any) (string, error) {
	var b bytes.Buffer
	if err := templates.ExecuteTemplate(&b, name, data); err != nil {
		return "", err
	}
	return b.String(), nil
}

type global struct{ Name, Path string }

// globals lists every directory as a Stata global, ordered by path.
func (s *scaffolder) globals() []global {
	var out []global
	for k, p := range s.r.View.Directories {
		out = append(out, global{k, p})
	}
	for k, e := range s.r.View.ExtraDirectories {
		out = append(out, global{k, e.Path})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// folders lists each directory and its parents, parents first, so mkdir
// works one level at a time.
func folders(gs []global) []string {
	seen := map[string]bool{}
	var out []string
	for _, g := range gs {
		parts := strings.Split(g.Path, "/")
		for i := range parts {
			p := strings.Join(parts[:i+1], "/")
			if p != "." && p != "" && !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	sort.Strings(out)
	return out
}

func (s *scaffolder) directories() error {
	for _, g := range s.globals() {
		dir := filepath.Join(s.o.Dir, filepath.FromSlash(g.Path))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		keep := filepath.Join(dir, ".gitkeep")
		if _, err := os.Stat(keep); errors.Is(err, fs.ErrNotExist) {
			if err := os.WriteFile(keep, nil, 0o644); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *scaffolder) settingsDo() error {
	gs := s.globals()
	content, err := s.render("settings.do.tmpl", map[string]any{
		"Name":    s.r.View.Name,
		"Globals": gs,
		"Folders": folders(gs),
	})
	if err != nil {
		return err
	}
	return s.write("settings.do", content, 0o644)
}

// ref returns the global for the first directory key that exists, or a
// path under $root.
func (s *scaffolder) ref(fallback string, keys ...string) string {
	for _, k := range keys {
		if _, ok := s.r.View.Directories[k]; ok {
			return "$" + k
		}
	}
	return "$root/" + fallback
}

// masterDo writes master.do, which runs the project's do-files in order.
// Starter do-files come only from a blueprint's files.
func (s *scaffolder) masterDo() error {
	v := s.r.View
	content, err := s.render("master.do.tmpl", map[string]any{
		"Name":  v.Name,
		"DoDir": s.ref("do", "do"),
		"Logs":  s.ref("logs", "logs"),
	})
	if err != nil {
		return err
	}
	return s.write("master.do", content, 0o644)
}

func (s *scaffolder) envFile() error {
	if len(s.r.View.Env) == 0 {
		return nil
	}
	keys := make([]string, 0, len(s.r.View.Env))
	for k := range s.r.View.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("# Environment for this project. This file is gitignored; never commit it.\n")
	for _, k := range keys {
		value := s.r.View.Env[k]
		if value == nil {
			value = ""
		}
		fmt.Fprintf(&b, "%s=%v\n", k, value)
	}
	return s.write(".env", b.String(), 0o600)
}

func (s *scaffolder) gitignore() error {
	return s.write(".gitignore", s.r.View.Git.GitignoreContent, 0o644)
}

// restricted lists every directory with a "private" segment, collapsed to
// that segment, as R does for the agent deny rules.
func (s *scaffolder) restricted() []string {
	seen := map[string]bool{}
	var out []string
	for _, g := range s.globals() {
		parts := strings.Split(g.Path, "/")
		for i, part := range parts {
			if part == "private" {
				p := strings.Join(parts[:i+1], "/")
				if !seen[p] {
					seen[p] = true
					out = append(out, p)
				}
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

func (s *scaffolder) ai() error {
	v := s.r.View
	var selected []string
	for name := range v.AI.Assistants {
		if _, ok := assistantTable[name]; ok {
			selected = append(selected, name)
		}
	}
	sort.Strings(selected)
	skillsDirs := []string{".agents/skills"}
	var stubs []string
	if len(selected) > 0 {
		skillsDirs = nil
		seen := map[string]bool{}
		for _, name := range selected {
			row := assistantTable[name]
			if !seen[row.skillsDir] {
				seen[row.skillsDir] = true
				skillsDirs = append(skillsDirs, row.skillsDir)
			}
			if row.stub != "" {
				stubs = append(stubs, row.stub)
			}
		}
	}

	if _, own := v.Files["AGENTS.md"]; v.AI.AgentsMD == "stub" && !own {
		restricted := make([]string, 0)
		for _, p := range s.restricted() {
			restricted = append(restricted, "`"+p+"/`")
		}
		first := "root"
		for _, k := range []string{"inputs_raw", "inputs_private_raw"} {
			if _, ok := v.Directories[k]; ok {
				first = k
				break
			}
		}
		if gs := s.globals(); first == "root" && len(gs) > 0 {
			first = gs[0].Name
		}
		content, err := s.render("AGENTS.md.tmpl", map[string]any{
			"Name": v.Name, "Sensitive": v.Security.Sensitive,
			"Restricted": strings.Join(restricted, ", "), "FirstGlobal": first,
		})
		if err != nil {
			return err
		}
		if err := s.write("AGENTS.md", content, 0o644); err != nil {
			return err
		}
	}
	for _, stub := range stubs {
		content := "@AGENTS.md\n"
		if stub != "CLAUDE.md" {
			content = pointerStub(v.Name)
		}
		if err := s.write(stub, content, 0o644); err != nil {
			return err
		}
	}

	names := make([]string, 0, len(v.Skills))
	for name := range v.Skills {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		entry := v.Skills[name]
		files := map[string]string{}
		if entry.Package {
			shipped, err := fs.Sub(skillFiles, "skills/"+name)
			if err == nil {
				err = fs.WalkDir(shipped, ".", func(p string, d fs.DirEntry, err error) error {
					if err != nil || d.IsDir() {
						return err
					}
					data, err := fs.ReadFile(shipped, p)
					files[p] = string(data)
					return err
				})
			}
			if err != nil || len(files) == 0 {
				s.res.Notes = append(s.res.Notes, fmt.Sprintf("Skipped skill %s: it ships with the R package, not with fw.", name))
				continue
			}
		} else {
			files["SKILL.md"] = entry.Content
			for p, c := range entry.Files {
				files[p] = c
			}
		}
		for _, dir := range skillsDirs {
			if entry.Package || entry.Sync != nil {
				bundle := entry.Bundle
				if len(bundle) == 0 {
					for p, c := range files {
						if p != ".framework-skill.json" {
							bundle = append(bundle, skillsync.File{Path: p, Content: base64.StdEncoding.EncodeToString([]byte(c))})
						}
					}
				}
				canonical := ""
				variant := "stata"
				server := ""
				var snapshot *skillsync.Snapshot
				if entry.Package {
					canonical = "framework:" + name
				}
				if entry.Sync != nil {
					variant = entry.Sync.Variant
					server = entry.Sync.Server
					snapshot = &skillsync.Snapshot{ID: entry.Sync.ID, Version: entry.Sync.Version, Hash: entry.Sync.Hash}
				}
				if err := skillsync.InstallSource(s.o.Dir, dir+"/"+name, canonical, variant, bundle, snapshot, server); err != nil {
					return err
				}
				s.res.Created = append(s.res.Created, dir+"/"+name+"/SKILL.md")
				continue
			}
			for p, c := range files {
				if err := s.write(dir+"/"+name+"/"+p, c, 0o644); err != nil {
					return err
				}
			}
		}
	}

	if v.Security.Sensitive && v.AI.Assistants["claude"] {
		return s.agentGuards()
	}
	return nil
}

func pointerStub(name string) string {
	return fmt.Sprintf("# %s\n\nProject instructions live in @AGENTS.md (the canonical AI context file) and in\nthe skills under `.claude/skills/`. Read AGENTS.md before working here; do not\nduplicate its content in this file.\n", name)
}

// agentGuards keeps Claude Code out of restricted folders and .env: deny
// rules for its file tools, and a fail-closed sandbox that also stops
// subprocesses such as Stata from reading them. The rules match R's.
func (s *scaffolder) agentGuards() error {
	restricted := s.restricted()
	deny := make([]string, 0, 2*len(restricted)+1)
	sandbox := make([]string, 0, len(restricted)+1)
	for _, p := range restricted {
		deny = append(deny, "Read(/"+p+"/**)", "Edit(/"+p+"/**)")
		sandbox = append(sandbox, "./"+p)
	}
	deny = append(deny, "Read(/.env)")
	sandbox = append(sandbox, "./.env")
	settings := map[string]any{
		"permissions": map[string]any{"deny": deny},
		"sandbox": map[string]any{
			"enabled":           true,
			"failIfUnavailable": true,
			"filesystem":        map[string]any{"denyRead": sandbox},
		},
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return s.write(".claude/settings.json", string(data)+"\n", 0o644)
}

var templateVar = regexp.MustCompile(`\{\{\s*(project_name|author|author_email|affiliation|date)\s*\}\}`)

// specFiles writes the spec's files last, so they replace generated files of
// the same name. Templates fill only the fixed variables, as in R.
func (s *scaffolder) specFiles() error {
	v := s.r.View
	vars := map[string]string{
		"project_name": v.Name,
		"author":       v.Author.Name,
		"author_email": v.Author.Email,
		"affiliation":  v.Author.Affiliation,
		"date":         s.o.Now.Format("2006-01-02"),
	}
	names := make([]string, 0, len(v.Files))
	for rel := range v.Files {
		names = append(names, rel)
	}
	sort.Strings(names)
	for _, rel := range names {
		entry := v.Files[rel]
		content := entry.Content
		if entry.Template {
			content = templateVar.ReplaceAllStringFunc(content, func(m string) string {
				return vars[templateVar.FindStringSubmatch(m)[1]]
			})
		}
		if err := s.write(rel, content, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// settingsYml writes settings.yml, which holds what settings.do cannot: the
// data catalog that fw_use and fw_save read, and the publishing destinations
// that fw_publish reads. Its directories match settings.do. Destinations
// from the layers are written out; otherwise a commented example shows one.
func (s *scaffolder) settingsYml() error {
	publish := ""
	if value, ok := s.r.Doc["publish"]; ok && value != nil {
		var b bytes.Buffer
		enc := yaml.NewEncoder(&b)
		enc.SetIndent(2)
		if err := enc.Encode(map[string]any{"publish": value}); err != nil {
			return err
		}
		if err := enc.Close(); err != nil {
			return err
		}
		lines := strings.Split(strings.TrimSuffix(b.String(), "\n"), "\n")
		for i, line := range lines {
			lines[i] = "  " + line
		}
		publish = strings.Join(lines, "\n")
	}
	catalog := "inputs/raw"
	for _, k := range []string{"inputs_raw", "inputs_private_raw"} {
		if p, ok := s.r.View.Directories[k]; ok {
			catalog = p
			break
		}
	}
	content, err := s.render("settings.yml.tmpl", map[string]any{
		"Name":        s.r.View.Name,
		"Globals":     s.globals(),
		"CatalogPath": catalog,
		"CatalogKeys": strings.Split(catalog, "/"),
		"CatalogName": strings.ReplaceAll(catalog, "/", "."),
		"Sensitive":   s.r.View.Security.Sensitive,
		"Publish":     publish,
	})
	if err != nil {
		return err
	}
	return s.write("settings.yml", content, 0o644)
}

func (s *scaffolder) record() error {
	data, err := json.MarshalIndent(s.r.Doc, "", "  ")
	if err != nil {
		return err
	}
	return s.write(".framework/spec.json", string(data)+"\n", 0o644)
}

// git makes the repository and the first commit. Before adding files it
// makes sure .env is ignored, whatever the layers' gitignore text says.
func (s *scaffolder) git() error {
	if _, err := exec.LookPath("git"); err != nil {
		s.res.Notes = append(s.res.Notes, "git is not installed, so the project has no repository yet.")
		return nil
	}
	run := func(args ...string) (string, error) {
		cmd := exec.Command("git", args...) // #nosec G204 -- fixed git subcommands
		cmd.Dir = s.o.Dir
		out, err := cmd.CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}
	if out, err := run("init", "-q"); err != nil {
		return fmt.Errorf("git init: %w: %s", err, out)
	}
	if _, err := run("check-ignore", "-q", ".env"); err != nil {
		f, err := os.OpenFile(filepath.Join(s.o.Dir, ".gitignore"), os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		_, werr := f.WriteString("\n# Secrets\n.env\n")
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			return werr
		}
	}
	// git takes the identity from GIT_AUTHOR_* and GIT_COMMITTER_*, then
	// from user.name and user.email.
	name, _ := run("config", "user.name")
	email, _ := run("config", "user.email")
	or := func(env, fallback string) string {
		if v := os.Getenv(env); v != "" {
			return v
		}
		return fallback
	}
	for _, v := range []string{or("GIT_AUTHOR_NAME", name), or("GIT_AUTHOR_EMAIL", email), or("GIT_COMMITTER_NAME", name), or("GIT_COMMITTER_EMAIL", email)} {
		if v == "" {
			s.res.Notes = append(s.res.Notes, "git user.name or user.email is not set, so fw made the repository without a first commit.")
			return nil
		}
	}
	if out, err := run("add", "-A"); err != nil {
		return fmt.Errorf("git add: %w: %s", err, out)
	}
	if out, err := run("commit", "-q", "-m", "Initial commit from Framework"); err != nil {
		return fmt.Errorf("git commit: %w: %s", err, out)
	}
	return nil
}

// CheckAdoptable returns an error when dir is already a Framework project.
// A config.yml counts only when it is Framework's own.
func CheckAdoptable(dir string) error {
	if found := adopt.Existing(dir); found != "" {
		return fmt.Errorf("%s is already a Framework project (found %s). Nothing was changed", dir, found)
	}
	return nil
}

// AdoptLayer is the layer fw adopt records for an existing Stata project: a
// bare project with exactly its mapped folders, and no AI files or git, as
// R's adopt() records.
func AdoptLayer(directories map[string]string, extras map[string]any, sensitive bool) map[string]any {
	dirs := map[string]any{}
	for k, v := range directories {
		dirs[k] = v
	}
	return map[string]any{
		"language":          "stata",
		"type":              "bare",
		"core_files":        false,
		"directories":       dirs,
		"extra_directories": extras,
		"git":               map[string]any{"use_git": false},
		"ai":                map[string]any{"enabled": false},
		"security":          map[string]any{"sensitive": sensitive},
	}
}

// adoptIgnores are the lines fw adopt makes sure a Stata project's
// .gitignore has: secrets and private folders. Data in other folders stays
// committable, as in new projects.
var adoptIgnores = []string{".env", ".env.*", "!.env.example", "**/private/**", "**/*_private/**"}

// AdoptGitignore lists the .gitignore lines for an adopted project: the
// secrets and private-folder rules, and each folder that looks private.
func AdoptGitignore(private []string) []string {
	lines := append([]string{}, adoptIgnores...)
	for _, p := range private {
		lines = append(lines, "/"+strings.Trim(p, "/")+"/")
	}
	return lines
}

// AdoptResult is what Adopt wrote.
type AdoptResult struct {
	Created   []string // files fw created
	Gitignore []string // lines added to .gitignore
}

// Adopt turns an existing folder into a Stata project by writing
// settings.do, settings.yml, and .framework/spec.json, and by adding the
// ignore lines .gitignore lacks. It creates, moves, and overwrites nothing
// else, never changes a line already in .gitignore, and changes nothing when
// the folder is already a project. The spec records the .gitignore as it is
// afterwards.
func Adopt(r Resolved, dir string, ignore []string) (AdoptResult, error) {
	if err := CheckAdoptable(dir); err != nil {
		return AdoptResult{}, err
	}
	s := &scaffolder{r: r, o: Options{Dir: dir}}
	var res AdoptResult
	for _, step := range []func() error{s.settingsDo, s.settingsYml} {
		if err := step(); err != nil {
			return AdoptResult{Created: s.res.Created}, err
		}
	}
	_, statErr := os.Stat(filepath.Join(dir, ".gitignore"))
	added, err := project.AppendGitignore(dir, "Added by fw adopt: secrets and private data stay out of git", ignore)
	if err != nil {
		return AdoptResult{Created: s.res.Created}, err
	}
	if errors.Is(statErr, fs.ErrNotExist) && len(added) > 0 {
		s.res.Created = append(s.res.Created, ".gitignore")
	}
	res.Gitignore = added
	if data, err := os.ReadFile(filepath.Join(dir, ".gitignore")); err == nil {
		git := asMap(r.Doc["git"])
		git["gitignore_content"] = string(data)
		r.Doc["git"] = git
	}
	if err := s.record(); err != nil {
		return AdoptResult{Created: s.res.Created}, err
	}
	res.Created = s.res.Created
	return res, nil
}
