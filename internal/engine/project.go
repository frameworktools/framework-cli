package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
)

// Project describes an existing project: its language, and how to run code
// inside its own environment so pinned package versions apply.
type Project struct {
	Root           string
	Language       string // r, python, stata, or "" when unknown
	LanguageSource string // where the language came from
	// Runner is the program and leading arguments that run code in the
	// project's environment, from the project root. Empty for Stata, which
	// fw does not run.
	Runner []string
	// Reason says why fw chose Runner, for fw project status.
	Reason string
}

func exists(root, rel string) bool {
	_, err := os.Stat(filepath.Join(root, rel))
	return err == nil
}

// Detect reads the project at root. The language comes from
// .framework/spec.json, or from the files present for projects made before
// specs.
func (f Finder) Detect(root string) Project {
	p := Project{Root: root}
	if data, err := os.ReadFile(filepath.Join(root, ".framework", "spec.json")); err == nil {
		var doc struct {
			Language string `json:"language"`
		}
		if json.Unmarshal(data, &doc) == nil {
			p.Language, p.LanguageSource = doc.Language, ".framework/spec.json"
			if p.Language == "" {
				p.Language, p.LanguageSource = "r", ".framework/spec.json (no language means r)"
			}
		}
	}
	if p.Language == "" {
		switch {
		case exists(root, "master.do") && exists(root, "settings.do"):
			p.Language, p.LanguageSource = "stata", "master.do and settings.do"
		case exists(root, "pyproject.toml"):
			p.Language, p.LanguageSource = "python", "pyproject.toml"
		case exists(root, "settings.yml") || exists(root, "renv.lock"):
			p.Language, p.LanguageSource = "r", "settings.yml"
		}
	}

	switch p.Language {
	case "r":
		rscript := f.find("FW_RSCRIPT", "Rscript")
		if rscript == "" {
			p.Reason = "R is not installed, or Rscript is not on your PATH"
			return p
		}
		p.Runner = []string{rscript}
		switch {
		case exists(root, "renv.lock"):
			p.Reason = "Rscript from the project folder; .Rprofile activates renv, so renv.lock's versions apply"
		case exists(root, ".Rprofile"):
			p.Reason = "Rscript from the project folder, which runs its .Rprofile"
		default:
			p.Reason = "Rscript from the project folder, with your R library"
		}
	case "python":
		venvPython := filepath.Join(".venv", "bin", "python")
		if runtime.GOOS == "windows" {
			venvPython = filepath.Join(".venv", "Scripts", "python.exe")
		}
		uv := f.find("FW_UV", "uv")
		switch {
		case exists(root, "uv.lock") && uv != "":
			p.Runner, p.Reason = []string{uv, "run", "python"}, "uv run, so uv.lock's versions apply"
		case exists(root, venvPython):
			p.Runner, p.Reason = []string{filepath.Join(root, venvPython)}, "the project's .venv"
		case uv != "":
			p.Runner, p.Reason = []string{uv, "run", "python"}, "uv run with pyproject.toml"
		default:
			if py := f.find("FW_PYTHON", "python3"); py != "" {
				p.Runner, p.Reason = []string{py}, "python3 on your PATH (no uv.lock or .venv found)"
			} else {
				p.Reason = "neither uv nor python3 was found"
			}
		}
	case "stata":
		p.Reason = "fw does not run Stata. Open Stata in the project folder and run: do master.do"
	}
	return p
}

// UnavailableError means the project's language package has no bootstrap
// yet, or fw cannot run the language at all.
type UnavailableError struct{ Msg string }

func (e *UnavailableError) Error() string { return e.Msg }

// Bootstrap returns the command that prepares an existing project after a
// clone: the language package's own bootstrap, run in the project's
// environment from its root.
func (f Finder) Bootstrap(p Project) (Command, error) {
	if p.Language == "stata" {
		return Command{}, &UnavailableError{"Stata projects need no bootstrap. Open Stata in the project folder and run: do master.do. settings.do creates the folders git does not keep."}
	}
	if p.Language == "" {
		return Command{}, &UnavailableError{"fw cannot tell this project's language (no .framework/spec.json, settings.yml, or pyproject.toml)"}
	}
	if len(p.Runner) == 0 {
		return Command{}, &UnavailableError{"cannot run this " + p.Language + " project: " + p.Reason}
	}
	run := func(args ...string) Command {
		return Command{Program: p.Runner[0], Args: append(append([]string{}, p.Runner[1:]...), args...), Dir: p.Root}
	}
	var probe, cmd Command
	switch p.Language {
	case "r":
		probe = run("-e", `if (!requireNamespace("framework", quietly = TRUE) || !exists("bootstrap", envir = asNamespace("framework"), inherits = FALSE)) quit(status = 3)`)
		cmd = run("-e", "framework::bootstrap(interactive = FALSE)")
	case "python":
		probe = run("-m", "framework", "bootstrap", "--help")
		cmd = run("-m", "framework", "bootstrap")
	default:
		return Command{}, &UnavailableError{"unknown language " + p.Language}
	}
	if !f.Probe(probe) {
		pkg := map[string]string{"r": "framework R package", "python": "framework-py package"}[p.Language]
		return Command{}, &UnavailableError{"fw could not find bootstrap in this project's environment. Either the environment could not be set up, or the " + pkg + " there has no bootstrap yet. To see which, run: " + probe.String()}
	}
	return cmd, nil
}
