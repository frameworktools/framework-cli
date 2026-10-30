package cli

import (
	"fmt"
	"os/exec"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/frameworktools/framework-cli/internal/cloud"
)

func newDoctorCmd(a *app) *cobra.Command {
	var urlFlag string
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Report the runtimes, configuration folders, and server fw would use",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dirs := a.dirs
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)

			fmt.Fprintln(w, "Runtimes")
			for _, name := range []string{"Rscript", "uv", "python3", "git"} {
				path, err := exec.LookPath(name)
				if err != nil {
					path = "not found"
				}
				fmt.Fprintf(w, "  %s\t%s\n", name, path)
			}

			fmt.Fprintln(w, "\nConfiguration")
			fmt.Fprintf(w, "  fw folder\t%s%s\n", dirs.FW, missing(dirs.FW))
			fmt.Fprintf(w, "  R folder\t%s%s\n", dirs.R, missing(dirs.R))

			fmt.Fprintln(w, "\nServer")
			server, err := a.server(urlFlag, cloud.UserScope)
			if err != nil {
				fmt.Fprintf(w, "  error\t%s\n", err)
			} else {
				fmt.Fprintf(w, "  url\t%s\n", server.URL)
				fmt.Fprintf(w, "  from\t%s\n", server.Source)
			}
			return w.Flush()
		},
	}
	cmd.Flags().StringVar(&urlFlag, "url", "", "Framework Cloud server")
	return cmd
}
