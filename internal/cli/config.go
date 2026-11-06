package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

func newConfigCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Show and sync fw's configuration folder",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "path",
		Short: "Print fw's configuration folder and the R package's folder",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dirs := a.dirs
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "fw:  %s%s\n", dirs.FW, missing(dirs.FW))
			fmt.Fprintf(out, "R:   %s%s\n", dirs.R, missing(dirs.R))
			return nil
		},
	}, &cobra.Command{
		Use:   "sync",
		Short: "Copy the saved server and token files from the R package's folder",
		Long: "Copies cloud.yml and the cloud-token files from the R package's folder into\n" +
			"fw's folder. Existing files are kept, and the R folder is never changed.\n" +
			"Keyring logins need no sync: R, Python, and fw share the same entries.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dirs := a.dirs
			if filepath.Clean(dirs.R) == filepath.Clean(dirs.FW) {
				fmt.Fprintf(cmd.OutOrStdout(), "Nothing to copy. fw and the R package share %s, as FW_CONFIG_HOME sets.\n", dirs.FW)
				return nil
			}
			result, err := dirs.Sync()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			switch {
			case len(result.Copied) > 0:
				fmt.Fprintf(out, "Copied from %s: %s\n", dirs.R, strings.Join(result.Copied, ", "))
			case len(result.Skipped) > 0:
				fmt.Fprintln(out, "Nothing to copy. fw's folder already has these files.")
			default:
				fmt.Fprintf(out, "Nothing to copy from %s.\n", dirs.R)
			}
			if len(result.Copied) > 0 && len(result.Skipped) > 0 {
				fmt.Fprintf(out, "Kept fw's copy of: %s\n", strings.Join(result.Skipped, ", "))
			}
			return nil
		},
	})
	return cmd
}

func missing(path string) string {
	if _, err := os.Stat(path); err != nil {
		return " (not created yet)"
	}
	return ""
}
