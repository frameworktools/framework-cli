package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/frameworktools/framework-cli/internal/cloud"
	"github.com/frameworktools/framework-cli/internal/engine"
	"github.com/frameworktools/framework-cli/internal/project"
	"github.com/spf13/cobra"
)

// fw setup pull and push: a project's packages and skills against its setup
// on Framework Cloud. R and Python projects run their package's
// pull_setup() and push_setup(). Packages aren't managed for Stata
// projects, so their setup is their skills.

// cloudSetup is GET /api/v1/projects/setup.
type cloudSetup struct {
	Revision string           `json:"revision"`
	Language string           `json:"language"`
	Packages []map[string]any `json:"packages"`
	Skills   []map[string]any `json:"skills"`
}

// setupLock is .framework/setup.lock.json: the setup revision last pulled,
// which push_setup() sends as base_revision. Shared with R and Python.
type setupLock struct {
	Schema   int    `json:"schema"`
	Revision string `json:"revision"`
	Server   string `json:"server"`
	PulledAt string `json:"pulled_at"`
}

const setupLockFile = ".framework/setup.lock.json"

func writeSetupLock(root, revision, server string, now time.Time) error {
	data, err := json.MarshalIndent(setupLock{Schema: 1, Revision: revision, Server: server, PulledAt: now.UTC().Format(time.RFC3339)}, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(root, filepath.FromSlash(setupLockFile))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { //nolint:gosec // a project folder, shared through git
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644) //nolint:gosec // a project file, shared through git
}

func newSetupCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Bring a project's packages and skills in line with its setup on Framework Cloud",
		Long: "A project's setup is its packages and skills, as Framework Cloud keeps them.\n" +
			"fw setup pull brings this project up to date with it; fw setup push sends this\n" +
			"project's packages and edited skills back. Folders and structure are not part\n" +
			"of the setup.",
	}
	cmd.AddCommand(newSetupPullCmd(a), newSetupPushCmd(a))
	return cmd
}

func newSetupPullCmd(a *app) *cobra.Command {
	var overwrite, yes, dryRun bool
	cmd := &cobra.Command{
		Use:   "pull",
		Short: "Bring the project's packages and skills up to date with Framework Cloud",
		Long: "Reads the project's setup on Framework Cloud with its project token\n" +
			"(FW_PROJECT_TOKEN in .env), lists the changes, and asks before applying them:\n" +
			"packages to add, packages whose settings differ, packages only in this\n" +
			"project, and skills to add or update. Applying adds the packages to the\n" +
			"project's settings and installs them, then pulls the skills as fw skills pull\n" +
			"does. Local packages are never removed, and local edits such as version pins\n" +
			"are kept; --overwrite uses Framework Cloud's settings and skills instead.\n\n" +
			"Without a terminal, pass --yes to apply or --dry-run to only list the changes.\n" +
			"In R and Python projects, fw runs the language package's pull_setup(). Packages\n" +
			"aren't managed for Stata projects, so only their skills change.",
		Example: "  fw setup pull --dry-run\n  fw setup pull\n  fw setup pull --yes --overwrite",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			root := a.projectRoot()
			if root == "" {
				return errors.New("not inside a Framework project (no .framework folder, settings.yml, settings.yaml, settings.do, or Framework config.yml found)")
			}
			p := a.engines.Detect(root)
			if p.Language != "stata" {
				if a.jsonOut {
					return usageError("--json is for Stata projects; R and Python print their own summary")
				}
				o := engine.SetupOptions{Overwrite: overwrite}
				return a.handOffSetup(cmd, root, "pull_setup()", "Apply these changes?", yes, dryRun, func(m engine.SetupMode) (engine.Command, error) {
					return a.engines.PullSetup(p, o, m)
				})
			}
			return a.pullStataSetup(cmd, root, overwrite, yes, dryRun)
		},
	}
	cmd.Flags().BoolVar(&overwrite, "overwrite", false, "use Framework Cloud's package settings and skills over local edits (edited skills are backed up first)")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "apply without asking (required without a terminal)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "list the changes without applying them")
	cmd.Flags().BoolVar(&a.jsonOut, "json", false, jsonHelp+" (Stata projects)")
	return cmd
}

