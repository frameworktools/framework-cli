package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/frameworktools/framework-cli/internal/catalog"
)

// dataCloud is a fake registry: GET and POST /api/v1/projects/data.
type dataCloud struct {
	mu       sync.Mutex
	url      string
	revision string
	entries  []catalog.Row
	posts    []map[string]any
	missing  bool
	handler  http.Handler
}

func newDataCloud(t *testing.T) *dataCloud {
	t.Helper()
	c := &dataCloud{revision: "rev-1"}
	c.handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		defer c.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Authorization") != "Bearer proj-tok" {
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"message":"Unauthenticated."}`))
			return
		}
		if c.missing || r.URL.Path != "/api/v1/projects/data" {
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"message":"Not found"}`))
			return
		}
		if r.Method == "GET" {
			_ = json.NewEncoder(w).Encode(map[string]any{"revision": c.revision, "entries": c.entries})
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		c.posts = append(c.posts, body)
		if body["force"] != true && body["base_revision"] != c.revision {
			w.WriteHeader(409)
			_, _ = w.Write([]byte(`{"message":"Pull first","revision":"` + c.revision + `"}`))
			return
		}
		names := map[string]bool{}
		var added, kept, removed []catalog.Row
		for _, e := range body["entries"].([]any) {
			m := e.(map[string]any)
			names[m["name"].(string)] = true
			found := false
			for _, x := range c.entries {
				found = found || x.Name == m["name"]
			}
			if !found {
				added = append(added, catalog.Row{Name: m["name"].(string), Path: m["path"].(string)})
			}
		}
		for _, x := range c.entries {
			if !names[x.Name] {
				if body["remove"] == true {
					removed = append(removed, x)
				} else {
					kept = append(kept, x)
				}
			}
		}
		dry := body["dry_run"] == true
		if !dry && len(added)+len(removed) > 0 {
			c.revision = "rev-2"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"applied": !dry && len(added)+len(removed) > 0, "revision": c.revision,
			"changes": map[string]any{"added": added, "changed": []any{}, "removed": removed, "kept": kept}})
	})
	srv := httptest.NewServer(c.handler)
	t.Cleanup(srv.Close)
	c.url = srv.URL
	return c
}

func stataDataProject(t *testing.T, home, server string) string {
	t.Helper()
	dir := stataSkillsProject(t, home, server, true)
	writeTestFile(t, filepath.Join(dir, "settings.yml"), "# Settings\ndefault:\n  project_name: Trial\n  # Catalog\n  data:\n    inputs:\n      raw:\n        survey:\n          path: inputs/raw/survey.csv\n          stata_names:\n            long_name: short\n")
	return dir
}

func strp(s string) *string { return &s }

