package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frameworktools/framework-cli/internal/project"
)

// newStataArgs creates a bare Stata project, which fw scaffolds itself.
var newStataArgs = []string{"new", "My Study", "--lang", "stata", "--type", "bare", "--offline"}

func TestNewAsksWhereTheProjectGoes(t *testing.T) {
	cases := map[string]struct {
		stdin string
		want  func(home string) string
	}{
		"enter":             {"\n", func(h string) string { return filepath.Join(h, "my-study") }},
		"yes":               {"YES\n", func(h string) string { return filepath.Join(h, "my-study") }},
		"other answer":      {"maybe\ny\n", func(h string) string { return filepath.Join(h, "my-study") }},
		"no, blank":         {"n\n\n", func(h string) string { return filepath.Join(h, "my-study") }},
		"no, parent":        {"no\nwork/papers\n", func(h string) string { return filepath.Join(h, "work", "papers", "my-study") }},
		"no, home":          {"n\n~/elsewhere\n", func(h string) string { return filepath.Join(h, "elsewhere", "my-study") }},
		"no, tilde alone":   {"n\n~\n", func(h string) string { return filepath.Join(h, "my-study") }},
		"no, names project": {"n\nwork/my-study\n", func(h string) string { return filepath.Join(h, "work", "my-study") }},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, nil)
			h.app.interactive = true
			out, errOut, err := h.run(t, c.stdin, newStataArgs...)
			if err != nil {
				t.Fatalf("%v\n%s", err, errOut)
			}
			want := c.want(h.app.home)
			if _, err := os.Stat(filepath.Join(want, "settings.yml")); err != nil {
				t.Errorf("no project at %s: %v", want, err)
			}
			here := filepath.Join(h.app.home, "my-study")
			if !strings.Contains(errOut, "Create the project here ("+here+")? [y/n] ") {
				t.Errorf("question:\n%s", errOut)
			}
			if strings.HasPrefix(c.stdin, "n") && !strings.Contains(errOut, "Where should the project go? Leave blank to use the current directory ("+h.app.home+"): ") {
				t.Errorf("second question:\n%s", errOut)
			}
			if (name == "other answer") != strings.Contains(errOut, "Answer y or n.") {
				t.Errorf("re-ask:\n%s", errOut)
			}
			if !strings.Contains(errOut, "Creating the project at "+want+"\n") || !strings.Contains(out, "Project ready at "+want+"\n") {
				t.Errorf("progress:\n%s\n%s", out, errOut)
			}
		})
	}
}

func TestNewLocationWithoutATerminal(t *testing.T) {
	h := newHarness(t, nil)
	_, errOut, err := h.run(t, "", newStataArgs...)
	want := "--dir " + filepath.Join(h.app.home, "my-study")
	if exitCode(err) != exitUsage || !strings.Contains(err.Error(), "Pass --dir") || !strings.Contains(err.Error(), want) {
		t.Fatalf("got %v", err)
	}
	if strings.Contains(errOut, "Create the project here") {
		t.Error("asked without a terminal")
	}
	if _, err := os.Stat(filepath.Join(h.app.home, "my-study")); !os.IsNotExist(err) {
		t.Error("created a project without a location")
	}

	// --headless never asks, even at a terminal
	h.app.interactive = true
	if _, _, err := h.run(t, "\n", append(newStataArgs, "--headless")...); exitCode(err) != exitUsage || !strings.Contains(err.Error(), "Pass --dir") {
		t.Errorf("headless: %v", err)
	}
	// A closed stdin at the question stops instead of looping
	if _, _, err := h.run(t, "", newStataArgs...); exitCode(err) != exitUsage || !strings.Contains(err.Error(), "Pass --dir") {
		t.Errorf("no answer: %v", err)
	}
}

func TestNewExplicitDirSkipsTheQuestion(t *testing.T) {
	h := newHarness(t, nil)
	h.app.interactive = true
	out, errOut, err := h.run(t, "", append(newStataArgs, "--dir", "~/projects/study")...)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(h.app.home, "projects", "study")
	if strings.Contains(errOut, "Create the project here") {
		t.Errorf("asked with --dir:\n%s", errOut)
	}
	if !strings.Contains(out, "Project ready at "+dir) {
		t.Errorf("output:\n%s", out)
	}
}

