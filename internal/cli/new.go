package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/frameworktools/framework-cli/internal/cloud"
	"github.com/frameworktools/framework-cli/internal/engine"
	"github.com/frameworktools/framework-cli/internal/project"
	"github.com/frameworktools/framework-cli/internal/spec"
	"github.com/frameworktools/framework-cli/internal/stata"
)

type newOptions struct {
	from      string
	pullToken bool
	language  string
	kind      string
	dir       string
	headless  bool
	print     bool
	url       string
	offline   bool
	workflow  string
	pieces    pieceOptions
	dest      destOptions
}

// cloudLink is how a project created from the cloud gets its .env entries.
type cloudLink struct {
	slug   string        // mint a pull token for this slug with the user's client
	client *cloud.Client // the user's client, for slug
	token  string        // or use this project token as is
	url    string
}

func newNewCmd(a *app) *cobra.Command {
	var o newOptions
	cmd := &cobra.Command{
		Use:   "new [NAME]",
		Short: "Create a project with its language's own package",
		Long: "Creates a Framework project. R projects are created by the framework R\n" +
			"package and Python projects by framework-py. fw finds the runtime, runs it,\n" +
			"and prints the command to run instead when the runtime is missing. fw builds\n" +
			"Stata projects itself: folders, master.do, settings.do, and settings.yml.\n\n" +
			"Types and workflows by language:\n" +
			"  r, python  --type project (the default), project_sensitive, course,\n" +
			"             presentation, or bare; --workflow quarto, jupyter, or scripts\n" +
			"  stata      --type project (the default), project_sensitive, or bare;\n" +
			"             --workflow scripts only\n\n" +
			"Without --from, fw creates an R project from your R defaults (author,\n" +
			"directories, and packages), the same as framework::new().\n\n" +
			"Logged in, fw new NAME --lang stata builds from your defaults on Framework\n" +
			"Cloud and saves them for offline use. When the server cannot be reached it\n" +
			"uses the saved defaults and says from when. With none saved it asks at a\n" +
			"terminal, and without one it stops unless --offline is given. --offline\n" +
			"uses the saved defaults, or fw's built-ins, without contacting the server.\n" +
			"For Python and R projects fw passes --offline and --workflow to\n" +
			"framework-py (0.1.0.dev9 or later) or the framework R package, which apply\n" +
			"the same rules. Logged in, they also register the project on Framework\n" +
			"Cloud and write FW_PROJECT_TOKEN to its .env. --offline keeps it on this\n" +
			"machine.\n\n" +
			"--from takes a layers file or the slug of one of your projects on Framework\n" +
			"Cloud. A project from the cloud gets FW_PROJECT_TOKEN and FW_CLOUD_URL in its\n" +
			".env. --pull-token creates a project from a project token instead of your\n" +
			"login; fw asks for the token, or reads it from stdin.\n\n" +
			"--blueprint, --structure, --package-set, --skill-set, --skill, --tool,\n" +
			"--sensitive, --ai, and --git build the project from pieces on Framework\n" +
			"Cloud, with your defaults there filling the rest; --defaults uses only your\n" +
			"defaults. These need fw login. At a terminal fw then asks whether to also\n" +
			"create the project on the cloud, defaulting to No. Without a terminal it\n" +
			"creates it there only with --cloud. --no-cloud keeps it on this machine.\n\n" +
			"--zip has Framework Cloud build the project and downloads it into the\n" +
			"folder, so R and Python don't need to be installed. --github OWNER[/REPO]\n" +
			"creates the project in a new GitHub repository, private unless --public,\n" +
			"waits for it, and clones it here (--no-clone leaves it on GitHub). Both\n" +
			"create the project on Framework Cloud and need fw login.\n\n" +
			"At a terminal, fw new with no name asks for the name, language, and\n" +
			"blueprint. Logged in, fw asks how you want the project: built here,\n" +
			"downloaded, or on GitHub. --local, --zip, and --github answer that\n" +
			"question, and without a terminal fw never asks.",
		Example: "  fw new\n" +
			"  fw new vaccine-study\n" +
			"  fw new survey --lang python --zip\n" +
			"  fw new survey --blueprint framework/project --github my-lab/survey\n" +
			"  fw new survey --blueprint framework/project --tool renv --sensitive\n" +
			"  fw new --from vaccine-study\n" +
			"  fw new --from .framework/layers.json --dir . --headless\n" +
			"  fw new --pull-token --url https://framework.example.edu",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			return runNew(cmd, a, o, name)
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.from, "from", "", "a layers file, or the slug of your project on Framework Cloud")
	f.BoolVar(&o.pullToken, "pull-token", false, "create the project from a project token (asked for, or read from stdin)")
	f.StringVar(&o.language, "lang", "r", "language for a project without --from: r, python, or stata")
	f.StringVar(&o.kind, "type", "", "project type for a project without --from: project (the Standard Project, the default), project_sensitive, or bare; R and Python also take course and presentation, Stata does not")
	f.StringVar(&o.dir, "dir", "", "folder to create the project in (default: ./<name>)")
	f.BoolVar(&o.headless, "headless", false, "run without prompts, for automation; the language package refuses to overwrite existing work")
	f.BoolVar(&o.print, "print", false, "print the command instead of running it")
	f.StringVar(&o.url, "url", "", "Framework Cloud server")
	f.BoolVar(&o.offline, "offline", false, "build from your saved Framework Cloud defaults, or the built-ins, without contacting the server")
	f.StringVar(&o.workflow, "workflow", "", "how notebooks are written: quarto, jupyter, or scripts for R and Python; scripts only for Stata")
	addPieceFlags(cmd, &o.pieces)
	addDestFlags(cmd, &o.dest)
	cmd.MarkFlagsMutuallyExclusive("from", "pull-token")
	return cmd
}

