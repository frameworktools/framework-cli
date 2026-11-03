package engine

import (
	"errors"
	"os/exec"
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
	if err != nil || cmd.String() != "/bin/uv run --no-project --with 'framework-py>=0.1.0.dev5' python -m framework create --from-layers /l.json --path /w" {
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

	env := map[string]string{"FW_PYTHON_PACKAGE": "/src/framework-py"}
	cmd, _ = finder(map[string]string{"uv": "uv"}, true, env).Create("python", req)
	if !strings.Contains(cmd.String(), "--with /src/framework-py") {
		t.Errorf("FW_PYTHON_PACKAGE: %s", cmd)
	}
}

func TestCreatePythonFromDefaults(t *testing.T) {
	f := finder(map[string]string{"uv": "uv"}, true, nil)
	cmd, err := f.Create("python", Request{Name: "My Study", Type: "project", Dir: "/w/my-study", Workflow: "jupyter", Offline: true})
	if err != nil || cmd.String() != "uv run --no-project --with 'framework-py>=0.1.0.dev5' python -m framework create --name 'My Study' --path /w/my-study --type project --workflow jupyter --offline" {
		t.Errorf("got %s %v", cmd, err)
	}
}

func TestAdoptPython(t *testing.T) {
	cmd, err := finder(map[string]string{"uv": "uv"}, true, nil).AdoptPython("/w/old", true)
	if err != nil || cmd.String() != "uv run --no-project --with 'framework-py>=0.1.0.dev5' python -m framework adopt --path /w/old --yes" {
		t.Errorf("got %s %v", cmd, err)
	}
}
