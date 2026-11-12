package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/frameworktools/framework-cli/internal/catalog"
	"github.com/frameworktools/framework-cli/internal/cloud"
	"github.com/frameworktools/framework-cli/internal/engine"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// fw data pull and push: a project's data catalog (the data section of its
// settings) against its data registry on Framework Cloud. Only definitions
// move: never data files, and never hashes. R and Python projects run their
// package's pull_data_registry() and push_data_registry(); fw handles Stata
// projects itself.

const dataLockFile = ".framework/data.lock.json"

// dataLock is .framework/data.lock.json: the registry revision last pulled
// or pushed, which a push sends as base_revision. Shared with R and Python.
type dataLock struct {
	Schema    int    `json:"schema"`
	Revision  string `json:"revision"`
	Server    string `json:"server"`
	UpdatedAt string `json:"updated_at"`
}

func readDataLock(root string) dataLock {
	var l dataLock
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(dataLockFile))) //nolint:gosec // a project file
	if err == nil {
		_ = json.Unmarshal(data, &l)
	}
	return l
}

func writeDataLock(root, revision, server string, now time.Time) error {
	data, err := json.MarshalIndent(dataLock{Schema: 1, Revision: revision, Server: server, UpdatedAt: now.UTC().Format(time.RFC3339)}, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(root, filepath.FromSlash(dataLockFile))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { //nolint:gosec // a project folder, shared through git
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644) //nolint:gosec // a project file, shared through git
}

// cloudData is GET /api/v1/projects/data.
type cloudData struct {
	Revision string        `json:"revision"`
	Entries  []catalog.Row `json:"entries"`
}

// dataChange is one changed entry in a push's reply.
type dataChange struct {
	Name string      `json:"name"`
	From catalog.Row `json:"from"`
	To   catalog.Row `json:"to"`
}

// dataPushReply is POST /api/v1/projects/data's answer.
type dataPushReply struct {
	Applied  bool   `json:"applied"`
	Revision string `json:"revision"`
	Changes  struct {
		Added   []catalog.Row `json:"added"`
		Changed []dataChange  `json:"changed"`
		Removed []catalog.Row `json:"removed"`
		Kept    []catalog.Row `json:"kept"`
	} `json:"changes"`
}

func newDataCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "data",
		Short: "Bring a project's data catalog in line with its data registry on Framework Cloud",
		Long: "A project's data registry is its data catalog, the data section of its settings,\n" +
			"as Framework Cloud keeps it. fw data pull brings this project's catalog up to\n" +
			"date with it; fw data push sends this project's entries back. Only definitions\n" +
			"move: names, paths, types, and options. Data files never do, and file hashes\n" +
			"stay in the project.",
	}
	cmd.AddCommand(newDataPullCmd(a), newDataPushCmd(a))
	return cmd
}

func newDataPullCmd(a *app) *cobra.Command {
	var overwrite, yes, dryRun bool
	cmd := &cobra.Command{
		Use:   "pull",
		Short: "Bring the project's data catalog up to date with Framework Cloud",
		Long: "Reads the project's data registry on Framework Cloud with its project token\n" +
			"(FW_PROJECT_TOKEN in .env), lists the changes, and asks before applying them:\n" +
			"entries to add, entries whose definition differs, and entries only in this\n" +
			"project. Applying writes the new entries into the settings' data section.\n" +
			"Local entries are never removed, and local definitions are kept; --overwrite\n" +
			"uses Framework Cloud's instead.\n\n" +
			"Without a terminal, pass --yes to apply or --dry-run to only list the changes.\n" +
			"In R and Python projects, fw runs the language package's pull_data_registry().",
		Example: "  fw data pull --dry-run\n  fw data pull\n  fw data pull --yes --overwrite",
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
				return a.handOffSetup(cmd, root, "pull_data_registry()", "Apply these changes?", yes, dryRun, func(m engine.SetupMode) (engine.Command, error) {
					return a.engines.PullData(p, o, m)
				})
			}
			return a.pullStataData(cmd, root, overwrite, yes, dryRun)
		},
	}
	cmd.Flags().BoolVar(&overwrite, "overwrite", false, "use Framework Cloud's definitions over local edits")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "apply without asking (required without a terminal)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "list the changes without applying them")
	cmd.Flags().BoolVar(&a.jsonOut, "json", false, jsonHelp+" (Stata projects)")
	return cmd
}

