package config

import (
	"os"
	"path/filepath"
	"sort"

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
	if servers := d.SavedServers(); len(servers) > 0 {
		return servers[0]
	}
	return ""
}

// SavedServers returns the server named by each readable cloud.yml in fw's
// folder and R's folder, the most recently written first. On a tie fw's
// folder comes first. Duplicates are kept; callers compare normalized URLs.
func (d Dirs) SavedServers() []string {
	type saved struct {
		url  string
		time int64
	}
	var found []saved
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
		found = append(found, saved{doc.Server, info.ModTime().UnixNano()})
	}
	sort.SliceStable(found, func(i, j int) bool { return found[i].time > found[j].time })
	urls := make([]string, len(found))
	for i, f := range found {
		urls[i] = f.url
	}
	return urls
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
