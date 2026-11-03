package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
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
	if !strings.Contains(errOut, "'output/private' looks private") || !strings.Contains(out, "settings.do and .framework/spec.json") {
		t.Errorf("output:\n%s\n%s", out, errOut)
	}
	added := map[string]bool{}
	for _, f := range files(t, dir) {
		added[f] = true
	}
	for _, f := range before {
		delete(added, f)
	}
	if len(added) != 3 || !added["settings.do"] || !added[".framework"] || !added[".framework/spec.json"] {
		t.Errorf("adopt should add only settings.do and the spec: %v", added)
	}
	var doc struct {
		Type        string                       `json:"type"`
		Directories map[string]string            `json:"directories"`
		Extra       map[string]map[string]string `json:"extra_directories"`
		AI          struct{ Enabled bool }       `json:"ai"`
		Source      map[string]any               `json:"source"`
	}
	data, _ := os.ReadFile(filepath.Join(dir, ".framework", "spec.json")) // #nosec G304 -- test path
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Type != "bare" || doc.Directories["do"] != "do" || doc.Directories["inputs_raw"] != "data/raw" || doc.Extra["irb"]["path"] != "IRB" || doc.AI.Enabled ||
		doc.Source["origin"] != "cli" || doc.Source["adopted"] != true || doc.Source["fw_version"] == nil {
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
	if _, _, err := h.run(t, "", "adopt", empty, "--yes", "--lang", "r"); err == nil || !strings.Contains(err.Error(), "framework::adopt") {
		t.Errorf("R: %v", err)
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

	out, _, err = h.run(t, "", "structure", "import", dir, "--yes", "--name", "Old Study")
	if err != nil || !strings.Contains(out, "ada/old-study") {
		t.Fatalf("%v\n%s", err, out)
	}
	var sent map[string]any
	if err := json.Unmarshal([]byte(lastStructure), &sent); err != nil {
		t.Fatal(err)
	}
	dirs, _ := sent["directories"].(map[string]any)
	if sent["name"] != "Old Study" || dirs["do"] != "do" || dirs["inputs_final"] != "data/clean" {
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
	if got := f.ran[0].String(); !strings.Contains(got, "'framework-py>=0.1.0.dev6'") || !strings.HasSuffix(got, "--workflow jupyter --offline") {
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
		"r workflow":      {"new", "S", "--workflow", "quarto"},
	} {
		if _, _, err := h.run(t, "", args...); exitCode(err) != exitUsage {
			t.Errorf("%s: %v", name, err)
		}
	}
}
