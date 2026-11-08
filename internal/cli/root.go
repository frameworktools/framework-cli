// Package cli defines the fw command tree.
package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

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
	// reason is a stable name for the problem in --json errors. found and
	// hint add what fw found and the command to run instead.
	reason, found, hint string
	// problems lists what is wrong, one line each, for --json errors.
	problems []string
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
		Version:       versionText(),
	}
	root.SetVersionTemplate("{{.Version}}\n")
	root.AddCommand(
		newLoginCmd(a), newLogoutCmd(a), newStatusCmd(a), newNewCmd(a), newProjectsCmd(a), newProjectCmd(a),
		newBlueprintsCmd(a), newPiecesCmd(a, structuresKind), newPiecesCmd(a, toolsKind),
		newPiecesCmd(a, packageSetsKind), newPiecesCmd(a, skillsKind), newPublishCmd(a), newAdoptCmd(a), newConfigCmd(a), newDoctorCmd(a), newVersionCmd(),
	)
	root.SetHelpCommand(&cobra.Command{
		Use:   "help [command]",
		Short: "Help about any command",
		Long:  "Shows help for any command, as in: fw help publish",
		ValidArgsFunction: func(_ *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
			target, _, err := root.Find(args)
			if err != nil {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			var names []cobra.Completion
			for _, sub := range target.Commands() {
				if sub.IsAvailableCommand() && strings.HasPrefix(sub.Name(), toComplete) {
					names = append(names, cobra.CompletionWithDesc(sub.Name(), sub.Short))
				}
			}
			return names, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(_ *cobra.Command, args []string) error {
			target, rest, err := root.Find(args)
			if err != nil || len(rest) > 0 {
				return usageError(fmt.Sprintf("unknown help topic %q. Run fw help to list the commands", strings.Join(args, " ")))
			}
			return target.Help()
		},
	})
	// Add cobra's completion command now, so markUsage sees it.
	root.InitDefaultCompletionCmd()
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
			e.Reason, e.Found, e.Hint, e.Problems = exit.reason, exit.found, exit.hint, exit.problems
		}
		data, _ := json.Marshal(e)
		fmt.Fprintln(w, string(data))
		return
	}
	fmt.Fprintln(w, "fw:", err)
}
