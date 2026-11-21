package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	pub "github.com/frameworktools/framework-cli/internal/publish"
)

func newSnippetCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "snippet",
		Short: "Publish code snippets from executed notebooks",
		Long: "A code snippet is a notebook cell's code and the text it printed. Blog\n" +
			"posts and reports embed it beside the results it produced.",
	}
	cmd.AddCommand(newSnippetPublishCmd(a))
	return cmd
}

func newSnippetPublishCmd(a *app) *cobra.Command {
	var f publishFlags
	var cell, repository string
	var folded, unfolded, noOutput bool
	var links []string
	cmd := &cobra.Command{
		Use:   "publish NOTEBOOK.ipynb --cell LABEL",
		Short: "Publish one cell of an executed notebook as a code snippet",
		Long: "Publishes one cell of a saved Jupyter notebook as a code_snippet artifact:\n" +
			"its code and the text outputs saved with it. Nothing is run. Run the\n" +
			"notebook and save it first.\n\n" +
			"--cell names the cell by a tag, a Quarto \"#| label:\" line, or the label\n" +
			"on its %%fw_snippet line. Quarto option lines and a leading\n" +
			"%%fw_snippet line are left out of the code.\n" +
			"Outputs keep their order: stdout as output, stderr as a message, the\n" +
			"text/plain form of results and displays, and errors with their\n" +
			"traceback. An output that carries an image is left out whole.\n\n" +
			"The snippet records the notebook's path in its git repository and the\n" +
			"commit checked out. It names a repository only when --repository or\n" +
			"publish.repository in settings.yml gives one.\n\n" +
			"Publishing goes through fw publish, to the same destinations. The slug\n" +
			"defaults to the label. Framework Cloud keeps the version when the\n" +
			"snippet has not changed.",
		Example: "  fw snippet publish analysis.ipynb --cell fit-ols\n" +
			"  fw snippet publish notebooks/models.ipynb --cell fit-ols --folded --yes\n" +
			"  fw snippet publish analysis.ipynb --cell fit-ols --link notebook=https://example.org/analysis --link model=ols-model",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if cell == "" {
				return usageError("--cell names the cell to publish, by tag or by its #| label: line")
			}
			if folded && unfolded {
				return usageError("pass --folded or --unfolded, not both")
			}
			path := args[0]
			if !filepath.IsAbs(path) {
				path = filepath.Join(a.cwd, path)
			}
			root, err := a.requireProject()
			if err != nil {
				return err
			}
			parsedLinks, err := parseSnippetLinks(links)
			if err != nil {
				return err
			}
			if repository == "" {
				if repository, err = settingsRepository(root); err != nil {
					return err
				}
			}
			if repository != "" && !httpsURL.MatchString(repository) {
				return usageError("the repository must be an https URL, such as https://github.com/owner/repo")
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			nb, err := readNotebook(data)
			if err != nil {
				return fmt.Errorf("%s: %w", filepath.Base(path), err)
			}
			found, err := nb.findCell(cell)
			if err != nil {
				return fmt.Errorf("%s: %w", filepath.Base(path), err)
			}
			language, err := nb.language()
			if err != nil {
				return fmt.Errorf("%s: %w", filepath.Base(path), err)
			}
			var fold *bool
			if folded || unfolded {
				fold = &folded
			}
			file, commit := snippetLocation(root, path)
			doc := snippetDocument{
				Schema: "framework.artifact/v1",
				Kind:   "code_snippet",
				Source: snippetSource{Language: language, File: file, Label: cell, Commit: commit, Repository: repository},
				Snippet: snippetBody{
					Blocks: cellBlocks(found, !noOutput, fold),
				},
			}
			if len(parsedLinks) > 0 {
				doc.Display = &snippetDisplay{Links: parsedLinks}
			}

			dir, err := os.MkdirTemp("", "fw-snippet-")
			if err != nil {
				return err
			}
			defer os.RemoveAll(dir)
			out := filepath.Join(dir, "code_snippet.json")
			body, err := json.MarshalIndent(doc, "", "  ")
			if err != nil {
				return err
			}
			if err := os.WriteFile(out, append(body, '\n'), 0o600); err != nil {
				return err
			}

			what := "code only"
			if n := len(doc.Snippet.Blocks) - 1; n > 0 {
				what = fmt.Sprintf("code and %d output %s", n, plural(n, "block", "blocks"))
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "Snippet from %s, cell %s: %s.\n", filepath.Base(path), cell, what)
			// The upload is a temporary file, so check the notebook's path.
			asked, err := a.checkPrivate(cmd, root, []string{path}, f.includePrivate, f.yes)
			if err != nil {
				return err
			}
			if asked {
				f.yes = true
			}
			if f.slug == "" {
				f.slug = pub.Slug(cell)
			}
			if f.title == "" {
				f.title = cell
			}
			f.kind = "code_snippet"
			return a.publishPath(cmd, out, f)
		},
	}
	cmd.Flags().StringVar(&cell, "cell", "", "the cell's tag or label")
	cmd.Flags().BoolVar(&folded, "folded", false, "fold the output blocks")
	cmd.Flags().BoolVar(&unfolded, "unfolded", false, "never fold the output blocks")
	cmd.Flags().BoolVar(&noOutput, "no-output", false, "publish the code only")
	cmd.Flags().StringArrayVar(&links, "link", nil, "notebook=URL or model=SLUG|URL; repeatable")
	cmd.Flags().StringVar(&repository, "repository", "", "the https repository URL to name (default: publish.repository in settings.yml)")
	cmd.Flags().StringVar(&f.slug, "slug", "", "the artifact's name in the project (default: from the label)")
	cmd.Flags().StringVar(&f.title, "title", "", "the artifact's title (default: the label)")
	cmd.Flags().StringVar(&f.description, "description", "", "the artifact description")
	cmd.Flags().StringVar(&f.visibility, "visibility", "", "artifact visibility (Cloud: public, unlisted, or private)")
	cmd.Flags().StringVar(&f.password, "password", "", "password required to view the artifact")
	cmd.Flags().StringVar(&f.collection, "collection", "", "existing project collection ID or slug")
	cmd.Flags().BoolVar(&f.shareUnlisted, "share-unlisted", false, "allow collection readers to access this unlisted artifact")
	cmd.Flags().StringVar(&f.to, "to", "", "named publishing destination (default: publish.default, then cloud)")
	cmd.Flags().BoolVar(&f.includePrivate, "include-private", false, "publish from a notebook whose path contains \"private\" without asking")
	cmd.Flags().BoolVarP(&f.yes, "yes", "y", false, "upload without asking")
	cmd.Flags().StringVar(&f.urlFlag, "url", "", "Framework Cloud server (default: the project's FW_CLOUD_URL)")
	addDisplayFlag(cmd, &f)
	cmd.Flags().BoolVar(&a.jsonOut, "json", false, jsonHelp)
	return cmd
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// The framework.artifact/v1 code_snippet document, in the R client's field
// order. Python sends no width.
type snippetDocument struct {
	Schema  string          `json:"schema"`
	Kind    string          `json:"kind"`
	Source  snippetSource   `json:"source"`
	Snippet snippetBody     `json:"snippet"`
	Display *snippetDisplay `json:"display,omitempty"`
}

type snippetSource struct {
	Language   string `json:"language"`
	File       string `json:"file,omitempty"`
	Label      string `json:"label,omitempty"`
	Commit     string `json:"commit,omitempty"`
	Repository string `json:"repository,omitempty"`
}

type snippetBody struct {
	Blocks []snippetBlock `json:"blocks"`
}

type snippetBlock struct {
	Type   string `json:"type"`
	Text   string `json:"text"`
	Folded *bool  `json:"folded,omitempty"`
}

type snippetDisplay struct {
	Links []snippetLink `json:"links"`
}

type snippetLink struct {
	Rel      string `json:"rel"`
	URL      string `json:"url,omitempty"`
	Artifact string `json:"artifact,omitempty"`
}

var (
	httpsURL     = regexp.MustCompile(`^https://\S+$`)
	artifactSlug = regexp.MustCompile(`^[A-Za-z0-9_-]{1,120}$`)
)

// parseSnippetLinks reads --link notebook=URL and --link model=SLUG|URL.
func parseSnippetLinks(values []string) ([]snippetLink, error) {
	var links []snippetLink
	for _, v := range values {
		rel, value, ok := strings.Cut(v, "=")
		if !ok || (rel != "notebook" && rel != "model") || value == "" {
			return nil, usageError("--link takes notebook=URL or model=SLUG, as in --link notebook=https://example.org/analysis")
		}
		switch {
		case httpsURL.MatchString(value):
			links = append(links, snippetLink{Rel: rel, URL: value})
		case rel == "model" && artifactSlug.MatchString(value):
			links = append(links, snippetLink{Rel: rel, Artifact: value})
		case rel == "model":
			return nil, usageError("a model link takes an artifact slug in this project or an https URL")
		default:
			return nil, usageError("a notebook link takes an https URL")
		}
	}
	return links, nil
}

// settingsRepository reads publish.repository from settings.yml, at the
// top level or under default.
func settingsRepository(root string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, "settings.yml"))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var doc struct {
		Publish struct {
			Repository string `yaml:"repository"`
		} `yaml:"publish"`
		Default struct {
			Publish struct {
				Repository string `yaml:"repository"`
			} `yaml:"publish"`
		} `yaml:"default"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return "", fmt.Errorf("settings.yml: %w", err)
	}
	if doc.Publish.Repository != "" {
		return doc.Publish.Repository, nil
	}
	return doc.Default.Publish.Repository, nil
}

var commitHash = regexp.MustCompile(`^[0-9a-f]{40}$`)

// snippetLocation returns the notebook's path from the top of its git
// repository, which a GitHub permalink needs, with the commit checked out.
// Outside git, the path is from the project root, else the file name.
func snippetLocation(root, path string) (file, commit string) {
	resolved := path
	if r, err := filepath.EvalSymlinks(path); err == nil {
		resolved = r
	}
	g := gitRepo{dir: filepath.Dir(resolved), noPrompt: true}
	base := ""
	if top, err := g.run("rev-parse", "--show-toplevel"); err == nil && top != "" {
		base = filepath.FromSlash(top)
		if head, err := g.run("rev-parse", "HEAD"); err == nil && commitHash.MatchString(head) {
			commit = head
		}
	} else {
		base = root
	}
	if r, err := filepath.EvalSymlinks(base); err == nil {
		base = r
	}
	if rel, err := filepath.Rel(base, resolved); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel) {
		return filepath.ToSlash(rel), commit
	}
	return filepath.Base(path), commit
}

// notebookText is nbformat's multiline string: a string or a list of them.
type notebookText string

func (t *notebookText) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*t = notebookText(s)
		return nil
	}
	var parts []string
	if err := json.Unmarshal(b, &parts); err != nil {
		return err
	}
	*t = notebookText(strings.Join(parts, ""))
	return nil
}

type notebook struct {
	Nbformat int `json:"nbformat"`
	Metadata struct {
		Kernelspec struct {
			Language string `json:"language"`
		} `json:"kernelspec"`
		LanguageInfo struct {
			Name string `json:"name"`
		} `json:"language_info"`
	} `json:"metadata"`
	Cells []notebookCell `json:"cells"`
}

type notebookCell struct {
	CellType string       `json:"cell_type"`
	Source   notebookText `json:"source"`
	Metadata struct {
		Tags []string `json:"tags"`
	} `json:"metadata"`
	ExecutionCount *int             `json:"execution_count"`
	Outputs        []notebookOutput `json:"outputs"`
}

type notebookOutput struct {
	OutputType string                     `json:"output_type"`
	Name       string                     `json:"name"`
	Text       notebookText               `json:"text"`
	Data       map[string]json.RawMessage `json:"data"`
	Ename      string                     `json:"ename"`
	Evalue     string                     `json:"evalue"`
	Traceback  []string                   `json:"traceback"`
	Metadata   struct {
		Framework struct {
			Role string `json:"role"`
		} `json:"framework"`
	} `json:"metadata"`
}

// snippetStatus marks the line %%fw_snippet shows after publishing, which
// is not part of the cell's output.
const snippetStatus = "snippet-status"

func readNotebook(data []byte) (*notebook, error) {
	var nb notebook
	if err := json.Unmarshal(data, &nb); err != nil {
		return nil, fmt.Errorf("not a Jupyter notebook: %w", err)
	}
	if nb.Nbformat != 4 {
		return nil, fmt.Errorf("fw reads Jupyter notebooks in format 4; this one is format %d", nb.Nbformat)
	}
	return &nb, nil
}

var (
	quartoLabel  = regexp.MustCompile(`^\s*#\|\s*label:\s*["']?([^"'\s]+)["']?\s*$`)
	quartoOption = regexp.MustCompile(`^\s*#\|`)
	snippetMagic = regexp.MustCompile(`^\s*%%fw_snippet(\s|$)`)
)

