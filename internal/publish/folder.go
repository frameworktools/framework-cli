package publish

import (
	"encoding/json"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
)

type Receipt struct {
	Driver     string  `json:"driver"`
	URL        string  `json:"url"`
	Version    int     `json:"version"`
	VersionURL string  `json:"version_url"`
	Location   string  `json:"location,omitempty"`
	Commit     *string `json:"commit,omitempty"`
}

// Copy rejects symlinks in either tree. Static destinations must not escape the site.
func Copy(src, dst string) error {
	if info, err := os.Lstat(dst); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("destination is a symbolic link: %s", dst)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("source is a symbolic link: %s", src)
	}
	if info.IsDir() {
		if err = os.MkdirAll(dst, 0750); err != nil {
			return err
		}
		rows, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if err = Copy(filepath.Join(src, row.Name()), filepath.Join(dst, row.Name())); err != nil {
				return err
			}
		}
		return nil
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0644) // #nosec G306 -- published web files
}

// Folder keeps every delivered bundle in an immutable v/N directory.
func Folder(b *Bundle, root, url string) (Receipt, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return Receipt{}, err
	}
	// Check every existing ancestor before creating directories.
	for p := filepath.Join(abs, b.Manifest.Slug, "v"); ; p = filepath.Dir(p) {
		// macOS links /var, /tmp, and /etc into /private.
		systemLink := runtime.GOOS == "darwin" && (p == "/var" || p == "/tmp" || p == "/etc")
		if st, e := os.Lstat(p); e == nil && st.Mode()&os.ModeSymlink != 0 && !systemLink {
			return Receipt{}, fmt.Errorf("destination contains a symbolic link: %s", p)
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	for _, name := range []string{"index.html", "index.json"} {
		if st, e := os.Lstat(filepath.Join(abs, name)); e == nil && st.Mode()&os.ModeSymlink != 0 {
			return Receipt{}, fmt.Errorf("site index is a symbolic link: %s", name)
		}
	}
	dest := filepath.Join(abs, b.Manifest.Slug)
	versions := filepath.Join(dest, "v")
	if err = os.MkdirAll(versions, 0750); err != nil {
		return Receipt{}, err
	}
	n := 1
	rows, err := os.ReadDir(versions)
	if err != nil {
		return Receipt{}, err
	}
	for _, r := range rows {
		v, e := strconv.Atoi(r.Name())
		if e == nil && v >= n {
			n = v + 1
		}
	}
	// Repeated identical content keeps the existing version and commit.
	if oldData, e := os.ReadFile(filepath.Join(dest, "artifact.json")); e == nil {
		var old Manifest
		if json.Unmarshal(oldData, &old) == nil && reflect.DeepEqual(old.Files, b.Manifest.Files) && old.Title == b.Manifest.Title && reflect.DeepEqual(old.Description, b.Manifest.Description) && old.Kind == b.Manifest.Kind {
			b.Manifest = old
			if e = os.WriteFile(filepath.Join(b.Dir, "artifact.json"), oldData, 0644); e != nil { // #nosec G306 -- published web files
				return Receipt{}, e
			}
			r := Receipt{Driver: "folder", Version: n - 1, Location: dest}
			if url != "" {
				r.URL = strings.TrimRight(url, "/") + "/" + b.Manifest.Slug + "/"
				r.VersionURL = r.URL + "v/" + strconv.Itoa(n-1) + "/"
			}
			return r, nil
		}
	}
	archive := filepath.Join(versions, strconv.Itoa(n))
	if err = os.Mkdir(archive, 0750); err != nil {
		return Receipt{}, err
	}
	if err = Copy(b.Dir, archive); err != nil {
		return Receipt{}, err
	}
	// Remove stale latest files, preserving archives.
	rows, err = os.ReadDir(dest)
	if err != nil {
		return Receipt{}, err
	}
	for _, r := range rows {
		if r.Name() != "v" {
			if err = os.RemoveAll(filepath.Join(dest, r.Name())); err != nil {
				return Receipt{}, err
			}
		}
	}
	if err = Copy(b.Dir, dest); err != nil {
		return Receipt{}, err
	}
	if err = Index(abs); err != nil {
		return Receipt{}, err
	}
	res := Receipt{Driver: "folder", Version: n, Location: dest}
	if url != "" {
		res.URL = strings.TrimRight(url, "/") + "/" + b.Manifest.Slug + "/"
		res.VersionURL = res.URL + "v/" + strconv.Itoa(n) + "/"
	}
	return res, nil
}
func Index(root string) error {
	for _, name := range []string{"index.html", "index.json"} {
		if st, e := os.Lstat(filepath.Join(root, name)); e == nil && st.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("site index is a symbolic link: %s", name)
		}
	}

	rows, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	var manifests []Manifest
	page := "<!doctype html><html lang=\"en\"><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width\"><title>Artifacts</title><main><h1>Artifacts</h1><ul>"
	for _, r := range rows {
		if !r.IsDir() {
			continue
		}
		data, e := os.ReadFile(filepath.Join(root, r.Name(), "artifact.json"))
		if e != nil {
			continue
		}
		var m Manifest
		if json.Unmarshal(data, &m) != nil || m.Schema != "framework.bundle/v1" {
			continue
		}
		manifests = append(manifests, m)
		page += "<li><a href=\"" + html.EscapeString(m.Slug) + "/\">" + html.EscapeString(m.Title) + "</a></li>"
	}
	data, _ := json.MarshalIndent(manifests, "", "  ")
	if err = os.WriteFile(filepath.Join(root, "index.json"), data, 0644); err != nil { // #nosec G306 -- published web files
		return err
	}
	return os.WriteFile(filepath.Join(root, "index.html"), []byte(page+"</ul></main></html>"), 0644) // #nosec G306 -- published web files
}
