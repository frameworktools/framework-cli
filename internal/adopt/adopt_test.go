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
			got := rules.Map(c.Folders, c.Language)
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
	m := r.Map([]string{"data", "data/raw", "IRB"}, "r")
	m = r.SetRole(m, "IRB", "docs")
	m = r.SetRole(m, "data/raw", "leave")
	m = r.SetRole(m, "data", "other")
	if m.Directories["docs"] != "IRB" || m.Directories["inputs_raw"] != "" || len(m.ExtraDirectories) != 1 || m.ExtraDirectories["data"].Path != "data" {
		t.Errorf("%+v", m)
	}
}

func TestDetectLanguage(t *testing.T) {
	for files, want := range map[string]string{"master.do": "stata", "pyproject.toml": "python", "study.Rproj": "r", "": "", "a.do,renv.lock": ""} {
		root := t.TempDir()
		if files != "" {
			for _, f := range strings.Split(files, ",") {
				if err := os.WriteFile(filepath.Join(root, f), nil, 0o600); err != nil {
					t.Fatal(err)
				}
			}
		}
		if got := DetectLanguage(root); got != want {
			t.Errorf("%q: got %q, want %q", files, got, want)
		}
	}
}
