package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"

	"github.com/frameworktools/framework-cli/internal/adopt"
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
	p.Language, p.LanguageSource = DetectLanguage(root)

	switch p.Language {
	case "r":
		rscript := f.find("FW_RSCRIPT", "Rscript")
		if rscript == "" {
			p.Reason = "R is not installed, or Rscript is not on your PATH"
			return p
		}
		p.Runner = []string{rscript}
		switch {
		case exists(root, "renv.lock") && exists(root, ".Rprofile"):
			p.Reason = "Rscript from the project folder; .Rprofile activates renv, so renv.lock's versions apply"
		case exists(root, "renv.lock"):
			p.Reason = "Rscript from the project folder, with your R library. renv.lock is there, but no .Rprofile activates renv"
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
		case uv != "" && exists(root, "pyproject.toml"):
			p.Runner, p.Reason = []string{uv, "run", "python"}, "uv run with pyproject.toml"
		default:
			none := "the project has no Python environment: no pyproject.toml, uv.lock, or .venv. Make one with uv init, or python3 -m venv .venv"
			if exists(root, "pyproject.toml") {
				none = "no uv.lock or .venv found, and uv is not installed"
			}
			if py := f.find("FW_PYTHON", "python3"); py != "" {
				p.Runner, p.Reason = []string{py}, "python3 on your PATH; "+none
			} else {
				p.Reason = "neither uv nor python3 was found; " + none
			}
		}
	case "stata":
		p.Reason = "fw does not run Stata. Open Stata in the project folder and run: do " + stataEntry(root)
	}
	return p
}

// stataEntry is the do-file to run first: master.do, or settings.do in an
// adopted project, which has no master.do.
func stataEntry(root string) string {
	if exists(root, "master.do") || !exists(root, "settings.do") {
		return "master.do"
	}
	return "settings.do"
}

// DetectLanguage returns the project's language and where it came from:
// .framework/spec.json, or the files present for projects made before
// specs. It returns "" when it cannot tell.
func DetectLanguage(root string) (language, source string) {
	if data, err := os.ReadFile(filepath.Join(root, ".framework", "spec.json")); err == nil {
		var doc struct {
			Language string `json:"language"`
		}
		if json.Unmarshal(data, &doc) == nil {
			if doc.Language == "" {
				return "r", ".framework/spec.json (no language means r)"
			}
			return doc.Language, ".framework/spec.json"
		}
	}
	switch {
	case exists(root, "master.do") && exists(root, "settings.do"):
		return "stata", "master.do and settings.do"
	case exists(root, "pyproject.toml"):
		return "python", "pyproject.toml"
	case exists(root, "settings.do"):
		// A bare Stata project has settings.do and settings.yml, and no master.do
		return "stata", "settings.do"
	case exists(root, "settings.yml") || exists(root, "renv.lock"):
		return "r", "settings.yml"
	case exists(root, "settings.yaml"):
		return "r", "settings.yaml"
	case adopt.FrameworkConfig(filepath.Join(root, "config.yml")):
		// Framework 1 was for R only
		return "r", "config.yml"
	}
	return "", ""
}

// UnavailableError means the project's language package has no bootstrap
// yet, or fw cannot run the language at all. NotNeeded means the project
// needs no bootstrap, which is not a failure.
type UnavailableError struct {
	Msg       string
	NotNeeded bool
}

func (e *UnavailableError) Error() string { return e.Msg }

// Bootstrap returns the command that prepares an existing project after a
// clone: the language package's own bootstrap, run in the project's
// environment from its root.
func (f Finder) Bootstrap(p Project) (Command, error) {
	if p.Language == "stata" {
		return Command{}, &UnavailableError{Msg: "Stata projects need no bootstrap. Open Stata in the project folder and run: do " + stataEntry(p.Root) + ". settings.do creates the folders git does not keep.", NotNeeded: true}
	}
	if p.Language == "" {
		return Command{}, &UnavailableError{Msg: "fw cannot tell this project's language (no .framework/spec.json, settings.yml, or pyproject.toml)"}
	}
	if len(p.Runner) == 0 {
		return Command{}, &UnavailableError{Msg: "cannot run this " + p.Language + " project: " + p.Reason}
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
		return Command{}, &UnavailableError{Msg: "unknown language " + p.Language}
	}
	if !f.Probe(probe) {
		pkg := map[string]string{"r": "framework R package", "python": "framework-py package"}[p.Language]
		return Command{}, &UnavailableError{Msg: "fw could not find bootstrap in this project's environment. Either the environment could not be set up, or the " + pkg + " there has no bootstrap yet. To see which, run: " + probe.String()}
	}
	return cmd, nil
}
