package cli

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frameworktools/framework-cli/internal/engine"
	"github.com/frameworktools/framework-cli/internal/project"
)

// fakeEngines finds R and uv, and records what fw runs. The fake run creates
// the target folder, as the language package would.
type fakeEngines struct {
	ran      []engine.Command
	layers   []string // layers file contents seen at run time
	exitCode int
}

func (f *fakeEngines) install(h *harness, found ...string) {
	set := map[string]bool{}
	for _, name := range found {
		set[name] = true
	}
	h.app.engines = engine.Finder{
		Getenv: func(string) string { return "" },
		LookPath: func(name string) (string, error) {
			if set[name] {
				return "/bin/" + name, nil
			}
			return "", exec.ErrNotFound
		},
		Probe: func(engine.Command) bool { return true },
		Home:  h.app.home,
	}
	h.app.run = func(c engine.Command) (int, error) {
		f.ran = append(f.ran, c)
		for i, arg := range c.Args {
			if strings.HasSuffix(arg, ".json") {
				data, _ := os.ReadFile(arg)
				f.layers = append(f.layers, string(data))
				if i+1 < len(c.Args) && c.Args[i+1] != "--path" {
					_ = os.MkdirAll(c.Args[i+1], 0o700)
				}
			}
			if arg == "--path" {
				_ = os.MkdirAll(c.Args[i+1], 0o700)
			}
		}
		return f.exitCode, nil
	}
}

func TestNewFromRDefaults(t *testing.T) {
	h := newHarness(t, nil)
	f := &fakeEngines{}
	f.install(h, "Rscript")
	_, _, err := h.run(t, "", "new", "My Study", "--type", "project_sensitive")
	if err != nil {
		t.Fatal(err)
	}
	if len(f.ran) != 1 {
		t.Fatalf("ran %v", f.ran)
	}
	got := f.ran[0].Args
	want := []string{"My Study", filepath.Join(h.app.home, "my-study"), "project_sensitive"}
	if !strings.Contains(got[1], "framework::new(") || strings.Join(got[len(got)-3:], "|") != strings.Join(want, "|") {
		t.Errorf("args %q", got)
	}
	if f.ran[0].Dir != h.app.home {
		t.Errorf("ran in %s; want the home folder", f.ran[0].Dir)
	}
}

func TestNewPythonFromWizard(t *testing.T) {
	h := newHarness(t, nil)
	f := &fakeEngines{}
	f.install(h, "uv")
	if _, _, err := h.run(t, "", "new", "py study", "--lang", "python", "--type", "course"); err != nil {
		t.Fatal(err)
	}
	want := []string{"--name", "py study", "--path", filepath.Join(h.app.home, "py-study"), "--type", "course"}
	if len(f.ran) != 1 || len(f.ran[0].Args) < len(want) || strings.Join(f.ran[0].Args[len(f.ran[0].Args)-len(want):], "|") != strings.Join(want, "|") {
		t.Errorf("ran %v; want arguments ending %q", f.ran, want)
	}
}

func TestNewFromSlugLinksProject(t *testing.T) {
	srv := fakeCloud(t)
	h := newHarness(t, map[string]string{"FW_CLOUD_TOKEN": "user-tok", "FW_CLOUD_URL": srv.URL})
	f := &fakeEngines{}
	f.install(h, "Rscript")
	before := minted
	out, _, err := h.run(t, "", "new", "--from", "vaccine-study")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(h.app.home, "vaccine-study")
	if !strings.Contains(f.ran[0].Args[1], "project_create_from_layers") || f.ran[0].Args[len(f.ran[0].Args)-1] != dir {
		t.Errorf("args %q", f.ran[0].Args)
	}
	if minted != before+1 {
		t.Error("want one pull token minted after creation")
	}
	if project.EnvValue(dir, "FW_PROJECT_TOKEN") != "minted-tok" || project.EnvValue(dir, "FW_CLOUD_URL") != "https://issuer.example.edu" {
		t.Error(".env not linked")
	}
	if strings.Contains(out, "minted-tok") {
		t.Error("printed the project token")
	}
	if layersFile := f.ran[0].Args[len(f.ran[0].Args)-2]; !strings.HasSuffix(layersFile, ".json") {
		t.Errorf("want a layers file argument, got %q", layersFile)
	} else if _, err := os.Stat(layersFile); !os.IsNotExist(err) {
		t.Error("the temporary layers file should be removed after a run")
	}
}

