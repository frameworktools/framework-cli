package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/frameworktools/framework-cli/internal/project"
	pub "github.com/frameworktools/framework-cli/internal/publish"
	"github.com/frameworktools/framework-cli/internal/spec"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

type publishDestination struct {
	Driver     string         `yaml:"driver" json:"driver"`
	Path       string         `yaml:"path" json:"path"`
	URL        string         `yaml:"url" json:"url"`
	Bucket     string         `yaml:"bucket" json:"bucket"`
	Prefix     string         `yaml:"prefix" json:"prefix"`
	Endpoint   string         `yaml:"endpoint" json:"endpoint"`
	Visibility string         `yaml:"visibility" json:"visibility"`
	Password   string         `yaml:"password" json:"password"`
	Expiry     string         `yaml:"expires_at" json:"expires_at"`
	Private    *bool          `yaml:"private" json:"private"`
	Options    map[string]any `yaml:"-" json:"-"`
}

func (d *publishDestination) UnmarshalYAML(node *yaml.Node) error {
	var raw map[string]any
	if e := node.Decode(&raw); e != nil {
		return e
	}
	if e := spec.ValidatePublishSecrets(raw); e != nil {
		return e
	}
	type plain publishDestination
	var decoded plain
	if e := node.Decode(&decoded); e != nil {
		return e
	}
	*d = publishDestination(decoded)
	d.Options = raw
	return d.normalize()
}
func (d *publishDestination) UnmarshalJSON(data []byte) error {
	var raw map[string]any
	if e := json.Unmarshal(data, &raw); e != nil {
		return e
	}
	if e := spec.ValidatePublishSecrets(raw); e != nil {
		return e
	}
	type plain publishDestination
	var decoded plain
	if e := json.Unmarshal(data, &decoded); e != nil {
		return e
	}
	*d = publishDestination(decoded)
	d.Options = raw
	return d.normalize()
}
func (d *publishDestination) normalize() error {
	if d.Visibility == "" && d.Private != nil {
		d.Visibility = "public"
		if *d.Private {
			d.Visibility = "private"
		}
	}
	if d.Visibility != "" && d.Visibility != "public" && d.Visibility != "private" {
		return usageError("destination visibility must be public or private")
	}
	return nil
}

// resolveEnvOption reads a setting written as env("VARIABLE") or
// env("VARIABLE", "default") from the environment. An unset variable takes
// the default when there is one, and is an error otherwise. Any other value
// is used as written.
func resolveEnvOption(value string) (string, error) {
	if name, def, hasDefault, ok := spec.EnvCall(value); ok {
		resolved, exists := os.LookupEnv(name)
		if !exists {
			if hasDefault {
				return def, nil
			}
			return "", usageError("publishing environment variable is not set: " + name)
		}
		return resolved, nil
	}
	return value, nil
}

type publishSettings struct {
	Default      string                        `yaml:"default" json:"default"`
	Destinations map[string]publishDestination `yaml:"destinations" json:"destinations"`
}

