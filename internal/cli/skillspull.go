package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/frameworktools/framework-cli/internal/cloud"
	"github.com/frameworktools/framework-cli/internal/engine"
	"github.com/frameworktools/framework-cli/internal/project"
	"github.com/frameworktools/framework-cli/internal/skillsync"
	"github.com/frameworktools/framework-cli/internal/spec"
	"github.com/frameworktools/framework-cli/internal/stata"
	"github.com/spf13/cobra"
)

// skillsSpec is what skills commands read from .framework/spec.json.
type skillsSpec struct {
	Type     string `json:"type"`
	Security struct {
		Sensitive bool `json:"sensitive"`
	} `json:"security"`
	AI struct {
		Assistants map[string]bool `json:"assistants"`
	} `json:"ai"`
}

func readSkillsSpec(root string) skillsSpec {
	var s skillsSpec
	if data, err := os.ReadFile(filepath.Join(root, ".framework", "spec.json")); err == nil {
		_ = json.Unmarshal(data, &s)
	}
	if s.Type == "" {
		s.Type = "project"
	}
	return s
}

// projectSkillsDirs are the skills folders the project's assistants read
// (.agents/skills when none is set), then .agents/skills and .claude/skills
// when they exist. The first is the one AGENTS.md lists.
func projectSkillsDirs(root string, s skillsSpec) []string {
	var names []string
	for name, on := range s.AI.Assistants {
		if on && stata.AssistantSkillsDir(name) != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	var dirs []string
	add := func(dir string) {
		if !contains(dirs, dir) {
			dirs = append(dirs, dir)
		}
	}
	for _, name := range names {
		add(stata.AssistantSkillsDir(name))
	}
	if len(dirs) == 0 {
		add(".agents/skills")
	}
	for _, dir := range []string{".agents/skills", ".claude/skills"} {
		if info, err := os.Stat(filepath.Join(root, filepath.FromSlash(dir))); err == nil && info.IsDir() {
			add(dir)
		}
	}
	return dirs
}

// pullJSON is fw skills pull --json.
type pullJSON struct {
	Source  string                  `json:"source"`
	DryRun  bool                    `json:"dry_run"`
	Skills  []skillsync.SkillStatus `json:"skills"`
	Counts  map[string]int          `json:"counts"`
	Message string                  `json:"message"`
	Notes   []string                `json:"notes"`
	Backups []string                `json:"backups"`
}

func newSkillsPullCmd(a *app) *cobra.Command {
	var overwrite, dryRun bool
	cmd := &cobra.Command{
		Use:   "pull",
		Short: "Bring the project's skills up to date with its setup on Framework Cloud",
		Long: "Reads the project's current setup on Framework Cloud with its project token\n" +
			"(FW_PROJECT_TOKEN in .env) and brings its skills up to date: it adds missing\n" +
			"skills and updates unedited ones to their latest versions. When you are logged\n" +
			"in, Cloud skills come at their newest version. Skills you edited are kept and\n" +
			"listed; --overwrite replaces them, moving the old copies to\n" +
			".framework/skills-backups/ first. A skill no longer in the setup is never\n" +
			"deleted, only listed. The Skills table in AGENTS.md is refreshed.\n\n" +
			"Without a project token, or when Framework Cloud can't be reached, fw uses the\n" +
			"package skills for the project's type and says so. In R and Python projects,\n" +
			"fw runs the language package's pull_skills(), which ships those skills.\n" +
			"--dry-run reports what would change and writes nothing.",
		Example: "  fw skills pull\n  fw skills pull --dry-run\n  fw skills pull --overwrite --json",
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
				command, err := a.engines.PullSkills(p, overwrite, dryRun)
				if err != nil {
					return err
				}
				fmt.Fprintf(cmd.ErrOrStderr(), "Running in %s: %s\n", root, command)
				code, err := a.run(command)
				if err != nil {
					return err
				}
				if code != 0 {
					return &exitError{code: code, err: fmt.Errorf("pull_skills() exited with status %d; its last message names the problem", code)}
				}
				return nil
			}
			return a.pullStataSkills(cmd, root, overwrite, dryRun)
		},
	}
	cmd.Flags().BoolVar(&overwrite, "overwrite", false, "replace locally edited skills too, after backing them up to .framework/skills-backups/")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "report what would change without writing anything")
	cmd.Flags().BoolVar(&a.jsonOut, "json", false, jsonHelp+" (Stata projects)")
	return cmd
}

