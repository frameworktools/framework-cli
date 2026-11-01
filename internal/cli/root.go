// Package cli defines the fw command tree.
package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	// reason is a stable name for the problem in --json errors.
	reason string
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
		newBlueprintsCmd(a), newPiecesCmd(a, structuresKind), newPiecesCmd(a, toolsKind),
		newPiecesCmd(a, packageSetsKind), newPiecesCmd(a, skillsKind), newPublishCmd(a), newConfigCmd(a), newDoctorCmd(a), newVersionCmd(),
	)
	markUsage(root)
	return root
}

// Execute runs fw and returns the process exit code.
func Execute() int {
	cloud.UserAgent = "framework-cli/" + version
	a, err := newApp()
	if err == nil {
		err = newRootCmd(a).Execute()
	}
	report(os.Stderr, a, err)
	return exitCode(err)
}

// report prints a command's error on stderr: as one JSON line after --json,
// otherwise as text. Silent errors were already reported by the command.
func report(w io.Writer, a *app, err error) {
	if err == nil {
		return
	}
	var exit *exitError
	if errors.As(err, &exit) && exit.silent {
		return
	}
	if a != nil && a.jsonOut {
		e := jsonError{Error: err.Error(), Code: exitCode(err)}
		if exit != nil {
			e.Reason = exit.reason
		}
		data, _ := json.Marshal(e)
		fmt.Fprintln(w, string(data))
		return
	}
	fmt.Fprintln(w, "fw:", err)
}
