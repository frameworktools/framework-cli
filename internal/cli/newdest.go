package cli

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/frameworktools/framework-cli/internal/cloud"
	"github.com/frameworktools/framework-cli/internal/engine"
)

// Where fw new puts the project: built here by the language's package, or
// built by Framework Cloud and downloaded, or scaffolded on GitHub.
const (
	destLocal  = "local"
	destZip    = "zip"
	destGitHub = "github"
)

// destOptions are fw new's flags for where the project goes.
type destOptions struct {
	zip, local, public, noClone bool
	github                      string // OWNER or OWNER/REPO
}

func addDestFlags(cmd *cobra.Command, d *destOptions) {
	f := cmd.Flags()
	f.BoolVar(&d.zip, "zip", false, "have Framework Cloud build the project and download it here; needs no R or Python")
	f.StringVar(&d.github, "github", "", "create the project on GitHub under OWNER (or OWNER/REPO) and clone it here")
	f.BoolVar(&d.public, "public", false, "with --github, make the repository public (default: private)")
	f.BoolVar(&d.noClone, "no-clone", false, "with --github, leave the repository on GitHub without cloning it")
	f.BoolVar(&d.local, "local", false, "build the project here with the language's own package, without asking")
	cmd.MarkFlagsMutuallyExclusive("zip", "github", "local")
}

// destFlag reports which destination flag was given, if any.
func (d destOptions) flag() string {
	switch {
	case d.zip:
		return destZip
	case d.github != "":
		return destGitHub
	case d.local:
		return destLocal
	}
	return ""
}

// checkDestFlags rejects destination flags that make no sense together
// with the other options.
func checkDestFlags(cmd *cobra.Command, o newOptions) error {
	dest := o.dest.flag()
	if (o.dest.public || o.dest.noClone) && dest != destGitHub {
		return usageError("--public and --no-clone go with --github")
	}
	if dest != destZip && dest != destGitHub {
		return nil
	}
	switch {
	case o.from != "" || o.pullToken:
		return usageError("--zip and --github create a new project on Framework Cloud; --from and --pull-token set up an existing one")
	case o.headless || o.print:
		return usageError("--zip and --github create the project on Framework Cloud; drop --headless and --print")
	case o.offline:
		return usageError("--zip and --github need the server; drop --offline")
	case o.pieces.noCloud || o.pieces.cloud:
		return usageError("--zip and --github always create the project on Framework Cloud; drop --cloud and --no-cloud")
	case o.kind != "":
		return usageError("--type picks one of fw's built-in setups; with --zip or --github, use --blueprint or --structure")
	}
	if cmd.Flags().Changed("github") {
		owner, repo, _ := strings.Cut(o.dest.github, "/")
		if owner == "" || strings.Contains(repo, "/") {
			return usageError("--github takes OWNER or OWNER/REPO")
		}
	}
	return nil
}

// choose asks a numbered question and returns the index picked. Enter
// takes the default. It asks again after an answer it doesn't understand.
func (a *app) choose(cmd *cobra.Command, question string, options []string, def int) (int, error) {
	errOut := cmd.ErrOrStderr()
	fmt.Fprintln(errOut, question)
	for i, opt := range options {
		fmt.Fprintf(errOut, "  %d) %s\n", i+1, opt)
	}
	for tries := 0; tries < 5; tries++ {
		fmt.Fprintf(errOut, "Choose 1-%d [%d]: ", len(options), def+1)
		answer, err := a.readLine()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return def, nil
			}
			return 0, err
		}
		if answer == "" {
			return def, nil
		}
		if n, err := strconv.Atoi(answer); err == nil && n >= 1 && n <= len(options) {
			return n - 1, nil
		}
		fmt.Fprintf(errOut, "Type a number from 1 to %d.\n", len(options))
	}
	return 0, errors.New("no choice was made; no project was created")
}

// askLine asks for a line of text, offering def for Enter.
func (a *app) askLine(cmd *cobra.Command, question, def string) (string, error) {
	if def != "" {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s [%s]: ", question, def)
	} else {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s: ", question)
	}
	answer, err := a.readLine()
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	if answer == "" {
		return def, nil
	}
	return answer, nil
}

// askYesDefault asks a yes-or-no question with the given default.
func (a *app) askYesDefault(cmd *cobra.Command, question string, def bool) (bool, error) {
	hint := "[y/N]"
	if def {
		hint = "[Y/n]"
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "%s %s ", question, hint)
	answer, err := a.readLine()
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	switch strings.ToLower(answer) {
	case "":
		return def, nil
	case "y", "yes":
		return true, nil
	}
	return false, nil
}

