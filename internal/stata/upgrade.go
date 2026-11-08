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
	Action  string   `json:"action"` // create, insert, append, update, or mkdir
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

// UpgradeOptions are fw project upgrade's choices for a Stata project.
type UpgradeOptions struct {
	// Type changes the project's type: project, project_sensitive, or
	// bare, as R's upgrade(type =) does. Empty keeps it.
	Type string
}

// PlanUpgrade works out what upgrading the Stata project at root would
// change, without changing anything.
func PlanUpgrade(root, fwVersion string, o UpgradeOptions) (UpgradePlan, error) {
	plan := UpgradePlan{Changes: []UpgradeChange{}, Notes: []string{}}
	// Several steps can change one file; each builds on the content the
	// steps before planned, and the plan has one change per file.
	planned := map[string]int{}
	current := func(rel, onDisk string) string {
		if i, ok := planned[rel]; ok {
			return plan.Changes[i].content
		}
		return onDisk
	}
	change := func(c UpgradeChange) {
		i, ok := planned[c.File]
		if !ok {
			planned[c.File] = len(plan.Changes)
			plan.Changes = append(plan.Changes, c)
			return
		}
		prev := &plan.Changes[i]
		prev.content = c.content
		prev.Summary += "; " + c.Summary
		prev.Lines = append(prev.Lines, c.Lines...)
		if prev.Action != "create" && prev.Action != c.Action {
			prev.Action = "update"
		}
	}
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
	var did []string
	specAction := "update"
	if hasSpec {
		var doc map[string]any
		if err := json.Unmarshal([]byte(specText), &doc); err != nil {
			return plan, fmt.Errorf(".framework/spec.json is not valid JSON: %w", err)
		}
		if resolved, err = FromSpec(doc); err != nil {
			return plan, fmt.Errorf(".framework/spec.json: %w", err)
		}
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
	} else {
		if resolved, err = rebuildSpec(root, settingsDo, hasMaster, fwVersion); err != nil {
			return plan, err
		}
		specAction = "create"
		summary := fmt.Sprintf("record the project's spec (type %s, %d folders from settings.do)", resolved.View.Type, len(resolved.View.Directories)+len(resolved.View.ExtraDirectories))
		if quarto {
			setQuarto(&resolved)
			summary += ", with tools.quarto: true for its Quarto files"
		}
		did = append(did, summary)
	}
	// --type: the new type, and for project_sensitive the guards and the
	// private and public folders, as R's upgrade(type =) does
	var added []global
	if o.Type != "" {
		d, a, notes := convertType(&resolved, o.Type)
		did, added = append(did, d...), a
		plan.Notes = append(plan.Notes, notes...)
	}
	if len(did) > 0 {
		data, err := json.MarshalIndent(resolved.Doc, "", "  ")
		if err != nil {
			return plan, err
		}
		change(UpgradeChange{File: ".framework/spec.json", Action: specAction, Summary: strings.Join(did, "; "), content: string(data) + "\n"})
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
		change(UpgradeChange{File: "settings.yml", Action: "create",
			Summary: "the settings framework-stata 0.2.0 reads: project name, folders, data catalog, and publishing", content: b.String()})
	} else {
		if len(added) > 0 {
			if c, note := directoriesChange(settingsYml, added); c != nil {
				change(*c)
			} else {
				plan.Notes = append(plan.Notes, note)
			}
		}
		if sensitive {
			if c, note := securityChange(current("settings.yml", settingsYml)); c != nil {
				change(*c)
			} else if note != "" {
				plan.Notes = append(plan.Notes, note)
			}
		}
	}

	// 3. settings.do's header names settings.yml.
	switch {
	case strings.Contains(settingsDo, "settings.yml lists the same folders"):
	case strings.Contains(settingsDo, oldHeaderLine):
		content, ok := insertAfter(settingsDo, func(l string) bool { return strings.TrimSpace(l) == oldHeaderLine }, headerNote)
		if ok {
			change(UpgradeChange{File: "settings.do", Action: "insert",
				Summary: "say in the header that settings.yml lists the same folders", Lines: headerNote, content: content})
		}
	default:
		plan.Notes = append(plan.Notes, "settings.do's header is not fw's, so fw left it alone. Folders change in settings.do and settings.yml together.")
	}
	// The folders --type added get their globals, and mkdir lines when
	// settings.do makes its folders.
	if len(added) > 0 {
		if c, note := globalsChange(current("settings.do", settingsDo), added); c != nil {
			change(*c)
		} else {
			plan.Notes = append(plan.Notes, note)
		}
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

	// 5. Private folders git doesn't keep, missing in a fresh checkout or
	// added by the settings, are created.
	for _, p := range MissingPrivateFolders(root, resolved.View) {
		plan.Changes = append(plan.Changes, UpgradeChange{File: p + "/", Action: "mkdir",
			Summary: "create the private folder the settings name (git doesn't keep it)"})
	}

	// 6. .gitignore gains the rules it lacks: the current template's for a
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

	// 7. A sensitive project keeps Claude Code out of its private folders,
	// whatever its AI settings say, as R's upgrade() does.
	if sensitive {
		text, has, err := read(".claude/settings.json")
		if err != nil {
			return plan, err
		}
		c, note, err := guardChange(&scaffolder{r: resolved}, text, has)
		if err != nil {
			return plan, err
		}
		if c != nil {
			change(*c)
			if !resolved.View.AI.Enabled || !resolved.View.AI.Assistants["claude"] {
				plan.Notes = append(plan.Notes, "The project's AI settings leave Claude Code out; fw adds its guard anyway, so it stays out of the private folders if someone uses it.")
			}
		}
		if note != "" {
			plan.Notes = append(plan.Notes, note)
		}
	}
	return plan, nil
}

// convertType changes the project's type in the resolved spec, as R's
// upgrade(type =) does. To project_sensitive it turns security.sensitive
// on and adds the project_sensitive layout's private and public folders
// whose keys and paths the project doesn't have yet; existing folders stay,
// so code that names them keeps working. From a sensitive project to
// another type, the flag stays on, with a note. It returns what it did, the
// folders it added, and notes.
func convertType(r *Resolved, kind string) (did []string, added []global, notes []string) {
	if r.View.Type != kind {
		did = append(did, fmt.Sprintf("set type to %s (was %s)", kind, r.View.Type))
		r.Doc["type"], r.View.Type = kind, kind
	}
	if kind != "project_sensitive" {
		if r.View.Security.Sensitive {
			notes = append(notes, "The project stays sensitive (security: sensitive: true): fw doesn't turn Framework's guards off with a type. Set it to false in settings.yml and the spec by hand if the project holds no restricted data.")
		}
		return did, nil, notes
	}
	if !r.View.Security.Sensitive {
		security := asMap(r.Doc["security"])
		security["sensitive"] = true
		r.Doc["security"] = security
		r.View.Security.Sensitive = true
		did = append(did, "set security.sensitive to true")
	}
	dirs := asMap(r.Doc["directories"])
	paths := map[string]bool{}
	for _, p := range r.View.Directories {
		paths[p] = true
	}
	for _, e := range r.View.ExtraDirectories {
		paths[e.Path] = true
	}
	layout := asMap(defaults("project_sensitive")["directories"])
	keys := make([]string, 0, len(layout))
	for k := range layout {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var names []string
	for _, k := range keys {
		p, _ := layout[k].(string)
		_, isDir := r.View.Directories[k]
		_, isExtra := r.View.ExtraDirectories[k]
		if !strings.Contains(k, "_private_") && !strings.Contains(k, "_public_") || isDir || isExtra || paths[p] {
			continue
		}
		dirs[k] = p
		if r.View.Directories == nil {
			r.View.Directories = map[string]string{}
		}
		r.View.Directories[k] = p
		paths[p] = true
		added = append(added, global{k, p})
		names = append(names, k)
	}
	r.Doc["directories"] = dirs
	if len(added) > 0 {
		did = append(did, "add the private and public folders of project_sensitive: "+strings.Join(names, ", "))
	}
	return did, added, notes
}

// directoriesChange inserts the added folders at the end of settings.yml's
// directories: block, or says why it can't.
func directoriesChange(text string, added []global) (*UpgradeChange, string) {
	var lines []string
	for _, g := range added {
		lines = append(lines, "    "+g.Name+": "+yamlScalar(g.Path))
	}
	byHand := "settings.yml's directories: section isn't a block fw can add to, so add these under default: directories: by hand:\n  " + strings.Join(lines, "\n  ")
	all := strings.Split(text, "\n")
	start := -1
	for i, l := range all {
		if strings.TrimRight(l, " \r") == "  directories:" {
			start = i
			break
		}
	}
	if start < 0 {
		return nil, byHand
	}
	last := start
	for i := start + 1; i < len(all); i++ {
		if !strings.HasPrefix(all[i], "    ") {
			break
		}
		last = i
	}
	out := append(append(append([]string{}, all[:last+1]...), lines...), all[last+1:]...)
	content := strings.Join(out, "\n")
	var check struct {
		Default struct {
			Directories map[string]string `yaml:"directories"`
		} `yaml:"default"`
	}
	if yaml.Unmarshal([]byte(content), &check) != nil {
		return nil, byHand
	}
	for _, g := range added {
		if check.Default.Directories[g.Name] != g.Path {
			return nil, byHand
		}
	}
	return &UpgradeChange{File: "settings.yml", Action: "insert", Summary: "list the folders project_sensitive adds", Lines: lines, content: content}, ""
}

var mkdirLine = regexp.MustCompile("^capture mkdir `\"\\$root/(.+)\"'\\s*$")

// globalsChange inserts the added folders' globals after settings.do's last
// folder global, and, when settings.do makes its folders, mkdir lines for
// the ones it lacks after its last mkdir line.
func globalsChange(text string, added []global) (*UpgradeChange, string) {
	var globals []string
	for _, g := range added {
		globals = append(globals, fmt.Sprintf("global %s `\"$root/%s\"'", g.Name, g.Path))
	}
	all := strings.Split(text, "\n")
	lastGlobal, lastMkdir := -1, -1
	made := map[string]bool{}
	for i, l := range all {
		l = strings.TrimRight(l, "\r")
		if m := globalLine.FindStringSubmatch(l); m != nil && m[1] != "root" {
			lastGlobal = i
		}
		if m := mkdirLine.FindStringSubmatch(l); m != nil {
			lastMkdir = i
			made[m[1]] = true
		}
	}
	if lastGlobal < 0 {
		return nil, "settings.do has no folder globals fw recognizes, so add these by hand:\n  " + strings.Join(globals, "\n  ")
	}
	var mkdirs []string
	if lastMkdir > lastGlobal {
		for _, f := range folders(added) {
			if !made[f] {
				mkdirs = append(mkdirs, fmt.Sprintf("capture mkdir `\"$root/%s\"'", f))
			}
		}
	}
	out := append([]string{}, all[:lastGlobal+1]...)
	out = append(out, globals...)
	if len(mkdirs) > 0 {
		out = append(out, all[lastGlobal+1:lastMkdir+1]...)
		out = append(out, mkdirs...)
		out = append(out, all[lastMkdir+1:]...)
	} else {
		out = append(out, all[lastGlobal+1:]...)
	}
	return &UpgradeChange{File: "settings.do", Action: "insert", Summary: "add globals for the folders project_sensitive adds",
		Lines: append(globals, mkdirs...), content: strings.Join(out, "\n")}, ""
}

// guardChange adds what .claude/settings.json lacks of the guard that keeps
// Claude Code out of the project's private folders and .env: deny rules for
// its file tools and the sandbox's denyRead list. Other settings stay.
func guardChange(s *scaffolder, text string, has bool) (*UpgradeChange, string, error) {
	restricted := s.restricted()
	if len(restricted) == 0 {
		return nil, "", nil
	}
	settings := map[string]any{}
	if has {
		if err := json.Unmarshal([]byte(text), &settings); err != nil {
			return nil, ".claude/settings.json isn't valid JSON, so fw did not add the guard that keeps Claude Code out of " + strings.Join(restricted, ", ") + ".", nil
		}
	}
	var deny, sandbox []string
	for _, p := range restricted {
		deny = append(deny, "Read(/"+p+"/**)", "Edit(/"+p+"/**)")
		sandbox = append(sandbox, "./"+p)
	}
	deny = append(deny, "Read(/.env)")
	sandbox = append(sandbox, "./.env")
	addTo := func(list any, items []string) ([]any, []string) {
		have, _ := list.([]any)
		var added []string
		for _, it := range items {
			found := false
			for _, h := range have {
				found = found || h == it
			}
			if !found {
				have = append(have, it)
				added = append(added, it)
			}
		}
		return have, added
	}
	permissions := asMap(settings["permissions"])
	var newDeny []string
	permissions["deny"], newDeny = addTo(permissions["deny"], deny)
	settings["permissions"] = permissions
	box, hadBox := settings["sandbox"].(map[string]any)
	if !hadBox {
		box = map[string]any{"enabled": true, "failIfUnavailable": true}
	}
	fsys := asMap(box["filesystem"])
	var newRead []string
	fsys["denyRead"], newRead = addTo(fsys["denyRead"], sandbox)
	box["filesystem"] = fsys
	settings["sandbox"] = box
	if len(newDeny)+len(newRead) == 0 {
		return nil, "", nil
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return nil, "", err
	}
	action := "update"
	if !has {
		action = "create"
	}
	return &UpgradeChange{File: ".claude/settings.json", Action: action,
		Summary: "keep Claude Code out of " + strings.Join(restricted, ", "), Lines: append(newDeny, newRead...), content: string(data) + "\n"}, "", nil
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
			if c.Action == "mkdir" {
				continue
			}
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
		if c.Action == "mkdir" {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return backupDir, err
			}
			continue
		}
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
