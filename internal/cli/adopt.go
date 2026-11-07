package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/frameworktools/framework-cli/internal/adopt"
	"github.com/frameworktools/framework-cli/internal/cloud"
	"github.com/frameworktools/framework-cli/internal/engine"
	"github.com/frameworktools/framework-cli/internal/spec"
	"github.com/frameworktools/framework-cli/internal/stata"
)

// projectDir returns the absolute folder an adopt or import command works
// on: the argument, or the current folder.
func (a *app) projectDir(args []string) (string, error) {
	dir := a.cwd
	if len(args) == 1 {
		dir = args[0]
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(a.cwd, dir)
		}
	}
	info, err := os.Stat(dir)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a folder", dir)
	}
	return filepath.Clean(dir), nil
}

// reviewMapping shows the proposed mapping on stderr and lets the person
// accept it, change a folder's role, or cancel. --yes accepts it as shown.
func (a *app) reviewMapping(cmd *cobra.Command, rules adopt.Rules, m adopt.Mapping, language string, yes bool) (adopt.Mapping, error) {
	errOut := cmd.ErrOrStderr()
	if m.Empty() {
		return m, errors.New("no folders found to map. Nothing was changed")
	}
	for _, w := range m.Warnings {
		fmt.Fprintf(errOut, "Warning: %s\n", w.Text())
	}
	show := func() [][2]string {
		rows := rules.Rows(m)
		fmt.Fprintln(errOut, "Proposed mapping:")
		for i, row := range rows {
			fmt.Fprintf(errOut, "  %2d. %-30s %s\n", i+1, row[0], row[1])
		}
		return rows
	}
	if yes {
		show()
		return m, nil
	}
	if !a.interactive {
		show()
		return m, usageError("no terminal to review the mapping from. After reading it above, rerun with --yes to accept it")
	}
	keys := rules.Keys(language)
	for {
		rows := show()
		fmt.Fprint(errOut, "Accept (a), change a folder (number), or cancel (c)? [a] ")
		answer, err := a.readLine()
		if err != nil {
			return m, err
		}
		switch answer = strings.ToLower(answer); answer {
		case "", "a":
			return m, nil
		case "c":
			return m, errors.New("canceled; nothing was changed")
		}
		i, err := strconv.Atoi(answer)
		if err != nil || i < 1 || i > len(rows) {
			continue
		}
		fmt.Fprintf(errOut, "Role for %s (%s, other, or leave): ", rows[i-1][0], strings.Join(keys, ", "))
		role, err := a.readLine()
		if err != nil {
			return m, err
		}
		if role != "other" && role != "leave" && !contains(keys, role) {
			fmt.Fprintf(errOut, "%q is not a role. Nothing changed.\n", role)
			continue
		}
		m = rules.SetRole(m, rows[i-1][0], role)
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func structureBody(name, description string, m adopt.Mapping) cloud.NewStructure {
	s := cloud.NewStructure{Name: name, Description: description, Directories: m.Directories}
	if len(m.ExtraDirectories) > 0 {
		s.ExtraDirectories = map[string]cloud.ExtraDirectory{}
		for k, e := range m.ExtraDirectories {
			s.ExtraDirectories[k] = cloud.ExtraDirectory{Label: e.Label, Path: e.Path}
		}
	}
	return s
}

// uploadStructure saves the mapping as a private structure on the server.
func uploadStructure(cmd *cobra.Command, client *cloud.Client, name, description string, m adopt.Mapping) (cloud.CreatedStructure, error) {
	created, err := client.CreateStructure(cmd.Context(), structureBody(name, description, m))
	if err != nil {
		return created, explain(err, client.URL)
	}
	return created, nil
}

func newStructureImportCmd(a *app) *cobra.Command {
	var name, description, lang string
	var yes bool
	cmd := &cobra.Command{
		Use:   "import [DIR]",
		Short: "Save a folder's layout as a structure on Framework Cloud",
		Long: "Maps the folders of DIR (default: the current folder) to Framework's\n" +
			"directories, the same way as fw adopt, and saves the layout as a structure\n" +
			"on Framework Cloud, private to you. fw looks at folder names only, at most\n" +
			"three levels deep, and sends only the folder paths and their roles: never\n" +
			"file names or contents. Nothing in the folder changes.\n\n" +
			"At a terminal fw shows the proposed mapping and lets you change a folder's\n" +
			"role before saving. Without one, pass --yes after reading it. With --json\n" +
			"and no --yes, fw prints the proposed mapping and saves nothing.",
		Example: "  fw structures import ~/projects/old-study --name \"Lab layout\"\n" +
			"  fw structures import --json\n" +
			"  fw structures import --yes --json",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if lang != "" && lang != "r" && lang != "python" && lang != "stata" {
				return usageError("--lang takes r, python, or stata")
			}
			urlFlag, _ := cmd.Flags().GetString("url")
			client, err := a.userClient(urlFlag)
			if err != nil {
				return err
			}
			dir, err := a.projectDir(args)
			if err != nil {
				return err
			}
			if lang == "" {
				if lang, _ = adopt.DetectLanguage(dir); lang == "" {
					lang = "r"
				}
			}
			if name == "" {
				name = filepath.Base(dir)
			}
			rules := adopt.Shared()
			folders, err := rules.Scan(dir)
			if err != nil {
				return err
			}
			m := rules.Map(folders, lang)
			if a.jsonOut && !yes {
				return writeJSON(cmd.OutOrStdout(), map[string]any{"name": name, "language": lang, "saved": false,
					"directories": m.Directories, "extra_directories": m.ExtraDirectories, "warnings": m.Warnings})
			}
			if m, err = a.reviewMapping(cmd, rules, m, lang, yes); err != nil {
				return err
			}
			created, err := uploadStructure(cmd, client, name, description, m)
			if err != nil {
				return err
			}
			if a.jsonOut {
				return writeJSON(cmd.OutOrStdout(), map[string]any{"key": created.Key, "name": created.Name, "url": created.URL, "saved": true,
					"directories": m.Directories, "extra_directories": m.ExtraDirectories, "warnings": m.Warnings})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Saved the structure %s on %s, private to you: %s\nUse it with: fw new NAME --structure %s\n",
				created.Key, cloud.Host(client.URL), created.URL, created.Key)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&name, "name", "", "the structure's name (default: the folder's name)")
	f.StringVar(&description, "description", "", "a description for the structure")
	f.StringVar(&lang, "lang", "", "map folders for this language: r, python, or stata (default: detected, else r)")
	f.BoolVarP(&yes, "yes", "y", false, "accept the proposed mapping and save it without asking")
	f.BoolVar(&a.jsonOut, "json", false, jsonHelp)
	return cmd
}

// adoptTypes are the project types fw adopt takes. course and presentation
// projects are made new, not adopted.
var adoptTypes = []string{"project", "project_sensitive", "bare"}

func newAdoptCmd(a *app) *cobra.Command {
	var lang string
	var yes bool
	var o engine.AdoptOptions
	cmd := &cobra.Command{
		Use:   "adopt [DIR]",
		Short: "Make an existing folder a Framework project, without moving anything",
		Long: "Turns DIR (default: the current folder) into a Framework project in place.\n" +
			"fw looks at folder names only, at most three levels deep, proposes which\n" +
			"Framework directory each one is, and lets you change a folder's role at a\n" +
			"terminal. Without one, pass --yes after reading the proposal.\n\n" +
			"For a Stata project fw writes settings.do and .framework/spec.json and\n" +
			"nothing else: it creates, moves, and overwrites no files, and stops if the\n" +
			"folder is already a project. The project stays on this machine; run\n" +
			"fw project push-spec to send its spec to Framework Cloud. Python projects\n" +
			"are adopted by framework-py (0.1.0.dev10 or later), and R projects by the\n" +
			"framework R package's adopt(). fw runs them for you. To change a folder's\n" +
			"role in an R project, run framework::adopt() in R instead.\n\n" +
			"fw detects the language from the files at the top of the folder: .do files\n" +
			"for Stata, pyproject.toml for Python, and renv.lock, DESCRIPTION, or an\n" +
			".Rproj file for R. Without those, setup.py, setup.cfg, requirements*.txt,\n" +
			"or .ipynb files mean Python, and .R, .Rmd, or .qmd files mean R (a .qmd\n" +
			"with Python chunks means Python). fw says which file decided, and --lang\n" +
			"overrides it.\n\n" +
			"With --json and no --yes, fw prints the proposal as JSON and changes\n" +
			"nothing. With --yes --json, it prints what was adopted: the files it\n" +
			"wrote and the directories recorded in .framework/spec.json.",
		Example: "  fw adopt ~/projects/old-study --lang stata\n" +
			"  fw adopt --json\n" +
			"  fw adopt --yes",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if lang != "" && lang != "r" && lang != "python" && lang != "stata" {
				return usageError("--lang takes r, python, or stata")
			}
			if o.Type != "" && !contains(adoptTypes, o.Type) {
				return usageError("--type takes project, project_sensitive, or bare")
			}
			if o.Type == "project_sensitive" {
				o.Sensitive = true
			}
			dir, err := a.projectDir(args)
			if err != nil {
				return err
			}
			// Before any scan or proposal: an existing project is upgraded, not adopted.
			if found := adopt.Existing(dir); found != "" {
				return alreadyProject(dir, found)
			}
			source := "--lang"
			if lang == "" {
				if lang, source = adopt.DetectLanguage(dir); lang != "" {
					fmt.Fprintf(cmd.ErrOrStderr(), "Language: %s (from %s). --lang overrides it.\n", lang, source)
				}
			}
			if lang == "" {
				if !a.interactive {
					return usageError("cannot tell the project's language from its files. Pass --lang r, python, or stata")
				}
				fmt.Fprint(cmd.ErrOrStderr(), "Which language is this project: r, python, or stata? ")
				if lang, err = a.readLine(); err != nil {
					return err
				}
				if lang != "r" && lang != "python" && lang != "stata" {
					return usageError("the language must be r, python, or stata")
				}
				source = "your answer"
			}
			if a.jsonOut && !yes {
				return a.adoptProposal(cmd, dir, lang, source, o)
			}
			switch lang {
			case "r":
				return a.adoptR(cmd, dir, yes, o)
			case "python":
				return a.adoptPython(cmd, dir, yes, o)
			}
			return a.adoptStata(cmd, dir, yes, o)
		},
	}
	f := cmd.Flags()
	f.StringVar(&lang, "lang", "", "the project's language: r, python, or stata (default: detected)")
	f.BoolVarP(&yes, "yes", "y", false, "accept the proposed mapping without asking")
	f.StringVar(&o.Type, "type", "", "the project type: project, project_sensitive, or bare (default: the language package's choice; bare for Stata)")
	f.BoolVar(&o.Sensitive, "sensitive", false, "the project holds sensitive data: turn on Framework's guards for private folders")
	f.BoolVar(&a.jsonOut, "json", false, jsonHelp)
	return cmd
}

// sensitiveHint tells someone adopting without --sensitive that folders
// look private, since nothing asks when fw runs the package for them.
func sensitiveHint(cmd *cobra.Command, m adopt.Mapping, o engine.AdoptOptions) {
	if len(m.Warnings) > 0 && !o.Sensitive {
		fmt.Fprintln(cmd.ErrOrStderr(), "Some folders look private, and the project is adopted as not sensitive. If it holds sensitive data, "+
			"add --sensitive to turn on Framework's guards (or set security: sensitive: true in settings.yml later).")
	}
}

// alreadyProject is fw adopt's error for a folder that is already a
// Framework project. With --json it names the file found and the command to
// run instead.
func alreadyProject(dir, found string) error {
	msg := fmt.Sprintf("%s is already a Framework project (found %s). Nothing was changed. To bring it to the current layout, run fw project upgrade in it", dir, found)
	if found != ".framework/spec.json" {
		msg = fmt.Sprintf("%s is already a Framework project, made before Framework 2 (found %s). Nothing was changed. Run fw project upgrade in it to bring it to the current layout", dir, found)
	}
	return &exitError{code: exitFailure, err: errors.New(msg), reason: "already_framework", found: found, hint: "fw project upgrade"}
}

// adoptR shows the proposed mapping, which fw and R make from the same
// rules, and has the framework R package's adopt() write the project. R
// reviews a mapping only in an R session, so changing a folder's role needs
// framework::adopt() in R.
func (a *app) adoptR(cmd *cobra.Command, dir string, yes bool, o engine.AdoptOptions) error {
	command, err := a.engines.AdoptR(dir, o)
	if err != nil {
		return err
	}
	rules := adopt.Shared()
	folders, err := rules.Scan(dir)
	if err != nil {
		return err
	}
	m := rules.Map(folders, "r")
	if !yes {
		errOut := cmd.ErrOrStderr()
		if m.Empty() {
			return errors.New("no folders found to map. Nothing was changed")
		}
		for _, w := range m.Warnings {
			fmt.Fprintf(errOut, "Warning: %s\n", w.Text())
		}
		fmt.Fprintln(errOut, "Proposed mapping:")
		for i, row := range rules.Rows(m) {
			fmt.Fprintf(errOut, "  %2d. %-30s %s\n", i+1, row[0], row[1])
		}
		fmt.Fprintf(errOut, "To change a folder's role, run framework::adopt(%q) in R.\n", dir)
		if !a.interactive {
			return usageError("no terminal to review the mapping from. After reading it above, rerun with --yes to accept it")
		}
		ok, err := a.askYes(cmd, "Accept this mapping?")
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("canceled; nothing was changed")
		}
	}
	sensitiveHint(cmd, m, o)
	return a.runAdopt(cmd, command, dir, "r", "framework::adopt()")
}

