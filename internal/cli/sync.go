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

	"github.com/frameworktools/framework-cli/internal/catalog"
	"github.com/frameworktools/framework-cli/internal/cloud"
	"github.com/frameworktools/framework-cli/internal/engine"
	"github.com/frameworktools/framework-cli/internal/skillsync"
	"github.com/spf13/cobra"
)

// fw pull and fw push: everything that syncs with Framework Cloud, in one
// command. That is a project's packages, its skills, and its data registry.
// R and Python projects run their package's pull_all() and push_all().
// Packages aren't managed for Stata projects, so fw syncs their skills and
// data registry itself. fw skills pull|push and fw data pull|push sync one
// part.

// cloudSetup is GET /api/v1/projects/setup.
type cloudSetup struct {
	Revision string           `json:"revision"`
	Language string           `json:"language"`
	Packages []map[string]any `json:"packages"`
	Skills   []map[string]any `json:"skills"`
}

// setupLock is .framework/setup.lock.json: the package list's revision last
// pulled, which push_all() sends as base_revision. Shared with R and Python.
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

const notInProject = "not inside a Framework project (no .framework folder, settings.yml, settings.yaml, settings.do, or Framework config.yml found)"

func newPullCmd(a *app) *cobra.Command {
	var overwrite, yes, dryRun bool
	cmd := &cobra.Command{
		Use:   "pull",
		Short: "Bring the project's packages, skills, and data registry up to date with Framework Cloud",
		Long: "Pulls project packages, skills, and data registry definitions from Framework\n" +
			"Cloud using FW_PROJECT_TOKEN in .env. fw groups incoming changes by section\n" +
			"and asks once before applying them. New packages are added to settings and\n" +
			"installed. Missing skills are added and unedited skills are updated. New data\n" +
			"entries are written into settings; data files never move.\n" +
			"Local files are never deleted, and local edits (version pins, edited skills,\n" +
			"data definitions) are preserved unless --overwrite is passed (edited skills are\n" +
			"backed up first). If one section fails, the remaining sections still apply and\n" +
			"fw reports each section's status.\n\n" +
			"Without a terminal, pass --yes to apply or --dry-run to list changes.\n" +
			"In R and Python projects, fw runs pull_all(). For Stata projects, fw syncs\n" +
			"skills and the data registry. Use fw skills pull or fw data pull for one section.",
		Example: "  fw pull --dry-run\n  fw pull\n  fw pull --yes --overwrite",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			root := a.projectRoot()
			if root == "" {
				return errors.New(notInProject)
			}
			p := a.engines.Detect(root)
			if p.Language != "stata" {
				if a.jsonOut {
					return usageError("--json is for Stata projects; R and Python print their own summary")
				}
				o := engine.SetupOptions{Overwrite: overwrite}
				err := a.handOffSetup(cmd, root, "pull_all()", "Apply these changes?", yes, dryRun, func(m engine.SetupMode) (engine.Command, error) {
					return a.engines.PullAll(p, o, m)
				})
				var old *engine.OldRSkillsError
				if errors.As(err, &old) {
					return a.rDataOnly(cmd, root, old, "fw pull", "pull_data_registry()", "Apply these changes?", yes, dryRun, func(m engine.SetupMode) (engine.Command, error) {
						return a.engines.PullData(p, o, m)
					})
				}
				return err
			}
			return a.pullStata(cmd, root, overwrite, yes, dryRun)
		},
	}
	cmd.Flags().BoolVar(&overwrite, "overwrite", false, "use Framework Cloud's packages, skills, and data definitions over local edits (edited skills are backed up first)")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "apply without asking (required without a terminal)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "list the changes without applying them")
	cmd.Flags().BoolVar(&a.jsonOut, "json", false, jsonHelp+" (Stata projects)")
	return cmd
}

