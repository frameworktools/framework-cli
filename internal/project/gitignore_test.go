package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppendGitignore(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".gitignore")
	if err := os.WriteFile(path, []byte("*.csv\n  .env\n!keep.csv"), 0o600); err != nil {
		t.Fatal(err)
	}
	added, err := AppendGitignore(dir, "Added by test", []string{"# comment", ".env", "", "**/private/**", "*.csv", "**/private/**", "/logs/"})
	if err != nil || strings.Join(added, ",") != "**/private/**,/logs/" {
		t.Fatalf("%v %v", added, err)
	}
	data, _ := os.ReadFile(path) // #nosec G304 -- test path
	if string(data) != "*.csv\n  .env\n!keep.csv\n\n# Added by test\n**/private/**\n/logs/\n" {
		t.Errorf("got %q", data)
	}
	// A second run adds nothing and leaves the file as it is.
	if added, err := AppendGitignore(dir, "Added by test", []string{".env", "/logs/"}); err != nil || len(added) != 0 {
		t.Errorf("again: %v %v", added, err)
	}
	if again, _ := os.ReadFile(path); string(again) != string(data) { // #nosec G304 -- test path
		t.Error("changed the file")
	}
	// No .gitignore yet: one is made.
	fresh := t.TempDir()
	if added, err := AppendGitignore(fresh, "Added", []string{".env"}); err != nil || len(added) != 1 {
		t.Errorf("fresh: %v %v", added, err)
	}
}
