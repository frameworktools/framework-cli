// Package cli defines the fw command tree.
package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/frameworktools/framework-cli/internal/cloud"
)

// Set at build time with -ldflags "-X ...".
var (
	version = "dev"
	commit  = ""
	date    = ""
)

// exitError carries a specific exit code out of a command. A silent error
// has already told the user what happened.
type exitError struct {
	code   int
	err    error
	silent bool
}

func (e *exitError) Error() string {
	if e.err == nil {
		return fmt.Sprintf("exit status %d", e.code)
	}
	return e.err.Error()
}

func newRootCmd(a *app) *cobra.Command {
	root := &cobra.Command{
		Use:   "fw",
		Short: "Command-line helper for Framework projects and Framework Cloud",
		Long: "fw works with Framework Cloud (framework.pub or a self-hosted server) and\n" +
			"with Framework projects. The R and Python packages never need it.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(
		newLoginCmd(a), newLogoutCmd(a), newStatusCmd(a), newNewCmd(a), newProjectsCmd(a), newProjectCmd(a),
		newConfigCmd(a), newDoctorCmd(a), newVersionCmd(),
	)
	return root
}

// Execute runs fw and returns the process exit code.
func Execute() int {
	cloud.UserAgent = "framework-cli/" + version
	a, err := newApp()
	if err == nil {
		err = newRootCmd(a).Execute()
	}
	if err == nil {
		return 0
	}
	var exit *exitError
	if errors.As(err, &exit) {
		if !exit.silent {
			fmt.Fprintln(os.Stderr, "fw:", err)
		}
		return exit.code
	}
	fmt.Fprintln(os.Stderr, "fw:", err)
	return 1
}