// pullStataSkills is fw skills pull for a Stata project, whose skills fw
// ships itself.
func (a *app) pullStataSkills(cmd *cobra.Command, root string, overwrite, dryRun bool) error {
	local := readSkillsSpec(root)
	dirs := projectSkillsDirs(root, local)
	out := pullJSON{Source: "cloud", DryRun: dryRun, Notes: []string{}, Backups: []string{}}

	wanted, reason, err := a.cloudSkills(cmd.Context(), root, &out.Notes)
	if err != nil {
		return err
	}
	if reason != "" {
		out.Source = "package"
		out.Notes = append([]string{fmt.Sprintf("Framework Cloud not reached (%s); using the package skills for this project type.", reason)}, out.Notes...)
		names := stata.PackageSkills(local.Type, local.Security.Sensitive)
		if len(names) == 0 {
			out.Notes = append(out.Notes, "fw ships no package skills for a "+local.Type+" Stata project, so there is nothing to install.")
		}
		wanted = nil
		for _, name := range names {
			wanted = append(wanted, skillsync.Wanted{Name: name, Package: true, Files: stata.ShippedSkill(name)})
		}
	}

	res, err := skillsync.Pull(skillsync.PullOptions{Root: root, Dirs: dirs, Skills: wanted, Overwrite: overwrite, DryRun: dryRun, Variant: "stata"})
	if err != nil {
		return err
	}
	out.Skills, out.Counts = res.Skills, res.Counts
	if res.Backups != nil {
		out.Backups = res.Backups
	}
	if out.Skills == nil {
		out.Skills = []skillsync.SkillStatus{}
	}
	for _, k := range []string{"added", "updated", "current", "edited", "replaced", "removed", "unavailable"} {
		out.Counts[k] += 0
	}
	if !dryRun {
		names := orderedSkills(root, dirs[0], wanted)
		if file, err := skillsync.RefreshSkillsTable(root, dirs[0], names); err != nil {
			return err
		} else if file != "" {
			out.Notes = append(out.Notes, "Refreshed the Skills table in "+file+".")
		}
	}
	out.Message = pullSummary(out.Counts, dryRun)
	if a.jsonOut {
		return writeJSON(cmd.OutOrStdout(), out)
	}
	printPull(cmd.OutOrStdout(), out)
	return nil
}

// orderedSkills lists the skill folders in dir: the setup's order first,
// then the others sorted.
func orderedSkills(root, dir string, wanted []skillsync.Wanted) []string {
	present := skillsync.SkillFolders(root, dir)
	var names []string
	for _, w := range wanted {
		if contains(present, w.Name) {
			names = append(names, w.Name)
		}
	}
	for _, name := range present {
		if !contains(names, name) {
			names = append(names, name)
		}
	}
	return names
}

