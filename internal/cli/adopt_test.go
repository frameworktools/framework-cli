package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/frameworktools/framework-cli/internal/engine"
)

// oldStudy makes a Stata project folder that predates Framework.
func oldStudy(t *testing.T, root string) string {
	t.Helper()
	dir := filepath.Join(root, "old-study")
	for _, d := range []string{"do", "data/raw", "data/clean", "logs", "IRB", "output/private"} {
		if err := os.MkdirAll(filepath.Join(dir, filepath.FromSlash(d)), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	writeTestFile(t, filepath.Join(dir, "analysis.do"), "use data\n")
	return dir
}

func files(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	_ = filepath.Walk(dir, func(p string, _ os.FileInfo, err error) error {
		if err == nil && p != dir {
			rel, _ := filepath.Rel(dir, p)
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	sort.Strings(out)
	return out
}

func TestAdoptStata(t *testing.T) {
	h := newHarness(t, nil)
	dir := oldStudy(t, h.app.home)
	before := files(t, dir)

	// Without a terminal, the mapping needs --yes.
	if _, errOut, err := h.run(t, "", "adopt", dir); exitCode(err) != exitUsage || !strings.Contains(errOut, "do                             do") {
		t.Errorf("no --yes: %v\n%s", err, errOut)
	}
	if got := files(t, dir); strings.Join(got, ",") != strings.Join(before, ",") {
		t.Fatal("changed files without --yes")
	}

	out, errOut, err := h.run(t, "", "adopt", dir, "--yes")
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	if !strings.Contains(errOut, "'output/private' looks private") || !strings.Contains(out, "settings.do, settings.yml, .gitignore, .framework/spec.json") ||
		!strings.Contains(out, "/output/private/") {
		t.Errorf("output:\n%s\n%s", out, errOut)
	}
	added := map[string]bool{}
	for _, f := range files(t, dir) {
		added[f] = true
	}
	for _, f := range before {
		delete(added, f)
	}
	if len(added) != 5 || !added["settings.do"] || !added["settings.yml"] || !added[".gitignore"] || !added[".framework"] || !added[".framework/spec.json"] {
		t.Errorf("adopt should add only the settings, .gitignore, and the spec: %v", added)
	}
	if ignore := readTestFile(t, filepath.Join(dir, ".gitignore")); !strings.Contains(ignore, "\n.env\n") || !strings.Contains(ignore, "**/private/**") || !strings.Contains(ignore, "/output/private/") {
		t.Errorf(".gitignore:\n%s", ignore)
	}
	if yml := readTestFile(t, filepath.Join(dir, "settings.yml")); !strings.Contains(yml, "inputs_raw: data/raw") || !strings.Contains(yml, "project_name: old-study") {
		t.Errorf("settings.yml:\n%s", yml)
	}
	var doc struct {
		Type        string                       `json:"type"`
		Directories map[string]string            `json:"directories"`
		Extra       map[string]map[string]string `json:"extra_directories"`
		AI          struct{ Enabled bool }       `json:"ai"`
		Source      map[string]any               `json:"source"`
		Git         map[string]any               `json:"git"`
	}
	data, _ := os.ReadFile(filepath.Join(dir, ".framework", "spec.json")) // #nosec G304 -- test path
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Type != "project" || doc.Directories["do"] != "do" || doc.Directories["inputs_raw"] != "data/raw" || doc.Extra["irb"]["path"] != "IRB" || doc.AI.Enabled ||
		doc.Source["origin"] != "cli" || doc.Source["adopted"] != true || doc.Source["fw_version"] == nil ||
		doc.Git["gitignore_content"] != readTestFile(t, filepath.Join(dir, ".gitignore")) {
		t.Errorf("spec: %+v", doc)
	}
	if settings := readTestFile(t, filepath.Join(dir, "settings.do")); !strings.Contains(settings, "global do") || !strings.Contains(settings, "global irb") {
		t.Errorf("settings.do:\n%s", settings)
	}

	// A second run changes nothing.
	if _, _, err := h.run(t, "", "adopt", dir, "--yes"); err == nil || !strings.Contains(err.Error(), "already a Framework project") {
		t.Errorf("second adopt: %v", err)
	}
}

func TestAdoptReviewAndLanguage(t *testing.T) {
	h := newHarness(t, nil)
	dir := oldStudy(t, h.app.home)

	// At a terminal: leave a folder out, then accept, and say it is not sensitive.
	h.app.interactive = true
	if _, errOut, err := h.run(t, "1\nleave\na\nn\n", "adopt", dir); err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	data, _ := os.ReadFile(filepath.Join(dir, ".framework", "spec.json")) // #nosec G304 -- test path
	if strings.Contains(string(data), `"inputs_raw"`) {
		t.Errorf("the left-out folder is still mapped:\n%s", data)
	}
	h.app.interactive = false

	empty := filepath.Join(h.app.home, "unknown")
	if err := os.MkdirAll(filepath.Join(empty, "data"), 0o750); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.run(t, "", "adopt", empty, "--yes"); exitCode(err) != exitUsage || !strings.Contains(err.Error(), "--lang") {
		t.Errorf("unknown language: %v", err)
	}
}

// fw adopt hands R projects to framework::adopt() once the installed R
// package has it, whatever its version.
func TestAdoptR(t *testing.T) {
	h := newHarness(t, nil)
	dir := oldStudy(t, h.app.home)
	f := &fakeEngines{}
	f.install(h, "Rscript")

	// No terminal, no --yes: show the mapping and stop.
	_, errOut, err := h.run(t, "", "adopt", dir, "--lang", "r")
	if exitCode(err) != exitUsage || !strings.Contains(errOut, "Proposed mapping:") || !strings.Contains(errOut, "framework::adopt(") || len(f.ran) != 0 {
		t.Errorf("no terminal: %v ran %v\n%s", err, f.ran, errOut)
	}
	// At a terminal, No cancels.
	h.app.interactive = true
	if _, _, err := h.run(t, "n\n", "adopt", dir, "--lang", "r"); err == nil || len(f.ran) != 0 {
		t.Errorf("declined: %v ran %v", err, f.ran)
	}
	h.app.interactive = false
	if _, _, err := h.run(t, "", "adopt", dir, "--lang", "r", "--yes"); err != nil {
		t.Fatal(err)
	}
	if len(f.ran) != 1 || !strings.Contains(f.ran[0].Args[1], "framework::adopt(a[1], yes = TRUE)") || f.ran[0].Args[2] != dir {
		t.Errorf("ran %v", f.ran)
	}

	// An R package without adopt(): say to update it.
	h.app.engines.Probe = func(c engine.Command) bool { return !strings.Contains(c.Args[1], "adopt") }
	if _, _, err := h.run(t, "", "adopt", dir, "--lang", "r", "--yes"); err == nil || !strings.Contains(err.Error(), "has no adopt()") {
		t.Errorf("old R package: %v", err)
	}
}

// --private maps folders to private keys, keeping their paths: fw does it
// for Stata and passes the folders to R and Python. A sensitive adopt lists
// the folders that may hold restricted data, and asks about each at a
// terminal.
func TestAdoptPrivate(t *testing.T) {
	h := newHarness(t, nil)
	dir := oldStudy(t, h.app.home)
	for _, d := range []string{"reports", "data-files"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o750); err != nil {
			t.Fatal(err)
		}
	}

	// A folder the mapping doesn't have is a usage error, before anything runs.
	if _, _, err := h.run(t, "", "adopt", dir, "--json", "--private", "nope"); exitCode(err) != exitUsage || !strings.Contains(err.Error(), "'nope' isn't a folder adopt maps here") {
		t.Errorf("unknown folder: %v", err)
	}

	// The proposal shows what is marked and what may hold restricted data.
	out, _, err := h.run(t, "", "adopt", dir, "--json", "--sensitive", "--private", "./reports/")
	var prop struct {
		Directories map[string]string `json:"directories"`
		Marked      []string          `json:"marked_private"`
		Candidates  []string          `json:"restricted_candidates"`
		Gitignore   []string          `json:"gitignore_additions"`
	}
	if err != nil || json.Unmarshal([]byte(out), &prop) != nil || prop.Directories["outputs_private_reports"] != "reports" ||
		strings.Join(prop.Marked, ",") != "reports" || strings.Join(prop.Candidates, ",") != "data/raw,data/clean,data-files,output" ||
		!contains(prop.Gitignore, "/reports/") {
		t.Errorf("proposal: %v\n%s", err, out)
	}

	// With --yes, the rest are listed with the flags that mark them.
	_, errOut, err := h.run(t, "", "adopt", dir, "--yes", "--sensitive", "--private", "reports", "--private", "data-files")
	if err != nil || !strings.Contains(errOut, "May hold restricted data (mark private?): data/raw, data/clean, output.") ||
		!strings.Contains(errOut, "--private data/raw --private data/clean --private output") {
		t.Fatalf("%v\n%s", err, errOut)
	}
	if yml := readTestFile(t, filepath.Join(dir, "settings.yml")); !strings.Contains(yml, "outputs_private_reports: reports") || !strings.Contains(yml, "inputs_private_data_files: data-files") {
		t.Errorf("settings.yml:\n%s", yml)
	}
	if ignore := readTestFile(t, filepath.Join(dir, ".gitignore")); !strings.Contains(ignore, "\n/reports/\n") || !strings.Contains(ignore, "\n/data-files/\n") {
		t.Errorf(".gitignore:\n%s", ignore)
	}

	// At a terminal, fw asks about each candidate, yes by default.
	other := oldStudy(t, t.TempDir())
	h.app.interactive = true
	if _, errOut, err := h.run(t, "a\n\nn\n\n", "adopt", other, "--sensitive"); err != nil || !strings.Contains(errOut, "'data/raw' may hold restricted data. Mark it private? [Y/n]") {
		t.Fatalf("%v\n%s", err, errOut)
	}
	if yml := readTestFile(t, filepath.Join(other, "settings.yml")); !strings.Contains(yml, "inputs_private_raw: data/raw") || !strings.Contains(yml, "inputs_final: data/clean") ||
		!strings.Contains(yml, "outputs_private_output: output") {
		t.Errorf("asked: settings.yml:\n%s", yml)
	}
	h.app.interactive = false

	// R and Python get the folders.
	f := &fakeEngines{}
	f.install(h, "Rscript", "uv")
	r := oldStudy(t, t.TempDir())
	if err := os.MkdirAll(filepath.Join(r, "reports"), 0o750); err != nil {
		t.Fatal(err)
	}
	if _, errOut, err := h.run(t, "", "adopt", r, "--lang", "r", "--yes", "--private", "reports", "--private", "data/raw"); err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	if len(f.ran) != 1 || !strings.Contains(f.ran[0].Args[1], "private = a[2:length(a)]") || strings.Join(f.ran[0].Args[2:], ",") != r+",reports,data/raw" {
		t.Errorf("R: %v", f.ran)
	}
	if _, errOut, err := h.run(t, "", "adopt", r, "--lang", "python", "--yes", "--private", "reports"); err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	if len(f.ran) != 2 || !strings.Contains(f.ran[1].String(), "adopt --path "+r+" --yes --private reports --force") {
		t.Errorf("Python: %v", f.ran[1:])
	}
	if _, _, err := h.run(t, "", "adopt", r, "--lang", "r", "--yes", "--private", "nope"); exitCode(err) != exitUsage || len(f.ran) != 2 {
		t.Errorf("R, unknown folder: %v", err)
	}
}

func TestStructureImport(t *testing.T) {
	srv := fakeCloud(t)
	h := newHarness(t, map[string]string{"FW_CLOUD_TOKEN": "user-tok", "FW_CLOUD_URL": srv.URL})
	dir := oldStudy(t, h.app.home)
	before := files(t, dir)

	// --json without --yes shows the proposal and saves nothing.
	lastStructure = ""
	out, _, err := h.run(t, "", "structures", "import", dir, "--json")
	if err != nil || lastStructure != "" || !strings.Contains(out, `"saved": false`) || !strings.Contains(out, `"code": "private_path"`) {
		t.Errorf("proposal: %v %q\n%s", err, lastStructure, out)
	}

	if _, _, err := h.run(t, "", "structures", "import", dir, "--yes", "--type", "spaceship"); exitCode(err) != exitUsage {
		t.Errorf("bad type: %v", err)
	}
	out, _, err = h.run(t, "", "structure", "import", dir, "--yes", "--name", "Old Study", "--type", "bare")
	if err != nil || !strings.Contains(out, "ada/old-study") {
		t.Fatalf("%v\n%s", err, out)
	}
	var sent map[string]any
	if err := json.Unmarshal([]byte(lastStructure), &sent); err != nil {
		t.Fatal(err)
	}
	dirs, _ := sent["directories"].(map[string]any)
	if sent["name"] != "Old Study" || sent["type"] != "bare" || dirs["do"] != "do" || dirs["inputs_final"] != "data/clean" {
		t.Errorf("body: %s", lastStructure)
	}
	if strings.Contains(lastStructure, "analysis.do") {
		t.Error("a file name was sent")
	}
	if got := files(t, dir); strings.Join(got, ",") != strings.Join(before, ",") {
		t.Error("import changed the folder")
	}

	logged := newHarness(t, map[string]string{"FW_CLOUD_URL": srv.URL})
	if _, _, err := logged.run(t, "", "structures", "import", dir, "--yes"); exitCode(err) != exitAuth {
		t.Errorf("logged out: %v", err)
	}
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path) // #nosec G304 -- test path
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestPythonHandoff(t *testing.T) {
	h := newHarness(t, nil)
	f := &fakeEngines{}
	f.install(h, "uv")

	if _, errOut, err := h.run(t, "", "new", "Study", "--lang", "python", "--workflow", "jupyter", "--offline"); err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	if got := f.ran[0].String(); !strings.Contains(got, "'framework-py>=0.1.0.dev14'") || !strings.HasSuffix(got, "--workflow jupyter --offline") {
		t.Errorf("new: %s", got)
	}

	dir := filepath.Join(h.app.home, "old")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(dir, "pyproject.toml"), "[project]\n")
	if _, errOut, err := h.run(t, "", "adopt", dir, "--yes"); err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	if got := f.ran[1].String(); !strings.Contains(got, "python -m framework adopt --path ") || !strings.HasSuffix(got, " --yes") {
		t.Errorf("adopt: %s", got)
	}

	for name, args := range map[string][]string{
		"stata quarto":    {"new", "S", "--lang", "stata", "--workflow", "quarto"},
		"pieces workflow": {"new", "S", "--structure", "framework/project", "--workflow", "jupyter"},
		"bad workflow":    {"new", "S", "--lang", "python", "--workflow", "notebooks"},
	} {
		if _, _, err := h.run(t, "", args...); exitCode(err) != exitUsage {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// fw adopt stops on an existing project before scanning, with the file it
// found and the command to run instead, for every language.
func TestAdoptExistingProject(t *testing.T) {
	h := newHarness(t, nil)
	f := &fakeEngines{}
	f.install(h, "Rscript", "uv")
	dir := oldStudy(t, h.app.home)
	writeTestFile(t, filepath.Join(dir, "settings.yml"), "default:\n  project_type: project\n")
	writeTestFile(t, filepath.Join(dir, "x.Rproj"), "")
	for _, args := range [][]string{{"adopt", dir}, {"adopt", dir, "--json"}, {"adopt", dir, "--lang", "python", "--yes"}, {"adopt", dir, "--lang", "stata", "--yes"}} {
		out, errOut, err := h.run(t, "", args...)
		if exitCode(err) != exitFailure || strings.Contains(errOut, "Proposed mapping") || out != "" || len(f.ran) != 0 {
			t.Errorf("%v: %v ran %v\n%s%s", args, err, f.ran, out, errOut)
		}
		var exit *exitError
		if !errors.As(err, &exit) || exit.found != "settings.yml" || exit.hint != "fw project upgrade" || exit.reason != "already_framework" ||
			!strings.Contains(err.Error(), "fw project upgrade") {
			t.Errorf("%v: %#v", args, err)
		}
	}
	var buf strings.Builder
	h.app.jsonOut = true
	report(&buf, h.app, alreadyProject(dir, "settings.yml"))
	var got map[string]any
	if err := json.Unmarshal([]byte(buf.String()), &got); err != nil || got["found"] != "settings.yml" || got["hint"] != "fw project upgrade" {
		t.Errorf("JSON error: %v %s", err, buf.String())
	}

	// A project's own config.yml is not Framework's.
	own := oldStudy(t, t.TempDir())
	writeTestFile(t, filepath.Join(own, "config.yml"), "repositories:\n  data:\n    name: x\n")
	if _, errOut, err := h.run(t, "", "adopt", own, "--lang", "stata", "--yes"); err != nil {
		t.Errorf("own config.yml: %v\n%s", err, errOut)
	}
}

// fw adopt --json prints JSON for R and Python: the proposal without --yes,
// and what was adopted with it. The package's own output goes to stderr.
func TestAdoptJSON(t *testing.T) {
	for _, lang := range []string{"r", "python"} {
		t.Run(lang, func(t *testing.T) {
			h := newHarness(t, nil)
			f := &fakeEngines{}
			f.install(h, "Rscript", "uv")
			dir := oldStudy(t, h.app.home)
			before := files(t, dir)

			out, _, err := h.run(t, "", "adopt", dir, "--lang", lang, "--json")
			var proposal map[string]any
			if err != nil || json.Unmarshal([]byte(out), &proposal) != nil || len(f.ran) != 0 {
				t.Fatalf("proposal: %v ran %v\n%s", err, f.ran, out)
			}
			if proposal["language"] != lang || proposal["adopted"] != false || proposal["directories"].(map[string]any)["inputs_raw"] != "data/raw" {
				t.Errorf("proposal: %s", out)
			}
			if got := files(t, dir); strings.Join(got, ",") != strings.Join(before, ",") {
				t.Fatal("the proposal changed files")
			}

			var seen engine.Command
			h.app.run = func(c engine.Command) (int, error) {
				seen = c
				writeTestFile(t, filepath.Join(dir, "settings.yml"), "default: {}\n")
				writeTestFile(t, filepath.Join(dir, ".framework", "spec.json"), `{"type":"bare","directories":{"inputs_raw":"data/raw"},"extra_directories":{},"security":{"sensitive":false}}`)
				return 0, nil
			}
			out, _, err = h.run(t, "", "adopt", dir, "--lang", lang, "--yes", "--json")
			var res struct {
				Path        string            `json:"path"`
				Language    string            `json:"language"`
				Created     []string          `json:"created"`
				Directories map[string]string `json:"directories"`
			}
			if err != nil || json.Unmarshal([]byte(out), &res) != nil {
				t.Fatalf("adopted: %v\n%s", err, out)
			}
			if !seen.StdoutToStderr || res.Path != dir || res.Language != lang || strings.Join(res.Created, ",") != ".framework/spec.json,settings.yml" || res.Directories["inputs_raw"] != "data/raw" {
				t.Errorf("adopted: %+v %+v", seen, res)
			}
		})
	}
}

// --type and --sensitive reach R and Python, and set Stata's spec.
func TestAdoptTypeAndSensitive(t *testing.T) {
	h := newHarness(t, nil)
	f := &fakeEngines{}
	f.install(h, "Rscript", "uv")
	dir := oldStudy(t, h.app.home)

	if _, _, err := h.run(t, "", "adopt", dir, "--type", "course", "--yes"); exitCode(err) != exitUsage {
		t.Errorf("course: %v", err)
	}
	_, errOut, err := h.run(t, "", "adopt", dir, "--lang", "r", "--type", "project_sensitive", "--yes")
	if err != nil || !strings.Contains(f.ran[0].Args[1], "type = a[2], sensitive = TRUE") || f.ran[0].Args[3] != "project_sensitive" || strings.Contains(errOut, "add --sensitive") {
		t.Errorf("R: %v %v\n%s", err, f.ran, errOut)
	}
	_, errOut, err = h.run(t, "", "adopt", dir, "--lang", "python", "--sensitive", "--yes")
	if err != nil || !strings.HasSuffix(f.ran[1].String(), "--yes --sensitive --force") {
		t.Errorf("python: %v %v\n%s", err, f.ran, errOut)
	}
	// Without --sensitive, private-looking folders get a hint.
	if _, errOut, _ = h.run(t, "", "adopt", dir, "--lang", "r", "--yes"); !strings.Contains(errOut, "add --sensitive") {
		t.Errorf("no hint:\n%s", errOut)
	}

	out, _, err := h.run(t, "", "adopt", dir, "--lang", "stata", "--type", "project", "--sensitive", "--yes", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Type        string            `json:"type"`
		Directories map[string]string `json:"directories"`
		Security    struct{ Sensitive bool }
	}
	if err := json.Unmarshal([]byte(readTestFile(t, filepath.Join(dir, ".framework", "spec.json"))), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Type != "project" || !doc.Security.Sensitive || doc.Directories["outputs_tables"] != "" {
		t.Errorf("stata spec: %+v\n%s", doc, out)
	}
	if _, err := os.Stat(filepath.Join(dir, "master.do")); err == nil {
		t.Error("adopt wrote master.do")
	}
}

// A project of loose files, with no folders, can be adopted.
func TestAdoptNoFolders(t *testing.T) {
	h := newHarness(t, nil)
	f := &fakeEngines{}
	f.install(h, "Rscript")
	dir := filepath.Join(h.app.home, "single")
	writeTestFile(t, filepath.Join(dir, "analysis.qmd"), "```{r}\n1\n```\n")
	_, errOut, err := h.run(t, "", "adopt", dir, "--yes")
	if err != nil || len(f.ran) != 1 || !strings.Contains(errOut, "Language: r (from analysis.qmd)") {
		t.Errorf("R: %v %v\n%s", err, f.ran, errOut)
	}

	stataDir := filepath.Join(h.app.home, "loose")
	writeTestFile(t, filepath.Join(stataDir, "clean.do"), "use x\n")
	if _, errOut, err := h.run(t, "", "adopt", stataDir); exitCode(err) != exitUsage {
		t.Errorf("no --yes: %v\n%s", err, errOut)
	}
	if _, errOut, err := h.run(t, "", "adopt", stataDir, "--yes"); err != nil || !strings.Contains(errOut, "No folders to map") {
		t.Errorf("stata: %v\n%s", err, errOut)
	}
	if !strings.Contains(readTestFile(t, filepath.Join(stataDir, "settings.do")), "global root") {
		t.Error("no settings.do")
	}
}

// fw adopt adds only the .gitignore lines a Stata project lacks, keeps the
// rest, and lists them in the proposal. A --lang that disagrees with the
// files gets a warning.
func TestAdoptStataGitignore(t *testing.T) {
	h := newHarness(t, nil)
	dir := oldStudy(t, h.app.home)
	writeTestFile(t, filepath.Join(dir, ".gitignore"), "# mine\n*.dta\n.env\n")
	writeTestFile(t, filepath.Join(dir, "study.Rproj"), "")
	if err := os.Remove(filepath.Join(dir, "analysis.do")); err != nil {
		t.Fatal(err)
	}

	out, errOut, err := h.run(t, "", "adopt", dir, "--lang", "stata", "--json")
	var proposal struct {
		Additions []string `json:"gitignore_additions"`
		LeftOut   []string `json:"gitignore_left_out"`
	}
	// The folder's .gitignore has rules, so no negation is appended after them.
	if err != nil || json.Unmarshal([]byte(out), &proposal) != nil || strings.Join(proposal.Additions, " ") != ".env.* **/private/** **/*_private/** /output/private/" ||
		strings.Join(proposal.LeftOut, " ") != "!.env.example" ||
		!strings.Contains(errOut, "Warning: the files say r (from study.Rproj), but --lang says stata") {
		t.Fatalf("%v %+v\n%s", err, proposal, errOut)
	}
	out, _, err = h.run(t, "", "adopt", dir, "--lang", "stata", "--yes")
	if err != nil || !strings.Contains(out, "Left out of .gitignore: !.env.example.") {
		t.Fatalf("%v\n%s", err, out)
	}
	ignore := readTestFile(t, filepath.Join(dir, ".gitignore"))
	if !strings.HasPrefix(ignore, "# mine\n*.dta\n.env\n\n# Added by fw adopt") || strings.Count(ignore, "\n.env\n") != 1 || strings.Contains(ignore, "!") {
		t.Errorf(".gitignore:\n%s", ignore)
	}
}

// The proposal infers the type with the shared rules: a course for R, a
// project for Stata, which has no course type; render folders are listed
// for R and become other folders in Stata.
func TestAdoptInfersType(t *testing.T) {
	h := newHarness(t, nil)
	dir := filepath.Join(h.app.home, "course")
	for _, d := range []string{"slides", "modules/01", "assignments", "data/raw", "outputs/notebooks", "settings", "report_files"} {
		if err := os.MkdirAll(filepath.Join(dir, filepath.FromSlash(d)), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	proposal := func(args ...string) map[string]any {
		t.Helper()
		out, errOut, err := h.run(t, "", append([]string{"adopt", dir, "--json"}, args...)...)
		var res map[string]any
		if err != nil || json.Unmarshal([]byte(out), &res) != nil {
			t.Fatalf("%v\n%s%s", err, out, errOut)
		}
		return res
	}
	r := proposal("--lang", "r")
	dirs := r["directories"].(map[string]any)
	if r["type"] != "course" || dirs["slides"] != "slides" || dirs["notebooks"] != "modules" || dirs["data"] != "data" ||
		r["render_dirs"].(map[string]any)["notebooks"] != "outputs/notebooks" || strings.Contains(fmt.Sprint(r), "settings") || strings.Contains(fmt.Sprint(r), "report_files") {
		t.Errorf("r: %v", r)
	}
	st := proposal("--lang", "stata")
	if st["type"] != "project" || st["directories"].(map[string]any)["inputs_raw"] != "data/raw" || len(st["render_dirs"].(map[string]any)) != 0 ||
		st["extra_directories"].(map[string]any)["outputs_notebooks"] == nil {
		t.Errorf("stata: %v", st)
	}
	if _, _, err := h.run(t, "", "adopt", dir, "--lang", "stata", "--type", "course", "--yes"); exitCode(err) != exitUsage {
		t.Errorf("stata course: %v", err)
	}
}
