package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/frameworktools/framework-cli/internal/cloud"
	"github.com/frameworktools/framework-cli/internal/project"
	pub "github.com/frameworktools/framework-cli/internal/publish"
)

// fw publish --to github copies a rendered document into the repository's
// published/ folder, commits only that folder, and pushes. The workflow that
// Framework Cloud adds to the repository deploys published/ to GitHub Pages.
// Documents never pass through the cloud.

const pagesPublicNote = "GitHub Pages sites are public, even when the repository is private."

// pagesResult is fw publish --to github --json.
type pagesResult struct {
	Driver     string  `json:"driver"`
	Version    int     `json:"version"`
	VersionURL string  `json:"version_url"`
	URL        string  `json:"url"`
	Commit     *string `json:"commit"`
	Pushed     bool    `json:"pushed"`
	// PagesEnabled is null when fw cannot ask Framework Cloud.
	PagesEnabled *bool `json:"pages_enabled"`
}

// gitRepo runs git in one repository.
type gitRepo struct {
	dir string
	// noPrompt stops git from asking for credentials without a terminal.
	noPrompt bool
}

func (g gitRepo) run(args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", g.dir}, args...)...) // #nosec G204 -- fixed git subcommands
	if g.noPrompt {
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	}
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errOut.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", args[0], msg)
	}
	return strings.TrimSpace(out.String()), nil
}

var githubRemote = regexp.MustCompile(`^(?:https?://(?:[^@/]+@)?github\.com/|git@github\.com:|ssh://git@github\.com/)([^/]+)/([^/]+?)(?:\.git)?/?$`)

// parseGitHubRemote returns the owner and repository of a GitHub remote URL.
func parseGitHubRemote(url string) (owner, repo string, ok bool) {
	m := githubRemote.FindStringSubmatch(strings.TrimSpace(url))
	if m == nil {
		return "", "", false
	}
	return m[1], m[2], true
}

// pagesSiteURL works out the Pages address of a repository from its name.
func pagesSiteURL(owner, repo string) string {
	host := strings.ToLower(owner) + ".github.io"
	if strings.EqualFold(repo, host) {
		return "https://" + host + "/"
	}
	return "https://" + host + "/" + repo + "/"
}

// githubRemoteOf picks the remote to push to: the current branch's
// upstream remote, then origin, then any GitHub remote. It reads the
// configured URL, before any insteadOf rewriting.
func githubRemoteOf(g gitRepo, branch string) (name, owner, repo string, err error) {
	var candidates []string
	if r, err := g.run("config", "--get", "branch."+branch+".remote"); err == nil && r != "" {
		candidates = append(candidates, r)
	}
	candidates = append(candidates, "origin")
	if all, err := g.run("remote"); err == nil {
		candidates = append(candidates, strings.Fields(all)...)
	}
	for _, r := range candidates {
		url, err := g.run("config", "--get", "remote."+r+".url")
		if err != nil {
			continue
		}
		if owner, repo, ok := parseGitHubRemote(url); ok {
			return r, owner, repo, nil
		}
	}
	return "", "", "", errors.New("this project's git repository has no GitHub remote. Create the project on GitHub first, then publish again")
}

// defaultBranch returns the remote's default branch, or "" if git cannot
// tell.
func defaultBranch(g gitRepo, remote string) string {
	if ref, err := g.run("symbolic-ref", "--short", "refs/remotes/"+remote+"/HEAD"); err == nil {
		return strings.TrimPrefix(ref, remote+"/")
	}
	out, err := g.run("ls-remote", "--symref", remote, "HEAD")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(line, "ref: refs/heads/"); ok {
			return strings.Fields(rest)[0]
		}
	}
	return ""
}

var pagesSlugRe = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// pagesSlug turns a file name into a folder name for published/.
func pagesSlug(name string) string {
	return pub.Slug(strings.TrimSuffix(name, filepath.Ext(name)))
}

