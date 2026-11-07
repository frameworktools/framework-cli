package adopt

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestConformance runs the shared adopt/ cases from the R package.
func TestConformance(t *testing.T) {
	files, _ := filepath.Glob("../spec/testdata/conformance/adopt/*.json")
	if len(files) == 0 {
		t.Fatal("no adopt/ cases")
	}
	rules := Shared()
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			data, err := os.ReadFile(f) // #nosec G304 -- test fixtures
			if err != nil {
				t.Fatal(err)
			}
			var c struct {
				Language string   `json:"language"`
				Folders  []string `json:"folders"`
				Expected Mapping  `json:"expected"`
			}
			if err := json.Unmarshal(data, &c); err != nil {
				t.Fatal(err)
			}
			if c.Language == "" {
				c.Language = "r"
			}
			got := rules.Map(c.Folders, c.Language, "")
			if !reflect.DeepEqual(got, c.Expected) {
				g, _ := json.MarshalIndent(got, "", "  ")
				w, _ := json.MarshalIndent(c.Expected, "", "  ")
				t.Errorf("got\n%s\nwant\n%s", g, w)
			}
		})
	}
}

func TestScan(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"data/raw/2024/jan", "data/clean", ".git/objects", "renv/library", "Node_Modules/x", "docs/.hidden"} {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(d)), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "data", "notes.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Shared().Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"data", "data/clean", "data/raw", "data/raw/2024", "docs"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestSetRole(t *testing.T) {
	r := Shared()
	m := r.Map([]string{"data", "data/raw", "IRB", "archive", "Archive"}, "r", "")
	m = r.SetRole(m, "IRB", "docs")
	m = r.SetRole(m, "data/raw", "leave")
	// data is a course rule's key, so it is numbered, as in R
	m = r.SetRole(m, "data", "other")
	if m.Directories["docs"] != "IRB" || m.Directories["inputs_raw"] != "" || len(m.ExtraDirectories) != 3 || m.ExtraDirectories["data_2"].Path != "data" {
		t.Errorf("%+v", m)
	}
	// A folder set to other never replaces another extra (B26)
	m = r.SetRole(m, "archive", "leave")
	m = r.SetRole(m, "archive", "other")
	if m.ExtraDirectories["archive"].Path != "Archive" || m.ExtraDirectories["archive_2"].Path != "archive" || len(m.ExtraDirectories) != 3 {
		t.Errorf("%+v", m.ExtraDirectories)
	}
}

func TestDetectLanguage(t *testing.T) {
	for files, want := range map[string]string{
		"master.do": "stata", "pyproject.toml": "python", "study.Rproj": "r", "": "", "a.do,renv.lock": "",
		// weaker signs, used only without project files
		"setup.py": "python", "requirements-dev.txt,manage.py": "python", "setup.cfg": "python", "explore.ipynb": "python",
		"clean.R": "r", "report.Rmd": "r", "analysis.qmd": "r", "py.qmd": "python",
		"setup.py,study.Rproj": "r", "clean.R,requirements.txt": "",
	} {
		root := t.TempDir()
		var first string
		if files != "" {
			for _, f := range strings.Split(files, ",") {
				content := ""
				switch f {
				case "analysis.qmd":
					content = "# A\n\n```{r}\n1\n```\n"
				case "py.qmd":
					content = "# A\n\n```{python}\n1\n```\n"
				}
				if err := os.WriteFile(filepath.Join(root, f), []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
				if first == "" {
					first = f
				}
			}
		}
		got, source := DetectLanguage(root)
		if got != want {
			t.Errorf("%q: got %q, want %q", files, got, want)
		}
		if got != "" && source == "" {
			t.Errorf("%q: no source", files)
		}
	}
}

func TestExisting(t *testing.T) {
	write := func(dir, rel, content string) {
		t.Helper()
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for name, c := range map[string]struct {
		files map[string]string
		want  string
	}{
		"nothing":            {map[string]string{"README.md": "x"}, ""},
		"settings.yml":       {map[string]string{"settings.yml": "default: {}\n"}, "settings.yml"},
		"settings.yaml":      {map[string]string{"settings.yaml": "default: {}\n"}, "settings.yaml"},
		"spec first":         {map[string]string{"settings.yml": "", ".framework/spec.json": "{}"}, ".framework/spec.json"},
		"stata":              {map[string]string{"settings.do": ""}, "settings.do"},
		"framework config":   {map[string]string{"config.yml": "default:\n  project_type: project\n  packages: [dplyr]\n"}, "config.yml"},
		"own config":         {map[string]string{"config.yml": "repositories:\n  data:\n    name: x\n"}, ""},
		"config package":     {map[string]string{"config.yml": "default:\n  db: postgres\n  data: inputs\n"}, ""},
		"unparseable config": {map[string]string{"config.yml": "default: [\n"}, ""},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			for rel, content := range c.files {
				write(dir, rel, content)
			}
			if got := Existing(dir); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

// Skip entries may be globs, such as Quarto's *_files render folders. The
// shared rules come from the R package, so the test adds its own entries.
func TestScanSkipGlobs(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"settings", "report_files/figure-html", "Analysis_Cache", "_site", "scripts", "profiles"} {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(d)), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	rules := Shared()
	rules.Skip = append(rules.Skip, "settings", "_site", "*_files", "*_cache")
	folders, err := rules.Scan(root)
	if err != nil || strings.Join(folders, ",") != "profiles,scripts" {
		t.Errorf("scan: %v %v", folders, err)
	}
	m := rules.Map([]string{"settings", "nb_files", "scripts"}, "r", "")
	if len(m.ExtraDirectories) != 0 || m.Directories["scripts"] != "scripts" {
		t.Errorf("map: %+v", m)
	}
}