func newPushCmd(a *app) *cobra.Command {
	var remove, force, yes, dryRun bool
	var server string
	cmd := &cobra.Command{
		Use:   "push",
		Short: "Send the project's packages, edited skills, and data registry to Framework Cloud",
		Long: "Pushes the project's package list, locally edited skills, and data registry\n" +
			"to Framework Cloud. fw previews outgoing changes grouped by section and asks\n" +
			"once before sending. New setups, ZIP downloads, and Update on GitHub use the\n" +
			"updated package list and data registry; pushed skill versions apply wherever\n" +
			"each skill is attached. Cloud-only packages and data entries are kept unless\n" +
			"--remove is passed. Data files and file hashes are never uploaded.\n\n" +
			"If a section changed on Framework Cloud since the last pull, fw refuses that\n" +
			"section until you run fw pull or pass --force. Remaining sections still push,\n" +
			"and fw reports each section's status. Edited skills authenticate with fw login;\n" +
			"packages and the data registry use FW_PROJECT_TOKEN.\n\n" +
			"Without a terminal, pass --yes (or --dry-run to preview). In R and Python\n" +
			"projects, fw runs push_all(). For Stata projects, fw syncs skills and the data\n" +
			"registry. Use fw skills push or fw data push for one section.",
		Example: "  fw push --dry-run\n  fw push\n  fw push --yes --remove",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			root := a.projectRoot()
			if root == "" {
				return errors.New(notInProject)
			}
			p := a.engines.Detect(root)
			if p.Language != "stata" {
				if a.jsonOut {
					return usageError("--json is for Stata projects; R and Python print their own summary")
				}
				o := engine.SetupOptions{Remove: remove, Force: force}
				err := a.handOffSetup(cmd, root, "push_all()", "Push these changes?", yes, dryRun, func(m engine.SetupMode) (engine.Command, error) {
					return a.engines.PushAll(p, o, m)
				})
				var old *engine.OldRSkillsError
				if errors.As(err, &old) {
					return a.rDataOnly(cmd, root, old, "fw push", "push_data_registry()", "Push these changes?", yes, dryRun, func(m engine.SetupMode) (engine.Command, error) {
						return a.engines.PushData(p, o, m)
					})
				}
				return err
			}
			return a.pushStata(cmd, root, server, remove, force, yes, dryRun)
		},
	}
	cmd.Flags().StringVar(&server, "url", "", "Framework Cloud server for skills")
	cmd.Flags().BoolVar(&remove, "remove", false, "remove packages and data entries from Framework Cloud that this project doesn't have")
	cmd.Flags().BoolVar(&force, "force", false, "push even when a part changed on Framework Cloud since the last pull")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "push without asking (required without a terminal)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would change without sending anything")
	cmd.Flags().BoolVar(&a.jsonOut, "json", false, jsonHelp+" (Stata projects)")
	return cmd
}

// rDataOnly is fw pull or fw push for an R project whose framework package
// has the old skill sync: fw runs only the data registry part, through the
// package's data function, and refuses packages and skills, which only its
// pull_all() and push_all() handle, together with the old skill sync.
func (a *app) rDataOnly(cmd *cobra.Command, root string, old *engine.OldRSkillsError, command, what, question string, yes, dryRun bool, build func(engine.SetupMode) (engine.Command, error)) error {
	fmt.Fprintln(cmd.ErrOrStderr(), "Packages and Skills: refused. "+old.Reason+". Running only the data registry part.")
	refused := fmt.Sprintf("Packages and Skills (%s)", old.Reason)
	if err := a.handOffSetup(cmd, root, what, question, yes, dryRun, build); err != nil {
		return &exitError{code: 1, err: fmt.Errorf("%s did not finish. Not done: Data registry (%w); %s", command, err, refused)}
	}
	done := "Data registry"
	if dryRun {
		done += " (dry run)"
	}
	return &exitError{code: 1, err: fmt.Errorf("%s did not finish. Done: %s. Not done: %s", command, done, refused)}
}

