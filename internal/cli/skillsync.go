package cli

import (
	"encoding/json"
	"fmt"

	"github.com/frameworktools/framework-cli/internal/skillsync"
	"github.com/spf13/cobra"
)

func newSkillSyncCmd(a *app) *cobra.Command {
	var opts skillsync.Options
	var server string
	cmd := &cobra.Command{Use: "sync PATH", Short: "Synchronize a versioned skill directory", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
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
		opts.Path = args[0]
		result, err := skillsync.Sync(cmd.Context(), client, opts)
		if err != nil {
			return explain(err, client.URL)
		}
		if a.jsonOut {
			err = json.NewEncoder(cmd.OutOrStdout()).Encode(result)
		} else {
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s: %s (version %d)\n", result.Action, result.ID, result.Version)
		}
		if err == nil && (result.Action == "conflict" || result.Action == "blocked" || result.Action == "missing") {
			return &exitError{code: 1, silent: true, reason: "skill_sync_conflict"}
		}
		return err
	}}
	cmd.Flags().StringVar(&server, "url", "", "Framework Cloud server")
	cmd.Flags().StringVar(&opts.Direction, "direction", "auto", "auto, push, pull, or status")
	cmd.Flags().StringVar(&opts.ID, "id", "", "Explicit Cloud skill ID for adoption or an initial pull")
	cmd.Flags().StringVar(&opts.Variant, "variant", "r", "r, python, or stata")
	cmd.Flags().StringVar(&opts.Resolve, "resolve", "", "Explicit conflict choice: local or remote; replaced files are backed up")
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false, "Report changes without writing files or Cloud versions")
	cmd.Flags().BoolVar(&a.jsonOut, "json", false, "JSON output")
	return cmd
}
