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

// projectSkillsDirs are the skills folders the project's enabled
// assistants read (.agents/skills when none is set). The first is the one
// AGENTS.md lists. A disabled assistant's folder isn't looked at: the
// copies the lock tracks there are found through the lock. An enabled
// folder fw can't look up, or that isn't a folder, is an error, so a pull
// never installs into some assistants' folders and silently skips another's.
func projectSkillsDirs(root string, s skillsSpec) ([]string, error) {
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
	for _, dir := range dirs {
		info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(dir)))
		switch {
		case os.IsNotExist(err):
		case err != nil:
			return nil, fmt.Errorf("look up %s: %w", dir, err)
		case !info.IsDir():
			return nil, fmt.Errorf("%s is not a folder; skills folders must be real folders", dir)
		}
	}
	return dirs, nil
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
				// The package fell back to its own skills without saying
				// how to link the project
				if a.projectToken(root) == "" {
					fmt.Fprintln(cmd.ErrOrStderr(), notLinked(""))
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
	out, err := a.stataSkills(cmd.Context(), root, overwrite, dryRun, skillsync.DefaultCommands)
	if err != nil {
		return err
	}
	if a.jsonOut {
		return writeJSON(cmd.OutOrStdout(), out)
	}
	printPull(cmd.OutOrStdout(), out)
	return nil
}

// stataSkills pulls a Stata project's skills, or with dryRun says what a
// pull would do, and returns the result unprinted. c names the commands its
// hints give.
func (a *app) stataSkills(ctx context.Context, root string, overwrite, dryRun bool, c skillsync.Commands) (pullJSON, error) {
	local := readSkillsSpec(root)
	out := pullJSON{Source: "cloud", DryRun: dryRun, Notes: []string{}, Backups: []string{}}

	// The Cloud setup's assistants count too: one turned on there gets its
	// skills folder, so folders are chosen once the setup is read
	cloudAssistants := map[string]bool{}
	sources, reason, err := a.cloudSkills(ctx, root, &out.Notes, cloudAssistants)
	if err != nil {
		return out, err
	}
	merged := local
	merged.AI.Assistants = map[string]bool{}
	for name, on := range local.AI.Assistants {
		merged.AI.Assistants[name] = on
	}
	for name, on := range cloudAssistants {
		if on {
			merged.AI.Assistants[name] = true
		}
	}
	dirs, err := projectSkillsDirs(root, merged)
	if err != nil {
		return out, err
	}
	offer := skillsync.Offer{Source: "cloud", Skills: sources}
	if reason != "" {
		// Offline: the package skills, which never replace a skill last
		// updated from Cloud; Cloud's own skills are reported unavailable
		out.Source, offer.Source = "package", "package"
		note := fmt.Sprintf("Framework Cloud not reached (%s); using the package skills for this project type.", reason)
		if reason == reasonNotLinked {
			note = notLinked("fw uses the package skills for this project type")
		}
		out.Notes = append([]string{note}, out.Notes...)
		names := stata.PackageSkills(local.Type, local.Security.Sensitive)
		if len(names) == 0 {
			out.Notes = append(out.Notes, "fw ships no package skills for a "+local.Type+" Stata project, so there is nothing to install.")
		}
		offer.Skills = nil
		for _, name := range names {
			offer.Skills = append(offer.Skills, stata.PackageSource(name))
		}
	} else if offer.ByIdentity, err = a.restoreMissing(ctx, root); err != nil {
		return out, err
	}
	res, err := skillsync.Pull(skillsync.PullOptions{Root: root, Dirs: dirs, Offer: offer, Overwrite: overwrite, DryRun: dryRun,
		Offline: out.Source == "package", Commands: c})
	if err != nil {
		return out, err
	}
	out.Skills, out.Counts = res.Skills, res.Counts
	if res.Backups != nil {
		out.Backups = res.Backups
	}
	if out.Skills == nil {
		out.Skills = []skillsync.SkillStatus{}
	}
	for _, k := range []string{"added", "restored", "updated", "current", "edited", "replaced", "diverged", "missing", "removed", "unavailable", "failed", "kept", "untracked"} {
		out.Counts[k] += 0
	}
	if !dryRun {
		names, err := orderedSkills(root, dirs[0], offer.Skills)
		if err != nil {
			return out, err
		}
		if file, err := skillsync.RefreshSkillsTable(root, dirs[0], names); err != nil {
			return out, err
		} else if file != "" {
			out.Notes = append(out.Notes, "Refreshed the Skills table in "+file+".")
		}
	}
	out.Message = pullSummary(out.Counts, dryRun)
	return out, nil
}