// handOffSetup runs a language package's sync function, such as pull_all()
// or push_all(). Run by fw, the package has no terminal to ask from, so at
// a terminal fw runs it once as a dry run to list the changes, asks, and
// then runs it with confirm = TRUE. Without a project token it runs
// nothing.
func (a *app) handOffSetup(cmd *cobra.Command, root, what, question string, yes, dryRun bool, build func(engine.SetupMode) (engine.Command, error)) error {
	// The package stops without a project token too, but without saying
	// how to link the project
	if a.projectToken(root) == "" {
		return errNotLinked{}
	}
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

// setupClient is the project's server with its project token, or
// errNotLinked when it has none.
func (a *app) setupClient(root string) (*cloud.Client, error) {
	token := a.projectToken(root)
	if token == "" {
		return nil, errNotLinked{}
	}
	server, err := a.server("", cloud.ProjectScope)
	if err != nil {
		return nil, err
	}
	return cloud.NewClient(server.URL, token), nil
}

// projectTokenFor is the project's token when the project is on the server
// at url, so a skill push can name the project; otherwise empty.
func (a *app) projectTokenFor(root, url string) string {
	client, err := a.setupClient(root)
	if err != nil || strings.TrimRight(client.URL, "/") != strings.TrimRight(url, "/") {
		return ""
	}
	return client.Token
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
		return s, fmt.Errorf("project not read: Framework Cloud not reached (cannot reach %s); nothing was changed", host)
	case status == 404:
		return s, fmt.Errorf("this Framework Cloud server (%s) has no setup endpoint yet; nothing was changed. fw skills pull updates the skills", host)
	case status == 401 || status == 403:
		return s, fmt.Errorf("project not read: Framework Cloud not reached (%s refused the project token); nothing was changed", host)
	default:
		return s, fmt.Errorf("project not read: Framework Cloud not reached (%w); nothing was changed", err)
	}
}

// skillsPlanLine is "1 to add, 2 to update, 1 locally edited (kept)", or
// "" when nothing would change.
func skillsPlanLine(counts map[string]int) string {
	parts := []string{}
	for _, p := range []struct{ key, label string }{
		{"added", "to add"}, {"restored", "to restore"}, {"updated", "to update"}, {"replaced", "to replace (backed up first)"},
		{"edited", "locally edited (kept)"}, {"removed", "no longer in the project's setup (kept)"},
		{"diverged", "diverged (nothing written)"}, {"missing", "missing (every copy gone)"}, {"failed", "failed"},
		{"kept", "kept (another skill's)"}, {"untracked", "untracked (left alone)"},
	} {
		if n := counts[p.key]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, p.label))
		}
	}
	return strings.Join(parts, ", ")
}

// syncPart is one part of fw pull or fw push in a Stata project: its
// changes, and how to apply them. A part whose changes can't be worked out
// has err instead.
type syncPart struct {
	label    string   // Packages, Skills, Data registry
	title    string   // the label, with a revision
	lines    []string // the changes, indented
	warnings []string // what a push affects
	state    string   // shown instead of "up to date" when there are no lines
	quiet    bool     // nothing worth showing
	pending  bool     // something to apply
	err      error
	// failed names what already can't be done, found while planning, such
	// as diverged skills: the command reports it and exits 1, dry run or not
	failed string
	apply  func() error
}

// syncLines is the combined change list, by part. refused describes a part
// whose changes couldn't be worked out.
func syncLines(parts []*syncPart, heading, current, refused string) []string {
	all := true
	for _, p := range parts {
		all = all && p.quiet && p.err == nil
	}
	if all {
		return []string{current}
	}
	lines := []string{heading}
	for _, p := range parts {
		title := p.title
		if title == "" {
			title = p.label
		}
		switch {
		case p.err != nil:
			lines = append(lines, fmt.Sprintf("%s: %s. %s", p.label, refused, partReason(p.err)))
		case len(p.lines) == 0 && len(p.warnings) == 0:
			state := p.state
			if state == "" {
				state = "up to date"
			}
			lines = append(lines, title+": "+state)
		default:
			lines = append(lines, title+":")
			lines = append(lines, p.lines...)
			for _, w := range p.warnings {
				lines = append(lines, "  "+w)
			}
		}
	}
	return lines
}

// partReason is an error as a sentence within a part's line.
func partReason(err error) string {
	msg := strings.TrimSuffix(err.Error(), "; nothing was changed")
	if msg != "" {
		msg = strings.ToUpper(msg[:1]) + msg[1:]
	}
	return strings.TrimSuffix(msg, ".") + "."
}

