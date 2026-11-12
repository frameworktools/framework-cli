package cli

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/frameworktools/framework-cli/internal/skillsync"
	"github.com/frameworktools/framework-cli/internal/stata"
)

// skillsCloud fakes the endpoints fw skills pull and push use: the project
// layers by project token ("proj-tok"), and skill sync by user token
// ("user-tok"). The account skill "analysis" has the ID acct-1.
type skillsCloud struct {
	mu       sync.Mutex
	server   *httptest.Server
	versions []skillsync.Snapshot // acct-1, oldest first
	layerVer int                  // the version the project's layers name
	posts    []map[string]any
	own      *skillsync.Snapshot // the copy a built-in push made
}

func skillText(name, body string) string {
	return "---\nname: " + name + "\ndescription: " + body + " skill\n---\n" + body + "\n"
}

func snapshot(id string, version int, text string) skillsync.Snapshot {
	bundle := skillsync.BundleFromText(text, nil)
	h, _ := skillsync.Hash(bundle)
	return skillsync.Snapshot{ID: id, Version: version, Hash: h, Bundle: bundle, Variant: "stata"}
}

func (c *skillsCloud) publish(text string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.versions = append(c.versions, snapshot("acct-1", len(c.versions)+1, text))
}

func newSkillsCloud(t *testing.T) *skillsCloud {
	t.Helper()
	c := &skillsCloud{}
	c.publish(skillText("analysis", "First"))
	c.layerVer = 1
	auth := func(r *http.Request) string { return strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ") }
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/projects/pull", func(w http.ResponseWriter, r *http.Request) {
		if auth(r) != "proj-tok" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"message":"This endpoint requires a project token."}`))
			return
		}
		c.mu.Lock()
		v := c.versions[c.layerVer-1]
		c.mu.Unlock()
		raw, _ := base64.StdEncoding.DecodeString(v.Bundle[0].Content)
		entry := map[string]any{"content": string(raw), "bundle": v.Bundle,
			"sync": map[string]any{"skill_id": v.ID, "version": v.Version, "hash": v.Hash, "variant": "stata", "server": c.server.URL}}
		env := map[string]any{"spec_version": 1, "name": "Trial", "layers": []any{
			map[string]any{"kind": "base", "key": "project", "layer": map[string]any{"type": "project", "language": "stata",
				"skills": map[string]any{"analysis": entry}}},
			map[string]any{"kind": "project", "key": "trial", "layer": map[string]any{"language": "stata"}},
		}}
		_ = json.NewEncoder(w).Encode(env)
	})
	mux.HandleFunc("GET /api/v1/skills/sync/{id}", func(w http.ResponseWriter, r *http.Request) {
		if auth(r) != "user-tok" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"message":"A user token is required."}`))
			return
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		id := r.PathValue("id")
		if strings.HasPrefix(id, "framework:") {
			// A built-in skill: the shipped copy, found by its hash
			s := snapshot(id, 1, "")
			s.Bundle = stata.ShippedSkill(strings.TrimPrefix(id, "framework:"))
			s.Hash, _ = skillsync.Hash(s.Bundle)
			s.System = true
			if h := r.URL.Query().Get("hash"); h != "" && h != s.Hash {
				w.WriteHeader(http.StatusConflict)
				return
			}
			_ = json.NewEncoder(w).Encode(s)
			return
		}
		if c.own != nil && id == c.own.ID {
			_ = json.NewEncoder(w).Encode(c.own)
			return
		}
		_ = json.NewEncoder(w).Encode(c.versions[len(c.versions)-1])
	})
	mux.HandleFunc("POST /api/v1/skills/sync", func(w http.ResponseWriter, r *http.Request) {
		if auth(r) != "user-tok" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		c.mu.Lock()
		defer c.mu.Unlock()
		c.posts = append(c.posts, body)
		var bundle []skillsync.File
		data, _ := json.Marshal(body["bundle"])
		_ = json.Unmarshal(data, &bundle)
		h, _ := skillsync.Hash(bundle)
		if id, _ := body["skill_id"].(string); strings.HasPrefix(id, "framework:") {
			c.own = &skillsync.Snapshot{ID: "own-1", Version: 1, Hash: h, Bundle: bundle, Variant: "stata"}
			_ = json.NewEncoder(w).Encode(c.own)
			return
		}
		head := c.versions[len(c.versions)-1]
		if int(body["expected_version"].(float64)) != head.Version {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"message":"The skill changed. Reconcile before syncing."}`))
			return
		}
		next := skillsync.Snapshot{ID: "acct-1", Version: head.Version + 1, Hash: h, Bundle: bundle, Variant: "stata"}
		c.versions = append(c.versions, next)
		_ = json.NewEncoder(w).Encode(next)
	})
	c.server = httptest.NewServer(mux)
	t.Cleanup(c.server.Close)
	return c
}

func stataSkillsProject(t *testing.T, home, server string, token bool) string {
	t.Helper()
	dir := filepath.Join(home, "trial")
	writeTestFile(t, filepath.Join(dir, ".framework", "spec.json"), `{"spec_version":1,"language":"stata","type":"project","ai":{"assistants":{"claude":true}}}`)
	writeTestFile(t, filepath.Join(dir, "settings.do"), "global root `\"`c(pwd)'\"'\n")
	writeTestFile(t, filepath.Join(dir, "master.do"), "do \"settings.do\"\n")
	writeTestFile(t, filepath.Join(dir, "AGENTS.md"), "# Trial\n\n## Skills\n\nold table\n\n## Project Notes\n\nMine.\n")
	env := "FW_CLOUD_URL=" + server + "\n"
	if token {
		env += "FW_PROJECT_TOKEN=proj-tok\n"
	}
	writeTestFile(t, filepath.Join(dir, ".env"), env)
	return dir
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestSkillsPullStata(t *testing.T) {
	c := newSkillsCloud(t)
	h := newHarness(t, map[string]string{"FW_CLOUD_TOKEN": "user-tok"})
	dir := stataSkillsProject(t, h.app.home, c.server.URL, true)
	h.app.cwd = dir
	analysis := filepath.Join(dir, ".claude", "skills", "analysis", "SKILL.md")
	builtin := filepath.Join(dir, ".claude", "skills", "framework-stata", "SKILL.md")

	// New skills: the shipped one and the Cloud one
	out, _, err := h.run(t, "", "skills", "pull")
	if err != nil || !strings.Contains(out, "Skills: 2 added") || !strings.Contains(readFile(t, analysis), "First") {
		t.Fatalf("first pull: %v\n%s", err, out)
	}
	if !strings.Contains(readFile(t, builtin), "name: framework-stata") {
		t.Error("framework-stata not installed")
	}
	agents := readFile(t, filepath.Join(dir, "AGENTS.md"))
	if !strings.Contains(agents, "| `analysis` | First skill | `.claude/skills/analysis/SKILL.md` |") || strings.Contains(agents, "old table") || !strings.Contains(agents, "## Project Notes\n\nMine.") {
		t.Errorf("AGENTS.md:\n%s", agents)
	}
	lock := readFile(t, filepath.Join(dir, ".framework", "skills.lock.json"))
	if !strings.Contains(lock, `"skill_id": "acct-1"`) || !strings.Contains(lock, `"skill_id": "framework:framework-stata"`) {
		t.Errorf("lock:\n%s", lock)
	}

	// A newer version on Cloud, which the layers don't name yet: a login reads it
	c.publish(skillText("analysis", "Second"))
	out, _, err = h.run(t, "", "skills", "pull")
	if err != nil || !strings.Contains(out, "Skills: 1 updated, 1 up to date") || !strings.Contains(readFile(t, analysis), "Second") {
		t.Fatalf("update: %v\n%s", err, out)
	}

	// An edited skill is kept; a skill no longer in the setup is listed, not deleted
	writeTestFile(t, builtin, "mine\n")
	writeTestFile(t, filepath.Join(dir, ".claude", "skills", "old-skill", "SKILL.md"), skillText("old-skill", "Old"))
	c.publish(skillText("analysis", "Third"))
	out, _, err = h.run(t, "", "skills", "pull")
	want := "Skills: 1 updated, 1 locally edited (kept), 1 no longer in the project's setup"
	if err != nil || !strings.Contains(out, want) || !strings.Contains(out, "Kept (locally edited): framework-stata — fw skills pull --overwrite replaces it") ||
		!strings.Contains(out, "No longer in the project's setup (kept): old-skill") {
		t.Fatalf("edited: %v\n%s", err, out)
	}
	if readFile(t, builtin) != "mine\n" || readFile(t, filepath.Join(dir, ".claude", "skills", "old-skill", "SKILL.md")) == "" {
		t.Error("an edited or removed skill changed")
	}
	if !strings.Contains(readFile(t, filepath.Join(dir, "AGENTS.md")), "| `old-skill` | Old skill |") {
		t.Error("the table lost a skill still in the folder")
	}

	// A dry run writes nothing
	out, _, err = h.run(t, "", "skills", "pull", "--overwrite", "--dry-run", "--json")
	var res pullJSON
	if err != nil || json.Unmarshal([]byte(out), &res) != nil || !res.DryRun || res.Counts["replaced"] != 1 || res.Source != "cloud" ||
		!strings.HasPrefix(res.Message, "Skills (dry run, nothing written): ") || readFile(t, builtin) != "mine\n" {
		t.Fatalf("dry run: %v\n%s", err, out)
	}

	// --overwrite replaces the edited skill after a backup
	out, _, err = h.run(t, "", "skills", "pull", "--overwrite")
	if err != nil || !strings.Contains(out, "1 replaced (backed up)") || !strings.Contains(readFile(t, builtin), "name: framework-stata") {
		t.Fatalf("overwrite: %v\n%s", err, out)
	}
	backups, _ := filepath.Glob(filepath.Join(dir, ".framework", "skills-backups", "*", "SKILL.md"))
	found := false
	for _, b := range backups {
		found = found || readFile(t, b) == "mine\n"
	}
	if !found {
		t.Errorf("no backup of the edited skill in %v", backups)
	}
}

func TestSkillsPullOffline(t *testing.T) {
	c := newSkillsCloud(t)
	h := newHarness(t, nil)
	dir := stataSkillsProject(t, h.app.home, c.server.URL, false)
	h.app.cwd = dir
	out, _, err := h.run(t, "", "skills", "pull")
	if err != nil || !strings.HasPrefix(out, "Framework Cloud not reached (this project has no FW_PROJECT_TOKEN in .env); using the package skills for this project type.\nSkills: 1 added") {
		t.Fatalf("no token: %v\n%s", err, out)
	}

	// Cloud down
	writeTestFile(t, filepath.Join(dir, ".env"), "FW_CLOUD_URL=http://127.0.0.1:1\nFW_PROJECT_TOKEN=proj-tok\n")
	out, _, err = h.run(t, "", "skills", "pull", "--json")
	var res pullJSON
	if err != nil || json.Unmarshal([]byte(out), &res) != nil || res.Source != "package" || res.Counts["current"] != 1 ||
		!strings.Contains(res.Notes[0], "cannot reach 127.0.0.1:1") {
		t.Fatalf("offline: %v\n%s", err, out)
	}
}

func TestSkillsPullRunsLanguagePackage(t *testing.T) {
	h := newHarness(t, nil)
	f := &fakeEngines{}
	f.install(h, "Rscript", "uv")
	r := filepath.Join(h.app.home, "r")
	writeTestFile(t, filepath.Join(r, "settings.yml"), "default:\n  project_type: project\n")
	h.app.cwd = r
	if _, _, err := h.run(t, "", "skills", "pull", "--overwrite"); err != nil || len(f.ran) != 1 ||
		f.ran[0].Args[1] != "invisible(framework::pull_skills(overwrite = TRUE, dry_run = FALSE))" || f.ran[0].Dir != r {
		t.Fatalf("R: %v %v", err, f.ran)
	}
	if _, _, err := h.run(t, "", "skills", "pull", "--json"); err == nil || !strings.Contains(err.Error(), "--json is for Stata projects") {
		t.Errorf("R json: %v", err)
	}
	py := filepath.Join(h.app.home, "py")
	writeTestFile(t, filepath.Join(py, "pyproject.toml"), "[project]\n")
	writeTestFile(t, filepath.Join(py, "uv.lock"), "")
	writeTestFile(t, filepath.Join(py, ".framework", "spec.json"), `{"language":"python"}`)
	h.app.cwd = py
	if _, _, err := h.run(t, "", "skills", "pull", "--dry-run"); err != nil || len(f.ran) != 2 ||
		!strings.HasSuffix(f.ran[1].String(), "uv run python -c 'import framework; framework.pull_skills(overwrite=False, dry_run=True)'") {
		t.Fatalf("Python: %v %v", err, f.ran)
	}
}

func TestSkillsPush(t *testing.T) {
	c := newSkillsCloud(t)
	h := newHarness(t, map[string]string{"FW_CLOUD_TOKEN": "user-tok"})
	dir := stataSkillsProject(t, h.app.home, c.server.URL, true)
	h.app.cwd = dir
	if _, _, err := h.run(t, "", "skills", "pull"); err != nil {
		t.Fatal(err)
	}
	analysis := filepath.Join(dir, ".claude", "skills", "analysis", "SKILL.md")
	builtin := filepath.Join(dir, ".claude", "skills", "framework-stata", "SKILL.md")

	out, _, err := h.run(t, "", "skills", "push")
	if err != nil || !strings.Contains(out, "Skills pushed: nothing to push") {
		t.Fatalf("nothing: %v\n%s", err, out)
	}

	writeTestFile(t, analysis, skillText("analysis", "Mine"))
	writeTestFile(t, filepath.Join(dir, ".claude", "skills", "scratch", "SKILL.md"), skillText("scratch", "Scratch"))
	out, _, err = h.run(t, "", "skills", "push", "--dry-run")
	if err != nil || !strings.Contains(out, "This replaces the current version of analysis, used wherever it's attached.") ||
		!strings.Contains(out, "not tracked; use fw skills sync .claude/skills/scratch to add it") ||
		!strings.Contains(out, "Skills pushed (dry run, nothing sent): 1 to push, 1 not tracked") || len(c.posts) != 0 {
		t.Fatalf("dry run: %v\n%s", err, out)
	}

	// No terminal and no --yes
	if _, _, err = h.run(t, "", "skills", "push"); err == nil || !strings.Contains(err.Error(), "--yes") || len(c.posts) != 0 {
		t.Fatalf("no terminal: %v", err)
	}
	// At a terminal, No sends nothing
	h.app.interactive = true
	if _, _, err = h.run(t, "n\n", "skills", "push"); err == nil || len(c.posts) != 0 {
		t.Fatalf("declined: %v", err)
	}
	out, _, err = h.run(t, "y\n", "skills", "push")
	if err != nil || !strings.Contains(out, "Skills pushed: 1 pushed, 1 not tracked") || len(c.versions) != 2 {
		t.Fatalf("push: %v\n%s", err, out)
	}
	h.app.interactive = false

	// Cloud changed since the last pull: refuse, unless forced
	c.publish(skillText("analysis", "Theirs"))
	writeTestFile(t, analysis, skillText("analysis", "Mine again"))
	out, _, err = h.run(t, "", "skills", "push", "--yes")
	if err == nil || !strings.Contains(out, "Pull first: analysis changed on Framework Cloud") || !strings.Contains(out, "1 refused (pull first)") || len(c.versions) != 3 {
		t.Fatalf("conflict: %v\n%s", err, out)
	}
	out, _, err = h.run(t, "", "skills", "push", "--yes", "--force")
	if err != nil || !strings.Contains(out, "Cloud has a newer version of analysis than this project pulled.") || len(c.versions) != 4 ||
		!strings.Contains(string(mustDecode(t, c.versions[3].Bundle[0].Content)), "Mine again") {
		t.Fatalf("force: %v\n%s", err, out)
	}

	// An edited built-in skill becomes the person's own
	writeTestFile(t, builtin, skillText("framework-stata", "My Stata"))
	out, _, err = h.run(t, "", "skills", "push", "--yes", "--json")
	var res pushJSON
	if err != nil || json.Unmarshal([]byte(out), &res) != nil || res.Counts["pushed"] != 1 || c.own == nil {
		t.Fatalf("built-in: %v\n%s", err, out)
	}
	if last := c.posts[len(c.posts)-1]; last["skill_id"] != "framework:framework-stata" {
		t.Errorf("built-in push sent %v", last["skill_id"])
	}
}

func TestSkillsPushNeedsLogin(t *testing.T) {
	c := newSkillsCloud(t)
	h := newHarness(t, nil)
	h.app.cwd = stataSkillsProject(t, h.app.home, c.server.URL, true)
	if _, _, err := h.run(t, "", "skills", "push", "--url", c.server.URL); err == nil || !strings.Contains(err.Error(), "not logged in") {
		t.Fatalf("login: %v", err)
	}
}

func mustDecode(t *testing.T, s string) []byte {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
