package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestLockMatchesCopies is the drift check CI runs with go test: every file
// copied from the R package must match its assets.lock.json entry.
func TestLockMatchesCopies(t *testing.T) {
	problems, lock, err := check(filepath.Join("..", ".."), "")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range problems {
		t.Error(p)
	}
	if len(problems) > 0 {
		t.Log("Change shared files in the R repository, then run: go run ./tools/syncassets --from ../framework --ref <tag or commit>")
	}
	if lock.Ref == worktree {
		t.Errorf("the copies came from an uncommitted R working tree; copy them again from a pushed commit or tag")
	}
}

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t.invalid"}, args...)...) // #nosec G204 -- fixed test arguments
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestSyncAndCheck(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repo, root := t.TempDir(), t.TempDir()
	write(t, repo, "inst/schema/a.v1.json", `{"a":1}`)
	write(t, repo, "inst/conformance/README.md", "fixtures\n")
	write(t, repo, "inst/conformance/merge/case.json", `{}`)
	write(t, repo, "inst/other.txt", "not shared")
	gitRun(t, repo, "init", "-q")
	gitRun(t, repo, "add", ".")
	gitRun(t, repo, "commit", "-q", "-m", "one")

	n, err := sync(root, repo, "HEAD")
	if err != nil || n != 3 {
		t.Fatalf("sync: %d %v", n, err)
	}
	if problems, lock, err := check(root, repo); err != nil || len(problems) != 0 || len(lock.Commit) != 40 {
		t.Fatalf("clean check: %v %v %+v", problems, err, lock)
	}

	// A local edit, an extra file, and a change upstream are all reported.
	write(t, root, "internal/spec/schema/a.v1.json", `{"a":2}`)
	write(t, root, "internal/spec/schema/extra.json", `{}`)
	write(t, repo, "inst/schema/b.v1.json", `{}`)
	gitRun(t, repo, "add", ".")
	gitRun(t, repo, "commit", "-q", "-m", "two")
	gitRun(t, repo, "tag", "v2")
	problems, _, err := check(root, repo)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(problems, "\n")
	for _, want := range []string{"edited locally: internal/spec/schema/a.v1.json", "not in assets.lock.json: internal/spec/schema/extra.json"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	// The check compares against the locked commit, so a newer upstream file
	// is not reported until the copies move to it.
	if strings.Contains(got, "b.v1.json") {
		t.Errorf("reported a file newer than the locked commit:\n%s", got)
	}

	// Copying again at a tag repairs the edit and picks up the new file.
	if err := os.Remove(filepath.Join(root, "internal/spec/schema/extra.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := sync(root, repo, "v2"); err != nil {
		t.Fatal(err)
	}
	if problems, lock, err := check(root, repo); err != nil || len(problems) != 0 || lock.Ref != "v2" || len(lock.Files) != 4 {
		t.Fatalf("after re-sync: %v %v %+v", problems, err, lock)
	}

	// A file R removed is deleted from the copies on the next sync.
	gitRun(t, repo, "rm", "-q", "inst/schema/b.v1.json")
	gitRun(t, repo, "commit", "-q", "-m", "three")
	if _, err := sync(root, repo, "HEAD"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "internal/spec/schema/b.v1.json")); !os.IsNotExist(err) {
		t.Errorf("b.v1.json was not removed: %v", err)
	}
	if problems, _, err := check(root, repo); err != nil || len(problems) != 0 {
		t.Fatalf("after removal: %v %v", problems, err)
	}
}
