package cli

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// structureProject is the shared fixture in a linked project, in a git
// work tree.
func structureProject(t *testing.T, h *harness, url string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root := linkedProjectAt(t, h, url, "my-project")
	files := map[string]string{
		".gitignore": "data/raw/\n*.rds\n.env\n", "README.md": "", "R/clean.R": "",
		"data/raw/a.csv": "", "data/raw/b.csv": "", "data/private/ids.csv": "",
		"outputs/fit.rds": "", "outputs/draws.rds": "", "outputs/table.csv": "",
	}
	for rel, content := range files {
		write(t, root, rel, content)
	}
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	return root
}

func TestProjectStructurePreview(t *testing.T) {
	srv := fakeCloud(t)
	h := newHarness(t, nil)
	structureProject(t, h, srv.URL)
	lastUpload = nil
	out, errOut, err := h.run(t, "", "project", "structure", "--note", "R=Reusable functions")
	if err != nil {
		t.Fatal(err, errOut)
	}
	for _, want := range []string{
		"my-project/\n",
		"│   ├── private/   private · 1 file\n",
		"│   └── raw/       ignored by data/raw/ (.gitignore:1) · 2 files\n",
		"│   ├── draws.rds  ignored by *.rds (.gitignore:2)\n",
		"├── R/             Reusable functions\n",
		"└── .env           ignored by .env (.gitignore:3)\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "a.csv") || strings.Contains(out, "ids.csv") || !strings.Contains(errOut, "Nothing was uploaded") || lastUpload != nil {
		t.Errorf("preview:\n%s\n%s", out, errOut)
	}
	if _, _, err := h.run(t, "", "project", "structure", "--note", "nope=x"); exitCode(err) != exitUsage {
		t.Errorf("note on a missing path: %v", err)
	}
	if _, _, err := h.run(t, "", "project", "structure", "--ignored", "maybe"); exitCode(err) != exitUsage {
		t.Errorf("bad --ignored: %v", err)
	}
}

func TestPublishStructure(t *testing.T) {
	srv := fakeCloud(t)
	h := newHarness(t, nil)
	structureProject(t, h, srv.URL)
	out, errOut, err := h.run(t, "", "publish", "--structure", "--note", "R/=Reusable functions", "--open", "R", "--highlight", "README.md", "--display", "ignored=off", "--yes")
	if err != nil {
		t.Fatal(err, errOut)
	}
	if lastUpload["kind"] != "tree" || lastUpload["slug"] != "structure" || lastUpload["title"] != "my-project structure" || lastUpload["name"] != "structure.json" || lastUpload["display"] != `{"ignored":false}` {
		t.Errorf("upload: %+v", lastUpload)
	}
	if out != "Published analysis, version 2.\nShare:  https://x/go/abcd\nManage: https://x/artifacts/7\n" {
		t.Errorf("stdout:\n%s", out)
	}
	if !strings.Contains(errOut, "│   └── *.rds     ignored (.gitignore:2) · 2 files\n") || strings.Contains(errOut, "fit.rds") {
		t.Errorf("stderr:\n%s", errOut)
	}
	data := lastUpload["data"]
	for _, name := range []string{"a.csv", "b.csv", "ids.csv", "fit.rds", "draws.rds", "FW_PROJECT_TOKEN"} {
		if strings.Contains(data, name) {
			t.Errorf("the upload names %s:\n%s", name, data)
		}
	}
	var doc struct {
		Source    map[string]string `json:"source"`
		Root      string            `json:"root"`
		Method    string            `json:"method"`
		Open      []string          `json:"open"`
		Highlight []string          `json:"highlight"`
		Nodes     []struct {
			Path    string         `json:"path"`
			Type    string         `json:"type"`
			Note    string         `json:"note"`
			Private bool           `json:"private"`
			Hidden  *int           `json:"hidden"`
			Ignored map[string]any `json:"ignored"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal([]byte(data), &doc); err != nil {
		t.Fatal(err, data)
	}
	if doc.Source["language"] != "shell" || doc.Root != "my-project" || doc.Method != "git" || strings.Join(doc.Open, ",") != "R" || strings.Join(doc.Highlight, ",") != "README.md" {
		t.Errorf("document: %s", data)
	}
	got := map[string]bool{}
	for _, n := range doc.Nodes {
		key := n.Path + " " + n.Type
		if n.Ignored != nil {
			key += " " + n.Ignored["rule"].(string)
		}
		if n.Hidden != nil {
			key += " " + strings.Repeat("#", *n.Hidden)
		}
		if n.Private {
			key += " private"
		}
		if n.Note != "" {
			key += " " + n.Note
		}
		got[key] = true
	}
	for _, want := range []string{
		".gitignore file", " ignored .env #", "README.md file", "R dir Reusable functions", "R/clean.R file", "data dir",
		"data/raw dir data/raw/ ##", "data/private dir # private", "outputs dir", "outputs ignored *.rds ##", "outputs/table.csv file",
	} {
		if !got[want] {
			t.Errorf("missing node %q in %v", want, got)
		}
	}
}

func TestPublishStructureOptions(t *testing.T) {
	srv := fakeCloud(t)
	h := newHarness(t, nil)
	root := structureProject(t, h, srv.URL)
	write(t, root, "settings.yml", "default:\n  directories:\n    outputs_private: outputs\n")
	if _, errOut, err := h.run(t, "", "publish", "--structure", "R", "--yes"); err != nil || lastUpload["slug"] != "structure-r" || lastUpload["title"] != "R structure" || lastUpload["name"] != "structure-r.json" {
		t.Errorf("subfolder: %v %s %+v", err, errOut, lastUpload)
	}
	if _, errOut, err := h.run(t, "", "publish", "--structure", "--ignored", "hide", "--slug", "layout", "--yes"); err != nil {
		t.Fatal(err, errOut)
	}
	data := lastUpload["data"]
	if strings.Contains(data, `"ignored"`) || strings.Contains(data, "data/raw") || !strings.Contains(data, `"path": "outputs",`) || !strings.Contains(data, `"private": true`) || strings.Contains(data, "table.csv") || lastUpload["slug"] != "layout" {
		t.Errorf("hide, with outputs private in settings:\n%s", data)
	}
	for _, args := range [][]string{
		{"publish", "--structure", "data/raw", "--yes"},
		{"publish", "--structure", "data/private", "--yes"},
		{"publish", "--structure", "--kind", "html", "--yes"},
		{"publish", "--structure", "--highlight", "nope", "--yes"},
		{"publish", "--structure", "--open", "README.md", "--yes"},
		{"publish", "README.md", "--depth", "2", "--yes"},
		{"publish", "README.md", "R/clean.R", "--yes"},
	} {
		if _, _, err := h.run(t, "", args...); err == nil {
			t.Errorf("%v: no error", args)
		}
	}
}