func TestNewFromSlugNotFound(t *testing.T) {
	srv := fakeCloud(t)
	h := newHarness(t, map[string]string{"FW_CLOUD_TOKEN": "user-tok", "FW_CLOUD_URL": srv.URL})
	f := &fakeEngines{}
	f.install(h, "Rscript")
	_, _, err := h.run(t, "", "new", "--from", "nope")
	if err == nil || !strings.Contains(err.Error(), "no project named") || len(f.ran) != 0 {
		t.Errorf("got %v, ran %v", err, f.ran)
	}
}

func TestNewWithPullToken(t *testing.T) {
	srv := fakeCloud(t)
	h := newHarness(t, map[string]string{"FW_CLOUD_URL": srv.URL})
	f := &fakeEngines{}
	f.install(h, "uv")
	if _, _, err := h.run(t, "proj-tok\n", "new", "--pull-token"); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(h.app.home, "vaccine-study")
	if !strings.Contains(f.ran[0].String(), "-m framework create") {
		t.Errorf("want python: %s", f.ran[0])
	}
	if project.EnvValue(dir, "FW_PROJECT_TOKEN") != "proj-tok" || project.EnvValue(dir, "FW_CLOUD_URL") != srv.URL {
		t.Error(".env not linked")
	}

	_, _, err := h.run(t, "user-tok\n", "new", "--pull-token", "--dir", "other")
	if err == nil || !strings.Contains(err.Error(), "needs a project token") {
		t.Errorf("user token: %v", err)
	}
}

func TestNewPrintRunsNothing(t *testing.T) {
	srv := fakeCloud(t)
	h := newHarness(t, map[string]string{"FW_CLOUD_TOKEN": "user-tok", "FW_CLOUD_URL": srv.URL})
	f := &fakeEngines{}
	f.install(h, "Rscript")
	before := minted
	out, errOut, err := h.run(t, "", "new", "--from", "vaccine-study", "--print")
	if err != nil {
		t.Fatal(err)
	}
	if len(f.ran) != 0 || minted != before {
		t.Error("--print must not run anything or mint a token")
	}
	if !strings.Contains(out, "project_create_from_layers") || !strings.Contains(errOut, "layers are saved in") {
		t.Errorf("out %s\nerr %s", out, errOut)
	}
}

func TestNewMissingRuntimeKeepsLayers(t *testing.T) {
	h := newHarness(t, nil)
	f := &fakeEngines{}
	f.install(h)
	file := filepath.Join(h.app.home, "l.json")
	writeTestFile(t, file, `{"spec_version":1,"name":"A","layers":[]}`)
	_, errOut, err := h.run(t, "", "new", "--from", file)
	var missing *engine.MissingError
	if !errors.As(err, &missing) || !strings.Contains(err.Error(), "Rscript -e") || !strings.Contains(errOut, "layers are saved") {
		t.Fatalf("got %v\n%s", err, errOut)
	}
}