// restoreMissing fetches, with your login, the newest version of each
// tracked Cloud skill all of whose copies are gone, so pull can put it back
// from its own identity. A skill fw can't fetch is left out: pull then
// reports it missing, with what would put it back.
func (a *app) restoreMissing(ctx context.Context, root string) (map[string]skillsync.Source, error) {
	missing, err := skillsync.MissingEntries(root)
	if err != nil || len(missing) == 0 {
		return nil, err
	}
	restore := map[string]skillsync.Source{}
	for _, m := range missing {
		server := strings.TrimRight(m.Server, "/")
		if server == "" {
			continue
		}
		found, ok := a.tokens().Lookup(server)
		if !ok {
			continue
		}
		var snap skillsync.Snapshot
		route := "/api/v1/skills/sync/" + url.PathEscape(m.ID) + "?variant=" + url.QueryEscape(m.Variant)
		if err := cloud.NewClient(server, found.Token).Do(ctx, "GET", route, nil, &snap); err != nil {
			continue
		}
		if h, err := skillsync.Hash(snap.Bundle); err != nil || h != snap.Hash {
			continue
		}
		restore[m.Key] = skillsync.CloudSource("", skillsync.SyncSource{ID: m.ID, Version: snap.Version, Hash: snap.Hash,
			Variant: m.Variant, Server: server, KnownHashes: snap.KnownHashes}, snap.Bundle, server)
	}
	return restore, nil
}