// adoptPython hands the folder to framework-py's adopt.
func (a *app) adoptPython(cmd *cobra.Command, dir string, yes bool, o engine.AdoptOptions) error {
	command, err := a.engines.AdoptPython(dir, yes, o)
	if err != nil {
		return err
	}
	rules := adopt.Shared()
	if folders, err := rules.Scan(dir); err == nil && yes {
		sensitiveHint(cmd, rules.Map(folders, "python"), o)
	}
	return a.runAdopt(cmd, command, dir, "python", "framework-py")
}

// runAdopt runs a language package's adopt. With --json its output goes to
// stderr, and fw prints what it wrote and recorded.
func (a *app) runAdopt(cmd *cobra.Command, command engine.Command, dir, lang, what string) error {
	pkg := map[string]string{"r": "the framework R package", "python": "framework-py"}[lang]
	fmt.Fprintf(cmd.ErrOrStderr(), "Adopting %s with %s\n", dir, pkg)
	before := projectFiles(dir)
	command.StdoutToStderr = a.jsonOut
	code, err := a.run(command)
	if err != nil {
		return err
	}
	if code != 0 {
		return &exitError{code: code, err: fmt.Errorf("%s exited with status %d", what, code)}
	}
	if !a.jsonOut {
		return nil
	}
	return writeJSON(cmd.OutOrStdout(), adoptedResult(dir, lang, before))
}

