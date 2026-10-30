package project

import (
	"os"
	"path/filepath"
)

// markers identify a Framework project root: the spec folder that every
// language writes, or the settings file of R projects made before specs.
var markers = []string{".framework", "settings.yml"}

// FindRoot walks up from start to the nearest Framework project root. It
// returns "" when start is not inside a project.
func FindRoot(start string) string {
	dir, err := filepath.Abs(start)
	if err != nil {
		return ""
	}
	for {
		for _, marker := range markers {
			if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}
