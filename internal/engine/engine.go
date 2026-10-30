// Package engine builds the commands that hand project creation to each
// language's own package. fw never creates R or Python projects itself.
package engine

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/frameworktools/framework-cli/internal/config"
)

// Command is a program to run, and how to show it to people.
type Command struct {
	Program string
	Args    []string
	// Dir is the working directory. New projects run from the home folder,
	// so another project's .Rprofile or renv in the current folder does not
	// take over.
	Dir string
}

// String renders the command for a POSIX shell.
func (c Command) String() string {
	parts := make([]string, 0, len(c.Args)+1)
	for _, p := range append([]string{c.Program}, c.Args...) {
		parts = append(parts, shellQuote(p))
	}
	return strings.Join(parts, " ")
}

func shellQuote(s string) string {
	if s != "" && strings.IndexFunc(s, needsQuote) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func needsQuote(r rune) bool {
	plain := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_./=:@%+,", r)
	return !plain
}

// MissingError means a language runtime or package is not installed. Steps
// tell the user what to run instead.
type MissingError struct {
	What  string
	Steps []string
}

func (e *MissingError) Error() string {
	return e.What + " Run these instead:\n  " + strings.Join(e.Steps, "\n  ")
}

// Finder locates runtimes. Tests replace its functions.
type Finder struct {
	Getenv   config.Env
	LookPath func(string) (string, error)
	// Probe runs a command and reports whether it exited with status 0.
	Probe func(Command) bool
	Home  string
}

// System returns a Finder for this machine.
func System(getenv config.Env, home string) Finder {
	return Finder{
		Getenv:   getenv,
		LookPath: exec.LookPath,
		Probe: func(c Command) bool {
			cmd := exec.Command(c.Program, c.Args...) // #nosec G204 -- programs fw found itself
			cmd.Dir = c.Dir
			return cmd.Run() == nil
		},
		Home: home,
	}
}

// Request describes the project to create.
type Request struct {
	// LayersFile is a layers.v1 file. When empty, Name, Type, and Dir
	// describe a project made from the user's own defaults.
	LayersFile  string
	Name        string
	Type        string
	Dir         string // absolute target folder
	Interactive bool   // false runs headless
}

// Create returns the command that creates the project in language.
func (f Finder) Create(language string, req Request) (Command, error) {
	switch language {
	case "r":
		return f.createR(req)
	case "python":
		return f.createPython(req)
	default:
		return Command{}, fmt.Errorf("fw cannot hand %q projects to a language package", language)
	}
}

func (f Finder) find(override, name string) string {
	if v := f.Getenv(override); v != "" {
		return v
	}
	path, err := f.LookPath(name)
	if err != nil {
		return ""
	}
	return path
}

func rBool(b bool) string {
	if b {
		return "TRUE"
	}
	return "FALSE"
}

func (f Finder) createR(req Request) (Command, error) {
	var expr string
	var args []string
	if req.LayersFile != "" {
		expr = "a <- commandArgs(TRUE); framework::project_create_from_layers(a[1], a[2], interactive = " + rBool(req.Interactive) + ")"
		args = []string{req.LayersFile, req.Dir}
	} else {
		expr = "a <- commandArgs(TRUE); framework::new(name = a[1], location = a[2], type = if (nzchar(a[3])) a[3] else NULL, browse = FALSE)"
		args = []string{req.Name, req.Dir, req.Type}
	}

	rscript := f.find("FW_RSCRIPT", "Rscript")
	command := func(program string) Command {
		// Rscript passes everything after -e EXPR to commandArgs(TRUE). An
		// explicit --args would arrive as the first argument.
		return Command{Program: program, Args: append([]string{"-e", expr}, args...), Dir: f.Home}
	}
	install := `Rscript -e 'install.packages("pak"); pak::pak("frameworktools/framework")'`
	if rscript == "" {
		return Command{}, &MissingError{
			What:  "R is not installed, or Rscript is not on your PATH.",
			Steps: []string{"# install R from https://cloud.r-project.org", install, command("Rscript").String()},
		}
	}
	check := `if (!requireNamespace("framework", quietly = TRUE) || !exists("project_create_from_layers", envir = asNamespace("framework"))) quit(status = 3)`
	if !f.Probe(Command{Program: rscript, Args: []string{"-e", check}, Dir: f.Home}) {
		return Command{}, &MissingError{
			What:  "The framework R package (version 2 or later) is not installed.",
			Steps: []string{install, command(rscript).String()},
		}
	}
	return command(rscript), nil
}

func (f Finder) createPython(req Request) (Command, error) {
	var moduleArgs []string
	if req.LayersFile != "" {
		moduleArgs = []string{"-m", "framework", "create", "--from-layers", req.LayersFile, "--path", req.Dir}
	} else {
		// framework.new(): the user's own defaults apply, as in R
		moduleArgs = []string{"-m", "framework", "create", "--name", req.Name, "--path", req.Dir}
		if req.Type != "" {
			moduleArgs = append(moduleArgs, "--type", req.Type)
		}
	}
	if req.Interactive {
		moduleArgs = append(moduleArgs, "--interactive")
	}
	pkg := f.Getenv("FW_PYTHON_PACKAGE")
	if pkg == "" {
		pkg = "framework-py"
	}
	viaUV := func(uv string) Command {
		return Command{Program: uv, Args: append([]string{"run", "--no-project", "--with", pkg, "python"}, moduleArgs...), Dir: f.Home}
	}

	if python := f.Getenv("FW_PYTHON"); python != "" {
		return Command{Program: python, Args: moduleArgs, Dir: f.Home}, nil
	}
	if uv := f.find("FW_UV", "uv"); uv != "" {
		return viaUV(uv), nil
	}
	if python := f.find("", "python3"); python != "" {
		check := Command{Program: python, Args: []string{"-c", "import framework; framework.project_create_from_layers; framework.new"}, Dir: f.Home}
		if f.Probe(check) {
			return Command{Program: python, Args: moduleArgs, Dir: f.Home}, nil
		}
	}
	return Command{}, &MissingError{
		What: "Neither uv nor a Python with the framework-py package was found.",
		Steps: []string{
			"# install uv from https://docs.astral.sh/uv/",
			viaUV("uv").String(),
		},
	}
}