// pagesFiles lists what to copy into published/<slug>/: a source path and
// its name in that folder. A rendered file becomes index.html, next to the
// _files folders it refers to. A folder is copied as it is and must open at
// index.html.
func pagesFiles(path string) (map[string]string, []string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, nil, fmt.Errorf("refusing to publish %s: it is a symbolic link", path)
	}
	files := map[string]string{}
	var sources []string
	add := func(src, dst string) {
		files[src] = filepath.ToSlash(dst)
		sources = append(sources, src)
	}
	if info.IsDir() {
		rels, err := bundleFiles(path)
		if err != nil {
			return nil, nil, err
		}
		hasIndex := false
		for _, rel := range rels {
			hasIndex = hasIndex || filepath.ToSlash(rel) == "index.html"
			add(filepath.Join(path, rel), rel)
		}
		if !hasIndex {
			return nil, nil, fmt.Errorf("%s has no index.html, which GitHub Pages opens first", path)
		}
		return files, sources, nil
	}
	ext := strings.ToLower(filepath.Ext(path))
	if ext != ".html" && ext != ".htm" {
		return nil, nil, fmt.Errorf("fw publishes HTML: a rendered Quarto document or another .html file. Render %s first", filepath.Base(path))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	dir := filepath.Dir(path)
	add(path, "index.html")
	for _, res := range resourceDirs(dir, data) {
		rels, err := bundleFiles(filepath.Join(dir, res))
		if err != nil {
			return nil, nil, err
		}
		for _, rel := range rels {
			add(filepath.Join(dir, res, rel), filepath.Join(res, rel))
		}
	}
	return files, sources, nil
}

// replaceDir makes dir hold exactly files, mapping source paths to names
// inside dir.
func replaceDir(dir string, files map[string]string) error {
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	for src, rel := range files {
		dst := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
			return err
		}
		if err := copyFile(src, dst); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src) // #nosec G304 -- a file the user asked to publish
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644) // #nosec G302 G304 -- published web files
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// cloudPagesURL asks Framework Cloud for the repository's Pages address.
// With a project token, GET /api/v1/me reports it. Servers from before
// pages_url was added there need the project's details, read with the
// user's login. ok is false when fw cannot ask.
func (a *app) cloudPagesURL(ctx context.Context, root, flag string) (pagesURL *string, ok bool) {
	token := project.EnvValue(root, "FW_PROJECT_TOKEN")
	if token == "" {
		return nil, false
	}
	server, err := a.server(flag, cloud.ProjectScope)
	if err != nil {
		return nil, false
	}
	var me struct {
		Kind     string          `json:"kind"`
		Project  string          `json:"project"`
		PagesURL json.RawMessage `json:"pages_url"`
	}
	if err := cloud.NewClient(server.URL, token).Do(ctx, http.MethodGet, "/api/v1/me", nil, &me); err != nil || me.Kind != "project" || me.Project == "" {
		return nil, false
	}
	if me.PagesURL != nil {
		var u *string
		if json.Unmarshal(me.PagesURL, &u) != nil {
			return nil, false
		}
		return u, true
	}
	found, loggedIn := a.tokens().Lookup(server.URL)
	if !loggedIn {
		return nil, false
	}
	detail, err := cloud.NewClient(server.URL, found.Token).Project(ctx, me.Project)
	if err != nil {
		return nil, false
	}
	if detail.GitHub == nil {
		return nil, true
	}
	return detail.GitHub.PagesURL, true
}

type pagesOptions struct {
	path, slug, urlFlag string
	includePrivate, yes bool
	artifact            *staticPublishOptions
}

