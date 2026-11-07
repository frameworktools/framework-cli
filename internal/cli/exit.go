package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/spf13/cobra"

	"github.com/frameworktools/framework-cli/internal/cloud"
)

// Exit codes. Scripts and agents can branch on these; they are documented in
// the README and the docs site.
const (
	exitOK       = 0
	exitFailure  = 1 // anything else
	exitUsage    = 2 // bad flags or arguments
	exitAuth     = 3 // not logged in, or the server rejected the token (401, 403)
	exitNotFound = 4 // the server has no such project or resource (404)
	exitLogin    = 5 // browser login was denied, or its code expired
	exitPrivate  = 6 // an upload has "private" in a path and needs --include-private
)

// errNotLoggedIn means no token is stored for the server.
type errNotLoggedIn struct{ msg string }

func (e *errNotLoggedIn) Error() string { return e.msg }

// exitCode picks the process exit code for an error returned by a command.
func exitCode(err error) int {
	if err == nil {
		return exitOK
	}
	var exit *exitError
	if errors.As(err, &exit) {
		return exit.code
	}
	var notLoggedIn *errNotLoggedIn
	switch {
	case errors.As(err, &notLoggedIn):
		return exitAuth
	case errors.Is(err, cloud.ErrDeviceDenied), errors.Is(err, cloud.ErrDeviceExpired):
		return exitLogin
	}
	switch cloud.StatusOf(err) {
	case http.StatusUnauthorized, http.StatusForbidden:
		return exitAuth
	case http.StatusNotFound:
		return exitNotFound
	}
	// Cobra reports an unknown command or flag, and flags that cannot go
	// together, as plain errors.
	if msg := err.Error(); strings.HasPrefix(msg, "unknown command") || strings.HasPrefix(msg, "unknown flag") ||
		strings.HasPrefix(msg, "unknown shorthand flag") || strings.HasPrefix(msg, "if any flags in the group") {
		return exitUsage
	}
	return exitFailure
}

// markUsage makes flag and argument errors exit with exitUsage throughout
// the command tree.
func markUsage(cmd *cobra.Command) {
	cmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return &exitError{code: exitUsage, err: err}
	})
	// A group such as fw project only lists its commands. Cobra shows that
	// list and exits 0 for an unknown command, as in fw completion tcsh.
	if cmd.HasParent() && cmd.HasSubCommands() && cmd.Run == nil && cmd.RunE == nil {
		cmd.Args = cobra.ArbitraryArgs
		cmd.RunE = func(c *cobra.Command, args []string) error {
			if len(args) > 0 {
				return usageError(fmt.Sprintf("unknown command %q for %q. Run %s --help to list its commands", args[0], c.CommandPath(), c.CommandPath()))
			}
			return c.Help()
		}
	}
	if args := cmd.Args; args != nil {
		cmd.Args = func(c *cobra.Command, a []string) error {
			if err := args(c, a); err != nil {
				return &exitError{code: exitUsage, err: err}
			}
			return nil
		}
	}
	for _, sub := range cmd.Commands() {
		markUsage(sub)
	}
}

// usageError is a usage problem a command found itself, such as a missing
// choice between two flags.
func usageError(msg string) error {
	return &exitError{code: exitUsage, err: errors.New(msg)}
}

// writeJSON prints v as indented JSON followed by a newline.
func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// jsonError is what a failed command prints on stderr with --json.
type jsonError struct {
	Error  string `json:"error"`
	Code   int    `json:"exit_code"`
	Reason string `json:"reason,omitempty"`
	Found  string `json:"found,omitempty"`
	Hint   string `json:"hint,omitempty"`
}