func newDataPushCmd(a *app) *cobra.Command {
	var remove, force, yes, dryRun bool
	cmd := &cobra.Command{
		Use:   "push",
		Short: "Send the project's data catalog to Framework Cloud",
		Long: "Sends this project's data catalog to its data registry on Framework Cloud,\n" +
			"after showing what changes there and asking. New copies of the project, ZIP\n" +
			"downloads, and Update on GitHub use the new registry. Entries only on\n" +
			"Framework Cloud are kept unless you pass --remove. When the registry changed on\n" +
			"Framework Cloud since this project last pulled it, fw refuses: run fw data pull\n" +
			"first, or pass --force to replace Cloud's newer changes. No data files and no\n" +
			"hashes are sent.\n\n" +
			"Without a terminal, pass --yes. --dry-run sends nothing. In R and Python\n" +
			"projects, fw runs the language package's push_data_registry().",
		Example: "  fw data push --dry-run\n  fw data push\n  fw data push --yes --remove",
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
				return a.handOffSetup(cmd, root, "push_data_registry()", "Push these changes?", yes, dryRun, func(m engine.SetupMode) (engine.Command, error) {
					return a.engines.PushData(p, o, m)
				})
			}
			return a.pushStataData(cmd, root, remove, force, yes, dryRun)
		},
	}
	cmd.Flags().BoolVar(&remove, "remove", false, "remove entries from the Cloud registry that this project doesn't have")
	cmd.Flags().BoolVar(&force, "force", false, "push even when the registry changed on Framework Cloud since the last pull")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "push without asking (required without a terminal)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would change without sending anything")
	cmd.Flags().BoolVar(&a.jsonOut, "json", false, jsonHelp+" (Stata projects)")
	return cmd
}

// dataClient is the project's server with its project token, or an error
// that changes nothing.
func (a *app) dataClient(root string) (*cloud.Client, error) {
	client, reason, err := a.setupClient(root)
	if err != nil {
		return nil, err
	}
	if reason != "" {
		return nil, fmt.Errorf("data registry not read: Framework Cloud not reached (%s); nothing was changed", reason)
	}
	return client, nil
}

// dataError explains a failed registry request.
func dataError(client *cloud.Client, err error) error {
	host := cloud.Host(client.URL)
	var apiErr *cloud.APIError
	switch status := cloud.StatusOf(err); {
	case cloud.IsOffline(err):
		return fmt.Errorf("data registry not read: Framework Cloud not reached (cannot reach %s); nothing was changed", host)
	case status == 404:
		return fmt.Errorf("this Framework Cloud server (%s) has no data registry endpoint yet; nothing was changed", host)
	case status == 401 || status == 403:
		return fmt.Errorf("data registry not read: Framework Cloud refused the project token (%s). Check FW_PROJECT_TOKEN in the project's .env; nothing was changed", host)
	case status == 422 && errors.As(err, &apiErr):
		return fmt.Errorf("data registry refused by Framework Cloud: %s; nothing was changed", strings.TrimSuffix(apiErr.Message, "."))
	default:
		return fmt.Errorf("data registry not read: Framework Cloud not reached (%w); nothing was changed", err)
	}
}

func fetchData(ctx context.Context, client *cloud.Client) (cloudData, error) {
	var d cloudData
	if err := client.Do(ctx, "GET", "/api/v1/projects/data", nil, &d); err != nil {
		return d, dataError(client, err)
	}
	return d, nil
}

func rowNames(rows []catalog.Row) string {
	names := make([]string, len(rows))
	for i, r := range rows {
		names[i] = r.Name
	}
	return strings.Join(names, ", ")
}

// dataPullJSON is fw data pull --json, for Stata projects, matching
// pull_data_registry()'s result.
type dataPullJSON struct {
	Revision string `json:"revision"`
	Entries  struct {
		Added      []string `json:"added"`
		Changed    []string `json:"changed"`
		DifferKept []string `json:"differ_kept"`
		KeptLocal  []string `json:"kept_local"`
	} `json:"entries"`
	Applied bool   `json:"applied"`
	DryRun  bool   `json:"dry_run"`
	Message string `json:"message"`
}