func (a *app) publishToGitHub(cmd *cobra.Command, opts pagesOptions) error {
	root, err := a.requireProject()
	if err != nil {
		return err
	}
	g := gitRepo{dir: root, noPrompt: !a.interactive}
	top, err := g.run("rev-parse", "--show-toplevel")
	if err != nil {
		return errors.New("this project is not in a git repository. Create the project on GitHub first, then publish again")
	}
	g.dir = filepath.Clean(top)
	branch, err := g.run("symbolic-ref", "--short", "HEAD")
	if err != nil {
		return errors.New("the repository is not on a branch. Check out the default branch and publish again")
	}
	remote, owner, repo, err := githubRemoteOf(g, branch)
	if err != nil {
		return err
	}

	slug := opts.slug
	if slug == "" {
		slug = pagesSlug(filepath.Base(opts.path))
	}
	if !pagesSlugRe.MatchString(slug) {
		return usageError(fmt.Sprintf("%q cannot name a folder in published/. Use lowercase letters, digits, dashes, dots, and underscores, as in --slug results", slug))
	}
	var files map[string]string
	var sources []string
	var artifact *pub.Bundle
	if opts.artifact != nil {
		artifact, err = buildStatic(*opts.artifact)
		if err != nil {
			return err
		}
		defer os.RemoveAll(artifact.Dir)
		slug = artifact.Manifest.Slug
		sources = artifact.Sources
		files = map[string]string{}
		rels, e := bundleFiles(artifact.Dir)
		if e != nil {
			return e
		}
		for _, rel := range rels {
			files[filepath.Join(artifact.Dir, rel)] = filepath.ToSlash(rel)
		}
	} else {
		files, sources, err = pagesFiles(opts.path)
		if err != nil {
			return err
		}
	}

	errOut := cmd.ErrOrStderr()
	rel := "published/" + slug
	fmt.Fprintf(errOut, "Publishing %s (%d files) to %s/ in %s/%s on GitHub.\n", filepath.Base(opts.path), len(files), rel, owner, repo)
	fmt.Fprintln(errOut, pagesPublicNote)
	asked, err := a.checkPrivate(cmd, root, sources, opts.includePrivate, opts.yes)
	if err != nil {
		return err
	}
	if !asked {
		if err := a.confirm(cmd, opts.yes, "Publish it?"); err != nil {
			return err
		}
	}

	var delivered pub.Receipt
	if artifact != nil {
		delivered, err = pub.Folder(artifact, filepath.Join(g.dir, "published"), "")
		if err != nil {
			return err
		}
	} else if err := replaceDir(filepath.Join(g.dir, "published", slug), files); err != nil {
		return err
	}
	// Measure the complete resulting site, including every retained archive
	// and other artifact, before staging, committing, or pushing anything.
	siteBytes, sizeErr := pagesSiteSize(filepath.Join(g.dir, "published"))
	if sizeErr != nil {
		return sizeErr
	}
	if err := pagesSiteSizePolicy(siteBytes, errOut); err != nil {
		return err
	}
	if _, err := g.run("add", "-A", "--", rel, "published/index.html", "published/index.json"); err != nil {
		return err
	}
	// R and Python projects ignore *_files/, which holds a Quarto page's
	// styles and figures. Force-add exactly the files fw wrote, then check
	// that git has every one before committing.
	wrote := pagesWritten(g.dir, rel, files, artifact, delivered.Version)
	if err := g.addForce(wrote); err != nil {
		return err
	}
	if missing, err := g.notStaged(wrote); err != nil {
		return err
	} else if len(missing) > 0 {
		return fmt.Errorf("git did not stage %d published files, such as %s; nothing was committed or pushed. Check the repository's .gitignore and git settings", len(missing), missing[0])
	}
	res := pagesResult{Driver: "github", Version: delivered.Version}
	if _, err := g.run("diff", "--cached", "--quiet", "--", rel, "published/index.html", "published/index.json"); err != nil {
		if _, err := g.run("commit", "-m", "Publish "+slug, "--", rel, "published/index.html", "published/index.json"); err != nil {
			return err
		}
		sha, err := g.run("rev-parse", "HEAD")
		if err != nil {
			return err
		}
		res.Commit = &sha
		if _, err := g.run("rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}"); err == nil {
			_, err = g.run("push")
			if err != nil {
				return fmt.Errorf("committed %s, but pushing failed: %w. Run git push when it can reach GitHub", sha[:7], err)
			}
		} else if _, err := g.run("push", "-u", remote, "HEAD"); err != nil {
			return fmt.Errorf("committed %s, but pushing failed: %w. Run git push when it can reach GitHub", sha[:7], err)
		}
		res.Pushed = true
	}
	if def := defaultBranch(g, remote); def != "" && def != branch {
		fmt.Fprintf(errOut, "Warning: GitHub Pages deploys only from %s, and this is %s. The document goes live once it reaches %s.\n", def, branch, def)
	}

	site := pagesSiteURL(owner, repo)
	if pagesURL, ok := a.cloudPagesURL(cmd.Context(), root, opts.urlFlag); ok {
		enabled := pagesURL != nil && *pagesURL != ""
		res.PagesEnabled = &enabled
		if enabled {
			site = strings.TrimSuffix(*pagesURL, "/") + "/"
		} else {
			server, _ := a.server(opts.urlFlag, cloud.ProjectScope)
			fmt.Fprintf(errOut, "Warning: GitHub Pages is off for this repository. Turn it on under Projects at %s/projects.\n", strings.TrimSuffix(server.URL, "/"))
		}
	}
	res.URL = site + slug + "/"
	if artifact != nil {
		delivered.Driver = "github"
		delivered.URL = res.URL
		delivered.VersionURL = fmt.Sprintf("%sv/%d/", res.URL, delivered.Version)
		res.VersionURL = delivered.VersionURL
		delivered.Commit = res.Commit
		a.trackExternal(cmd, artifact, delivered, opts.urlFlag, "public")
	}

	out := cmd.OutOrStdout()
	if a.jsonOut {
		return writeJSON(out, res)
	}
	if res.Commit == nil {
		fmt.Fprintf(out, "No change: %s/ already holds this version.\n", rel)
	} else {
		fmt.Fprintf(out, "Published %s in commit %s and pushed it.\n", slug, (*res.Commit)[:7])
	}
	fmt.Fprintf(out, "Link: %s\n", res.URL)
	if res.Commit != nil {
		fmt.Fprintln(out, "GitHub Pages takes a minute or two to deploy it.")
	}
	return nil
}

