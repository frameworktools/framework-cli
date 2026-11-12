package engine

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func finder(found map[string]string, probeOK bool, env map[string]string) Finder {
	return Finder{
		Getenv: func(k string) string { return env[k] },
		LookPath: func(name string) (string, error) {
			if p, ok := found[name]; ok {
				return p, nil
			}
			return "", exec.ErrNotFound
		},
		Probe: func(Command) bool { return probeOK },
		Home:  "/home/u",
	}
}

func TestCreateRFromLayers(t *testing.T) {
	f := finder(map[string]string{"Rscript": "/usr/bin/Rscript"}, true, nil)
	cmd, err := f.Create("r", Request{LayersFile: "/tmp/l.json", Dir: "/w/My Study", Interactive: true})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Program != "/usr/bin/Rscript" || cmd.Dir != "/home/u" {
		t.Errorf("got %+v", cmd)
	}
	args := strings.Join(cmd.Args, "\x00")
	if !strings.Contains(args, "project_create_from_layers(a[1], a[2], interactive = TRUE)") ||
		!strings.HasSuffix(args, ")\x00/tmp/l.json\x00/w/My Study") {
		t.Errorf("args %q", cmd.Args)
	}
	if !strings.Contains(cmd.String(), "'/w/My Study'") {
		t.Errorf("display %s", cmd.String())
	}
}