// orderedSkills lists the skill folders in dir: the setup's order first,
// then the others sorted.
func orderedSkills(root, dir string, sources []skillsync.Source) ([]string, error) {
	present, err := skillsync.SkillFolders(root, dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, s := range sources {
		if contains(present, s.Name) {
			names = append(names, s.Name)
		}
	}
	for _, name := range present {
		if !contains(names, name) {
			names = append(names, name)
		}
	}
	return names, nil
}

// reasonNotLinked is cloudSkills' reason when the project has no token.
const reasonNotLinked = "not linked"

// cloudSkills reads the project's skills from its layers on Framework Cloud.
// A non-empty reason means Cloud gave no list, and the caller falls back to
// the package skills.
// The setup's assistants are copied into assistants.
func (a *app) cloudSkills(ctx context.Context, root string, notes *[]string, assistants map[string]bool) ([]skillsync.Source, string, error) {
	token := a.projectToken(root)
	if token == "" {
		return nil, reasonNotLinked, nil
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
	for name, on := range resolved.View.AI.Assistants {
		assistants[name] = on
	}
	sources, err := stata.SetupSkills(resolved.View, server.URL)
	if err != nil {
		return nil, "", err
	}
	for i := range sources {
		a.latestSkill(ctx, &sources[i], notes)
	}
	return sources, "", nil
}

// latestSkill swaps a Cloud skill for its newest version when a user login
// can read it. The project's layers name the version chosen when the setup
// was last saved; project tokens can't read skills.
func (a *app) latestSkill(ctx context.Context, s *skillsync.Source, notes *[]string) {
	if s.Channel != skillsync.ChannelCloud || s.Identity.SkillID == "" {
		return
	}
	found, ok := a.tokens().Lookup(s.Server)
	if !ok {
		return
	}
	var snap skillsync.Snapshot
	route := "/api/v1/skills/sync/" + url.PathEscape(s.Identity.SkillID) + "?variant=" + url.QueryEscape(s.Identity.Variant)
	if err := cloud.NewClient(s.Server, found.Token).Do(ctx, "GET", route, nil, &snap); err != nil {
		*notes = append(*notes, fmt.Sprintf("Could not check %s for a newer version (%v); using the version in the project's setup.", s.Name, err))
		return
	}
	if h, err := skillsync.Hash(snap.Bundle); err != nil || h != snap.Hash {
		*notes = append(*notes, fmt.Sprintf("The newest version of %s failed verification; using the version in the project's setup.", s.Name))
		return
	}
	if snap.Version > s.Version {
		known := append(append(append([]string{}, s.Known...), snap.KnownHashes...), snap.Hash)
		*s = skillsync.CloudSource(s.Name, skillsync.SyncSource{ID: s.Identity.SkillID, Version: snap.Version, Hash: snap.Hash,
			Variant: s.Identity.Variant, Server: s.Server}, snap.Bundle, s.Server, known...)
	}
}

// pullSummary is the one-line summary, with the parts that aren't zero.
func pullSummary(counts map[string]int, dryRun bool) string {
	parts := []string{}
	for _, p := range []struct{ key, label string }{
		{"added", "added"}, {"restored", "restored"}, {"updated", "updated"}, {"current", "up to date"}, {"edited", "locally edited (kept)"},
		{"replaced", "replaced (backed up)"}, {"removed", "no longer in the project's setup"}, {"unavailable", "not available"},
		{"diverged", "diverged (nothing written)"}, {"missing", "missing"}, {"failed", "failed"},
		{"kept", "kept (another skill's)"}, {"untracked", "untracked (left alone)"},
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
		if line := skillRowLine(s); line != "" {
			fmt.Fprintln(w, line)
		}
	}
	for _, n := range out.Notes {
		fmt.Fprintln(w, n)
	}
}

// skillRowLine is one row as text: its status, its name, and its reason,
// then the command when the reason doesn't already give it. Rows that need
// no explanation (added, updated, current) print nothing.
func skillRowLine(s skillsync.SkillStatus) string {
	if s.Reason == "" && s.Status != skillsync.StatusRestored {
		return ""
	}
	label := strings.ToUpper(s.Status[:1]) + s.Status[1:]
	line := "  " + label + ": " + s.Name
	if s.Reason != "" {
		line += " — " + s.Reason
	}
	if s.Hint != "" && !strings.Contains(s.Reason, s.Hint) {
		line += " Run: " + s.Hint
	}
	return line
}

type pushJSON struct {
	DryRun  bool                    `json:"dry_run"`
	Skills  []skillsync.SkillStatus `json:"skills"`
	Counts  map[string]int          `json:"counts"`
	Message string                  `json:"message"`
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
			"A skill's copies in each assistant's folder go together: when they differ, or\n" +
			"one is missing beside an edited one, fw names the fw skills copy command that\n" +
			"settles them. When a skill changed on Framework Cloud since the project last\n" +
			"pulled it, fw refuses: run fw skills pull first. --force replaces the Cloud\n" +
			"version anyway; the Cloud changes stay in the skill's history. After fw skills\n" +
			"move changed a copy's identity, --force sends its edits as the next version.\n" +
			"Skills fw doesn't track are listed; fw skills sync PATH adds one, and fw skills\n" +
			"move records a folder you moved by hand. --dry-run sends nothing.",
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
			// An R project's skills come from its framework package's pull:
			// an older one can leave copies out of step, so push waits for it
			if err := a.engines.CheckSkillsLock(a.engines.Detect(root), "fw skills push"); err != nil {
				return err
			}
			client, err := a.skillsClient(server)
			if err != nil {
				return err
			}
			return a.pushSkills(cmd, client, root, force, yes, dryRun)
		},
	}
	cmd.Flags().StringVar(&server, "url", "", "Framework Cloud server")
	cmd.Flags().BoolVar(&force, "force", false, "push even when the skill changed on Framework Cloud since the last pull, or has no version to push against")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "don't ask before pushing (required without a terminal)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would be pushed without sending anything")
	cmd.Flags().BoolVar(&a.jsonOut, "json", false, jsonHelp)
	return cmd
}