func TestNewPullTokenAsksWhere(t *testing.T) {
	srv := fakeCloud(t)
	h := newHarness(t, map[string]string{"FW_CLOUD_URL": srv.URL})
	f := &fakeEngines{}
	f.install(h, "uv")
	h.app.interactive = true
	h.app.readSecret = func() (string, error) { return "proj-tok", nil }
	_, errOut, err := h.run(t, "n\nwork\n", "new", "--pull-token")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(h.app.home, "work", "vaccine-study")
	if !strings.Contains(errOut, "Create the project here ("+filepath.Join(h.app.home, "vaccine-study")+")? [y/n] ") {
		t.Errorf("question:\n%s", errOut)
	}
	if len(f.ran) != 1 || f.ran[0].Dir != dir && !strings.Contains(f.ran[0].String(), dir) {
		t.Errorf("ran %v, want %s", f.ran, dir)
	}
	if project.EnvValue(dir, "FW_PROJECT_TOKEN") != "proj-tok" {
		t.Error(".env not linked in the chosen folder")
	}

	// A token piped in has no terminal to ask at
	h.app.interactive = false
	if _, _, err := h.run(t, "proj-tok\n", "new", "--pull-token"); exitCode(err) != exitUsage || !strings.Contains(err.Error(), "Pass --dir") {
		t.Errorf("piped: %v", err)
	}
}

func TestNewPrintTakesTheDefaultWithoutAsking(t *testing.T) {
	h := newHarness(t, nil)
	h.app.interactive = true
	_, errOut, err := h.run(t, "", append(newStataArgs, "--print")...)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(errOut, "Create the project here") || !strings.Contains(errOut, filepath.Join(h.app.home, "my-study")) {
		t.Errorf("print:\n%s", errOut)
	}
}

