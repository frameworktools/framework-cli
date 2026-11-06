package cli

import (
	"fmt"
	"slices"
	"strconv"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/frameworktools/framework-cli/internal/cloud"
	"github.com/frameworktools/framework-cli/internal/project"
)

const jsonHelp = "print JSON on stdout, for scripts and agents"

// statusJSON is fw status --json. Token values never appear in it.
type statusJSON struct {
	Server        string       `json:"server"`
	ServerSource  string       `json:"server_source"`
	LoggedIn      bool         `json:"logged_in"`
	Account       *accountJSON `json:"account"`
	TokenSource   string       `json:"token_source,omitempty"`
	Problem       string       `json:"problem,omitempty"`
	ProjectServer *serverJSON  `json:"project_server,omitempty"`
}

type accountJSON struct {
	Kind  string `json:"kind"`
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
}

type serverJSON struct {
	URL    string `json:"url"`
	Source string `json:"source"`
}

func newStatusCmd(a *app) *cobra.Command {
	var urlFlag string
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show the server in use and whether you are logged in",
		Long: "Shows which server fw uses and where that setting came from, and who the\n" +
			"stored token belongs to. Inside a project linked to a different server,\n" +
			"it shows that server too. Exits with status 3 when not logged in or when\n" +
			"the server rejects the stored token.\n\n" +
			"FW_CLOUD_TOKEN and FW_TOKEN_FILE hold one token for every server, so fw\n" +
			"sends it to whichever server --url names. Keyring logins are per server.",
		Example: "  fw status\n  fw status --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			server, err := a.server(urlFlag, cloud.UserScope)
			if err != nil {
				return err
			}
			res := statusJSON{Server: server.URL, ServerSource: server.Source}
			if root := a.projectRoot(); root != "" && project.EnvValue(root, "FW_PROJECT_TOKEN") != "" {
				if proj, err := a.server("", cloud.ProjectScope); err == nil && proj.URL != server.URL {
					res.ProjectServer = &serverJSON{URL: proj.URL, Source: proj.Source}
				}
			}

			found, ok := a.tokens().Lookup(server.URL)
			var me cloud.Me
			code := exitOK
			switch {
			case !ok:
				res.Problem = "not logged in. Run: " + loginCommand(server.URL)
				code = exitAuth
			default:
				res.TokenSource = found.Source
				me, err = cloud.NewClient(server.URL, found.Token).Me(cmd.Context())
				if err != nil {
					res.Problem = "checking the stored token failed: " + explain(err, server.URL).Error()
					code = exitCode(err)
				} else {
					res.LoggedIn = true
					res.Account = &accountJSON{Kind: me.Kind, Name: me.Name, Email: me.Email}
				}
			}

			if a.jsonOut {
				if err := writeJSON(out, res); err != nil {
					return err
				}
			} else {
				fmt.Fprintf(out, "Server: %s (from %s)\n", server.URL, server.Source)
				if p := res.ProjectServer; p != nil {
					fmt.Fprintf(out, "This project is linked to %s (from %s).\n", p.URL, p.Source)
				}
				switch {
				case !ok:
					fmt.Fprintf(out, "Not logged in. Run: %s\n", loginCommand(server.URL))
				case !res.LoggedIn:
					fmt.Fprintf(out, "A token is stored in %s, but %s\n", found.Source, res.Problem)
				default:
					fmt.Fprintf(out, "Logged in to %s as %s.\n", cloud.Host(server.URL), me.Who())
					fmt.Fprintf(out, "Token: %s\n", found.Source)
				}
			}
			if code != exitOK {
				return &exitError{code: code, silent: true}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&urlFlag, "url", "", "check this server instead of the default")
	cmd.Flags().BoolVar(&a.jsonOut, "json", false, jsonHelp)
	return cmd
}

// projectJSON is one project in fw projects list --json.
type projectJSON struct {
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Language    string `json:"language"`
	Type        string `json:"type"`
	Description string `json:"description"`
	CreatedAt   string `json:"created_at"`
}

func newProjectsCmd(a *app) *cobra.Command {
	var urlFlag string
	cmd := &cobra.Command{
		Use:   "projects",
		Short: "Work with your projects on Framework Cloud",
	}
	cmd.PersistentFlags().StringVar(&urlFlag, "url", "", "Framework Cloud server")
	list := &cobra.Command{
		Use:     "list",
		Short:   "List your projects",
		Example: "  fw projects list\n  fw projects list --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := a.userClient(urlFlag)
			if err != nil {
				return err
			}
			projects, err := client.Projects(cmd.Context())
			if err != nil {
				return explain(err, client.URL)
			}
			rows := make([]projectJSON, 0, len(projects))
			for _, p := range projects {
				language := p.Language
				if language == "" {
					language = "r"
				}
				rows = append(rows, projectJSON{p.Slug, p.Name, language, p.ProjectType, p.Description, p.CreatedAt})
			}
			out := cmd.OutOrStdout()
			if a.jsonOut {
				return writeJSON(out, struct {
					Server   string        `json:"server"`
					Projects []projectJSON `json:"projects"`
				}{client.URL, rows})
			}
			if len(rows) == 0 {
				fmt.Fprintf(out, "You have no projects on %s.\n", cloud.Host(client.URL))
				return nil
			}
			w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "SLUG\tNAME\tLANGUAGE\tTYPE\tCREATED")
			for _, p := range rows {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", p.Slug, p.Name, p.Language, p.Type, day(p.CreatedAt))
			}
			return w.Flush()
		},
	}
	list.Flags().BoolVar(&a.jsonOut, "json", false, jsonHelp)
	cmd.AddCommand(list)
	return cmd
}

