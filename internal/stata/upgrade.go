package stata

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/frameworktools/framework-cli/internal/adopt"
	"github.com/frameworktools/framework-cli/internal/project"
	"github.com/frameworktools/framework-cli/internal/spec"
)

// A Stata project made by an older fw, or adopted by one, lacks some of
// what fw writes today: .framework/spec.json (before adopt and specs),
// settings.yml (fw 0.4.5 and earlier), the settings.do header that names
// settings.yml, master.do's check for framework-stata and its fw_project
// call, and .gitignore rules. Upgrade adds them. It never removes or changes
// a line of a text file: it creates missing files, inserts lines, and
// appends to .gitignore. Every file it changes is backed up first.

// UpgradeChange is one change an upgrade makes to one file.
type UpgradeChange struct {
	File    string   `json:"file"`
	Action  string   `json:"action"` // create, insert, append, or update
	Summary string   `json:"summary"`
	Lines   []string `json:"lines,omitempty"` // lines added, for the plan
	content string   // the file's new content
}

// UpgradePlan is what an upgrade would do, and what it leaves for people.
type UpgradePlan struct {
	Changes []UpgradeChange `json:"changes"`
	Notes   []string        `json:"notes"`
}

// oldHeaderLine is settings.do's header line from fw 0.4.5 and earlier,
// when nothing in Stata read settings.yml.
const oldHeaderLine = "* Stata cannot read the project's YAML settings, so the paths live here."

// headerNote follows oldHeaderLine in an upgraded settings.do.
var headerNote = []string{
	"* The line above is out of date: framework-stata 0.2.0 and later read the",
	"* YAML settings too. settings.yml lists the same folders and holds the rest:",
	"* the project name, the data catalog for fw_use and fw_save, the publishing",
	"* destinations for fw_publish, and the security settings. Framework for",
	"* Stata (fw_project) reads both files. When both set a folder, settings.yml",
	"* wins: fw_project prints a note and lists the folder in r(conflicts).",
	"* Change a folder here and in settings.yml.",
}

// projectBlock loads the project in master.do after settings.do, as
// master.do.tmpl does.
var projectBlock = []string{
	"",
	"* fw_project reads settings.do and settings.yml, and sets $fw_root,",
	"* $fw_project_name, and an $fw_ global for each folder that fw_use, fw_save,",
	"* fw_table, fw_graph, and fw_publish use. It comes with framework-stata 0.2.0",
	"* or later, the first with fw_table.",
	"capture which fw_table",
	"if _rc {",
	`    display as error "master.do needs framework-stata 0.2.0 or later. Install or update it with:"`,
	`    display as error ` + "`" + `"  net install framework, from("https://raw.githubusercontent.com/frameworktools/framework-stata/main") replace"'`,
	"    exit 199",
	"}",
	"fw_project",
}

var globalLine = regexp.MustCompile("^global ([A-Za-z_][A-Za-z0-9_]*) `\"\\$root/(.+)\"'\\s*$")

// SettingsGlobals reads the folder globals from settings.do: name to path.
func SettingsGlobals(text string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		if m := globalLine.FindStringSubmatch(strings.TrimRight(line, "\r")); m != nil && m[1] != "root" {
			out[m[1]] = m[2]
		}
	}
	return out
}

// knownKeys are Framework directory keys; other globals become extra
// directories in a rebuilt spec.
func knownKeys() map[string]bool {
	known := map[string]bool{}
	for _, k := range adopt.Shared().Keys("stata", "project") {
		known[k] = true
	}
	for _, kind := range Types {
		for k := range asMap(defaults(kind)["directories"]) {
			known[k] = true
		}
	}
	return known
}

