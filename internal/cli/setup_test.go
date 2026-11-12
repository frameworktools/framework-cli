package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frameworktools/framework-cli/internal/engine"
)

func TestSetupPullStata(t *testing.T) {
	c := newSkillsCloud(t)
	h := newHarness(t, map[string]string{"FW_CLOUD_TOKEN": "user-tok"})
	dir := stataSkillsProject(t, h.app.home, c.server.URL, true)
	h.app.cwd = dir
	analysis := filepath.Join(dir, ".claude", "skills", "analysis", "SKILL.md")
	lock := filepath.Join(dir, ".framework", "setup.lock.json")

	// A dry run lists the changes and writes nothing
	out, _, err := h.run(t, "", "setup", "pull", "--dry-run")
	if err != nil || !strings.Contains(out, "Packages aren't managed for Stata projects, so only skills change.\n") ||
		!strings.Contains(out, "Setup changes from Framework Cloud (revision rev-1):\n  Skills: 2 to add\n") {
		t.Fatalf("dry run: %v\n%s", err, out)
	}
	if _, err := os.Stat(analysis); err == nil {
		t.Error("a dry run installed a skill")
	}
	if _, err := os.Stat(lock); err == nil {
		t.Error("a dry run saved the revision")
	}

	// No terminal and no --yes
	if _, _, err = h.run(t, "", "setup", "pull"); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("no terminal: %v", err)
	}
	// At a terminal, No changes nothing
	h.app.interactive = true
	if _, _, err = h.run(t, "n\n", "setup", "pull"); err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("declined: %v", err)
	}
	if _, err := os.Stat(analysis); err == nil {
		t.Error("declining installed a skill")
	}
	out, _, err = h.run(t, "y\n", "setup", "pull")
	if err != nil || !strings.Contains(out, "Skills: 2 added") || !strings.Contains(out, "Setup pulled: revision rev-1") || !strings.Contains(readFile(t, analysis), "First") {
		t.Fatalf("apply: %v\n%s", err, out)
	}
	h.app.interactive = false
	var saved setupLock
	if err := json.Unmarshal([]byte(readFile(t, lock)), &saved); err != nil || saved.Schema != 1 || saved.Revision != "rev-1" || saved.Server != c.server.URL || saved.PulledAt == "" {
		t.Fatalf("lock: %v %+v", err, saved)
	}

	// Nothing new
	out, _, err = h.run(t, "", "setup", "pull")
	if err != nil || !strings.Contains(out, "Setup is up to date with Framework Cloud (revision rev-1).") {
		t.Fatalf("current: %v\n%s", err, out)
	}

	// An edited skill is kept, unless --overwrite
	writeTestFile(t, analysis, "mine\n")
	c.publish(skillText("analysis", "Second"))
	c.layerVer = 2
	out, _, err = h.run(t, "", "setup", "pull", "--yes")
	if err != nil || !strings.Contains(out, "Setup is up to date with Framework Cloud (revision rev-2).") ||
		!strings.Contains(out, "Kept (locally edited): analysis — fw setup pull --overwrite replaces it") || readFile(t, analysis) != "mine\n" {
		t.Fatalf("kept: %v\n%s", err, out)
	}
	if !strings.Contains(readFile(t, lock), `"revision": "rev-2"`) {
		t.Error("the new revision was not saved")
	}
	out, _, err = h.run(t, "", "setup", "pull", "--yes", "--overwrite", "--json")
	var res setupPullJSON
	if err != nil || json.Unmarshal([]byte(out), &res) != nil || !res.Applied || res.Skills.Counts["replaced"] != 1 || !strings.Contains(readFile(t, analysis), "Second") {
		t.Fatalf("overwrite: %v\n%s", err, out)
	}
}

func TestSetupPullStataUnavailable(t *testing.T) {
	c := newSkillsCloud(t)
	h := newHarness(t, nil)
	dir := stataSkillsProject(t, h.app.home, c.server.URL, false)
	h.app.cwd = dir
	if _, _, err := h.run(t, "", "setup", "pull", "--dry-run"); err == nil || !strings.Contains(err.Error(), "no FW_PROJECT_TOKEN") {
		t.Fatalf("no token: %v", err)
	}
	writeTestFile(t, filepath.Join(dir, ".env"), "FW_CLOUD_URL="+c.server.URL+"\nFW_PROJECT_TOKEN=proj-tok\n")
	c.noSetup = true
	if _, _, err := h.run(t, "", "setup", "pull", "--yes"); err == nil || !strings.Contains(err.Error(), "has no setup endpoint yet") {
		t.Fatalf("404: %v", err)
	}
	writeTestFile(t, filepath.Join(dir, ".env"), "FW_CLOUD_URL=http://127.0.0.1:1\nFW_PROJECT_TOKEN=proj-tok\n")
	if _, _, err := h.run(t, "", "setup", "pull", "--yes"); err == nil || !strings.Contains(err.Error(), "Framework Cloud not reached (cannot reach 127.0.0.1:1)") {
		t.Fatalf("offline: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".claude", "skills")); err == nil {
		t.Error("a failed setup pull installed skills")
	}
}

func TestSetupPushStata(t *testing.T) {
	c := newSkillsCloud(t)
	h := newHarness(t, map[string]string{"FW_CLOUD_TOKEN": "user-tok"})
	dir := stataSkillsProject(t, h.app.home, c.server.URL, true)
	h.app.cwd = dir
	if _, _, err := h.run(t, "", "setup", "pull", "--yes"); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(dir, ".claude", "skills", "analysis", "SKILL.md"), skillText("analysis", "Mine"))
	out, _, err := h.run(t, "", "setup", "push", "--dry-run", "--remove")
	if err != nil || !strings.HasPrefix(out, "Packages aren't managed for Stata projects, so only skills are pushed.\n") ||
		!strings.Contains(out, "1 to push") || len(c.posts) != 0 {
		t.Fatalf("dry run: %v\n%s", err, out)
	}
	if _, _, err = h.run(t, "", "setup", "push"); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("no terminal: %v", err)
	}
	c.publish(skillText("analysis", "Theirs"))
	if out, _, err = h.run(t, "", "setup", "push", "--yes"); err == nil || !strings.Contains(out, "Pull first") {
		t.Fatalf("conflict: %v\n%s", err, out)
	}
	if out, _, err = h.run(t, "", "setup", "push", "--yes", "--force"); err != nil || !strings.Contains(out, "Skills pushed: 1 pushed") {
		t.Fatalf("force: %v\n%s", err, out)
	}
}