// findCell returns the one code cell tagged label, with a Quarto
// "#| label: label" line, or that ran %%fw_snippet label.
func (nb *notebook) findCell(label string) (*notebookCell, error) {
	var found []int
	for i := range nb.Cells {
		c := &nb.Cells[i]
		if c.CellType == "code" && c.matches(label) {
			found = append(found, i)
		}
	}
	switch len(found) {
	case 0:
		return nil, fmt.Errorf("no code cell is tagged %q or has the line \"#| label: %s\"", label, label)
	case 1:
	default:
		return nil, fmt.Errorf("%d code cells match %q (cells %s). Give each a different tag or label", len(found), label, cellNumbers(found))
	}
	c := &nb.Cells[found[0]]
	if c.ExecutionCount == nil && len(c.Outputs) == 0 {
		return nil, fmt.Errorf("cell %q has not been run. Run the notebook and save it, then publish again", label)
	}
	return c, nil
}

func cellNumbers(idx []int) string {
	parts := make([]string, len(idx))
	for i, n := range idx {
		parts[i] = fmt.Sprint(n + 1)
	}
	return strings.Join(parts, ", ")
}

func (c *notebookCell) matches(label string) bool {
	for _, tag := range c.Metadata.Tags {
		if tag == label {
			return true
		}
	}
	lines := strings.Split(string(c.Source), "\n")
	// A cell that ran %%fw_snippet LABEL names its label there.
	if fields := strings.Fields(lines[0]); len(fields) > 1 && fields[0] == "%%fw_snippet" && fields[1] == label {
		return true
	}
	for _, line := range lines {
		if m := quartoLabel.FindStringSubmatch(line); m != nil && m[1] == label {
			return true
		}
	}
	return false
}