// projectsDirCloud answers GET /api/v1/defaults with projectsDir (null when
// "") and GET /api/v1/projects/pull with a Python project, sending header as
// Framework-Projects-Dir when it isn't "".
func projectsDirCloud(t *testing.T, projectsDir, header string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/defaults", func(w http.ResponseWriter, _ *http.Request) {
		value := any(nil)
		if projectsDir != "" {
			value = projectsDir
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ide": "positron", "projects_dir": value})
	})
	mux.HandleFunc("GET /api/v1/projects/pull", func(w http.ResponseWriter, _ *http.Request) {
		if header != "" {
			w.Header().Set("Framework-Projects-Dir", header)
		}
		_, _ = w.Write([]byte(`{"spec_version":1,"name":"Vaccine Study","layers":[{"kind":"base","key":"project","layer":{"type":"project"}},{"kind":"project","key":"vaccine-study","layer":{"language":"python"}}]}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func writeSyncedSettings(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cloud-settings.yml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestNewOffersTheProjectsFolderFromCloudSettings(t *testing.T) {
	srv := projectsDirCloud(t, "~/research/new", "")
	h := newHarness(t, map[string]string{"FW_CLOUD_URL": srv.URL, "FW_CLOUD_TOKEN": "user-tok"})
	h.app.interactive = true
	// A newStataArgs project is --offline, which skips the server: drop it
	args := []string{"new", "My Study", "--lang", "stata", "--type", "bare"}
	out, errOut, err := h.run(t, "\n", args...)
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	want := filepath.Join(h.app.home, "research", "new", "my-study")
	if !strings.Contains(errOut, "Create the project in "+want+"? [y/n] ") || strings.Contains(errOut, "Create the project here") {
		t.Errorf("question:\n%s", errOut)
	}
	if _, err := os.Stat(filepath.Join(want, "settings.yml")); err != nil {
		t.Errorf("no project in the new Projects folder: %v", err)
	}
	if !strings.Contains(out, "Project ready at "+want+"\n") {
		t.Errorf("output:\n%s", out)
	}
}

func TestNewUnsetProjectsFolderAsksAboutHere(t *testing.T) {
	srv := projectsDirCloud(t, "", "")
	h := newHarness(t, map[string]string{"FW_CLOUD_URL": srv.URL, "FW_CLOUD_TOKEN": "user-tok"})
	h.app.interactive = true
	_, errOut, err := h.run(t, "\n", "new", "My Study", "--lang", "stata", "--type", "bare")
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	if !strings.Contains(errOut, "Create the project here ("+filepath.Join(h.app.home, "my-study")+")? [y/n] ") {
		t.Errorf("question:\n%s", errOut)
	}
}

func TestNewProjectsFolderFromTheSyncedCopy(t *testing.T) {
	h := newHarness(t, nil)
	h.app.interactive = true
	writeSyncedSettings(t, h.app.dirs.R, "defaults:\n  ide: positron\n  projects_dir: ~/from-r\n")
	_, errOut, err := h.run(t, "y\n", newStataArgs...)
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	want := filepath.Join(h.app.home, "from-r", "my-study")
	if !strings.Contains(errOut, "Create the project in "+want+"? [y/n] ") {
		t.Errorf("question:\n%s", errOut)
	}
	if _, err := os.Stat(filepath.Join(want, "settings.yml")); err != nil {
		t.Errorf("no project at %s: %v", want, err)
	}
}

func TestNewDecliningTheProjectsFolderAsksWhere(t *testing.T) {
	h := newHarness(t, nil)
	h.app.interactive = true
	writeSyncedSettings(t, h.app.dirs.FW, "defaults:\n  projects_dir: /srv/research\n")
	_, errOut, err := h.run(t, "n\n\n", newStataArgs...)
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	// On Windows, /srv/research is on the current drive.
	want := filepath.Join(filepath.VolumeName(h.app.cwd)+"/srv/research", "my-study")
	if !strings.Contains(errOut, "Create the project in "+want+"? [y/n] ") {
		t.Errorf("question:\n%s", errOut)
	}
	if !strings.Contains(errOut, "Where should the project go? Leave blank to use the current directory ("+h.app.home+"): ") {
		t.Errorf("second question:\n%s", errOut)
	}
	if _, err := os.Stat(filepath.Join(h.app.home, "my-study", "settings.yml")); err != nil {
		t.Errorf("no project here: %v", err)
	}
}

func TestNewWithoutATerminalIgnoresTheProjectsFolder(t *testing.T) {
	h := newHarness(t, nil)
	writeSyncedSettings(t, h.app.dirs.FW, "defaults:\n  projects_dir: /srv/research\n")
	_, _, err := h.run(t, "", newStataArgs...)
	if exitCode(err) != exitUsage || !strings.Contains(err.Error(), "--dir "+filepath.Join(h.app.home, "my-study")) {
		t.Fatalf("got %v", err)
	}
}

func TestNewPullTokenOffersTheOwnersProjectsFolder(t *testing.T) {
	owner := filepath.Join(t.TempDir(), "My Projects", "études")
	settings := filepath.Join(t.TempDir(), "settings")
	cases := map[string]struct {
		header   string
		settings string
		want     string
	}{
		"header, encoded": {url.PathEscape(owner), settings, owner},
		"no header":       {"", settings, settings},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			srv := projectsDirCloud(t, c.settings, c.header)
			h := newHarness(t, map[string]string{"FW_CLOUD_URL": srv.URL, "FW_CLOUD_TOKEN": "user-tok"})
			f := &fakeEngines{}
			f.install(h, "uv")
			h.app.interactive = true
			h.app.readSecret = func() (string, error) { return "proj-tok", nil }
			_, errOut, err := h.run(t, "\n", "new", "--pull-token")
			if err != nil {
				t.Fatalf("%v\n%s", err, errOut)
			}
			dir := filepath.Join(c.want, "vaccine-study")
			if !strings.Contains(errOut, "Create the project in "+dir+"? [y/n] ") {
				t.Errorf("question:\n%s", errOut)
			}
			if len(f.ran) != 1 || f.ran[0].Dir != dir && !strings.Contains(f.ran[0].String(), dir) {
				t.Errorf("ran %v, want %s", f.ran, dir)
			}
		})
	}
}
