package skillsync

import (
	"os"
	"path/filepath"
	"testing"
)

func check(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Error(err)
	}
}

func writeRel(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	check(t, os.MkdirAll(filepath.Dir(p), 0755))
	check(t, os.WriteFile(p, []byte(body), 0644))
}

func readRel(t *testing.T, root, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func skillMD(name, body string) string { return "---\nname: " + name + "\n---\n" + body + "\n" }

// entriesIn lists a folder's entries, or nothing when it doesn't exist.
func entriesIn(t *testing.T, root, rel string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}
