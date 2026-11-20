package cli

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// The one message for a project with no project token, as in a fresh
// clone, whose .env git ignores.
func TestNotLinkedMessage(t *testing.T) {
	want := "This project isn't linked to Framework Cloud (no FW_PROJECT_TOKEN in the environment or .env), so nothing was changed. " +
		"Link it with: fw project link --from SLUG. fw projects list shows the slugs."
	if got := (errNotLinked{}).Error(); got != want {
		t.Errorf("got %q", got)
	}
	if got := notLinked(""); got != "This project isn't linked to Framework Cloud (no FW_PROJECT_TOKEN in the environment or .env). "+linkHint {
		t.Errorf("no effect: %q", got)
	}
}

// fw pull, fw push, and fw data pull and push in an R or Python project stop before running the
// package, whose own message has no link hint.
func TestPullPushNotLinked(t *testing.T) {
	h := newHarness(t, nil)
	f := &fakeEngines{}
	f.install(h, "Rscript", "uv")
	r := filepath.Join(h.app.home, "r")
	writeTestFile(t, filepath.Join(r, "settings.yml"), "default:\n  project_type: project\n")
	py := filepath.Join(h.app.home, "py")
	writeTestFile(t, filepath.Join(py, "pyproject.toml"), "[project]\n")
	writeTestFile(t, filepath.Join(py, "uv.lock"), "")
	writeTestFile(t, filepath.Join(py, ".framework", "spec.json"), `{"language":"python"}`)
	for _, dir := range []string{r, py} {
		h.app.cwd = dir
		for _, c := range [][]string{{"pull"}, {"push"}, {"data", "pull"}, {"data", "push"}} {
			if _, _, err := h.run(t, "", append(c, "--yes")...); err == nil || err.Error() != notLinked("nothing was changed") || len(f.ran) != 0 {
				t.Errorf("%s in %s: %v %v", c, filepath.Base(dir), err, f.ran)
			}
		}
	}
	// A token in the environment links it, as in CI
	h.app.getenv = func(k string) string { return map[string]string{"FW_PROJECT_TOKEN": "proj-tok"}[k] }
	h.app.cwd = r
	if _, _, err := h.run(t, "", "pull", "--yes"); err != nil || len(f.ran) != 1 {
		t.Errorf("token in the environment: %v %v", err, f.ran)
	}
}

// fw skills pull in an R project runs the package, which falls back to its
// own skills, then says how to link the project.
func TestSkillsPullRNotLinked(t *testing.T) {
	h := newHarness(t, nil)
	f := &fakeEngines{}
	f.install(h, "Rscript")
	r := filepath.Join(h.app.home, "r")
	writeTestFile(t, filepath.Join(r, "settings.yml"), "default:\n  project_type: project\n")
	h.app.cwd = r
	_, stderr, err := h.run(t, "", "skills", "pull")
	if err != nil || len(f.ran) != 1 || !strings.Contains(stderr, notLinked("")) {
		t.Fatalf("no token: %v %v\n%s", err, f.ran, stderr)
	}
	writeTestFile(t, filepath.Join(r, ".env"), "FW_PROJECT_TOKEN=proj-tok\n")
	if _, stderr, err = h.run(t, "", "skills", "pull"); err != nil || strings.Contains(stderr, linkHint) {
		t.Errorf("linked: %v\n%s", err, stderr)
	}
}

// fw project status and fw project bootstrap in a fresh clone: no .env.
func TestFreshCloneNotLinked(t *testing.T) {
	h := newHarness(t, nil)
	f := &fakeEngines{}
	f.install(h, "Rscript")
	proj := filepath.Join(h.app.home, "clone")
	writeTestFile(t, filepath.Join(proj, "settings.yml"), "x: 1\n")
	h.app.cwd = proj

	out, _, err := h.run(t, "", "project", "status")
	if err != nil || !strings.Contains(out, "Cloud") || !strings.Contains(out, notLinked("")) {
		t.Errorf("status: %v\n%s", err, out)
	}
	out, _, err = h.run(t, "", "project", "status", "--json")
	var res projectStatusJSON
	if err != nil || json.Unmarshal([]byte(out), &res) != nil || res.Cloud.Linked || res.Cloud.Problem != notLinked("") {
		t.Errorf("status --json: %v\n%s", err, out)
	}

	// Bootstrap runs, then says how to link the project
	out, _, err = h.run(t, "", "project", "bootstrap")
	if err != nil || len(f.ran) != 1 || !strings.Contains(out, "Note: "+notLinked("")) {
		t.Errorf("bootstrap: %v %v\n%s", err, f.ran, out)
	}
	if out, _, err = h.run(t, "", "project", "bootstrap", "--print"); err != nil || strings.Contains(out, linkHint) {
		t.Errorf("bootstrap --print: %v\n%s", err, out)
	}
	writeTestFile(t, filepath.Join(proj, ".env"), "FW_PROJECT_TOKEN=proj-tok\n")
	if out, _, err = h.run(t, "", "project", "bootstrap"); err != nil || strings.Contains(out, linkHint) {
		t.Errorf("linked bootstrap: %v\n%s", err, out)
	}

	// A Stata project needs no package bootstrap, and still gets the hint
	stataDir := filepath.Join(h.app.home, "stata")
	writeTestFile(t, filepath.Join(stataDir, "settings.do"), "")
	writeTestFile(t, filepath.Join(stataDir, ".framework", "spec.json"), `{"language":"stata"}`)
	h.app.cwd = stataDir
	if out, _, err = h.run(t, "", "project", "bootstrap"); err != nil || !strings.Contains(out, "Note: "+notLinked("")) {
		t.Errorf("Stata bootstrap: %v\n%s", err, out)
	}
}

// Publishing needs the project token; without one fw says how to link or
// create the project.
func TestPublishNotLinked(t *testing.T) {
	h := newHarness(t, nil)
	proj := filepath.Join(h.app.home, "clone")
	writeTestFile(t, filepath.Join(proj, "settings.yml"), "x: 1\n")
	h.app.cwd = proj
	_, _, err := h.app.projectClient("")
	if err == nil || err.Error() != notLinked("nothing was published")+createHint || exitCode(err) != exitAuth {
		t.Errorf("publish: %v (exit %d)", err, exitCode(err))
	}
}
