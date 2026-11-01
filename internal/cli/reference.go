package cli

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// The docs site's reference (docs-reference.v1) is generated from the command
// tree, so help text and the site never disagree. Everything here that is not
// in the commands themselves is listed below.

type refCategory struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Position    int      `json:"position"`
	Common      []string `json:"common,omitempty"`
}

var refCategories = []refCategory{
	{"account", "Account", "Log in to Framework Cloud and check who you are", 1, []string{"login", "status"}},
	{"cloud", "Cloud", "Your projects, blueprints, and published documents on Framework Cloud", 2, []string{"publish", "projects list"}},
	{"projects", "Projects", "Create, link, and prepare Framework projects on this machine", 3, []string{"new", "project bootstrap"}},
	{"setup", "Setup", "Configuration, diagnostics, and the installed version", 4, nil},
}

// refCategoryOf assigns each command to a category. A test fails when a
// command is missing.
var refCategoryOf = map[string]string{
	"login": "account", "logout": "account", "status": "account",
	"projects list": "cloud", "project show": "cloud", "blueprints list": "cloud", "publish": "cloud",
	"structures list": "cloud", "tools list": "cloud", "package-sets list": "cloud", "skills list": "cloud",
	"new": "projects", "project link": "projects", "project status": "projects", "project bootstrap": "projects",
	"project push-spec": "projects", "adopt": "projects", "structures import": "cloud",
	"config path": "setup", "config sync": "setup", "doctor": "setup", "version": "setup",
}

// refSince records the release that added each command.
var refSince = map[string]string{
	"projects list": "0.1.0", "login": "0.1.0", "logout": "0.1.0", "status": "0.1.0", "new": "0.1.0",
	"project link": "0.1.0", "project status": "0.1.0", "project bootstrap": "0.1.0",
	"config path": "0.1.0", "config sync": "0.1.0", "doctor": "0.1.0", "version": "0.1.0",
	"project show": "0.2.0", "blueprints list": "0.2.0", "publish": "0.2.0", "project push-spec": "0.2.0",
	"structures list": "0.3.0", "tools list": "0.3.0", "package-sets list": "0.3.0", "skills list": "0.3.0",
	"adopt": "0.4.0", "structures import": "0.4.0",
}

// refEnv names the environment variable that sets a flag.
var refEnv = map[string]string{"url": "FW_CLOUD_URL"}

var refSeeAlso = map[string][]string{
	"login":             {"status", "logout", "guide:agents"},
	"status":            {"login", "guide:agents"},
	"projects list":     {"project show", "new"},
	"project show":      {"projects list", "new"},
	"blueprints list":   {"structures list", "tools list", "package-sets list", "skills list"},
	"structures list":   {"blueprints list", "structures import", "new"},
	"structures import": {"structures list", "adopt", "new"},
	"adopt":             {"structures import", "project push-spec", "guide:projects"},
	"tools list":        {"blueprints list", "new"},
	"package-sets list": {"blueprints list", "new"},
	"skills list":       {"blueprints list", "new"},
	"publish":           {"project link", "guide:publishing", "r:ref:publish"},
	"new":               {"project bootstrap", "project link", "guide:projects"},
	"project link":      {"publish", "project push-spec"},
	"project status":    {"project bootstrap", "doctor"},
	"project bootstrap": {"new", "r:ref:bootstrap", "py:ref:bootstrap"},
	"project push-spec": {"project link", "project show"},
	"config sync":       {"config path"},
	"doctor":            {"project status", "config path"},
}