// walkthrough asks for what plain `fw new` at a terminal leaves out: the
// name, the language, and, logged in, the blueprint. Answers are set as the
// flags would set them, so the rest of fw new treats them the same.
func (a *app) walkthrough(cmd *cobra.Command, o *newOptions) (string, error) {
	errOut := cmd.ErrOrStderr()
	fmt.Fprintln(errOut, "Creating a new Framework project. Press Enter to take the answer in [brackets].")

	name, err := a.askLine(cmd, "Project name", "")
	if err != nil {
		return "", err
	}
	if name == "" {
		return "", errors.New("a project name is required")
	}

	if !cmd.Flags().Changed("lang") {
		languages := []string{"r", "python", "stata"}
		i, err := a.choose(cmd, "Which language?", []string{"R", "Python", "Stata"}, 0)
		if err != nil {
			return "", err
		}
		if err := cmd.Flags().Set("lang", languages[i]); err != nil {
			return "", err
		}
		o.language = languages[i]
	}

	if usesPieces(cmd) || o.kind != "" {
		return name, nil
	}
	client, err := a.userClient(o.url)
	if err != nil {
		var notLoggedIn *errNotLoggedIn
		if errors.As(err, &notLoggedIn) {
			fmt.Fprintln(errOut, "Not logged in, so fw uses the language's own defaults. fw login lets you pick a blueprint, download the project ready-made, or create it on GitHub.")
			return name, nil
		}
		return "", err
	}
	blueprints, err := client.Blueprints(cmd.Context())
	if err != nil {
		fmt.Fprintf(errOut, "Could not list blueprints (%v); using your defaults.\n", explain(err, client.URL))
		return name, nil
	}
	options := []string{"Your defaults on " + cloud.Host(client.URL)}
	var keys []string
	for _, b := range blueprints {
		if len(b.Languages) > 0 && !contains(b.Languages, o.language) {
			continue
		}
		label := b.Name
		if label == "" {
			label = b.Key
		}
		if b.Description != "" {
			label += ": " + firstSentence(b.Description)
		}
		options = append(options, label)
		keys = append(keys, b.Key)
	}
	if len(keys) == 0 {
		return name, nil
	}
	i, err := a.choose(cmd, "Which blueprint?", options, 0)
	if err != nil {
		return "", err
	}
	if i > 0 {
		if err := cmd.Flags().Set("blueprint", keys[i-1]); err != nil {
			return "", err
		}
	}
	return name, nil
}

// destination returns where the project goes: from the flags, or, at a
// terminal and logged in to a server that offers more, from asking.
// Building it here stays the default.
func (a *app) destination(cmd *cobra.Command, o *newOptions, name string) (string, error) {
	if d := o.dest.flag(); d != "" {
		return d, nil
	}
	if !a.interactive || o.from != "" || o.pullToken || o.headless || o.print || o.offline || o.pieces.cloud || o.pieces.noCloud || o.kind != "" {
		return destLocal, nil
	}
	client, err := a.userClient(o.url)
	if err != nil {
		return destLocal, nil
	}
	d, err := client.Destinations(cmd.Context())
	if err != nil {
		// Older servers have no downloads; anything else shows up later
		return destLocal, nil
	}
	ghReason := d.GitHub.Unavailable[o.language]
	if !d.Download.Available && (!d.GitHub.Available || ghReason != "") {
		return destLocal, nil
	}

	host := cloud.Host(client.URL)
	here := map[string]string{"r": "the framework R package", "python": "framework-py", "stata": "fw"}[o.language]
	options := []string{fmt.Sprintf("Build it here with %s", here)}
	dests := []string{destLocal}
	if d.Download.Available {
		options = append(options, fmt.Sprintf("Download it ready-made from %s (nothing to install, no Git)", host))
		dests = append(dests, destZip)
	}
	if d.GitHub.Available && ghReason == "" {
		options = append(options, "Create it on GitHub, then clone it here")
		dests = append(dests, destGitHub)
	}
	i, err := a.choose(cmd, "How do you want the project?", options, 0)
	if err != nil {
		return "", err
	}
	if dests[i] != destGitHub {
		return dests[i], nil
	}

	// GitHub: who owns the repository, its name, and whether it's private
	switch {
	case d.GitHub.Login == "":
		return "", fmt.Errorf("connect GitHub first at %s, then run fw new again", d.GitHub.ConnectURL)
	case d.GitHub.Owners == nil:
		return "", fmt.Errorf("%s could not reach GitHub; try again in a minute", host)
	case len(d.GitHub.Owners) == 0:
		return "", fmt.Errorf("install the Framework app on your GitHub account or organization at %s, then run fw new again", d.GitHub.InstallURL)
	}
	owner := d.GitHub.Owners[0].Login
	if len(d.GitHub.Owners) > 1 {
		names := make([]string, len(d.GitHub.Owners))
		for i, ow := range d.GitHub.Owners {
			names[i] = ow.Login
		}
		j, err := a.choose(cmd, "Which GitHub account owns the repository?", names, 0)
		if err != nil {
			return "", err
		}
		owner = names[j]
	}
	repo, err := a.askLine(cmd, "Repository name", slugify(name))
	if err != nil {
		return "", err
	}
	private, err := a.askYesDefault(cmd, "Make the repository private?", true)
	if err != nil {
		return "", err
	}
	o.dest.github = owner + "/" + repo
	o.dest.public = !private
	return destGitHub, nil
}

