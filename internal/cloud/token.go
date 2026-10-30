package cloud

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/frameworktools/framework-cli/internal/config"
	"github.com/frameworktools/framework-cli/internal/keyring"
)

// LegacyKeyringService is the keyring service older R releases used for
// framework.pub. It is read and removed, never written.
const LegacyKeyringService = "framework.pub"

const keyringAccount = "token"

// Tokens finds, stores, and removes user tokens. The lookup order matches
// R's .fw_cloud_token():
//
//  1. FW_CLOUD_TOKEN
//  2. FW_TOKEN_FILE, which is authoritative when set
//  3. the keyring (and the legacy service, for framework.pub)
//  4. the token file in fw's folder, then in R's folder
//  5. ~/.secrets/framework/token, for framework.pub only
type Tokens struct {
	Getenv  config.Env
	Dirs    config.Dirs
	Keyring keyring.Store
	Home    string
}

// Found is a token and a description of where it came from. Source never
// contains the token.
type Found struct {
	Token  string
	Source string
}

func (t Tokens) legacyFile() string {
	return filepath.Join(t.Home, ".secrets", "framework", "token")
}

// Lookup returns the token for url, or ok=false when there is none.
func (t Tokens) Lookup(url string) (Found, bool) {
	if token := strings.TrimSpace(t.Getenv("FW_CLOUD_TOKEN")); token != "" {
		return Found{token, "the FW_CLOUD_TOKEN environment variable"}, true
	}
	if override := t.Getenv("FW_TOKEN_FILE"); override != "" {
		path := config.ExpandHome(override, t.Home)
		if token := readTokenFile(path); token != "" {
			return Found{token, path + " (FW_TOKEN_FILE)"}, true
		}
		return Found{}, false
	}

	services := []string{KeyringService(url)}
	if IsDefault(url) {
		services = append(services, LegacyKeyringService)
	}
	for _, service := range services {
		if token, err := t.Keyring.Get(service, keyringAccount); err == nil && token != "" {
			return Found{token, "the system keyring (" + service + ")"}, true
		}
	}

	for _, dir := range []string{t.Dirs.FW, t.Dirs.R} {
		path := filepath.Join(dir, TokenFileName(url))
		if token := readTokenFile(path); token != "" {
			return Found{token, path}, true
		}
	}
	if IsDefault(url) {
		if token := readTokenFile(t.legacyFile()); token != "" {
			return Found{token, t.legacyFile()}, true
		}
	}
	return Found{}, false
}

// Store saves token for url: in FW_TOKEN_FILE when set, else the keyring,
// else a 0600 file in fw's folder. It returns where the token went.
func (t Tokens) Store(url, token string) (string, error) {
	if override := t.Getenv("FW_TOKEN_FILE"); override != "" {
		path := config.ExpandHome(override, t.Home)
		if err := writeTokenFile(path, token); err != nil {
			return "", err
		}
		return path + " (FW_TOKEN_FILE)", nil
	}

	service := KeyringService(url)
	keyErr := t.Keyring.Set(service, keyringAccount, token)
	if keyErr == nil {
		t.removeLegacyFile(url)
		return "the system keyring (" + service + ")", nil
	}

	if err := t.Dirs.Ensure(); err != nil {
		return "", err
	}
	path := filepath.Join(t.Dirs.FW, TokenFileName(url))
	if err := writeTokenFile(path, token); err != nil {
		return "", fmt.Errorf("the keyring failed (%w), and so did %s: %w", keyErr, path, err)
	}
	t.removeLegacyFile(url)
	return path + " (the system keyring is unavailable: " + keyErr.Error() + ")", nil
}

// removeLegacyFile retires ~/.secrets/framework/token after a framework.pub
// login, so only one copy of the secret exists. R does the same.
func (t Tokens) removeLegacyFile(url string) {
	if IsDefault(url) {
		_ = os.Remove(t.legacyFile())
	}
}

// Removal is what Remove deleted, and token files it left alone.
type Removal struct {
	Removed []string
	// Kept lists token files in R's folder. fw never writes there, so it
	// names them for the user instead.
	Kept []string
}

// Remove deletes the stored tokens for url from the keyring and fw's folder.
// With FW_TOKEN_FILE set, it deletes only that file.
func (t Tokens) Remove(url string) (Removal, error) {
	var r Removal
	if override := t.Getenv("FW_TOKEN_FILE"); override != "" {
		path := config.ExpandHome(override, t.Home)
		if err := os.Remove(path); err == nil {
			r.Removed = append(r.Removed, path)
		} else if !errors.Is(err, os.ErrNotExist) {
			return r, err
		}
		return r, nil
	}

	services := []string{KeyringService(url)}
	if IsDefault(url) {
		services = append(services, LegacyKeyringService)
	}
	for _, service := range services {
		err := t.Keyring.Delete(service, keyringAccount)
		switch {
		case err == nil:
			r.Removed = append(r.Removed, "the system keyring ("+service+")")
		case !errors.Is(err, keyring.ErrNotFound):
			return r, fmt.Errorf("keyring (%s): %w", service, err)
		}
	}

	paths := []string{filepath.Join(t.Dirs.FW, TokenFileName(url))}
	if IsDefault(url) {
		paths = append(paths, t.legacyFile())
	}
	for _, path := range paths {
		if err := os.Remove(path); err == nil {
			r.Removed = append(r.Removed, path)
		} else if !errors.Is(err, os.ErrNotExist) {
			return r, err
		}
	}

	if filepath.Clean(t.Dirs.R) != filepath.Clean(t.Dirs.FW) {
		path := filepath.Join(t.Dirs.R, TokenFileName(url))
		if readTokenFile(path) != "" {
			r.Kept = append(r.Kept, path)
		}
	}
	return r, nil
}

func readTokenFile(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	if scanner.Scan() {
		return strings.TrimSpace(scanner.Text())
	}
	return ""
}

func writeTokenFile(path, token string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		return err
	}
	// WriteFile keeps the mode of an existing file
	return os.Chmod(path, 0o600)
}