// syncOutcome is which parts were applied, already in step, or failed.
type syncOutcome struct {
	Applied []string          `json:"applied"`
	Current []string          `json:"current"`
	Failed  map[string]string `json:"failed"`
}

// applyParts applies each part in turn. A part that fails doesn't stop the
// others.
func applyParts(parts []*syncPart) syncOutcome {
	out := syncOutcome{Applied: []string{}, Current: []string{}, Failed: map[string]string{}}
	for _, p := range parts {
		err := p.err
		if err == nil && p.apply != nil {
			err = p.apply()
		}
		switch {
		case err != nil:
			out.Failed[p.label] = strings.TrimSuffix(partReason(err), ".")
		case p.pending:
			out.Applied = append(out.Applied, p.label)
		default:
			out.Current = append(out.Current, p.label)
		}
	}
	return out
}

// partialError names which parts were applied and which weren't.
func partialError(command, done string, parts []*syncPart, o syncOutcome) error {
	applied := "nothing"
	if len(o.Applied) > 0 {
		applied = strings.Join(o.Applied, ", ")
	}
	msg := fmt.Sprintf("%s did not finish. %s: %s. ", command, done, applied)
	if len(o.Current) > 0 {
		msg += "Already in step: " + strings.Join(o.Current, ", ") + ". "
	}
	var reasons []string
	for _, p := range parts {
		if r, ok := o.Failed[p.label]; ok {
			reasons = append(reasons, fmt.Sprintf("%s (%s)", p.label, r))
		}
	}
	return errors.New(msg + "Not " + strings.ToLower(done) + ": " + strings.Join(reasons, "; ") + ".")
}

// partNames are the JSON names of the parts in o's lists.
func partNames(labels []string) []string {
	names := []string{}
	for _, l := range labels {
		names = append(names, strings.ReplaceAll(strings.ToLower(l), " ", "_"))
	}
	return names
}

func failedNames(failed map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range failed {
		out[partNames([]string{k})[0]] = v
	}
	return out
}

// noCatalog is the data registry part of a project without settings.yml,
// which has no data catalog to sync.
func noCatalog(err error) *syncPart {
	if errors.Is(err, catalog.ErrNoSettings) {
		return &syncPart{label: "Data registry", state: "no data catalog (this project has no settings.yml)", quiet: true}
	}
	return nil
}

// syncPullJSON is fw pull --json, for Stata projects, matching pull_all()'s
// result.
type syncPullJSON struct {
	Packages struct {
		Revision string `json:"revision"`
		Managed  bool   `json:"managed"`
	} `json:"packages"`
	Skills       *pullJSON         `json:"skills"`
	DataRegistry *dataPullJSON     `json:"data_registry"`
	Applied      []string          `json:"applied"`
	Failed       map[string]string `json:"failed"`
	DryRun       bool              `json:"dry_run"`
	Message      string            `json:"message"`
}

