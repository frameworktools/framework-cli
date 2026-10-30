package cloud

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frameworktools/framework-cli/internal/config"
	"github.com/frameworktools/framework-cli/internal/keyring"
)

func newTokens(t *testing.T, env map[string]string) (Tokens, keyring.Memory) {
	t.Helper()
	home := t.TempDir()
	mem := keyring.Memory{}
	return Tokens{
		Getenv:  envOf(env),
		Dirs:    config.Dirs{FW: filepath.Join(home, "fw"), R: filepath.Join(home, "r")},
		Keyring: mem,
		Home:    home,
	}, mem
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

const local = "http://localhost:8080"

func TestLookupOrder(t *testing.T) {
	tokens, mem := newTokens(t, nil)
	if _, ok := tokens.Lookup(local); ok {
		t.Fatal("want no token")
	}

	writeFile(t, filepath.Join(tokens.Dirs.R, "cloud-token-localhost_8080"), "r-file\n")
	if got, _ := tokens.Lookup(local); got.Token != "r-file" {
		t.Errorf("R folder fallback: got %q", got.Token)
	}
	writeFile(t, filepath.Join(tokens.Dirs.FW, "cloud-token-localhost_8080"), "fw-file\nsecond line\n")
	if got, _ := tokens.Lookup(local); got.Token != "fw-file" {
		t.Errorf("fw folder: got %q", got.Token)
	}
	_ = mem.Set("framework-cloud:localhost:8080", "token", "from-keyring")
	if got, _ := tokens.Lookup(local); got.Token != "from-keyring" {
		t.Errorf("keyring: got %q", got.Token)
	}

	tokens.Getenv = envOf(map[string]string{"FW_CLOUD_TOKEN": "from-env"})
	if got, _ := tokens.Lookup(local); got.Token != "from-env" || strings.Contains(got.Source, "from-env") {
		t.Errorf("env: got %+v", got)
	}
}

func TestTokenFileOverrideIsAuthoritative(t *testing.T) {
	tokens, mem := newTokens(t, nil)
	_ = mem.Set("framework-cloud:localhost:8080", "token", "from-keyring")
	path := filepath.Join(tokens.Home, "override")
	tokens.Getenv = envOf(map[string]string{"FW_TOKEN_FILE": path})

	if _, ok := tokens.Lookup(local); ok {
		t.Fatal("an empty FW_TOKEN_FILE must not fall through to the keyring")
	}
	if _, err := tokens.Store(local, "stored"); err != nil {
		t.Fatal(err)
	}
	if got, _ := tokens.Lookup(local); got.Token != "stored" {
		t.Errorf("got %q", got.Token)
	}
	if v, _ := mem.Get("framework-cloud:localhost:8080", "token"); v != "from-keyring" {
		t.Error("Store with FW_TOKEN_FILE must not touch the keyring")
	}
}

func TestLegacyFramworkPub(t *testing.T) {
	tokens, mem := newTokens(t, nil)
	legacy := filepath.Join(tokens.Home, ".secrets", "framework", "token")
	writeFile(t, legacy, "legacy-file\n")
	if got, _ := tokens.Lookup(DefaultURL); got.Token != "legacy-file" {
		t.Errorf("legacy file: got %q", got.Token)
	}
	if _, ok := tokens.Lookup(local); ok {
		t.Error("the legacy file is for framework.pub only")
	}
	_ = mem.Set(LegacyKeyringService, "token", "legacy-keyring")
	if got, _ := tokens.Lookup(DefaultURL); got.Token != "legacy-keyring" {
		t.Errorf("legacy keyring: got %q", got.Token)
	}

	if _, err := tokens.Store(DefaultURL, "new"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Error("login should retire the legacy file")
	}
	if got, _ := tokens.Lookup(DefaultURL); got.Token != "new" {
		t.Errorf("after store: got %q", got.Token)
	}

	r, err := tokens.Remove(DefaultURL)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Removed) != 2 {
		t.Errorf("removed %v; want both keyring services", r.Removed)
	}
	if _, ok := tokens.Lookup(DefaultURL); ok {
		t.Error("token still found after Remove")
	}
}

type brokenKeyring struct{ keyring.Memory }

func (brokenKeyring) Set(string, string, string) error { return os.ErrPermission }

func TestStoreFallsBackToFile(t *testing.T) {
	tokens, _ := newTokens(t, nil)
	tokens.Keyring = brokenKeyring{keyring.Memory{}}
	where, err := tokens.Store(local, "file-token")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(tokens.Dirs.FW, "cloud-token-localhost_8080")
	if !strings.HasPrefix(where, path) {
		t.Errorf("stored in %q; want %s", where, path)
	}
	if info, err := os.Stat(path); err != nil || (os.PathSeparator == '/' && info.Mode().Perm() != 0o600) {
		t.Errorf("token file: %v %v", info, err)
	}
}

func TestRemoveNamesRFiles(t *testing.T) {
	tokens, _ := newTokens(t, nil)
	writeFile(t, filepath.Join(tokens.Dirs.R, "cloud-token-localhost_8080"), "r-file\n")
	r, err := tokens.Remove(local)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Removed) != 0 || len(r.Kept) != 1 {
		t.Errorf("got %+v", r)
	}
}
