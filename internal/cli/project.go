package cli

import (
	"errors"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/frameworktools/framework-cli/internal/cloud"
	"github.com/frameworktools/framework-cli/internal/engine"
	"github.com/frameworktools/framework-cli/internal/project"
)

func newProjectCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "project",
		Short: "Work with the Framework project in the current folder",
	}
	var from, urlFlag string
	var pullToken bool
	link := &cobra.Command{
		Use:   "link",
		Short: "Link this project to Framework Cloud",
		Long: "Adds FW_PROJECT_TOKEN and FW_CLOUD_URL to the project's .env, so the R and\n" +
			"Python packages can publish to the server the project came from. Values\n" +
			"already in .env are kept.\n\n" +
			"--from SLUG uses your login to get a new project token for one of your\n" +
			"projects. --pull-token takes a project token from the project's page; fw\n" +
			"asks for it, or reads it from stdin.",
		Example: "  fw project link --from vaccine-study\n" +
			"  fw project link --pull-token --url https://framework.example.edu",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			root, err := a.requireProject()
			if err != nil {
				return err
			}
			var l cloudLink
			switch {
			case from != "":
				client, err := a.userClient(urlFlag)
				if err != nil {
					return err
				}
				l = cloudLink{slug: from, client: client, url: client.URL}
			case pullToken:
				server, err := a.server(urlFlag, cloud.UserScope)
				if err != nil {
					return err
				}
				token, err := a.readProjectToken(cmd)
				if err != nil {
					return err
				}
				me, err := cloud.NewClient(server.URL, token).Me(cmd.Context())
				if err != nil {
					return explain(err, server.URL)
				}
				if me.Kind != "project" {
					return errors.New("that is a user token. Use a project token from the project's page, or: fw project link --from SLUG")
				}
				l = cloudLink{token: token, url: server.URL}
			default:
				return usageError("give --from SLUG or --pull-token")
			}
			return linkProject(cmd, &l, root)
		},
	}
	link.Flags().StringVar(&from, "from", "", "the slug of your project on Framework Cloud")
	link.Flags().BoolVar(&pullToken, "pull-token", false, "use a project token (asked for, or read from stdin)")
	link.Flags().StringVar(&urlFlag, "url", "", "Framework Cloud server")
	link.MarkFlagsMutuallyExclusive("from", "pull-token")
	cmd.AddCommand(link, newProjectShowCmd(a), newProjectStatusCmd(a), newProjectBootstrapCmd(a), newPushSpecCmd(a))
	return cmd
}

func (a *app) requireProject() (string, error) {
	root := a.projectRoot()
	if root == "" {
		return "", errors.New("not inside a Framework project (no .framework folder or settings.yml found)")
	}
	return root, nil
}

// projectStatusJSON is fw project status --json.
type projectStatusJSON struct {
	Root           string     `json:"root"`
	Language       string     `json:"language"`
	LanguageSource string     `json:"language_source,omitempty"`
	Runner         []string   `json:"runner"`
	RunnerReason   string     `json:"runner_reason,omitempty"`
	Cloud          cloudState `json:"cloud"`
	Warnings       []string   `json:"warnings"`
}

// cloudState is the project's link to Framework Cloud. Linked means .env
// has a project token; OK means the server accepted it.
type cloudState struct {
	Linked       bool   `json:"linked"`
	Server       string `json:"server,omitempty"`
	ServerSource string `json:"server_source,omitempty"`
	Project      string `json:"project,omitempty"`
	OK           bool   `json:"ok"`
	Problem      string `json:"problem,omitempty"`
}

func newProjectStatusCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "status",
		Short:   "Show this project's language, runtime, and cloud link",
		Example: "  fw project status\n  fw project status --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			root, err := a.requireProject()
			if err != nil {
				return err
			}
			p := a.engines.Detect(root)
			res := projectStatusJSON{
				Root: root, Language: p.Language, LanguageSource: p.LanguageSource,
				Runner: p.Runner, RunnerReason: p.Reason, Warnings: []string{},
			}
			if res.Runner == nil {
				res.Runner = []string{}
			}
			if token := project.EnvValue(root, "FW_PROJECT_TOKEN"); token != "" {
				res.Cloud.Linked = true
				server, serr := a.server("", cloud.ProjectScope)
				if serr != nil {
					res.Cloud.Problem = serr.Error()
				} else {
					res.Cloud.Server, res.Cloud.ServerSource = server.URL, server.Source
					me, err := cloud.NewClient(server.URL, token).Me(cmd.Context())
					switch {
					case err != nil:
						res.Cloud.Problem = "checking the project token failed: " + err.Error()
					case me.Kind != "project":
						res.Cloud.Problem = "FW_PROJECT_TOKEN is not a project token"
					default:
						res.Cloud.OK, res.Cloud.Project = true, me.Project
					}
				}
			}
			if project.EnvTracked(root) {
				res.Warnings = append(res.Warnings, "git tracks .env. Run git rm --cached .env, add .env to .gitignore, and rotate any tokens in it.")
			}
			if a.jsonOut {
				return writeJSON(cmd.OutOrStdout(), res)
			}

			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintf(w, "Project\t%s\n", root)
			if p.Language == "" {
				fmt.Fprintln(w, "Language\tunknown")
			} else {
				fmt.Fprintf(w, "Language\t%s (from %s)\n", p.Language, p.LanguageSource)
			}
			if len(p.Runner) > 0 {
				fmt.Fprintf(w, "Runs with\t%s\n", strings.Join(p.Runner, " "))
			}
			if p.Reason != "" {
				fmt.Fprintf(w, "\t%s\n", p.Reason)
			}
			c := res.Cloud
			switch {
			case !c.Linked:
				fmt.Fprintln(w, "Cloud\tnot linked (run fw project link)")
			case c.Server == "":
				fmt.Fprintf(w, "Cloud\t%s\n", c.Problem)
			case !c.OK:
				fmt.Fprintf(w, "Cloud\t%s (from %s); %s\n", c.Server, c.ServerSource, c.Problem)
			default:
				fmt.Fprintf(w, "Cloud\tlinked to %s as project %s (from %s)\n", cloud.Host(c.Server), c.Project, c.ServerSource)
			}
			for _, warning := range res.Warnings {
				fmt.Fprintf(w, "Warning\t%s\n", warning)
			}
			return w.Flush()
		},
	}
	cmd.Flags().BoolVar(&a.jsonOut, "json", false, jsonHelp)
	return cmd
}

func newProjectBootstrapCmd(a *app) *cobra.Command {
	var printOnly bool
	cmd := &cobra.Command{
		Use:   "bootstrap",
		Short: "Prepare a cloned project with its language package",
		Long: "Runs the language package's bootstrap in the project's own environment:\n" +
			"framework::bootstrap() for R and python -m framework bootstrap for Python.\n" +
			"It restores packages, creates .env from .env.example, creates the folders\n" +
			"git does not keep, and installs the git hooks. It never contacts the cloud.\n" +
			"Stata projects need no bootstrap.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			root, err := a.requireProject()
			if err != nil {
				return err
			}
			command, err := a.engines.Bootstrap(a.engines.Detect(root))
			var unavailable *engine.UnavailableError
			if errors.As(err, &unavailable) {
				fmt.Fprintln(cmd.OutOrStdout(), unavailable.Msg)
				return nil
			}
			if err != nil {
				return err
			}
			if printOnly {
				fmt.Fprintf(cmd.OutOrStdout(), "cd %s && %s\n", root, command)
				return nil
			}
			code, err := a.run(command)
			if err != nil {
				return err
			}
			if code != 0 {
				return &exitError{code: code, err: fmt.Errorf("bootstrap exited with status %d; its last message names the problem", code)}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&printOnly, "print", false, "print the command instead of running it")
	return cmd
}
