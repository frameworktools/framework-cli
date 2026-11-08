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
	added, leftOut, err := AppendGitignore(dir, "Added by test", []string{"# comment", ".env", "", "**/private/**", "*.csv", "**/private/**", "!.env.example", "/logs/", "!keep.csv"})
	if err != nil || strings.Join(added, ",") != "**/private/**,/logs/" || strings.Join(leftOut, ",") != "!.env.example" {
		t.Fatalf("%v %v %v", added, leftOut, err)
	}
	data, _ := os.ReadFile(path) // #nosec G304 -- test path
	if string(data) != "*.csv\n  .env\n!keep.csv\n\n# Added by test\n**/private/**\n/logs/\n" {
		t.Errorf("got %q", data)
	}
	// A second run adds nothing and leaves the file as it is.
	if added, _, err := AppendGitignore(dir, "Added by test", []string{".env", "/logs/"}); err != nil || len(added) != 0 {
		t.Errorf("again: %v %v", added, err)
	}
	if again, _ := os.ReadFile(path); string(again) != string(data) { // #nosec G304 -- test path
		t.Error("changed the file")
	}
	// No .gitignore yet, or one with only comments: it gets the rules
	// whole, negations included.
	fresh := t.TempDir()
	if added, leftOut, err := AppendGitignore(fresh, "Added", []string{".env", ".env.*", "!.env.example"}); err != nil || len(added) != 3 || len(leftOut) != 0 {
		t.Errorf("fresh: %v %v %v", added, leftOut, err)
	}
	comments := t.TempDir()
	if err := os.WriteFile(filepath.Join(comments, ".gitignore"), []byte("# nothing yet\n\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if added, leftOut, err := AppendGitignore(comments, "Added", []string{".env", "!.env.example"}); err != nil || len(added) != 2 || len(leftOut) != 0 {
		t.Errorf("comments only: %v %v %v", added, leftOut, err)
	}
}
