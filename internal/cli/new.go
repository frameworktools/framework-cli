package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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
	pieces    pieceOptions
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
			"Stata projects itself: folders, master.do, settings.do, and do-file templates.\n\n" +
			"Without --from, fw creates an R project from your R defaults (author,\n" +
			"directories, and packages), the same as framework::new().\n\n" +
			"--from takes a layers file or the slug of one of your projects on Framework\n" +
			"Cloud. A project from the cloud gets FW_PROJECT_TOKEN and FW_CLOUD_URL in its\n" +
			".env. --pull-token creates a project from a project token instead of your\n" +
			"login; fw asks for the token, or reads it from stdin.\n\n" +
			"--blueprint, --structure, --package-set, --skill-set, --skill, --tool,\n" +
			"--sensitive, --ai, and --git build the project from pieces on Framework\n" +
			"Cloud, with your defaults there filling the rest; --defaults uses only your\n" +
			"defaults. These need fw login. At a terminal fw then asks whether to also\n" +
			"create the project on the cloud, defaulting to No. Without a terminal it\n" +
			"creates it there only with --cloud. --no-cloud keeps it on this machine.",
		Example: "  fw new vaccine-study\n" +
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
	f.StringVar(&o.kind, "type", "", "project type for a project without --from: project, project_sensitive, course, presentation, or bare")
	f.StringVar(&o.dir, "dir", "", "folder to create the project in (default: ./<name>)")
	f.BoolVar(&o.headless, "headless", false, "run without prompts, for automation; the language package refuses to overwrite existing work")
	f.BoolVar(&o.print, "print", false, "print the command instead of running it")
	f.StringVar(&o.url, "url", "", "Framework Cloud server")
	addPieceFlags(cmd, &o.pieces)
	cmd.MarkFlagsMutuallyExclusive("from", "pull-token")
	return cmd
}

func runNew(cmd *cobra.Command, a *app, o newOptions, name string) error {
	ctx := cmd.Context()
	errOut := cmd.ErrOrStderr()
	fromCloud := o.pullToken || (o.from != "" && !isLayersPath(o.from))

	if (o.from != "" || o.pullToken) && (name != "" || cmd.Flags().Changed("lang") || o.kind != "") {
		return errors.New("NAME, --lang, and --type describe a new project; with --from or --pull-token they come from the layers")
	}
	if o.headless && fromCloud {
		return errors.New("--headless works with a layers file, for automation. Fetch the layers first, or drop --headless")
	}
	pieces := usesPieces(cmd)
	switch {
	case pieces && (o.from != "" || o.pullToken):
		return usageError("--blueprint, --structure, and the other cloud choices describe a new project; --from and --pull-token take an existing one")
	case pieces && o.kind != "":
		return usageError("--type picks one of fw's built-in setups; with --blueprint or --structure, the cloud's pieces decide instead")
	case pieces && o.headless:
		return usageError("--headless works with a layers file, for automation. Drop it to build from cloud choices")
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
	if name == "" {
		if !a.interactive {
			return errors.New("give a project name: fw new NAME")
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
	req := engine.Request{Name: name, Type: o.kind, Dir: dir, Interactive: !o.headless}
	if env == nil {
		req.Interactive = req.Interactive && a.interactive
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
		return &exitError{code: code, err: fmt.Errorf("%s exited with status %d; the project was not created", filepath.Base(command.Program), code)}
	}

	if link != nil {
		if err := linkProject(cmd, link, dir); err != nil {
			return fmt.Errorf("the project was created, but linking it to the cloud failed: %w", err)
		}
	}
	return nil
}

func linkProject(cmd *cobra.Command, link *cloudLink, dir string) error {
	token, url := link.token, link.url
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
	added, err := project.LinkCloud(dir, token, url)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	if len(added) > 0 {
		fmt.Fprintf(out, "Linked to %s: added %s to .env.\n", cloud.Host(url), strings.Join(added, " and "))
	}
	if !project.GitignoresEnv(dir) {
		fmt.Fprintln(out, "Warning: no .gitignore line ignores .env, which holds the project token. Add .env to .gitignore before you commit.")
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
	}
	token = strings.TrimSpace(token)
	if err == nil && token == "" {
		err = errors.New("no project token given")
	}
	return token, err
}

// isLayersPath reports whether --from names a file rather than a slug.
func isLayersPath(from string) bool {
	if strings.HasSuffix(from, ".json") || strings.ContainsAny(from, `/\`) {
		return true
	}
	_, err := os.Stat(from)
	return err == nil
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