// skillsClient is the project's server, where its skills came from, with
// the person's own login.
func (a *app) skillsClient(server string) (*cloud.Client, error) {
	srv, err := a.server(server, cloud.ProjectScope)
	if err != nil {
		return nil, err
	}
	found, ok := a.tokens().Lookup(srv.URL)
	if !ok {
		return nil, &errNotLoggedIn{fmt.Sprintf("pushing skills needs your login: not logged in to %s. Run: %s", cloud.Host(srv.URL), loginCommand(srv.URL))}
	}
	return cloud.NewClient(srv.URL, found.Token), nil
}

// skillRemote asks Framework Cloud for the head of a tracked skill: by its
// Cloud ID, or for a built-in, the version the project has. A built-in
// Cloud never published, or a skill with no Cloud identity, has none.
func skillRemote(ctx context.Context, client *cloud.Client) func(string, skillsync.LockEntry) (skillsync.Remote, bool, error) {
	return func(_ string, e skillsync.LockEntry) (skillsync.Remote, bool, error) {
		id := e.Identity
		// A Cloud skill lives on its own server: asking another would read
		// its absence as "not found", and push it there as a new skill
		if server := strings.TrimRight(client.URL, "/"); id.Kind == skillsync.KindCloud && strings.TrimRight(id.Server, "/") != server {
			return skillsync.Remote{}, false, fmt.Errorf("%s is a skill on %s, and this push goes to %s, so it wasn't checked or sent. Push from a login to %s",
				id.SkillID, id.Server, server, id.Server)
		}
		var route string
		switch {
		case id.Kind == skillsync.KindCloud:
			route = "/api/v1/skills/sync/" + url.PathEscape(id.SkillID) + "?variant=" + url.QueryEscape(id.Variant)
		case id.Kind == skillsync.KindBuiltin && e.Base != nil && e.Base.Channel == skillsync.ChannelCloud:
			route = "/api/v1/skills/sync/" + url.PathEscape(id.SkillID) + "?variant=" + url.QueryEscape(id.Variant)
		case id.Kind == skillsync.KindBuiltin && e.Base != nil:
			route = "/api/v1/skills/sync/" + url.PathEscape(id.SkillID) + "?variant=" + url.QueryEscape(id.Variant) + "&hash=" + url.QueryEscape(e.Base.Hash)
		default:
			return skillsync.Remote{}, false, nil
		}
		var snap skillsync.Snapshot
		if err := client.Do(ctx, "GET", route, nil, &snap); err != nil {
			if status := cloud.StatusOf(err); status == 404 || (id.Kind == skillsync.KindBuiltin && status == 409) {
				return skillsync.Remote{}, false, nil
			}
			return skillsync.Remote{}, false, explain(err, client.URL)
		}
		if h, err := skillsync.Hash(snap.Bundle); err != nil || h != snap.Hash {
			return skillsync.Remote{}, false, errors.New("the copy on Framework Cloud failed verification")
		}
		return skillsync.Remote{Version: snap.Version, Hash: snap.Hash, ID: snap.ID}, true, nil
	}
}

// skillSender posts one push to Framework Cloud.
func skillSender(ctx context.Context, client *cloud.Client, projectToken string) skillsync.PushSender {
	return func(req skillsync.PushRequest) (skillsync.Snapshot, error) {
		var id any
		if req.SkillID != "" {
			id = req.SkillID
		}
		body := map[string]any{"request_id": req.RequestID, "skill_id": id, "expected_version": req.ExpectedVersion, "variant": req.Variant,
			"bundle": req.Bundle, "client": map[string]string{"app": "framework-cli"}}
		if projectToken != "" {
			body["project_token"] = projectToken
		}
		var snap skillsync.Snapshot
		if err := client.Do(ctx, "POST", "/api/v1/skills/sync", body, &snap); err != nil {
			if cloud.StatusOf(err) == 409 {
				return snap, errors.New("pull first: it changed on Framework Cloud while pushing")
			}
			return snap, explain(err, client.URL)
		}
		return snap, nil
	}
}

