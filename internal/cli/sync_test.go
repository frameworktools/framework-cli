package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frameworktools/framework-cli/internal/catalog"
	"github.com/frameworktools/framework-cli/internal/engine"
)

// A Stata project on one fake Cloud that serves its setup, skills, and data
// registry.
func stataSyncProject(t *testing.T, env map[string]string) (*harness, *skillsCloud, *dataCloud, string) {
	t.Helper()
	c := newSkillsCloud(t)
	d := newDataCloud(t)
	d.revision = "data-1"
	c.data = d.handler
	h := newHarness(t, env)
	dir := stataDataProject(t, h.app.home, c.server.URL)
	h.app.cwd = dir
	return h, c, d, dir
}

func TestPullStata(t *testing.T) {
	h, c, d, dir := stataSyncProject(t, map[string]string{"FW_CLOUD_TOKEN": "user-tok"})
	d.entries = []catalog.Row{
		{Name: "inputs.final.analysis", Path: "inputs/final/analysis.dta", Type: strp("stata")},
		{Name: "inputs.raw.survey", Path: "inputs/raw/survey.csv", StataNames: map[string]string{"long_name": "short"}},
	}
	analysis := filepath.Join(dir, ".claude", "skills", "analysis", "SKILL.md")
	settings := filepath.Join(dir, "settings.yml")
	setupLockPath := filepath.Join(dir, ".framework", "setup.lock.json")
	dataLockPath := filepath.Join(dir, ".framework", "data.lock.json")

	// A dry run lists every part's changes and writes nothing
	out, _, err := h.run(t, "", "pull", "--dry-run")
	want := "Changes from Framework Cloud:\n" +
		"Packages: not managed for Stata projects\n" +
		"Skills:\n  2 to add\n" +
		"Data registry (revision data-1):\n  Entries to add: inputs.final.analysis\n"
	if err != nil || out != want {
		t.Fatalf("dry run: %v\n%s", err, out)
	}
	for _, p := range []string{analysis, setupLockPath, dataLockPath} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("a dry run wrote %s", p)
		}
	}

	// No terminal and no --yes; at a terminal, No changes nothing
	if _, _, err = h.run(t, "", "pull"); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("no terminal: %v", err)
	}
	h.app.interactive = true
	if _, _, err = h.run(t, "n\n", "pull"); err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("declined: %v", err)
	}
	if _, err := os.Stat(analysis); err == nil {
		t.Error("declining installed a skill")
	}
	// One question applies every part
	out, stderr, err := h.run(t, "y\n", "pull")
	h.app.interactive = false
	if err != nil || strings.Count(stderr, "[y/N]") != 1 || !strings.Contains(out, "✓ Skills: 2 added") ||
		!strings.Contains(out, "✓ Data registry: 1 entry added or updated") || !strings.Contains(readFile(t, analysis), "First") ||
		!strings.Contains(readFile(t, settings), "path: inputs/final/analysis.dta") {
		t.Fatalf("apply: %v\n%s\n%s", err, out, stderr)
	}
	var saved setupLock
	if err := json.Unmarshal([]byte(readFile(t, setupLockPath)), &saved); err != nil || saved.Revision != "rev-1" || saved.Server != c.server.URL {
		t.Fatalf("setup lock: %v %+v", err, saved)
	}
	if !strings.Contains(readFile(t, dataLockPath), `"revision": "data-1"`) {
		t.Error("the data registry revision was not saved")
	}

	// Nothing new
	if out, _, err = h.run(t, "", "pull"); err != nil || out != "Project is up to date with Framework Cloud.\n" {
		t.Fatalf("current: %v\n%s", err, out)
	}

	// Local edits are kept, unless --overwrite
	writeTestFile(t, analysis, "mine\n")
	c.publish(skillText("analysis", "Second"))
	c.layerVer = 2
	d.entries[1].Path = "inputs/raw/survey_v2.csv"
	out, _, err = h.run(t, "", "pull", "--yes")
	if err != nil || !strings.Contains(out, "Kept (locally edited): analysis — fw pull --overwrite replaces it") ||
		!strings.Contains(out, "inputs.raw.survey (path inputs/raw/survey.csv -> inputs/raw/survey_v2.csv) — kept; fw pull --overwrite uses Cloud's") ||
		readFile(t, analysis) != "mine\n" || !strings.Contains(readFile(t, settings), "path: inputs/raw/survey.csv\n") {
		t.Fatalf("kept: %v\n%s", err, out)
	}
	if !strings.Contains(readFile(t, setupLockPath), `"revision": "rev-2"`) {
		t.Error("the new revision was not saved")
	}
	out, _, err = h.run(t, "", "pull", "--yes", "--overwrite", "--json")
	var res syncPullJSON
	if err != nil || json.Unmarshal([]byte(out), &res) != nil || res.Skills.Counts["replaced"] != 1 || len(res.DataRegistry.Entries.Changed) != 1 ||
		strings.Join(res.Applied, ",") != "skills,data_registry" || res.Packages.Managed || !strings.Contains(readFile(t, analysis), "Second") ||
		!strings.Contains(readFile(t, settings), "survey_v2.csv") {
		t.Fatalf("overwrite: %v\n%s", err, out)
	}
}