// cloudSkills reads the project's skills from its layers on Framework Cloud.
// A non-empty reason means Cloud gave no list, and the caller falls back to
// the package skills.
func (a *app) cloudSkills(ctx context.Context, root string, notes *[]string) ([]skillsync.Wanted, string, error) {
	token := a.getenv("FW_PROJECT_TOKEN")
	if token == "" {
		token = project.EnvValue(root, "FW_PROJECT_TOKEN")
	}
	if token == "" {
		return nil, "this project has no FW_PROJECT_TOKEN in .env", nil
	}
	server, err := a.server("", cloud.ProjectScope)
	if err != nil {
		return nil, err.Error(), nil
	}
	raw, _, err := cloud.NewClient(server.URL, token).Pull(ctx)
	if err != nil {
		switch status := cloud.StatusOf(err); {
		case cloud.IsOffline(err):
			return nil, "cannot reach " + cloud.Host(server.URL), nil
		case status == 401 || status == 403:
			return nil, fmt.Sprintf("%s refused the project token", cloud.Host(server.URL)), nil
		default:
			return nil, err.Error(), nil
		}
	}
	var env spec.Envelope
	if err = json.Unmarshal(raw, &env); err != nil || env.SpecVersion != 1 {
		return nil, "", fmt.Errorf("%s returned a project setup this version of fw cannot read", cloud.Host(server.URL))
	}
	if lang := env.Language(); lang != "stata" {
		return nil, "", fmt.Errorf("on Framework Cloud this is a %s project, not a Stata project", lang)
	}
	if env.Name == "" {
		env.Name = filepath.Base(root)
	}
	resolved, err := stata.Resolve(env, "")
	if err != nil {
		return nil, "", fmt.Errorf("the project's setup on Framework Cloud: %w", err)
	}
	wanted := stata.WantedSkills(resolved.View)
	for i := range wanted {
		a.latestSkill(ctx, &wanted[i], server.URL, notes)
	}
	return wanted, "", nil
}

// latestSkill swaps a Cloud skill for its newest version when a user login
// can read it. The project's layers name the version chosen when the setup
// was last saved; project tokens can't read skills.
func (a *app) latestSkill(ctx context.Context, w *skillsync.Wanted, projectServer string, notes *[]string) {
	if w.Sync == nil || w.Sync.ID == "" {
		return
	}
	server := strings.TrimRight(w.Sync.Server, "/")
	if server == "" {
		server = projectServer
	}
	found, ok := a.tokens().Lookup(server)
	if !ok {
		return
	}
	var snap skillsync.Snapshot
	route := "/api/v1/skills/sync/" + url.PathEscape(w.Sync.ID) + "?variant=" + url.QueryEscape(w.Sync.Variant)
	if err := cloud.NewClient(server, found.Token).Do(ctx, "GET", route, nil, &snap); err != nil {
		*notes = append(*notes, fmt.Sprintf("Could not check %s for a newer version (%v); using the version in the project's setup.", w.Name, err))
		return
	}
	if h, err := skillsync.Hash(snap.Bundle); err != nil || h != snap.Hash {
		*notes = append(*notes, fmt.Sprintf("The newest version of %s failed verification; using the version in the project's setup.", w.Name))
		return
	}
	if snap.Version > w.Sync.Version {
		w.Files = snap.Bundle
		w.Sync = &skillsync.Source{ID: w.Sync.ID, Version: snap.Version, Hash: snap.Hash, Variant: w.Sync.Variant, Server: w.Sync.Server}
	}
}

// pullSummary is the one-line summary, with the parts that aren't zero.
func pullSummary(counts map[string]int, dryRun bool) string {
	parts := []string{}
	for _, p := range []struct{ key, label string }{
		{"added", "added"}, {"updated", "updated"}, {"current", "up to date"}, {"edited", "locally edited (kept)"},
		{"replaced", "replaced (backed up)"}, {"removed", "no longer in the project's setup"}, {"unavailable", "not available"},
	} {
		if n := counts[p.key]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, p.label))
		}
	}
	prefix := "Skills: "
	if dryRun {
		prefix = "Skills (dry run, nothing written): "
	}
	if len(parts) == 0 {
		return prefix + "none"
	}
	return prefix + strings.Join(parts, ", ")
}

func printPull(w io.Writer, out pullJSON) {
	if out.Source == "package" && len(out.Notes) > 0 {
		fmt.Fprintln(w, out.Notes[0])
		out.Notes = out.Notes[1:]
	}
	fmt.Fprintln(w, out.Message)
	for _, s := range out.Skills {
		switch s.Status {
		case skillsync.StatusEdited:
			fmt.Fprintf(w, "  Kept (locally edited): %s — fw skills pull --overwrite replaces it\n", s.Name)
		case skillsync.StatusRemoved:
			fmt.Fprintf(w, "  No longer in the project's setup (kept): %s\n", s.Name)
		case skillsync.StatusUnavailable:
			fmt.Fprintf(w, "  Not available from fw: %s ships with the R package\n", s.Name)
		case skillsync.StatusReplaced:
			fmt.Fprintf(w, "  Replaced: %s (the old copy is in .framework/skills-backups/)\n", s.Name)
		}
	}
	for _, n := range out.Notes {
		fmt.Fprintln(w, n)
	}
}

