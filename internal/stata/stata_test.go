package stata

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/frameworktools/framework-cli/internal/spec"
)

func envelope(t *testing.T, doc string) spec.Envelope {
	t.Helper()
	env, err := spec.Parse([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	return env
}

func read(t *testing.T, dir, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("%s: %v", rel, err)
	}
	return string(data)
}

func scaffold(t *testing.T, doc string, headless bool) (string, Result) {
	t.Helper()
	r, err := Resolve(envelope(t, doc), "test")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "proj")
	res, err := Scaffold(r, Options{Dir: dir, Headless: headless, Now: time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	return dir, res
}

const basic = `{"spec_version":1,"name":"Vaccine Study","author":{"name":"Ada","email":"ada@example.edu","affiliation":"JHU"},
 "layers":[{"kind":"project","key":"vaccine-study","layer":{"language":"stata",
   "directories":{"codebooks":"docs/codebooks","scratch":null},
   "git":{"gitignore_content":"*.xlsx"},
   "files":{"README.md":{"content":"# {{project_name}} by {{ author }} {{< include x >}}","template":true}}}}]}`

func TestResolveBasic(t *testing.T) {
	r, err := Resolve(envelope(t, basic), "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	v := r.View
	if v.Type != "project" || v.Name != "Vaccine Study" || v.Author.Name != "Ada" || v.Security.Sensitive {
		t.Errorf("view %+v", v)
	}
	if _, ok := v.Directories["scratch"]; ok {
		t.Error("null should remove scratch")
	}
	if v.Directories["codebooks"] != "docs/codebooks" || v.Directories["do"] != "do" {
		t.Errorf("directories %v", v.Directories)
	}
	if !v.AI.Assistants["claude"] || !v.Skills["framework-stata"].Package {
		t.Errorf("ai %+v skills %+v", v.AI, v.Skills)
	}
	if strings.Contains(v.Git.GitignoreContent, "*.dta") || !strings.Contains(v.Git.GitignoreContent, "**/private/**") || !strings.Contains(v.Git.GitignoreContent, "# --- from project vaccine-study ---\n*.xlsx") {
		t.Errorf("gitignore:\n%s", v.Git.GitignoreContent)
	}
	if sc, _ := r.Doc["scaffold"].(map[string]any); sc["workflow"] != "scripts" || sc["notebook_format"] != "script" || sc["ggplot_theme"] != nil {
		t.Errorf("a Stata spec records the Scripts-only workflow and no R scaffold settings: %v", sc)
	}
	if r.Doc["language"] != "stata" || r.Doc["source"].(map[string]any)["origin"] != "cli" || r.Doc["source"].(map[string]any)["fw_version"] != "1.2.3" {
		t.Errorf("doc %v %v", r.Doc["language"], r.Doc["source"])
	}
}

func TestResolveRefusals(t *testing.T) {
	cases := map[string]string{
		"r project":   `{"spec_version":1,"name":"A","layers":[]}`,
		"course":      `{"spec_version":1,"name":"A","layers":[{"kind":"project","layer":{"language":"stata","type":"course"}}]}`,
		"global name": `{"spec_version":1,"name":"A","layers":[{"kind":"project","layer":{"language":"stata","directories":{"root":"x"}}}]}`,
		"long name":   `{"spec_version":1,"name":"A","layers":[{"kind":"project","layer":{"language":"stata","directories":{"a_very_long_directory_key_over_32":"x"}}}]}`,
		"dollar path": `{"spec_version":1,"name":"A","layers":[{"kind":"project","layer":{"language":"stata","directories":{"x":"a$b"}}}]}`,
		"escape":      `{"spec_version":1,"name":"A","layers":[{"kind":"project","layer":{"language":"stata","files":{"../x":{"content":"x"}}}}]}`,
	}
	for name, doc := range cases {
		env, err := spec.Parse([]byte(doc))
		if name == "escape" {
			if err == nil {
				t.Error("escape: the layers schema should refuse a path outside the project")
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if _, err := Resolve(env, "test"); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestScaffoldProject(t *testing.T) {
	dir, _ := scaffold(t, basic, true)

	settings := read(t, dir, "settings.do")
	for _, want := range []string{
		"global inputs_raw `\"$root/inputs/raw\"'",
		"global codebooks `\"$root/docs/codebooks\"'",
		"capture mkdir `\"$root/inputs\"'",
		"capture mkdir `\"$root/inputs/raw\"'",
	} {
		if !strings.Contains(settings, want) {
			t.Errorf("settings.do lacks %s:\n%s", want, settings)
		}
	}
	if strings.Index(settings, `mkdir `+"`"+`"$root/inputs"'`) > strings.Index(settings, `mkdir `+"`"+`"$root/inputs/raw"'`) {
		t.Error("parents must be created first")
	}
	if strings.Contains(settings, "cannot read") || !strings.Contains(settings, "settings.yml") {
		t.Errorf("settings.do should explain how it and settings.yml divide the work:\n%s", settings)
	}

	// settings.yml holds the catalog and destinations, with settings.do's folders
	yml := read(t, dir, "settings.yml")
	for _, want := range []string{
		"default:\n  project_name: Vaccine Study\n  directories:\n",
		"    inputs_raw: inputs/raw\n",
		"    codebooks: docs/codebooks\n",
		"  data: {}\n",
		"  #   publish:\n",
		"  #         driver: folder\n",
		"#       path: inputs/raw/survey.csv",
	} {
		if !strings.Contains(yml, want) {
			t.Errorf("settings.yml lacks %q:\n%s", want, yml)
		}
	}
	if strings.Contains(yml, "scratch") {
		t.Errorf("settings.yml lists a removed folder:\n%s", yml)
	}

	master := read(t, dir, "master.do")
	if !strings.Contains(master, "* do `\"$do/import.do\"'") || !strings.Contains(master, "`\"$logs/master_") {
		t.Errorf("master.do:\n%s", master)
	}
	// fw_project loads the project before any step, and set type double is
	// offered, commented
	if i, j := strings.Index(master, "do \"settings.do\""), strings.Index(master, "\nfw_project\n"); i < 0 || j < i || strings.Index(master, "log using") < j {
		t.Errorf("master.do should run fw_project after settings.do and before the log:\n%s", master)
	}
	if !strings.Contains(master, "\n* set type double\n") {
		t.Errorf("master.do lacks the commented set type double:\n%s", master)
	}
	// Starter do-files come only from a blueprint's files.
	if entries, err := os.ReadDir(filepath.Join(dir, "do")); err != nil || len(entries) != 1 || entries[0].Name() != ".gitkeep" {
		t.Errorf("do/ should be empty: %v %v", entries, err)
	}
	if got := read(t, dir, "README.md"); got != "# Vaccine Study by Ada {{< include x >}}" {
		t.Errorf("README.md = %q", got)
	}
	if read(t, dir, "CLAUDE.md") != "@AGENTS.md\n" || !strings.Contains(read(t, dir, "AGENTS.md"), "`$inputs_raw`") {
		t.Error("AI files")
	}
	if !strings.Contains(read(t, dir, ".claude/skills/framework-stata/SKILL.md"), "name: framework-stata") {
		t.Error("skill not installed")
	}
	if _, err := os.Stat(filepath.Join(dir, ".claude/settings.json")); !os.IsNotExist(err) {
		t.Error("deny rules are for sensitive projects only")
	}
	if _, err := os.Stat(filepath.Join(dir, "inputs/raw/.gitkeep")); err != nil {
		t.Error(err)
	}

	var record map[string]any
	if err := json.Unmarshal([]byte(read(t, dir, ".framework/spec.json")), &record); err != nil || record["language"] != "stata" {
		t.Errorf("spec.json: %v %v", err, record["language"])
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); !os.IsNotExist(err) {
		t.Error("headless must not run git")
	}
}

func TestScaffoldSensitive(t *testing.T) {
	doc := `{"spec_version":1,"name":"Restricted","layers":[{"kind":"project","layer":{"language":"stata","type":"project_sensitive","ai":{"assistants":{"codex":true}}}}]}`
	dir, _ := scaffold(t, doc, true)

	var settings struct {
		Permissions struct{ Deny []string }
		Sandbox     struct {
			Enabled           bool
			FailIfUnavailable bool
			Filesystem        struct{ DenyRead []string }
		}
	}
	if err := json.Unmarshal([]byte(read(t, dir, ".claude/settings.json")), &settings); err != nil {
		t.Fatal(err)
	}
	want := []string{"Read(/inputs/private/**)", "Edit(/inputs/private/**)", "Read(/outputs/private/**)", "Edit(/outputs/private/**)", "Read(/.env)"}
	if strings.Join(settings.Permissions.Deny, " ") != strings.Join(want, " ") {
		t.Errorf("deny %v", settings.Permissions.Deny)
	}
	if !settings.Sandbox.Enabled || !settings.Sandbox.FailIfUnavailable || len(settings.Sandbox.Filesystem.DenyRead) != 3 {
		t.Errorf("sandbox %+v", settings.Sandbox)
	}
	for _, rel := range []string{".claude/skills/framework-sensitive-data/SKILL.md", ".agents/skills/framework-sensitive-data/SKILL.md", ".agents/skills/framework-stata/SKILL.md"} {
		read(t, dir, rel)
	}
	if !strings.Contains(read(t, dir, "AGENTS.md"), "`inputs/private/`, `outputs/private/`") {
		t.Errorf("AGENTS.md:\n%s", read(t, dir, "AGENTS.md"))
	}
	if gi := read(t, dir, ".gitignore"); !strings.Contains(gi, "/inputs/private/") || strings.Contains(gi, "*.dta") {
		t.Errorf("sensitive gitignore:\n%s", gi)
	}
	// The same public input folders as R and Python
	for _, rel := range []string{"inputs/public/raw", "inputs/public/intermediate", "inputs/public/final"} {
		if _, err := os.Stat(filepath.Join(dir, rel, ".gitkeep")); err != nil {
			t.Error(err)
		}
	}
	if yml := read(t, dir, "settings.yml"); !strings.Contains(yml, "    inputs_public_raw: inputs/public/raw\n") {
		t.Errorf("settings.yml:\n%s", yml)
	}
}

func TestScaffoldBare(t *testing.T) {
	dir, _ := scaffold(t, `{"spec_version":1,"name":"B","layers":[{"kind":"project","layer":{"language":"stata","type":"bare"}}]}`, true)
	for _, rel := range []string{"settings.do", ".gitignore", ".framework/spec.json"} {
		read(t, dir, rel)
	}
	if yml := read(t, dir, "settings.yml"); !strings.Contains(yml, "  project_name: B\n  directories: {}\n") || !strings.Contains(yml, "  data: {}\n") {
		t.Errorf("bare settings.yml:\n%s", yml)
	}
	for _, rel := range []string{"master.do", "AGENTS.md", ".claude"} {
		if _, err := os.Stat(filepath.Join(dir, rel)); !os.IsNotExist(err) {
			t.Errorf("bare project has %s", rel)
		}
	}
}

// Cloud structures arrive as a base layer with any type label. Behavior
// follows core_files and the directories, not the label.
func TestStructureLayers(t *testing.T) {
	dir, _ := scaffold(t, `{"spec_version":1,"name":"B","layers":[
		{"kind":"base","key":"framework/bare","layer":{"type":"bare","core_files":false}},
		{"kind":"blueprint","layer":{"ai":{"enabled":true,"assistants":{"claude":true}},"tools":{"renv":false}}},
		{"kind":"project","layer":{"language":"stata"}}]}`, true)
	for _, rel := range []string{"settings.do", ".framework/spec.json"} {
		read(t, dir, rel)
	}
	// As in R, a project without core files gets no AI files either.
	for _, rel := range []string{"master.do", ".env", "AGENTS.md"} {
		if _, err := os.Stat(filepath.Join(dir, rel)); !os.IsNotExist(err) {
			t.Errorf("core_files: false project has %s", rel)
		}
	}
	if spec := read(t, dir, ".framework/spec.json"); !strings.Contains(spec, `"core_files": false`) {
		t.Errorf("spec does not record core_files:\n%s", spec)
	}

	dir, _ = scaffold(t, `{"spec_version":1,"name":"S","layers":[
		{"kind":"base","key":"ada/survey","layer":{"type":"survey_stata","directories":{"do":"code","inputs_raw":"data/raw"}}},
		{"kind":"project","layer":{"language":"stata"}}]}`, true)
	for _, rel := range []string{"master.do", "code", "data/raw"} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Errorf("custom structure lacks %s", rel)
		}
	}

	for name, doc := range map[string]string{
		"tool on":       `{"spec_version":1,"name":"T","layers":[{"kind":"facet","key":"framework/tool-quarto","layer":{"tools":{"quarto":true}}},{"kind":"project","layer":{"language":"stata"}}]}`,
		"unknown label": `{"spec_version":1,"name":"T","layers":[{"kind":"project","layer":{"language":"stata","type":"survey_stata"}}]}`,
	} {
		if _, err := Resolve(envelope(t, doc), "test"); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestScaffoldHeadlessChecks(t *testing.T) {
	r, err := Resolve(envelope(t, basic), "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, ".github"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "analysis.do"), []byte("x"), 0o644)
	if _, err := Scaffold(r, Options{Dir: dir, Headless: true}); err == nil || !strings.Contains(err.Error(), "analysis.do") {
		t.Errorf("unexpected file: %v", err)
	}

	r, err = Resolve(envelope(t, `{"spec_version":1,"name":"W","layers":[{"kind":"project","layer":{"language":"stata","files":{".github/workflows/x.yml":{"content":"x"}}}}]}`), "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Scaffold(r, Options{Dir: t.TempDir(), Headless: true}); err == nil || !strings.Contains(err.Error(), "workflow") {
		t.Errorf("workflow file: %v", err)
	}
}

func TestScaffoldGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	cfg := filepath.Join(t.TempDir(), "gitconfig")
	_ = os.WriteFile(cfg, []byte("[user]\n\tname = Test\n\temail = test@example.edu\n[init]\n\tdefaultBranch = main\n"), 0o644)
	t.Setenv("GIT_CONFIG_GLOBAL", cfg)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")

	doc := `{"spec_version":1,"name":"G","layers":[{"kind":"project","layer":{"language":"stata","env":{"API_URL":"https://x"},"git":{"gitignore_content":"!.env"}}}]}`
	dir, res := scaffold(t, doc, false)
	cmd := exec.Command("git", "ls-files")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("%v %v", err, res.Notes)
	}
	files := string(out)
	if !strings.Contains(files, "master.do") || !strings.Contains(files, "settings.do") {
		t.Errorf("commit lacks do-files:\n%s", files)
	}
	if strings.Contains(files, ".env") {
		t.Error("committed .env despite a layer un-ignoring it")
	}
}

// The cloud's top layer for a Stata project removes R-only folders that the
// layers beneath it define, with nulls.
func TestCloudStataLayerRemovesRFolders(t *testing.T) {
	doc := `{"spec_version":1,"name":"C","layers":[
	  {"kind":"base","key":"project","layer":{"type":"project","directories":{"notebooks":"notebooks","scripts":"scripts","functions":"functions","outputs_models":"outputs/models","inputs_raw":"inputs/raw"},"render_dirs":{"notebooks":"outputs/notebooks"}}},
	  {"kind":"project","key":"c","layer":{"language":"stata","directories":{"functions":null,"notebooks":null,"scripts":null,"outputs_models":null},"render_dirs":{"notebooks":null}}}]}`
	r, err := Resolve(envelope(t, doc), "test")
	if err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{"notebooks", "scripts", "functions", "outputs_models"} {
		if _, ok := r.View.Directories[gone]; ok {
			t.Errorf("%s should be removed", gone)
		}
	}
	if r.View.Directories["inputs_raw"] != "inputs/raw" || r.View.Directories["do"] != "do" {
		t.Errorf("directories %v", r.View.Directories)
	}
}

func TestLayerWithoutKind(t *testing.T) {
	if _, err := Resolve(envelope(t, `{"spec_version":1,"name":"K","layers":[{"layer":{"language":"stata"}}]}`), "test"); err != nil {
		t.Fatal(err)
	}
}

func TestWorkflows(t *testing.T) {
	layer := func(scaffold string) string {
		return `{"spec_version":1,"name":"W","layers":[{"kind":"facet","key":"framework/workflow","layer":{"scaffold":` + scaffold + `}},{"kind":"project","layer":{"language":"stata"}}]}`
	}
	if _, err := Resolve(envelope(t, layer(`{"workflow":"scripts","notebook_format":"script"}`)), ""); err != nil {
		t.Errorf("scripts: %v", err)
	}
	for _, scaffold := range []string{`{"workflow":"quarto","notebook_format":"quarto"}`, `{"workflow":"jupyter"}`, `{"notebook_format":"rmarkdown"}`} {
		if _, err := Resolve(envelope(t, layer(scaffold)), ""); err == nil || !strings.Contains(err.Error(), "Stata projects") {
			t.Errorf("%s: %v", scaffold, err)
		}
	}
}

func TestPublishingSettingsFromLayers(t *testing.T) {
	doc := `{"spec_version":1,"name":"Published Study","layers":[{"kind":"settings","layer":{"language":"stata","publish":{"default":"pages","destinations":{"pages":{"driver":"github"}}},"git":{"use_git":false},"ai":{"enabled":false}}}]}`
	dir, _ := scaffold(t, doc, true)
	settings := read(t, dir, "settings.yml")
	if !strings.Contains(settings, "\n  publish:\n    default: pages\n    destinations:\n      pages:\n        driver: github\n") || strings.Contains(settings, "#   publish:") {
		t.Fatal(settings)
	}
	recorded := read(t, dir, ".framework/spec.json")
	var specDoc map[string]any
	if e := json.Unmarshal([]byte(recorded), &specDoc); e != nil {
		t.Fatal(e)
	}
	if specDoc["publish"] == nil {
		t.Fatal("publishing settings lost from resolved spec")
	}
}

// GIT_AUTHOR_* and GIT_COMMITTER_* give git an identity without user.name.
func TestScaffoldGitIdentityFromEnvironment(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	cfg := filepath.Join(t.TempDir(), "gitconfig")
	_ = os.WriteFile(cfg, []byte("[init]\n\tdefaultBranch = main\n"), 0o644)
	t.Setenv("GIT_CONFIG_GLOBAL", cfg)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	doc := `{"spec_version":1,"name":"G","layers":[{"kind":"project","layer":{"language":"stata"}}]}`
	for name, env := range map[string]map[string]string{
		"set":     {"GIT_AUTHOR_NAME": "Ada", "GIT_AUTHOR_EMAIL": "ada@example.edu", "GIT_COMMITTER_NAME": "Ada", "GIT_COMMITTER_EMAIL": "ada@example.edu"},
		"partial": {"GIT_AUTHOR_NAME": "Ada", "GIT_AUTHOR_EMAIL": "ada@example.edu", "GIT_COMMITTER_NAME": "", "GIT_COMMITTER_EMAIL": ""},
	} {
		for k, v := range env {
			t.Setenv(k, v)
		}
		dir, res := scaffold(t, doc, false)
		cmd := exec.Command("git", "rev-parse", "HEAD")
		cmd.Dir = dir
		committed := cmd.Run() == nil
		skipped := strings.Contains(strings.Join(res.Notes, " "), "without a first commit")
		if committed != (name == "set") || skipped == committed {
			t.Errorf("%s: committed %v, notes %v", name, committed, res.Notes)
		}
	}
}