// pullStata is fw pull for a Stata project: its skills and its data
// registry. Its packages aren't managed.
func (a *app) pullStata(cmd *cobra.Command, root string, overwrite, yes, dryRun bool) error {
	ctx := cmd.Context()
	w := cmd.OutOrStdout()
	if a.jsonOut {
		w = cmd.ErrOrStderr()
	}
	client, err := a.setupClient(root)
	if err != nil {
		return err
	}
	setup, err := fetchSetup(ctx, client)
	if err != nil {
		return err
	}
	out := syncPullJSON{DryRun: dryRun, Applied: []string{}, Failed: map[string]string{}}
	out.Packages.Revision = setup.Revision

	packages := &syncPart{label: "Packages", state: "not managed for Stata projects", quiet: true,
		apply: func() error { return writeSetupLock(root, setup.Revision, client.URL, time.Now()) }}

	skills := &syncPart{label: "Skills"}
	if plan, err := a.stataSkills(ctx, root, overwrite, true, syncCommands); err != nil {
		skills.err = err
	} else if plan.Source != "cloud" {
		skills.err = errors.New(strings.TrimSuffix(plan.Notes[0], "; using the package skills for this project type."))
	} else {
		out.Skills = &plan
		if line := skillsPlanLine(plan.Counts); line != "" {
			skills.lines = append(skills.lines, "  "+line)
		}
		for _, s := range plan.Skills {
			if line := skillRowLine(s); line != "" {
				skills.lines = append(skills.lines, line)
			}
		}
		if len(skills.lines) > 0 {
			for _, n := range plan.Notes {
				skills.lines = append(skills.lines, "  "+n)
			}
		}
		// Diverged and missing skills fail the pull, as fw push's do
		var failed []string
		for _, s := range plan.Skills {
			if s.Status == skillsync.StatusDiverged || s.Status == skillsync.StatusMissing || s.Status == skillsync.StatusFailed {
				failed = append(failed, s.Name+": "+strings.TrimSuffix(s.Reason, "."))
			}
		}
		skills.failed = strings.Join(failed, "; ")
		c := plan.Counts
		skills.pending = c["added"]+c["restored"]+c["updated"]+c["replaced"] > 0
		skills.quiet = len(skills.lines) == 0
		// Always applied, even with no file to write: the lock may still
		// change, as when a folder fw skills copy filled joins its skill
		skills.apply = func() error {
			res, err := a.stataSkills(ctx, root, overwrite, false, syncCommands)
			if err != nil {
				return err
			}
			out.Skills = &res
			if skills.pending {
				fmt.Fprintln(w, "✓ "+res.Message)
			}
			return nil
		}
	}

	data := &syncPart{label: "Data registry"}
	if plan, err := planDataPull(ctx, client, root, overwrite, dryRun, "fw pull"); err != nil {
		if none := noCatalog(err); none != nil {
			data = none
		} else {
			data.err = err
		}
	} else {
		data.title = fmt.Sprintf("Data registry (revision %s)", plan.out.Revision)
		data.lines, data.pending, data.quiet = plan.lines, plan.changes, plan.quiet
		out.DataRegistry = &plan.out
		data.apply = func() error { return applyDataPull(w, client, root, &plan) }
	}

	parts := []*syncPart{packages, skills, data}
	lines := syncLines(parts, "Changes from Framework Cloud:", "Project is up to date with Framework Cloud.", "not read")
	out.Message = strings.Join(lines, "\n")
	fmt.Fprintln(w, out.Message)
	planFailed := false
	for _, p := range parts {
		switch {
		case p.err != nil:
			out.Failed[partNames([]string{p.label})[0]] = strings.TrimSuffix(partReason(p.err), ".")
		case p.failed != "":
			out.Failed[partNames([]string{p.label})[0]] = p.failed
			planFailed = true
		}
	}
	finish := func(err error) error {
		if a.jsonOut {
			if jerr := writeJSON(cmd.OutOrStdout(), out); jerr != nil {
				return jerr
			}
		}
		return err
	}
	// A diverged or missing skill fails the pull, dry run or not, as a
	// skill that can't be pushed fails fw push
	refused := &exitError{code: 1, silent: true, reason: "skill_pull_refused"}
	if dryRun {
		if planFailed {
			return finish(refused)
		}
		return finish(nil)
	}
	if err := a.confirmParts(cmd, parts, yes, "Apply these changes?", "apply"); err != nil {
		return err
	}
	o := applyParts(parts)
	out.Applied, out.Failed = partNames(o.Applied), failedNames(o.Failed)
	if skills.failed != "" {
		if _, ok := out.Failed["skills"]; !ok {
			out.Failed["skills"] = skills.failed
		}
	}
	if len(o.Failed) > 0 {
		return finish(partialError("fw pull", "Applied", parts, o))
	}
	if planFailed {
		return finish(refused)
	}
	return finish(nil)
}

// confirmParts asks once before applying the parts, when any has changes.
func (a *app) confirmParts(cmd *cobra.Command, parts []*syncPart, yes bool, question, verb string) error {
	pending := false
	for _, p := range parts {
		pending = pending || p.pending
	}
	if !pending || yes {
		return nil
	}
	if !a.interactive {
		return usageError("no terminal to confirm from. Rerun with --yes to " + verb + " the changes above, or --dry-run to only list them")
	}
	ok, err := a.askYes(cmd, question)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("canceled; nothing was changed")
	}
	return nil
}