func (a *app) pullStataData(cmd *cobra.Command, root string, overwrite, yes, dryRun bool) error {
	ctx := cmd.Context()
	w := cmd.OutOrStdout()
	if a.jsonOut {
		w = cmd.ErrOrStderr()
	}
	client, err := a.dataClient(root)
	if err != nil {
		return err
	}
	registry, err := fetchData(ctx, client)
	if err != nil {
		return err
	}
	file, err := catalog.Open(root)
	if err != nil {
		return fmt.Errorf("data catalog not read: %w; nothing was changed", err)
	}
	local := file.Rows()
	cloudRows := map[string]catalog.Row{}
	var added []catalog.Row
	out := dataPullJSON{Revision: registry.Revision, DryRun: dryRun}
	out.Entries.Added, out.Entries.Changed, out.Entries.DifferKept, out.Entries.KeptLocal = []string{}, []string{}, []string{}, []string{}
	var differ []string
	for _, r := range registry.Entries {
		cloudRows[r.Name] = r
		if l, ok := local[r.Name]; !ok {
			added = append(added, r)
			out.Entries.Added = append(out.Entries.Added, r.Name)
		} else if !catalog.Same(l, r) {
			differ = append(differ, r.Name)
		}
	}
	for name := range local {
		if _, ok := cloudRows[name]; !ok {
			out.Entries.KeptLocal = append(out.Entries.KeptLocal, name)
		}
	}
	sort.Strings(differ)
	sort.Strings(out.Entries.KeptLocal)
	if overwrite {
		out.Entries.Changed = differ
	} else {
		out.Entries.DifferKept = differ
	}

	lines := []string{fmt.Sprintf("Data registry changes from Framework Cloud (revision %s):", registry.Revision)}
	if len(added) > 0 {
		lines = append(lines, "  Entries to add: "+rowNames(added))
	}
	if len(differ) > 0 {
		details := make([]string, len(differ))
		for i, n := range differ {
			details[i] = fmt.Sprintf("%s (%s)", n, catalog.Diff(local[n], cloudRows[n]))
		}
		tail := "kept; fw data pull --overwrite uses Cloud's"
		if overwrite {
			tail = "Cloud's will be used"
		}
		lines = append(lines, "  Entries that differ (local -> Cloud): "+strings.Join(details, "; ")+" — "+tail)
	}
	if len(out.Entries.KeptLocal) > 0 {
		lines = append(lines, "  Only in this project (kept): "+strings.Join(out.Entries.KeptLocal, ", "))
	}
	if len(added) == 0 && len(differ) == 0 && len(out.Entries.KeptLocal) == 0 {
		lines = []string{fmt.Sprintf("Data registry is up to date with Framework Cloud (revision %s).", registry.Revision)}
	}
	out.Message = strings.Join(lines, "\n")
	fmt.Fprintln(w, out.Message)
	changes := len(added) > 0 || (overwrite && len(differ) > 0)

	finish := func() error {
		if a.jsonOut {
			return writeJSON(cmd.OutOrStdout(), out)
		}
		return nil
	}
	if dryRun {
		return finish()
	}
	if changes {
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
		rows := added
		if overwrite {
			for _, n := range differ {
				rows = append(rows, cloudRows[n])
			}
		}
		for _, r := range rows {
			file.Set(r)
		}
		if err := file.Save(); err != nil {
			return err
		}
		word := "entries"
		if len(rows) == 1 {
			word = "entry"
		}
		fmt.Fprintf(w, "✓ Data registry: %d %s added or updated\n", len(rows), word)
		out.Applied = true
	}
	if err := writeDataLock(root, registry.Revision, client.URL, time.Now()); err != nil {
		return err
	}
	return finish()
}

// dataPushJSON is fw data push --json, for Stata projects, matching
// push_data_registry()'s result.
type dataPushJSON struct {
	Revision string `json:"revision"`
	Changes  any    `json:"changes"`
	Applied  bool   `json:"applied"`
	DryRun   bool   `json:"dry_run"`
	Message  string `json:"message"`
}

