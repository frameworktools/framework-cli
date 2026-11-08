package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/frameworktools/framework-cli/internal/cloud"
	"github.com/frameworktools/framework-cli/internal/project"
)

// createOptions are fw project link --create's flags, which fw adopt
// --cloud passes on.
type createOptions struct {
	name, slug, description, url string
	public, includePrivate       bool
}

var validSlug = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// localSpec is the part of .framework/spec.json that describes the project
// to Framework Cloud.
type localSpec struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Language string `json:"language"`
	Source   struct {
		Adopted bool `json:"adopted"`
	} `json:"source"`
}

// createdLink is fw project link --create's --json output.
type createdLink struct {
	Slug        string `json:"slug"`
	URL         string `json:"url,omitempty"`
	Type        string `json:"type,omitempty"`
	Adopted     bool   `json:"adopted"`
	Created     bool   `json:"created"`
	Linked      bool   `json:"linked"`
	SpecChanged bool   `json:"spec_changed"`
	Message     string `json:"message,omitempty"`
}

// linkCreate is fw project link --create.
func (a *app) linkCreate(cmd *cobra.Command, root string, o createOptions) error {
	res, err := a.createLink(cmd, root, o)
	if err != nil || !a.jsonOut {
		return err
	}
	return writeJSON(cmd.OutOrStdout(), res)
}

// createLink creates the Framework Cloud project for the local project at
// root from its spec, links it with a project token in .env, and sends the
// spec. It changes nothing when the project is already linked. It reports
// on stdout, or on stderr with --json, and returns the --json result.
func (a *app) createLink(cmd *cobra.Command, root string, o createOptions) (createdLink, error) {
	out := cmd.OutOrStdout()
	if a.jsonOut {
		out = cmd.ErrOrStderr()
	}
	ctx := cmd.Context()

	// 1. Already linked: say so and change nothing.
	if token := project.EnvValue(root, "FW_PROJECT_TOKEN"); token != "" {
		slug := ""
		if server, err := a.server(o.url, cloud.ProjectScope); err == nil {
			if me, err := cloud.NewClient(server.URL, token).Me(ctx); err == nil && me.Kind == "project" {
				slug = me.Project
			}
		}
		msg := "This project is already linked: .env has FW_PROJECT_TOKEN. Nothing changed. To send its spec, run: fw project push-spec"
		if slug != "" {
			msg = fmt.Sprintf("Already linked to %s. Nothing changed. To send its spec, run: fw project push-spec", slug)
		}
		fmt.Fprintln(out, msg)
		return createdLink{Slug: slug, Linked: true, Message: msg}, nil
	}

	// 2. Local checks, before anything is created.
	if o.public {
		return createdLink{}, usageError("Framework Cloud projects are private to your account; there is no public project to make. " +
			"Publish artifacts or reports with public visibility instead. Leave out --public (or pass --private)")
	}
	specPath := filepath.Join(root, ".framework", "spec.json")
	data, err := os.ReadFile(specPath)
	if errors.Is(err, os.ErrNotExist) {
		return createdLink{}, errors.New("this project has no .framework/spec.json, which Framework Cloud is made from. Run fw project upgrade --apply first, then fw project link --create")
	}
	if err != nil {
		return createdLink{}, err
	}
	var s localSpec
	if err := json.Unmarshal(data, &s); err != nil {
		return createdLink{}, fmt.Errorf(".framework/spec.json is not valid JSON: %w", err)
	}
	if s.Language == "" {
		s.Language = "r"
	}
	if data, err = checkSpecForCloud(cmd.ErrOrStderr(), data, "fw project link --create"); err != nil {
		return createdLink{}, err
	}
	if project.EnvTracked(root) {
		return createdLink{}, fmt.Errorf("git tracks %s, so the project token would be committed. Run `git rm --cached .env`, then fw project link --create again", filepath.Join(root, ".env"))
	}
	if _, err := a.checkPrivate(cmd, root, []string{specPath}, o.includePrivate, false); err != nil {
		return createdLink{}, err
	}
	name := o.name
	for _, n := range []string{s.Name, filepath.Base(root)} {
		if name == "" {
			name = n
		}
	}
	slug := o.slug
	if slug == "" {
		slug = slugify(name)
	}
	if !validSlug.MatchString(slug) {
		return createdLink{}, usageError(fmt.Sprintf("%q cannot be a slug: use lowercase letters, digits, and hyphens, as in --slug old-study", slug))
	}

	client, err := a.userClient(o.url)
	if err != nil {
		return createdLink{}, err
	}
	host := cloud.Host(client.URL)

	// 3. Create the project from its spec, at its slug, in one request. The
	// server stores the spec as the project's only layer, so the cloud
	// matches the folder, and records it as received.
	created, err := client.CreateProjectFromSpec(ctx, cloud.SpecProject{Spec: data, Name: name, Slug: slug, Description: o.description, Adopted: s.Source.Adopted})
	if field := cloud.FieldProblem(err, "slug"); field != "" {
		return createdLink{}, a.slugTaken(cmd, out, client, root, slug, field)
	}
	if err != nil {
		return createdLink{}, explain(err, client.URL)
	}
	fmt.Fprintf(out, "Created %s on %s: %s\n", created.Slug, host, created.URL)
	// A server from before the spec API (framework-cloud ef3224d) ignores
	// the spec and slug, builds the project from the user's defaults, and
	// does not report a type. fw then sends the spec separately.
	stored := created.Type != ""
	if !stored {
		fmt.Fprintf(cmd.ErrOrStderr(), "Warning: %s is older than the spec API, so it made the project from your defaults at %s. fw sends the spec next; the project's layers stay your defaults until the server is updated.\n", host, created.Slug)
	}
	finish := func(step string, err error) error {
		return fmt.Errorf("created %s on %s, but %s failed: %w. To finish, run: fw project link --from %s", created.Slug, host, step, err, created.Slug)
	}

	// 4. Link: the project token and server go in .env, which git ignores.
	if err := linkProjectTo(cmd, out, &cloudLink{token: created.Token, url: client.URL}, root); err != nil {
		return createdLink{}, finish("linking this folder", err)
	}

	// 5. An older server needs the spec sent with the new project token.
	if !stored {
		if _, err := cloud.NewClient(client.URL, created.Token).PushSpec(ctx, data); err != nil {
			return createdLink{}, fmt.Errorf("created %s on %s and linked this folder, but sending the spec failed: %w. Run: fw project push-spec", created.Slug, host, explainProject(err))
		}
		fmt.Fprintln(out, "Sent .framework/spec.json.")
	}
	return createdLink{Slug: created.Slug, URL: created.URL, Type: created.Type, Adopted: created.Adopted, Created: true, Linked: true, SpecChanged: true}, nil
}