// pushItem is one skill fw skills push looked at.
type pushItem struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Status  string `json:"status"`
	ID      string `json:"skill_id,omitempty"`
	Version int    `json:"version,omitempty"`
	Message string `json:"message,omitempty"`

	builtin, forced bool
	variant         string
}

type pushJSON struct {
	DryRun  bool           `json:"dry_run"`
	Skills  []pushItem     `json:"skills"`
	Counts  map[string]int `json:"counts"`
	Message string         `json:"message"`
}

func newSkillsPushCmd(a *app) *cobra.Command {
	var force, yes, dryRun bool
	var server string
	cmd := &cobra.Command{
		Use:   "push",
		Short: "Send locally edited skills to Framework Cloud as new versions",
		Long: "Sends each skill you edited in this project to Framework Cloud as a new version\n" +
			"of your own skill. fw shows what each new version replaces and asks before\n" +
			"sending; without a terminal, pass --yes. A built-in Framework skill can't\n" +
			"change: pushing an edited one saves it as your own skill.\n\n" +
			"When a skill changed on Framework Cloud since the project last pulled it, fw\n" +
			"refuses: run fw skills pull first. --force replaces the Cloud version anyway;\n" +
			"the Cloud changes stay in the skill's history. Skills fw doesn't track are\n" +
			"listed; fw skills sync PATH adds one. --dry-run sends nothing.",
		Example: "  fw skills push --dry-run\n  fw skills push\n  fw skills push --yes --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			root := a.projectRoot()
			if root == "" {
				return errors.New("not inside a Framework project (no .framework folder, settings.yml, settings.yaml, settings.do, or Framework config.yml found)")
			}
			if server == "" {
				server, _ = cmd.Parent().PersistentFlags().GetString("url")
			}
			// The project's server, where its skills came from, with the
			// person's own login
			srv, err := a.server(server, cloud.ProjectScope)
			if err != nil {
				return err
			}
			found, ok := a.tokens().Lookup(srv.URL)
			if !ok {
				return &errNotLoggedIn{fmt.Sprintf("pushing skills needs your login: not logged in to %s. Run: %s", cloud.Host(srv.URL), loginCommand(srv.URL))}
			}
			client := cloud.NewClient(srv.URL, found.Token)
			return a.pushSkills(cmd, client, root, force, yes, dryRun)
		},
	}
	cmd.Flags().StringVar(&server, "url", "", "Framework Cloud server")
	cmd.Flags().BoolVar(&force, "force", false, "push even when the skill changed on Framework Cloud since the last pull")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "don't ask before pushing (required without a terminal)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would be pushed without sending anything")
	cmd.Flags().BoolVar(&a.jsonOut, "json", false, jsonHelp)
	return cmd
}