// PlanUpgrade works out what upgrading the Stata project at root would
// change, without changing anything.
func PlanUpgrade(root, fwVersion string) (UpgradePlan, error) {
	plan := UpgradePlan{Changes: []UpgradeChange{}, Notes: []string{}}
	read := func(rel string) (string, bool, error) {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if errors.Is(err, fs.ErrNotExist) {
			return "", false, nil
		}
		return string(data), err == nil, err
	}
	settingsDo, ok, err := read("settings.do")
	if err != nil {
		return plan, err
	}
	if !ok {
		return plan, errors.New("this Stata project has no settings.do. fw upgrades Stata projects made or adopted by fw, which all have one")
	}
	_, hasMaster, err := read("master.do")
	if err != nil {
		return plan, err
	}

	// 1. The spec: rebuilt from settings.do when missing.
	specText, hasSpec, err := read(".framework/spec.json")
	if err != nil {
		return plan, err
	}
	var resolved Resolved
	quarto := adopt.Shared().DetectQuarto(root)
	if hasSpec {
		var doc map[string]any
		if err := json.Unmarshal([]byte(specText), &doc); err != nil {
			return plan, fmt.Errorf(".framework/spec.json is not valid JSON: %w", err)
		}
		if resolved, err = FromSpec(doc); err != nil {
			return plan, fmt.Errorf(".framework/spec.json: %w", err)
		}
		var did []string
		if doc["type"] == "project_sensitive" && !resolved.View.Security.Sensitive {
			security := asMap(doc["security"])
			security["sensitive"] = true
			doc["security"] = security
			resolved.View.Security.Sensitive = true
			did = append(did, "set security.sensitive to true, as the project_sensitive type says")
		}
		if quarto && !resolved.View.Tools["quarto"] {
			setQuarto(&resolved)
			did = append(did, "record tools.quarto: true (the project has Quarto files)")
		}
		if len(did) > 0 {
			data, err := json.MarshalIndent(doc, "", "  ")
			if err != nil {
				return plan, err
			}
			plan.Changes = append(plan.Changes, UpgradeChange{File: ".framework/spec.json", Action: "update",
				Summary: strings.Join(did, "; "), content: string(data) + "\n"})
		}
	} else {
		if resolved, err = rebuildSpec(root, settingsDo, hasMaster, fwVersion); err != nil {
			return plan, err
		}
		summary := fmt.Sprintf("record the project's spec (type %s, %d folders from settings.do)", resolved.View.Type, len(resolved.View.Directories)+len(resolved.View.ExtraDirectories))
		if quarto {
			setQuarto(&resolved)
			summary += ", with tools.quarto: true for its Quarto files"
		}
		data, err := json.MarshalIndent(resolved.Doc, "", "  ")
		if err != nil {
			return plan, err
		}
		plan.Changes = append(plan.Changes, UpgradeChange{File: ".framework/spec.json", Action: "create",
			Summary: summary, content: string(data) + "\n"})
	}
	sensitive := resolved.View.Security.Sensitive

	// 2. settings.yml: written when missing; security added when sensitive.
	settingsYml, hasYml, err := read("settings.yml")
	if err != nil {
		return plan, err
	}
	if !hasYml {
		s := &scaffolder{r: resolved, o: Options{Dir: root}}
		var b strings.Builder
		if err := s.renderSettingsYml(&b); err != nil {
			return plan, err
		}
		plan.Changes = append(plan.Changes, UpgradeChange{File: "settings.yml", Action: "create",
			Summary: "the settings framework-stata 0.2.0 reads: project name, folders, data catalog, and publishing", content: b.String()})
	} else if sensitive {
		if change, note := securityChange(settingsYml); change != nil {
			plan.Changes = append(plan.Changes, *change)
		} else if note != "" {
			plan.Notes = append(plan.Notes, note)
		}
	}

	// 3. settings.do's header names settings.yml.
	switch {
	case strings.Contains(settingsDo, "settings.yml lists the same folders"):
	case strings.Contains(settingsDo, oldHeaderLine):
		content, ok := insertAfter(settingsDo, func(l string) bool { return strings.TrimSpace(l) == oldHeaderLine }, headerNote)
		if ok {
			plan.Changes = append(plan.Changes, UpgradeChange{File: "settings.do", Action: "insert",
				Summary: "say in the header that settings.yml lists the same folders", Lines: headerNote, content: content})
		}
	default:
		plan.Notes = append(plan.Notes, "settings.do's header is not fw's, so fw left it alone. Folders change in settings.do and settings.yml together.")
	}

	// 4. master.do loads the project through framework-stata 0.2.0 or later.
	if master, _, _ := read("master.do"); hasMaster {
		switch {
		case strings.Contains(master, "capture which fw_table"):
		case strings.Contains(master, "capture which fw_project"):
			// An unreleased fw checked for fw_project only: add the fw_table check after it.
			lines := strings.Split(master, "\n")
			start := -1
			for i, l := range lines {
				if strings.TrimSpace(l) == "capture which fw_project" {
					start = i
					break
				}
			}
			end := -1
			for i := start + 1; start >= 0 && i < len(lines); i++ {
				if strings.TrimSpace(lines[i]) == "}" {
					end = i
					break
				}
			}
			if end < 0 {
				plan.Notes = append(plan.Notes, "master.do checks for fw_project in a way fw does not recognize, so fw left it alone. framework-stata 0.2.0 or later is needed for fw_table.")
				break
			}
			add := projectBlock[5:11] // the fw_table check
			add = append([]string{"* fw_table and fw_graph need framework-stata 0.2.0 or later."}, add...)
			content := strings.Join(append(append(append([]string{}, lines[:end+1]...), add...), lines[end+1:]...), "\n")
			plan.Changes = append(plan.Changes, UpgradeChange{File: "master.do", Action: "insert",
				Summary: "check for framework-stata 0.2.0 or later (fw_table)", Lines: add, content: content})
		case regexp.MustCompile(`(?m)^\s*fw_project\b`).MatchString(master):
			plan.Notes = append(plan.Notes, "master.do already runs fw_project, so fw left it alone.")
		default:
			content, ok := insertAfter(master, func(l string) bool { return strings.TrimSpace(l) == `do "settings.do"` }, projectBlock)
			if !ok {
				plan.Notes = append(plan.Notes, `master.do has no line do "settings.do", so fw did not add the fw_project call. Add it after the line that runs settings.do.`)
				break
			}
			plan.Changes = append(plan.Changes, UpgradeChange{File: "master.do", Action: "insert",
				Summary: "load the project with fw_project, after checking for framework-stata 0.2.0 or later", Lines: projectBlock[1:], content: content})
		}
	}

	// 5. .gitignore gains the rules it lacks: the current template's for a
	// project fw made, and adopt's (secrets and private folders) for an
	// adopted one, whose own rules for logs and caches stay its choice.
	want := strings.Split(gitignore(sensitive, nil), "\n")
	if adopted, _ := asMap(resolved.Doc["source"])["adopted"].(bool); adopted {
		var private []string
		s := &scaffolder{r: resolved}
		private = append(private, s.restricted()...)
		want = AdoptGitignore(private)
	}
	want = append(want, "/.framework/backups/")
	missing, leftOut, err := project.MissingGitignoreLines(root, want)
	if err != nil {
		return plan, err
	}
	if len(leftOut) > 0 {
		plan.Notes = append(plan.Notes, fmt.Sprintf(".gitignore: left out %s. Added after your own rules, they would win over them and could make files in folders you ignore (README files in data folders) committable.",
			strings.Join(leftOut, " ")))
	}
	if len(missing) > 0 {
		current, _, _ := read(".gitignore")
		content := current
		if content != "" && !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		if content != "" {
			content += "\n"
		}
		content += "# " + upgradeIgnoreHeader + "\n" + strings.Join(missing, "\n") + "\n"
		action := "append"
		if current == "" {
			action = "create"
		}
		plan.Changes = append(plan.Changes, UpgradeChange{File: ".gitignore", Action: action,
			Summary: "add the rules fw's Stata projects have now; no line is removed", Lines: missing, content: content})
	}
	return plan, nil
}

