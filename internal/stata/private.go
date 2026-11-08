package stata

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// PrivateFolders lists the private folders a Stata project's settings name:
// each directories entry whose key contains "private" and not "public" and
// whose path is inside the project, sorted. Git doesn't keep them, so a
// fresh checkout lacks them. R: scaffold() creates the same folders.
func PrivateFolders(v View) []string {
	var out []string
	for k, p := range v.Directories {
		if !strings.Contains(k, "private") || strings.Contains(k, "public") {
			continue
		}
		clean := path.Clean(strings.ReplaceAll(p, `\`, "/"))
		if clean == "." || path.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") || filepath.IsAbs(p) {
			continue
		}
		if !contains(out, clean) {
			out = append(out, clean)
		}
	}
	sort.Strings(out)
	return out
}

// MissingPrivateFolders lists the PrivateFolders that don't exist under
// root.
func MissingPrivateFolders(root string, v View) []string {
	var out []string
	for _, p := range PrivateFolders(v) {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(p))); errors.Is(err, fs.ErrNotExist) {
			out = append(out, p)
		}
	}
	return out
}

// CreatePrivateFolders creates the private folders the project at root
// lacks, as its .framework/spec.json names them, and returns them. A
// project without a spec gets none: settings.do creates its folders when
// Stata runs it.
func CreatePrivateFolders(root string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(root, ".framework", "spec.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	r, err := FromSpec(doc)
	if err != nil {
		return nil, err
	}
	missing := MissingPrivateFolders(root, r.View)
	for _, p := range missing {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(p)), 0o755); err != nil {
			return nil, err
		}
	}
	return missing, nil
}
