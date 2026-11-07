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
	var from string
	var pullToken, create, private bool
	var co createOptions
	link := &cobra.Command{
		Use:   "link",
		Short: "Link this project to Framework Cloud, or create it there",
		Long: "Adds FW_PROJECT_TOKEN and FW_CLOUD_URL to the project's .env, so the R and\n" +
			"Python packages can publish to the server the project came from. Values\n" +
			"already in .env are kept, and fw adds .env to .gitignore. A project that\n" +
			"already has FW_PROJECT_TOKEN is linked: fw changes nothing and gets no new\n" +
			"token. To link it to another project, remove FW_PROJECT_TOKEN and\n" +
			"FW_CLOUD_URL from .env first.\n\n" +
			"--from SLUG uses your login to get a new project token for one of your\n" +
			"projects. --pull-token takes a project token from the project's page; fw\n" +
			"asks for it, or reads it from stdin.\n\n" +
			"--create makes the Framework Cloud project for a project that exists only\n" +
			"here, such as one made with fw adopt, from its .framework/spec.json. The\n" +
			"server stores the spec as the project's only layer, so the Cloud project\n" +
			"has exactly this folder's name, type, language, folders, and tools, and\n" +
			"none of your defaults; an adopted project is marked as adopted. fw then\n" +
			"links the project. The slug comes from the name unless --slug gives one.\n" +
			"When you already have a project with that slug, fw offers to link to it\n" +
			"(--from) instead and creates nothing; a slug held by a project in your\n" +
			"Trash can't be reused. Framework Cloud projects are private to your\n" +
			"account. A project with no spec needs fw project upgrade --apply first.\n" +
			"An older self-hosted server, from before the spec API, builds the project\n" +
			"from your defaults instead; fw then sends the spec separately.",
		Example: "  fw project link --from vaccine-study\n" +
			"  fw project link --pull-token --url https://framework.example.edu\n" +
			"  fw project link --create\n" +
			"  fw project link --create --slug old-study --name \"Old study\" --json",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			root, err := a.requireProject()
			if err != nil {
				return err
			}
			if create {
				return a.linkCreate(cmd, root, co)
			}
			for _, flag := range []string{"slug", "name", "description", "private", "public", "include-private"} {
				if cmd.Flags().Changed(flag) {
					return usageError("--" + flag + " goes with --create")
				}
			}
			if from == "" && !pullToken {
				return usageError("give --from SLUG, --pull-token, or --create")
			}
			// Check before minting a token that would never be written.
			if err := a.checkNotLinked(cmd, root, from); errors.Is(err, errAlreadyLinked) {
				return nil
			} else if err != nil {
				return err
			}
			var l cloudLink
			switch {
			case from != "":
				client, err := a.userClient(co.url)
				if err != nil {
					return err
				}
				l = cloudLink{slug: from, client: client, url: client.URL}
			case pullToken:
				server, err := a.server(co.url, cloud.UserScope)
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
	lf := link.Flags()
	lf.StringVar(&from, "from", "", "the slug of your project on Framework Cloud")
	lf.BoolVar(&pullToken, "pull-token", false, "use a project token (asked for, or read from stdin)")
	lf.BoolVar(&create, "create", false, "create the project on Framework Cloud from .framework/spec.json, then link it and send the spec")
	lf.StringVar(&co.slug, "slug", "", "with --create: the project's slug (default: made from the name)")
	lf.StringVar(&co.name, "name", "", "with --create: the project's name (default: the spec's name, else the folder's)")
	lf.StringVar(&co.description, "description", "", "with --create: a description for the project")
	lf.BoolVar(&private, "private", false, "with --create: keep the project private to your account (the default; Cloud projects are always private)")
	lf.BoolVar(&co.public, "public", false, "with --create: not available; Framework Cloud has no public projects")
	lf.BoolVar(&co.includePrivate, "include-private", false, "with --create: send the spec without asking when the project's path contains \"private\"")
	lf.StringVar(&co.url, "url", "", "Framework Cloud server")
	lf.BoolVar(&a.jsonOut, "json", false, jsonHelp+" (with --create)")
	link.MarkFlagsMutuallyExclusive("from", "pull-token", "create")
	link.MarkFlagsMutuallyExclusive("private", "public")
	cmd.AddCommand(link, newProjectShowCmd(a), newProjectStatusCmd(a), newProjectBootstrapCmd(a), newPushSpecCmd(a), newProjectUpgradeCmd(a))
	return cmd
}

// checkNotLinked stops fw project link when .env already has a project
// token. Linking again to the same project is not an error.
func (a *app) checkNotLinked(cmd *cobra.Command, root, slug string) error {
	token := project.EnvValue(root, "FW_PROJECT_TOKEN")
	if token == "" {
		return nil
	}
	current := ""
	if server, err := a.server("", cloud.ProjectScope); err == nil {
		if me, err := cloud.NewClient(server.URL, token).Me(cmd.Context()); err == nil && me.Kind == "project" {
			current = me.Project
		}
	}
	if current != "" && current == slug {
		fmt.Fprintf(cmd.OutOrStdout(), "Already linked to %s. Nothing changed.\n", current)
		return errAlreadyLinked
	}
	what := "another project"
	if current != "" {
		what = current
	}
	return fmt.Errorf("this project is already linked to %s: .env has FW_PROJECT_TOKEN. Nothing changed. To link it to a different project, remove FW_PROJECT_TOKEN and FW_CLOUD_URL from .env, then run fw project link again", what)
}

// errAlreadyLinked ends fw project link successfully without linking.
var errAlreadyLinked = errors.New("already linked")

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
		Use:   "status",
		Short: "Show this project's language, runtime, and cloud link",
		Long: "Shows the project's language, how fw runs its code, and its link to\n" +
			"Framework Cloud. Exits with status 3 when the server rejects the project\n" +
			"token, and 1 when fw cannot check it. A project with no link exits 0.",
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
			code := exitOK
			if token := project.EnvValue(root, "FW_PROJECT_TOKEN"); token != "" {
				res.Cloud.Linked = true
				server, serr := a.server("", cloud.ProjectScope)
				if serr != nil {
					res.Cloud.Problem = serr.Error()
					code = exitFailure
				} else {
					res.Cloud.Server, res.Cloud.ServerSource = server.URL, server.Source
					me, err := cloud.NewClient(server.URL, token).Me(cmd.Context())
					switch {
					case err != nil:
						res.Cloud.Problem = "checking the project token failed: " + err.Error()
						code = exitCode(err)
					case me.Kind != "project":
						res.Cloud.Problem = "FW_PROJECT_TOKEN is not a project token"
						code = exitAuth
					default:
						res.Cloud.OK, res.Cloud.Project = true, me.Project
					}
				}
			}
			if project.EnvTracked(root) {
				res.Warnings = append(res.Warnings, "git tracks .env. Run git rm --cached .env, add .env to .gitignore, and rotate any tokens in it.")
			}
			done := func(err error) error {
				if err == nil && code != exitOK {
					return &exitError{code: code, silent: true}
				}
				return err
			}
			if a.jsonOut {
				return done(writeJSON(cmd.OutOrStdout(), res))
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
			return done(w.Flush())
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
			"Stata projects need no bootstrap. When the runtime or its bootstrap is\n" +
			"missing, fw says so and exits 1.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			root, err := a.requireProject()
			if err != nil {
				return err
			}
			command, err := a.engines.Bootstrap(a.engines.Detect(root))
			var unavailable *engine.UnavailableError
			if errors.As(err, &unavailable) && unavailable.NotNeeded {
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
