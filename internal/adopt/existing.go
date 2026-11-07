package adopt

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// markers are the files that make a folder a Framework project, in the
// order they are reported. config.yml counts only when it is Framework's
// own (see FrameworkConfig).
var markers = []string{".framework/spec.json", "settings.yml", "settings.yaml", "settings.do", "config.yml"}

// frameworkKeys are settings only Framework's config.yml has under default:.
// Generic keys such as data or options are left out, since R's config
// package uses default: too.
var frameworkKeys = []string{"project_type", "project_name", "directories", "packages", "connections", "ai", "git", "security", "scaffold"}

// Existing returns the file that shows dir is already a Framework project,
// relative to dir with forward slashes, or "" when it is not one.
func Existing(dir string) string {
	for _, rel := range markers {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if _, err := os.Stat(path); err != nil {
			continue
		}
		if rel == "config.yml" && !FrameworkConfig(path) {
			continue
		}
		return rel
	}
	return ""
}

// FrameworkConfig reports whether the config.yml at path is a Framework
// settings file from before settings.yml: a default: section with at least
// one Framework key. A project's own config.yml, such as a pipeline's, is not.
func FrameworkConfig(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var doc map[string]any
	if yaml.Unmarshal(data, &doc) != nil {
		return false
	}
	def, ok := doc["default"].(map[string]any)
	if !ok {
		return false
	}
	for _, k := range frameworkKeys {
		if _, ok := def[k]; ok {
			return true
		}
	}
	return false
}
