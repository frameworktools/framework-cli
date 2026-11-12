package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frameworktools/framework-cli/internal/engine"
	"github.com/frameworktools/framework-cli/internal/stata"
)

// oldStataProject writes a Stata project as fw 0.4.5 made it: no
// settings.yml or spec, the old settings.do header, and a master.do that
// does not load the project. .gitignore has a line of the user's own.
func oldStataProject(t *testing.T, root string) string {
	t.Helper()
	dir := filepath.Join(root, "trial")
	writeTestFile(t, filepath.Join(dir, "settings.do"), "* settings.do: paths for trial\n*\n"+
		"* fw wrote this file from the project spec. Every do-file refers to folders\n"+
		"* through these globals, so the project runs on any machine without edits.\n"+
		"* Stata cannot read the project's YAML settings, so the paths live here.\n*\n"+
		"global root `\"`c(pwd)'\"'\n\n* Folders\n"+
		"global do `\"$root/do\"'\nglobal inputs_raw `\"$root/inputs/raw\"'\nglobal logs `\"$root/logs\"'\nglobal irb `\"$root/IRB\"'\n")
	writeTestFile(t, filepath.Join(dir, "master.do"), "* master.do: runs every step of trial in order\n\nversion 16\nclear all\nset more off\n\n"+
		"do \"settings.do\"\n\ncapture log close master\n* my step\ndo \"$do/clean.do\"\n")
	writeTestFile(t, filepath.Join(dir, ".gitignore"), "# mine\n*.dta\n*.log\n")
	writeTestFile(t, filepath.Join(dir, "do", "clean.do"), "use x\n")
	return dir
}

// keeps reports whether every line of before is still in after, in order.
func keeps(before, after string) bool {
	rest := strings.Split(after, "\n")
	for _, line := range strings.Split(before, "\n") {
		i := 0
		for i < len(rest) && rest[i] != line {
			i++
		}
		if i == len(rest) {
			return false
		}
		rest = rest[i+1:]
	}
	return true
}

func TestUpgradeStata(t *testing.T) {
	h := newHarness(t, nil)
	dir := oldStataProject(t, h.app.home)
	h.app.cwd = dir
	before := map[string]string{}
	for _, f := range []string{"settings.do", "master.do", ".gitignore"} {
		before[f] = readTestFile(t, filepath.Join(dir, f))
	}

	// A dry run prints the plan and changes nothing.
	out, errOut, err := h.run(t, "", "project", "upgrade")
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	for _, want := range []string{"create  .framework/spec.json", "create  settings.yml", "insert  settings.do", "insert  master.do", "append  .gitignore", "+ fw_project", "Nothing was changed"} {
		if !strings.Contains(out, want) {
			t.Errorf("plan lacks %q:\n%s", want, out)
		}
	}
	for f, text := range before {
		if readTestFile(t, filepath.Join(dir, f)) != text {
			t.Errorf("the dry run changed %s", f)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "settings.yml")); err == nil {
		t.Error("the dry run wrote settings.yml")
	}

	// --apply makes the changes, keeping every line, and backs up the files.
	out, errOut, err = h.run(t, "", "project", "upgrade", "--apply")
	if err != nil || !strings.Contains(out, ".framework/backups/") {
		t.Fatalf("%v\n%s%s", err, out, errOut)
	}
	for f, text := range before {
		after := readTestFile(t, filepath.Join(dir, f))
		if !keeps(text, after) || after == text {
			t.Errorf("%s lost a line or did not change:\n%s", f, after)
		}
	}
	master := readTestFile(t, filepath.Join(dir, "master.do"))
	if !strings.Contains(master, "do \"settings.do\"\n\n* fw_project reads") || !strings.Contains(master, "capture which fw_table") || strings.Index(master, "fw_project\n") > strings.Index(master, "capture log close") {
		t.Errorf("master.do:\n%s", master)
	}
	if do := readTestFile(t, filepath.Join(dir, "settings.do")); !strings.Contains(do, "so the paths live here.\n* The line above is out of date") {
		t.Errorf("settings.do:\n%s", do)
	}
	yml := readTestFile(t, filepath.Join(dir, "settings.yml"))
	if !strings.Contains(yml, "project_name: trial") || !strings.Contains(yml, "inputs_raw: inputs/raw") || !strings.Contains(yml, "irb: IRB") {
		t.Errorf("settings.yml:\n%s", yml)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(readTestFile(t, filepath.Join(dir, ".framework", "spec.json"))), &doc); err != nil {
		t.Fatal(err)
	}
	if _, err := stata.FromSpec(doc); err != nil || doc["type"] != "project" || doc["name"] != "trial" {
		t.Errorf("spec: %v %v", err, doc)
	}
	ignore := readTestFile(t, filepath.Join(dir, ".gitignore"))
	if !strings.HasPrefix(ignore, before[".gitignore"]+"\n# Added by fw project upgrade\n") || !strings.Contains(ignore, "**/private/**") || !strings.Contains(ignore, "/.framework/backups/") ||
		strings.Count(ignore, "*.log\n") != 1 {
		t.Errorf(".gitignore:\n%s", ignore)
	}
	backups, _ := filepath.Glob(filepath.Join(dir, ".framework", "backups", "*", "master.do"))
	if len(backups) != 1 || readTestFile(t, backups[0]) != before["master.do"] {
		t.Errorf("backups: %v", backups)
	}

	// Again: nothing to do.
	out, _, err = h.run(t, "", "project", "upgrade", "--apply")
	if err != nil || !strings.Contains(out, "Nothing to upgrade") {
		t.Errorf("second run: %v\n%s", err, out)
	}
}

