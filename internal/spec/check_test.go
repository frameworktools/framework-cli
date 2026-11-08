package spec

import (
	"encoding/json"
	"strings"
	"testing"
)

// validSpec matches spec.v1.
const validSpec = `{"spec_version":1,"name":"study","type":"project","language":"r",
"author":{"name":"Ada","email":"ada@example.edu","affiliation":""},
"directories":{"inputs_raw":"inputs/raw"},"extra_directories":{},
"render_dirs":{},"packages":{"default_packages":{}},"scaffold":{"seed_on_scaffold":false,"seed":""},
"connections":{"databases":{},"storage_buckets":{}},
"security":{"sensitive":false},"git":{"use_git":true,"hooks":{},"gitignore_content":""},"ai":{"enabled":false,"assistants":{},"agents_md":"none"},
"env":{},"data_hooks":{},"files":{},"skills":{},"source":{"origin":"r"}}`

func edit(t *testing.T, f func(doc map[string]any)) []byte {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(validSpec), &doc); err != nil {
		t.Fatal(err)
	}
	f(doc)
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestCheckSpecValid(t *testing.T) {
	c, err := CheckSpec([]byte(validSpec))
	if err != nil || len(c.Problems) != 0 || len(c.Dropped) != 0 || string(c.Data) != validSpec {
		t.Errorf("%v %+v", err, c)
	}
}

// The keys an older upgrade() wrote are dropped and reported;
// what is left matches the schema, and nothing else changes.
func TestCheckSpecStaleKeys(t *testing.T) {
	data := edit(t, func(doc map[string]any) {
		doc["scaffold"].(map[string]any)["positron"] = true
		doc["source"].(map[string]any)["upgraded_from"] = "1.1.0"
		doc["name"] = "A & <B>"
	})
	c, err := CheckSpec(data)
	if err != nil || len(c.Problems) != 0 || strings.Join(c.Dropped, ",") != "scaffold.positron,source.upgraded_from" {
		t.Fatalf("%v %+v", err, c)
	}
	if strings.Contains(string(c.Data), "positron") || strings.Contains(string(c.Data), "upgraded_from") || !strings.Contains(string(c.Data), `"A & <B>"`) {
		t.Errorf("sent %s", c.Data)
	}
	var before, after map[string]any
	_ = json.Unmarshal(data, &before)
	_ = json.Unmarshal(c.Data, &after)
	delete(before["scaffold"].(map[string]any), "positron")
	delete(before["source"].(map[string]any), "upgraded_from")
	b, _ := json.Marshal(before)
	a, _ := json.Marshal(after)
	if string(a) != string(b) {
		t.Errorf("changed more than the stale keys:\n%s\n%s", a, b)
	}
}

// Every other problem is named by its dot path, in order.
func TestCheckSpecProblems(t *testing.T) {
	data := edit(t, func(doc map[string]any) {
		doc["git"].(map[string]any)["remote"] = "x"
		delete(doc, "author")
		doc["directories"].(map[string]any)["Bad Key"] = "y"
		doc["connections"].(map[string]any)["databases"] = map[string]any{"main": map[string]any{"host": "h"}}
		doc["security"].(map[string]any)["sensitive"] = "yes"
		doc["upgraded_at"] = "today"
	})
	c, err := CheckSpec(data)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, p := range c.Problems {
		got = append(got, p.String())
	}
	want := []string{
		"author: missing",
		"connections.databases.main.driver: missing",
		"directories.Bad Key: not an allowed key name",
		"git.remote: not part of spec.v1",
		"security.sensitive: got string, want boolean",
		"upgraded_at: not part of spec.v1",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("problems:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if _, err := CheckSpec([]byte("not json")); err == nil {
		t.Error("not JSON: no error")
	}
}