// projectFiles lists the files and folders at the top of dir and in
// .framework, which is where adopt writes.
func projectFiles(dir string) map[string]bool {
	found := map[string]bool{}
	for _, sub := range []string{"", ".framework"} {
		entries, _ := os.ReadDir(filepath.Join(dir, sub))
		for _, e := range entries {
			found[path.Join(sub, e.Name())] = true
		}
	}
	return found
}

// adoptedResult is fw adopt --yes --json's output for R and Python: the
// files that are new since before, and what .framework/spec.json records.
func adoptedResult(dir, lang string, before map[string]bool) map[string]any {
	created := []string{}
	for f := range projectFiles(dir) {
		if !before[f] && f != ".framework" {
			created = append(created, f)
		}
	}
	sort.Strings(created)
	res := map[string]any{"path": dir, "language": lang, "created": created}
	var doc struct {
		Type             string         `json:"type"`
		Directories      map[string]any `json:"directories"`
		ExtraDirectories map[string]any `json:"extra_directories"`
		Security         struct {
			Sensitive bool `json:"sensitive"`
		} `json:"security"`
	}
	if data, err := os.ReadFile(filepath.Join(dir, ".framework", "spec.json")); err == nil && json.Unmarshal(data, &doc) == nil {
		res["type"], res["directories"], res["extra_directories"], res["sensitive"] = doc.Type, doc.Directories, doc.ExtraDirectories, doc.Security.Sensitive
	}
	return res
}