// pagesWritten lists the files fw wrote for one publish, relative to the
// repository with forward slashes: the latest copy, its v/N archive, and
// the site index. Paths missing on disk are left out.
func pagesWritten(top, rel string, files map[string]string, artifact *pub.Bundle, version int) []string {
	var paths []string
	if artifact != nil {
		names := []string{"artifact.json"}
		for _, f := range artifact.Manifest.Files {
			names = append(names, f.Path)
		}
		for _, name := range names {
			paths = append(paths, rel+"/"+name, fmt.Sprintf("%s/v/%d/%s", rel, version, name))
		}
		paths = append(paths, "published/index.html", "published/index.json")
	} else {
		for _, name := range files {
			paths = append(paths, rel+"/"+name)
		}
	}
	kept := paths[:0]
	for _, p := range paths {
		if info, err := os.Lstat(filepath.Join(top, filepath.FromSlash(p))); err == nil && info.Mode().IsRegular() {
			kept = append(kept, p)
		}
	}
	sort.Strings(kept)
	return kept
}

// literal stops git from reading glob characters in file names.
func literal(paths []string) []string {
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = ":(literal)" + p
	}
	return out
}

// addForce stages paths even when .gitignore matches them, in batches.
func (g gitRepo) addForce(paths []string) error {
	for start := 0; start < len(paths); start += 200 {
		batch := paths[start:min(start+200, len(paths))]
		if _, err := g.run(append([]string{"add", "-f", "--"}, literal(batch)...)...); err != nil {
			return err
		}
	}
	return nil
}

// notStaged returns the paths that are not in git's index.
func (g gitRepo) notStaged(paths []string) ([]string, error) {
	staged := map[string]bool{}
	for start := 0; start < len(paths); start += 200 {
		batch := paths[start:min(start+200, len(paths))]
		out, err := g.run(append([]string{"ls-files", "--cached", "-z", "--"}, literal(batch)...)...)
		if err != nil {
			return nil, err
		}
		for _, name := range strings.Split(out, "\x00") {
			staged[name] = true
		}
	}
	var missing []string
	for _, p := range paths {
		if !staged[p] {
			missing = append(missing, p)
		}
	}
	return missing, nil
}

const pagesWarnBytes int64 = 800_000_000
const pagesLimitBytes int64 = 1_000_000_000

func pagesSiteSize(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("GitHub Pages site contains a symbolic link: %s", path)
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		return nil
	})
	return total, err
}
func pagesSiteSizePolicy(size int64, out io.Writer) error {
	if size > pagesLimitBytes {
		return fmt.Errorf("publishing would exceed the 1 GB GitHub Pages site limit (%d bytes, including retained versions); nothing was committed or pushed", size)
	}
	if size >= pagesWarnBytes {
		fmt.Fprintf(out, "Warning: GitHub Pages site uses %.1f MB of its 1 GB limit, including retained versions.\n", float64(size)/1e6)
	}
	return nil
}
