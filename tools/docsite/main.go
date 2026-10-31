// Command docsite builds docs-site.tar.gz for cli.framework.pub: docs/site
// plus a reference.json generated from fw's command tree. The release
// workflow runs it and attaches the archive to the GitHub release, and
// framework-cloud imports it. The format is inst/schema/docs-site.md in the
// framework R repository.
//
//	go run ./tools/docsite --version 0.2.0 --out build/docs-site.tar.gz
package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/frameworktools/framework-cli/internal/cli"
	"github.com/frameworktools/framework-cli/internal/spec"
)

type site struct {
	Product  string `yaml:"product"`
	Title    string `yaml:"title"`
	Sections []struct {
		ID        string `yaml:"id"`
		Reference bool   `yaml:"reference"`
	} `yaml:"sections"`
}

type frontMatter struct {
	ID          string `yaml:"id"`
	Title       string `yaml:"title"`
	Description string `yaml:"description"`
	Section     string `yaml:"section"`
	Position    *int   `yaml:"position"`
}

var imageExts = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".svg": true, ".webp": true, ".gif": true}

// checkSite reads docs/site and reports problems the cloud would reject.
// It returns the files to archive, relative to dir.
func checkSite(dir string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(dir, "site.yml"))
	if err != nil {
		return nil, err
	}
	var s site
	if err := yaml.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("site.yml: %w", err)
	}
	if s.Product != "cli" || s.Title == "" {
		return nil, errors.New("site.yml needs product: cli and a title")
	}
	sections := map[string]bool{}
	for _, sec := range s.Sections {
		sections[sec.ID] = !sec.Reference
	}

	files := []string{"site.yml"}
	var problems []string
	ids := map[string]string{}
	guides, err := filepath.Glob(filepath.Join(dir, "guides", "*.md"))
	if err != nil {
		return nil, err
	}
	sort.Strings(guides)
	for _, g := range guides {
		rel := "guides/" + filepath.Base(g)
		files = append(files, rel)
		text, err := os.ReadFile(g)
		if err != nil {
			return nil, err
		}
		fm, body, err := splitFrontMatter(string(text))
		if err != nil {
			problems = append(problems, rel+": "+err.Error())
			continue
		}
		switch {
		case fm.ID == "" || fm.Title == "" || fm.Description == "" || fm.Position == nil:
			problems = append(problems, rel+": front matter needs id, title, description, section, and position")
		case !sections[fm.Section]:
			problems = append(problems, rel+": section "+fm.Section+" is not a guides section in site.yml")
		case strings.Contains(fm.Description, "\n"):
			problems = append(problems, rel+": description must be one line")
		}
		if other, dup := ids[fm.ID]; dup {
			problems = append(problems, rel+": id "+fm.ID+" is also used by "+other)
		}
		ids[fm.ID] = rel
		inFence := false
		for _, line := range strings.Split(body, "\n") {
			if strings.HasPrefix(line, "```") {
				if !inFence && strings.TrimSpace(line) == "```" {
					problems = append(problems, rel+": every code fence names a language")
				}
				inFence = !inFence
				continue
			}
			if !inFence && strings.HasPrefix(line, "# ") {
				problems = append(problems, rel+": the body has no h1; the title comes from the front matter")
			}
		}
	}

	err = filepath.WalkDir(filepath.Join(dir, "images"), func(path string, d fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) {
			return fs.SkipDir
		}
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		if !imageExts[strings.ToLower(filepath.Ext(path))] {
			problems = append(problems, filepath.ToSlash(rel)+": images must be png, jpg, svg, webp, or gif")
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(problems) > 0 {
		return nil, errors.New(strings.Join(problems, "\n"))
	}
	return files, nil
}

func splitFrontMatter(text string) (frontMatter, string, error) {
	var fm frontMatter
	text = strings.ReplaceAll(text, "\r\n", "\n")
	rest, ok := strings.CutPrefix(text, "---\n")
	if !ok {
		return fm, "", errors.New("no front matter")
	}
	head, body, ok := strings.Cut(rest, "\n---\n")
	if !ok {
		return fm, "", errors.New("front matter is not closed")
	}
	if err := yaml.Unmarshal([]byte(head), &fm); err != nil {
		return fm, "", err
	}
	return fm, body, nil
}

// build writes the archive: one top folder, docs-site/, holding the site
// files and reference.json.
func build(dir, version string) ([]byte, error) {
	files, err := checkSite(dir)
	if err != nil {
		return nil, err
	}
	ref, err := cli.Reference(version)
	if err != nil {
		return nil, err
	}
	if err := spec.ValidateDocsReference(ref); err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	// A fixed time keeps the archive the same for the same inputs.
	mtime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	add := func(name string, data []byte) error {
		if err := tw.WriteHeader(&tar.Header{Name: "docs-site/" + name, Mode: 0o644, Size: int64(len(data)), ModTime: mtime, Typeflag: tar.TypeReg}); err != nil {
			return err
		}
		_, err := tw.Write(data)
		return err
	}
	for _, rel := range files {
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			return nil, err
		}
		if err := add(rel, data); err != nil {
			return nil, err
		}
	}
	if err := add("reference.json", ref); err != nil {
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func main() {
	version := flag.String("version", "", "the release version, such as 0.2.0")
	out := flag.String("out", "build/docs-site.tar.gz", "where to write the archive")
	dir := flag.String("site", "docs/site", "the docs/site folder")
	flag.Parse()
	if *version == "" {
		fmt.Fprintln(os.Stderr, "docsite: --version is required")
		os.Exit(2)
	}
	data, err := build(*dir, *version)
	if err == nil {
		err = os.MkdirAll(filepath.Dir(*out), 0o750)
	}
	if err == nil {
		err = os.WriteFile(*out, data, 0o644) // #nosec G306 -- a release artifact
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "docsite:", err)
		os.Exit(1)
	}
	fmt.Printf("Wrote %s (%d bytes)\n", *out, len(data))
}
