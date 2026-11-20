package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/frameworktools/framework-cli/internal/adopt"
	"github.com/frameworktools/framework-cli/internal/engine"
	"github.com/frameworktools/framework-cli/internal/stata"
)

func newProjectUpgradeCmd(a *app) *cobra.Command {
	var o engine.UpgradeOptions
	cmd := &cobra.Command{
		Use:   "upgrade",
		Short: "Bring an older Framework project to the current layout",
		Long: "Brings a project made by an older Framework, or adopted by one, to the\n" +
			"current layout. Without --apply it only prints what would change.\n\n" +
			"R projects run framework::upgrade(dry_run = TRUE), or dry_run = FALSE with\n" +
			"--apply, and Python projects run python -m framework upgrade, in the\n" +
			"project's own environment. When the package there has no upgrade yet, fw\n" +
			"says to update it and changes nothing.\n\n" +
			"fw upgrades Stata projects itself. It records .framework/spec.json from\n" +
			"settings.do when missing, writes settings.yml when absent,\n" +
			"adds the settings.do header that references settings.yml, updates master.do\n" +
			"to check for framework-stata 0.2.0+ and run fw_scaffold when framework-stata\n" +
			"0.3.0+ is installed (falling back to fw_project), creates missing gitignored\n" +
			"private folders, and appends missing .gitignore rules (omitting !... negations\n" +
			"when existing rules are present). The spec records tools.quarto when the\n" +
			"project has a _quarto.yml, _quarto.yaml, or .qmd file at most three\n" +
			"folders deep. It never deletes or modifies existing lines: it\n" +
			"only creates files, inserts lines, and appends. Modified files are backed\n" +
			"up to .framework/backups/<timestamp>/ first unless --no-backup is passed.\n" +
			"Running it again changes nothing.\n\n" +
			"--type changes the project's type, as R's upgrade(type =) does. To\n" +
			"project_sensitive it turns on security: sensitive and adds the Privacy\n" +
			"Sensitive layout's private and public folders the project lacks, keeping\n" +
			"its own; the .gitignore rules, the private folders, and the guard that\n" +
			"keeps Claude Code out of them follow. A sensitive project changed to\n" +
			"another type stays sensitive. In Stata, fw adds the new folders to\n" +
			"settings.yml and settings.do. --move-settings moves settings keys outside\n" +
			"the environment sections into settings/<key>.yml (R and Python). R\n" +
			"passes them as upgrade(type =, move_settings = TRUE), which needs a\n" +
			"framework package newer than 2.0.1, and Python as --type and\n" +
			"--move-settings, which need a framework-py newer than 0.1.0.dev13; fw\n" +
			"says when the package lacks them.",
		Example: "  fw project upgrade\n  fw project upgrade --apply\n  fw project upgrade --apply --no-backup\n" +
			"  fw project upgrade --type project_sensitive\n  fw project upgrade --apply --move-settings",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			root := a.upgradeRoot()
			if root == "" {
				return errors.New("not inside a Framework project (no .framework folder, settings.yml, settings.yaml, settings.do, or Framework config.yml found). To make this folder one, run fw adopt")
			}
			if o.Type != "" && !contains(adoptTypes, o.Type) {
				return usageError("--type takes project, project_sensitive, course, presentation, or bare")
			}
			p := a.engines.Detect(root)
			if p.Language == "stata" {
				if o.Type != "" && !contains(stata.Types, o.Type) {
					return usageError("Stata projects take --type project, project_sensitive, or bare")
				}
				if o.MoveSettings {
					return usageError("--move-settings is for R and Python projects; a Stata project's settings.yml has no environment sections to move keys out of")
				}
				return a.upgradeStata(cmd, root, o)
			}
			if a.jsonOut {
				return usageError("--json is for Stata projects; R and Python print their own plan")
			}
			command, err := a.engines.Upgrade(p, o)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "Running in %s: %s\n", root, command)
			code, err := a.run(command)
			if err != nil {
				return err
			}
			if code != 0 {
				return &exitError{code: code, err: fmt.Errorf("the upgrade exited with status %d; its last message names the problem", code)}
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.BoolVar(&o.Apply, "apply", false, "make the changes (default: print them only)")
	f.BoolVar(&o.NoBackup, "no-backup", false, "don't copy changed files to .framework/backups first")
	f.StringVar(&o.Type, "type", "", "change the project's type: project, project_sensitive, course, presentation, or bare (Stata: project, project_sensitive, or bare)")
	f.BoolVar(&o.MoveSettings, "move-settings", false, "move settings keys outside the environment sections into settings/<key>.yml (R and Python)")
	f.BoolVar(&a.jsonOut, "json", false, jsonHelp+" (Stata projects)")
	return cmd
}

// upgradeRoot finds the project to upgrade: the nearest Framework project,
// or the current folder when it holds an older project's settings, which
// fw's other commands don't recognize.
func (a *app) upgradeRoot() string {
	if root := a.projectRoot(); root != "" {
		return root
	}
	for _, rel := range []string{"settings.yaml", "settings.do"} {
		if _, err := os.Stat(filepath.Join(a.cwd, rel)); err == nil {
			return a.cwd
		}
	}
	if adopt.FrameworkConfig(filepath.Join(a.cwd, "config.yml")) {
		return a.cwd
	}
	return ""
}

// upgradeStata plans, and with --apply makes, a Stata project's upgrade.
func (a *app) upgradeStata(cmd *cobra.Command, root string, o engine.UpgradeOptions) error {
	plan, err := stata.PlanUpgrade(root, version, stata.UpgradeOptions{Type: o.Type})
	if err != nil {
		return err
	}
	backup := ""
	if o.Apply && len(plan.Changes) > 0 {
		if backup, err = stata.ApplyUpgrade(root, plan, !o.NoBackup, time.Now()); err != nil {
			return err
		}
	}
	if a.jsonOut {
		return writeJSON(cmd.OutOrStdout(), map[string]any{"path": root, "language": "stata", "applied": o.Apply && len(plan.Changes) > 0,
			"changes": plan.Changes, "notes": plan.Notes, "backup": backup})
	}
	out := cmd.OutOrStdout()
	switch {
	case len(plan.Changes) == 0:
		fmt.Fprintln(out, "Nothing to upgrade: this Stata project has the current layout.")
	case o.Apply:
		fmt.Fprintln(out, "Upgraded this Stata project:")
	default:
		fmt.Fprintln(out, "fw project upgrade would change this Stata project as follows:")
	}
	for _, c := range plan.Changes {
		fmt.Fprintf(out, "  %-7s %-21s %s\n", c.Action, c.File, c.Summary)
		for _, l := range c.Lines {
			fmt.Fprintf(out, "          + %s\n", strings.TrimRight(l, " "))
		}
	}
	for _, n := range plan.Notes {
		fmt.Fprintf(out, "Note: %s\n", n)
	}
	if len(plan.Changes) == 0 {
		return nil
	}
	if !o.Apply {
		fmt.Fprintln(out, "Nothing was changed. To make these changes, run: fw project upgrade --apply")
		return nil
	}
	if backup != "" {
		fmt.Fprintf(out, "The files as they were are in %s.\n", backup)
	}
	fmt.Fprintln(out, "Next: review the changes (git diff), then commit them.")
	if a.projectToken(root) == "" {
		fmt.Fprintln(out, notLinked("")+createHint)
	}
	return nil
}
