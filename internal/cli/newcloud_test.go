package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frameworktools/framework-cli/internal/project"
)

func TestNewFromCloudChoices(t *testing.T) {
	srv := fakeCloud(t)
	h := newHarness(t, map[string]string{"FW_CLOUD_TOKEN": "user-tok", "FW_CLOUD_URL": srv.URL})
	f := &fakeEngines{}
	f.install(h, "Rscript")

	// Without a terminal or --cloud, the project stays on this machine.
	lastChoices = ""
	_, errOut, err := h.run(t, "", "new", "Survey", "--blueprint", "framework/project", "--tool", "renv", "--no-tool", "quarto",
		"--package-set", "framework/tidyverse-analysis", "--skill-set", "framework/ggplot2", "--sensitive", "--ai=false")
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	var sent map[string]any
	if err := json.Unmarshal([]byte(lastChoices), &sent); err != nil {
		t.Fatal(err, lastChoices)
	}
	want := `{"blueprint":"framework/project","name":"Survey","package_sets":["framework/tidyverse-analysis"],"settings":{"ai":false,"sensitive":true},"skill_sets":["framework/ggplot2"],"tools":{"quarto":false,"renv":true}}`
	if got, _ := json.Marshal(sent); string(got) != want {
		t.Errorf("choices sent:\n%s\nwant\n%s", got, want)
	}
	if !strings.Contains(errOut, "Add --cloud") || len(f.layers) != 1 || !strings.Contains(f.layers[0], "framework/project") {
		t.Errorf("local create: %s %v", errOut, f.layers)
	}
	if project.EnvValue(filepath.Join(h.app.home, "survey"), "FW_PROJECT_TOKEN") != "" {
		t.Error("a local-only project got a project token")
	}

	// --defaults sends only the name, and --cloud creates and links it.
	_, errOut, err = h.run(t, "", "new", "Second", "--defaults", "--cloud")
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	if lastChoices != `{"name":"Second"}` || !strings.Contains(errOut, "Created survey on") {
		t.Errorf("--defaults --cloud: %s\n%s", lastChoices, errOut)
	}
	if dir := filepath.Join(h.app.home, "second"); project.EnvValue(dir, "FW_PROJECT_TOKEN") != "created-tok" {
		t.Error("the created project was not linked")
	}

	// At a terminal fw asks, defaulting to No.
	h.app.interactive = true
	lastChoices = ""
	_, errOut, err = h.run(t, "\n", "new", "Third", "--structure", "framework/project")
	if err != nil || !strings.Contains(errOut, "Also create this project on") || project.EnvValue(filepath.Join(h.app.home, "third"), "FW_PROJECT_TOKEN") != "" {
		t.Errorf("declined: %v\n%s", err, errOut)
	}
	if _, _, err = h.run(t, "y\n", "new", "Fourth", "--structure", "framework/project"); err != nil || project.EnvValue(filepath.Join(h.app.home, "fourth"), "FW_PROJECT_TOKEN") != "created-tok" {
		t.Errorf("accepted: %v", err)
	}
	if _, errOut, err = h.run(t, "", "new", "Fifth", "--structure", "framework/project", "--no-cloud"); err != nil || strings.Contains(errOut, "Also create") {
		t.Errorf("--no-cloud asked: %v\n%s", err, errOut)
	}
	h.app.interactive = false

	// The server's field errors come through.
	if _, _, err := h.run(t, "", "new", "Sixth", "--lang", "stata", "--structure", "framework/project", "--tool", "quarto"); err == nil || !strings.Contains(err.Error(), "Stata projects have no tools") {
		t.Errorf("422: %v", err)
	}
	if _, err := os.Stat(filepath.Join(h.app.home, "sixth")); !os.IsNotExist(err) {
		t.Error("created a folder after the cloud refused the choices")
	}

	for name, args := range map[string][]string{
		"with --from":   {"new", "--from", "vaccine-study", "--tool", "renv"},
		"with --type":   {"new", "X", "--blueprint", "framework/project", "--type", "bare"},
		"print --cloud": {"new", "X", "--defaults", "--cloud", "--print"},
		"both clouds":   {"new", "X", "--defaults", "--cloud", "--no-cloud"},
	} {
		if _, _, err := h.run(t, "", args...); exitCode(err) != exitUsage {
			t.Errorf("%s: exit %d %v", name, exitCode(err), err)
		}
	}
}

func TestNewFromCloudChoicesNeedsLogin(t *testing.T) {
	h := newHarness(t, nil)
	_, _, err := h.run(t, "", "new", "Survey", "--blueprint", "framework/project")
	if exitCode(err) != exitAuth || !strings.Contains(err.Error(), "need a login") {
		t.Errorf("logged out: exit %d %v", exitCode(err), err)
	}
}