func TestSetupRunsLanguagePackage(t *testing.T) {
	h := newHarness(t, nil)
	f := &fakeEngines{}
	f.install(h, "Rscript", "uv")
	r := filepath.Join(h.app.home, "r")
	writeTestFile(t, filepath.Join(r, "settings.yml"), "default:\n  project_type: project\n")
	h.app.cwd = r

	if _, _, err := h.run(t, "", "setup", "pull", "--overwrite", "--yes"); err != nil || len(f.ran) != 1 ||
		f.ran[0].Args[1] != "invisible(framework::pull_setup(overwrite = TRUE, confirm = TRUE))" || f.ran[0].Dir != r {
		t.Fatalf("R pull: %v %v", err, f.ran)
	}
	if _, _, err := h.run(t, "", "setup", "push", "--dry-run", "--remove"); err != nil || len(f.ran) != 2 ||
		f.ran[1].Args[1] != "invisible(framework::push_setup(remove = TRUE, force = FALSE, dry_run = TRUE))" {
		t.Fatalf("R push: %v %v", err, f.ran)
	}
	// No terminal and no --yes: nothing runs
	if _, _, err := h.run(t, "", "setup", "push"); err == nil || !strings.Contains(err.Error(), "--yes") || len(f.ran) != 2 {
		t.Fatalf("no terminal: %v %v", err, f.ran)
	}
	// At a terminal: a dry run, the question, then confirm = TRUE
	h.app.interactive = true
	if _, _, err := h.run(t, "y\n", "setup", "push", "--force"); err != nil || len(f.ran) != 4 ||
		f.ran[2].Args[1] != "invisible(framework::push_setup(remove = FALSE, force = TRUE, dry_run = TRUE))" ||
		f.ran[3].Args[1] != "invisible(framework::push_setup(remove = FALSE, force = TRUE, confirm = TRUE))" {
		t.Fatalf("terminal: %v %v", err, f.ran)
	}
	if _, _, err := h.run(t, "n\n", "setup", "pull"); err == nil || !strings.Contains(err.Error(), "canceled") || len(f.ran) != 5 {
		t.Fatalf("declined: %v %v", err, f.ran)
	}
	h.app.interactive = false
	if _, _, err := h.run(t, "", "setup", "pull", "--json"); err == nil || !strings.Contains(err.Error(), "--json is for Stata projects") {
		t.Errorf("R json: %v", err)
	}

	py := filepath.Join(h.app.home, "py")
	writeTestFile(t, filepath.Join(py, "pyproject.toml"), "[project]\n")
	writeTestFile(t, filepath.Join(py, "uv.lock"), "")
	writeTestFile(t, filepath.Join(py, ".framework", "spec.json"), `{"language":"python"}`)
	h.app.cwd = py
	if _, _, err := h.run(t, "", "setup", "pull", "--dry-run"); err != nil ||
		!strings.HasSuffix(f.ran[len(f.ran)-1].String(), "; framework.pull_setup(overwrite=False, dry_run=True)'") {
		t.Fatalf("Python pull: %v %v", err, f.ran)
	}
	if _, _, err := h.run(t, "", "setup", "push", "--yes", "--remove", "--force"); err != nil ||
		!strings.HasSuffix(f.ran[len(f.ran)-1].String(), "; framework.push_setup(remove=True, force=True, confirm=True)'") {
		t.Fatalf("Python push: %v %v", err, f.ran)
	}
}

func TestSetupMissingPackageFunction(t *testing.T) {
	h := newHarness(t, nil)
	f := &fakeEngines{}
	f.install(h, "Rscript")
	h.app.engines.Probe = func(engine.Command) bool { return false }
	r := filepath.Join(h.app.home, "r")
	writeTestFile(t, filepath.Join(r, "settings.yml"), "default:\n  project_type: project\n")
	h.app.cwd = r
	if _, _, err := h.run(t, "", "setup", "pull", "--dry-run"); err == nil || !strings.Contains(err.Error(), "no pull_setup() yet") ||
		!strings.Contains(err.Error(), "fw setup pull again") || len(f.ran) != 0 {
		t.Fatalf("missing: %v %v", err, f.ran)
	}
}