// When one part can't be read, the others are still applied, and the error
// names which were and which weren't.
func TestPullStataPartial(t *testing.T) {
	h, c, _, dir := stataSyncProject(t, map[string]string{"FW_CLOUD_TOKEN": "user-tok"})
	c.data = nil
	host := strings.TrimPrefix(c.server.URL, "http://")
	reason := "This Framework Cloud server (" + host + ") has no data registry endpoint yet"
	out, _, err := h.run(t, "", "pull", "--dry-run")
	if err != nil || !strings.Contains(out, "Data registry: not read. "+reason+".\n") {
		t.Fatalf("dry run: %v\n%s", err, out)
	}
	_, _, err = h.run(t, "", "pull", "--yes")
	if err == nil || err.Error() != "fw pull did not finish. Applied: Skills. Already in step: Packages. Not applied: Data registry ("+reason+")." {
		t.Fatalf("partial: %v", err)
	}
	if !strings.Contains(readFile(t, filepath.Join(dir, ".claude", "skills", "analysis", "SKILL.md")), "First") {
		t.Error("skills were not applied")
	}
	if _, err := os.Stat(filepath.Join(dir, ".framework", "data.lock.json")); err == nil {
		t.Error("the data registry's revision was saved")
	}
}

func TestPullStataUnavailable(t *testing.T) {
	c := newSkillsCloud(t)
	h := newHarness(t, nil)
	dir := stataSkillsProject(t, h.app.home, c.server.URL, false)
	h.app.cwd = dir
	if _, _, err := h.run(t, "", "pull", "--dry-run"); err == nil || !strings.Contains(err.Error(), "no FW_PROJECT_TOKEN") {
		t.Fatalf("no token: %v", err)
	}
	writeTestFile(t, filepath.Join(dir, ".env"), "FW_CLOUD_URL="+c.server.URL+"\nFW_PROJECT_TOKEN=proj-tok\n")
	c.noSetup = true
	if _, _, err := h.run(t, "", "pull", "--yes"); err == nil || !strings.Contains(err.Error(), "has no setup endpoint yet") {
		t.Fatalf("404: %v", err)
	}
	c.noSetup = false
	// Without settings.yml there is no data catalog: only skills sync
	if out, _, err := h.run(t, "", "pull", "--dry-run"); err != nil || !strings.Contains(out, "Data registry: no data catalog (this project has no settings.yml)\n") {
		t.Fatalf("no catalog: %v\n%s", err, out)
	}
	writeTestFile(t, filepath.Join(dir, ".env"), "FW_CLOUD_URL=http://127.0.0.1:1\nFW_PROJECT_TOKEN=proj-tok\n")
	if _, _, err := h.run(t, "", "pull", "--yes"); err == nil || !strings.Contains(err.Error(), "Framework Cloud not reached (cannot reach 127.0.0.1:1)") {
		t.Fatalf("offline: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".claude", "skills")); err == nil {
		t.Error("a failed pull installed skills")
	}
}

func TestPushStata(t *testing.T) {
	h, c, d, dir := stataSyncProject(t, map[string]string{"FW_CLOUD_TOKEN": "user-tok"})
	if _, _, err := h.run(t, "", "pull", "--yes"); err != nil {
		t.Fatal(err)
	}
	d.entries = []catalog.Row{{Name: "inputs.old", Path: "old.csv"}}
	writeTestFile(t, filepath.Join(dir, ".claude", "skills", "analysis", "SKILL.md"), skillText("analysis", "Mine"))

	out, _, err := h.run(t, "", "push", "--dry-run")
	want := "Changes for Framework Cloud:\n" +
		"Packages: not managed for Stata projects\n" +
		"Skills:\n  Skills to push: analysis\n  This replaces the current version of analysis, used wherever it's attached.\n" +
		"Data registry:\n  Entries to add: inputs.raw.survey\n  On Cloud only (kept): inputs.old\n" +
		"  This changes Trial's data registry on Framework Cloud. New copies of the project, zips, and Update on GitHub will use it. Only definitions are sent: no data files and no hashes.\n"
	if err != nil || out != want || len(c.posts) != 0 {
		t.Fatalf("dry run: %v\n%s", err, out)
	}
	if _, _, err = h.run(t, "", "push"); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("no terminal: %v", err)
	}

	// The registry changed on Cloud: the skill is pushed, the registry isn't
	writeTestFile(t, filepath.Join(dir, ".framework", "data.lock.json"), `{"schema":1,"revision":"data-0"}`)
	pullFirst := "Framework Cloud's data registry changed since this project last pulled it (revision data-1). Pull first: fw pull. fw push --force replaces Cloud's changes"
	if out, _, err = h.run(t, "", "push", "--dry-run"); err != nil || !strings.Contains(out, "Data registry: can't push. "+pullFirst+".\n") {
		t.Fatalf("dry run conflict: %v\n%s", err, out)
	}
	out, _, err = h.run(t, "", "push", "--yes")
	if err == nil || err.Error() != "fw push did not finish. Pushed: Skills. Already in step: Packages. Not pushed: Data registry ("+pullFirst+")." ||
		!strings.Contains(out, "✓ Skills pushed: analysis") || len(c.posts) != 1 {
		t.Fatalf("partial: %v\n%s", err, out)
	}
	out, _, err = h.run(t, "", "push", "--yes", "--force", "--remove", "--json")
	var res syncPushJSON
	if err != nil || json.Unmarshal([]byte(out), &res) != nil || strings.Join(res.Applied, ",") != "data_registry" ||
		!res.DataRegistry.Applied || res.DataRegistry.Revision != "rev-2" || !strings.Contains(readFile(t, filepath.Join(dir, ".framework", "data.lock.json")), `"revision": "rev-2"`) {
		t.Fatalf("force: %v\n%s", err, out)
	}

	// A skill changed on Cloud: Pull first, unless --force
	writeTestFile(t, filepath.Join(dir, ".claude", "skills", "analysis", "SKILL.md"), skillText("analysis", "Mine again"))
	c.publish(skillText("analysis", "Theirs"))
	out, _, err = h.run(t, "", "push", "--yes")
	if err == nil || !strings.Contains(out, "  Pull first: analysis changed on Cloud since this project pulled it. fw push --force replaces it.") ||
		!strings.Contains(err.Error(), "Not pushed: Skills (Not pushed: analysis changed on Cloud since this project pulled it; fw push --force replaces it).") {
		t.Fatalf("skill conflict: %v\n%s", err, out)
	}
	if out, _, err = h.run(t, "", "push", "--yes", "--force"); err != nil || !strings.Contains(out, "✓ Skills pushed: analysis") {
		t.Fatalf("skill force: %v\n%s", err, out)
	}
	d.entries = []catalog.Row{{Name: "inputs.raw.survey", Path: "inputs/raw/survey.csv"}}
	if out, _, err = h.run(t, "", "push", "--yes"); err != nil || out != "Framework Cloud already matches this project.\n" {
		t.Fatalf("current: %v\n%s", err, out)
	}
}