// refReturns describes the --json output of each command that has it.
var refReturns = map[string]string{
	"structures import": "With `--json` and no `--yes`, the proposed mapping and nothing saved: `{\"name\", \"language\", \"saved\": false, \"directories\", \"extra_directories\", \"warnings\"}`. " +
		"With `--yes`, the same with `\"saved\": true` and the new structure's `key`, `name`, and `url`. Each warning is `{\"code\": \"private_path\", \"path\": ...}`.",
	"adopt": "With `--json` (and `--yes`), `{\"path\", \"language\", \"created\", \"directories\", \"extra_directories\", \"warnings\"}`. `created` lists the files fw wrote.",
	"login": "With `--json`, stdout gets the same object as `fw status --json`. Without a terminal, the code to approve goes to stderr as one JSON line:\n\n" +
		"```json\n{\"user_code\": \"QGMK-JE6Q\", \"verification_url\": \"https://framework.pub/device?code=QGMK-JE6Q\", \"expires_in\": 600}\n```",
	"status": "With `--json`, an object with `server`, `server_source`, `logged_in`, `account` (`kind`, `name`, `email`, or `null`), `token_source`, " +
		"`problem` when not logged in, and `project_server` when the current project is linked to another server. Token values never appear.",
	"projects list":     "With `--json`, `{\"server\": ..., \"projects\": [...]}`. Each project has `slug`, `name`, `language`, `type`, `description`, and `created_at`.",
	"project show":      "With `--json`, the project: `slug`, `name`, `description`, `language`, `type`, `blueprint` (`key`, `version`, or `null`), `github` (`repository`, `url`, `private`, `scaffold_status`, `pages_url`, or `null`), `created_at`, `updated_at`, and `spec_received_at`.",
	"blueprints list":   "With `--json`, `{\"server\": ..., \"blueprints\": [...]}`. Each blueprint has `key`, `name`, `description`, `language`, `type`, `version`, `visibility`, `system`, `structure`, `tools`, `package_sets`, and `skill_sets`. Older servers list `facets` instead of the pieces.",
	"structures list":   "With `--json`, `{\"server\": ..., \"structures\": [...]}`, each with `key`, `name`, `description`, `type`, `version`, `system`, and `visibility`.",
	"tools list":        "With `--json`, `{\"server\": ..., \"tools\": [...]}`, each with `key`, `name`, `description`, `language` (a list), `requires`, `version`, `system`, and `visibility`.",
	"package-sets list": "With `--json`, `{\"server\": ..., \"package_sets\": [...]}`, each with `key`, `name`, `description`, `language`, `packages`, `version`, `system`, and `visibility`.",
	"skills list":       "With `--json`, `{\"server\": ..., \"skills\": [...]}`, or `skill_sets` with `--sets`. A skill set also has `language`, `skills`, and the `packages` they need.",
	"project status":    "With `--json`, an object with `root`, `language`, `language_source`, `runner` (a list), `runner_reason`, `cloud` (`linked`, `server`, `server_source`, `project`, `ok`, `problem`), and `warnings`.",
	"publish": "With `--json`, the document: `slug`, `version`, `url` (the link to share), `private_url`, `readable_url`, `version_url`, `raw_url`, and `notice`. " +
		"With `--to github`, `url`, `commit` (null when nothing changed), `pushed`, and `pages_enabled` (null when the project is not linked, or fw could not reach Framework Cloud).",
	"project push-spec": "With `--json`, `received_at`, `spec_version`, and `changed`, which is false when the cloud already had this spec.",
}

// refNeedsServer marks commands whose examples need a server or credentials.
var refNeedsServer = map[string]bool{
	"login": true, "logout": true, "status": true, "projects list": true, "project show": true,
	"blueprints list": true, "structures list": true, "tools list": true, "package-sets list": true, "skills list": true, "publish": true, "project link": true, "project push-spec": true, "new": true,
}

type refParam struct {
	Name        string `json:"name"`
	Type        string `json:"type,omitempty"`
	Default     string `json:"default,omitempty"`
	Required    bool   `json:"required,omitempty"`
	Env         string `json:"env,omitempty"`
	Description string `json:"description"`
}

type refExample struct {
	Code     string `json:"code"`
	Runnable *bool  `json:"runnable,omitempty"`
}

type refSection struct {
	Title   string `json:"title"`
	Content string `json:"content"`
}

type refEntry struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Kind        string       `json:"kind"`
	Category    string       `json:"category"`
	Title       string       `json:"title"`
	Summary     string       `json:"summary"`
	Usage       string       `json:"usage"`
	Description string       `json:"description,omitempty"`
	Parameters  []refParam   `json:"parameters,omitempty"`
	Returns     string       `json:"returns,omitempty"`
	Sections    []refSection `json:"sections,omitempty"`
	Examples    []refExample `json:"examples,omitempty"`
	SeeAlso     []string     `json:"see_also,omitempty"`
	Lifecycle   string       `json:"lifecycle"`
	Since       string       `json:"since,omitempty"`
}

type reference struct {
	Schema     string        `json:"schema"`
	Product    string        `json:"product"`
	Version    string        `json:"version"`
	Categories []refCategory `json:"categories"`
	Entries    []refEntry    `json:"entries"`
}

const exitCodesMarkdown = "| Code | Meaning |\n|------|---------|\n" +
	"| 0 | Success |\n| 1 | Failure |\n| 2 | Bad flags or arguments |\n" +
	"| 3 | Not logged in, or the server rejected the token |\n| 4 | The server has no such project or resource |\n" +
	"| 5 | Browser login was denied, or its code expired |\n" +
	"| 6 | An upload has \"private\" in a path; rerun with `--include-private` if you mean to send it |\n\n" +
	"With `--json`, a failure also prints one JSON line on stderr: `{\"error\": \"...\", \"exit_code\": 4}`. " +
	"Exit code 6 adds `\"reason\": \"private_path\"`."

