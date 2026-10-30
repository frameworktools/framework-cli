package cli

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the fw version",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "fw %s", version)
			if commit != "" {
				fmt.Fprintf(out, " (%s", commit)
				if date != "" {
					fmt.Fprintf(out, ", %s", date)
				}
				fmt.Fprint(out, ")")
			}
			fmt.Fprintf(out, " %s/%s\n", runtime.GOOS, runtime.GOARCH)
		},
	}
}
