package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/frameworktools/framework-cli/internal/cloud"
	"github.com/frameworktools/framework-cli/internal/spec"
)

// pieceOptions are fw new's flags for building a project from pieces on
// Framework Cloud.
type pieceOptions struct {
	blueprint, structure     string
	packageSets, skillSets   []string
	skills, tools, noTools   []string
	sensitive, ai, git       bool
	defaults, cloud, noCloud bool
}

func addPieceFlags(cmd *cobra.Command, p *pieceOptions) {
	f := cmd.Flags()
	f.StringVar(&p.blueprint, "blueprint", "", "start from this blueprint on Framework Cloud, as in fw blueprints list")
	f.StringVar(&p.structure, "structure", "", "use this project structure, as in fw structures list")
	f.StringArrayVar(&p.packageSets, "package-set", nil, "add a package set (repeatable)")
	f.StringArrayVar(&p.skillSets, "skill-set", nil, "add a skill set (repeatable)")
	f.StringArrayVar(&p.skills, "skill", nil, "add a skill (repeatable)")
	f.StringArrayVar(&p.tools, "tool", nil, "turn on a tool, such as quarto or renv (repeatable)")
	f.StringArrayVar(&p.noTools, "no-tool", nil, "turn off a tool the blueprint or your defaults turn on (repeatable)")
	f.BoolVar(&p.sensitive, "sensitive", false, "split data and outputs into private and public folders; --sensitive=false turns it off")
	f.BoolVar(&p.ai, "ai", false, "set up AI assistants and skills; --ai=false turns them off")
	f.BoolVar(&p.git, "git", false, "create a git repository; --git=false skips it")
	f.BoolVar(&p.defaults, "defaults", false, "build the project from your defaults on Framework Cloud")
	f.BoolVar(&p.cloud, "cloud", false, "also create the project on Framework Cloud, without asking")
	f.BoolVar(&p.noCloud, "no-cloud", false, "create the project only on this machine, without asking")
	cmd.MarkFlagsMutuallyExclusive("cloud", "no-cloud")
}

// pieceFlags are the flags that make fw new ask Framework Cloud.
var pieceFlags = []string{"blueprint", "structure", "package-set", "skill-set", "skill", "tool", "no-tool", "sensitive", "ai", "git", "defaults", "cloud"}

func usesPieces(cmd *cobra.Command) bool {
	for _, name := range pieceFlags {
		if cmd.Flags().Changed(name) {
			return true
		}
	}
	return false
}

func (p pieceOptions) choices(cmd *cobra.Command, name, language string) cloud.Choices {
	c := cloud.Choices{
		Name: name, Blueprint: p.blueprint, Structure: p.structure,
		PackageSets: p.packageSets, SkillSets: p.skillSets, Skills: p.skills,
	}
	if cmd.Flags().Changed("lang") {
		c.Language = language
	}
	if len(p.tools)+len(p.noTools) > 0 {
		c.Tools = map[string]bool{}
		for _, t := range p.tools {
			c.Tools[t] = true
		}
		for _, t := range p.noTools {
			c.Tools[t] = false
		}
	}
	settings := map[string]any{}
	for flag, value := range map[string]bool{"sensitive": p.sensitive, "ai": p.ai, "git": p.git} {
		if cmd.Flags().Changed(flag) {
			settings[flag] = value
		}
	}
	if len(settings) > 0 {
		c.Settings = settings
	}
	return c
}

// layersFromPieces asks Framework Cloud to build the project's layers from
// the flags and the user's defaults. When the person agrees, or passes
// --cloud, it also creates the project there and returns its link.
func (a *app) layersFromPieces(cmd *cobra.Command, o newOptions, name string) (*spec.Envelope, *cloudLink, error) {
	client, err := a.userClient(o.url)
	if err != nil {
		var notLoggedIn *errNotLoggedIn
		if errors.As(err, &notLoggedIn) {
			return nil, nil, &errNotLoggedIn{"--blueprint and the other cloud choices need a login. " + err.Error() +
				". Without a login, fw new NAME --lang LANG creates a project from the language's own defaults"}
		}
		return nil, nil, err
	}
	host := cloud.Host(client.URL)
	choices := o.pieces.choices(cmd, name, o.language)

	create := o.pieces.cloud
	note := ""
	if !create && !o.pieces.noCloud {
		if a.interactive {
			if err := a.ask(cmd, fmt.Sprintf("Also create this project on %s?", host)); err == nil {
				create = true
			} else {
				note = "Creating it only on this machine."
			}
		} else {
			note = fmt.Sprintf("Creating it only on this machine. Add --cloud to also create it on %s.", host)
		}
	}

	var raw []byte
	var link *cloudLink
	if create {
		created, err := client.CreateProject(cmd.Context(), choices)
		if err != nil {
			return nil, nil, explain(err, client.URL)
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "Created %s on %s: %s\n", created.Slug, host, created.URL)
		raw, err = client.ProjectLayers(cmd.Context(), created.Slug)
		if err != nil {
			return nil, nil, fmt.Errorf("created %s on %s, but fetching its layers failed: %w. Run: fw new --from %s", created.Slug, host, explain(err, client.URL), created.Slug)
		}
		link = &cloudLink{token: created.Token, url: client.URL}
	} else {
		raw, err = client.Resolve(cmd.Context(), choices)
		if err != nil {
			return nil, nil, explain(err, client.URL)
		}
	}
	env, err := spec.Parse(raw)
	if err != nil {
		return nil, nil, fmt.Errorf("the layers from %s %w", host, err)
	}
	if note != "" {
		fmt.Fprintln(cmd.ErrOrStderr(), note)
	}
	return &env, link, nil
}