// Edited skills need a login; the data registry goes with the project token.
func TestPushStataNotSignedIn(t *testing.T) {
	h, _, d, dir := stataSyncProject(t, map[string]string{"FW_CLOUD_TOKEN": "user-tok"})
	if _, _, err := h.run(t, "", "pull", "--yes"); err != nil {
		t.Fatal(err)
	}
	h.app.getenv = func(string) string { return "" }
	writeTestFile(t, filepath.Join(dir, ".claude", "skills", "analysis", "SKILL.md"), skillText("analysis", "Mine"))
	out, _, err := h.run(t, "", "push", "--yes")
	if err == nil || !strings.Contains(out, "Skills: can't push. Not signed in, so edited skills (analysis) can't be pushed. Run: fw login") ||
		!strings.Contains(err.Error(), "Pushed: Data registry. Already in step: Packages. Not pushed: Skills (Not signed in") || len(d.posts) != 2 {
		t.Fatalf("not signed in: %v\n%s", err, out)
	}
}

func TestPullPushRunLanguagePackage(t *testing.T) {
	h := newHarness(t, nil)
	f := &fakeEngines{}
	f.install(h, "Rscript", "uv")
	r := filepath.Join(h.app.home, "r")
	writeTestFile(t, filepath.Join(r, "settings.yml"), "default:\n  project_type: project\n")
	h.app.cwd = r

	if _, _, err := h.run(t, "", "pull", "--overwrite", "--yes"); err != nil || len(f.ran) != 1 ||
		f.ran[0].Args[1] != "invisible(framework::pull_all(overwrite = TRUE, confirm = TRUE))" || f.ran[0].Dir != r || f.ran[0].Rewrite == nil {
		t.Fatalf("R pull: %v %v", err, f.ran)
	}
	if _, _, err := h.run(t, "", "push", "--dry-run", "--remove"); err != nil || len(f.ran) != 2 ||
		f.ran[1].Args[1] != "invisible(framework::push_all(remove = TRUE, force = FALSE, dry_run = TRUE))" || f.ran[1].Rewrite == nil {
		t.Fatalf("R push: %v %v", err, f.ran)
	}
	// No terminal and no --yes: nothing runs
	if _, _, err := h.run(t, "", "push"); err == nil || !strings.Contains(err.Error(), "--yes") || len(f.ran) != 2 {
		t.Fatalf("no terminal: %v %v", err, f.ran)
	}
	// At a terminal: a dry run, the question, then confirm = TRUE
	h.app.interactive = true
	if _, _, err := h.run(t, "y\n", "push", "--force"); err != nil || len(f.ran) != 4 ||
		f.ran[2].Args[1] != "invisible(framework::push_all(remove = FALSE, force = TRUE, dry_run = TRUE))" ||
		f.ran[3].Args[1] != "invisible(framework::push_all(remove = FALSE, force = TRUE, confirm = TRUE))" {
		t.Fatalf("terminal: %v %v", err, f.ran)
	}
	if _, _, err := h.run(t, "n\n", "pull"); err == nil || !strings.Contains(err.Error(), "canceled") || len(f.ran) != 5 {
		t.Fatalf("declined: %v %v", err, f.ran)
	}
	h.app.interactive = false
	if _, _, err := h.run(t, "", "pull", "--json"); err == nil || !strings.Contains(err.Error(), "--json is for Stata projects") {
		t.Errorf("R json: %v", err)
	}

	py := filepath.Join(h.app.home, "py")
	writeTestFile(t, filepath.Join(py, "pyproject.toml"), "[project]\n")
	writeTestFile(t, filepath.Join(py, "uv.lock"), "")
	writeTestFile(t, filepath.Join(py, ".framework", "spec.json"), `{"language":"python"}`)
	h.app.cwd = py
	if _, _, err := h.run(t, "", "pull", "--dry-run"); err != nil ||
		!strings.HasSuffix(f.ran[len(f.ran)-1].String(), "; framework.pull_all(overwrite=False, dry_run=True)'") {
		t.Fatalf("Python pull: %v %v", err, f.ran)
	}
	if _, _, err := h.run(t, "", "push", "--yes", "--remove", "--force"); err != nil ||
		!strings.HasSuffix(f.ran[len(f.ran)-1].String(), "; framework.push_all(remove=True, force=True, confirm=True)'") {
		t.Fatalf("Python push: %v %v", err, f.ran)
	}
}