func TestDataPullStata(t *testing.T) {
	c := newDataCloud(t)
	h := newHarness(t, nil)
	dir := stataDataProject(t, h.app.home, c.url)
	h.app.cwd = dir
	settings := filepath.Join(dir, "settings.yml")
	lock := filepath.Join(dir, ".framework", "data.lock.json")
	c.entries = []catalog.Row{
		{Name: "inputs.final.analysis", Path: "inputs/final/analysis.dta", Type: strp("stata"), Locked: true},
		{Name: "inputs.raw.survey", Path: "inputs/raw/survey_v2.csv"},
	}

	out, _, err := h.run(t, "", "data", "pull", "--dry-run")
	if err != nil || !strings.Contains(out, "Data registry changes from Framework Cloud (revision rev-1):\n  Entries to add: inputs.final.analysis\n") ||
		!strings.Contains(out, "Entries that differ (local -> Cloud): inputs.raw.survey (path inputs/raw/survey.csv -> inputs/raw/survey_v2.csv, stata_names changed) — kept; fw data pull --overwrite uses Cloud's") {
		t.Fatalf("dry run: %v\n%s", err, out)
	}
	if _, err := os.Stat(lock); err == nil {
		t.Error("a dry run saved the revision")
	}
	if _, _, err = h.run(t, "", "data", "pull"); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("no terminal: %v", err)
	}
	h.app.interactive = true
	if _, _, err = h.run(t, "n\n", "data", "pull"); err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("declined: %v", err)
	}
	out, _, err = h.run(t, "y\n", "data", "pull")
	h.app.interactive = false
	text := readFile(t, settings)
	if err != nil || !strings.Contains(out, "Data registry: 1 entry added or updated") || !strings.Contains(text, "# Catalog") ||
		!strings.Contains(text, "path: inputs/raw/survey.csv") || !strings.Contains(text, "analysis:\n          path: inputs/final/analysis.dta\n          type: stata\n          locked: true") {
		t.Fatalf("apply: %v\n%s\n%s", err, out, text)
	}
	if !strings.Contains(readFile(t, lock), `"revision": "rev-1"`) {
		t.Error("revision not saved")
	}
	out, _, err = h.run(t, "", "data", "pull", "--yes", "--overwrite", "--json")
	var res dataPullJSON
	text = readFile(t, settings)
	if err != nil || json.Unmarshal([]byte(out), &res) != nil || !res.Applied || len(res.Entries.Changed) != 1 ||
		!strings.Contains(text, "path: inputs/raw/survey_v2.csv") || strings.Contains(text, "long_name: short") {
		t.Fatalf("overwrite: %v\n%s\n%s", err, out, text)
	}
	if out, _, err = h.run(t, "", "data", "pull"); err != nil || !strings.Contains(out, "Data registry is up to date with Framework Cloud (revision rev-1).") {
		t.Fatalf("current: %v\n%s", err, out)
	}
}

func TestDataPushStata(t *testing.T) {
	c := newDataCloud(t)
	h := newHarness(t, nil)
	dir := stataDataProject(t, h.app.home, c.url)
	h.app.cwd = dir
	c.entries = []catalog.Row{{Name: "inputs.old", Path: "old.csv"}}

	out, _, err := h.run(t, "", "data", "push", "--dry-run")
	if err != nil || !strings.Contains(out, "  Entries to add: inputs.raw.survey\n  On Cloud only (kept): inputs.old\n") ||
		!strings.Contains(out, "This changes Trial's data registry") || len(c.posts) != 1 || c.posts[0]["dry_run"] != true {
		t.Fatalf("dry run: %v\n%s", err, out)
	}
	sent := c.posts[0]["entries"].([]any)[0].(map[string]any)
	if sent["name"] != "inputs.raw.survey" || sent["stata_names"].(map[string]any)["long_name"] != "short" || sent["locked"] != false {
		t.Errorf("sent: %v", sent)
	}
	if _, _, err = h.run(t, "", "data", "push"); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("no terminal: %v", err)
	}
	// A stale lock: Pull first, then --force
	writeTestFile(t, filepath.Join(dir, ".framework", "data.lock.json"), `{"schema":1,"revision":"rev-0"}`)
	if _, _, err = h.run(t, "", "data", "push", "--yes"); err == nil || !strings.Contains(err.Error(), "Pull first: fw data pull") || !strings.Contains(err.Error(), "(revision rev-1)") {
		t.Fatalf("conflict: %v", err)
	}
	out, _, err = h.run(t, "", "data", "push", "--yes", "--force", "--remove")
	if err != nil || !strings.Contains(out, "Entries to remove: inputs.old") || !strings.Contains(out, "Cloud's newer data registry changes will be replaced") ||
		!strings.Contains(out, "Data registry pushed: revision rev-2 (1 added, 1 removed)") || !strings.Contains(readFile(t, filepath.Join(dir, ".framework", "data.lock.json")), `"revision": "rev-2"`) {
		t.Fatalf("force: %v\n%s", err, out)
	}
}

