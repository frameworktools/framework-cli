package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// pinned is where syncassets copies the cases from the R repository.
const pinned = "../../internal/spec/testdata/conformance/create/stata"

// TestPinnedCases creates each pinned case's spec and compares the paths,
// as every engine does with its language's cases.
func TestPinnedCases(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join(pinned, "*.json"))
	if len(files) == 0 {
		t.Skip("no create/stata cases are pinned yet")
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			data, err := os.ReadFile(f) // #nosec G304 -- test fixtures
			if err != nil {
				t.Fatal(err)
			}
			var c Case
			if err := json.Unmarshal(data, &c); err != nil {
				t.Fatal(err)
			}
			got, err := Create(c.Spec)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, c.Expected.Paths) {
				t.Errorf("paths differ\n got: %v\nwant: %v", got, c.Expected.Paths)
			}
		})
	}
}

// TestCasesUpToDate fails when fw's Stata creation has changed since the
// cases were generated, so they get regenerated and sent to the R repository.
func TestCasesUpToDate(t *testing.T) {
	for _, c := range cases {
		got, err := build(c.description, c.layers)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		data, err := os.ReadFile(filepath.Join(pinned, c.name+".json")) // #nosec G304 -- test fixtures
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		fresh, _ := json.MarshalIndent(got, "", "  ")
		if string(append(fresh, '\n')) != string(data) {
			t.Errorf("%s is out of date. Run go run ./tools/createfixtures --out <R repo>/inst/conformance/create/stata and send the files to the R repository", c.name)
		}
	}
}