func newSetupPushCmd(a *app) *cobra.Command {
	var remove, force, yes, dryRun bool
	var server string
	cmd := &cobra.Command{
		Use:   "push",
		Short: "Send the project's packages and edited skills to Framework Cloud",
		Long: "Sends this project's package list to its setup on Framework Cloud, after\n" +
			"showing what changes there and asking. New setups, ZIP downloads, and Update\n" +
			"on GitHub use the new setup. Packages only on Framework Cloud are kept unless\n" +
			"you pass --remove. When the setup changed on Framework Cloud since this project\n" +
			"last pulled it, fw refuses: run fw setup pull first, or pass --force to replace\n" +
			"Cloud's newer changes. Edited skills are then pushed as fw skills push does,\n" +
			"with its own warning.\n\n" +
			"Without a terminal, pass --yes. --dry-run sends nothing. In R and Python\n" +
			"projects, fw runs the language package's push_setup(). Packages aren't\n" +
			"managed for Stata projects, so only their skills are pushed.",
		Example: "  fw setup push --dry-run\n  fw setup push\n  fw setup push --yes --remove",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			root := a.projectRoot()
			if root == "" {
				return errors.New("not inside a Framework project (no .framework folder, settings.yml, settings.yaml, settings.do, or Framework config.yml found)")
			}
			p := a.engines.Detect(root)
			if p.Language != "stata" {
				if a.jsonOut {
					return usageError("--json is for Stata projects; R and Python print their own summary")
				}
				o := engine.SetupOptions{Remove: remove, Force: force}
				return a.handOffSetup(cmd, root, "push_setup()", "Push these changes?", yes, dryRun, func(m engine.SetupMode) (engine.Command, error) {
					return a.engines.PushSetup(p, o, m)
				})
			}
			w := cmd.OutOrStdout()
			if a.jsonOut {
				w = cmd.ErrOrStderr()
			}
			note := "Packages aren't managed for Stata projects, so only skills are pushed."
			fmt.Fprintln(w, note)
			client, err := a.skillsClient(server)
			if err != nil {
				return err
			}
			return a.pushSkills(cmd, client, root, force, yes, dryRun, true)
		},
	}
	cmd.Flags().StringVar(&server, "url", "", "Framework Cloud server")
	cmd.Flags().BoolVar(&remove, "remove", false, "remove packages from the Cloud setup that this project doesn't have")
	cmd.Flags().BoolVar(&force, "force", false, "push even when the setup or a skill changed on Framework Cloud since the last pull")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "push without asking (required without a terminal)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would change without sending anything")
	cmd.Flags().BoolVar(&a.jsonOut, "json", false, jsonHelp+" (Stata projects)")
	return cmd
}

// handOffSetup runs the language package's pull_setup() or push_setup().
// Run by fw, the package has no terminal to ask from, so at a terminal fw
// runs it once as a dry run to list the changes, asks, and then runs it
// with confirm = TRUE.
func (a *app) handOffSetup(cmd *cobra.Command, root, what, question string, yes, dryRun bool, build func(engine.SetupMode) (engine.Command, error)) error {
	run := func(mode engine.SetupMode) error {
		command, err := build(mode)
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "Running in %s: %s\n", root, command)
		code, err := a.run(command)
		if err != nil {
			return err
		}
		if code != 0 {
			return &exitError{code: code, err: fmt.Errorf("%s exited with status %d; its last message names the problem", what, code)}
		}
		return nil
	}
	switch {
	case dryRun:
		return run(engine.SetupDryRun)
	case yes:
		return run(engine.SetupConfirmed)
	case !a.interactive:
		return usageError("no terminal to confirm from. Rerun with --dry-run to list the changes, then with --yes to apply them")
	}
	if err := run(engine.SetupDryRun); err != nil {
		return err
	}
	ok, err := a.askYes(cmd, question)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("canceled; nothing was changed")
	}
	return run(engine.SetupConfirmed)
}

// setupClient is the project's server with its project token. A
// non-empty reason says why there is none.
func (a *app) setupClient(root string) (*cloud.Client, string, error) {
	token := a.getenv("FW_PROJECT_TOKEN")
	if token == "" {
		token = project.EnvValue(root, "FW_PROJECT_TOKEN")
	}
	if token == "" {
		return nil, "this project has no FW_PROJECT_TOKEN in .env", nil
	}
	server, err := a.server("", cloud.ProjectScope)
	if err != nil {
		return nil, "", err
	}
	return cloud.NewClient(server.URL, token), "", nil
}

// fetchSetup reads GET /api/v1/projects/setup, explaining failures.
func fetchSetup(ctx context.Context, client *cloud.Client) (cloudSetup, error) {
	var s cloudSetup
	err := client.Do(ctx, "GET", "/api/v1/projects/setup", nil, &s)
	host := cloud.Host(client.URL)
	switch status := cloud.StatusOf(err); {
	case err == nil:
		return s, nil
	case cloud.IsOffline(err):
		return s, fmt.Errorf("setup not read: Framework Cloud not reached (cannot reach %s); nothing was changed", host)
	case status == 404:
		return s, fmt.Errorf("this Framework Cloud server (%s) has no setup endpoint yet; nothing was changed. fw skills pull updates the skills", host)
	case status == 401 || status == 403:
		return s, fmt.Errorf("setup not read: Framework Cloud not reached (%s refused the project token); nothing was changed", host)
	default:
		return s, fmt.Errorf("setup not read: Framework Cloud not reached (%w); nothing was changed", err)
	}
}

