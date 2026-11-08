package cli

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/frameworktools/framework-cli/internal/cloud"
	"github.com/frameworktools/framework-cli/internal/project"
	pub "github.com/frameworktools/framework-cli/internal/publish"
)

// artifactKinds are the kinds Framework Cloud accepts. file comes last.
var artifactKinds = []string{"html", "image", "chart", "table", "data", "model", "test", "text", "pdf", "file"}

// maxUpload is the server's limit for one document.
const maxUpload = 50 << 20

// projectClient returns a client for the current project's server with its
// project token from .env.
func (a *app) projectClient(flag string) (*cloud.Client, string, error) {
	root, err := a.requireProject()
	if err != nil {
		return nil, "", err
	}
	token := project.EnvValue(root, "FW_PROJECT_TOKEN")
	if token == "" {
		return nil, "", &errNotLoggedIn{"this project has no FW_PROJECT_TOKEN in .env. Run fw project link --create to create it on Framework Cloud, or fw project link --from SLUG to link it to a project there"}
	}
	server, err := a.server(flag, cloud.ProjectScope)
	if err != nil {
		return nil, "", err
	}
	return cloud.NewClient(server.URL, token), root, nil
}

// confirm asks before an upload. Without a terminal, --yes is required.
func (a *app) confirm(cmd *cobra.Command, yes bool, question string) error {
	if yes {
		return nil
	}
	if !a.interactive {
		return usageError("no terminal to confirm from. After reading the summary above, rerun with --yes")
	}
	return a.ask(cmd, question)
}

// ask asks a yes or no question at the terminal, defaulting to No.
func (a *app) ask(cmd *cobra.Command, question string) error {
	yes, err := a.askYes(cmd, question)
	if err != nil {
		return err
	}
	if !yes {
		return errors.New("canceled; nothing was uploaded")
	}
	return nil
}

// askYes asks a yes-or-no question that defaults to No.
func (a *app) askYes(cmd *cobra.Command, question string) (bool, error) {
	fmt.Fprintf(cmd.ErrOrStderr(), "%s [y/N] ", question)
	answer, err := a.readLine()
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	return strings.HasPrefix(strings.ToLower(answer), "y"), nil
}

// privatePaths returns the paths whose full path contains "private" in any
// letter case. Such folders usually hold restricted data, so fw warns before
// uploading from them. A leading /private/ is ignored: macOS adds it when it
// resolves /tmp, /var, and /etc. R and Python check the same way.
func privatePaths(paths ...string) []string {
	var found []string
	for _, p := range paths {
		if isPrivatePath(p) {
			found = append(found, p)
		}
	}
	return found
}

func isPrivatePath(p string) bool {
	p = filepath.ToSlash(p)
	if rest, ok := strings.CutPrefix(p, "/private/"); ok {
		p = rest
	}
	return strings.Contains(strings.ToLower(p), "private")
}

// checkPrivate warns when an upload includes a path with "private" in it.
// At a terminal it asks, defaulting to No, and reports that it asked so the
// caller does not ask again. With --yes, with --json, or without a terminal,
// the upload needs --include-private.
func (a *app) checkPrivate(cmd *cobra.Command, root string, paths []string, include, yes bool) (asked bool, err error) {
	found := privatePaths(paths...)
	if len(found) == 0 {
		return false, nil
	}
	shown := make([]string, 0, 5)
	for _, p := range found[:min(len(found), 5)] {
		// Show the path inside the project, unless only a folder above the
		// project matches; then the full path shows where "private" is.
		if rel, err := filepath.Rel(root, p); err == nil && !strings.HasPrefix(rel, "..") && isPrivatePath(rel) {
			p = rel
		}
		shown = append(shown, filepath.ToSlash(p))
	}
	list := strings.Join(shown, ", ")
	if len(found) > len(shown) {
		list += fmt.Sprintf(" ... and %d more", len(found)-len(shown))
	}
	what := fmt.Sprintf("%d files to upload have \"private\" in their path: %s", len(found), list)
	if len(found) == 1 {
		what = "a file to upload has \"private\" in its path: " + list
	}
	if include {
		fmt.Fprintf(cmd.ErrOrStderr(), "Including them as --include-private asks: %s.\n", what)
		return false, nil
	}
	if !a.interactive || yes || a.jsonOut {
		return false, &exitError{code: exitPrivate, reason: "private_path", err: fmt.Errorf(
			"%s. Folders named private usually hold restricted data. Rerun with --include-private if you mean to upload them", what)}
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "Warning: %s. Folders named private usually hold restricted data.\n", what)
	return true, a.ask(cmd, "Upload them anyway?")
}