func TestCreateRFromDefaults(t *testing.T) {
	f := finder(map[string]string{"Rscript": "Rscript"}, true, nil)
	cmd, err := f.Create("r", Request{Name: "it's", Type: "", Dir: "/w/its"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cmd.Args[1], "framework::new(") || cmd.Args[len(cmd.Args)-3] != "it's" {
		t.Errorf("args %q", cmd.Args)
	}
	if !strings.Contains(cmd.String(), `'it'\''s'`) {
		t.Errorf("display %s", cmd.String())
	}
}

func TestCreateRMissing(t *testing.T) {
	var missing *MissingError
	_, err := finder(nil, true, nil).Create("r", Request{LayersFile: "/l.json", Dir: "/w"})
	if !errors.As(err, &missing) || !strings.Contains(err.Error(), "Rscript -e") {
		t.Errorf("no R: %v", err)
	}
	_, err = finder(map[string]string{"Rscript": "Rscript"}, false, nil).Create("r", Request{LayersFile: "/l.json", Dir: "/w"})
	if !errors.As(err, &missing) || !strings.Contains(err.Error(), "pak::pak") {
		t.Errorf("no package: %v", err)
	}
}

func TestCreatePython(t *testing.T) {
	req := Request{LayersFile: "/l.json", Dir: "/w"}
	cmd, err := finder(map[string]string{"uv": "/bin/uv", "python3": "/bin/python3"}, true, nil).Create("python", req)
	if err != nil || cmd.String() != "/bin/uv run --no-project --refresh-package framework-py --with 'framework-py>=0.1.0.dev16' python -m framework create --from-layers /l.json --path /w" {
		t.Errorf("uv: %s %v", cmd, err)
	}

	cmd, err = finder(map[string]string{"python3": "/bin/python3"}, true, nil).Create("python", Request{LayersFile: "/l.json", Dir: "/w", Interactive: true})
	if err != nil || cmd.String() != "/bin/python3 -m framework create --from-layers /l.json --path /w --interactive" {
		t.Errorf("python3: %s %v", cmd, err)
	}

	_, err = finder(map[string]string{"python3": "/bin/python3"}, false, nil).Create("python", req)
	var missing *MissingError
	if !errors.As(err, &missing) {
		t.Errorf("python3 without the package: %v", err)
	}

	// A package of the user's choosing has no minimum version to refresh for
	env := map[string]string{"FW_PYTHON_PACKAGE": "/src/framework-py"}
	cmd, _ = finder(map[string]string{"uv": "uv"}, true, env).Create("python", req)
	if !strings.Contains(cmd.String(), "--with /src/framework-py") || strings.Contains(cmd.String(), "--refresh-package") {
		t.Errorf("FW_PYTHON_PACKAGE: %s", cmd)
	}

	// uv refuses --refresh-package when it is told to stay offline
	for value, refresh := range map[string]bool{"1": false, "true": false, "Yes": false, "0": true, "false": true} {
		cmd, _ = finder(map[string]string{"uv": "uv"}, true, map[string]string{"UV_OFFLINE": value}).Create("python", req)
		if strings.Contains(cmd.String(), "--refresh-package framework-py") != refresh {
			t.Errorf("UV_OFFLINE=%s: %s", value, cmd)
		}
	}
}

func TestCreatePythonFromDefaults(t *testing.T) {
	f := finder(map[string]string{"uv": "uv"}, true, nil)
	cmd, err := f.Create("python", Request{Name: "My Study", Type: "project", Dir: "/w/my-study", Workflow: "jupyter", Offline: true})
	if err != nil || cmd.String() != "uv run --no-project --refresh-package framework-py --with 'framework-py>=0.1.0.dev16' python -m framework create --name 'My Study' --path /w/my-study --type project --workflow jupyter --offline" {
		t.Errorf("got %s %v", cmd, err)
	}
}

func TestAdoptPython(t *testing.T) {
	cmd, err := finder(map[string]string{"uv": "uv"}, true, nil).AdoptPython("/w/old", true, AdoptOptions{})
	if err != nil || cmd.String() != "uv run --no-project --refresh-package framework-py --with 'framework-py>=0.1.0.dev16' python -m framework adopt --path /w/old --yes" {
		t.Errorf("got %s %v", cmd, err)
	}
	cmd, err = finder(map[string]string{"uv": "uv"}, true, nil).AdoptPython("/w/old", true, AdoptOptions{Type: "project", Sensitive: true})
	if err != nil || !strings.HasSuffix(cmd.String(), "adopt --path /w/old --yes --type project --sensitive") {
		t.Errorf("got %s %v", cmd, err)
	}
	// An explicit --lang python passes --force, so framework-py adopts an R-looking folder.
	cmd, err = finder(map[string]string{"uv": "uv"}, true, nil).AdoptPython("/w/old", true, AdoptOptions{Force: true})
	if err != nil || !strings.HasSuffix(cmd.String(), "adopt --path /w/old --yes --force") {
		t.Errorf("force: got %s %v", cmd, err)
	}
	// An older framework-py, whose adopt has no --type: say so, and run nothing.
	if _, err := finder(map[string]string{"uv": "uv"}, false, nil).AdoptPython("/w/old", true, AdoptOptions{Sensitive: true}); err == nil || !strings.Contains(err.Error(), "does not take --sensitive in adopt") {
		t.Errorf("old framework-py: %v", err)
	}
	// --private, once per folder, needs a framework-py that lists it.
	var probed []string
	f := finder(map[string]string{"uv": "uv"}, true, nil)
	f.Probe = func(c Command) bool { probed = append(probed, c.Args[len(c.Args)-1]); return true }
	cmd, err = f.AdoptPython("/w/old", true, AdoptOptions{Sensitive: true, Private: []string{"reports", "data files"}})
	if err != nil || !strings.HasSuffix(cmd.String(), "adopt --path /w/old --yes --sensitive --private reports --private 'data files'") ||
		len(probed) != 1 || !strings.Contains(probed[0], `["--sensitive", "--private"]`) {
		t.Errorf("private: %s %v %q", cmd, err, probed)
	}
	if _, err := finder(map[string]string{"uv": "uv"}, false, nil).AdoptPython("/w/old", true, AdoptOptions{Private: []string{"reports"}}); err == nil ||
		!strings.Contains(err.Error(), "does not take --private in adopt yet; --private came after framework-py 0.1.0.dev13") {
		t.Errorf("old framework-py, --private: %v", err)
	}
	// Without them, nothing is checked.
	if _, err := finder(map[string]string{"uv": "uv"}, false, nil).AdoptPython("/w/old", true, AdoptOptions{}); err != nil {
		t.Errorf("plain: %v", err)
	}
}

func TestAdoptR(t *testing.T) {
	f := finder(map[string]string{"Rscript": "/bin/Rscript"}, true, nil)
	cmd, err := f.AdoptR("/w/old", AdoptOptions{Type: "project_sensitive", Sensitive: true})
	if err != nil || cmd.Args[1] != "a <- commandArgs(TRUE); framework::adopt(a[1], yes = TRUE, type = a[2], sensitive = TRUE)" || strings.Join(cmd.Args[2:], ",") != "/w/old,project_sensitive" {
		t.Errorf("got %v %v", cmd.Args, err)
	}
	var checked []string
	f.Probe = func(c Command) bool {
		checked = append(checked, c.Args[1])
		return !strings.Contains(c.Args[1], "formals")
	}
	var missing *MissingError
	if _, err := f.AdoptR("/w/old", AdoptOptions{Sensitive: true}); !errors.As(err, &missing) || !strings.Contains(err.Error(), "does not take type or sensitive") {
		t.Errorf("old R package: %v", err)
	}
	if len(checked) == 0 || !strings.Contains(checked[0], `c("sensitive") %in% names(formals(`) {
		t.Errorf("checked %v", checked)
	}

	// private = takes the folders after the other arguments.
	f.Probe = func(Command) bool { return true }
	cmd, err = f.AdoptR("/w/old", AdoptOptions{Type: "project", Private: []string{"reports", "data-files"}})
	if err != nil || cmd.Args[1] != "a <- commandArgs(TRUE); framework::adopt(a[1], yes = TRUE, type = a[2], private = a[3:length(a)])" ||
		strings.Join(cmd.Args[2:], ",") != "/w/old,project,reports,data-files" {
		t.Errorf("private: %v %v", cmd.Args, err)
	}
	checked = nil
	f.Probe = func(c Command) bool {
		checked = append(checked, c.Args[1])
		return !strings.Contains(c.Args[1], "formals")
	}
	if _, err := f.AdoptR("/w/old", AdoptOptions{Private: []string{"reports"}}); !errors.As(err, &missing) ||
		!strings.Contains(err.Error(), "does not take private, which came after framework 2.0.1. Update it") || !strings.Contains(checked[0], `c("private")`) {
		t.Errorf("old R package, private: %v %v", err, checked)
	}
}

func TestRCapabilityChecks(t *testing.T) {
	// FW_RSCRIPT naming a missing program is named as such.
	var missing *MissingError
	_, err := finder(nil, true, map[string]string{"FW_RSCRIPT": "/no/Rscript"}).Create("r", Request{Name: "s", Dir: "/w"})
	if !errors.As(err, &missing) || !strings.Contains(err.Error(), "FW_RSCRIPT is set to /no/Rscript") {
		t.Errorf("FW_RSCRIPT missing: %v", err)
	}

	// Installed but lacking the capability: update it, not install it.
	f := finder(map[string]string{"Rscript": "Rscript"}, true, nil)
	f.Probe = func(c Command) bool { return !strings.Contains(c.Args[1], "offline") }
	_, err = f.Create("r", Request{Name: "s", Dir: "/w", Offline: true})
	if !errors.As(err, &missing) || !strings.Contains(err.Error(), "does not take --offline or --workflow. Update it") {
		t.Errorf("old package: %v", err)
	}
	// The check asks about functions and arguments, not a version number.
	var probes []string
	f.Probe = func(c Command) bool { probes = append(probes, c.Args[1]); return true }
	cmd, err := f.Create("r", Request{Name: "s", Dir: "/w", Workflow: "scripts"})
	if err != nil || strings.Contains(strings.Join(probes, " "), "packageVersion") || !strings.Contains(probes[0], `names(formals(get("new_project"`) {
		t.Errorf("probe %q: %v", probes, err)
	}
	if got := strings.Join(cmd.Args[len(cmd.Args)-2:], " "); got != "scripts FALSE" {
		t.Errorf("args %q", cmd.Args)
	}

	cmd, err = f.AdoptR("/w/old", AdoptOptions{})
	if err != nil || !strings.Contains(cmd.Args[1], "framework::adopt(a[1], yes = TRUE)") || cmd.Args[2] != "/w/old" || !strings.Contains(probes[len(probes)-1], `"adopt"`) {
		t.Errorf("adopt: %v %v", cmd, err)
	}
}

// Framework's own errors from a Python call print as one "Error:" line,
// as R's do; others keep their traceback.
func TestPyCallPrintsFrameworkErrorsAsOneLine(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("no python3")
	}
	dir := t.TempDir()
	module := "class FrameworkError(Exception):\n    pass\n\n" +
		"def pull_setup(**kwargs):\n    raise FrameworkError(\"Pull first: pull_setup().\")\n\n" +
		"def push_setup(**kwargs):\n    raise ValueError(\"bug\")\n"
	if err := os.WriteFile(filepath.Join(dir, "framework.py"), []byte(module), 0o600); err != nil {
		t.Fatal(err)
	}
	run := func(call string) (string, int) {
		cmd := exec.Command(python, "-c", pyCall(call)) //nolint:gosec // python3 from PATH, running the test's own module
		cmd.Dir = dir
		out, _ := cmd.CombinedOutput()
		return string(out), cmd.ProcessState.ExitCode()
	}
	if out, code := run("pull_setup(confirm=True)"); out != "Error: Pull first: pull_setup().\n" || code != 1 {
		t.Errorf("framework error: %d %q", code, out)
	}
	if out, code := run("push_setup(confirm=True)"); !strings.Contains(out, "Traceback") || code != 1 {
		t.Errorf("other error: %d %q", code, out)
	}
}