// refCommands lists the documented commands: every runnable command except
// help and shell completion.
func refCommands(root *cobra.Command) []*cobra.Command {
	var out []*cobra.Command
	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			if sub.Hidden || sub.Name() == "help" || sub.Name() == "completion" {
				continue
			}
			if sub.Runnable() {
				out = append(out, sub)
			}
			walk(sub)
		}
	}
	walk(root)
	return out
}

func refName(c *cobra.Command) string {
	return strings.TrimPrefix(c.CommandPath(), c.Root().Name()+" ")
}

// refPositionals turns the argument words in Use, such as SLUG or [NAME],
// into parameters.
func refPositionals(c *cobra.Command) []refParam {
	var params []refParam
	for _, word := range strings.Fields(c.Use)[1:] {
		optional := strings.HasPrefix(word, "[")
		name := strings.Trim(word, "[]")
		if strings.HasPrefix(name, "-") || name == "flags" {
			continue
		}
		params = append(params, refParam{Name: name, Type: "string", Required: !optional, Description: refArgHelp[refName(c)+" "+name]})
	}
	return params
}

var refArgHelp = map[string]string{
	"new NAME":          "The project name. With `--from` or `--pull-token` it comes from the layers instead.",
	"project show SLUG": "The project's slug, as `fw projects list` shows it.",
	"publish PATH":      "An `.html` file, or a folder of rendered output.",
}

func refFlags(c *cobra.Command) []refParam {
	var params []refParam
	add := func(f *pflag.Flag) {
		if f.Name == "help" || f.Hidden {
			return
		}
		name := "--" + f.Name
		if f.Shorthand != "" {
			name = "-" + f.Shorthand + ", " + name
		}
		def := f.DefValue
		if f.Value.Type() == "bool" && def == "false" {
			def = ""
		}
		if def != "" {
			def = `"` + def + `"`
		}
		params = append(params, refParam{
			Name: name, Type: f.Value.Type(), Default: def, Env: refEnv[f.Name], Description: f.Usage,
		})
	}
	c.LocalFlags().VisitAll(add)
	c.InheritedFlags().VisitAll(add)
	sort.SliceStable(params, func(i, j int) bool {
		return strings.TrimLeft(params[i].Name, "-") < strings.TrimLeft(params[j].Name, "-")
	})
	return params
}

// refDescription turns hard-wrapped help text into Markdown paragraphs.
func refDescription(long string) string {
	var paras []string
	for _, para := range strings.Split(long, "\n\n") {
		paras = append(paras, strings.Join(strings.Fields(para), " "))
	}
	return strings.Join(paras, "\n\n")
}

// buildReference returns the docs reference for the command tree.
func buildReference(root *cobra.Command, version string) reference {
	ref := reference{Schema: "docs-reference.v1", Product: "cli", Version: strings.TrimPrefix(version, "v"), Categories: refCategories}
	for _, c := range refCommands(root) {
		name := refName(c)
		e := refEntry{
			ID: strings.ReplaceAll(name, " ", "-"), Name: name, Kind: "command", Category: refCategoryOf[name],
			Title: "fw " + name, Summary: c.Short, Usage: c.UseLine(), Lifecycle: "stable", Since: refSince[name],
			Returns: refReturns[name], SeeAlso: refSeeAlso[name],
		}
		if c.Long != "" {
			e.Description = refDescription(c.Long)
		}
		e.Parameters = append(refPositionals(c), refFlags(c)...)
		if c.Example != "" {
			var lines []string
			for _, line := range strings.Split(c.Example, "\n") {
				lines = append(lines, strings.TrimSpace(line))
			}
			ex := refExample{Code: strings.Join(lines, "\n")}
			if refNeedsServer[name] {
				no := false
				ex.Runnable = &no
			}
			e.Examples = []refExample{ex}
		}
		if c.Flags().Lookup("json") != nil || refNeedsServer[name] {
			e.Sections = []refSection{{Title: "Exit codes", Content: exitCodesMarkdown}}
		}
		ref.Entries = append(ref.Entries, e)
	}
	return ref
}

// Reference returns reference.json for the docs site.
func Reference(version string) ([]byte, error) {
	a := &app{getenv: func(string) string { return "" }}
	data, err := json.MarshalIndent(buildReference(newRootCmd(a), version), "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}