// setupPullJSON is fw setup pull --json, for Stata projects.
type setupPullJSON struct {
	Revision string   `json:"revision"`
	DryRun   bool     `json:"dry_run"`
	Applied  bool     `json:"applied"`
	Changes  bool     `json:"changes"`
	Skills   pullJSON `json:"skills"`
}

func (a *app) pullStataSetup(cmd *cobra.Command, root string, overwrite, yes, dryRun bool) error {
	ctx := cmd.Context()
	w := cmd.OutOrStdout()
	if a.jsonOut {
		w = cmd.ErrOrStderr()
	}
	client, reason, err := a.setupClient(root)
	if err != nil {
		return err
	}
	if reason != "" {
		return fmt.Errorf("setup not read: Framework Cloud not reached (%s); nothing was changed", reason)
	}
	setup, err := fetchSetup(ctx, client)
	if err != nil {
		return err
	}
	plan, err := a.stataSkills(ctx, root, overwrite, true)
	if err != nil {
		return err
	}
	if plan.Source != "cloud" {
		return errors.New(strings.TrimSuffix(plan.Notes[0], "; using the package skills for this project type.") + "; nothing was changed")
	}
	out := setupPullJSON{Revision: setup.Revision, DryRun: dryRun, Skills: plan}
	c := plan.Counts
	out.Changes = c["added"]+c["updated"]+c["replaced"] > 0
	fmt.Fprintln(w, "Packages aren't managed for Stata projects, so only skills change.")
	printSetupPlan(w, setup.Revision, plan, out.Changes)

	if dryRun {
		return a.finishSetupPull(cmd, out)
	}
	if out.Changes {
		if !yes {
			if !a.interactive {
				return usageError("no terminal to confirm from. Rerun with --yes to apply the changes above, or --dry-run to only list them")
			}
			ok, err := a.askYes(cmd, "Apply these changes?")
			if err != nil {
				return err
			}
			if !ok {
				return errors.New("canceled; nothing was changed")
			}
		}
		res, err := a.stataSkills(ctx, root, overwrite, false)
		if err != nil {
			return err
		}
		out.Skills, out.Applied = res, true
		if !a.jsonOut {
			printPull(w, res)
		}
	}
	if err := writeSetupLock(root, setup.Revision, client.URL, time.Now()); err != nil {
		return err
	}
	if out.Applied {
		fmt.Fprintf(w, "Setup pulled: revision %s\n", setup.Revision)
	}
	return a.finishSetupPull(cmd, out)
}

func (a *app) finishSetupPull(cmd *cobra.Command, out setupPullJSON) error {
	if a.jsonOut {
		return writeJSON(cmd.OutOrStdout(), out)
	}
	return nil
}

// printSetupPlan lists what a pull would change. Packages aren't managed
// for Stata projects, so their list is skills only.
func printSetupPlan(w io.Writer, revision string, plan pullJSON, changes bool) {
	if !changes {
		fmt.Fprintf(w, "Setup is up to date with Framework Cloud (revision %s).\n", revision)
	} else {
		fmt.Fprintf(w, "Setup changes from Framework Cloud (revision %s):\n", revision)
		fmt.Fprintln(w, "  "+skillsPlanLine(plan.Counts))
	}
	for _, s := range plan.Skills {
		switch s.Status {
		case "edited":
			fmt.Fprintf(w, "  Kept (locally edited): %s — fw setup pull --overwrite replaces it\n", s.Name)
		case "replaced":
			fmt.Fprintf(w, "  Replaced after a backup: %s\n", s.Name)
		case "removed":
			fmt.Fprintf(w, "  No longer in the project's setup (kept): %s\n", s.Name)
		}
	}
}

// skillsPlanLine is "Skills: 1 to add, 2 to update, 1 locally edited (kept)".
func skillsPlanLine(counts map[string]int) string {
	parts := []string{}
	for _, p := range []struct{ key, label string }{
		{"added", "to add"}, {"updated", "to update"}, {"replaced", "to replace (backed up first)"},
		{"edited", "locally edited (kept)"}, {"removed", "no longer in the project's setup (kept)"},
	} {
		if n := counts[p.key]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, p.label))
		}
	}
	if len(parts) == 0 {
		return "Skills: up to date"
	}
	return "Skills: " + strings.Join(parts, ", ")
}