// bundleFiles lists the regular files under dir, relative to it, in order.
// Symbolic links are refused so a bundle cannot pull in files from elsewhere.
func bundleFiles(dir string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("refusing to publish %s: it is a symbolic link", path)
		}
		if d.Type().IsRegular() {
			rel, err := filepath.Rel(dir, path)
			if err != nil {
				return err
			}
			files = append(files, rel)
		}
		return nil
	})
	sort.Strings(files)
	return files, err
}

// zipFiles zips base/rel for each rel, with forward-slash names.
func zipFiles(base string, rels []string) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, rel := range rels {
		data, err := os.ReadFile(filepath.Join(base, rel))
		if err != nil {
			return nil, err
		}
		w, err := zw.Create(filepath.ToSlash(rel))
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(data); err != nil {
			return nil, err
		}
		if buf.Len() > maxUpload {
			return nil, fmt.Errorf("the bundle is larger than the server's 50 MB limit")
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// planUpload decides what to send for path: one HTML file, an HTML file with
// its Quarto <name>_files folder, or a rendered folder with an entry point.
// It returns the upload and the files it holds, as absolute paths.
func planUpload(path, entrypoint string) (cloud.Upload, []string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return cloud.Upload{}, nil, err
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return cloud.Upload{}, nil, fmt.Errorf("refusing to publish %s: it is a symbolic link", path)
	}

	if info.IsDir() {
		if entrypoint == "" {
			entrypoint = "index.html"
		}
		clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(entrypoint)))
		if filepath.IsAbs(entrypoint) || clean == ".." || strings.HasPrefix(clean, "../") {
			return cloud.Upload{}, nil, usageError("--entrypoint must be a path inside the folder")
		}
		rels, err := bundleFiles(path)
		if err != nil {
			return cloud.Upload{}, nil, err
		}
		found := false
		abs := make([]string, len(rels))
		for i, rel := range rels {
			abs[i] = filepath.Join(path, rel)
			found = found || filepath.ToSlash(rel) == clean
		}
		if !found {
			return cloud.Upload{}, nil, fmt.Errorf("%s has no %s. Name the HTML file to open with --entrypoint", path, clean)
		}
		data, err := zipFiles(path, rels)
		if err != nil {
			return cloud.Upload{}, nil, err
		}
		return cloud.Upload{Name: filepath.Base(path) + ".zip", Data: data, Bundle: true, Entrypoint: clean}, abs, nil
	}

	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".qmd" || ext == ".ipynb" || ext == ".rmd" {
		return cloud.Upload{}, nil, fmt.Errorf("render %s to HTML before publishing", filepath.Base(path))
	}
	if entrypoint != "" {
		return cloud.Upload{}, nil, usageError("--entrypoint is for publishing a folder")
	}
	dir, name := filepath.Split(path)
	data, err := os.ReadFile(path)
	if err != nil {
		return cloud.Upload{}, nil, err
	}
	if resources := resourceDirs(dir, data); (ext == ".html" || ext == ".htm") && len(resources) > 0 {
		// Quarto without embed-resources writes analysis.html next to
		// analysis_files/. Send both, so the page keeps its styles and figures.
		all := []string{name}
		abs := []string{path}
		for _, res := range resources {
			rels, err := bundleFiles(filepath.Join(dir, res))
			if err != nil {
				return cloud.Upload{}, nil, err
			}
			for _, rel := range rels {
				all = append(all, filepath.Join(res, rel))
				abs = append(abs, filepath.Join(dir, res, rel))
			}
		}
		zipped, err := zipFiles(dir, all)
		if err != nil {
			return cloud.Upload{}, nil, err
		}
		return cloud.Upload{Name: strings.TrimSuffix(name, filepath.Ext(name)) + ".zip", Data: zipped, Bundle: true, Entrypoint: name}, abs, nil
	}
	return cloud.Upload{Name: name, Data: data}, []string{path}, nil
}

