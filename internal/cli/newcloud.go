package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/frameworktools/framework-cli/internal/cloud"
	"github.com/frameworktools/framework-cli/internal/config"
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
	return len(changedPieceFlags(cmd)) > 0
}

// changedPieceFlags names the cloud piece flags given, as "--git, --tool".
func changedPieceFlags(cmd *cobra.Command) []string {
	var names []string
	for _, name := range pieceFlags {
		if cmd.Flags().Changed(name) {
			names = append(names, "--"+name)
		}
	}
	return names
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
		res, err := client.Resolve(cmd.Context(), choices)
		if err != nil {
			return nil, nil, explain(err, client.URL)
		}
		printNotices(cmd, res.Notices)
		raw = res.Layers
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

// savedDefaults returns the layers for a project built from the person's
// defaults on Framework Cloud, or nil to build from fw's built-ins. Signed
// in, it resolves on the server and saves the result, so a later offline
// project can use it. When the server cannot be reached it uses the saved
// layers and says so. With none saved, it asks at a terminal and otherwise
// stops unless --offline is given. Not logged in, there are no cloud
// defaults, so it returns nil.
func (a *app) savedDefaults(cmd *cobra.Command, o newOptions, name, language string) (*spec.Envelope, error) {
	errOut := cmd.ErrOrStderr()
	client, err := a.userClient(o.url)
	if err != nil {
		var notLoggedIn *errNotLoggedIn
		if errors.As(err, &notLoggedIn) {
			return nil, nil
		}
		return nil, err
	}
	host := cloud.Host(client.URL)

	reason := "--offline was given"
	if !o.offline {
		res, err := client.Resolve(cmd.Context(), cloud.Choices{Language: language})
		if err == nil {
			env, err := parseNamed(res.Layers, name)
			if err != nil {
				return nil, fmt.Errorf("the layers from %s %w", host, err)
			}
			cache := config.CachedDefaults{FetchedAt: time.Now().UTC(), Revision: res.Revision, Envelope: res.Layers}
			if err := a.dirs.SaveDefaults(host, language, cache); err != nil {
				fmt.Fprintf(errOut, "Warning: could not save your defaults for offline use: %v\n", err)
			}
			fmt.Fprintf(errOut, "Using your Framework Cloud defaults from %s.\n", host)
			printNotices(cmd, res.Notices)
			return &env, nil
		}
		if !cloud.IsOffline(err) {
			return nil, explain(err, client.URL)
		}
		reason = "cannot reach " + host
		if status := cloud.StatusOf(err); status != 0 {
			reason = fmt.Sprintf("%s answered HTTP %d", host, status)
		}
	}

	if cached, ok := a.dirs.LoadDefaults(host, language); ok {
		env, err := parseNamed(cached.Envelope, name)
		if err != nil {
			return nil, fmt.Errorf("your saved defaults from %s %w", host, err)
		}
		fmt.Fprintf(errOut, "Using your Framework Cloud defaults from %s, saved %s (%s).\n",
			host, cached.FetchedAt.Local().Format("2006-01-02"), reason)
		return &env, nil
	}

	missing := fmt.Sprintf("there are no saved Framework Cloud defaults for %s from %s", language, host)
	switch {
	case o.offline:
		fmt.Fprintf(errOut, "Using fw's built-in defaults: %s.\n", missing)
		return nil, nil
	case a.interactive:
		yes, err := a.askYes(cmd, fmt.Sprintf("%s, and %s. Create the project from fw's built-in defaults?", reason, missing))
		if err != nil {
			return nil, err
		}
		if !yes {
			return nil, errors.New("canceled; no project was created")
		}
		return nil, nil
	default:
		return nil, fmt.Errorf("%s, and %s. Try again when %s is available, or add --offline to use fw's built-in defaults", reason, missing, host)
	}
}

// printNotices shows the server's notices about how it resolved the layers.
func printNotices(cmd *cobra.Command, notices []string) {
	for _, n := range notices {
		fmt.Fprintf(cmd.ErrOrStderr(), "Note from the server: %s\n", n)
	}
}

// parseNamed validates layers resolved from defaults alone after giving
// them the new project's name in place of the server's placeholder.
func parseNamed(raw []byte, name string) (spec.Envelope, error) {
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return spec.Envelope{}, fmt.Errorf("are not valid JSON: %w", err)
	}
	doc["name"] = name
	data, err := json.Marshal(doc)
	if err != nil {
		return spec.Envelope{}, err
	}
	return spec.Parse(data)
}