// slugTaken handles a slug the server refused on create. A slug of one of
// the user's projects gets the offer to link to it instead (--from); one
// held by a project in the Trash, or a slug refused for another reason,
// is an error.
func (a *app) slugTaken(cmd *cobra.Command, out io.Writer, client *cloud.Client, root, slug, problem string) error {
	host := cloud.Host(client.URL)
	if strings.Contains(problem, "deleted") {
		return &exitError{code: exitFailure, reason: "slug_in_trash", err: fmt.Errorf(
			"%s is the slug of a project in your Trash on %s, so it can't be used again. Restore that project and run fw project link --from %s, or pass --slug with another name", slug, host, slug)}
	}
	if !strings.Contains(problem, "already have") {
		return usageError(fmt.Sprintf("%s refused the slug %s: %s", host, slug, problem))
	}
	if a.interactive && !a.jsonOut {
		yes, err := a.askYes(cmd, fmt.Sprintf("You already have a project %s on %s. Link this folder to it instead?", slug, host))
		if err != nil {
			return err
		}
		if yes {
			if err := linkProjectTo(cmd, out, &cloudLink{slug: slug, client: client, url: client.URL}, root); err != nil {
				return err
			}
			fmt.Fprintln(out, "To send this folder's spec to it, run: fw project push-spec")
			return nil
		}
	}
	return &exitError{code: exitFailure, reason: "slug_taken", hint: "fw project link --from " + slug, err: fmt.Errorf(
		"you already have a project %s on %s. To link this folder to it, run fw project link --from %s. To create a new project, pass --slug with another name", slug, host, slug)}
}