// resourceDirs lists the folders next to an HTML file whose names end in
// _files and that the HTML refers to. Quarto names the folder after the
// source document, which can differ from the HTML file's name.
func resourceDirs(dir string, html []byte) []string {
	entries, err := os.ReadDir(filepath.Join(dir, "."))
	if err != nil {
		return nil
	}
	var found []string
	for _, e := range entries {
		if e.IsDir() && strings.HasSuffix(e.Name(), "_files") && bytes.Contains(html, []byte(e.Name()+"/")) {
			found = append(found, e.Name())
		}
	}
	return found
}

func sizeText(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d bytes", n)
}

func newPublishCmd(a *app) *cobra.Command {
	var urlFlag, slug, title, entrypoint, to, description, kind, password, visibility, collection string
	var readable, includePrivate, yes, shareUnlisted bool
	cmd := &cobra.Command{
		Use:   "publish PATH",
		Short: "Publish an artifact to a configured destination",
		Long: "Publishes a file, rendered HTML document, or rendered folder. Framework builds\n" +
			"an artifact bundle with checksums before delivery. --to selects a named\n" +
			"destination in settings.yml, then publish.default, then Framework Cloud.\n\n" +
			"Cloud uses the project token in .env. GitHub writes published/ in the\n" +
			"project repository, commits the artifact and site index, and pushes.\n" +
			"Folder and S3 destinations preserve version archives and an offline reader.\n" +
			"S3 requires the AWS CLI and confirmed bucket access policy.\n\n" +
			"GitHub Pages sites are public even from private repositories. Static\n" +
			"destinations reject passwords and unlisted links. Folder visibility must\n" +
			"be declared in its destination settings.\n\n" +
			"Rendered folders open at --entrypoint (default index.html). Referenced\n" +
			"Quarto _files folders accompany HTML files. Symbolic links are refused.\n\n" +
			"fw shows a summary and asks first; --yes skips the question. Files with\n" +
			"private in their paths require --include-private without a terminal.",
		Example: "  fw publish notebooks/analysis.html\n" +
			"  fw publish outputs/reports/summary.html --slug summary --readable --yes\n" +
			"  fw publish _site --entrypoint index.html --json --yes\n" +
			"  fw publish outputs/notebooks/results.html --to github --slug results",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if shareUnlisted && collection == "" {
				return usageError("--share-unlisted requires --collection")
			}
			if kind != "" && !slices.Contains(artifactKinds, kind) {
				return usageError("--kind takes " + strings.Join(artifactKinds[:len(artifactKinds)-1], ", ") + ", or file")
			}
			path := args[0]
			if !filepath.IsAbs(path) {
				path = filepath.Join(a.cwd, path)
			}
			root, err := a.requireProject()
			if err != nil {
				return err
			}
			destination, err := resolvePublish(root, to)
			if err != nil {
				return err
			}
			opts := staticPublishOptions{path: path, slug: slug, title: title, description: description, kind: kind, entrypoint: entrypoint, urlFlag: urlFlag, visibility: visibility, password: password, includePrivate: includePrivate, yes: yes}
			if destination.Driver != "cloud" {
				for _, name := range []string{"collection", "share-unlisted", "readable"} {
					if cmd.Flags().Changed(name) {
						return usageError("--" + name + " requires Framework Cloud")
					}
				}
				if destination.Driver == "github" {
					if destination.Expiry != "" {
						return usageError("GitHub Pages does not support expiry")
					}
					if destination.Password != "" || cmd.Flags().Changed("password") || password != "" {
						return usageError("GitHub Pages does not support passwords")
					}
					vis := visibility
					if vis == "" {
						vis = destination.Visibility
					}
					if vis != "" && vis != "public" {
						return usageError("private GitHub Pages access has not been confirmed; publishing stopped")
					}
					return a.publishToGitHub(cmd, pagesOptions{path: path, slug: slug, urlFlag: urlFlag, includePrivate: includePrivate, yes: yes, artifact: &opts})
				}
				return a.publishStatic(cmd, destination, opts)
			}
			if visibility == "" {
				visibility = destination.Visibility
			}
			if password == "" && !cmd.Flags().Changed("password") {
				password, err = resolveEnvOption(destination.Password)
				if err != nil {
					return err
				}
			}
			if destination.Expiry != "" {
				return usageError("artifact expiry is not supported by this command")
			}
			client, root, err := a.projectClient(urlFlag)
			if err != nil {
				return err
			}
			var selected *cloud.Collection
			if collection != "" {
				rows, err := client.Collections(cmd.Context())
				if err != nil {
					return explainProject(err)
				}
				for _, row := range rows {
					if row.Slug == collection || strconv.Itoa(row.ID) == collection {
						if selected != nil {
							return usageError("collection is ambiguous in this project")
						}
						matched := row
						selected = &matched
					}
				}
				if selected == nil {
					return usageError("collection was not found in this project")
				}
				// As on the server: an explicit --visibility wins, and
				// --readable alone switches between public and unlisted.
				known, public := visibility != "", visibility == "public"
				if visibility == "" && cmd.Flags().Changed("readable") {
					known, public = true, readable
				}
				// Ask before uploading: the server would take the upload, then
				// refuse to attach a non-public artifact.
				if selected.Visibility == "public" && !public {
					return usageError("public collections require public artifacts; pass --visibility public")
				}
				if selected.Visibility == "unlisted" && known && !public && !shareUnlisted {
					return usageError("pass --share-unlisted to share an unlisted artifact with collection readers")
				}
			}
			upload, files, err := planUpload(path, entrypoint)
			if err != nil {
				return err
			}
			if len(upload.Data) > maxUpload {
				return fmt.Errorf("%s is %s, larger than the server's 50 MB limit", upload.Name, sizeText(len(upload.Data)))
			}
			upload.Slug, upload.Title = slug, title
			if upload.Slug == "" {
				base := filepath.Base(filepath.Clean(path))
				if info, e := os.Stat(path); e == nil && !info.IsDir() {
					base = strings.TrimSuffix(base, filepath.Ext(base))
				}
				upload.Slug = pub.Slug(base)
			}
			if upload.Slug == "" || pub.Slug(upload.Slug) != upload.Slug {
				return usageError("--slug must use lowercase letters, digits, and single hyphens")
			}

			// Every destination builds the same offline bundle. Cloud keeps its
			// existing multipart wire format for older-server compatibility.
			opts.slug = upload.Slug
			commonBundle, buildErr := buildStatic(opts)
			if buildErr != nil {
				return buildErr
			}
			defer os.RemoveAll(commonBundle.Dir)
			upload.PasswordSet, upload.DescriptionSet = cmd.Flags().Changed("password"), cmd.Flags().Changed("description")
			upload.Description, upload.Kind = description, kind
			upload.Password, upload.Visibility = password, visibility
			if cmd.Flags().Changed("readable") {
				upload.Readable = &readable
			}

			errOut := cmd.ErrOrStderr()
			what := filepath.Base(path)
			if upload.Bundle {
				what = fmt.Sprintf("%s (%d files, opening at %s)", what, len(files), upload.Entrypoint)
			}
			fmt.Fprintf(errOut, "Publishing %s, %s, to this project on %s.\n", what, sizeText(len(upload.Data)), cloud.Host(client.URL))
			asked, err := a.checkPrivate(cmd, root, files, includePrivate, yes)
			if err != nil {
				return err
			}
			if !asked {
				if err := a.confirm(cmd, yes, "Upload it?"); err != nil {
					return err
				}
			}

			doc, err := client.Publish(cmd.Context(), upload)
			if cloud.StatusOf(err) == http.StatusPaymentRequired {
				return &exitError{code: exitFailure, err: cloud.ErrPaymentRequired(cloud.Host(client.URL))}
			}
			if err != nil {
				return explainProject(err)
			}
			if selected != nil {
				attached := false
				doc.CollectionID, doc.CollectionAttached = selected.ID, &attached
				var attachErr error
				if doc.ID == 0 {
					attachErr = errors.New("the server did not return an artifact ID")
				} else {
					attachErr = client.AttachCollection(cmd.Context(), selected.ID, doc.ID, shareUnlisted)
				}
				if attachErr != nil {
					doc.CollectionWarning = fmt.Sprintf("Artifact published at %s, but collection attachment failed: %v. Attach it from the project Outputs page; publishing again creates another version.", doc.URL, attachErr)
					fmt.Fprintln(cmd.ErrOrStderr(), "Warning: "+doc.CollectionWarning)
				} else {
					attached = true
					fmt.Fprintln(cmd.ErrOrStderr(), "Added to collection: "+selected.Title)
				}
			}
			out := cmd.OutOrStdout()
			if a.jsonOut {
				return writeJSON(out, doc)
			}
			fmt.Fprintf(out, "Published %s, version %d.\n", doc.Slug, doc.Version)
			fmt.Fprintf(out, "Link: %s\n", doc.URL)
			if doc.ReadableURL == nil && doc.PrivateURL != "" && doc.PrivateURL != doc.URL {
				fmt.Fprintf(out, "Private link: %s\n", doc.PrivateURL)
			}
			if doc.VersionURL != "" {
				fmt.Fprintf(out, "This version: %s\n", doc.VersionURL)
			}
			if doc.Notice != nil && *doc.Notice != "" {
				fmt.Fprintf(out, "Note: %s\n", *doc.Notice)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&slug, "slug", "", "the document's name in the project (default: from the file name)")
	cmd.Flags().StringVar(&to, "to", "", "named publishing destination (default: publish.default, then cloud)")
	cmd.Flags().StringVar(&title, "title", "", "the document's title")
	cmd.Flags().StringVar(&collection, "collection", "", "existing project collection ID or slug")
	cmd.Flags().BoolVar(&shareUnlisted, "share-unlisted", false, "allow collection readers to access this unlisted artifact")
	cmd.Flags().StringVar(&description, "description", "", "the artifact description")
	cmd.Flags().StringVar(&kind, "kind", "", "artifact kind: "+strings.Join(artifactKinds[:len(artifactKinds)-1], ", ")+", or file (default detected)")
	cmd.Flags().StringVar(&password, "password", "", "password required to view the artifact")
	cmd.Flags().StringVar(&visibility, "visibility", "", "artifact visibility (Cloud: public, unlisted, or private)")
	cmd.Flags().BoolVar(&readable, "readable", false, "give the document a readable address; --readable=false removes it")
	cmd.Flags().StringVar(&entrypoint, "entrypoint", "", "with a folder: the HTML file to open (default index.html)")
	cmd.Flags().BoolVar(&includePrivate, "include-private", false, "upload files whose path contains \"private\" without asking")
	// 0.2.0 called it --allow-private.
	cmd.Flags().BoolVar(&includePrivate, "allow-private", false, "")
	_ = cmd.Flags().MarkHidden("allow-private")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "upload without asking")
	cmd.Flags().StringVar(&urlFlag, "url", "", "Framework Cloud server (default: the project's FW_CLOUD_URL)")
	cmd.Flags().BoolVar(&a.jsonOut, "json", false, jsonHelp)
	return cmd
}

