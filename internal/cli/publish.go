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
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/frameworktools/framework-cli/internal/cloud"
	"github.com/frameworktools/framework-cli/internal/project"
)

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
		return nil, "", &errNotLoggedIn{"this project has no FW_PROJECT_TOKEN in .env. Run: fw project link --from SLUG"}
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
	fmt.Fprintf(cmd.ErrOrStderr(), "%s [y/N] ", question)
	answer, err := a.readLine()
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if !strings.HasPrefix(strings.ToLower(answer), "y") {
		return errors.New("canceled; nothing was uploaded")
	}
	return nil
}

// privatePart returns the first path, relative to root, that lies in a
// folder named private, as R's publish() checks. Paths outside root are not
// checked.
func privatePart(root string, paths ...string) string {
	for _, p := range paths {
		rel, err := filepath.Rel(root, p)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
			if part == "private" {
				return filepath.ToSlash(rel)
			}
		}
	}
	return ""
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
	if ext != ".html" && ext != ".htm" {
		return cloud.Upload{}, nil, fmt.Errorf("fw publishes HTML: a rendered Quarto document or another .html file. Render %s first", filepath.Base(path))
	}
	if entrypoint != "" {
		return cloud.Upload{}, nil, usageError("--entrypoint is for publishing a folder")
	}
	dir, name := filepath.Split(path)
	data, err := os.ReadFile(path)
	if err != nil {
		return cloud.Upload{}, nil, err
	}
	if resources := resourceDirs(dir, data); len(resources) > 0 {
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
	var urlFlag, slug, title, entrypoint string
	var readable, allowPrivate, yes bool
	cmd := &cobra.Command{
		Use:   "publish PATH",
		Short: "Publish a rendered notebook or HTML file to Framework Cloud",
		Long: "Uploads a rendered Quarto document or another HTML file to the current\n" +
			"project on Framework Cloud, as R's publish() does. It uses the project\n" +
			"token in .env, so run fw project link first.\n\n" +
			"PATH is an .html file or a folder of rendered output. When analysis.html\n" +
			"has an analysis_files/ folder next to it, both are sent. A folder is sent\n" +
			"whole, opening at --entrypoint (default index.html).\n\n" +
			"Every document gets a private link. --readable also gives it a readable\n" +
			"address under your handle, and --readable=false takes that away.\n" +
			"Publishing to the same slug again adds a new version.\n\n" +
			"fw sends only what PATH names. It refuses anything inside a folder named\n" +
			"private in the project unless you pass --allow-private. It shows what it\n" +
			"will send and asks first; --yes skips the question.",
		Example: "  fw publish notebooks/analysis.html\n" +
			"  fw publish outputs/reports/summary.html --slug summary --readable --yes\n" +
			"  fw publish _site --entrypoint index.html --json --yes",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, root, err := a.projectClient(urlFlag)
			if err != nil {
				return err
			}
			path := args[0]
			if !filepath.IsAbs(path) {
				path = filepath.Join(a.cwd, path)
			}
			upload, files, err := planUpload(path, entrypoint)
			if err != nil {
				return err
			}
			if p := privatePart(root, files...); p != "" && !allowPrivate {
				return fmt.Errorf("refusing to publish %s: it is inside a private folder, which holds restricted data. Pass --allow-private if you really mean to publish it", p)
			}
			if len(upload.Data) > maxUpload {
				return fmt.Errorf("%s is %s, larger than the server's 50 MB limit", upload.Name, sizeText(len(upload.Data)))
			}
			upload.Slug, upload.Title = slug, title
			if cmd.Flags().Changed("readable") {
				upload.Readable = &readable
			}

			errOut := cmd.ErrOrStderr()
			what := filepath.Base(path)
			if upload.Bundle {
				what = fmt.Sprintf("%s (%d files, opening at %s)", what, len(files), upload.Entrypoint)
			}
			fmt.Fprintf(errOut, "Publishing %s, %s, to this project on %s.\n", what, sizeText(len(upload.Data)), cloud.Host(client.URL))
			if err := a.confirm(cmd, yes, "Upload it?"); err != nil {
				return err
			}

			doc, err := client.Publish(cmd.Context(), upload)
			if cloud.StatusOf(err) == http.StatusPaymentRequired {
				return &exitError{code: exitFailure, err: cloud.ErrPaymentRequired(cloud.Host(client.URL))}
			}
			if err != nil {
				return explainProject(err)
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
	cmd.Flags().StringVar(&title, "title", "", "the document's title")
	cmd.Flags().BoolVar(&readable, "readable", false, "give the document a readable address; --readable=false removes it")
	cmd.Flags().StringVar(&entrypoint, "entrypoint", "", "with a folder: the HTML file to open (default index.html)")
	cmd.Flags().BoolVar(&allowPrivate, "allow-private", false, "publish even from a folder named private")
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
	cmd := &cobra.Command{
		Use:   "push-spec",
		Short: "Send this project's spec to Framework Cloud",
		Long: "Sends .framework/spec.json to the project on Framework Cloud, which shows\n" +
			"it on the project page. The spec describes the project's structure:\n" +
			"folders, settings, packages, and templates. It never holds data, and the\n" +
			"server refuses secret values in it. It uses the project token in .env.",
		Example: "  fw project push-spec\n  fw project push-spec --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, root, err := a.projectClient(urlFlag)
			if err != nil {
				return err
			}
			data, err := os.ReadFile(filepath.Join(root, ".framework", "spec.json"))
			if errors.Is(err, fs.ErrNotExist) {
				return errors.New("this project has no .framework/spec.json. Projects made by older versions of R and Python don't record one")
			}
			if err != nil {
				return err
			}
			var doc map[string]json.RawMessage
			if err := json.Unmarshal(data, &doc); err != nil {
				return fmt.Errorf(".framework/spec.json is not valid JSON: %w", err)
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

			res, err := client.PushSpec(cmd.Context(), data)
			if err != nil {
				return explainProject(err)
			}
			if a.jsonOut {
				return writeJSON(cmd.OutOrStdout(), res)
			}
			if res.Changed {
				fmt.Fprintf(cmd.OutOrStdout(), "Sent. The cloud recorded spec version %d at %s.\n", res.SpecVersion, res.ReceivedAt)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "No change: the cloud already has this spec (received %s).\n", res.ReceivedAt)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&urlFlag, "url", "", "Framework Cloud server (default: the project's FW_CLOUD_URL)")
	cmd.Flags().BoolVar(&a.jsonOut, "json", false, jsonHelp)
	return cmd
}
