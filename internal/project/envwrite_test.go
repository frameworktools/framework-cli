package project

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestLinkCloud(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("FW_CLOUD_URL=https://kept.example.edu\nOTHER=1"), 0o600); err != nil {
		t.Fatal(err)
	}
	added, err := LinkCloud(dir, "fw_tok", "https://new.example.edu")
	if err != nil || len(added) != 1 || added[0] != "FW_PROJECT_TOKEN" {
		t.Fatalf("added %v, %v", added, err)
	}
	if got := EnvValue(dir, "FW_CLOUD_URL"); got != "https://kept.example.edu" {
		t.Errorf("FW_CLOUD_URL = %q; an existing value must be kept", got)
	}
	if got := EnvValue(dir, "FW_PROJECT_TOKEN"); got != "fw_tok" {
		t.Errorf("FW_PROJECT_TOKEN = %q", got)
	}
	if got := EnvValue(dir, "OTHER"); got != "1" {
		t.Errorf("OTHER = %q", got)
	}
}

func TestGitignoresEnv(t *testing.T) {
	dir := t.TempDir()
	if GitignoresEnv(dir) {
		t.Error("no .gitignore: want false")
	}
	_ = os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("*.dta\n"), 0o600)
	if GitignoresEnv(dir) {
		t.Error("want false without a .env line")
	}
	_ = os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("*.dta\n.env\n"), 0o600)
	if !GitignoresEnv(dir) {
		t.Error("want true")
	}
}

func TestEnvTracked(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...) // #nosec G204 -- test-only git calls
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if EnvTracked(dir) {
		t.Error("not a repository: want false")
	}
	git("init", "-q")
	_ = os.WriteFile(filepath.Join(dir, ".env"), []byte("A=1\n"), 0o600)
	if EnvTracked(dir) {
		t.Error("untracked: want false")
	}
	git("add", ".env")
	if !EnvTracked(dir) {
		t.Error("staged: want true")
	}
}

func TestGitignoresEnvNegation(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(".env\n!.env\n"), 0o600)
	if GitignoresEnv(dir) {
		t.Error("the last .env rule un-ignores it: want false")
	}
}

func TestIgnoreEnv(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("*.dta"), 0o600)
	changed, err := IgnoreEnv(dir)
	if err != nil || !changed {
		t.Fatalf("%v %v", changed, err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, ".gitignore")) // #nosec G304 -- test path
	if string(data) != "*.dta\n\n# Secrets (added by Framework)\n.env\n" {
		t.Errorf(".gitignore = %q", data)
	}
	if changed, _ := IgnoreEnv(dir); changed {
		t.Error("a second call should change nothing")
	}

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repo := t.TempDir()
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	if changed, err := IgnoreEnv(repo); err != nil || !changed {
		t.Fatalf("repository without .gitignore: %v %v", changed, err)
	}
	if !GitignoresEnv(repo) {
		t.Error("git should now ignore .env")
	}
}
