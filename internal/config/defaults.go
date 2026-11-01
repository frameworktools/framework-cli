package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// CachedDefaults is the layers Framework Cloud resolved from a person's
// defaults for one language, kept so projects can be created offline. R,
// Python, and fw read and write the same shape.
type CachedDefaults struct {
	FetchedAt time.Time       `json:"fetched_at"`
	Revision  string          `json:"defaults_revision"`
	Envelope  json.RawMessage `json:"envelope"`
}

// defaultsPath is cache/defaults/<host[:port]>/<language>.json in dir. A
// colon cannot appear in a Windows folder name, so every colon becomes an
// underscore.
func defaultsPath(dir, host, language string) string {
	return filepath.Join(dir, "cache", "defaults", strings.ReplaceAll(host, ":", "_"), language+".json")
}

// SaveDefaults writes the cache for host and language in fw's own folder.
func (d Dirs) SaveDefaults(host, language string, c CachedDefaults) error {
	path := defaultsPath(d.FW, host, language)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// LoadDefaults reads the newest cache for host and language from fw's folder
// or R's. ok is false when neither has a readable one.
func (d Dirs) LoadDefaults(host, language string) (CachedDefaults, bool) {
	var best CachedDefaults
	found := false
	for _, dir := range []string{d.FW, d.R} {
		if dir == "" {
			continue
		}
		data, err := os.ReadFile(defaultsPath(dir, host, language))
		if err != nil {
			continue
		}
		var c CachedDefaults
		if json.Unmarshal(data, &c) != nil || len(c.Envelope) == 0 {
			continue
		}
		if !found || c.FetchedAt.After(best.FetchedAt) {
			best, found = c, true
		}
	}
	return best, found
}