// language is the notebook's kernel language. Python when unrecorded.
func (nb *notebook) language() (string, error) {
	lang := nb.Metadata.Kernelspec.Language
	if lang == "" {
		lang = nb.Metadata.LanguageInfo.Name
	}
	lang = strings.ToLower(strings.TrimSpace(lang))
	switch lang {
	case "":
		return "python", nil
	case "python", "r", "stata":
		return lang, nil
	}
	return "", fmt.Errorf("the notebook's language is %s. Code snippets take Python, R, or Stata", lang)
}

// cellCode is the cell's source without a leading %%fw_snippet line or
// Quarto option lines, and without blank lines at either end.
func cellCode(source string) string {
	lines := strings.Split(strings.ReplaceAll(source, "\r\n", "\n"), "\n")
	if len(lines) > 0 && snippetMagic.MatchString(lines[0]) {
		lines = lines[1:]
	}
	for len(lines) > 0 && (quartoOption.MatchString(lines[0]) || strings.TrimSpace(lines[0]) == "") {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

// cellBlocks turns a cell into snippet blocks: its code, then its text
// outputs in order. Stream pieces of the same name join as printed, and
// adjacent output blocks join on a newline, as the R client joins them.
func cellBlocks(c *notebookCell, output bool, folded *bool) []snippetBlock {
	blocks := []snippetBlock{{Type: "code", Text: cellCode(string(c.Source))}}
	if !output {
		return blocks
	}
	var pieces []snippetBlock
	lastStream := ""
	for _, o := range c.Outputs {
		var piece snippetBlock
		stream := ""
		switch o.OutputType {
		case "stream":
			stream = o.Name
			piece = snippetBlock{Type: "output", Text: string(o.Text)}
			if o.Name == "stderr" {
				piece.Type = "message"
			}
		case "execute_result", "display_data":
			if o.Metadata.Framework.Role == snippetStatus {
				continue
			}
			text, ok := plainText(o.Data)
			if !ok {
				continue
			}
			piece = snippetBlock{Type: "output", Text: text}
		case "error":
			text := strings.Join(o.Traceback, "\n")
			if len(o.Traceback) == 0 {
				text = o.Ename + ": " + o.Evalue
			}
			piece = snippetBlock{Type: "error", Text: text}
		default:
			continue
		}
		if stream != "" && stream == lastStream {
			pieces[len(pieces)-1].Text += piece.Text
		} else {
			pieces = append(pieces, piece)
		}
		lastStream = stream
	}
	for _, p := range pieces {
		p.Text = strings.TrimSuffix(p.Text, "\n")
		if p.Text == "" {
			continue
		}
		if last := &blocks[len(blocks)-1]; last.Type == "output" && p.Type == "output" {
			last.Text += "\n" + p.Text
			continue
		}
		if p.Type == "output" && folded != nil {
			v := *folded
			p.Folded = &v
		}
		blocks = append(blocks, p)
	}
	return blocks
}

// plainText is a result's text/plain form. An output that carries an image
// is left out whole, so a figure's "<Figure size ...>" text does not appear.
func plainText(data map[string]json.RawMessage) (string, bool) {
	for mime := range data {
		if strings.HasPrefix(mime, "image/") {
			return "", false
		}
	}
	raw, ok := data["text/plain"]
	if !ok {
		return "", false
	}
	var text notebookText
	if json.Unmarshal(raw, &text) != nil {
		return "", false
	}
	return string(text), true
}
