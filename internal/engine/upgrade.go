package engine

// UpgradeOptions are fw project upgrade's flags. Without Apply, the
// language package only prints its plan.
type UpgradeOptions struct {
	Apply    bool
	NoBackup bool
}

// Upgrade returns the command that brings an R or Python project to the
// current layout with the language package's own upgrade, run in the
// project's environment from its root: framework::upgrade(dry_run = ...)
// in R, and python -m framework upgrade [--apply] [--no-backup] in Python.
// When the project's environment lacks it, fw says to update the package.
// A Python project without an environment of its own runs framework-py as
// fw adopt does.
func (f Finder) Upgrade(p Project, o UpgradeOptions) (Command, error) {
	switch p.Language {
	case "r", "python":
	case "":
		return Command{}, &UnavailableError{Msg: "fw cannot tell this project's language (no .framework/spec.json, settings.yml, or pyproject.toml)"}
	default:
		return Command{}, &UnavailableError{Msg: "fw upgrades " + p.Language + " projects itself"}
	}
	if len(p.Runner) == 0 && p.Language == "r" {
		return Command{}, &UnavailableError{Msg: "cannot run this R project: " + p.Reason}
	}
	run := func(runner []string, args ...string) Command {
		return Command{Program: runner[0], Args: append(append([]string{}, runner[1:]...), args...), Dir: p.Root}
	}
	if p.Language == "r" {
		expr := "framework::upgrade(dry_run = " + rBool(!o.Apply)
		need := `!exists("upgrade", envir = asNamespace("framework"), inherits = FALSE)`
		if o.NoBackup {
			expr += ", backup = FALSE"
			need += ` || !("backup" %in% names(formals(get("upgrade", envir = asNamespace("framework")))))`
		}
		probe := run(p.Runner, "-e", `if (!requireNamespace("framework", quietly = TRUE)) quit(status = 3); if (`+need+`) quit(status = 4)`)
		if !f.Probe(probe) {
			what := "upgrade()"
			if o.NoBackup {
				what = "upgrade() that takes backup = FALSE"
			}
			return Command{}, &UnavailableError{Msg: "the framework R package in this project's environment is missing or has no " + what + " yet: framework 2.0.1 and earlier don't. " +
				"Update it with pak::pak(\"frameworktools/framework\") (or renv::update(\"framework\") in an renv project), then run fw project upgrade again. Nothing was changed. " +
				"To check the environment, run: " + probe.String()}
		}
		return run(p.Runner, "-e", expr+")"), nil
	}

	args := []string{"-m", "framework", "upgrade"}
	if o.Apply {
		args = append(args, "--apply")
	}
	if o.NoBackup {
		args = append(args, "--no-backup")
	}
	missing := &UnavailableError{Msg: "framework-py has no upgrade command yet: 0.1.0.dev12 and earlier don't. Update framework-py in the project's environment " +
		"(uv add -U framework-py, or pip install -U framework-py), then run fw project upgrade again. Nothing was changed."}
	if len(p.Runner) > 0 && f.Probe(run(p.Runner, "-m", "framework", "upgrade", "--help")) {
		return run(p.Runner, args...), nil
	}
	if exists(p.Root, "pyproject.toml") || exists(p.Root, "uv.lock") {
		return Command{}, missing
	}
	// No environment of its own, such as an adopted Django app: run
	// framework-py as fw adopt does.
	command, err := f.python(args)
	if err != nil {
		return Command{}, err
	}
	command.Dir = p.Root
	probe, err := f.python([]string{"-m", "framework", "upgrade", "--help"})
	if err != nil || !f.Probe(probe) {
		return Command{}, missing
	}
	return command, nil
}