func runNew(cmd *cobra.Command, a *app, o newOptions, name string) error {
	ctx := cmd.Context()
	errOut := cmd.ErrOrStderr()
	fromCloud := o.pullToken || (o.from != "" && !isLayersPath(o.from))

	if (o.from != "" || o.pullToken) && (name != "" || cmd.Flags().Changed("lang") || o.kind != "") {
		return usageError("NAME, --lang, and --type describe a new project; with --from or --pull-token they come from the layers")
	}
	if o.headless && fromCloud {
		return usageError("--headless works with a layers file, for automation. Fetch the layers first, or drop --headless")
	}
	if err := checkDestFlags(cmd, o); err != nil {
		return err
	}
	// Plain fw new at a terminal asks for the name, language, and blueprint
	if a.interactive && name == "" && o.from == "" && !o.pullToken && !o.headless && !o.print && o.workflow == "" {
		var err error
		if name, err = a.walkthrough(cmd, &o); err != nil {
			return err
		}
		o.language, _ = cmd.Flags().GetString("lang")
		o.pieces.blueprint, _ = cmd.Flags().GetString("blueprint")
	}
	pieces := usesPieces(cmd)
	switch {
	case pieces && (o.from != "" || o.pullToken):
		return usageError("--blueprint, --structure, and the other cloud choices describe a new project; --from and --pull-token take an existing one")
	case pieces && o.kind != "":
		given := strings.Join(changedPieceFlags(cmd), ", ")
		return usageError(fmt.Sprintf("--type picks one of fw's built-in setups and cannot be combined with cloud choices (%s). Drop --type to build from pieces on Framework Cloud, or drop %s to use the built-in setup", given, given))
	case pieces && o.headless:
		return usageError("--headless works with a layers file, for automation. Drop it to build from cloud choices")
	case o.workflow != "" && o.workflow != "quarto" && o.workflow != "jupyter" && o.workflow != "scripts":
		return usageError("--workflow takes quarto, jupyter, or scripts")
	case o.workflow != "" && (pieces || o.from != "" || o.pullToken):
		return usageError("--workflow describes a project built from your defaults; with --from, --pull-token, or cloud choices the layers decide it")
	case o.offline && (pieces || o.from != "" || o.pullToken):
		return usageError("--offline builds from your saved defaults; --from, --pull-token, and cloud choices need the server")
	case pieces && o.print && o.pieces.cloud:
		return usageError("--print only shows the command, so it cannot create the project on the cloud. Drop --cloud")
	}

	// Gather the layers, if any
	var env *spec.Envelope
	var link *cloudLink
	switch {
	case o.from != "" && !fromCloud:
		e, err := spec.ReadFile(o.from)
		if err != nil {
			return err
		}
		env = &e
	case o.from != "":
		client, err := a.userClient(o.url)
		if err != nil {
			return err
		}
		raw, err := client.ProjectLayers(ctx, o.from)
		if err != nil {
			return explain(err, client.URL)
		}
		e, err := spec.Parse(raw)
		if err != nil {
			return fmt.Errorf("the layers from %s %w", cloud.Host(client.URL), err)
		}
		env = &e
		link = &cloudLink{slug: o.from, client: client, url: client.URL}
	case o.pullToken:
		server, err := a.server(o.url, cloud.UserScope)
		if err != nil {
			return err
		}
		token, err := a.readProjectToken(cmd)
		if err != nil {
			return err
		}
		raw, err := cloud.NewClient(server.URL, token).PullLayers(ctx)
		if err != nil {
			if cloud.StatusOf(err) == 403 {
				return fmt.Errorf("%w. --pull-token needs a project token from the project's page on %s", err, cloud.Host(server.URL))
			}
			return explain(err, server.URL)
		}
		e, err := spec.Parse(raw)
		if err != nil {
			return fmt.Errorf("the layers from %s %w", cloud.Host(server.URL), err)
		}
		env = &e
		link = &cloudLink{token: token, url: server.URL}
	}

	language := o.language
	if env != nil {
		name = env.Name
		language = env.Language()
	}
	if language == "stata" {
		if err := checkStataChoices(o); err != nil {
			return err
		}
	}
	if name == "" {
		if !a.interactive {
			return usageError("give a project name: fw new NAME")
		}
		fmt.Fprint(errOut, "Project name: ")
		var err error
		if name, err = a.readLine(); err != nil {
			return err
		}
		if name == "" {
			return errors.New("a project name is required")
		}
	}

	// Downloaded from the cloud or set up on GitHub, instead of built here
	dest, err := a.destination(cmd, &o, name)
	if err != nil {
		return err
	}
	if dest != destLocal {
		return a.newOnCloud(cmd, o, name, dest)
	}

	dir, err := targetDir(a, o.dir, name)
	if err != nil {
		return err
	}
	if !o.headless && !o.print {
		if err := checkEmptyTarget(dir); err != nil {
			return err
		}
	}
	if pieces {
		if o.print {
			o.pieces.noCloud = true
		}
		e, l, err := a.layersFromPieces(cmd, o, name)
		if err != nil {
			return err
		}
		env, link = e, l
		language = env.Language()
	}

	switch language {
	case "r", "python":
	case "stata":
		if env == nil && o.kind == "" {
			e, err := a.savedDefaults(cmd, o, name, language)
			if err != nil {
				return err
			}
			env = e
		}
		if env == nil {
			layer, _ := json.Marshal(map[string]string{"language": "stata"})
			if o.kind != "" {
				layer, _ = json.Marshal(map[string]string{"language": "stata", "type": o.kind})
			}
			env = &spec.Envelope{
				SpecVersion: 1,
				Name:        name,
				Layers:      []spec.Entry{{Kind: "project", Layer: layer}},
				Source:      json.RawMessage(`{"origin":"cli"}`),
			}
		}
		return newStata(cmd, o, env, dir, link)
	default:
		return fmt.Errorf("unknown language %q: use r, python, or stata", language)
	}

	// Without layers, the language's new() builds the project from the user's
	// own defaults. It may prompt, so it is interactive only at a terminal.
	req := engine.Request{Name: name, Type: o.kind, Dir: dir, Interactive: !o.headless, Workflow: o.workflow, Offline: o.offline}
	if env == nil {
		req.Interactive = req.Interactive && a.interactive
	}
	// Logged in, R's new() asks the server for your defaults. Rscript cannot
	// ask you anything, so when the server is down fw asks at a terminal,
	// the same as for Stata, and otherwise names --offline if R stops.
	offlineHint := ""
	if language == "r" && env == nil && !o.offline && !o.print {
		if reason := a.cloudUnreachable(cmd, o.url); reason != "" {
			if !a.interactive {
				offlineHint = reason
			} else {
				yes, err := a.askYes(cmd, reason+". Create the project offline, from your saved defaults or Framework's built-in ones?")
				if err != nil {
					return err
				}
				if !yes {
					return errors.New("canceled; no project was created")
				}
				req.Offline = true
			}
		}
	}
	keepLayers := o.print
	if env != nil {
		path, err := writeLayersFile(env)
		if err != nil {
			return err
		}
		defer func() {
			if !keepLayers {
				_ = os.Remove(path)
			}
		}()
		req.LayersFile = path
	}

	command, err := a.engines.Create(language, req)
	var missing *engine.MissingError
	if errors.As(err, &missing) {
		keepLayers = true
		if req.LayersFile != "" {
			fmt.Fprintf(errOut, "The layers are saved in %s.\n", req.LayersFile)
		}
		return err
	}
	if err != nil {
		return err
	}
	if o.print {
		fmt.Fprintln(cmd.OutOrStdout(), command.String())
		if req.LayersFile != "" {
			fmt.Fprintf(errOut, "The layers are saved in %s.\n", req.LayersFile)
		}
		return nil
	}

	fmt.Fprintf(errOut, "Creating %s with the %s package in %s\n", name, packageName(language), dir)
	if code, err := a.run(command); err != nil {
		return err
	} else if code != 0 {
		err := fmt.Errorf("%s exited with status %d; the project was not created", filepath.Base(command.Program), code)
		if offlineHint != "" {
			err = fmt.Errorf("%w. fw %s. Try again when it is available, or add --offline to use your saved defaults or Framework's built-in ones", err, offlineHint)
		}
		return &exitError{code: code, err: err}
	}

	if link != nil {
		if err := linkProject(cmd, link, dir); err != nil {
			return fmt.Errorf("the project was created, but linking it to the cloud failed: %w", err)
		}
	}
	return nil
}

