package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	pub "github.com/frameworktools/framework-cli/internal/publish"
	"github.com/frameworktools/framework-cli/internal/structure"
)

// structureFlags are the flags that choose and describe a project structure.
type structureFlags struct {
	depth                           int
	exclude, notes, open, highlight []string
	ignored                         string
}

func addStructureFlags(cmd *cobra.Command, s *structureFlags, publish bool) {
	with := ""
	if publish {
		with = "with --structure: "
	}
	cmd.Flags().IntVar(&s.depth, "depth", 0, with+"levels to list; deeper folders show a file count (default: no limit)")
	cmd.Flags().StringArrayVar(&s.exclude, "exclude", nil, with+"leave out paths matching a gitignore-style glob; repeatable")
	cmd.Flags().StringArrayVar(&s.notes, "note", nil, with+"PATH=TEXT, a note beside a path; repeatable")
	cmd.Flags().StringVar(&s.ignored, "ignored", "rules", with+"rules shows ignored paths as their rules and file counts; hide leaves them out")
	if publish {
		cmd.Flags().StringArrayVar(&s.open, "open", nil, with+"a folder that starts open in embeds; repeatable")
		cmd.Flags().StringArrayVar(&s.highlight, "highlight", nil, with+"a path drawn highlighted; repeatable")
	}
}

// structureFlagNames are the flags fw publish takes only with --structure.
var structureFlagNames = []string{"depth", "exclude", "note", "ignored", "open", "highlight"}

func newStructureCmd(a *app) *cobra.Command {
	var s structureFlags
	cmd := &cobra.Command{
		Use:   "structure [PATH...]",
		Args:  cobra.ArbitraryArgs,
		Short: "Print a project's folder tree, with what .gitignore hides",
		Long: "Prints the project's folder tree and uploads nothing. PATH names one\n" +
			"folder or several; the default is the project root, or the current folder\n" +
			"outside a project.\n\n" +
			"Ignored files are listed by name with the rule that hides them, up to 20\n" +
			"per folder and rule. An ignored folder, a private folder (named private\n" +
			"or ending in _private, or a settings folder marked private), and a folder\n" +
			"below --depth show a file count instead of their contents. .git is left\n" +
			"out, and symbolic links are files.\n\n" +
			"git decides what is ignored when the folder is in a work tree. Without\n" +
			"git, fw reads the root .gitignore.\n\n" +
			"fw publish --structure publishes the tree, with each ignored file's name\n" +
			"replaced by its rule and a count.",
		Example: "  fw project structure\n" +
			"  fw project structure R data --depth 2\n" +
			"  fw project structure --exclude renv --note R=\"Reusable functions\"",
		RunE: func(cmd *cobra.Command, args []string) error {
			root := a.projectRoot()
			if root == "" {
				root = a.cwd
			}
			tree, err := a.buildStructure(root, args, s, true)
			if err != nil {
				return err
			}
			tree.Render(cmd.OutOrStdout(), true)
			fmt.Fprintf(cmd.ErrOrStderr(), "Ignore rules: %s. Nothing was uploaded; fw publish --structure publishes it.\n", methodText(tree.Method))
			return nil
		},
	}
	addStructureFlags(cmd, &s, false)
	return cmd
}

func methodText(method string) string {
	switch method {
	case "git":
		return "git"
	case "gitignore":
		return "the root .gitignore (no git work tree)"
	}
	return "none (no git work tree or .gitignore)"
}

// buildStructure builds the tree for fw project structure and fw publish
// --structure. PATHs are relative to the current folder.
func (a *app) buildStructure(root string, args []string, s structureFlags, local bool) (*structure.Tree, error) {
	if s.depth < 0 {
		return nil, usageError("--depth takes 0 (no limit) or more")
	}
	var hide bool
	switch s.ignored {
	case "rules", "":
	case "hide":
		hide = true
	default:
		return nil, usageError("--ignored takes rules or hide")
	}
	notes := map[string]string{}
	for _, n := range s.notes {
		p, text, ok := strings.Cut(n, "=")
		if !ok || strings.TrimSpace(p) == "" {
			return nil, usageError("--note takes PATH=TEXT")
		}
		notes[strings.TrimSpace(p)] = text
	}
	realRoot := root
	if r, err := filepath.EvalSymlinks(root); err == nil {
		realRoot = r
	}
	var paths []string
	for _, arg := range args {
		p := arg
		if !filepath.IsAbs(p) {
			p = filepath.Join(a.cwd, p)
		}
		if r, err := filepath.EvalSymlinks(p); err == nil {
			p = r
		}
		rel, err := filepath.Rel(realRoot, p)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, usageError(arg + " is outside the project")
		}
		paths = append(paths, filepath.ToSlash(rel))
	}
	tree, err := structure.Build(structure.Options{
		ProjectRoot: root, Paths: paths, Depth: s.depth, Exclude: s.exclude,
		HideIgnored: hide, PrivateDirs: settingsPrivateDirs(root), Local: local,
	})
	if err != nil {
		return nil, err
	}
	if err := tree.SetNotes(notes); err != nil {
		return nil, usageError(err.Error())
	}
	return tree, nil
}