// A project fw 0.4.6 adopted has a spec and the current header but no
// settings.yml; a sensitive one gets security in it. --no-backup backs up
// nothing.
func TestUpgradeAdoptedStata(t *testing.T) {
	h := newHarness(t, nil)
	dir := oldStudy(t, h.app.home)
	if _, _, err := h.run(t, "", "adopt", dir, "--lang", "stata", "--yes", "--type", "project_sensitive"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "settings.yml")); err != nil {
		t.Fatal(err)
	}
	h.app.cwd = dir
	out, _, err := h.run(t, "", "project", "upgrade", "--apply", "--no-backup", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var res struct {
		Applied bool                  `json:"applied"`
		Changes []stata.UpgradeChange `json:"changes"`
		Backup  string                `json:"backup"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	files := []string{}
	for _, c := range res.Changes {
		files = append(files, c.File)
	}
	if !res.Applied || res.Backup != "" || strings.Join(files, ",") != "settings.yml,.gitignore" {
		t.Errorf("%+v", res)
	}
	if yml := readTestFile(t, filepath.Join(dir, "settings.yml")); !strings.Contains(yml, "sensitive: true") {
		t.Errorf("settings.yml:\n%s", yml)
	}
	// An adopted project gets adopt's rules, not the template's logs and caches.
	if ignore := readTestFile(t, filepath.Join(dir, ".gitignore")); strings.Contains(ignore, "*.log") || !strings.HasSuffix(ignore, "# Added by fw project upgrade\n/.framework/backups/\n") {
		t.Errorf(".gitignore:\n%s", ignore)
	}
	if _, err := os.Stat(filepath.Join(dir, ".framework", "backups")); err == nil {
		t.Error("--no-backup backed up")
	}

	// settings.yml without security gets it appended under default:.
	writeTestFile(t, filepath.Join(dir, "settings.yml"), "default:\n  project_name: old-study\n  directories: {}\n")
	if _, _, err := h.run(t, "", "project", "upgrade", "--apply"); err != nil {
		t.Fatal(err)
	}
	if yml := readTestFile(t, filepath.Join(dir, "settings.yml")); !strings.HasPrefix(yml, "default:\n  project_name: old-study\n  directories: {}\n") || !strings.HasSuffix(yml, "  security:\n    sensitive: true\n") {
		t.Errorf("settings.yml:\n%s", yml)
	}
}

// R and Python projects run their package's upgrade, dry run by default.
func TestUpgradeRAndPython(t *testing.T) {
	h := newHarness(t, nil)
	f := &fakeEngines{}
	f.install(h, "Rscript", "uv")
	r := filepath.Join(h.app.home, "old-r")
	writeTestFile(t, filepath.Join(r, "settings.yml"), "default:\n  project_type: project\n")
	h.app.cwd = r
	if _, _, err := h.run(t, "", "project", "upgrade"); err != nil || len(f.ran) != 1 || f.ran[0].Args[1] != "framework::upgrade(dry_run = TRUE)" || f.ran[0].Dir != r {
		t.Errorf("dry run: %v %v", err, f.ran)
	}
	if _, _, err := h.run(t, "", "project", "upgrade", "--apply", "--no-backup"); err != nil || f.ran[1].Args[1] != "framework::upgrade(dry_run = FALSE, backup = FALSE)" {
		t.Errorf("apply: %v %v", err, f.ran)
	}
	// framework 2.0.1 has no upgrade(): say so, and run nothing.
	h.app.engines.Probe = func(c engine.Command) bool { return !strings.Contains(strings.Join(c.Args, " "), "upgrade") }
	if _, _, err := h.run(t, "", "project", "upgrade"); err == nil || !strings.Contains(err.Error(), "has no upgrade() yet") || len(f.ran) != 2 {
		t.Errorf("old R: %v %v", err, f.ran)
	}

	// A Framework 1 config.yml is found too.
	v1 := filepath.Join(h.app.home, "v1")
	writeTestFile(t, filepath.Join(v1, "config.yml"), "default:\n  packages: [dplyr]\n")
	h.app.cwd = v1
	h.app.engines.Probe = func(engine.Command) bool { return true }
	if _, _, err := h.run(t, "", "project", "upgrade"); err != nil || f.ran[2].Dir != v1 {
		t.Errorf("config.yml: %v %v", err, f.ran)
	}
	// A project's own config.yml is not a Framework project.
	own := filepath.Join(h.app.home, "own")
	writeTestFile(t, filepath.Join(own, "config.yml"), "repositories: {}\n")
	h.app.cwd = own
	if _, _, err := h.run(t, "", "project", "upgrade"); err == nil || !strings.Contains(err.Error(), "fw adopt") {
		t.Errorf("own config.yml: %v", err)
	}

	py := filepath.Join(h.app.home, "py")
	writeTestFile(t, filepath.Join(py, "pyproject.toml"), "[project]\n")
	writeTestFile(t, filepath.Join(py, "uv.lock"), "")
	writeTestFile(t, filepath.Join(py, ".framework", "spec.json"), `{"language":"python"}`)
	h.app.cwd = py
	if _, _, err := h.run(t, "", "project", "upgrade", "--apply"); err != nil || !strings.HasSuffix(f.ran[3].String(), "uv run python -m framework upgrade --apply") {
		t.Errorf("python: %v %v", err, f.ran)
	}
	h.app.engines.Probe = func(engine.Command) bool { return false }
	if _, _, err := h.run(t, "", "project", "upgrade"); err == nil || !strings.Contains(err.Error(), "framework-py has no upgrade command yet") {
		t.Errorf("old python: %v", err)
	}
}

// fw's Stata adopt and upgrade record tools.quarto when a folder adopt
// scans has Quarto files, and never append negations after a .gitignore's
// own rules.
func TestStataQuartoAndNegations(t *testing.T) {
	h := newHarness(t, nil)
	dir := oldStudy(t, h.app.home)
	writeTestFile(t, filepath.Join(dir, "IRB", "notes", "memo.qmd"), "---\ntitle: memo\n---\n")
	out, _, err := h.run(t, "", "adopt", dir, "--lang", "stata", "--yes", "--json")
	var res struct {
		Quarto  bool     `json:"quarto"`
		LeftOut []string `json:"gitignore_left_out"`
	}
	if err != nil || json.Unmarshal([]byte(out), &res) != nil || !res.Quarto || len(res.LeftOut) != 0 {
		t.Fatalf("%v\n%s", err, out)
	}
	// No .gitignore before: the rules go in whole, the negation too.
	if ignore := readTestFile(t, filepath.Join(dir, ".gitignore")); !strings.Contains(ignore, "\n!.env.example\n") {
		t.Errorf(".gitignore:\n%s", ignore)
	}
	var doc struct {
		Tools map[string]bool `json:"tools"`
	}
	if err := json.Unmarshal([]byte(readTestFile(t, filepath.Join(dir, ".framework", "spec.json"))), &doc); err != nil || !doc.Tools["quarto"] {
		t.Errorf("spec tools: %v %v", err, doc.Tools)
	}
	// Upgrading it reads the spec with quarto on and changes nothing about it.
	h.app.cwd = dir
	if out, errOut, err := h.run(t, "", "project", "upgrade"); err != nil || strings.Contains(out, ".framework/spec.json") {
		t.Errorf("upgrade of the adopted project: %v\n%s%s", err, out, errOut)
	}

	// An old project with a _quarto.yml gets tools.quarto in its new spec, and
	// its .gitignore, which has rules, gets no negation.
	old := oldStataProject(t, t.TempDir())
	writeTestFile(t, filepath.Join(old, "_quarto.yml"), "project:\n  type: default\n")
	h.app.cwd = old
	out, _, err = h.run(t, "", "project", "upgrade", "--apply", "--json")
	var plan struct {
		Changes []stata.UpgradeChange `json:"changes"`
		Notes   []string              `json:"notes"`
	}
	if err != nil || json.Unmarshal([]byte(out), &plan) != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "with tools.quarto: true for its Quarto files") || !strings.Contains(strings.Join(plan.Notes, "\n"), ".gitignore: left out !.env.example") {
		t.Errorf("plan:\n%s", out)
	}
	if ignore := readTestFile(t, filepath.Join(old, ".gitignore")); strings.Contains(ignore, "!") {
		t.Errorf(".gitignore:\n%s", ignore)
	}

	// A spec without it gets it on the next upgrade.
	var spec map[string]any
	if err := json.Unmarshal([]byte(readTestFile(t, filepath.Join(old, ".framework", "spec.json"))), &spec); err != nil {
		t.Fatal(err)
	}
	delete(spec, "tools")
	data, _ := json.Marshal(spec)
	writeTestFile(t, filepath.Join(old, ".framework", "spec.json"), string(data))
	if out, _, err := h.run(t, "", "project", "upgrade"); err != nil || !strings.Contains(out, "record tools.quarto: true (the project has Quarto files)") {
		t.Errorf("spec without tools: %v\n%s", err, out)
	}
}

// fw project upgrade creates the private folders a Stata project's settings
// name and a fresh checkout lacks, once.
func TestUpgradeStataCreatesPrivateFolders(t *testing.T) {
	h := newHarness(t, nil)
	if _, errOut, err := h.run(t, "", "new", "--dir", "sens", "sens", "--lang", "stata", "--type", "project_sensitive"); err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	dir := filepath.Join(h.app.home, "sens")
	var doc map[string]any
	if err := json.Unmarshal([]byte(readTestFile(t, filepath.Join(dir, ".framework", "spec.json"))), &doc); err != nil {
		t.Fatal(err)
	}
	r, err := stata.FromSpec(doc)
	if err != nil {
		t.Fatal(err)
	}
	private := stata.PrivateFolders(r.View)
	if len(private) == 0 {
		t.Fatal("no private folders")
	}
	for _, p := range private {
		if err := os.RemoveAll(filepath.Join(dir, filepath.FromSlash(p))); err != nil {
			t.Fatal(err)
		}
	}
	h.app.cwd = dir
	out, _, err := h.run(t, "", "project", "upgrade", "--apply")
	if err != nil || !strings.Contains(out, "mkdir   "+private[0]+"/") {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, p := range private {
		if info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(p))); err != nil || !info.IsDir() {
			t.Errorf("%s: %v", p, err)
		}
	}
	if out, _, err := h.run(t, "", "project", "upgrade"); err != nil || !strings.Contains(out, "Nothing to upgrade") {
		t.Errorf("again: %v\n%s", err, out)
	}
}

// --type project_sensitive turns a Stata project sensitive the way R's
// upgrade(type =) does: the flag, the layout's private and public folders
// (its own stay), settings.yml and settings.do, the private folders, the
// .gitignore rules, and the Claude Code guard, in one pass.
func TestUpgradeStataType(t *testing.T) {
	h := newHarness(t, nil)
	if _, errOut, err := h.run(t, "", "new", "--dir", "trial", "trial", "--lang", "stata", "--type", "project"); err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	dir := filepath.Join(h.app.home, "trial")
	h.app.cwd = dir
	if _, _, err := h.run(t, "", "project", "upgrade", "--apply"); err != nil { // the backups rule
		t.Fatal(err)
	}
	before := map[string]string{}
	for _, f := range []string{"settings.do", "settings.yml", ".gitignore"} {
		before[f] = readTestFile(t, filepath.Join(dir, f))
	}

	if _, _, err := h.run(t, "", "project", "upgrade", "--type", "course"); exitCode(err) != exitUsage {
		t.Errorf("course in Stata: %v", err)
	}
	if _, _, err := h.run(t, "", "project", "upgrade", "--move-settings"); exitCode(err) != exitUsage {
		t.Errorf("--move-settings in Stata: %v", err)
	}

	out, errOut, err := h.run(t, "", "project", "upgrade", "--type", "project_sensitive", "--apply")
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	for _, want := range []string{"set type to project_sensitive (was project); set security.sensitive to true; add the private and public folders of project_sensitive: inputs_private_final",
		"insert  settings.do", "+ global inputs_private_raw `\"$root/inputs/private/raw\"'", "+ capture mkdir `\"$root/inputs/private\"'",
		"settings.yml", "+     outputs_public_tables: outputs/public/tables", "+   security:",
		"mkdir   inputs/private/raw/", "keep Claude Code out of inputs/private, outputs/private"} {
		if !strings.Contains(out, want) {
			t.Errorf("plan lacks %q:\n%s", want, out)
		}
	}
	for f, text := range before {
		if after := readTestFile(t, filepath.Join(dir, f)); !keeps(text, after) {
			t.Errorf("%s lost a line:\n%s", f, after)
		}
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(readTestFile(t, filepath.Join(dir, ".framework", "spec.json"))), &doc); err != nil {
		t.Fatal(err)
	}
	r, err := stata.FromSpec(doc)
	if err != nil || r.View.Type != "project_sensitive" || !r.View.Security.Sensitive || r.View.Directories["inputs_raw"] != "inputs/raw" ||
		r.View.Directories["inputs_private_raw"] != "inputs/private/raw" || r.View.Directories["outputs_public_figures"] != "outputs/public/figures" {
		t.Errorf("spec: %v %+v", err, r.View)
	}
	settings := readTestFile(t, filepath.Join(dir, "settings.do"))
	if got := stata.SettingsGlobals(settings); got["outputs_private_tables"] != "outputs/private/tables" || got["inputs_raw"] != "inputs/raw" {
		t.Errorf("settings.do globals: %v", got)
	}
	if info, err := os.Stat(filepath.Join(dir, "outputs", "private", "tables")); err != nil || !info.IsDir() {
		t.Errorf("private folder: %v", err)
	}
	if guard := readTestFile(t, filepath.Join(dir, ".claude", "settings.json")); !strings.Contains(guard, `"Read(/inputs/private/**)"`) || !strings.Contains(guard, `"./outputs/private"`) {
		t.Errorf("guard:\n%s", guard)
	}
	if ignore := readTestFile(t, filepath.Join(dir, ".gitignore")); !strings.Contains(ignore, "**/private/**") {
		t.Errorf(".gitignore:\n%s", ignore)
	}
	// One pass: nothing is left to do.
	if out, _, err := h.run(t, "", "project", "upgrade", "--type", "project_sensitive"); err != nil || !strings.Contains(out, "Nothing to upgrade") {
		t.Errorf("second pass: %v\n%s", err, out)
	}
	// Back to project: the label changes, the project stays sensitive.
	out, _, err = h.run(t, "", "project", "upgrade", "--type", "project", "--apply", "--json")
	var res struct {
		Changes []stata.UpgradeChange `json:"changes"`
		Notes   []string              `json:"notes"`
	}
	if err != nil || json.Unmarshal([]byte(out), &res) != nil || len(res.Changes) != 1 || res.Changes[0].File != ".framework/spec.json" ||
		!strings.Contains(strings.Join(res.Notes, " "), "The project stays sensitive") {
		t.Errorf("to project: %v\n%s", err, out)
	}
}