// cloudUnreachable says why the user's server cannot be reached, or returns
// "" when it answers or the user is not logged in.
func (a *app) cloudUnreachable(cmd *cobra.Command, flag string) string {
	client, err := a.userClient(flag)
	if err != nil {
		return ""
	}
	if _, err := client.Me(cmd.Context()); cloud.IsOffline(err) {
		host := cloud.Host(client.URL)
		if status := cloud.StatusOf(err); status != 0 {
			return fmt.Sprintf("%s answered HTTP %d", host, status)
		}
		return "cannot reach " + host
	}
	return ""
}

func linkProject(cmd *cobra.Command, link *cloudLink, dir string) error {
	token, url := link.token, link.url
	if project.EnvValue(dir, "FW_PROJECT_TOKEN") != "" {
		fmt.Fprintln(cmd.OutOrStdout(), "Kept the FW_PROJECT_TOKEN already in .env; fw did not link the project again.")
		return nil
	}
	if link.slug != "" {
		hostname, _ := os.Hostname()
		minted, err := link.client.NewPullToken(cmd.Context(), link.slug, "fw on "+hostname)
		if err != nil {
			return explain(err, link.client.URL)
		}
		token = minted.Token
		if minted.URL != "" {
			url = minted.URL
		}
	}
	if project.EnvTracked(dir) {
		return fmt.Errorf("git tracks %s, so the project token would be committed. "+
			"Run `git rm --cached .env`, add .env to .gitignore, then run fw project link in %s", filepath.Join(dir, ".env"), dir)
	}
	out := cmd.OutOrStdout()
	ignored, err := project.IgnoreEnv(dir)
	if err != nil {
		return fmt.Errorf("could not add .env to .gitignore, so the project token was not written: %w", err)
	}
	if ignored {
		fmt.Fprintln(out, "Added .env to .gitignore so the project token stays out of git.")
	}
	added, err := project.LinkCloud(dir, token, url)
	if err != nil {
		return err
	}
	if len(added) > 0 {
		fmt.Fprintf(out, "Linked to %s: added %s to .env.\n", cloud.Host(url), strings.Join(added, " and "))
	}
	return nil
}