func (a *app) pushStataData(cmd *cobra.Command, root string, remove, force, yes, dryRun bool) error {
	ctx := cmd.Context()
	w := cmd.OutOrStdout()
	if a.jsonOut {
		w = cmd.ErrOrStderr()
	}
	client, err := a.dataClient(root)
	if err != nil {
		return err
	}
	file, err := catalog.Open(root)
	if err != nil {
		return fmt.Errorf("data catalog not read: %w; nothing was changed", err)
	}
	byName := file.Rows()
	local := make([]catalog.Row, 0, len(byName))
	for _, r := range byName {
		local = append(local, r)
	}
	sort.Slice(local, func(i, j int) bool { return local[i].Name < local[j].Name })
	base := readDataLock(root).Revision
	if base == "" {
		current, err := fetchData(ctx, client)
		if err != nil {
			return err
		}
		base = current.Revision
	}

	send := func(dry bool) (dataPushReply, error) {
		var reply dataPushReply
		body := map[string]any{"base_revision": base, "entries": local, "remove": remove, "force": force, "dry_run": dry}
		err := client.Do(ctx, "POST", "/api/v1/projects/data", body, &reply)
		if cloud.StatusOf(err) == 409 {
			latest := "unknown"
			if current, gerr := fetchData(ctx, client); gerr == nil {
				latest = current.Revision
			}
			return reply, fmt.Errorf("the data registry on Framework Cloud changed since this project last pulled it (revision %s). Pull first: fw data pull. fw data push --force replaces Cloud's changes", latest)
		}
		if err != nil {
			return reply, dataError(client, err)
		}
		return reply, nil
	}

	preview, err := send(true)
	if err != nil {
		return err
	}
	c := preview.Changes
	lines := []string{"Data registry changes for Framework Cloud:"}
	if len(c.Added) > 0 {
		lines = append(lines, "  Entries to add: "+rowNames(c.Added))
	}
	if len(c.Changed) > 0 {
		details := make([]string, len(c.Changed))
		for i, ch := range c.Changed {
			details[i] = fmt.Sprintf("%s (%s)", ch.Name, catalog.Diff(ch.From, ch.To))
		}
		lines = append(lines, "  Entries to change: "+strings.Join(details, "; "))
	}
	if len(c.Removed) > 0 {
		lines = append(lines, "  Entries to remove: "+rowNames(c.Removed))
	}
	if len(c.Kept) > 0 {
		lines = append(lines, "  On Cloud only (kept): "+rowNames(c.Kept))
	}
	pending := len(c.Added)+len(c.Changed)+len(c.Removed) > 0
	if !pending {
		lines[0] = "Data registry on Framework Cloud already matches this project."
	} else {
		lines = append(lines, fmt.Sprintf("This changes %s's data registry on Framework Cloud. New copies of the project, zips, and Update on GitHub will use it. Only definitions are sent: no data files and no hashes.", dataProjectName(root)))
		if force && preview.Revision != "" && base != preview.Revision {
			lines = append(lines, "Cloud's newer data registry changes will be replaced by this project's entries.")
		}
	}
	out := dataPushJSON{Revision: preview.Revision, Changes: preview.Changes, DryRun: dryRun, Message: strings.Join(lines, "\n")}
	fmt.Fprintln(w, out.Message)
	finish := func() error {
		if a.jsonOut {
			return writeJSON(cmd.OutOrStdout(), out)
		}
		return nil
	}
	if !pending || dryRun {
		return finish()
	}
	if !yes {
		if !a.interactive {
			return usageError("no terminal to confirm from. Rerun with --yes to push the changes above, or --dry-run to only list them")
		}
		ok, err := a.askYes(cmd, "Push these changes?")
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("canceled; nothing was pushed")
		}
	}
	reply, err := send(false)
	if err != nil {
		return err
	}
	if err := writeDataLock(root, reply.Revision, client.URL, time.Now()); err != nil {
		return err
	}
	out.Applied, out.Revision, out.Changes = reply.Applied, reply.Revision, reply.Changes
	if reply.Applied {
		var parts []string
		for _, p := range []struct {
			n    int
			word string
		}{{len(reply.Changes.Added), "added"}, {len(reply.Changes.Changed), "changed"}, {len(reply.Changes.Removed), "removed"}} {
			if p.n > 0 {
				parts = append(parts, fmt.Sprintf("%d %s", p.n, p.word))
			}
		}
		fmt.Fprintf(w, "Data registry pushed: revision %s (%s)\n", reply.Revision, strings.Join(parts, ", "))
	} else {
		fmt.Fprintln(w, "Already up to date.")
	}
	return finish()
}

// dataProjectName is settings.yml's project_name, else the folder's name.
func dataProjectName(root string) string {
	raw, err := os.ReadFile(filepath.Join(root, "settings.yml")) //nolint:gosec // a project file
	if err == nil {
		var s struct {
			Name    string `yaml:"project_name"`
			Default struct {
				Name string `yaml:"project_name"`
			} `yaml:"default"`
		}
		if yaml.Unmarshal(raw, &s) == nil {
			if s.Default.Name != "" {
				return s.Default.Name
			}
			if s.Name != "" {
				return s.Name
			}
		}
	}
	return filepath.Base(root)
}