// R and Python get --type and --move-settings when their package takes them.
func TestUpgradeTypePassedOn(t *testing.T) {
	h := newHarness(t, nil)
	f := &fakeEngines{}
	f.install(h, "Rscript", "uv")
	r := filepath.Join(h.app.home, "old-r")
	writeTestFile(t, filepath.Join(r, "settings.yml"), "default:\n  project_type: project\n")
	h.app.cwd = r
	if _, _, err := h.run(t, "", "project", "upgrade", "--type", "spaceship"); exitCode(err) != exitUsage {
		t.Errorf("bad type: %v", err)
	}
	if _, _, err := h.run(t, "", "project", "upgrade", "--apply", "--type", "project_sensitive", "--move-settings"); err != nil ||
		f.ran[0].Args[1] != `framework::upgrade(dry_run = FALSE, type = "project_sensitive", move_settings = TRUE)` {
		t.Errorf("R: %v %v", err, f.ran)
	}
	var probes []string
	h.app.engines.Probe = func(c engine.Command) bool {
		probes = append(probes, strings.Join(c.Args, " "))
		return !strings.Contains(strings.Join(c.Args, " "), "move_settings")
	}
	if _, _, err := h.run(t, "", "project", "upgrade", "--move-settings"); err == nil || !strings.Contains(err.Error(), "has no upgrade() that takes move_settings yet") || len(f.ran) != 1 ||
		!strings.Contains(probes[0], `c("move_settings") %in% names(formals(get("upgrade"`) {
		t.Errorf("old R: %v %v", err, probes)
	}

	py := filepath.Join(h.app.home, "py")
	writeTestFile(t, filepath.Join(py, "pyproject.toml"), "[project]\n")
	writeTestFile(t, filepath.Join(py, "uv.lock"), "")
	writeTestFile(t, filepath.Join(py, ".framework", "spec.json"), `{"language":"python"}`)
	h.app.cwd = py
	probes = nil
	h.app.engines.Probe = func(c engine.Command) bool { probes = append(probes, strings.Join(c.Args, " ")); return true }
	if _, _, err := h.run(t, "", "project", "upgrade", "--type", "course", "--move-settings"); err != nil ||
		!strings.HasSuffix(f.ran[1].String(), "uv run python -m framework upgrade --type course --move-settings") ||
		len(probes) != 1 || !strings.Contains(probes[0], `sys.argv = ["framework", "upgrade", "--help"]`) || !strings.Contains(probes[0], `["--type", "--move-settings"]`) {
		t.Errorf("python: %v %v %q", err, f.ran, probes)
	}
	h.app.engines.Probe = func(engine.Command) bool { return false }
	if _, _, err := h.run(t, "", "project", "upgrade", "--type", "course"); err == nil || !strings.Contains(err.Error(), "has no upgrade that takes --type yet") {
		t.Errorf("old python: %v", err)
	}
}