func (a *app) readProjectToken(cmd *cobra.Command) (string, error) {
	var token string
	var err error
	if a.interactive {
		fmt.Fprint(cmd.ErrOrStderr(), "Paste the project token (input is hidden): ")
		token, err = a.readSecret()
	} else {
		token, err = a.readLine()
		if errors.Is(err, io.EOF) {
			return "", usageError("no project token on stdin. Pipe one in, as in: fw new --pull-token < token.txt")
		}
	}
	token = strings.TrimSpace(token)
	if err == nil && token == "" {
		err = errors.New("no project token given")
	}
	return token, err
}

// isLayersPath reports whether --from names a file rather than a slug. A
// folder with the slug's name, as fw new SLUG --cloud leaves, is not a file.
func isLayersPath(from string) bool {
	if strings.HasSuffix(from, ".json") || strings.ContainsAny(from, `/\`) {
		return true
	}
	info, err := os.Stat(from)
	return err == nil && !info.IsDir()
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(name string) string {
	return strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(name), "-"), "-")
}

func targetDir(a *app, dir, name string) (string, error) {
	if dir == "" {
		slug := slugify(name)
		if slug == "" {
			return "", fmt.Errorf("cannot make a folder name from %q; use --dir", name)
		}
		dir = slug
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(a.cwd, dir)
	}
	return filepath.Clean(dir), nil
}

func checkEmptyTarget(dir string) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(entries) > 0 {
		return fmt.Errorf("%s already exists and is not empty", dir)
	}
	return nil
}

func writeLayersFile(env *spec.Envelope) (string, error) {
	data, err := env.Marshal()
	if err != nil {
		return "", err
	}
	f, err := os.CreateTemp("", "fw-layers-*.json")
	if err != nil {
		return "", err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return "", err
	}
	return f.Name(), f.Close()
}

func packageName(language string) string {
	if language == "python" {
		return "framework-py"
	}
	return "framework R"
}

// checkStataChoices rejects a --type or --workflow that Stata projects do not
// have, naming the ones they do.
func checkStataChoices(o newOptions) error {
	types := strings.Join(stata.Types[:len(stata.Types)-1], ", ") + ", or " + stata.Types[len(stata.Types)-1]
	if o.workflow != "" && o.workflow != "scripts" {
		return usageError(fmt.Sprintf("Stata projects have no %s workflow: they take --workflow scripts only, with --type %s. quarto and jupyter are for R and Python projects", o.workflow, types))
	}
	if o.kind != "" && !slices.Contains(stata.Types, o.kind) {
		other := ""
		switch o.kind {
		case "presentation":
			other = ". For slides, use --type project, export the figures to outputs/figures, and build the deck in an R or Python presentation project"
		case "course":
			other = ". course and presentation are for R and Python projects"
		}
		return usageError(fmt.Sprintf("fw has no Stata %q type: Stata projects take --type %s, with --workflow scripts%s", o.kind, types, other))
	}
	return nil
}

// newStata scaffolds a Stata project. fw builds these itself.
func newStata(cmd *cobra.Command, o newOptions, env *spec.Envelope, dir string, link *cloudLink) error {
	resolved, err := stata.Resolve(*env, version)
	if err != nil {
		return err
	}
	if o.print {
		data, err := json.MarshalIndent(resolved.Doc, "", "  ")
		if err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), string(data))
		fmt.Fprintf(cmd.ErrOrStderr(), "fw would scaffold this Stata project in %s.\n", dir)
		return nil
	}
	result, err := stata.Scaffold(resolved, stata.Options{Dir: dir, Headless: o.headless, Now: time.Now()})
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Created the Stata project %s in %s (%d files).\n", resolved.View.Name, dir, len(result.Created))
	for _, note := range result.Notes {
		fmt.Fprintln(out, note)
	}
	if link != nil {
		if err := linkProject(cmd, link, dir); err != nil {
			return fmt.Errorf("the project was created, but linking it to the cloud failed: %w", err)
		}
	}
	if resolved.View.Type != "bare" {
		fmt.Fprintln(out, "Next: open Stata in that folder and run: do master.do")
	}
	return nil
}
