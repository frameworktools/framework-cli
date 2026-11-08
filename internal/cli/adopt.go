package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/frameworktools/framework-cli/internal/adopt"
	"github.com/frameworktools/framework-cli/internal/cloud"
	"github.com/frameworktools/framework-cli/internal/engine"
	"github.com/frameworktools/framework-cli/internal/project"
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
		// A project of loose files, such as one analysis.qmd, has no folders
		fmt.Fprintln(errOut, "No folders to map: the project is recorded with no directories. Add folders to its settings later.")
		if yes {
			return m, nil
		}
		if !a.interactive {
			return m, usageError("no terminal to confirm from. Rerun with --yes to adopt the project with no directories")
		}
		ok, err := a.askYes(cmd, "Adopt it with no directories?")
		if err != nil {
			return m, err
		}
		if !ok {
			return m, errors.New("canceled; nothing was changed")
		}
		return m, nil
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
	keys := rules.Keys(language, m.Type)
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

func structureBody(name, description, kind string, m adopt.Mapping) cloud.NewStructure {
	s := cloud.NewStructure{Name: name, Description: description, Type: kind, Directories: m.Directories}
	if len(m.ExtraDirectories) > 0 {
		s.ExtraDirectories = map[string]cloud.ExtraDirectory{}
		for k, e := range m.ExtraDirectories {
			s.ExtraDirectories[k] = cloud.ExtraDirectory{Label: e.Label, Path: e.Path}
		}
	}
	return s
}

// uploadStructure saves the mapping as a private structure on the server.
// kind is the project type it is for; empty lets the server choose project.
func uploadStructure(cmd *cobra.Command, client *cloud.Client, name, description, kind string, m adopt.Mapping) (cloud.CreatedStructure, error) {
	created, err := client.CreateStructure(cmd.Context(), structureBody(name, description, kind, m))
	if err != nil {
		return created, explain(err, client.URL)
	}
	return created, nil
}