// fw setup push rewrites the packages' "Pull first" advice to name fw's
// command and flag.
func TestPushSetupAdvice(t *testing.T) {
	f := finder(map[string]string{"uv": "uv"}, true, nil)
	dir := t.TempDir()
	cmd, err := f.PushSetup(Project{Root: dir, Language: "python", Runner: []string{"python3"}}, SetupOptions{}, SetupConfirmed)
	if err != nil || cmd.Rewrite == nil {
		t.Fatalf("%v %+v", err, cmd)
	}
	for in, want := range map[string]string{
		// R's stop() and push_skills()
		"Error: Framework Cloud's setup changed since this project last pulled it (revision r2). Pull first: pull_setup(). push_setup(force = TRUE) replaces Cloud's changes.": "Error: Framework Cloud's setup changed since this project last pulled it (revision r2). Pull first: fw setup pull. fw setup push --force replaces Cloud's changes.",
		"  Pull first: analysis changed on Cloud since this project pulled it. push_skills(force = TRUE) replaces it.":                                                         "  Pull first: analysis changed on Cloud since this project pulled it. fw setup push --force replaces it.",
		// Python's
		"Error: Framework Cloud's setup changed since this project last pulled it (revision r2). Pull first: pull_setup(). push_setup(force=True) replaces Cloud's changes.": "Error: Framework Cloud's setup changed since this project last pulled it (revision r2). Pull first: fw setup pull. fw setup push --force replaces Cloud's changes.",
		"  Pull first: analysis changed on Cloud since this project pulled it. push_skills(force=True) replaces it.":                                                         "  Pull first: analysis changed on Cloud since this project pulled it. fw setup push --force replaces it.",
	} {
		if got := cmd.Rewrite.Replace(in); got != want {
			t.Errorf("got  %s\nwant %s", got, want)
		}
	}
	if pull, _ := f.PullSetup(Project{Root: dir, Language: "python", Runner: []string{"python3"}}, SetupOptions{}, SetupDryRun); pull.Rewrite != nil {
		t.Error("pull_setup() output is rewritten")
	}
}
