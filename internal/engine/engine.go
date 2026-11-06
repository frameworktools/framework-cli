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
	// Workflow and Offline go to the language's new(). Workflow is quarto,
	// jupyter, or scripts. Offline builds from saved defaults or built-ins
	// without contacting Framework Cloud.
	Workflow string
	Offline  bool
}

// MinPython is the first framework-py release whose new() takes workflow
// and offline, and that has adopt.
const MinPython = "0.1.0.dev6"

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
	var expr, need, lacks string
	var args []string
	if req.LayersFile != "" {
		expr = "a <- commandArgs(TRUE); framework::project_create_from_layers(a[1], a[2], interactive = " + rBool(req.Interactive) + ")"
		args = []string{req.LayersFile, req.Dir}
		need, lacks = rHas("project_create_from_layers"), "cannot create projects from layers"
	} else {
		expr = "a <- commandArgs(TRUE); framework::new(name = a[1], location = a[2], type = if (nzchar(a[3])) a[3] else NULL, browse = FALSE"
		args = []string{req.Name, req.Dir, req.Type}
		need, lacks = rHas("new"), "has no new()"
		if req.Offline || req.Workflow != "" {
			expr += ", workflow = if (nzchar(a[4])) a[4] else NULL, offline = as.logical(a[5])"
			args = append(args, req.Workflow, rBool(req.Offline))
			need = rHas("new", "new_project") + ` || !all(c("offline", "workflow") %in% names(formals(get("new_project", envir = asNamespace("framework")))))`
			lacks = "does not take --offline or --workflow"
		}
		expr += ")"
	}
	return f.rCommand(expr, args, need, lacks)
}

// AdoptR returns the command that hands an existing folder to the framework
// R package's adopt(), accepting its proposed mapping. R reviews a mapping
// only in an interactive R session, which Rscript is not.
func (f Finder) AdoptR(dir string) (Command, error) {
	return f.rCommand("a <- commandArgs(TRUE); framework::adopt(a[1], yes = TRUE)", []string{dir}, rHas("adopt"), "has no adopt()")
}

// rHas is an R condition that is true when the framework package lacks one
// of the functions. fw checks what the installed package can do, not its
// version number.
func rHas(functions ...string) string {
	quoted := make([]string, len(functions))
	for i, name := range functions {
		quoted[i] = `"` + name + `"`
	}
	return `!all(vapply(c(` + strings.Join(quoted, ", ") + `), exists, logical(1), envir = asNamespace("framework"), inherits = FALSE))`
}

// rCommand finds Rscript and checks that the framework package is installed
// and that need (an R condition, true when something is missing) is false.
func (f Finder) rCommand(expr string, args []string, need, lacks string) (Command, error) {
	command := func(program string) Command {
		// Rscript passes everything after -e EXPR to commandArgs(TRUE). An
		// explicit --args would arrive as the first argument.
		return Command{Program: program, Args: append([]string{"-e", expr}, args...), Dir: f.Home}
	}
	install := `Rscript -e 'install.packages("pak"); pak::pak("frameworktools/framework")'`
	if set := f.Getenv("FW_RSCRIPT"); set != "" {
		if _, err := f.LookPath(set); err != nil {
			return Command{}, &MissingError{
				What:  "FW_RSCRIPT is set to " + set + ", which is not a program fw can run. Fix or unset it.",
				Steps: []string{command("Rscript").String()},
			}
		}
	}
	rscript := f.find("FW_RSCRIPT", "Rscript")
	if rscript == "" {
		return Command{}, &MissingError{
			What:  "R is not installed, or Rscript is not on your PATH.",
			Steps: []string{"# install R from https://cloud.r-project.org", install, command("Rscript").String()},
		}
	}
	installed := `if (!requireNamespace("framework", quietly = TRUE)) quit(status = 3)`
	if !f.Probe(Command{Program: rscript, Args: []string{"-e", installed + "; if (" + need + ") quit(status = 4)"}, Dir: f.Home}) {
		what := "The framework R package is not installed."
		if f.Probe(Command{Program: rscript, Args: []string{"-e", installed}, Dir: f.Home}) {
			what = "The framework R package here " + lacks + ". Update it."
		}
		return Command{}, &MissingError{What: what, Steps: []string{install, command(rscript).String()}}
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
		if req.Workflow != "" {
			moduleArgs = append(moduleArgs, "--workflow", req.Workflow)
		}
		if req.Offline {
			moduleArgs = append(moduleArgs, "--offline")
		}
	}
	if req.Interactive {
		moduleArgs = append(moduleArgs, "--interactive")
	}
	return f.python(moduleArgs)
}

// AdoptPython returns the command that hands an existing folder to
// framework-py's adopt.
func (f Finder) AdoptPython(dir string, yes bool) (Command, error) {
	args := []string{"-m", "framework", "adopt", "--path", dir}
	if yes {
		args = append(args, "--yes")
	}
	return f.python(args)
}

// python runs framework-py's module with args: through FW_PYTHON, uv with
// framework-py MinPython or later, or a python3 whose framework-py has
// adopt and new()'s offline option.
func (f Finder) python(moduleArgs []string) (Command, error) {
	pkg := f.Getenv("FW_PYTHON_PACKAGE")
	if pkg == "" {
		pkg = "framework-py>=" + MinPython
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
		check := Command{Program: python, Args: []string{"-c",
			"import framework, inspect; framework.project_create_from_layers; framework.adopt; assert 'offline' in inspect.signature(framework.new).parameters"}, Dir: f.Home}
		if f.Probe(check) {
			return Command{Program: python, Args: moduleArgs, Dir: f.Home}, nil
		}
	}
	return Command{}, &MissingError{
		What: "Neither uv nor a Python with framework-py " + MinPython + " or later was found.",
		Steps: []string{
			"# install uv from https://docs.astral.sh/uv/",
			viaUV("uv").String(),
		},
	}
}
