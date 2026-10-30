// Package config locates Framework's user configuration folders.
//
// fw and the Python package share one folder: FW_CONFIG_HOME, else
// $XDG_CONFIG_HOME/framework, else ~/.config/framework. The R package keeps
// its own folder, tools::R_user_dir("framework", "config"), so it stays
// CRAN-safe. fw reads R's folder as a fallback and never writes to it.
package config

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Env reads environment variables. Tests replace it.
type Env func(key string) string

// Dirs holds the folders fw reads and writes.
type Dirs struct {
	// FW is fw's own folder, the only one it writes to.
	FW string
	// R is the R package's folder, read as a fallback. It equals FW when
	// FW_CONFIG_HOME is set, because R honors that variable too.
	R string
}

// Resolve finds the folders for the current user.
func Resolve() (Dirs, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Dirs{}, err
	}
	return resolve(os.Getenv, home, runtime.GOOS)
}

func resolve(env Env, home, goos string) (Dirs, error) {
	if home == "" {
		return Dirs{}, errors.New("cannot find your home folder")
	}
	if custom := env("FW_CONFIG_HOME"); custom != "" {
		dir := ExpandHome(custom, home)
		return Dirs{FW: dir, R: dir}, nil
	}

	fw := filepath.Join(home, ".config", "framework")
	if xdg := env("XDG_CONFIG_HOME"); xdg != "" {
		fw = filepath.Join(ExpandHome(xdg, home), "framework")
	}
	return Dirs{FW: fw, R: rUserDir(env, home, goos)}, nil
}

// rUserDir mirrors tools::R_user_dir("framework", "config").
func rUserDir(env Env, home, goos string) string {
	var base string
	switch {
	case env("R_USER_CONFIG_DIR") != "":
		base = ExpandHome(env("R_USER_CONFIG_DIR"), home)
	case env("XDG_CONFIG_HOME") != "":
		base = ExpandHome(env("XDG_CONFIG_HOME"), home)
	case goos == "windows":
		appdata := env("APPDATA")
		if appdata == "" {
			appdata = filepath.Join(home, "AppData", "Roaming")
		}
		base = filepath.Join(appdata, "R", "config")
	case goos == "darwin":
		base = filepath.Join(home, "Library", "Preferences", "org.R-project.R")
	default:
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "R", "framework")
}

// ExpandHome replaces a leading ~ with home.
func ExpandHome(path, home string) string {
	if path == "~" {
		return home
	}
	if strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) {
		return filepath.Join(home, path[2:])
	}
	return path
}

// Ensure creates fw's folder with owner-only permissions.
func (d Dirs) Ensure() error {
	return os.MkdirAll(d.FW, 0o700)
}

// Find returns the path of name in fw's folder, or in R's folder when only
// R has it. It returns "" when neither has it.
func (d Dirs) Find(name string) string {
	for _, dir := range []string{d.FW, d.R} {
		path := filepath.Join(dir, name)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path
		}
	}
	return ""
}