// publishStructure is fw publish --structure: it prints the tree as it
// will publish, writes it to <slug>.json, and publishes that as kind tree.
func (a *app) publishStructure(cmd *cobra.Command, args []string, s structureFlags, f publishFlags) error {
	for _, name := range []string{"entrypoint", "kind"} {
		if cmd.Flags().Changed(name) {
			return usageError("--" + name + " doesn't apply to --structure")
		}
	}
	root, err := a.requireProject()
	if err != nil {
		return err
	}
	destination, err := resolvePublish(root, f.to)
	if err != nil {
		return err
	}
	if destination.Driver != "cloud" {
		return usageError("--structure publishes to Framework Cloud only")
	}
	// Say the project isn't linked before printing the tree.
	if _, _, err := a.projectClient(f.urlFlag); err != nil {
		return err
	}
	tree, err := a.buildStructure(root, args, s, false)
	if err != nil {
		return err
	}
	doc, err := tree.Document("shell", s.open, s.highlight)
	if err != nil {
		return usageError(err.Error())
	}
	if f.slug == "" {
		f.slug = tree.Slug(pub.Slug)
	}
	if f.title == "" {
		f.title = tree.Title()
	}
	if f.slug == "" || pub.Slug(f.slug) != f.slug {
		return usageError("--slug must use lowercase letters, digits, and single hyphens")
	}
	body, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "fw-structure-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	out := filepath.Join(dir, f.slug+".json")
	if err := os.WriteFile(out, append(body, '\n'), 0o600); err != nil {
		return err
	}
	errOut := cmd.ErrOrStderr()
	tree.Render(errOut, false)
	fmt.Fprintf(errOut, "Ignore rules: %s. Ignored and private names stay on this computer.\n", methodText(tree.Method))
	f.kind = "tree"
	return a.publishPath(cmd, out, f)
}

// settingsPrivateDirs lists the settings folders marked private: each
// directories entry whose key contains "private" and not "public", from
// settings.yml (under default: when it has environments) and
// .framework/spec.json. R: .fw_private_directories().
func settingsPrivateDirs(root string) []string {
	dirs := map[string]string{}
	for _, name := range []string{"settings.yml", "settings.yaml"} {
		data, err := os.ReadFile(filepath.Join(root, name)) // #nosec G304 -- the project's own settings
		if err != nil {
			continue
		}
		var doc map[string]any
		if yaml.Unmarshal(data, &doc) != nil {
			break
		}
		config := doc
		if def, ok := doc["default"].(map[string]any); ok {
			config = def
		}
		switch d := config["directories"].(type) {
		case map[string]any:
			addDirs(dirs, d)
		case string:
			// A split settings file, such as settings/directories.yml.
			if split, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(d))); err == nil { // #nosec G304 -- named by the project's settings
				var sdoc map[string]any
				if yaml.Unmarshal(split, &sdoc) == nil {
					if inner, ok := sdoc["directories"].(map[string]any); ok {
						sdoc = inner
					}
					addDirs(dirs, sdoc)
				}
			}
		}
		break
	}
	if data, err := os.ReadFile(filepath.Join(root, ".framework", "spec.json")); err == nil { // #nosec G304 -- the project's own spec
		var spec struct {
			Directories map[string]any `json:"directories"`
		}
		if json.Unmarshal(data, &spec) == nil {
			addDirs(dirs, spec.Directories)
		}
	}
	out := make([]string, 0, len(dirs))
	for _, p := range dirs {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func addDirs(dirs map[string]string, m map[string]any) {
	for k, v := range m {
		key := strings.ToLower(k)
		p, ok := v.(string)
		if !ok || p == "" || !strings.Contains(key, "private") || strings.Contains(key, "public") {
			continue
		}
		p = strings.TrimSuffix(strings.TrimPrefix(filepath.ToSlash(p), "./"), "/")
		dirs[p] = p
	}
}
