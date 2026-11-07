package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"

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

// builtinTypes have a built-in structure, framework/<type>, on Framework
// Cloud.
var builtinTypes = []string{"project", "project_sensitive", "course", "presentation", "bare"}

var validSlug = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// localSpec is the part of .framework/spec.json that describes the project
// to Framework Cloud.
type localSpec struct {
	Name             string                          `json:"name"`
	Type             string                          `json:"type"`
	Language         string                          `json:"language"`
	Directories      map[string]string               `json:"directories"`
	ExtraDirectories map[string]cloud.ExtraDirectory `json:"extra_directories"`
	Security         struct {
		Sensitive bool `json:"sensitive"`
	} `json:"security"`
	Git struct {
		UseGit *bool `json:"use_git"`
	} `json:"git"`
	AI struct {
		Enabled *bool `json:"enabled"`
	} `json:"ai"`
	Source struct {
		Adopted bool `json:"adopted"`
	} `json:"source"`
}

// createdLink is fw project link --create's --json output.
type createdLink struct {
	Slug        string `json:"slug"`
	URL         string `json:"url,omitempty"`
	Structure   string `json:"structure,omitempty"`
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
		msg := "This project is already linked: .env has FW_PROJECT_TOKEN. Nothing changed."
		if slug != "" {
			msg = fmt.Sprintf("Already linked to %s. Nothing changed.", slug)
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

	// 3. A project with this slug already exists: offer to link to it.
	if existing, err := client.Project(ctx, slug); err == nil {
		if a.interactive && !a.jsonOut {
			yes, err := a.askYes(cmd, fmt.Sprintf("You already have a project %s (%s) on %s. Link this folder to it instead?", slug, existing.Name, host))
			if err != nil {
				return createdLink{}, err
			}
			if yes {
				if err := linkProjectTo(cmd, out, &cloudLink{slug: slug, client: client, url: client.URL}, root); err != nil {
					return createdLink{}, err
				}
				fmt.Fprintln(out, "To send this folder's spec to it, run: fw project push-spec")
				return createdLink{Slug: slug, Linked: true, Message: "linked to the existing project"}, nil
			}
		}
		return createdLink{}, &exitError{code: exitFailure, reason: "slug_taken", hint: "fw project link --from " + slug, err: fmt.Errorf(
			"you already have a project %s on %s. To link this folder to it, run fw project link --from %s. To create a new project, pass --slug with another name", slug, host, slug)}
	} else if cloud.StatusOf(err) != http.StatusNotFound {
		return createdLink{}, explain(err, client.URL)
	}

	// 4. The layout: the built-in structure for a project made by Framework,
	// or the project's own folders, saved as a private structure.
	structure := "framework/" + s.Type
	if s.Source.Adopted || !slices.Contains(builtinTypes, s.Type) || len(s.ExtraDirectories) > 0 {
		dirs := s.Directories
		if dirs == nil {
			dirs = map[string]string{}
		}
		saved, err := client.CreateStructure(ctx, cloud.NewStructure{Name: name + " layout", Description: "The folders of " + name + ", from fw project link --create",
			Directories: dirs, ExtraDirectories: s.ExtraDirectories})
		if err != nil {
			return createdLink{}, fmt.Errorf("saving the project's layout as a structure failed: %w", explain(err, client.URL))
		}
		structure = saved.Key
	}

	// 5. Create the project. The server makes the slug from the name, so a
	// different --slug creates it under the slug, then renames it.
	createName := name
	if slugify(name) != slug {
		createName = slug
	}
	settings := map[string]any{"sensitive": s.Security.Sensitive}
	if s.Git.UseGit != nil {
		settings["git"] = *s.Git.UseGit
	}
	if s.AI.Enabled != nil {
		settings["ai"] = *s.AI.Enabled
	}
	created, err := client.CreateProject(ctx, cloud.Choices{Name: createName, Description: o.description, Language: s.Language, Structure: structure, Settings: settings})
	if err != nil {
		return createdLink{}, explain(err, client.URL)
	}
	fmt.Fprintf(out, "Created %s on %s: %s\n", created.Slug, host, created.URL)
	finish := func(step string, err error) error {
		return fmt.Errorf("created %s on %s, but %s failed: %w. To finish, run: fw project link --from %s && fw project push-spec", created.Slug, host, step, err, created.Slug)
	}
	if createName != name {
		if err := client.RenameProject(ctx, created.Slug, name); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "Warning: could not name the project %q: %v. Rename it on its page.\n", name, err)
		}
	}

	// 6. Link: the project token and server go in .env, which git ignores.
	if err := linkProjectTo(cmd, out, &cloudLink{token: created.Token, url: client.URL}, root); err != nil {
		return createdLink{}, finish("linking this folder", err)
	}

	// 7. Send the spec with the new project token.
	res, err := cloud.NewClient(client.URL, created.Token).PushSpec(ctx, data)
	if err != nil {
		return createdLink{}, fmt.Errorf("created %s on %s and linked this folder, but sending the spec failed: %w. Run: fw project push-spec", created.Slug, host, explainProject(err))
	}
	fmt.Fprintln(out, "Sent .framework/spec.json.")
	return createdLink{Slug: created.Slug, URL: created.URL, Structure: structure, Created: true, Linked: true, SpecChanged: res.Changed}, nil
}