// syncPushJSON is fw push --json, for Stata projects, matching push_all()'s
// result.
type syncPushJSON struct {
	Packages struct {
		Managed bool `json:"managed"`
	} `json:"packages"`
	Skills       []skillsync.SkillStatus `json:"skills"`
	DataRegistry *dataPushJSON           `json:"data_registry"`
	Applied      []string                `json:"applied"`
	Failed       map[string]string       `json:"failed"`
	DryRun       bool                    `json:"dry_run"`
	Message      string                  `json:"message"`
}

// pushStata is fw push for a Stata project: its edited skills and its data
// registry. Its packages aren't managed.
func (a *app) pushStata(cmd *cobra.Command, root, server string, remove, force, yes, dryRun bool) error {
	ctx := cmd.Context()
	w := cmd.OutOrStdout()
	if a.jsonOut {
		w = cmd.ErrOrStderr()
	}
	client, err := a.setupClient(root)
	if err != nil {
		return err
	}
	out := syncPushJSON{DryRun: dryRun, Skills: []skillsync.SkillStatus{}, Applied: []string{}, Failed: map[string]string{}}

	packages := &syncPart{label: "Packages", state: "not managed for Stata projects", quiet: true}
	skills := a.planSkillsPart(ctx, root, server, force, &out.Skills, w)

	data := &syncPart{label: "Data registry"}
	if plan, err := planDataPush(ctx, client, root, remove, force, dryRun, "fw pull", "fw push"); err != nil {
		if none := noCatalog(err); none != nil {
			data = none
		} else {
			data.err = err
		}
	} else {
		data.lines, data.warnings, data.pending, data.quiet = plan.lines, plan.warnings, plan.pending, !plan.pending
		out.DataRegistry = &plan.out
		data.apply = func() error {
			if !plan.pending {
				return nil
			}
			return applyDataPush(w, client, root, &plan)
		}
	}

	parts := []*syncPart{packages, skills, data}
	lines := syncLines(parts, "Changes for Framework Cloud:", "Framework Cloud already matches this project.", "can't push")
	out.Message = strings.Join(lines, "\n")
	fmt.Fprintln(w, out.Message)
	for _, p := range parts {
		switch {
		case p.err != nil:
			out.Failed[partNames([]string{p.label})[0]] = strings.TrimSuffix(partReason(p.err), ".")
		case p.failed != "":
			out.Failed[partNames([]string{p.label})[0]] = p.failed
		}
	}
	finish := func(err error) error {
		if a.jsonOut {
			if jerr := writeJSON(cmd.OutOrStdout(), out); jerr != nil {
				return jerr
			}
		}
		return err
	}
	if dryRun {
		// A skill that can't be pushed, such as a diverged one, fails a dry
		// run too, as in fw skills push
		for _, p := range parts {
			if p.failed != "" {
				return finish(&exitError{code: 1, silent: true, reason: "skill_push_refused"})
			}
		}
		return finish(nil)
	}
	if err := a.confirmParts(cmd, parts, yes, "Push these changes?", "push"); err != nil {
		return err
	}
	o := applyParts(parts)
	out.Applied, out.Failed = partNames(o.Applied), failedNames(o.Failed)
	if len(o.Failed) > 0 {
		return finish(partialError("fw push", "Pushed", parts, o))
	}
	return finish(nil)
}

// syncCommands are the commands fw pull and fw push hints name.
var syncCommands = skillsync.Commands{Pull: "fw pull", Push: "fw push"}

