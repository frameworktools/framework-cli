package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/frameworktools/framework-cli/internal/adopt"
	"github.com/frameworktools/framework-cli/internal/cloud"
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
				if lang = adopt.DetectLanguage(dir); lang == "" {
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

func newAdoptCmd(a *app) *cobra.Command {
	var lang string
	var yes bool
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
			"are adopted by framework-py (0.1.0.dev4 or later), which fw runs for you.\n" +
			"R projects need framework::adopt() in R for now.\n\n" +
			"fw detects the language from the files at the top of the folder (.do files\n" +
			"for Stata, pyproject.toml for Python, renv.lock, DESCRIPTION, or an .Rproj\n" +
			"file for R). --lang overrides it.",
		Example: "  fw adopt ~/projects/old-study --lang stata\n" +
			"  fw adopt --yes",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if lang != "" && lang != "r" && lang != "python" && lang != "stata" {
				return usageError("--lang takes r, python, or stata")
			}
			dir, err := a.projectDir(args)
			if err != nil {
				return err
			}
			if lang == "" {
				lang = adopt.DetectLanguage(dir)
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
			}
			switch lang {
			case "r":
				return fmt.Errorf("fw adopt hands R projects to the framework R package, which needs version 2.0.0. Until fw supports it, run framework::adopt(%q) in R", dir)
			case "python":
				command, err := a.engines.AdoptPython(dir, yes)
				if err != nil {
					return err
				}
				fmt.Fprintf(cmd.ErrOrStderr(), "Adopting %s with framework-py\n", dir)
				code, err := a.run(command)
				if err != nil {
					return err
				}
				if code != 0 {
					return &exitError{code: code, err: fmt.Errorf("framework-py exited with status %d", code)}
				}
				return nil
			}
			return a.adoptStata(cmd, dir, yes)
		},
	}
	f := cmd.Flags()
	f.StringVar(&lang, "lang", "", "the project's language: r, python, or stata (default: detected)")
	f.BoolVarP(&yes, "yes", "y", false, "accept the proposed mapping without asking")
	f.BoolVar(&a.jsonOut, "json", false, jsonHelp)
	return cmd
}

func (a *app) adoptStata(cmd *cobra.Command, dir string, yes bool) error {
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
	sensitive := false
	if !yes && a.interactive && len(m.Warnings) > 0 {
		if sensitive, err = a.askYes(cmd, "Does this project hold sensitive data? Framework will treat its outputs as private by default."); err != nil {
			return err
		}
	}

	extras := map[string]any{}
	for k, e := range m.ExtraDirectories {
		extras[k] = map[string]any{"label": e.Label, "path": e.Path}
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
