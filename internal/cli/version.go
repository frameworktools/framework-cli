package cli

import (
	"fmt"
	"runtime"
	"runtime/debug"

	"github.com/spf13/cobra"
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the fw version",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			fmt.Fprintln(cmd.OutOrStdout(), versionText())
		},
	}
}

// versionText is what fw version and fw --version print. A local build has
// no release ldflags, so it names the git commit Go recorded, if any.
func versionText() string {
	text := "fw " + version
	rev, when := commit, date
	if rev == "" {
		if info, ok := debug.ReadBuildInfo(); ok {
			modified := false
			for _, s := range info.Settings {
				switch s.Key {
				case "vcs.revision":
					rev = s.Value
				case "vcs.modified":
					modified = s.Value == "true"
				}
			}
			if len(rev) > 12 {
				rev = rev[:12]
			}
			if rev != "" && modified {
				rev += ", modified"
			}
		}
	}
	if rev != "" {
		text += " (" + rev
		if when != "" {
			text += ", " + when
		}
		text += ")"
	}
	return text + " " + runtime.GOOS + "/" + runtime.GOARCH
}