const upgradeIgnoreHeader = "Added by fw project upgrade"

// rebuildSpec makes the spec of a project that has none from settings.do:
// its globals become the folders. A folder with a private segment makes the
// project sensitive, and master.do makes it a project rather than bare.
func rebuildSpec(root, settingsDo string, hasMaster bool, fwVersion string) (Resolved, error) {
	known := knownKeys()
	dirs := map[string]any{}
	extras := map[string]any{}
	sensitive := false
	for name, p := range SettingsGlobals(settingsDo) {
		if known[name] {
			dirs[name] = p
		} else {
			extras[name] = map[string]any{"label": path.Base(p), "path": p}
		}
		for _, seg := range strings.Split(p, "/") {
			sensitive = sensitive || seg == "private"
		}
	}
	kind := "bare"
	if hasMaster {
		kind = "project"
		if sensitive {
			kind = "project_sensitive"
		}
	}
	name := filepath.Base(root)
	if data, err := os.ReadFile(filepath.Join(root, "settings.yml")); err == nil {
		var doc struct {
			Default struct {
				ProjectName string `yaml:"project_name"`
			} `yaml:"default"`
		}
		if yaml.Unmarshal(data, &doc) == nil && doc.Default.ProjectName != "" {
			name = doc.Default.ProjectName
		}
	}
	layer := map[string]any{
		"language": "stata", "type": "bare", "core_files": hasMaster,
		"directories": dirs, "extra_directories": extras,
		"security": map[string]any{"sensitive": sensitive},
		"git":      map[string]any{"use_git": isDir(filepath.Join(root, ".git"))},
		"ai":       aiLayer(root),
	}
	raw, err := json.Marshal(layer)
	if err != nil {
		return Resolved{}, err
	}
	env := spec.Envelope{SpecVersion: 1, Name: name, Layers: []spec.Entry{{Kind: "project", Key: "upgraded", Layer: raw}},
		// spec.v1's source has no field for an upgrade yet (upgraded_from)
		Source: json.RawMessage(`{"origin":"cli"}`)}
	r, err := Resolve(env, fwVersion)
	if err != nil {
		return Resolved{}, err
	}
	// The layer resolves as bare, so no built-in folders are added.
	r.Doc["type"], r.View.Type = kind, kind
	r.Doc["skills"] = map[string]any{}
	return r, nil
}