// explainProject adds hints to errors from project-token calls.
func explainProject(err error) error {
	switch cloud.StatusOf(err) {
	case http.StatusUnauthorized:
		return fmt.Errorf("%w. The project token in .env is invalid or revoked. Run: fw project link --from SLUG", err)
	case http.StatusForbidden:
		return fmt.Errorf("%w. FW_PROJECT_TOKEN in .env must be a project token", err)
	}
	return err
}

func newPushSpecCmd(a *app) *cobra.Command {
	var urlFlag string
	var includePrivate bool
	cmd := &cobra.Command{
		Use:   "push-spec",
		Short: "Send this project's spec to Framework Cloud",
		Long: "Sends .framework/spec.json to the project on Framework Cloud, which shows\n" +
			"it on the project page. The spec describes the project's structure:\n" +
			"folders, settings, packages, and templates. It never holds data, and the\n" +
			"server refuses secret values in it. It uses the project token in .env.\n\n" +
			"fw first checks the spec against spec.v1, the schema the server checks it\n" +
			"against. It leaves out scaffold.positron and source.upgraded_from, which\n" +
			"an older upgrade() wrote and spec.v1 doesn't allow, and says so; the\n" +
			"file keeps them until fw project upgrade --apply rewrites it. Any\n" +
			"other problem stops fw before it sends anything, with the keys named,\n" +
			"including a connection credential that is written out: it must be\n" +
			"env(\"NAME\") or env(\"NAME\", \"\"), not a value or a non-empty default.\n" +
			"fw project upgrade --apply rewrites the spec (in R projects, with a\n" +
			"framework package newer than 2.0.1).\n\n" +
			"When the project's full path contains \"private\", fw warns and asks\n" +
			"first. With --json or no terminal, it stops with exit code 6 unless you\n" +
			"pass --include-private.",
		Example: "  fw project push-spec\n  fw project push-spec --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, root, err := a.projectClient(urlFlag)
			if err != nil {
				return err
			}
			specPath := filepath.Join(root, ".framework", "spec.json")
			data, err := os.ReadFile(specPath)
			if errors.Is(err, fs.ErrNotExist) {
				return errors.New("this project has no .framework/spec.json. Projects made by older versions of R, Python, and fw don't record one. Run fw project upgrade --apply to record it")
			}
			if err != nil {
				return err
			}
			var doc map[string]json.RawMessage
			if err := json.Unmarshal(data, &doc); err != nil {
				return fmt.Errorf(".framework/spec.json is not valid JSON: %w", err)
			}
			if data, err = checkSpecForCloud(cmd.ErrOrStderr(), data, "fw project push-spec"); err != nil {
				return err
			}
			sections := make([]string, 0, len(doc))
			for k := range doc {
				sections = append(sections, k)
			}
			sort.Strings(sections)
			sensitive := ""
			var security struct {
				Sensitive bool `json:"sensitive"`
			}
			if json.Unmarshal(doc["security"], &security) == nil && security.Sensitive {
				sensitive = " This is a sensitive project; the spec holds folder names and settings, not data."
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "Sending .framework/spec.json (%s, %d sections) to this project on %s.%s\n",
				sizeText(len(data)), len(sections), cloud.Host(client.URL), sensitive)
			if _, err := a.checkPrivate(cmd, root, []string{specPath}, includePrivate, false); err != nil {
				return err
			}

			res, err := client.PushSpec(cmd.Context(), data)
			if err != nil {
				return explainProject(err)
			}
			if a.jsonOut {
				return writeJSON(cmd.OutOrStdout(), res)
			}
			if res.Changed {
				fmt.Fprintf(cmd.OutOrStdout(), "Sent. The cloud recorded the new spec at %s.\n", res.ReceivedAt)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "No change: the cloud already has this spec (received %s).\n", res.ReceivedAt)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&includePrivate, "include-private", false, "send the spec without asking when the project's path contains \"private\"")
	cmd.Flags().StringVar(&urlFlag, "url", "", "Framework Cloud server (default: the project's FW_CLOUD_URL)")
	cmd.Flags().BoolVar(&a.jsonOut, "json", false, jsonHelp)
	return cmd
}
