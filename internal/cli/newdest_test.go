package cli

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/frameworktools/framework-cli/internal/engine"
	"github.com/frameworktools/framework-cli/internal/project"
)

// destCloud is a server that offers downloads and GitHub, as
// /api/v1/destinations describes them.
type destCloud struct {
	*httptest.Server
	created  []map[string]any
	polls    int
	statuses []string // scaffold statuses the project reports, in turn
}

func newDestCloud(t *testing.T, destinations string) *destCloud {
	t.Helper()
	c := &destCloud{statuses: []string{"pending", "running", "done"}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/destinations", func(w http.ResponseWriter, _ *http.Request) {
		if destinations == "" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(destinations))
	})
	mux.HandleFunc("GET /api/v1/blueprints", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"blueprints":[
			{"key":"framework/project","name":"Standard","description":"An analysis project","version":3,"languages":["r","python","stata"]},
			{"key":"framework/course","name":"Course","version":1,"languages":["r","python"]}]}`))
	})
	mux.HandleFunc("POST /api/v1/projects", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		c.created = append(c.created, body)
		language, _ := body["language"].(string)
		if language == "" {
			language = "r"
		}
		resp := map[string]any{"slug": "survey", "name": body["name"], "language": language, "url": c.URL + "/projects/survey", "token": "created-tok", "github": nil}
		if gh, ok := body["github"].(map[string]any); ok {
			resp["github"] = map[string]any{"repository": gh["owner"].(string) + "/survey", "scaffold_status": "pending"}
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(resp)
	})
	mux.HandleFunc("GET /api/v1/projects/survey/download", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write(testZip(t, map[string]string{
			"survey/":               "",
			"survey/inputs/raw/":    "",
			"survey/settings.yml":   "default:\n",
			"survey/.env":           "FW_CLOUD_URL=" + c.URL + "\n# FW_PROJECT_TOKEN=\n",
			"survey/.gitignore":     ".env\n",
			"survey/scripts/run.sh": "#!/bin/sh\n",
		}))
	})
	mux.HandleFunc("GET /api/v1/projects/survey", func(w http.ResponseWriter, _ *http.Request) {
		status := c.statuses[min(c.polls, len(c.statuses)-1)]
		c.polls++
		_ = json.NewEncoder(w).Encode(map[string]any{"slug": "survey", "name": "Survey", "github": map[string]any{
			"repository": "ada/survey", "url": "https://github.com/ada/survey", "private": true,
			"scaffold_status": status, "scaffold_error": "the workflow failed",
		}})
	})
	c.Server = httptest.NewServer(mux)
	t.Cleanup(c.Close)
	return c
}

func testZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		h := &zip.FileHeader{Name: name, Method: zip.Deflate}
		if strings.HasSuffix(name, ".sh") {
			h.SetMode(0o755)
		}
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.WriteString(w, content)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

const allDestinations = `{"download":{"available":true},"github":{"available":true,"login":"ada",
	"owners":[{"login":"ada","type":"User"},{"login":"lab","type":"Organization"}],
	"install_url":"https://github.com/apps/x/installations/new","connect_url":"https://x/account/github","unavailable":{}}}`

// gitClone fakes git: it records the command and makes the clone's folder.
func gitClone(h *harness) *[]engine.Command {
	var ran []engine.Command
	h.app.engines = engine.Finder{
		Getenv: func(string) string { return "" },
		LookPath: func(name string) (string, error) {
			if name == "git" {
				return "/bin/git", nil
			}
			return "", exec.ErrNotFound
		},
		Probe: func(engine.Command) bool { return true },
		Home:  h.app.home,
	}
	h.app.run = func(c engine.Command) (int, error) {
		ran = append(ran, c)
		_ = os.MkdirAll(c.Args[len(c.Args)-1], 0o750)
		return 0, nil
	}
	return &ran
}

func TestNewZipDownloadsTheBuiltProject(t *testing.T) {
	srv := newDestCloud(t, allDestinations)
	h := newHarness(t, map[string]string{"FW_CLOUD_TOKEN": "user-tok", "FW_CLOUD_URL": srv.URL})

	out, errOut, err := h.run(t, "", "new", "--dir", "survey", "Survey", "--lang", "python", "--blueprint", "framework/project", "--zip")
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	dir := filepath.Join(h.app.home, "survey")
	if got, _ := os.ReadFile(filepath.Join(dir, "settings.yml")); string(got) != "default:\n" {
		t.Errorf("settings.yml: %q", got)
	}
	if info, err := os.Stat(filepath.Join(dir, "inputs", "raw")); err != nil || !info.IsDir() {
		t.Error("empty folders were not created")
	}
	if info, err := os.Stat(filepath.Join(dir, "scripts", "run.sh")); err != nil || (runtime.GOOS != "windows" && info.Mode().Perm()&0o100 == 0) {
		t.Error("the executable bit was lost")
	}
	if project.EnvValue(dir, "FW_PROJECT_TOKEN") != "created-tok" || project.EnvValue(dir, "FW_CLOUD_URL") != srv.URL {
		t.Error("the downloaded project was not linked")
	}
	if env, _ := os.ReadFile(filepath.Join(dir, ".env")); string(env) != "FW_CLOUD_URL="+srv.URL+"\nFW_PROJECT_TOKEN=created-tok\n" {
		t.Errorf("the placeholder was not replaced in place: %q", env)
	}
	if !strings.Contains(out, "Project ready at "+filepath.Join(h.app.home, "survey")) || !strings.Contains(out, "Positron") {
		t.Errorf("output: %s", out)
	}
	sent := srv.created[0]
	if sent["language"] != "python" || sent["blueprint"] != "framework/project" || sent["github"] != nil {
		t.Errorf("sent: %v", sent)
	}

	// The folder must be empty, checked before anything is created
	if _, _, err := h.run(t, "", "new", "--dir", "survey", "Survey", "--zip"); err == nil || !strings.Contains(err.Error(), "not empty") || len(srv.created) != 1 {
		t.Errorf("into a full folder: %v, %d created", err, len(srv.created))
	}
}

func TestNewGitHubWaitsAndClones(t *testing.T) {
	srv := newDestCloud(t, allDestinations)
	h := newHarness(t, map[string]string{"FW_CLOUD_TOKEN": "user-tok", "FW_CLOUD_URL": srv.URL})
	ran := gitClone(h)

	out, errOut, err := h.run(t, "", "new", "--dir", "survey", "Survey", "--github", "lab/study-repo")
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	gh := srv.created[0]["github"].(map[string]any)
	if gh["owner"] != "lab" || gh["name"] != "study-repo" || gh["private"] != true {
		t.Errorf("github sent: %v", gh)
	}
	if srv.polls != 3 || !strings.Contains(errOut, "GitHub is building the project") {
		t.Errorf("polled %d times\n%s", srv.polls, errOut)
	}
	dir := filepath.Join(h.app.home, "survey")
	if len(*ran) != 1 || strings.Join((*ran)[0].Args, " ") != "clone https://github.com/ada/survey.git "+dir {
		t.Errorf("git: %v", *ran)
	}
	if project.EnvValue(dir, "FW_PROJECT_TOKEN") != "created-tok" {
		t.Error("the clone was not linked")
	}
	if !strings.Contains(out, "is ready on GitHub: https://github.com/ada/survey") {
		t.Errorf("output: %s", out)
	}

	// --public and --no-clone
	srv.polls = 0
	*ran = nil
	if out, _, err = h.run(t, "", "new", "Second", "--github", "ada", "--public", "--no-clone"); err != nil {
		t.Fatal(err)
	}
	if gh := srv.created[1]["github"].(map[string]any); gh["private"] != false || gh["name"] != nil {
		t.Errorf("github sent: %v", gh)
	}
	if len(*ran) != 0 || !strings.Contains(out, "git clone https://github.com/ada/survey.git") {
		t.Errorf("--no-clone cloned: %v\n%s", *ran, out)
	}
}

func TestNewGitHubReportsAFailedScaffold(t *testing.T) {
	srv := newDestCloud(t, allDestinations)
	srv.statuses = []string{"running", "failed"}
	h := newHarness(t, map[string]string{"FW_CLOUD_TOKEN": "user-tok", "FW_CLOUD_URL": srv.URL})
	ran := gitClone(h)

	_, _, err := h.run(t, "", "new", "--dir", "survey", "Survey", "--github", "ada")
	if err == nil || !strings.Contains(err.Error(), "the workflow failed") || !strings.Contains(err.Error(), "/projects/survey/github") {
		t.Errorf("err: %v", err)
	}
	if len(*ran) != 0 {
		t.Error("cloned a failed scaffold")
	}
}

func TestNewWalksThroughAtATerminal(t *testing.T) {
	srv := newDestCloud(t, allDestinations)
	h := newHarness(t, map[string]string{"FW_CLOUD_TOKEN": "user-tok", "FW_CLOUD_URL": srv.URL})
	h.app.interactive = true

	// Name, Stata, the Standard blueprint (Course doesn't support Stata), download
	_, errOut, err := h.run(t, "Survey\n3\n2\n2\n\n", "new")
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	for _, want := range []string{"Project name", "Which language?", "Which blueprint?", "Standard: An analysis project", "How do you want the project?", "Download it ready-made"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("missing %q in:\n%s", want, errOut)
		}
	}
	if strings.Contains(errOut, "Course") {
		t.Errorf("offered a blueprint without Stata:\n%s", errOut)
	}
	if sent := srv.created[0]; sent["name"] != "Survey" || sent["language"] != "stata" || sent["blueprint"] != "framework/project" {
		t.Errorf("sent: %v", sent)
	}
	if _, err := os.Stat(filepath.Join(h.app.home, "survey", "settings.yml")); err != nil {
		t.Error("not downloaded")
	}
}

func TestNewAsksForGitHubDetails(t *testing.T) {
	srv := newDestCloud(t, allDestinations)
	h := newHarness(t, map[string]string{"FW_CLOUD_TOKEN": "user-tok", "FW_CLOUD_URL": srv.URL})
	h.app.interactive = true
	gitClone(h)

	// A name given: only where it goes, then owner, repository, and private
	_, errOut, err := h.run(t, "3\n2\n\nn\n\n", "new", "Survey", "--lang", "r")
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	if strings.Contains(errOut, "Which language?") || strings.Contains(errOut, "Project name") {
		t.Errorf("asked for what the flags gave:\n%s", errOut)
	}
	gh := srv.created[0]["github"].(map[string]any)
	if gh["owner"] != "lab" || gh["name"] != "survey" || gh["private"] != false {
		t.Errorf("github sent: %v", gh)
	}
}

func TestNewHidesChoicesTheServerLacks(t *testing.T) {
	// Python can't go to GitHub on this server, and downloads are off
	srv := newDestCloud(t, `{"download":{"available":false},"github":{"available":true,"login":"ada","owners":[{"login":"ada","type":"User"}],"unavailable":{"python":"Not here."}}}`)
	h := newHarness(t, map[string]string{"FW_CLOUD_TOKEN": "user-tok", "FW_CLOUD_URL": srv.URL})
	h.app.interactive = true
	f := &fakeEngines{}
	f.install(h, "python3", "uv")

	_, errOut, err := h.run(t, "\n\n", "new", "Survey", "--lang", "python")
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	if strings.Contains(errOut, "How do you want the project?") || len(srv.created) != 0 {
		t.Errorf("asked with only one choice:\n%s", errOut)
	}
}

func TestNewDestinationFlagConflicts(t *testing.T) {
	h := newHarness(t, nil)
	for name, args := range map[string][]string{
		"zip with from":        {"new", "--from", "study", "--zip"},
		"github with headless": {"new", "X", "--github", "ada", "--headless"},
		"zip with offline":     {"new", "X", "--zip", "--offline"},
		"zip with type":        {"new", "X", "--zip", "--type", "bare"},
		"public alone":         {"new", "X", "--public"},
		"bad github":           {"new", "X", "--github", "a/b/c"},
		"zip and local":        {"new", "X", "--zip", "--local"},
	} {
		if _, _, err := h.run(t, "", args...); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	// Not logged in
	if _, _, err := h.run(t, "", "new", "X", "--zip"); err == nil || !strings.Contains(err.Error(), "needs a login") {
		t.Errorf("not logged in: %v", err)
	}
}

func TestUnzipProjectRefusesUnsafePaths(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"p/../../evil", "p/../x"} {
		data := testZip(t, map[string]string{name: "x"})
		if err := unzipProject(data, filepath.Join(dir, "out")); err == nil {
			t.Errorf("%s: extracted", name)
		}
	}
	if err := unzipProject([]byte("not a zip"), filepath.Join(dir, "out2")); err == nil {
		t.Error("extracted garbage")
	}
}

// A server without downloads: --zip stops before creating a project that
// would be left behind.
func TestNewZipChecksDownloadsFirst(t *testing.T) {
	for name, destinations := range map[string]string{
		"off":          `{"download":{"available":false},"github":{"available":false}}`,
		"older server": "",
	} {
		srv := newDestCloud(t, destinations)
		h := newHarness(t, map[string]string{"FW_CLOUD_TOKEN": "user-tok", "FW_CLOUD_URL": srv.URL})
		_, _, err := h.run(t, "", "new", "--dir", "survey", "Survey", "--zip")
		if err == nil || !strings.Contains(err.Error(), "fw created nothing") || len(srv.created) != 0 {
			t.Errorf("%s: %v; created %d", name, err, len(srv.created))
		}
	}
}
