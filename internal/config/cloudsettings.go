package config

import (
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// CloudSettingsFile is the copy of a person's Framework Cloud settings
// document that cloud_sync() keeps in R and Python.
const CloudSettingsFile = "cloud-settings.yml"

// SyncedProjectsDir returns defaults.projects_dir, the Projects folder set in
// Framework Cloud Settings, from the newest settings copy in fw's folder
// (written by Python) or R's. It is "" when neither copy sets it.
func (d Dirs) SyncedProjectsDir() string {
	best, bestTime := "", int64(-1)
	for _, dir := range []string{d.FW, d.R} {
		if dir == "" {
			continue
		}
		path := filepath.Join(dir, CloudSettingsFile)
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue
		}
		data, err := os.ReadFile(path) // #nosec G304 -- fixed name in the user's config folder
		if err != nil {
			continue
		}
		var doc struct {
			Defaults map[string]any `yaml:"defaults"`
		}
		if yaml.Unmarshal(data, &doc) != nil {
			continue
		}
		if t := info.ModTime().UnixNano(); t > bestTime {
			value, _ := doc.Defaults["projects_dir"].(string)
			best, bestTime = strings.TrimSpace(value), t
		}
	}
	return best
}