// pushNotes say what each planned push replaces, by entry.
func pushNotes(pp *skillsync.PushPlan) []string {
	var out []string
	for _, e := range pp.Entries() {
		switch {
		case e.Forced:
			out = append(out, "This makes the project's "+e.Name+" the latest version on Framework Cloud, over changes this project never pulled; they stay in its history.")
		case e.Fork:
			out = append(out, "This saves "+e.Name+" as your own skill; the built-in Framework skill stays as it is.")
		default:
			out = append(out, "This replaces the current version of "+e.Name+", used wherever it's attached.")
		}
	}
	return out
}

// pushSkills is fw skills push.
func (a *app) pushSkills(cmd *cobra.Command, client *cloud.Client, root string, force, yes, dryRun bool) error {
	ctx := cmd.Context()
	w := cmd.OutOrStdout()
	if a.jsonOut {
		w = cmd.ErrOrStderr()
	}
	dirs, err := projectSkillsDirs(root, readSkillsSpec(root))
	if err != nil {
		return err
	}
	pp, err := skillsync.PlanPush(skillsync.PushOptions{Root: root, Dirs: dirs, Force: force, Remote: skillRemote(ctx, client)})
	if err != nil {
		return err
	}
	for _, n := range pushNotes(pp) {
		fmt.Fprintln(w, n)
	}
	// Failed skills are printed once, at the end, with any that fail while
	// pushing
	for _, it := range pp.Rows() {
		if it.Status != skillsync.StatusPushed && it.Status != skillsync.StatusFailed && it.Reason != "" {
			fmt.Fprintln(w, "  "+strings.TrimPrefix(skillRowLine(it), "  "))
		}
	}
	var cancelled error
	if pp.Pending() && !dryRun {
		if err := a.confirm(cmd, yes, "Continue?"); err != nil {
			var exit *exitError
			if errors.As(err, &exit) {
				return err // no terminal: nothing was asked
			}
			// Declined: what would have been pushed is cancelled
			cancelled = err
			pp.Cancel()
		} else if err := pp.Apply(client.URL, skillSender(ctx, client, a.projectTokenFor(root, client.URL))); err != nil {
			return err
		}
	} else if pp.Acts() && !dryRun {
		// Only the lock changes: Cloud already has the bytes
		if err := pp.Apply(client.URL, skillSender(ctx, client, a.projectTokenFor(root, client.URL))); err != nil {
			return err
		}
	}
	rows := pp.Rows()
	counts := map[string]int{"pushed": 0, "current": 0, "refused": 0, "missing": 0, "untracked": 0, "cancelled": 0, "failed": 0}
	for _, it := range rows {
		counts[it.Status]++
	}
	out := pushJSON{DryRun: dryRun, Skills: rows, Counts: counts, Message: pushSummary(counts, dryRun)}
	for _, it := range rows {
		if it.Status == skillsync.StatusFailed {
			fmt.Fprintln(w, skillRowLine(it))
		}
	}
	if a.jsonOut {
		err = writeJSON(cmd.OutOrStdout(), out)
	} else {
		_, err = fmt.Fprintln(w, out.Message)
	}
	if err == nil && cancelled != nil {
		return cancelled
	}
	if err == nil && (counts["refused"] > 0 || counts["failed"] > 0 || counts["missing"] > 0) {
		return &exitError{code: 1, silent: true, reason: "skill_push_refused"}
	}
	return err
}

func pushSummary(counts map[string]int, dryRun bool) string {
	parts := []string{}
	pushed := "pushed"
	if dryRun {
		pushed = "to push"
	}
	labels := []struct{ key, label string }{{"pushed", pushed}, {"current", "already on Framework Cloud"}, {"refused", "refused"}, {"failed", "failed"},
		{"missing", "missing"}, {"cancelled", "cancelled"}, {"untracked", "not tracked"}}
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