// newOnCloud creates the project on Framework Cloud and then downloads it
// or has it scaffolded on GitHub and clones it.
func (a *app) newOnCloud(cmd *cobra.Command, o newOptions, name, dest string) error {
	out, errOut := cmd.OutOrStdout(), cmd.ErrOrStderr()
	client, err := a.userClient(o.url)
	if err != nil {
		var notLoggedIn *errNotLoggedIn
		if errors.As(err, &notLoggedIn) {
			return &errNotLoggedIn{"--zip and --github create the project on Framework Cloud, which needs a login. " + err.Error()}
		}
		return err
	}
	host := cloud.Host(client.URL)

	dir, err := targetDir(a, o.dir, name)
	if err != nil {
		return err
	}
	cloneInto := dest == destZip || (dest == destGitHub && !o.dest.noClone)
	if cloneInto {
		if err := checkEmptyTarget(dir); err != nil {
			return err
		}
	}

	choices := o.pieces.choices(cmd, name, o.language)
	choices.Workflow = o.workflow
	if dest == destGitHub {
		owner, repo, _ := strings.Cut(o.dest.github, "/")
		choices.GitHub = &cloud.NewRepository{Owner: owner, Name: repo, Private: !o.dest.public}
	}

	created, err := client.CreateProject(cmd.Context(), choices)
	if err != nil {
		return explain(err, client.URL)
	}
	fmt.Fprintf(errOut, "Created %s on %s: %s\n", created.Slug, host, created.URL)
	link := &cloudLink{token: created.Token, url: client.URL}

	if dest == destZip {
		fmt.Fprintf(errOut, "Building it on %s…\n", host)
		data, err := client.DownloadProject(cmd.Context(), created.Slug)
		if err != nil {
			return fmt.Errorf("created %s, but downloading it failed: %w. Try again with: fw new --from %s", created.Slug, explain(err, client.URL), created.Slug)
		}
		if err := unzipProject(data, dir); err != nil {
			return fmt.Errorf("created %s, but unpacking the download failed: %w", created.Slug, err)
		}
		if err := linkProject(cmd, link, dir); err != nil {
			return fmt.Errorf("the project was downloaded, but linking it to the cloud failed: %w", err)
		}
		fmt.Fprintf(out, "Downloaded %s into %s.\n", created.Name, dir)
		printOpenHint(out, created.Language, dir)
		return nil
	}

	repo := ""
	if created.GitHub != nil {
		repo = created.GitHub.Repository
	}
	fmt.Fprintf(errOut, "Setting it up on GitHub as %s. This usually takes a minute or two.\n", repo)
	detail, err := a.waitForScaffold(cmd, client, created.Slug)
	if err != nil {
		return err
	}
	cloneURL := detail.GitHub.URL + ".git"
	fmt.Fprintf(out, "%s is ready on GitHub: %s\n", created.Name, detail.GitHub.URL)

	lookPath := a.engines.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	git, lookErr := lookPath("git")
	switch {
	case o.dest.noClone:
		fmt.Fprintf(out, "Clone it with: git clone %s\n", cloneURL)
		return nil
	case lookErr != nil:
		fmt.Fprintf(out, "Git isn't installed here. In VS Code or Positron, choose Clone Repository and paste %s\n", cloneURL)
		return nil
	}
	code, err := a.run(engine.Command{Program: git, Args: []string{"clone", cloneURL, dir}, Dir: a.cwd})
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("git clone exited with status %d. The repository is at %s", code, detail.GitHub.URL)
	}
	if err := linkProject(cmd, link, dir); err != nil {
		return fmt.Errorf("the project was cloned, but linking it to the cloud failed: %w", err)
	}
	printOpenHint(out, created.Language, dir)
	return nil
}