// aiLayer describes the AI files an older fw wrote: AGENTS.md, and
// CLAUDE.md for Claude Code.
func aiLayer(root string) map[string]any {
	exists := func(rel string) bool {
		_, err := os.Stat(filepath.Join(root, rel))
		return err == nil
	}
	if !exists("AGENTS.md") {
		return map[string]any{"enabled": false}
	}
	assistants := map[string]any{}
	if exists("CLAUDE.md") || exists(".claude") {
		assistants["claude"] = true
	}
	return map[string]any{"enabled": true, "assistants": assistants, "agents_md": "stub"}
}

func isDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

// securityChange appends security: sensitive: true under default: in
// settings.yml when it has no security section. It works only when default:
// is the file's last top-level key, so appending lands inside it, and checks
// the result. Otherwise it returns a note for people to add it.
func securityChange(text string) (*UpgradeChange, string) {
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		return nil, "settings.yml could not be read as YAML, so fw did not add security: sensitive: true. Add it under default: by hand."
	}
	def, _ := doc["default"].(map[string]any)
	if def == nil {
		return nil, "settings.yml has no default: section, so fw did not add security: sensitive: true. Add it under default: by hand."
	}
	if _, ok := def["security"]; ok {
		return nil, ""
	}
	add := []string{"", "  # This project holds restricted data: fw_table and fw_graph save to the", "  # private folders unless told public.", "  security:", "    sensitive: true"}
	content := text
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += strings.Join(add[1:], "\n") + "\n"
	var check struct {
		Default struct {
			Security struct {
				Sensitive bool `yaml:"sensitive"`
			} `yaml:"security"`
		} `yaml:"default"`
	}
	if yaml.Unmarshal([]byte(content), &check) != nil || !check.Default.Security.Sensitive {
		return nil, "settings.yml's layout kept fw from adding security: sensitive: true safely. Add it under default: by hand."
	}
	return &UpgradeChange{File: "settings.yml", Action: "append", Summary: "turn on the sensitive-data guards, as the spec says",
		Lines: add[1:], content: content}, ""
}

// insertAfter inserts lines after the first line that match accepts.
func insertAfter(text string, match func(string) bool, lines []string) (string, bool) {
	all := strings.Split(text, "\n")
	for i, l := range all {
		if match(strings.TrimRight(l, "\r")) {
			out := append(append(append([]string{}, all[:i+1]...), lines...), all[i+1:]...)
			return strings.Join(out, "\n"), true
		}
	}
	return text, false
}

// ApplyUpgrade makes the plan's changes in root. Unless backup is false,
// each file it changes is first copied to .framework/backups/<timestamp>/,
// whose folder it returns ("" when nothing was backed up).
func ApplyUpgrade(root string, plan UpgradePlan, backup bool, now time.Time) (string, error) {
	backupDir := ""
	if backup {
		var existing []string
		for _, c := range plan.Changes {
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(c.File))); err == nil {
				existing = append(existing, c.File)
			}
		}
		sort.Strings(existing)
		if len(existing) > 0 {
			backupDir = path.Join(".framework", "backups", now.UTC().Format("20060102T150405Z"))
			for _, rel := range existing {
				data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
				if err != nil {
					return "", err
				}
				target := filepath.Join(root, filepath.FromSlash(backupDir), filepath.FromSlash(rel))
				if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
					return "", err
				}
				if err := os.WriteFile(target, data, 0o600); err != nil {
					return "", err
				}
			}
		}
	}
	for _, c := range plan.Changes {
		target := filepath.Join(root, filepath.FromSlash(c.File))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return backupDir, err
		}
		mode := os.FileMode(0o644)
		if info, err := os.Stat(target); err == nil {
			mode = info.Mode().Perm()
		}
		if err := os.WriteFile(target, []byte(c.content), mode); err != nil {
			return backupDir, fmt.Errorf("writing %s: %w", c.File, err)
		}
	}
	return backupDir, nil
}

// setQuarto records tools.quarto: true, for a project with Quarto files
// (adopt.Rules.DetectQuarto).
func setQuarto(r *Resolved) {
	tools := asMap(r.Doc["tools"])
	tools["quarto"] = true
	r.Doc["tools"] = tools
	if r.View.Tools == nil {
		r.View.Tools = map[string]bool{}
	}
	r.View.Tools["quarto"] = true
}