func TestDataStataUnavailable(t *testing.T) {
	c := newDataCloud(t)
	h := newHarness(t, nil)
	dir := stataDataProject(t, h.app.home, c.url)
	h.app.cwd = dir
	before := readFile(t, filepath.Join(dir, "settings.yml"))
	writeTestFile(t, filepath.Join(dir, ".env"), "FW_CLOUD_URL="+c.url+"\n")
	if _, _, err := h.run(t, "", "data", "pull", "--yes"); err == nil || !strings.Contains(err.Error(), "no FW_PROJECT_TOKEN") {
		t.Fatalf("no token: %v", err)
	}
	writeTestFile(t, filepath.Join(dir, ".env"), "FW_CLOUD_URL="+c.url+"\nFW_PROJECT_TOKEN=wrong\n")
	if _, _, err := h.run(t, "", "data", "push", "--yes"); err == nil || !strings.Contains(err.Error(), "refused the project token") {
		t.Fatalf("refused: %v", err)
	}
	writeTestFile(t, filepath.Join(dir, ".env"), "FW_CLOUD_URL="+c.url+"\nFW_PROJECT_TOKEN=proj-tok\n")
	c.missing = true
	if _, _, err := h.run(t, "", "data", "pull", "--yes"); err == nil || !strings.Contains(err.Error(), "no data registry endpoint yet") {
		t.Fatalf("404: %v", err)
	}
	writeTestFile(t, filepath.Join(dir, ".env"), "FW_CLOUD_URL=http://127.0.0.1:1\nFW_PROJECT_TOKEN=proj-tok\n")
	if _, _, err := h.run(t, "", "data", "pull", "--yes"); err == nil || !strings.Contains(err.Error(), "Framework Cloud not reached (cannot reach 127.0.0.1:1)") {
		t.Fatalf("offline: %v", err)
	}
	if readFile(t, filepath.Join(dir, "settings.yml")) != before {
		t.Error("a failed pull changed settings.yml")
	}
}

func TestDataRunsLanguagePackage(t *testing.T) {
	h := newHarness(t, nil)
	f := &fakeEngines{}
	f.install(h, "Rscript", "uv")
	r := filepath.Join(h.app.home, "r")
	writeTestFile(t, filepath.Join(r, "settings.yml"), "default:\n  project_type: project\n")
	h.app.cwd = r
	if _, _, err := h.run(t, "", "data", "pull", "--overwrite", "--yes"); err != nil || len(f.ran) != 1 ||
		f.ran[0].Args[1] != "invisible(framework::pull_data_registry(overwrite = TRUE, confirm = TRUE))" {
		t.Fatalf("R pull: %v %v", err, f.ran)
	}
	if _, _, err := h.run(t, "", "data", "push", "--dry-run", "--remove"); err != nil || len(f.ran) != 2 ||
		f.ran[1].Args[1] != "invisible(framework::push_data_registry(remove = TRUE, force = FALSE, dry_run = TRUE))" {
		t.Fatalf("R push: %v %v", err, f.ran)
	}
	if _, _, err := h.run(t, "", "data", "push"); err == nil || !strings.Contains(err.Error(), "--yes") || len(f.ran) != 2 {
		t.Fatalf("no terminal: %v %v", err, f.ran)
	}
	py := filepath.Join(h.app.home, "py")
	writeTestFile(t, filepath.Join(py, "pyproject.toml"), "[project]\n")
	writeTestFile(t, filepath.Join(py, "uv.lock"), "")
	writeTestFile(t, filepath.Join(py, ".framework", "spec.json"), `{"language":"python"}`)
	h.app.cwd = py
	if _, _, err := h.run(t, "", "data", "push", "--yes", "--force"); err != nil ||
		!strings.HasSuffix(f.ran[len(f.ran)-1].String(), "; framework.push_data_registry(remove=False, force=True, confirm=True)'") {
		t.Fatalf("Python push: %v %v", err, f.ran)
	}
}
