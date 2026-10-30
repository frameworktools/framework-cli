package config

import (
	"os"
	"path/filepath"
	"testing"
)

func envOf(m map[string]string) Env {
	return func(key string) string { return m[key] }
}

func TestResolveDirs(t *testing.T) {
	home := filepath.FromSlash("/home/u")
	j := func(parts ...string) string { return filepath.Join(append([]string{home}, parts...)...) }

	cases := []struct {
		name  string
		env   map[string]string
		goos  string
		fw, r string
	}{
		{"FW_CONFIG_HOME is shared", map[string]string{"FW_CONFIG_HOME": "~/fw"}, "linux", j("fw"), j("fw")},
		{"linux defaults", nil, "linux", j(".config", "framework"), j(".config", "R", "framework")},
		{"macOS R folder", nil, "darwin", j(".config", "framework"), j("Library", "Preferences", "org.R-project.R", "R", "framework")},
		{"XDG applies to both", map[string]string{"XDG_CONFIG_HOME": "/x"}, "darwin", filepath.Join("/x", "framework"), filepath.Join("/x", "R", "framework")},
		{"R_USER_CONFIG_DIR beats XDG for R", map[string]string{"XDG_CONFIG_HOME": "/x", "R_USER_CONFIG_DIR": "/r"}, "linux", filepath.Join("/x", "framework"), filepath.Join("/r", "R", "framework")},
		{"windows APPDATA", map[string]string{"APPDATA": `C:\Users\u\AppData\Roaming`}, "windows", j(".config", "framework"), filepath.Join(`C:\Users\u\AppData\Roaming`, "R", "config", "R", "framework")},
	}
	for _, c := range cases {
		got, err := resolve(envOf(c.env), home, c.goos)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got.FW != c.fw || got.R != c.r {
			t.Errorf("%s: got %+v; want FW=%s R=%s", c.name, got, c.fw, c.r)
		}
	}
}

func TestSync(t *testing.T) {
	dirs := Dirs{FW: filepath.Join(t.TempDir(), "fw"), R: t.TempDir()}
	write := func(dir, name, body string) {
		t.Helper()
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(dirs.R, "cloud.yml", "server: https://r.example.edu\n")
	write(dirs.R, "cloud-token-localhost_8080", "r-token\n")
	write(dirs.R, "settings.yml", "not synced\n")

	got, err := dirs.Sync()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Copied) != 2 || len(got.Skipped) != 0 {
		t.Fatalf("first sync: %+v", got)
	}
	info, err := os.Stat(filepath.Join(dirs.FW, "cloud-token-localhost_8080"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 && os.PathSeparator == '/' {
		t.Errorf("token file mode %o; want 600", perm)
	}
	if _, err := os.Stat(filepath.Join(dirs.FW, "settings.yml")); !os.IsNotExist(err) {
		t.Error("settings.yml should not be synced")
	}

	// fw's own copy is kept on the next sync
	write(dirs.FW, "cloud.yml", "server: https://fw.example.edu\n")
	got, err = dirs.Sync()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Copied) != 0 || len(got.Skipped) != 2 {
		t.Fatalf("second sync: %+v", got)
	}
	if s := dirs.SavedServer(); s != "https://fw.example.edu" {
		t.Errorf("SavedServer = %q; want fw's copy", s)
	}
}