func (a *app) pushSkills(cmd *cobra.Command, client *cloud.Client, root string, force, yes, dryRun bool) error {
	ctx := cmd.Context()
	w := cmd.OutOrStdout()
	if a.jsonOut {
		w = cmd.ErrOrStderr()
	}
	variant := "r"
	if lang, _ := engine.DetectLanguage(root); lang == "python" || lang == "stata" {
		variant = lang
	}
	changes, untracked, err := skillsync.LocalChanges(root, projectSkillsDirs(root, readSkillsSpec(root)))
	if err != nil {
		return err
	}
	var items []pushItem
	for _, c := range changes {
		v := c.Variant
		if v == "" {
			v = variant
		}
		item := pushItem{Name: c.Name, Path: c.Path, builtin: c.Builtin, variant: v}
		r, err := skillsync.Sync(ctx, client, skillsync.Options{Root: root, Path: c.Path, Direction: "status", Variant: v})
		switch {
		case err != nil:
			item.Status, item.Message = "failed", explain(err, client.URL).Error()
		case r.Action == "push":
			item.Status = "push"
		case r.Action == "conflict" && force:
			item.Status, item.forced = "push", true
		case r.Action == "conflict":
			item.Status, item.Message = "refused", "Pull first: "+c.Name+" changed on Framework Cloud since this project pulled it. Run fw skills pull, or fw skills push --force to replace it."
		default:
			continue
		}
		items = append(items, item)
	}
	for _, p := range untracked {
		items = append(items, pushItem{Name: filepath.Base(p), Path: p, Status: "untracked",
			Message: "not tracked; use fw skills sync " + p + " to add it"})
	}

	pending := 0
	for i := range items {
		it := &items[i]
		if it.Status != "push" {
			continue
		}
		pending++
		switch {
		case it.forced:
			it.Message = "Cloud has a newer version of " + it.Name + " than this project pulled. Pushing replaces it as the latest version; the Cloud changes stay in its history."
		case it.builtin:
			it.Message = "This saves " + it.Name + " as your own skill; the built-in Framework skill stays as it is."
		default:
			it.Message = "This replaces the current version of " + it.Name + ", used wherever it's attached."
		}
		fmt.Fprintln(w, it.Message)
	}
	for _, it := range items {
		if it.Status != "push" && it.Message != "" {
			fmt.Fprintf(w, "  %s: %s\n", it.Path, it.Message)
		}
	}

	if pending > 0 && !dryRun {
		if err := a.confirm(cmd, yes, "Continue?"); err != nil {
			return err
		}
		for i := range items {
			it := &items[i]
			if it.Status != "push" {
				continue
			}
			o := skillsync.Options{Root: root, Path: it.Path, Direction: "push", Variant: it.variant}
			if it.forced {
				o.Resolve = "local"
			}
			r, err := skillsync.Sync(ctx, client, o)
			switch {
			case err != nil:
				it.Status, it.Message = "failed", explain(err, client.URL).Error()
			case r.Action == "push":
				it.Status, it.ID, it.Version = "pushed", r.ID, r.Version
			case r.Action == "conflict" || r.Action == "blocked":
				it.Status, it.Message = "refused", "Pull first: "+it.Name+" changed on Framework Cloud while pushing."
			default:
				it.Status = "unchanged"
			}
		}
	}
	counts := map[string]int{"pushed": 0, "push": 0, "refused": 0, "untracked": 0, "failed": 0}
	for _, it := range items {
		counts[it.Status]++
	}
	out := pushJSON{DryRun: dryRun, Skills: items, Counts: counts, Message: pushSummary(counts, dryRun)}
	if out.Skills == nil {
		out.Skills = []pushItem{}
	}
	for _, it := range items {
		if it.Status == "failed" {
			fmt.Fprintf(w, "  %s: %s\n", it.Path, it.Message)
		}
	}
	if a.jsonOut {
		err = writeJSON(cmd.OutOrStdout(), out)
	} else {
		_, err = fmt.Fprintln(w, out.Message)
	}
	if err == nil && (counts["refused"] > 0 || counts["failed"] > 0) {
		return &exitError{code: 1, silent: true, reason: "skill_push_refused"}
	}
	return err
}

func pushSummary(counts map[string]int, dryRun bool) string {
	parts := []string{}
	labels := []struct{ key, label string }{{"pushed", "pushed"}, {"push", "to push"}, {"refused", "refused (pull first)"}, {"failed", "failed"}, {"untracked", "not tracked"}}
	for _, l := range labels {
		if n := counts[l.key]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, l.label))
		}
	}
	prefix := "Skills pushed: "
	if dryRun {
		prefix = "Skills pushed (dry run, nothing sent): "
	}
	if len(parts) == 0 {
		return prefix + "nothing to push"
	}
	return prefix + strings.Join(parts, ", ")
}