func resolvePublish(root, to string) (publishDestination, error) {
	var doc struct {
		Publish *publishSettings `yaml:"publish" json:"publish"`
		Default struct {
			Publish *publishSettings `yaml:"publish" json:"publish"`
		} `yaml:"default" json:"default"`
	}
	data, err := os.ReadFile(filepath.Join(root, "settings.yml"))
	if err == nil {
		if err = yaml.Unmarshal(data, &doc); err != nil {
			return publishDestination{}, fmt.Errorf("settings.yml: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return publishDestination{}, err
	}
	selected := doc.Publish
	if selected == nil {
		selected = doc.Default.Publish
	}
	if selected == nil { // Stata and older projects can retain settings in the resolved spec.
		data, e := os.ReadFile(filepath.Join(root, ".framework", "spec.json"))
		if e == nil {
			var recorded struct {
				Publish *publishSettings `json:"publish"`
			}
			if e := json.Unmarshal(data, &recorded); e != nil {
				return publishDestination{}, fmt.Errorf("recorded publishing settings: %w", e)
			}
			selected = recorded.Publish
		}
	}
	if selected == nil {
		selected = &publishSettings{}
	}
	if to == "" {
		to = selected.Default
	}
	if to == "" {
		to = "cloud"
	}
	if d, ok := selected.Destinations[to]; ok {
		return d, nil
	}
	switch to {
	case "cloud", "github":
		return publishDestination{Driver: to}, nil
	}
	return publishDestination{}, usageError("unknown publishing destination: " + to)
}

type staticPublishOptions struct {
	path, slug, title, description, kind, entrypoint, urlFlag, visibility, password string
	includePrivate, yes                                                             bool
}

func buildStatic(opts staticPublishOptions) (*pub.Bundle, error) {
	slug := opts.slug
	if slug == "" {
		base := filepath.Base(opts.path)
		if info, e := os.Stat(opts.path); e == nil && !info.IsDir() {
			base = strings.TrimSuffix(base, filepath.Ext(base))
		}
		slug = pub.Slug(base)
	}
	if slug == "" || pub.Slug(slug) != slug {
		return nil, usageError("--slug must use lowercase letters, digits, and single hyphens")
	}
	title := opts.title
	if title == "" {
		title = slug
	}
	files := map[string]string{}
	original := filepath.Base(opts.path)
	kind := opts.kind
	info, err := os.Lstat(opts.path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("source is a symbolic link")
	}
	if info.IsDir() {
		rels, e := bundleFiles(opts.path)
		if e != nil {
			return nil, e
		}
		entry := opts.entrypoint
		if entry == "" {
			entry = "index.html"
		}
		if filepath.IsAbs(entry) || strings.HasPrefix(filepath.Clean(entry), "..") {
			return nil, usageError("entrypoint must be inside the folder")
		}
		found := false
		for _, r := range rels {
			if r == "artifact.json" || r == "v" || strings.HasPrefix(filepath.ToSlash(r), "v/") {
				return nil, fmt.Errorf("input contains reserved bundle path %s", r)
			}
			files[filepath.Join(opts.path, r)] = filepath.ToSlash(r)
			found = found || filepath.ToSlash(r) == entry
		}
		if !found {
			return nil, fmt.Errorf("folder has no %s", entry)
		}
		if entry != "index.html" {
			// Preserve the selected file and its relative assets. The reader shell
			// opens it in an iframe, while index.html remains the bundle entry.
			if old, ok := files[filepath.Join(opts.path, "index.html")]; ok && old == "index.html" {
				files[filepath.Join(opts.path, "index.html")] = "original-index.html"
			}
		}
		original = entry
		if kind == "" {
			kind = "html"
		}
	} else {
		ext := strings.ToLower(filepath.Ext(opts.path))
		if ext == ".qmd" || ext == ".ipynb" || ext == ".rmd" {
			return nil, fmt.Errorf("render %s before publishing", original)
		}
		if opts.entrypoint != "" {
			return nil, usageError("--entrypoint is for publishing a folder")
		}
		if original == "artifact.json" || original == "representation.json" {
			original = "original-" + original
		}
		files[opts.path] = original
		if ext == ".html" || ext == ".htm" {
			files[opts.path] = "index.html"
			original = "index.html"
			data, e := os.ReadFile(opts.path)
			if e != nil {
				return nil, e
			}
			for _, res := range resourceDirs(filepath.Dir(opts.path), data) {
				rels, e := bundleFiles(filepath.Join(filepath.Dir(opts.path), res))
				if e != nil {
					return nil, e
				}
				for _, r := range rels {
					files[filepath.Join(filepath.Dir(opts.path), res, r)] = filepath.ToSlash(filepath.Join(res, r))
				}
			}
			if kind == "" {
				kind = "html"
			}
		} else if kind == "" {
			switch ext {
			case ".png", ".jpg", ".jpeg", ".svg", ".gif", ".webp":
				kind = "image"
			case ".pdf":
				kind = "pdf"
			default:
				kind = "file"
			}
			if ext == ".json" {
				data, _ := os.ReadFile(opts.path)
				var d struct {
					Schema string `json:"schema"`
					Kind   string `json:"kind"`
				}
				if json.Unmarshal(data, &d) == nil && d.Schema == "framework.artifact/v1" {
					kind = d.Kind
				}
			}
		}
	}
	if strings.Contains(original, ":") {
		original = "./" + original
	}
	return pub.Build(files, slug, title, opts.description, kind, original, "framework-cli "+version, staticReader)
}

func (a *app) publishStatic(cmd *cobra.Command, d publishDestination, o staticPublishOptions) error {
	visibility := o.visibility
	if visibility == "" {
		visibility = d.Visibility
	}
	if visibility == "" {
		visibility = "public"
	}
	if d.Expiry != "" {
		return usageError("this destination does not support expiry")
	}
	if d.Password != "" || o.password != "" || cmd.Flags().Changed("password") {
		return usageError("this destination does not support passwords")
	}
	if visibility != "public" && visibility != "private" {
		return usageError("this destination supports public or private visibility, not unlisted links")
	}
	if d.Driver == "github" && visibility != "public" {
		return usageError("private GitHub Pages access has not been confirmed; publishing stopped")
	}
	if d.Driver == "folder" && d.Visibility == "" {
		return usageError("this folder destination needs visibility: public or visibility: private in its settings")
	}
	if d.Driver == "folder" && visibility != d.Visibility {
		return usageError("folder visibility must match its configured visibility; Framework cannot verify share permissions")
	}
	if d.Driver == "folder" && d.Path == "" {
		return usageError("folder destination requires path")
	}
	if d.Driver == "s3" {
		if d.Bucket == "" {
			return usageError("s3 destination requires bucket")
		}
		if _, err := exec.LookPath("aws"); err != nil {
			return usageError("s3 publishing requires the AWS CLI and configured credentials")
		}
	}
	b, err := buildStatic(o)
	if err != nil {
		return err
	}
	defer os.RemoveAll(b.Dir)
	root, err := a.requireProject()
	if err != nil {
		return err
	}
	where := destinationPath(root, a.home, d.Path)
	if d.Driver == "s3" {
		where = "s3://" + d.Bucket + "/" + strings.Trim(d.Prefix, "/")
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "Publishing %s (%d files) as %s, %s, to %s.\n", filepath.Base(o.path), len(b.Manifest.Files), b.Manifest.Slug, visibility, strings.TrimSuffix(where, "/"))
	asked, err := a.checkPrivate(cmd, root, b.Sources, o.includePrivate, o.yes)
	if err != nil {
		return err
	}
	if !asked {
		if err = a.confirm(cmd, o.yes, "Publish it?"); err != nil {
			return err
		}
	}
	var receipt pub.Receipt
	switch d.Driver {
	case "folder":
		receipt, err = pub.Folder(b, destinationPath(root, a.home, d.Path), d.URL)
	case "s3":
		receipt, err = deliverS3(cmd, b, d, visibility)
	default:
		return usageError("unsupported driver: " + d.Driver)
	}
	if err != nil {
		return err
	}
	a.trackExternal(cmd, b, receipt, o.urlFlag, visibility)
	if a.jsonOut {
		return writeJSON(cmd.OutOrStdout(), receipt)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Published %s, version %d.\n", b.Manifest.Slug, receipt.Version)
	if receipt.URL != "" {
		fmt.Fprintln(cmd.OutOrStdout(), "Link: "+receipt.URL)
	} else {
		fmt.Fprintln(cmd.OutOrStdout(), "Location: "+receipt.Location)
	}
	return nil
}

// destinationPath resolves a folder destination's path. ~ is the home
// folder, and a relative path starts at the project root, wherever fw runs.
func destinationPath(root, home, p string) string {
	if p == "~" {
		return home
	}
	if rest, ok := strings.CutPrefix(filepath.ToSlash(p), "~/"); ok {
		return filepath.Join(home, filepath.FromSlash(rest))
	}
	if !filepath.IsAbs(p) {
		return filepath.Join(root, p)
	}
	return p
}

// awsVariables maps destination setting names to the AWS CLI's variables.
// It holds names only; the values come from env("VARIABLE") references.
var awsVariables = map[string]string{ // #nosec G101 -- variable names, not credentials
	"access_key": "AWS_ACCESS_KEY_ID", "access_key_id": "AWS_ACCESS_KEY_ID", "aws_access_key_id": "AWS_ACCESS_KEY_ID",
	"secret_key": "AWS_SECRET_ACCESS_KEY", "secret_access_key": "AWS_SECRET_ACCESS_KEY", "aws_secret_access_key": "AWS_SECRET_ACCESS_KEY",
	"session_token": "AWS_SESSION_TOKEN", "aws_session_token": "AWS_SESSION_TOKEN",
	"region": "AWS_DEFAULT_REGION", "profile": "AWS_PROFILE",
}

func awsEnvironment(d publishDestination) ([]string, error) {
	options := map[string]any{}
	for key, value := range d.Options {
		options[key] = value
	}
	if credentials, ok := d.Options["credentials"].(map[string]any); ok {
		for key, value := range credentials {
			options[key] = value
		}
	}
	env := os.Environ()
	for key, target := range awsVariables {
		if value, ok := options[key].(string); ok && value != "" {
			resolved, e := resolveEnvOption(value)
			if e != nil {
				return nil, e
			}
			env = append(env, target+"="+resolved)
		}
	}
	return env, nil
}

func deliverS3(cmd *cobra.Command, b *pub.Bundle, d publishDestination, visibility string) (pub.Receipt, error) {
	// Require the bucket policy to control public access. Never change an ACL
	// implicitly. Check the settings before calling AWS at all.
	if visibility == "public" && d.Visibility != "public" {
		return pub.Receipt{}, usageError("public s3 publishing requires visibility: public in the destination and a public bucket policy")
	}
	if visibility == "private" && d.Visibility != "private" {
		return pub.Receipt{}, usageError("private s3 publishing requires visibility: private and a private bucket policy")
	}
	awsEnv, envErr := awsEnvironment(d)
	if envErr != nil {
		return pub.Receipt{}, envErr
	}

	stage, err := os.MkdirTemp("", "framework-s3-")
	if err != nil {
		return pub.Receipt{}, err
	}
	defer os.RemoveAll(stage)
	prefix := strings.Trim(d.Prefix, "/")
	target := "s3://" + d.Bucket + "/"
	if prefix != "" {
		target += prefix + "/"
	}
	run := func(args ...string) error {
		if d.Endpoint != "" {
			args = append(args, "--endpoint-url", d.Endpoint)
		}
		c := exec.CommandContext(cmd.Context(), "aws", args...) // #nosec G204 -- fixed aws subcommands; the bucket comes from the project's settings
		c.Env = awsEnv
		c.Stderr = cmd.ErrOrStderr()
		if err := c.Run(); err != nil {
			return fmt.Errorf("AWS CLI delivery failed: %w", err)
		}
		return nil
	}
	// Fail closed when the service cannot confirm bucket access policy.
	policyArgs := []string{"s3api", "get-bucket-policy-status", "--bucket", d.Bucket, "--output", "json"}
	if d.Endpoint != "" {
		policyArgs = append(policyArgs, "--endpoint-url", d.Endpoint)
	}
	policyCmd := exec.CommandContext(cmd.Context(), "aws", policyArgs...) // #nosec G204 -- fixed aws subcommand
	policyCmd.Env = awsEnv
	var policyOut bytes.Buffer
	policyCmd.Stdout = &policyOut
	policyCmd.Stderr = cmd.ErrOrStderr()
	if e := policyCmd.Run(); e != nil {
		return pub.Receipt{}, fmt.Errorf("cannot confirm bucket visibility; publishing stopped: %w", e)
	}
	var policy struct {
		Status struct {
			Public *bool `json:"IsPublic"`
		} `json:"PolicyStatus"`
	}
	if json.Unmarshal(policyOut.Bytes(), &policy) != nil || policy.Status.Public == nil {
		return pub.Receipt{}, usageError("bucket visibility could not be confirmed")
	}
	if *policy.Status.Public != (visibility == "public") {
		return pub.Receipt{}, usageError("requested visibility differs from bucket policy")
	}
	if visibility == "private" {
		args := []string{"s3api", "get-public-access-block", "--bucket", d.Bucket, "--output", "json"}
		if d.Endpoint != "" {
			args = append(args, "--endpoint-url", d.Endpoint)
		}
		c := exec.CommandContext(cmd.Context(), "aws", args...) // #nosec G204 -- fixed aws subcommand
		c.Env = awsEnv
		var out bytes.Buffer
		c.Stdout = &out
		c.Stderr = cmd.ErrOrStderr()
		if e := c.Run(); e != nil {
			return pub.Receipt{}, fmt.Errorf("cannot confirm private bucket protections: %w", e)
		}
		var blocks struct {
			Config struct {
				BlockACL    bool `json:"BlockPublicAcls"`
				IgnoreACL   bool `json:"IgnorePublicAcls"`
				BlockPolicy bool `json:"BlockPublicPolicy"`
				Restrict    bool `json:"RestrictPublicBuckets"`
			} `json:"PublicAccessBlockConfiguration"`
		}
		if json.Unmarshal(out.Bytes(), &blocks) != nil || !blocks.Config.BlockACL || !blocks.Config.IgnoreACL || !blocks.Config.BlockPolicy || !blocks.Config.Restrict {
			return pub.Receipt{}, usageError("private delivery requires all S3 public access blocks")
		}
	}
	// Read the destination before choosing the next immutable archive number.
	if err = run("s3", "sync", target, stage, "--only-show-errors"); err != nil {
		return pub.Receipt{}, err
	}
	r, err := pub.Folder(b, stage, d.URL)
	if err != nil {
		return pub.Receipt{}, err
	}
	if err = run("s3", "sync", stage, target, "--only-show-errors"); err != nil {
		return pub.Receipt{}, err
	}
	r.Driver = "s3"
	r.Location = target + b.Manifest.Slug + "/"
	return r, nil
}
func (a *app) trackExternal(cmd *cobra.Command, b *pub.Bundle, r pub.Receipt, flag, visibility string) {
	root, err := a.requireProject()
	if err != nil || project.EnvValue(root, "FW_PROJECT_TOKEN") == "" {
		return
	}
	client, _, err := a.projectClient(flag)
	if err != nil {
		return
	}
	var me struct {
		Project string `json:"project"`
	}
	if err = client.Do(cmd.Context(), http.MethodGet, "/api/v1/me", nil, &me); err != nil || me.Project == "" {
		if err == nil {
			err = errors.New("FW_PROJECT_TOKEN in .env is not a project token")
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "Warning: Published successfully, but cloud tracking failed: %v\n", explainProject(err))
		return
	}
	data, err := os.ReadFile(filepath.Join(b.Dir, "artifact.json"))
	if err != nil {
		return
	}
	var size int64
	for _, f := range b.Manifest.Files {
		size += f.Bytes
	}
	payload := map[string]any{"slug": b.Manifest.Slug, "title": b.Manifest.Title, "kind": b.Manifest.Kind, "driver": r.Driver, "version": r.Version, "size_bytes": size, "sha256": pub.Hash(data), "visibility": visibility, "published_at": b.Manifest.CreatedAt}
	if r.URL != "" {
		payload["url"] = r.URL
	}
	if r.VersionURL != "" {
		payload["version_url"] = r.VersionURL
	}
	var out map[string]any
	if err = client.Do(cmd.Context(), http.MethodPost, "/api/v1/projects/"+url.PathEscape(me.Project)+"/artifacts/external", payload, &out); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "Warning: Published successfully, but cloud tracking failed: %v\n", err)
	}
}