func TestNewRefusals(t *testing.T) {
	h := newHarness(t, nil)
	f := &fakeEngines{}
	f.install(h, "Rscript", "uv")
	writeTestFile(t, filepath.Join(h.app.home, "taken", "x"), "x")
	stata := filepath.Join(h.app.home, "stata.json")
	writeTestFile(t, stata, `{"spec_version":1,"name":"S","layers":[{"kind":"project","layer":{"language":"stata"}}]}`)

	cases := map[string][]string{
		"not empty":          {"new", "taken"},
		"name with --from":   {"new", "x", "--from", stata},
		"headless and cloud": {"new", "--from", "slug", "--headless"},
		"no name":            {"new"},
	}
	for name, args := range cases {
		if _, _, err := h.run(t, "", args...); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	if len(f.ran) != 0 {
		t.Errorf("ran %v", f.ran)
	}
}

func TestNewFailedRunDoesNotLink(t *testing.T) {
	srv := fakeCloud(t)
	h := newHarness(t, map[string]string{"FW_CLOUD_TOKEN": "user-tok", "FW_CLOUD_URL": srv.URL})
	f := &fakeEngines{exitCode: 1}
	f.install(h, "Rscript")
	before := minted
	_, _, err := h.run(t, "", "new", "--from", "vaccine-study")
	var exit *exitError
	if !errors.As(err, &exit) || exit.code != 1 || minted != before {
		t.Errorf("got %v, minted %d", err, minted-before)
	}
}

func writeTestFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestProjectLink(t *testing.T) {
	srv := fakeCloud(t)
	h := newHarness(t, map[string]string{"FW_CLOUD_URL": srv.URL})
	proj := filepath.Join(h.app.home, "study")
	writeTestFile(t, filepath.Join(proj, "settings.yml"), "x: 1\n")
	writeTestFile(t, filepath.Join(proj, ".gitignore"), ".env\n")
	h.app.cwd = proj

	if _, _, err := h.run(t, "user-tok\n", "project", "link", "--pull-token"); err == nil || !strings.Contains(err.Error(), "user token") {
		t.Errorf("user token: %v", err)
	}
	out, _, err := h.run(t, "proj-tok\n", "project", "link", "--pull-token")
	if err != nil || project.EnvValue(proj, "FW_PROJECT_TOKEN") != "proj-tok" || strings.Contains(out, "Warning") {
		t.Errorf("%v\n%s", err, out)
	}

	h.app.cwd = h.app.home
	if _, _, err := h.run(t, "", "project", "link", "--pull-token"); err == nil || !strings.Contains(err.Error(), "not inside") {
		t.Errorf("outside a project: %v", err)
	}
}

func TestLinkRefusesTrackedEnv(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	srv := fakeCloud(t)
	h := newHarness(t, map[string]string{"FW_CLOUD_URL": srv.URL})
	proj := filepath.Join(h.app.home, "study")
	writeTestFile(t, filepath.Join(proj, "settings.yml"), "x: 1\n")
	writeTestFile(t, filepath.Join(proj, ".env"), "A=1\n")
	for _, args := range [][]string{{"init", "-q"}, {"add", ".env"}} {
		cmd := exec.Command("git", args...) // #nosec G204 -- test-only git calls
		cmd.Dir = proj
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
	}
	h.app.cwd = proj
	_, _, err := h.run(t, "proj-tok\n", "project", "link", "--pull-token")
	if err == nil || !strings.Contains(err.Error(), "git rm --cached .env") {
		t.Fatalf("got %v", err)
	}
	if project.EnvValue(proj, "FW_PROJECT_TOKEN") != "" {
		t.Error("wrote the token into a tracked .env")
	}
}

func TestNewStata(t *testing.T) {
	h := newHarness(t, nil)
	f := &fakeEngines{}
	f.install(h)
	out, _, err := h.run(t, "", "new", "Stata Study", "--lang", "stata", "--type", "project_sensitive")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(h.app.home, "stata-study")
	for _, rel := range []string{"master.do", "settings.do", "do/.gitkeep", ".claude/settings.json", ".framework/spec.json"} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Errorf("missing %s", rel)
		}
	}
	if len(f.ran) != 0 || !strings.Contains(out, "do master.do") {
		t.Errorf("ran %v\n%s", f.ran, out)
	}
}

// Unsupported choices name what Stata projects take, before anything is made.
func TestNewStataUnsupportedChoices(t *testing.T) {
	h := newHarness(t, nil)
	for args, want := range map[string][]string{
		"--workflow quarto":   {"no quarto workflow", "--workflow scripts only", "project, project_sensitive, or bare", "R and Python"},
		"--workflow jupyter":  {"no jupyter workflow", "--workflow scripts only"},
		"--type presentation": {`no Stata "presentation" type`, "project, project_sensitive, or bare", "--workflow scripts", "R and Python"},
		"--type course":       {`no Stata "course" type`, "R and Python"},
		"--type survey":       {`no Stata "survey" type`, "project, project_sensitive, or bare"},
	} {
		_, _, err := h.run(t, "", append([]string{"new", "S", "--lang", "stata", "--offline"}, strings.Fields(args)...)...)
		if exitCode(err) != exitUsage {
			t.Errorf("%s: %v", args, err)
			continue
		}
		for _, w := range want {
			if !strings.Contains(err.Error(), w) {
				t.Errorf("%s: %q lacks %q", args, err, w)
			}
		}
	}
	if _, err := os.Stat(filepath.Join(h.app.home, "s")); !os.IsNotExist(err) {
		t.Error("a rejected choice created files")
	}
	help, _, err := h.run(t, "", "new", "--help")
	if err != nil || !strings.Contains(help, "stata      --type project (the default), project_sensitive, or bare;") || !strings.Contains(help, "--workflow scripts only") {
		t.Errorf("help:\n%s", help)
	}
}

