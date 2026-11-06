package cli

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/frameworktools/framework-cli/internal/engine"
	"github.com/frameworktools/framework-cli/internal/skillsync"
	"github.com/spf13/cobra"
)

func newSkillSyncCmd(a *app) *cobra.Command {
	var opts skillsync.Options
	var server string
	cmd := &cobra.Command{
		Use:   "sync PATH",
		Short: "Sync a skill folder with Framework Cloud",
		Long: "Syncs one skill folder, such as .agents/skills/analysis, with your skills\n" +
			"on Framework Cloud. fw sends local changes, pulls newer versions, and stops\n" +
			"with exit code 1 when both sides changed. --resolve local or --resolve\n" +
			"remote picks a side. When fw replaces local files, it moves the old ones to\n" +
			".framework/skills-backups/ and prints where.\n\n" +
			"PATH is a folder in the project, relative to the current folder. fw records\n" +
			"each skill's version in .framework/skills.lock.json. --variant defaults to\n" +
			"the project's language. --dry-run and --direction status change nothing.",
		Example: "  fw skills sync .agents/skills/analysis\n" +
			"  fw skills sync .agents/skills/analysis --direction status --json\n" +
			"  fw skills sync .agents/skills/analysis --id me:analysis --direction pull\n" +
			"  fw skills sync .agents/skills/analysis --resolve remote",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch opts.Direction {
			case "auto", "push", "pull", "status":
			default:
				return usageError("--direction takes auto, push, pull, or status")
			}
			switch opts.Variant {
			case "", "r", "python", "stata":
			default:
				return usageError("--variant takes r, python, or stata")
			}
			switch opts.Resolve {
			case "", "local", "remote":
			default:
				return usageError("--resolve takes local or remote")
			}
			if server == "" {
				server, _ = cmd.Parent().PersistentFlags().GetString("url")
			}
			client, err := a.userClient(server)
			if err != nil {
				return err
			}
			opts.Root = a.projectRoot()
			if opts.Root == "" {
				opts.Root = a.cwd
			}
			if opts.Path, err = skillPath(opts.Root, a.cwd, args[0]); err != nil {
				return err
			}
			if opts.Variant == "" {
				opts.Variant = "r"
				if lang, _ := engine.DetectLanguage(opts.Root); lang == "python" || lang == "stata" {
					opts.Variant = lang
				}
			}
			result, err := skillsync.Sync(cmd.Context(), client, opts)
			if err != nil {
				return explain(err, client.URL)
			}
			if a.jsonOut {
				err = json.NewEncoder(cmd.OutOrStdout()).Encode(result)
			} else {
				_, err = fmt.Fprintln(cmd.OutOrStdout(), skillResultText(result, opts.Path))
				for _, backup := range result.Backups {
					if err == nil {
						_, err = fmt.Fprintf(cmd.OutOrStdout(), "Backed up the replaced files to %s\n", backup)
					}
				}
			}
			if err == nil && (result.Action == "conflict" || result.Action == "blocked" || result.Action == "missing") {
				return &exitError{code: 1, silent: true, reason: "skill_sync_conflict"}
			}
			return err
		},
	}
	cmd.Flags().StringVar(&server, "url", "", "Framework Cloud server")
	cmd.Flags().StringVar(&opts.Direction, "direction", "auto", "auto, push, pull, or status")
	cmd.Flags().StringVar(&opts.ID, "id", "", "the skill's ID on Framework Cloud, to adopt it or pull it the first time")
	cmd.Flags().StringVar(&opts.Variant, "variant", "", "r, python, or stata (default: the project's language)")
	cmd.Flags().StringVar(&opts.Resolve, "resolve", "", "settle a conflict: local or remote; replaced files go to .framework/skills-backups/")
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false, "report what would change without writing files or new versions")
	cmd.Flags().BoolVar(&a.jsonOut, "json", false, jsonHelp)
	return cmd
}

// skillPath turns PATH, relative to the current folder, into the
// project-relative form the lock file records. A trailing slash, ./, and
// ../ are fine as long as the folder is inside the project.
func skillPath(root, cwd, path string) (string, error) {
	if !filepath.IsAbs(path) {
		path = filepath.Join(cwd, path)
	}
	rel, err := filepath.Rel(root, filepath.Clean(path))
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", usageError(fmt.Sprintf("%s is not a folder inside the project at %s", path, root))
	}
	return filepath.ToSlash(rel), nil
}

// skillResultText is one line saying what sync did, without empty IDs or
// version 0.
func skillResultText(r skillsync.Result, path string) string {
	text := r.Action + ": " + path
	switch {
	case r.ID != "" && r.Version > 0:
		text += fmt.Sprintf(" (%s, version %d)", r.ID, r.Version)
	case r.ID != "":
		text += " (" + r.ID + ")"
	case r.Action == "push":
		text += " (new on Framework Cloud)"
	}
	return text
}
