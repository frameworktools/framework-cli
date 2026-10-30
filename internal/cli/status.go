package cli

import (
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/frameworktools/framework-cli/internal/cloud"
	"github.com/frameworktools/framework-cli/internal/project"
)

func newStatusCmd(a *app) *cobra.Command {
	var urlFlag string
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show the server in use and whether you are logged in",
		Long: "Shows which server fw uses and where that setting came from, and who the\n" +
			"stored token belongs to. Inside a project linked to a different server,\n" +
			"it shows that server too. Exits with status 1 when not logged in.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			server, err := a.server(urlFlag, cloud.UserScope)
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "Server: %s (from %s)\n", server.URL, server.Source)

			if root := a.projectRoot(); root != "" && project.EnvValue(root, "FW_PROJECT_TOKEN") != "" {
				if proj, err := a.server("", cloud.ProjectScope); err == nil && proj.URL != server.URL {
					fmt.Fprintf(out, "This project is linked to %s (from %s).\n", proj.URL, proj.Source)
				}
			}

			found, ok := a.tokens().Lookup(server.URL)
			if !ok {
				fmt.Fprintf(out, "Not logged in. Run: %s\n", loginCommand(server.URL))
				return &exitError{code: 1, silent: true}
			}
			me, err := cloud.NewClient(server.URL, found.Token).Me(cmd.Context())
			if err != nil {
				fmt.Fprintf(out, "A token is stored in %s, but checking it failed: %v\n", found.Source, explain(err, server.URL))
				return &exitError{code: 1, silent: true}
			}
			fmt.Fprintf(out, "Logged in to %s as %s.\n", cloud.Host(server.URL), me.Who())
			fmt.Fprintf(out, "Token: %s\n", found.Source)
			return nil
		},
	}
	cmd.Flags().StringVar(&urlFlag, "url", "", "check this server instead of the default")
	return cmd
}

func newProjectsCmd(a *app) *cobra.Command {
	var urlFlag string
	cmd := &cobra.Command{
		Use:   "projects",
		Short: "Work with your projects on Framework Cloud",
	}
	cmd.PersistentFlags().StringVar(&urlFlag, "url", "", "Framework Cloud server")
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List your projects",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := a.userClient(urlFlag)
			if err != nil {
				return err
			}
			projects, err := client.Projects(cmd.Context())
			if err != nil {
				return explain(err, client.URL)
			}
			out := cmd.OutOrStdout()
			if len(projects) == 0 {
				fmt.Fprintf(out, "You have no projects on %s.\n", cloud.Host(client.URL))
				return nil
			}
			w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "SLUG\tNAME\tLANGUAGE\tTYPE\tCREATED")
			for _, p := range projects {
				language := p.Language
				if language == "" {
					language = "r"
				}
				created := p.CreatedAt
				if len(created) >= 10 {
					created = created[:10]
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", p.Slug, p.Name, language, p.ProjectType, created)
			}
			return w.Flush()
		},
	})
	return cmd
}
