package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/frameworktools/framework-cli/internal/cloud"
)

func consentNotice(url string) string {
	return "Logging in connects this machine to " + url + ".\n" +
		"Framework tools (fw, R, and Python) send data to it only when you use a\n" +
		"cloud feature:\n" +
		"  - settings sync: your Framework settings document\n" +
		"  - project registration: project name, type, and language\n" +
		"  - integrity ledger: file digests, sizes, and this machine's hostname\n" +
		"  - publishing: documents you explicitly publish\n" +
		"Data files and project code are never uploaded unless you publish them.\n" +
		"Details: " + url + "/privacy\n"
}

func newLoginCmd(a *app) *cobra.Command {
	var urlFlag string
	var withToken, paste, yes bool
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Log in to Framework Cloud",
		Long: "Logs in to framework.pub or a self-hosted server and makes it the default.\n\n" +
			"By default fw opens a browser to approve the login. --paste asks for a token\n" +
			"from the server's tokens page instead, and --with-token reads one from stdin.\n" +
			"The token is stored in the system keyring, where the R and Python packages\n" +
			"find it too.",
		Example: "  fw login\n" +
			"  fw login --url https://framework.example.edu\n" +
			"  fw login --with-token --yes < token.txt",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			errOut := cmd.ErrOrStderr()
			server, err := a.server(urlFlag, cloud.UserScope)
			if err != nil {
				return err
			}
			host := cloud.Host(server.URL)

			fmt.Fprint(errOut, consentNotice(server.URL))
			if !yes {
				if !a.interactive || withToken {
					return errors.New("fw login needs confirmation. After reading the notice above, rerun with --yes")
				}
				fmt.Fprintf(errOut, "\nConnect this machine to %s? [y/N] ", host)
				answer, err := a.readLine()
				if err != nil && !errors.Is(err, io.EOF) {
					return err
				}
				if !strings.HasPrefix(strings.ToLower(answer), "y") {
					return errors.New("canceled; this machine was not connected")
				}
			}

			client := cloud.NewClient(server.URL, "")
			var token string
			switch {
			case withToken:
				if token, err = a.readLine(); err != nil {
					return fmt.Errorf("reading the token from stdin: %w", err)
				}
			case !a.interactive:
				return errors.New("no terminal to log in from. Pipe a token to fw login --with-token --yes")
			case !paste:
				token, err = browserLogin(cmd, a, client)
				if errors.Is(err, cloud.ErrDeviceUnsupported) {
					fmt.Fprintf(errOut, "%s does not offer browser login. Paste a token instead.\n", host)
					token, err = pasteToken(cmd, a, server.URL)
				}
			default:
				token, err = pasteToken(cmd, a, server.URL)
			}
			if err != nil {
				return err
			}
			if token == "" {
				return errors.New("no token given")
			}

			client.Token = token
			me, err := client.Me(ctx)
			if err != nil {
				return explain(err, server.URL)
			}
			if me.Kind == "project" {
				return errors.New("that is a project token. Log in with a user token, or use it with: fw new --pull-token")
			}

			where, err := a.tokens().Store(server.URL, token)
			if err != nil {
				return err
			}
			if err := a.dirs.SaveServer(server.URL); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Logged in to %s as %s.\n", host, me.Who())
			fmt.Fprintf(out, "Token stored in %s.\n", where)
			fmt.Fprintf(out, "Default server: %s\n", server.URL)
			return nil
		},
	}
	cmd.Flags().StringVar(&urlFlag, "url", "", "Framework Cloud server (default: FW_CLOUD_URL, your last login, or framework.pub)")
	cmd.Flags().BoolVar(&withToken, "with-token", false, "read the token from stdin")
	cmd.Flags().BoolVar(&paste, "paste", false, "paste a token instead of approving in a browser")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm connecting without a prompt")
	cmd.MarkFlagsMutuallyExclusive("with-token", "paste")
	return cmd
}

func browserLogin(cmd *cobra.Command, a *app, client *cloud.Client) (string, error) {
	errOut := cmd.ErrOrStderr()
	hostname, _ := os.Hostname()
	code, err := client.StartDevice(cmd.Context(), "fw on "+hostname)
	if err != nil {
		return "", err
	}
	link := code.VerificationURLComplete
	if link == "" {
		link = code.VerificationURL
	}
	fmt.Fprintf(errOut, "\nYour code is %s. Approve it at:\n  %s\n", code.UserCode, link)
	if a.openURL(link) == nil {
		fmt.Fprintln(errOut, "Opened your browser. Waiting for approval...")
	} else {
		fmt.Fprintln(errOut, "Waiting for approval...")
	}
	return client.PollDevice(cmd.Context(), code, a.sleep)
}

func pasteToken(cmd *cobra.Command, a *app, url string) (string, error) {
	fmt.Fprintf(cmd.ErrOrStderr(), "\nCreate a token at %s/tokens and paste it here (input is hidden): ", url)
	token, err := a.readSecret()
	return strings.TrimSpace(token), err
}

func newLogoutCmd(a *app) *cobra.Command {
	var urlFlag string
	cmd := &cobra.Command{
		Use:   "logout",
		Short: "Remove this machine's stored token for a server",
		Long: "Removes the token from the system keyring and fw's token file. The keyring\n" +
			"entry is shared, so this also logs out the R and Python packages on this\n" +
			"machine. To revoke the token itself, delete it on the server's tokens page.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			server, err := a.server(urlFlag, cloud.UserScope)
			if err != nil {
				return err
			}
			result, err := a.tokens().Remove(server.URL)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(result.Removed) == 0 {
				fmt.Fprintf(out, "No stored token found for %s.\n", cloud.Host(server.URL))
			} else {
				fmt.Fprintf(out, "Logged out of %s. Removed: %s\n", cloud.Host(server.URL), strings.Join(result.Removed, ", "))
			}
			for _, path := range result.Kept {
				fmt.Fprintf(out, "The R package still has a token file at %s. Remove it with framework::cloud_logout() in R.\n", path)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&urlFlag, "url", "", "server to log out of (default: the server fw login would use)")
	return cmd
}