func TestNewStataFromSlugLinks(t *testing.T) {
	srv := fakeCloud(t)
	h := newHarness(t, map[string]string{"FW_CLOUD_TOKEN": "user-tok", "FW_CLOUD_URL": srv.URL})
	stataLayers = true
	defer func() { stataLayers = false }()
	if _, _, err := h.run(t, "", "new", "--from", "vaccine-study"); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(h.app.home, "vaccine-study")
	if project.EnvValue(dir, "FW_PROJECT_TOKEN") != "minted-tok" {
		t.Error(".env not linked")
	}
	if _, err := os.Stat(filepath.Join(dir, "master.do")); err != nil {
		t.Error(err)
	}
}

func TestNewStataPrint(t *testing.T) {
	h := newHarness(t, nil)
	out, _, err := h.run(t, "", "new", "S", "--lang", "stata", "--print")
	if err != nil || !strings.Contains(out, `"language": "stata"`) {
		t.Fatalf("%v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(h.app.home, "s")); !os.IsNotExist(err) {
		t.Error("--print created files")
	}
}

func TestProjectStatusAndBootstrap(t *testing.T) {
	srv := fakeCloud(t)
	h := newHarness(t, map[string]string{"FW_CLOUD_URL": srv.URL})
	f := &fakeEngines{}
	f.install(h, "Rscript")
	proj := filepath.Join(h.app.home, "study")
	writeTestFile(t, filepath.Join(proj, "settings.yml"), "x: 1\n")
	writeTestFile(t, filepath.Join(proj, "renv.lock"), "{}")
	writeTestFile(t, filepath.Join(proj, ".env"), "FW_PROJECT_TOKEN=proj-tok\n")
	h.app.cwd = filepath.Join(proj)

	out, _, err := h.run(t, "", "project", "status")
	if err != nil || !strings.Contains(out, "r (from settings.yml)") || !strings.Contains(out, "renv") || !strings.Contains(out, "as project study") {
		t.Errorf("status: %v\n%s", err, out)
	}
	if strings.Contains(out, "proj-tok") {
		t.Error("status printed the project token")
	}
	// renv.lock without .Rprofile: renv is not active.
	if strings.Contains(out, "renv.lock's versions apply") || !strings.Contains(out, "no .Rprofile activates renv") {
		t.Errorf("renv claim:\n%s", out)
	}

	if _, _, err := h.run(t, "", "project", "bootstrap"); err != nil {
		t.Fatal(err)
	}
	if len(f.ran) != 1 || f.ran[0].Dir != proj || !strings.Contains(f.ran[0].String(), "framework::bootstrap(interactive = FALSE)") {
		t.Errorf("ran %v", f.ran)
	}

	h.app.engines.Probe = func(engine.Command) bool { return false }
	_, _, err = h.run(t, "", "project", "bootstrap")
	if exitCode(err) != exitFailure || !strings.Contains(err.Error(), "no bootstrap yet") || len(f.ran) != 1 {
		t.Errorf("old package: %v", err)
	}

	// R missing: an error, not a message and exit 0.
	h.app.engines.LookPath = func(string) (string, error) { return "", exec.ErrNotFound }
	if _, _, err := h.run(t, "", "project", "bootstrap"); exitCode(err) != exitFailure || !strings.Contains(err.Error(), "R is not installed") {
		t.Errorf("no R: %v", err)
	}

	// An adopted Stata project has settings.do and no master.do.
	stataDir := filepath.Join(h.app.home, "adopted")
	writeTestFile(t, filepath.Join(stataDir, "settings.do"), "")
	writeTestFile(t, filepath.Join(stataDir, ".framework", "spec.json"), `{"language":"stata"}`)
	h.app.cwd = stataDir
	out, _, err = h.run(t, "", "project", "bootstrap")
	if err != nil || !strings.Contains(out, "do settings.do") || strings.Contains(out, "master.do") {
		t.Errorf("adopted Stata: %v\n%s", err, out)
	}
}

// fw new SLUG --cloud leaves a folder named SLUG. --from SLUG then still
// means the cloud project, not a layers file.
func TestNewFromSlugWithFolderOfThatName(t *testing.T) {
	srv := fakeCloud(t)
	h := newHarness(t, map[string]string{"FW_CLOUD_TOKEN": "user-tok", "FW_CLOUD_URL": srv.URL})
	f := &fakeEngines{}
	f.install(h, "Rscript")
	if err := os.Mkdir(filepath.Join(h.app.home, "vaccine-study"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Chdir(h.app.home)
	if _, _, err := h.run(t, "", "new", "--from", "vaccine-study"); err != nil {
		t.Fatal(err)
	}
	if len(f.ran) != 1 || !strings.Contains(f.ran[0].Args[1], "project_create_from_layers") {
		t.Errorf("ran %v", f.ran)
	}
	if isLayersPath("vaccine-study") {
		t.Error("a folder counted as a layers file")
	}
	write(t, h.app.home, "layers", "{}")
	if !isLayersPath("layers") {
		t.Error("an existing file without .json is a layers file")
	}
}

// R's new() takes offline and workflow. fw passes them when the installed
// package has them, whatever its version number.
func TestNewROfflineAndWorkflow(t *testing.T) {
	h := newHarness(t, nil)
	f := &fakeEngines{}
	f.install(h, "Rscript")
	if _, _, err := h.run(t, "", "new", "Study", "--offline", "--workflow", "scripts"); err != nil {
		t.Fatal(err)
	}
	got := f.ran[0].Args
	if !strings.Contains(got[1], "offline = as.logical(a[5])") || strings.Join(got[len(got)-2:], "|") != "scripts|TRUE" {
		t.Errorf("args %q", got)
	}

	// An R package whose new_project() lacks them: say to update it.
	h.app.engines.Probe = func(c engine.Command) bool { return !strings.Contains(c.Args[1], "offline") }
	if _, _, err := h.run(t, "", "new", "Other", "--offline"); err == nil || !strings.Contains(err.Error(), "does not take --offline") {
		t.Errorf("old R package: %v", err)
	}
}

// Logged in with the server down: R's new() stops without saved defaults.
// fw asks at a terminal, and otherwise names --offline.
func TestNewRServerDown(t *testing.T) {
	down := httptest.NewServer(http.NotFoundHandler())
	down.Close()
	h := newHarness(t, map[string]string{"FW_CLOUD_TOKEN": "user-tok", "FW_CLOUD_URL": down.URL})
	f := &fakeEngines{exitCode: 1}
	f.install(h, "Rscript")
	_, _, err := h.run(t, "", "new", "Study")
	if err == nil || !strings.Contains(err.Error(), "cannot reach") || !strings.Contains(err.Error(), "--offline") {
		t.Errorf("no terminal: %v", err)
	}
	if len(f.ran) != 1 || strings.Contains(f.ran[0].Args[1], "offline") {
		t.Errorf("without a terminal R runs as asked: %v", f.ran)
	}

	f.exitCode = 0
	h.app.interactive = true
	if _, _, err := h.run(t, "n\n", "new", "Declined"); err == nil || !strings.Contains(err.Error(), "canceled") || len(f.ran) != 1 {
		t.Errorf("declined: %v", err)
	}
	_, errOut, err := h.run(t, "y\n", "new", "Accepted")
	if err != nil || !strings.Contains(errOut, "Create the project offline") {
		t.Fatalf("accepted: %v\n%s", err, errOut)
	}
	if got := f.ran[len(f.ran)-1].Args; got[len(got)-1] != "TRUE" {
		t.Errorf("accepted at a terminal: want offline, got %q", got)
	}
}

// Linking a linked project changes nothing and mints no token.
func TestProjectLinkAlreadyLinked(t *testing.T) {
	srv := fakeCloud(t)
	h := newHarness(t, map[string]string{"FW_CLOUD_TOKEN": "user-tok", "FW_CLOUD_URL": srv.URL})
	root := linkedProject(t, h, srv.URL) // linked to "study"
	before := minted
	envBefore, _ := os.ReadFile(filepath.Join(root, ".env"))

	_, _, err := h.run(t, "", "project", "link", "--from", "vaccine-study")
	if err == nil || !strings.Contains(err.Error(), "already linked to study") {
		t.Errorf("other slug: %v", err)
	}
	out, _, err := h.run(t, "", "project", "link", "--from", "study")
	if err != nil || !strings.Contains(out, "Already linked to study") {
		t.Errorf("same slug: %v %s", err, out)
	}
	if _, _, err := h.run(t, "proj-tok\n", "project", "link", "--pull-token"); err == nil {
		t.Error("--pull-token on a linked project")
	}
	envAfter, _ := os.ReadFile(filepath.Join(root, ".env"))
	if minted != before || string(envAfter) != string(envBefore) {
		t.Errorf("minted %d tokens; .env changed: %v", minted-before, string(envAfter) != string(envBefore))
	}

	// An empty entry, as .env.example leaves, is not a link: fill it in.
	write(t, root, ".env", "# from .env.example\nFW_PROJECT_TOKEN=\nFW_CLOUD_URL=\nOTHER=1\n")
	if _, _, err := h.run(t, "", "project", "link", "--from", "vaccine-study"); err != nil {
		t.Fatal(err)
	}
	if project.EnvValue(root, "FW_PROJECT_TOKEN") != "minted-tok" || project.EnvValue(root, "OTHER") != "1" || minted != before+1 {
		data, _ := os.ReadFile(filepath.Join(root, ".env"))
		t.Errorf("empty entries: %s", data)
	}
}

func TestProjectStatusExitCodes(t *testing.T) {
	srv := fakeCloud(t)
	h := newHarness(t, nil)
	(&fakeEngines{}).install(h, "Rscript")
	root := linkedProject(t, h, srv.URL)
	if _, _, err := h.run(t, "", "project", "status"); err != nil {
		t.Errorf("accepted token: %v", err)
	}
	write(t, root, ".env", "FW_PROJECT_TOKEN=revoked\nFW_CLOUD_URL="+srv.URL+"\n")
	out, _, err := h.run(t, "", "project", "status", "--json")
	if exitCode(err) != exitAuth || !strings.Contains(out, `"ok": false`) {
		t.Errorf("rejected token: exit %d %v\n%s", exitCode(err), err, out)
	}
	write(t, root, ".env", "")
	if _, _, err := h.run(t, "", "project", "status"); err != nil {
		t.Errorf("not linked: %v", err)
	}
}

// Without --type, a new project is the Standard Project. bare is still there.
func TestNewDefaultsToStandardProject(t *testing.T) {
	h := newHarness(t, nil)
	f := &fakeEngines{}
	f.install(h)
	if _, _, err := h.run(t, "", "new", "Standard Study", "--lang", "stata", "--headless"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(h.app.home, "standard-study", ".framework", "spec.json"))
	if err != nil || !strings.Contains(string(data), `"type": "project"`) {
		t.Fatalf("default type: %v\n%s", err, data)
	}
	if _, _, err := h.run(t, "", "new", "Bare Study", "--lang", "stata", "--type", "bare", "--headless"); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(filepath.Join(h.app.home, "bare-study", ".framework", "spec.json"))
	if err != nil || !strings.Contains(string(data), `"type": "bare"`) {
		t.Fatalf("bare type: %v\n%s", err, data)
	}
	usage := newNewCmd(h.app).Flags().Lookup("type").Usage
	if !strings.Contains(usage, "project (the Standard Project, the default)") {
		t.Errorf("--type help: %s", usage)
	}
}