// day shortens an ISO 8601 time to its date.
func day(ts string) string {
	if len(ts) >= 10 {
		return ts[:10]
	}
	return ts
}

func newProjectShowCmd(a *app) *cobra.Command {
	var urlFlag string
	cmd := &cobra.Command{
		Use:   "show SLUG",
		Short: "Show one of your projects on Framework Cloud",
		Long: "Shows a project's name, language, type, blueprint, and linked GitHub\n" +
			"repository. Exits with status 4 when you have no project with that slug.",
		Example: "  fw project show vaccine-study\n  fw project show vaccine-study --json",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := a.userClient(urlFlag)
			if err != nil {
				return err
			}
			p, err := client.Project(cmd.Context(), args[0])
			if err != nil {
				return explain(err, client.URL)
			}
			out := cmd.OutOrStdout()
			if a.jsonOut {
				return writeJSON(out, p)
			}
			w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			fmt.Fprintf(w, "Slug\t%s\n", p.Slug)
			fmt.Fprintf(w, "Name\t%s\n", p.Name)
			if p.Description != "" {
				fmt.Fprintf(w, "Description\t%s\n", p.Description)
			}
			fmt.Fprintf(w, "Language\t%s\n", p.Language)
			fmt.Fprintf(w, "Type\t%s\n", p.Type)
			if b := p.Blueprint; b != nil {
				fmt.Fprintf(w, "Blueprint\t%s v%d\n", b.Key, b.Version)
			}
			if g := p.GitHub; g != nil {
				visibility := "public"
				if g.Private {
					visibility = "private"
				}
				line := g.URL + " (" + visibility
				if g.ScaffoldStatus != "" {
					line += ", scaffold " + g.ScaffoldStatus
				}
				fmt.Fprintf(w, "GitHub\t%s)\n", line)
			}
			fmt.Fprintf(w, "Created\t%s\n", day(p.CreatedAt))
			fmt.Fprintf(w, "Updated\t%s\n", day(p.UpdatedAt))
			if p.SpecReceivedAt != nil {
				fmt.Fprintf(w, "Spec\tsent %s\n", day(*p.SpecReceivedAt))
			}
			fmt.Fprintf(w, "\nCreate it locally with: fw new --from %s\n", p.Slug)
			return w.Flush()
		},
	}
	cmd.Flags().StringVar(&urlFlag, "url", "", "Framework Cloud server")
	cmd.Flags().BoolVar(&a.jsonOut, "json", false, jsonHelp)
	return cmd
}

func newBlueprintsCmd(a *app) *cobra.Command {
	var urlFlag, lang string
	cmd := &cobra.Command{
		Use:   "blueprints",
		Short: "Work with project blueprints on Framework Cloud",
	}
	cmd.PersistentFlags().StringVar(&urlFlag, "url", "", "Framework Cloud server")
	list := &cobra.Command{
		Use:   "list",
		Short: "List the blueprints you can start a project from",
		Long: "Lists Framework's blueprints, your own, and the ones shared with you. Use\n" +
			"a blueprint's key on Framework Cloud when you create a project.",
		Example: "  fw blueprints list\n  fw blueprints list --lang stata --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			switch lang {
			case "", "r", "python", "stata":
			default:
				return usageError("--lang takes r, python, or stata")
			}
			client, err := a.userClient(urlFlag)
			if err != nil {
				return err
			}
			blueprints, err := client.Blueprints(cmd.Context())
			if err != nil {
				return explain(err, client.URL)
			}
			if lang != "" {
				kept := blueprints[:0]
				for _, b := range blueprints {
					if l := blueprintLanguages(b); len(l) == 0 || slices.Contains(l, lang) {
						kept = append(kept, b)
					}
				}
				blueprints = kept
			}
			out := cmd.OutOrStdout()
			if a.jsonOut {
				if blueprints == nil {
					blueprints = []cloud.Blueprint{}
				}
				return writeJSON(out, struct {
					Server     string            `json:"server"`
					Blueprints []cloud.Blueprint `json:"blueprints"`
				}{client.URL, blueprints})
			}
			if len(blueprints) == 0 {
				fmt.Fprintf(out, "No blueprints on %s.\n", cloud.Host(client.URL))
				return nil
			}
			w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "KEY\tNAME\tLANGUAGES\tTYPE\tVERSION")
			for _, b := range blueprints {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", b.Key, b.Name, listText(blueprintLanguages(b), 3), b.Type, strconv.Itoa(b.Version))
			}
			return w.Flush()
		},
	}
	list.Flags().StringVar(&lang, "lang", "", "only blueprints for this language: r, python, or stata")
	list.Flags().BoolVar(&a.jsonOut, "json", false, jsonHelp)
	cmd.AddCommand(list)
	return cmd
}

// blueprintLanguages lists the languages a blueprint supports. Older
// servers send one language only.
func blueprintLanguages(b cloud.Blueprint) []string {
	if len(b.Languages) > 0 {
		return b.Languages
	}
	if b.Language != "" {
		return []string{b.Language}
	}
	return nil
}
