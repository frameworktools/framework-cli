package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// SyncResult lists what Sync did, by file name.
type SyncResult struct {
	Copied  []string
	Skipped []string // already present in fw's folder
}

// Sync copies cloud.yml and the token files from R's folder into fw's folder.
// It never overwrites a file and never writes to R's folder.
func (d Dirs) Sync() (SyncResult, error) {
	var result SyncResult
	if filepath.Clean(d.FW) == filepath.Clean(d.R) {
		return result, nil
	}
	entries, err := os.ReadDir(d.R)
	if err != nil {
		if os.IsNotExist(err) {
			return result, nil
		}
		return result, err
	}
	for _, entry := range entries {
		name := entry.Name()
		if !entry.Type().IsRegular() || !syncable(name) {
			continue
		}
		if err := d.Ensure(); err != nil {
			return result, err
		}
		copied, err := copyNew(filepath.Join(d.R, name), filepath.Join(d.FW, name))
		if err != nil {
			return result, err
		}
		if copied {
			result.Copied = append(result.Copied, name)
		} else {
			result.Skipped = append(result.Skipped, name)
		}
	}
	return result, nil
}

func syncable(name string) bool {
	return name == CloudFile || name == "cloud-token" || strings.HasPrefix(name, "cloud-token-")
}

// copyNew copies src to dst with mode 0600 unless dst exists.
func copyNew(src, dst string) (bool, error) {
	data, err := os.ReadFile(src)
	if err != nil {
		return false, err
	}
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return false, nil
		}
		return false, err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return false, err
	}
	return true, f.Close()
}