// planSkillsPart is the skills part of fw push: the edited, tracked skills
// to push. Checking them against Framework Cloud needs your login, so it is
// only needed when a skill would be pushed.
func (a *app) planSkillsPart(ctx context.Context, root, server string, force bool, items *[]skillsync.SkillStatus, w io.Writer) *syncPart {
	part := &syncPart{label: "Skills"}
	dirs, err := projectSkillsDirs(root, readSkillsSpec(root))
	if err != nil {
		part.err = err
		return part
	}
	o := skillsync.PushOptions{Root: root, Dirs: dirs, Force: force, Commands: syncCommands}
	pp, err := skillsync.PlanPush(o)
	if err != nil {
		part.err = err
		return part
	}
	// Only a skill with a copy to push is checked against Cloud
	var client *cloud.Client
	if names := pushCandidates(pp); len(names) > 0 {
		srv, err := a.server(server, cloud.ProjectScope)
		if err != nil {
			part.err = err
			return part
		}
		found, ok := a.tokens().Lookup(srv.URL)
		if !ok {
			part.err = fmt.Errorf("not signed in, so edited skills (%s) can't be pushed. Run: %s", strings.Join(names, ", "), loginCommand(srv.URL))
			return part
		}
		client = cloud.NewClient(srv.URL, found.Token)
		o.Remote = skillRemote(ctx, client)
		if pp, err = skillsync.PlanPush(o); err != nil {
			part.err = err
			return part
		}
	}
	rows := pp.Rows()
	*items = rows
	var push, failed []string
	shown := map[string]bool{}
	for _, it := range rows {
		if it.Status == skillsync.StatusFailed || it.Status == skillsync.StatusMissing {
			shown[it.Name+"\x00"+it.Reason] = true
		}
		switch it.Status {
		case skillsync.StatusPushed:
			push = append(push, it.Name)
		case skillsync.StatusRefused:
			part.lines = append(part.lines, "  Refused: "+it.Name+" — "+it.Reason)
		case skillsync.StatusFailed:
			part.lines = append(part.lines, "  Not checked: "+it.Name+" — "+it.Reason)
			failed = append(failed, it.Name+": "+strings.TrimSuffix(it.Reason, "."))
		case skillsync.StatusMissing:
			part.lines = append(part.lines, "  Missing: "+it.Name+" — "+it.Reason)
			failed = append(failed, it.Name+": "+strings.TrimSuffix(it.Reason, "."))
		}
	}
	part.warnings = pushNotes(pp)
	part.failed = strings.Join(failed, "; ")
	if len(push) > 0 {
		part.lines = append([]string{"  Skills to push: " + strings.Join(push, ", ")}, part.lines...)
	}
	part.quiet = len(part.lines) == 0
	for _, it := range rows {
		if it.Status == skillsync.StatusUntracked {
			part.lines = append(part.lines, "  Not tracked: "+it.Name+" — "+it.Reason)
		}
	}
	part.pending = pp.Pending()
	part.apply = func() error {
		if client != nil && pp.Acts() {
			token := a.projectTokenFor(root, client.URL)
			if err := pp.Apply(client.URL, skillSender(ctx, client, token)); err != nil {
				return err
			}
		}
		rows := pp.Rows()
		*items = rows
		var pushed, bad []string
		for _, it := range rows {
			switch it.Status {
			case skillsync.StatusPushed:
				pushed = append(pushed, it.Name)
			case skillsync.StatusRefused:
				bad = append(bad, it.Name+" changed on Cloud since this project pulled it; fw push --force replaces it")
			case skillsync.StatusFailed, skillsync.StatusMissing:
				// What failed while planning was printed then, once
				if shown[it.Name+"\x00"+it.Reason] {
					bad = append(bad, it.Name+" (see above)")
				} else {
					bad = append(bad, it.Name+": "+strings.TrimSuffix(it.Reason, "."))
				}
			}
		}
		if len(pushed) > 0 {
			fmt.Fprintln(w, "✓ Skills pushed: "+strings.Join(pushed, ", "))
		}
		if len(bad) > 0 {
			msg := "not pushed: " + strings.Join(bad, "; ")
			if len(pushed) > 0 {
				msg = "pushed " + strings.Join(pushed, ", ") + "; " + msg
			}
			return errors.New(msg)
		}
		return nil
	}
	return part
}

// pushCandidates names the skills a push would check against Cloud: those
// to push, and those refused for want of Cloud's head.
func pushCandidates(pp *skillsync.PushPlan) []string {
	var names []string
	for _, it := range pp.Rows() {
		if it.Status == skillsync.StatusPushed || it.Status == skillsync.StatusRefused {
			names = append(names, it.Name)
		}
	}
	return names
}
