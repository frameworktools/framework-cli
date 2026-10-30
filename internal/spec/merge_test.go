package spec

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

type conformanceCase struct {
	Description string  `json:"description"`
	Layers      []Entry `json:"layers"`
	Expected    struct {
		Spec      json.RawMessage `json:"spec"`
		Gitignore []Block         `json:"gitignore_blocks"`
		Conflicts []Conflict      `json:"conflicts"`
	} `json:"expected"`
}

// TestConformanceMerge runs the shared fixtures from the R package.
func TestConformanceMerge(t *testing.T) {
	files, err := filepath.Glob("testdata/conformance/merge/*.json")
	if err != nil || len(files) == 0 {
		t.Fatalf("no fixtures: %v", err)
	}
	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			data, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			var c conformanceCase
			if err := json.Unmarshal(data, &c); err != nil {
				t.Fatal(err)
			}
			labeled, err := Envelope{Layers: c.Layers}.LabeledLayers()
			if err != nil {
				t.Fatal(err)
			}
			got, err := Merge(labeled)
			if err != nil {
				t.Fatal(err)
			}

			want, err := DecodeObject(c.Expected.Spec)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(normalize(got.Spec), normalize(want)) {
				g, _ := json.MarshalIndent(got.Spec, "", "  ")
				t.Errorf("%s\nspec:\n%s\nwant:\n%s", c.Description, g, c.Expected.Spec)
			}

			if len(got.Gitignore) != len(c.Expected.Gitignore) || (len(got.Gitignore) > 0 && !reflect.DeepEqual(got.Gitignore, c.Expected.Gitignore)) {
				t.Errorf("gitignore blocks %+v; want %+v", got.Gitignore, c.Expected.Gitignore)
			}

			if !reflect.DeepEqual(conflictSet(got.Conflicts), conflictSet(c.Expected.Conflicts)) {
				t.Errorf("conflicts %+v; want %+v", got.Conflicts, c.Expected.Conflicts)
			}
		})
	}
}

// normalize makes {} and [] compare equal, as the fixtures require.
func normalize(v any) any {
	switch t := v.(type) {
	case map[string]any:
		if len(t) == 0 {
			return "<empty>"
		}
		out := map[string]any{}
		for k, x := range t {
			out[k] = normalize(x)
		}
		return out
	case []any:
		if len(t) == 0 {
			return "<empty>"
		}
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = normalize(x)
		}
		return out
	}
	return v
}

func conflictSet(cs []Conflict) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.Path+" | "+c.Layers)
	}
	sort.Strings(out)
	return out
}

func TestMergeDoesNotChangeInput(t *testing.T) {
	layer := map[string]any{"git": map[string]any{"gitignore_content": "x"}, "directories": map[string]any{"a": "a"}}
	if _, err := Merge([]Labeled{{Label: "l", Layer: layer}}); err != nil {
		t.Fatal(err)
	}
	if _, ok := layer["git"].(map[string]any)["gitignore_content"]; !ok {
		t.Error("Merge changed its input")
	}
}

func TestDefaultsNeverConflict(t *testing.T) {
	got, _ := Merge([]Labeled{
		{Label: DefaultsLabel, Layer: map[string]any{"type": "project"}},
		{Label: "project", Layer: map[string]any{"type": "bare"}},
	})
	if len(got.Conflicts) != 0 {
		t.Errorf("conflicts %+v", got.Conflicts)
	}
}