// waitForScaffold polls the project until GitHub finishes scaffolding it.
func (a *app) waitForScaffold(cmd *cobra.Command, client *cloud.Client, slug string) (cloud.ProjectDetail, error) {
	const every, limit = 5 * time.Second, 15 * time.Minute
	last := ""
	for waited := time.Duration(0); waited <= limit; waited += every {
		detail, err := client.Project(cmd.Context(), slug)
		if err != nil {
			return detail, explain(err, client.URL)
		}
		if gh := detail.GitHub; gh != nil {
			switch gh.ScaffoldStatus {
			case "done":
				return detail, nil
			case "failed":
				return detail, fmt.Errorf("GitHub could not set the project up: %s. The project page has a Retry button: %s/projects/%s/github", gh.ScaffoldError, client.URL, slug)
			}
			if gh.ScaffoldStatus != last && gh.ScaffoldStatus == "running" {
				fmt.Fprintln(cmd.ErrOrStderr(), "GitHub is building the project…")
			}
			last = gh.ScaffoldStatus
		}
		if err := a.sleep(cmd.Context(), every); err != nil {
			return detail, err
		}
	}
	return cloud.ProjectDetail{}, fmt.Errorf("GitHub is still setting the project up. Check %s/projects/%s/github, then clone it from there", client.URL, slug)
}

func printOpenHint(out io.Writer, language, dir string) {
	switch language {
	case "stata":
		fmt.Fprintf(out, "Next: open Stata in %s and run: do master.do\n", dir)
	case "python":
		fmt.Fprintf(out, "Next: open %s in Positron or VS Code.\n", dir)
	default:
		fmt.Fprintf(out, "Next: open %s in RStudio, Positron, or VS Code.\n", dir)
	}
}

// unzipProject extracts a project zip into dir. The zip holds the project
// under one top folder, which is replaced by dir. Paths that would leave
// dir, and links, are refused.
func unzipProject(data []byte, dir string) error {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return fmt.Errorf("the download is not a zip: %w", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil { // #nosec G301 -- a project folder, readable like any other
		return err
	}
	for _, f := range r.File {
		raw := strings.ReplaceAll(f.Name, `\`, "/")
		if path.IsAbs(raw) || contains(strings.Split(raw, "/"), "..") {
			return fmt.Errorf("unsafe path in the download: %s", f.Name)
		}
		_, rest, _ := strings.Cut(path.Clean(raw), "/")
		if rest == "" || rest == "." {
			continue
		}
		target := filepath.Join(dir, filepath.FromSlash(rest))
		mode := f.Mode()
		switch {
		case mode.IsDir():
			if err := os.MkdirAll(target, 0o755); err != nil { // #nosec G301 -- project folders, as Stata scaffolding makes them
				return err
			}
			continue
		case !mode.IsRegular():
			return fmt.Errorf("unexpected file type in the download: %s", f.Name)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil { // #nosec G301 -- project folders, as Stata scaffolding makes them
			return err
		}
		perm := os.FileMode(0o644)
		if mode.Perm()&0o100 != 0 {
			perm = 0o755
		}
		if err := writeZipFile(f, target, perm); err != nil {
			return err
		}
	}
	return nil
}

func writeZipFile(f *zip.File, target string, perm os.FileMode) error {
	src, err := f.Open()
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm) // #nosec G304 -- target is checked to stay inside dir
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, io.LimitReader(src, 256<<20)); err != nil {
		_ = dst.Close()
		return err
	}
	return dst.Close()
}

// firstSentence shortens a description for a menu line.
func firstSentence(s string) string {
	if i := strings.Index(s, ". "); i > 0 {
		s = s[:i]
	}
	if r := []rune(s); len(r) > 90 {
		s = strings.TrimSpace(string(r[:87])) + "..."
	}
	return strings.TrimSuffix(s, ".")
}
