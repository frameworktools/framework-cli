// Package publish builds destination-independent artifact bundles.
package publish

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type File struct {
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}
type Manifest struct {
	Schema         string  `json:"schema"`
	Slug           string  `json:"slug"`
	Title          string  `json:"title"`
	Description    *string `json:"description"`
	Kind           string  `json:"kind"`
	Entrypoint     string  `json:"entrypoint"`
	Representation *string `json:"representation"`
	Original       string  `json:"original"`
	Source         any     `json:"source"`
	Files          []File  `json:"files"`
	CreatedAt      string  `json:"created_at"`
	Generator      string  `json:"generator"`
}
type Bundle struct {
	Dir      string
	Manifest Manifest
	Sources  []string
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

func Slug(s string) string {
	return strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(s), "-"), "-")
}
func Hash(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// Build copies files (source paths mapped to bundle paths) into a temporary bundle.
func Build(files map[string]string, slug, title, description, kind, original, generator, reader string) (*Bundle, error) {
	dir, err := os.MkdirTemp("", "framework-bundle-")
	if err != nil {
		return nil, err
	}
	b := &Bundle{Dir: dir, Manifest: Manifest{Schema: "framework.bundle/v1", Slug: slug, Title: title, Kind: kind, Entrypoint: "index.html", Original: original, CreatedAt: time.Now().UTC().Format(time.RFC3339), Generator: generator}}
	if description != "" {
		b.Manifest.Description = &description
	}
	fail := func(e error) (*Bundle, error) { _ = os.RemoveAll(dir); return nil, e }
	seen := map[string]bool{}
	for src, rel := range files {
		clean := filepath.Clean(filepath.FromSlash(rel))
		if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return fail(fmt.Errorf("invalid bundle path %q", rel))
		}
		if seen[clean] {
			return fail(fmt.Errorf("duplicate bundle path %s", rel))
		}
		seen[clean] = true
		info, e := os.Lstat(src)
		if e != nil {
			return fail(e)
		}
		if !info.Mode().IsRegular() {
			return fail(fmt.Errorf("bundle source must be a regular file: %s", src))
		}
		data, e := os.ReadFile(src)
		if e != nil {
			return fail(e)
		}
		dst := filepath.Join(dir, clean)
		if e = os.MkdirAll(filepath.Dir(dst), 0750); e != nil {
			return fail(e)
		}
		if e = os.WriteFile(dst, data, 0644); e != nil { // #nosec G306 -- published web files
			return fail(e)
		}
		b.Sources = append(b.Sources, src)
	}
	if _, e := os.Stat(filepath.Join(dir, "index.html")); os.IsNotExist(e) {
		data, e := os.ReadFile(filepath.Join(dir, original))
		if e != nil {
			return fail(e)
		}
		var representation any
		if json.Unmarshal(data, &representation) == nil {
			if doc, ok := representation.(map[string]any); ok && doc["schema"] == "framework.artifact/v1" {
				r := "representation.json"
				b.Manifest.Representation = &r
				b.Manifest.Source = doc["source"]
				if e = os.WriteFile(filepath.Join(dir, r), data, 0644); e != nil { // #nosec G306 -- published web files
					return fail(e)
				}
			}
		}
		payload, _ := json.Marshal(map[string]any{"manifest": b.Manifest, "representation": representation})
		page := strings.ReplaceAll(reader, "__FRAMEWORK_TITLE__", html.EscapeString(title))
		page = strings.ReplaceAll(page, "__FRAMEWORK_ARTIFACT__", string(payload))
		if reader == "" {
			return fail(fmt.Errorf("static reader is unavailable"))
		}
		if e = os.WriteFile(filepath.Join(dir, "index.html"), []byte(page), 0644); e != nil { // #nosec G306 -- published web files
			return fail(e)
		}
	}
	// Read through os.Root so nothing in the walk can lead outside the bundle.
	inside, err := os.OpenRoot(dir)
	if err != nil {
		return fail(err)
	}
	defer inside.Close()
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			return nil
		}
		rel, e := filepath.Rel(dir, path)
		if e != nil {
			return e
		}
		data, e := inside.ReadFile(rel)
		if e != nil {
			return e
		}
		b.Manifest.Files = append(b.Manifest.Files, File{filepath.ToSlash(rel), int64(len(data)), Hash(data)})
		return nil
	})
	if err != nil {
		return fail(err)
	}
	sort.Slice(b.Manifest.Files, func(i, j int) bool { return b.Manifest.Files[i].Path < b.Manifest.Files[j].Path })
	data, _ := json.MarshalIndent(b.Manifest, "", "  ")
	if err = os.WriteFile(filepath.Join(dir, "artifact.json"), data, 0644); err != nil { // #nosec G306 -- published web files
		return fail(err)
	}
	return b, nil
}
