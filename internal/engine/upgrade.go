package engine

import "strings"

// UpgradeOptions are fw project upgrade's flags. Without Apply, the
// language package only prints its plan.
type UpgradeOptions struct {
	Apply    bool
	NoBackup bool
	// Type changes the project's type (project, project_sensitive, course,
	// presentation, or bare); empty keeps it.
	Type string
	// MoveSettings moves settings keys outside the environment sections
	// into settings/<key>.yml.
	MoveSettings bool
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
		var takes []string
		if o.NoBackup {
			expr += ", backup = FALSE"
			takes = append(takes, "backup")
		}
		if o.Type != "" {
			// One of a fixed list of type names, so it can go in the code
			expr += `, type = "` + o.Type + `"`
			takes = append(takes, "type")
		}
		if o.MoveSettings {
			expr += ", move_settings = TRUE"
			takes = append(takes, "move_settings")
		}
		need := `!exists("upgrade", envir = asNamespace("framework"), inherits = FALSE)`
		if len(takes) > 0 {
			need += ` || !all(c("` + strings.Join(takes, `", "`) + `") %in% names(formals(get("upgrade", envir = asNamespace("framework")))))`
		}
		probe := run(p.Runner, "-e", `if (!requireNamespace("framework", quietly = TRUE)) quit(status = 3); if (`+need+`) quit(status = 4)`)
		if !f.Probe(probe) {
			what := "upgrade()"
			if len(takes) > 0 {
				what = "upgrade() that takes " + strings.Join(takes, " and ")
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
	var flags []string
	if o.Type != "" {
		args = append(args, "--type", o.Type)
		flags = append(flags, "--type")
	}
	if o.MoveSettings {
		args = append(args, "--move-settings")
		flags = append(flags, "--move-settings")
	}
	missing := &UnavailableError{Msg: "framework-py has no upgrade command yet: 0.1.0.dev12 and earlier don't. Update framework-py in the project's environment " +
		"(uv add -U framework-py, or pip install -U framework-py), then run fw project upgrade again. Nothing was changed."}
	// --type and --move-settings need a framework-py whose upgrade lists them
	help := []string{"-m", "framework", "upgrade", "--help"}
	if len(flags) > 0 {
		help = []string{"-c", pythonHelpLists("upgrade", flags)}
		missing.Msg = "the framework-py that fw runs has no upgrade that takes " + strings.Join(flags, " and ") + " yet: they came after framework-py 0.1.0.dev13. " +
			"Update framework-py in the project's environment (uv add -U framework-py, or pip install -U framework-py), then run fw project upgrade again. Nothing was changed."
	}
	if len(p.Runner) > 0 && f.Probe(run(p.Runner, help...)) {
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
	probe, err := f.python(help)
	if err != nil || !f.Probe(probe) {
		return Command{}, missing
	}
	return command, nil
}

// PullSkills returns the command that runs the language package's own
// pull_skills() in an R or Python project, from its root:
// framework::pull_skills() in R and framework.pull_skills() in Python. The
// package ships the project's built-in skills, so it does the pull.
func (f Finder) PullSkills(p Project, overwrite, dryRun bool) (Command, error) {
	switch p.Language {
	case "r", "python":
	case "":
		return Command{}, &UnavailableError{Msg: "fw cannot tell this project's language (no .framework/spec.json, settings.yml, or pyproject.toml)"}
	default:
		return Command{}, &UnavailableError{Msg: "fw pulls " + p.Language + " projects' skills itself"}
	}
	if len(p.Runner) == 0 && p.Language == "r" {
		return Command{}, &UnavailableError{Msg: "cannot run this R project: " + p.Reason}
	}
	run := func(runner []string, args ...string) Command {
		return Command{Program: runner[0], Args: append(append([]string{}, runner[1:]...), args...), Dir: p.Root}
	}
	if p.Language == "r" {
		probe := run(p.Runner, "-e", `if (!requireNamespace("framework", quietly = TRUE)) quit(status = 3); if (`+rHas("pull_skills")+`) quit(status = 4)`)
		if !f.Probe(probe) {
			return Command{}, &UnavailableError{Msg: "the framework R package in this project's environment is missing or has no pull_skills() yet. " +
				"Update it with pak::pak(\"frameworktools/framework\") (or renv::update(\"framework\") in an renv project), then run fw skills pull again. Nothing was changed. " +
				"To check the environment, run: " + probe.String()}
		}
		return run(p.Runner, "-e", "invisible(framework::pull_skills(overwrite = "+rBool(overwrite)+", dry_run = "+rBool(dryRun)+"))"), nil
	}
	pyBool := func(b bool) string {
		if b {
			return "True"
		}
		return "False"
	}
	code := "import framework; framework.pull_skills(overwrite=" + pyBool(overwrite) + ", dry_run=" + pyBool(dryRun) + ")"
	check := []string{"-c", "import framework; framework.pull_skills"}
	missing := &UnavailableError{Msg: "framework-py has no pull_skills() yet. Update framework-py in the project's environment " +
		"(uv add -U framework-py, or pip install -U framework-py), then run fw skills pull again. Nothing was changed."}
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
