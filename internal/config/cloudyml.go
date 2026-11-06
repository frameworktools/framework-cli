package config

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// CloudFile holds the default server saved by a login.
const CloudFile = "cloud.yml"

type cloudYAML struct {
	Server string `yaml:"server"`
}

// SavedServer returns the default server from the most recently written
// cloud.yml in fw's folder or R's folder, so the last login in any tool wins.
// On a tie fw's folder comes first. R and Python follow the same rule. It
// returns "" when no readable file names one.
func (d Dirs) SavedServer() string {
	best, bestTime := "", int64(-1)
	for _, dir := range []string{d.FW, d.R} {
		path := filepath.Join(dir, CloudFile)
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var doc cloudYAML
		if yaml.Unmarshal(data, &doc) != nil || doc.Server == "" {
			continue
		}
		if t := info.ModTime().UnixNano(); t > bestTime {
			best, bestTime = doc.Server, t
		}
	}
	return best
}

// SaveServer writes url as the default server in fw's folder.
func (d Dirs) SaveServer(url string) error {
	if err := d.Ensure(); err != nil {
		return err
	}
	data, err := yaml.Marshal(cloudYAML{Server: url})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(d.FW, CloudFile), data, 0o600)
}