func TestPullMissingPackageFunction(t *testing.T) {
	h := newHarness(t, nil)
	f := &fakeEngines{}
	f.install(h, "Rscript")
	h.app.engines.Probe = func(engine.Command) bool { return false }
	r := filepath.Join(h.app.home, "r")
	writeTestFile(t, filepath.Join(r, "settings.yml"), "default:\n  project_type: project\n")
	h.app.cwd = r
	if _, _, err := h.run(t, "", "pull", "--dry-run"); err == nil || !strings.Contains(err.Error(), "no pull_all() yet") ||
		!strings.Contains(err.Error(), "fw pull again") || len(f.ran) != 0 {
		t.Fatalf("missing: %v %v", err, f.ran)
	}
	if _, _, err := h.run(t, "", "push", "--dry-run"); err == nil || !strings.Contains(err.Error(), "no push_all() yet") ||
		!strings.Contains(err.Error(), "fw push again") {
		t.Fatalf("missing push: %v", err)
	}
}

// fw setup is gone: fw pull and fw push replace it.
func TestSetupCommandRemoved(t *testing.T) {
	h := newHarness(t, nil)
	if _, _, err := h.run(t, "", "setup", "pull"); err == nil {
		t.Fatal("fw setup pull still runs")
	}
}

func TestLineRewriter(t *testing.T) {
	var got strings.Builder
	l := &lineRewriter{w: &got, r: strings.NewReplacer("push_all(force = TRUE)", "fw push --force")}
	// A phrase split across writes is still replaced; the last line has no newline
	for _, p := range []string{"Error: Pull first. push_all(for", "ce = TRUE) replaces it.\nnext ", "push_all(force = TRUE)"} {
		if n, err := l.Write([]byte(p)); err != nil || n != len(p) {
			t.Fatalf("write: %d %v", n, err)
		}
	}
	if got.String() != "Error: Pull first. fw push --force replaces it.\n" {
		t.Fatalf("before flush: %q", got.String())
	}
	l.Flush()
	if got.String() != "Error: Pull first. fw push --force replaces it.\nnext fw push --force" {
		t.Errorf("after flush: %q", got.String())
	}
}
