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

// SavedServer returns the default server from cloud.yml, reading fw's folder
// first and R's folder second. It returns "" when no readable file names one.
func (d Dirs) SavedServer() string {
	path := d.Find(CloudFile)
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var doc cloudYAML
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return ""
	}
	return doc.Server
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
