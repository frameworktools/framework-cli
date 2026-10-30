package engine

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func touch(t *testing.T, root string, rels ...string) {
	t.Helper()
	for _, rel := range rels {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDetect(t *testing.T) {
	all := map[string]string{"Rscript": "/bin/Rscript", "uv": "/bin/uv", "python3": "/bin/python3"}
	cases := []struct {
		name, lang, runner, reason string
		files                      []string
		found                      map[string]string
	}{
		{"renv", "r", "/bin/Rscript", "renv", []string{"settings.yml", "renv.lock"}, all},
		{"spec without language", "r", "/bin/Rscript", "your R library", []string{".framework/spec.json"}, all},
		{"uv.lock", "python", "/bin/uv run python", "uv.lock", []string{"pyproject.toml", "uv.lock"}, all},
		{"venv", "python", ".venv", ".venv", []string{"pyproject.toml", ".venv/bin/python", ".venv/Scripts/python.exe"}, map[string]string{}},
		{"pyproject only", "python", "/bin/python3", "python3", []string{"pyproject.toml"}, map[string]string{"python3": "/bin/python3"}},
		{"stata", "stata", "", "do master.do", []string{"master.do", "settings.do"}, all},
		{"no R", "r", "", "not installed", []string{"settings.yml"}, map[string]string{}},
	}
	for _, c := range cases {
		root := t.TempDir()
		touch(t, root, c.files...)
		p := finder(c.found, true, nil).Detect(root)
		if p.Language != c.lang || !strings.Contains(strings.Join(p.Runner, " "), c.runner) || !strings.Contains(p.Reason, c.reason) {
			t.Errorf("%s: got %+v", c.name, p)
		}
	}
}

func TestDetectSpecLanguage(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".framework"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".framework", "spec.json"), []byte(`{"language":"stata"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	touch(t, root, "settings.yml") // the spec wins over files
	if p := finder(nil, true, nil).Detect(root); p.Language != "stata" || p.LanguageSource != ".framework/spec.json" {
		t.Errorf("got %+v", p)
	}
}

func TestBootstrap(t *testing.T) {
	root := t.TempDir()
	touch(t, root, "settings.yml", "renv.lock")
	f := finder(map[string]string{"Rscript": "/bin/Rscript"}, true, nil)
	cmd, err := f.Bootstrap(f.Detect(root))
	if err != nil || cmd.Dir != root || cmd.Args[1] != "framework::bootstrap(interactive = FALSE)" {
		t.Errorf("R: %+v %v", cmd, err)
	}

	f = finder(map[string]string{"Rscript": "/bin/Rscript"}, false, nil)
	var unavailable *UnavailableError
	if _, err := f.Bootstrap(f.Detect(root)); !errors.As(err, &unavailable) || !strings.Contains(err.Error(), "no bootstrap yet") {
		t.Errorf("old R package: %v", err)
	}

	py := t.TempDir()
	touch(t, py, "pyproject.toml", "uv.lock")
	f = finder(map[string]string{"uv": "/bin/uv"}, true, nil)
	cmd, err = f.Bootstrap(f.Detect(py))
	if err != nil || cmd.String() != "/bin/uv run python -m framework bootstrap" || cmd.Dir != py {
		t.Errorf("python: %s %v", cmd, err)
	}

	st := t.TempDir()
	touch(t, st, "master.do", "settings.do")
	if _, err := f.Bootstrap(f.Detect(st)); !errors.As(err, &unavailable) || !strings.Contains(err.Error(), "do master.do") {
		t.Errorf("stata: %v", err)
	}
}
