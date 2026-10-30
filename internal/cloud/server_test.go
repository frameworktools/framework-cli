package cloud

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/frameworktools/framework-cli/internal/config"
)

func envOf(m map[string]string) config.Env {
	return func(key string) string { return m[key] }
}

func TestResolve(t *testing.T) {
	saved := t.TempDir()
	dirs := config.Dirs{FW: saved, R: t.TempDir()}
	if err := dirs.SaveServer("https://saved.example.edu"); err != nil {
		t.Fatal(err)
	}
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, ".env"), []byte("FW_CLOUD_URL=\"https://project.example.edu\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	withEnv := envOf(map[string]string{"FW_CLOUD_URL": "https://env.example.edu"})
	noEnv := envOf(nil)

	cases := []struct {
		name   string
		r      Resolver
		scope  Scope
		url    string
		source string
	}{
		{"flag wins", Resolver{Flag: "flag.example.edu", Getenv: withEnv, Dirs: dirs, ProjectDir: proj}, ProjectScope, "https://flag.example.edu", "the --url flag"},
		{"user: env before saved", Resolver{Getenv: withEnv, Dirs: dirs, ProjectDir: proj}, UserScope, "https://env.example.edu", "the FW_CLOUD_URL environment variable"},
		{"user: saved, never the project .env", Resolver{Getenv: noEnv, Dirs: dirs, ProjectDir: proj}, UserScope, "https://saved.example.edu", "your fw login default"},
		{"project: .env before env", Resolver{Getenv: withEnv, Dirs: dirs, ProjectDir: proj}, ProjectScope, "https://project.example.edu", "the project's .env"},
		{"project: never the saved default", Resolver{Getenv: noEnv, Dirs: dirs}, ProjectScope, DefaultURL, "the built-in default"},
	}
	for _, c := range cases {
		got, err := c.r.Resolve(c.scope)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got.URL != c.url || got.Source != c.source {
			t.Errorf("%s: got %+v; want %s from %s", c.name, got, c.url, c.source)
		}
	}
}

func TestResolveRejectsPlainHTTP(t *testing.T) {
	r := Resolver{Getenv: envOf(map[string]string{"FW_CLOUD_URL": "http://framework.example.edu"}), Dirs: config.Dirs{FW: t.TempDir(), R: t.TempDir()}}
	if _, err := r.Resolve(UserScope); err == nil {
		t.Fatal("want an error for plain http on a remote host")
	}
}

func TestSavedServerFallsBackToR(t *testing.T) {
	dirs := config.Dirs{FW: t.TempDir(), R: t.TempDir()}
	if err := os.WriteFile(filepath.Join(dirs.R, config.CloudFile), []byte("server: https://r.example.edu\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Resolver{Getenv: envOf(nil), Dirs: dirs}.Resolve(UserScope)
	if err != nil || got.URL != "https://r.example.edu" {
		t.Fatalf("got %+v, %v; want R's saved server", got, err)
	}
}