func newStructureImportCmd(a *app) *cobra.Command {
	var name, description, lang, kind string
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
			if kind != "" && !contains(structureTypes, kind) {
				return usageError("--type takes " + strings.Join(structureTypes, ", "))
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
			m := mapFolders(rules, folders, lang, kind)
			if m.Empty() {
				return errors.New("no folders found to map. Nothing was saved")
			}
			if a.jsonOut && !yes {
				return writeJSON(cmd.OutOrStdout(), map[string]any{"name": name, "language": lang, "saved": false, "type": m.Type,
					"directories": m.Directories, "extra_directories": m.ExtraDirectories, "warnings": m.Warnings})
			}
			if m, err = a.reviewMapping(cmd, rules, m, lang, yes); err != nil {
				return err
			}
			created, err := uploadStructure(cmd, client, name, description, m.Type, m)
			if err != nil {
				return err
			}
			if a.jsonOut {
				return writeJSON(cmd.OutOrStdout(), map[string]any{"key": created.Key, "name": created.Name, "url": created.URL, "saved": true, "type": m.Type,
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
	f.StringVar(&kind, "type", "", "the project type the structure is for, such as bare or project_sensitive (default: inferred from the folders)")
	f.BoolVarP(&yes, "yes", "y", false, "accept the proposed mapping and save it without asking")
	f.BoolVar(&a.jsonOut, "json", false, jsonHelp)
	return cmd
}

// structureTypes are the project types a structure can be for, as
// Framework Cloud lists them.
var structureTypes = []string{"project", "project_sensitive", "course", "presentation", "shiny", "plumber", "shiny_python", "fastapi", "bare"}

// adoptTypes are the project types fw adopt takes; Stata projects take only
// project, project_sensitive, and bare.
var adoptTypes = []string{"project", "project_sensitive", "course", "presentation", "bare"}

func newAdoptCmd(a *app) *cobra.Command {
	var lang string
	var yes bool
	var o engine.AdoptOptions
	var toCloud bool
	var co createOptions
	cmd := &cobra.Command{
		Use:   "adopt [DIR]",
		Short: "Make an existing folder a Framework project, without moving anything",
		Long: "Turns DIR (default: the current folder) into a Framework project in place.\n" +
			"fw looks at folder names only, at most three levels deep, proposes which\n" +
			"Framework directory each one is, and lets you change a folder's role at a\n" +
			"terminal. Without one, pass --yes after reading the proposal.\n\n" +
			"For a Stata project fw writes settings.do, settings.yml, and\n" +
			".framework/spec.json, and adds lines that keep secrets and private\n" +
			"folders out of git to .gitignore. It moves and overwrites no files, never\n" +
			"changes a line already in .gitignore, and stops if the folder is already\n" +
			"a project. A .gitignore that already has rules gets no negations (!...),\n" +
			"which would win over them; fw lists the ones it left out. The spec\n" +
			"records tools.quarto when a _quarto.yml, _quarto.yaml, or .qmd file is\n" +
			"at the top or in a folder fw scans. Python projects are adopted by\n" +
			"framework-py (0.1.0.dev13 or later), and R projects by the framework R\n" +
			"package's adopt(). fw runs them for you. To change a folder's role in an\n" +
			"R project, run framework::adopt() in R instead. An existing Framework\n" +
			"project is not adopted: run fw project upgrade in it.\n\n" +
			"fw infers the type from the folders: course when two or more course\n" +
			"folders (slides, modules, assignments, ...) are at the top, project_sensitive\n" +
			"when private and public data folders map, project when any folder maps,\n" +
			"and bare otherwise. --type sets it (project, project_sensitive, course,\n" +
			"presentation, or bare; Stata takes the first two and bare), and\n" +
			"--sensitive turns on Framework's guards for private data. Both need an R\n" +
			"or Python package recent enough to take them; fw says when it isn't.\n\n" +
			"In a sensitive project fw lists the folders that may hold restricted\n" +
			"data (inputs and outputs folders, data, cache, scratch, and folders named\n" +
			"like data, exports, reports, results, raw, tables, figures, or cohorts)\n" +
			"and, at a terminal, asks about each. --private FOLDER, repeatable, marks a\n" +
			"folder without asking: it keeps its path and gets a private key\n" +
			"(inputs_raw becomes inputs_private_raw; another folder becomes\n" +
			"outputs_private_<name> or inputs_private_<name>), and .gitignore keeps it\n" +
			"out of git. --private needs a framework package newer than 2.0.1 in R,\n" +
			"and a framework-py newer than 0.1.0.dev13 in Python.\n\n" +
			"The project stays on this machine unless you pass --cloud, which then\n" +
			"creates it on Framework Cloud from its spec, links it with a project token\n" +
			"in .env, and sends the spec, as fw project link --create does later.\n\n" +
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
			"  fw adopt --yes --type project --sensitive\n" +
			"  fw adopt --yes --sensitive --private reports --private data-files\n" +
			"  fw adopt --yes --cloud",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if lang != "" && lang != "r" && lang != "python" && lang != "stata" {
				return usageError("--lang takes r, python, or stata")
			}
			if o.Type != "" && !contains(adoptTypes, o.Type) {
				return usageError("--type takes project, project_sensitive, course, presentation, or bare")
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
			if detected, from := adopt.DetectLanguage(dir); lang != "" && detected != "" && detected != lang {
				fmt.Fprintf(cmd.ErrOrStderr(), "Warning: the files say %s (from %s), but --lang says %s. Adopting it as %s.\n", detected, from, lang, lang)
			}
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
			if lang == "stata" && (o.Type == "course" || o.Type == "presentation") {
				return usageError("Stata projects take --type project, project_sensitive, or bare")
			}
			for i, p := range o.Private {
				o.Private[i] = strings.TrimRight(strings.TrimPrefix(filepath.ToSlash(p), "./"), "/")
			}
			if a.jsonOut && !yes {
				return a.adoptProposal(cmd, dir, lang, source, o)
			}
			var res map[string]any
			switch lang {
			case "r":
				res, err = a.adoptR(cmd, dir, yes, o)
			case "python":
				// --lang python on a folder whose files say R or Stata is a
				// deliberate choice, so framework-py should not refuse it.
				o.Force = cmd.Flags().Changed("lang")
				res, err = a.adoptPython(cmd, dir, yes, o)
			default:
				res, err = a.adoptStata(cmd, dir, yes, o, toCloud)
			}
			if err != nil {
				return err
			}
			if toCloud {
				link, err := a.createLink(cmd, dir, co)
				if err != nil {
					return fmt.Errorf("%s is adopted, but creating it on Framework Cloud failed: %w", dir, err)
				}
				res["cloud"] = link
			}
			if a.jsonOut {
				return writeJSON(cmd.OutOrStdout(), res)
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&lang, "lang", "", "the project's language: r, python, or stata (default: detected)")
	f.BoolVarP(&yes, "yes", "y", false, "accept the proposed mapping without asking")
	f.StringVar(&o.Type, "type", "", "the project type: project, project_sensitive, course, presentation, or bare (default: inferred from the folders)")
	f.BoolVar(&o.Sensitive, "sensitive", false, "the project holds sensitive data: turn on Framework's guards for private folders")
	f.StringArrayVar(&o.Private, "private", nil, "map `FOLDER`, a path from the project folder, to a private key, keeping its path; repeat for more")
	f.BoolVar(&toCloud, "cloud", false, "after adopting, create the project on Framework Cloud and link it, as fw project link --create does")
	f.StringVar(&co.slug, "slug", "", "with --cloud: the project's slug on Framework Cloud (default: made from the folder's name)")
	f.BoolVar(&co.includePrivate, "include-private", false, "with --cloud: send the spec without asking when the project's path contains \"private\"")
	f.StringVar(&co.url, "url", "", "with --cloud: the Framework Cloud server")
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
func (a *app) adoptR(cmd *cobra.Command, dir string, yes bool, o engine.AdoptOptions) (map[string]any, error) {
	command, err := a.engines.AdoptR(dir, o)
	if err != nil {
		return nil, err
	}
	rules := adopt.Shared()
	folders, err := rules.Scan(dir)
	if err != nil {
		return nil, err
	}
	m := mapFolders(rules, folders, "r", o.Type)
	if m, err = markPrivate(rules, m, o.Private); err != nil {
		return nil, err
	}
	if !yes {
		errOut := cmd.ErrOrStderr()
		if m.Empty() {
			fmt.Fprintln(errOut, "No folders to map: the project is recorded with no directories.")
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
			return nil, usageError("no terminal to review the mapping from. After reading it above, rerun with --yes to accept it")
		}
		ok, err := a.askYes(cmd, "Accept this mapping?")
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, errors.New("canceled; nothing was changed")
		}
		// R asks about these only in an R session, so fw asks and passes
		// the answers on as private = ...
		_, marked, err := a.offerPrivate(cmd, rules, m, o.Sensitive || m.Type == "project_sensitive", false)
		if err != nil {
			return nil, err
		}
		if len(marked) > 0 {
			o.Private = append(o.Private, marked...)
			if command, err = a.engines.AdoptR(dir, o); err != nil {
				return nil, err
			}
		}
	}
	sensitiveHint(cmd, m, o)
	return a.runAdopt(cmd, command, dir, "r", "framework::adopt()")
}

// adoptPython hands the folder to framework-py's adopt.
func (a *app) adoptPython(cmd *cobra.Command, dir string, yes bool, o engine.AdoptOptions) (map[string]any, error) {
	command, err := a.engines.AdoptPython(dir, yes, o)
	if err != nil {
		return nil, err
	}
	rules := adopt.Shared()
	folders, err := rules.Scan(dir)
	if err != nil {
		return nil, err
	}
	m, err := markPrivate(rules, mapFolders(rules, folders, "python", o.Type), o.Private)
	if err != nil {
		return nil, err
	}
	// Without --yes, framework-py reviews the mapping and asks itself
	if yes {
		sensitiveHint(cmd, m, o)
		if _, _, err := a.offerPrivate(cmd, rules, m, o.Sensitive || m.Type == "project_sensitive", true); err != nil {
			return nil, err
		}
	}
	return a.runAdopt(cmd, command, dir, "python", "framework-py")
}

// runAdopt runs a language package's adopt and returns what it wrote and
// recorded. With --json the package's output goes to stderr.
func (a *app) runAdopt(cmd *cobra.Command, command engine.Command, dir, lang, what string) (map[string]any, error) {
	pkg := map[string]string{"r": "the framework R package", "python": "framework-py"}[lang]
	fmt.Fprintf(cmd.ErrOrStderr(), "Adopting %s with %s\n", dir, pkg)
	before := projectFiles(dir)
	command.StdoutToStderr = a.jsonOut
	code, err := a.run(command)
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, &exitError{code: code, err: fmt.Errorf("%s exited with status %d", what, code)}
	}
	return adoptedResult(dir, lang, before), nil
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
	m, err := markPrivate(rules, mapFolders(rules, folders, lang, o.Type), o.Private)
	if err != nil {
		return err
	}
	sensitive := o.Sensitive || m.Type == "project_sensitive"
	candidates := []string{}
	if sensitive {
		candidates = nonNil(rules.RestrictedCandidates(m))
	}
	res := map[string]any{
		"path": dir, "language": lang, "language_source": source, "adopted": false, "type": m.Type, "sensitive": sensitive,
		"directories": m.Directories, "extra_directories": m.ExtraDirectories, "render_dirs": m.RenderDirs, "warnings": m.Warnings,
		"marked_private": nonNil(m.MarkedPrivate), "restricted_candidates": candidates, "quarto": rules.DetectQuarto(dir),
	}
	// fw writes Stata projects itself, so it knows what .gitignore gets
	if lang == "stata" {
		missing, leftOut, err := project.MissingGitignoreLines(dir, stataIgnores(m))
		if err != nil {
			return err
		}
		res["gitignore_additions"], res["gitignore_left_out"] = nonNil(missing), nonNil(leftOut)
	}
	return writeJSON(cmd.OutOrStdout(), res)
}

// stataIgnores lists the .gitignore lines fw adopt adds to a Stata project.
func stataIgnores(m adopt.Mapping) []string {
	private := make([]string, 0, len(m.Warnings)+len(m.MarkedPrivate))
	for _, w := range m.Warnings {
		private = append(private, w.Path)
	}
	for _, p := range m.MarkedPrivate {
		if !slices.Contains(private, p) {
			private = append(private, p)
		}
	}
	return stata.AdoptGitignore(private)
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// mapFolders maps a project's folders with the shared rules. kind is the
// type given with --type, or "" to infer it. Stata has no course type, so a
// Stata folder that looks like a course maps as a project. In Stata, which
// has no rendered notebooks, render folders become other folders.
func mapFolders(rules adopt.Rules, folders []string, lang, kind string) adopt.Mapping {
	m := rules.Map(folders, lang, kind)
	if lang != "stata" {
		return m
	}
	if m.Type == "course" || m.Type == "presentation" {
		m = rules.Map(folders, lang, "project")
	}
	for _, p := range m.RenderDirs {
		m = rules.SetRole(m, p, "other")
	}
	return m
}

// adoptStata writes a Stata project itself. toCloud skips the offer to save
// the layout as a structure, since fw adopt --cloud creates the project.
func (a *app) adoptStata(cmd *cobra.Command, dir string, yes bool, o engine.AdoptOptions, toCloud bool) (map[string]any, error) {
	if err := stata.CheckAdoptable(dir); err != nil {
		return nil, err
	}
	rules := adopt.Shared()
	folders, err := rules.Scan(dir)
	if err != nil {
		return nil, err
	}
	m, err := markPrivate(rules, mapFolders(rules, folders, "stata", o.Type), o.Private)
	if err != nil {
		return nil, err
	}
	if m, err = a.reviewMapping(cmd, rules, m, "stata", yes); err != nil {
		return nil, err
	}
	sensitive := o.Sensitive || m.Type == "project_sensitive"
	if !sensitive && !yes && a.interactive && len(m.Warnings) > 0 {
		if sensitive, err = a.askYes(cmd, "Does this project hold sensitive data? Framework will treat its outputs as private by default."); err != nil {
			return nil, err
		}
	}
	if m, _, err = a.offerPrivate(cmd, rules, m, sensitive, yes); err != nil {
		return nil, err
	}

	extras := map[string]any{}
	for k, e := range m.ExtraDirectories {
		extras[k] = map[string]any{"label": e.Label, "path": e.Path}
	}
	if yes {
		sensitiveHint(cmd, m, o)
	}
	gitInfo, gitErr := os.Stat(filepath.Join(dir, ".git"))
	layer, err := json.Marshal(stata.AdoptLayer(m.Directories, extras, sensitive, gitErr == nil && gitInfo != nil))
	if err != nil {
		return nil, err
	}
	name := filepath.Base(dir)
	env := spec.Envelope{SpecVersion: 1, Name: name, Layers: []spec.Entry{{Kind: "project", Key: "adopted", Layer: layer}},
		Source: json.RawMessage(`{"origin":"cli","adopted":true}`)}
	resolved, err := stata.Resolve(env, version)
	if err != nil {
		return nil, err
	}
	resolved.Doc["skills"] = map[string]any{}
	// Quarto files anywhere adopt looks: recorded, as R's adopt() does. fw
	// writes nothing for Quarto in a Stata project.
	quarto := rules.DetectQuarto(dir)
	if quarto {
		tools := map[string]any{}
		if t, ok := resolved.Doc["tools"].(map[string]any); ok {
			tools = t
		}
		tools["quarto"] = true
		resolved.Doc["tools"] = tools
	}
	// The layer resolves as bare, so no built-in folders are added; the
	// given or inferred type is the label.
	resolved.Doc["type"], resolved.View.Type = m.Type, m.Type
	res, err := stata.Adopt(resolved, dir, stataIgnores(m))
	if err != nil {
		return nil, err
	}

	out := cmd.OutOrStdout()
	result := map[string]any{"path": dir, "language": "stata", "type": resolved.View.Type, "sensitive": sensitive,
		"created": res.Created, "gitignore_additions": nonNil(res.Gitignore), "gitignore_left_out": nonNil(res.GitignoreLeftOut),
		"directories": m.Directories, "extra_directories": m.ExtraDirectories, "warnings": m.Warnings, "marked_private": nonNil(m.MarkedPrivate), "quarto": quarto}
	if a.jsonOut {
		return result, nil
	}
	fmt.Fprintf(out, "%s is a Framework Stata project. fw wrote %s.\n", name, strings.Join(res.Created, ", "))
	if len(res.Gitignore) > 0 {
		fmt.Fprintf(out, "Added to .gitignore: %s\n", strings.Join(res.Gitignore, " "))
	}
	if len(res.GitignoreLeftOut) > 0 {
		fmt.Fprintf(out, "Left out of .gitignore: %s. Added after the folder's own rules, they could make files in ignored folders committable.\n", strings.Join(res.GitignoreLeftOut, " "))
	}
	fmt.Fprintln(out, "fw moved and overwrote nothing.")
	fmt.Fprintln(out, "Next: open Stata in that folder and run: do settings.do")
	if len(m.Warnings) > 0 && !sensitive {
		fmt.Fprintln(out, "Some folders look private. .gitignore now keeps them out of git; consider the Privacy Sensitive structure.")
	}
	if !yes && a.interactive && !toCloud {
		if client, err := a.userClient(""); err == nil {
			save, err := a.askYes(cmd, fmt.Sprintf("Save this layout as a structure on %s?", cloud.Host(client.URL)))
			if err != nil || !save {
				return result, err
			}
			created, err := uploadStructure(cmd, client, name, "", resolved.View.Type, m)
			if err != nil {
				return result, err
			}
			fmt.Fprintf(out, "Saved the structure %s: %s\n", created.Key, created.URL)
		}
	}
	return result, nil
}

// markPrivate maps each --private folder to a private key, as R's
// adopt(private = ...) does. A folder the mapping doesn't have is a usage
// error.
func markPrivate(rules adopt.Rules, m adopt.Mapping, private []string) (adopt.Mapping, error) {
	for _, p := range private {
		var err error
		if m, err = rules.MarkPrivate(m, p); err != nil {
			return m, usageError(err.Error() + ". --private takes a folder from that list")
		}
	}
	return m, nil
}

// restrictedHint is the line that lists folders that may hold restricted
// data in a sensitive adopt, with the flags that mark them.
func restrictedHint(candidates []string) string {
	flags := make([]string, len(candidates))
	for i, c := range candidates {
		flags[i] = "--private " + engine.Command{Program: c}.String()
	}
	return fmt.Sprintf("May hold restricted data (mark private?): %s. To map them to private keys, which Framework's checks and agent guards cover, add %s.",
		strings.Join(candidates, ", "), strings.Join(flags, " "))
}

// offerPrivate handles the folders that may hold restricted data in a
// sensitive adopt. At a terminal, without --yes, fw asks about each
// (default yes) and marks the ones the person accepts; otherwise it lists
// them with the flags that mark them. It returns the mapping and the
// folders marked here.
func (a *app) offerPrivate(cmd *cobra.Command, rules adopt.Rules, m adopt.Mapping, sensitive, yes bool) (adopt.Mapping, []string, error) {
	if !sensitive {
		return m, nil, nil
	}
	candidates := rules.RestrictedCandidates(m)
	if len(candidates) == 0 {
		return m, nil, nil
	}
	if yes || !a.interactive {
		fmt.Fprintln(cmd.ErrOrStderr(), restrictedHint(candidates))
		return m, nil, nil
	}
	var marked []string
	for _, c := range candidates {
		ok, err := a.askYesDefault(cmd, fmt.Sprintf("'%s' may hold restricted data. Mark it private?", c), true)
		if err != nil {
			return m, nil, err
		}
		if ok {
			if m, err = rules.MarkPrivate(m, c); err != nil {
				return m, nil, err
			}
			marked = append(marked, c)
		}
	}
	return m, marked, nil
}