// adoptProposal prints fw adopt --json's proposal without changing
// anything: the mapping fw and the language packages make from the shared
// rules.
func (a *app) adoptProposal(cmd *cobra.Command, dir, lang, source string, o engine.AdoptOptions) error {
	rules := adopt.Shared()
	folders, err := rules.Scan(dir)
	if err != nil {
		return err
	}
	m := rules.Map(folders, lang)
	return writeJSON(cmd.OutOrStdout(), map[string]any{
		"path": dir, "language": lang, "language_source": source, "adopted": false, "type": proposedType(lang, o), "sensitive": o.Sensitive,
		"directories": m.Directories, "extra_directories": m.ExtraDirectories, "warnings": m.Warnings,
	})
}

// proposedType is the type a proposal shows: --type, or bare for Stata.
// Without --type, the R and Python packages choose, so it is null.
func proposedType(lang string, o engine.AdoptOptions) any {
	switch {
	case o.Type != "":
		return o.Type
	case lang == "stata":
		return "bare"
	}
	return nil
}

func (a *app) adoptStata(cmd *cobra.Command, dir string, yes bool, o engine.AdoptOptions) error {
	if err := stata.CheckAdoptable(dir); err != nil {
		return err
	}
	rules := adopt.Shared()
	folders, err := rules.Scan(dir)
	if err != nil {
		return err
	}
	m, err := a.reviewMapping(cmd, rules, rules.Map(folders, "stata"), "stata", yes)
	if err != nil {
		return err
	}
	sensitive := o.Sensitive
	if !sensitive && !yes && a.interactive && len(m.Warnings) > 0 {
		if sensitive, err = a.askYes(cmd, "Does this project hold sensitive data? Framework will treat its outputs as private by default."); err != nil {
			return err
		}
	}

	extras := map[string]any{}
	for k, e := range m.ExtraDirectories {
		extras[k] = map[string]any{"label": e.Label, "path": e.Path}
	}
	if yes {
		sensitiveHint(cmd, m, o)
	}
	layer, err := json.Marshal(stata.AdoptLayer(m.Directories, extras, sensitive))
	if err != nil {
		return err
	}
	name := filepath.Base(dir)
	env := spec.Envelope{SpecVersion: 1, Name: name, Layers: []spec.Entry{{Kind: "project", Key: "adopted", Layer: layer}},
		Source: json.RawMessage(`{"origin":"cli","adopted":true}`)}
	resolved, err := stata.Resolve(env, version)
	if err != nil {
		return err
	}
	resolved.Doc["skills"] = map[string]any{}
	// The layer resolves as bare, so no built-in folders are added; --type
	// changes only the label.
	if o.Type != "" {
		resolved.Doc["type"], resolved.View.Type = o.Type, o.Type
	}
	created, err := stata.Adopt(resolved, dir)
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	if a.jsonOut {
		return writeJSON(out, map[string]any{"path": dir, "language": "stata", "created": created,
			"directories": m.Directories, "extra_directories": m.ExtraDirectories, "warnings": m.Warnings})
	}
	fmt.Fprintf(out, "%s is a Framework Stata project. fw wrote %s and changed nothing else.\n", name, strings.Join(created, " and "))
	fmt.Fprintln(out, "Next: open Stata in that folder and run: do settings.do")
	if len(m.Warnings) > 0 {
		fmt.Fprintln(out, "Some folders look private. Keep them out of git, and consider the Privacy Sensitive structure.")
	}
	if !yes && a.interactive {
		if client, err := a.userClient(""); err == nil {
			save, err := a.askYes(cmd, fmt.Sprintf("Save this layout as a structure on %s?", cloud.Host(client.URL)))
			if err != nil || !save {
				return err
			}
			created, err := uploadStructure(cmd, client, name, "", m)
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "Saved the structure %s: %s\n", created.Key, created.URL)
		}
	}
	return nil
}
