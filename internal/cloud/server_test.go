package cloud

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

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
		{"project: env when .env has none", Resolver{Getenv: withEnv, Dirs: dirs, ProjectDir: t.TempDir()}, ProjectScope, "https://env.example.edu", "the FW_CLOUD_URL environment variable"},
		{"project: saved login when nothing else names one", Resolver{Getenv: noEnv, Dirs: dirs, ProjectDir: t.TempDir()}, ProjectScope, "https://saved.example.edu", "your fw login default"},
		{"login: saved before the default", Resolver{Getenv: noEnv, Dirs: dirs}, LoginScope, "https://saved.example.edu", "your fw login default"},
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

// A self-hosted login is used for a project whose .env has no FW_CLOUD_URL,
// so its project token never goes to framework.pub.
func TestResolveProjectUsesSelfHostedLogin(t *testing.T) {
	dirs := config.Dirs{FW: t.TempDir(), R: t.TempDir()}
	if err := dirs.SaveServer("https://framework.example.edu"); err != nil {
		t.Fatal(err)
	}
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, ".env"), []byte("FW_PROJECT_TOKEN=p\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Resolver{Getenv: envOf(nil), Dirs: dirs, ProjectDir: proj}.Resolve(ProjectScope)
	if err != nil || got.URL != "https://framework.example.edu" {
		t.Fatalf("got %+v, %v; want the saved self-hosted server", got, err)
	}
}

// With nothing naming a server, only login offers framework.pub.
func TestResolveNeverDefaultsToPublicCloud(t *testing.T) {
	r := Resolver{Getenv: envOf(nil), Dirs: config.Dirs{FW: t.TempDir(), R: t.TempDir()}, ProjectDir: t.TempDir()}
	for _, scope := range []Scope{UserScope, ProjectScope} {
		got, err := r.Resolve(scope)
		if !errors.Is(err, ErrNoServer) {
			t.Errorf("scope %d: got %+v, %v; want ErrNoServer", scope, got, err)
		}
	}
	got, err := r.Resolve(LoginScope)
	if err != nil || got.URL != DefaultURL || got.Source != "the fw login default" {
		t.Errorf("login: got %+v, %v; want framework.pub as the login default", got, err)
	}
}

// Logins to two servers leave a project without FW_CLOUD_URL ambiguous. User
// calls still use the most recent login.
func TestResolveProjectFailsOnSeveralLogins(t *testing.T) {
	dirs := config.Dirs{FW: t.TempDir(), R: t.TempDir()}
	if err := os.WriteFile(filepath.Join(dirs.R, config.CloudFile), []byte("server: https://framework.pub\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := dirs.SaveServer("https://framework.example.edu"); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(dirs.R, config.CloudFile), old, old); err != nil {
		t.Fatal(err)
	}
	r := Resolver{Getenv: envOf(nil), Dirs: dirs, ProjectDir: t.TempDir()}
	if _, err := r.Resolve(ProjectScope); !errors.Is(err, ErrAmbiguousServer) {
		t.Errorf("project: %v; want ErrAmbiguousServer", err)
	}
	if got, err := r.Resolve(UserScope); err != nil || got.URL != "https://framework.example.edu" {
		t.Errorf("user: got %+v, %v; want the most recent login", got, err)
	}
	// The same server saved twice is one login
	if err := os.WriteFile(filepath.Join(dirs.R, config.CloudFile), []byte("server: framework.example.edu/\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := r.Resolve(ProjectScope); err != nil || got.URL != "https://framework.example.edu" {
		t.Errorf("duplicate login: got %+v, %v", got, err)
	}
}
