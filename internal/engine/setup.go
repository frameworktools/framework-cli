package engine

import "strings"

// SetupMode is how fw runs the language package's pull_setup() or
// push_setup(): only listing the changes, or applying them without a
// prompt of its own (fw asked already, or --yes was given).
type SetupMode int

const (
	SetupDryRun SetupMode = iota
	SetupConfirmed
)

// SetupOptions are fw setup pull's and push's flags.
type SetupOptions struct {
	Overwrite bool // pull
	Remove    bool // push
	Force     bool // push
}

// PullSetup returns the command that runs framework::pull_setup() in R or
// framework.pull_setup() in Python, from the project's root.
func (f Finder) PullSetup(p Project, o SetupOptions, mode SetupMode) (Command, error) {
	r, py := []string{"overwrite = " + rBool(o.Overwrite)}, []string{"overwrite=" + pyBool(o.Overwrite)}
	return f.packageCall(p, "pull_setup", "fw setup pull", setupArgs(r, py, mode))
}

// PushSetup returns the command that runs framework::push_setup() in R or
// framework.push_setup() in Python, from the project's root.
func (f Finder) PushSetup(p Project, o SetupOptions, mode SetupMode) (Command, error) {
	r := []string{"remove = " + rBool(o.Remove), "force = " + rBool(o.Force)}
	py := []string{"remove=" + pyBool(o.Remove), "force=" + pyBool(o.Force)}
	return f.packageCall(p, "push_setup", "fw setup push", setupArgs(r, py, mode))
}

// setupArgs adds dry_run = TRUE, or confirm = TRUE, to each language's
// arguments.
func setupArgs(r, py []string, mode SetupMode) [2]string {
	if mode == SetupDryRun {
		r, py = append(r, "dry_run = TRUE"), append(py, "dry_run=True")
	} else {
		r, py = append(r, "confirm = TRUE"), append(py, "confirm=True")
	}
	return [2]string{strings.Join(r, ", "), strings.Join(py, ", ")}
}

func pyBool(b bool) string {
	if b {
		return "True"
	}
	return "False"
}

// packageCall runs one function of the language package, framework::fn(args)
// in R and framework.fn(args) in Python, in the project's environment. When
// the package lacks it, fw says to update the package; again is the fw
// command to rerun after.
func (f Finder) packageCall(p Project, fn, again string, args [2]string) (Command, error) {
	switch p.Language {
	case "r", "python":
	case "":
		return Command{}, &UnavailableError{Msg: "fw cannot tell this project's language (no .framework/spec.json, settings.yml, or pyproject.toml)"}
	default:
		return Command{}, &UnavailableError{Msg: "fw handles " + p.Language + " projects itself"}
	}
	if len(p.Runner) == 0 && p.Language == "r" {
		return Command{}, &UnavailableError{Msg: "cannot run this R project: " + p.Reason}
	}
	run := func(runner []string, a ...string) Command {
		return Command{Program: runner[0], Args: append(append([]string{}, runner[1:]...), a...), Dir: p.Root}
	}
	if p.Language == "r" {
		probe := run(p.Runner, "-e", `if (!requireNamespace("framework", quietly = TRUE)) quit(status = 3); if (`+rHas(fn)+`) quit(status = 4)`)
		if !f.Probe(probe) {
			return Command{}, &UnavailableError{Msg: "the framework R package in this project's environment is missing or has no " + fn + "() yet. " +
				"Update it with pak::pak(\"frameworktools/framework\") (or renv::update(\"framework\") in an renv project), then run " + again + " again. Nothing was changed. " +
				"To check the environment, run: " + probe.String()}
		}
		return run(p.Runner, "-e", "invisible(framework::"+fn+"("+args[0]+"))"), nil
	}
	code := "import framework; framework." + fn + "(" + args[1] + ")"
	check := []string{"-c", "import framework; framework." + fn}
	missing := &UnavailableError{Msg: "framework-py has no " + fn + "() yet. Update framework-py in the project's environment " +
		"(uv add -U framework-py, or pip install -U framework-py), then run " + again + " again. Nothing was changed."}
	if len(p.Runner) > 0 && f.Probe(run(p.Runner, check...)) {
		return run(p.Runner, "-c", code), nil
	}
	if exists(p.Root, "pyproject.toml") || exists(p.Root, "uv.lock") {
		return Command{}, missing
	}
	command, err := f.python([]string{"-c", code})
	if err != nil {
		return Command{}, err
	}
	command.Dir = p.Root
	probe, err := f.python(check)
	if err != nil || !f.Probe(probe) {
		return Command{}, missing
	}
	return command, nil
}
