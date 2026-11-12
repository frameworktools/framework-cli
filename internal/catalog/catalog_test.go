package catalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func ptr[T any](v T) *T { return &v }

func TestRowsFindsEveryLayout(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "settings.yml"), `# top
default:
  project_name: x
  data:
    inputs:
      raw:
        survey:
          path: inputs/raw/survey.csv
          type: csv
          locked: true
          stata_names:
            long_column: short
        broken:
          type: csv
      final.analysis: inputs/final/analysis.dta
    outputs.tables.main:
      path: outputs/main.csv
`)
	f, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	rows := f.Rows()
	if len(rows) != 3 {
		t.Fatalf("rows: %+v", rows)
	}
	s := rows["inputs.raw.survey"]
	if s.Path != "inputs/raw/survey.csv" || !s.Locked || *s.Type != "csv" || s.StataNames["long_column"] != "short" {
		t.Errorf("survey: %+v", s)
	}
	if rows["inputs.final.analysis"].Path != "inputs/final/analysis.dta" || rows["outputs.tables.main"].Path != "outputs/main.csv" {
		t.Errorf("mixed and flat: %+v", rows)
	}
}

func TestSetKeepsCommentsAndLocalFields(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "settings.yml"), `# top comment
default:
  project_name: x
  # the catalog
  data:
    inputs:
      raw.survey:
        path: old.csv
        note: mine
  publish:
    default: local
`)
	f, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	f.Set(Row{Name: "inputs.raw.survey", Path: "inputs/raw/survey.csv", Type: ptr("csv"), Locked: true})
	f.Set(Row{Name: "inputs.final.analysis", Path: "inputs/final/a.dta", Description: ptr("Analysis")})
	if err := f.Save(); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "settings.yml"))
	text := string(got)
	for _, want := range []string{"# top comment", "# the catalog", "raw.survey:\n        path: inputs/raw/survey.csv\n        type: csv\n        locked: true\n        note: mine",
		"final:\n        analysis:\n          path: inputs/final/a.dta\n          description: Analysis", "publish:\n    default: local"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in\n%s", want, text)
		}
	}
	again, _ := Open(dir)
	if r := again.Rows()["inputs.raw.survey"]; !r.Locked || r.Path != "inputs/raw/survey.csv" {
		t.Errorf("reread: %+v", r)
	}
}

func TestSplitFileAndEmptyCatalog(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "settings.yml"), "default:\n  data: settings/data.yml\n")
	write(t, filepath.Join(dir, "settings", "data.yml"), "data: {}\n")
	f, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	f.Set(Row{Name: "a.b", Path: "a/b.csv"})
	if err := f.Save(); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "settings", "data.yml"))
	if string(got) != "data:\n  a:\n    b:\n      path: a/b.csv\n" {
		t.Errorf("split: %q", got)
	}
	settings, _ := os.ReadFile(filepath.Join(dir, "settings.yml"))
	if string(settings) != "default:\n  data: settings/data.yml\n" {
		t.Errorf("settings changed: %q", settings)
	}
}

func TestProblemsAndRender(t *testing.T) {
	problems := Problems(map[string]any{
		"inputs.raw":        map[string]any{"path": "inputs/raw.csv"},
		"inputs.raw.survey": map[string]any{"path": "../x.csv", "type": "word", "hash": "abc"},
	})
	joined := strings.Join(problems, "\n")
	for _, want := range []string{"unknown field 'hash'", "must be relative", "type must be one of", "'inputs.raw' is an entry"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in %s", want, joined)
		}
	}
	out, err := Render(map[string]any{"inputs.raw.survey": map[string]any{"path": "inputs/raw/s.csv", "locked": true}}, 2)
	if err != nil || out != "  data:\n    inputs:\n      raw:\n        survey:\n          path: inputs/raw/s.csv\n          locked: true" {
		t.Errorf("render: %v %q", err, out)
	}
}

func TestDiffNamesTheFieldsThatChanged(t *testing.T) {
	desc := "Survey"
	from := Row{Name: "survey", Path: "a.csv", Locked: true}
	if got := Diff(from, Row{Name: "survey", Path: "a.csv", Locked: true, Description: &desc}); got != "description changed" {
		t.Errorf("description: %q", got)
	}
	if got := Diff(from, Row{Name: "survey", Path: "a.csv", Locked: true, StataNames: map[string]string{"long_name": "short"}}); got != "stata_names changed" {
		t.Errorf("stata_names: %q", got)
	}
	typ := "csv"
	if got := Diff(from, Row{Name: "survey", Path: "b.csv", Type: &typ, Description: &desc}); got != "path a.csv -> b.csv, type none -> csv, locked true -> false, description changed" {
		t.Errorf("several: %q", got)
	}
}
