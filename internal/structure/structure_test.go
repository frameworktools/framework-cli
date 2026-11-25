package structure

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// fixture builds the shared project every client tests against.
func fixture(t *testing.T, withGit bool) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "my-project")
	files := map[string]string{
		".gitignore":           "data/raw/\n*.rds\n.env\n",
		".env":                 "SECRET=1\n",
		"README.md":            "",
		"R/clean.R":            "",
		"data/raw/a.csv":       "",
		"data/raw/b.csv":       "",
		"data/private/ids.csv": "",
		"outputs/fit.rds":      "",
		"outputs/draws.rds":    "",
		"outputs/table.csv":    "",
	}
	for rel, content := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if withGit {
		if _, err := exec.LookPath("git"); err != nil {
			t.Skip("git is not installed")
		}
		cmd := exec.Command("git", "init", "-q")
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git init: %v %s", err, out)
		}
	}
	return root
}

func nodeLines(doc *Document) []string {
	var out []string
	for _, n := range doc.Nodes {
		s := n.Path + " " + n.Type
		if n.Ignored != nil {
			s += " " + n.Ignored.Rule + "@" + n.Ignored.Source + ":" + itoa(n.Ignored.Line)
		}
		if n.Private {
			s += " private"
		}
		if n.Depth {
			s += " depth"
		}
		if n.Hidden != nil {
			s += " hidden=" + itoa(*n.Hidden)
		}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func itoa(n int) string { return thousandsless(n) }

func thousandsless(n int) string { return strings.ReplaceAll(thousands(n), ",", "") }

var fixtureNodes = []string{
	" ignored .env@.gitignore:3 hidden=1",
	".gitignore file",
	"R dir",
	"R/clean.R file",
	"README.md file",
	"data dir",
	"data/private dir private hidden=1",
	"data/raw dir data/raw/@.gitignore:1 hidden=2",
	"outputs dir",
	"outputs ignored *.rds@.gitignore:2 hidden=2",
	"outputs/table.csv file",
}

func checkFixture(t *testing.T, doc *Document, method string) {
	t.Helper()
	got := nodeLines(doc)
	want := append([]string(nil), fixtureNodes...)
	sort.Strings(want)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("nodes:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if doc.Method != method || doc.Root != "my-project" || doc.Base != "" || doc.Kind != "tree" || doc.Schema != "framework.artifact/v1" {
		t.Errorf("document: %+v", doc)
	}
	data, _ := json.Marshal(doc)
	for _, name := range []string{"a.csv", "b.csv", "ids.csv", "fit.rds", "draws.rds"} {
		if bytes.Contains(data, []byte(name)) {
			t.Errorf("the upload names %s: %s", name, data)
		}
	}
}

func TestSharedFixtureGit(t *testing.T) {
	root := fixture(t, true)
	tree, err := Build(Options{ProjectRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := tree.Document("shell", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	checkFixture(t, doc, "git")
}

func TestSharedFixtureGitignore(t *testing.T) {
	root := fixture(t, false)
	// No work tree: git, if there is one, says so, and fw reads .gitignore.
	tree, err := Build(Options{ProjectRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := tree.Document("shell", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	checkFixture(t, doc, "gitignore")
}

func TestSharedFixtureWithoutGit(t *testing.T) {
	saved := gitCommand
	gitCommand = func(string, []byte, ...string) ([]byte, error) { return nil, errors.New("git: not found") }
	t.Cleanup(func() { gitCommand = saved })
	root := fixture(t, true)
	tree, err := Build(Options{ProjectRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := tree.Document("shell", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	checkFixture(t, doc, "gitignore")
}

func TestNoGitignore(t *testing.T) {
	root := fixture(t, false)
	if err := os.Remove(filepath.Join(root, ".gitignore")); err != nil {
		t.Fatal(err)
	}
	tree, err := Build(Options{ProjectRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	doc, _ := tree.Document("shell", nil, nil)
	if doc.Method != "none" {
		t.Errorf("method %q", doc.Method)
	}
	// The private folder stays a stub with nothing ignored.
	got := strings.Join(nodeLines(doc), "\n")
	if !strings.Contains(got, "data/private dir private hidden=1") || strings.Contains(got, "ids.csv") || !strings.Contains(got, "outputs/fit.rds file") {
		t.Errorf("nodes:\n%s", got)
	}
}

func TestHideIgnored(t *testing.T) {
	root := fixture(t, true)
	if err := os.MkdirAll(filepath.Join(root, "cache_private"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("data/raw/\n*.rds\n.env\ncache_private/\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tree, err := Build(Options{ProjectRoot: root, HideIgnored: true})
	if err != nil {
		t.Fatal(err)
	}
	doc, _ := tree.Document("shell", nil, nil)
	got := strings.Join(nodeLines(doc), "\n")
	if strings.Contains(got, "ignored") || strings.Contains(got, "data/raw") || strings.Contains(got, "cache_private") || !strings.Contains(got, "data/private dir private hidden=1") {
		t.Errorf("nodes:\n%s", got)
	}
}

func TestDepthExcludeAndSettingsPrivate(t *testing.T) {
	root := fixture(t, true)
	for _, rel := range []string{"renv/library/x/a", "renv/library/x/b", "analysis/models/fit.R", "analysis/models/deep/more.R", "secure/keys.txt", "notes.log"} {
		p := filepath.Join(root, filepath.FromSlash(rel))
		_ = os.MkdirAll(filepath.Dir(p), 0o750)
		_ = os.WriteFile(p, nil, 0o600)
	}
	tree, err := Build(Options{ProjectRoot: root, Depth: 2, Exclude: []string{"*.log", "renv"}, PrivateDirs: []string{"secure/"}})
	if err != nil {
		t.Fatal(err)
	}
	doc, _ := tree.Document("shell", nil, nil)
	got := strings.Join(nodeLines(doc), "\n")
	for _, want := range []string{"analysis/models dir depth hidden=2", "secure dir private hidden=1"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	for _, bad := range []string{"renv", "notes.log", "keys.txt", "fit.R"} {
		if strings.Contains(got, bad) {
			t.Errorf("%q in:\n%s", bad, got)
		}
	}
}

func TestSubfolderAndSeveralFolders(t *testing.T) {
	root := fixture(t, true)
	tree, err := Build(Options{ProjectRoot: root, Paths: []string{"outputs"}})
	if err != nil {
		t.Fatal(err)
	}
	doc, _ := tree.Document("shell", nil, nil)
	if doc.Root != "outputs" || doc.Base != "outputs" || tree.Slug(func(s string) string { return s }) != "structure-outputs" || tree.Title() != "outputs structure" {
		t.Errorf("root %q base %q", doc.Root, doc.Base)
	}
	if got := strings.Join(nodeLines(doc), "\n"); got != " ignored *.rds@.gitignore:2 hidden=2\ntable.csv file" {
		t.Errorf("nodes:\n%s", got)
	}

	tree, err = Build(Options{ProjectRoot: root, Paths: []string{"R", "data"}})
	if err != nil {
		t.Fatal(err)
	}
	doc, _ = tree.Document("shell", nil, nil)
	want := "R dir\nR/clean.R file\ndata dir\ndata/private dir private hidden=1\ndata/raw dir data/raw/@.gitignore:1 hidden=2"
	if doc.Root != "my-project" || doc.Base != "" || strings.Join(nodeLines(doc), "\n") != want {
		t.Errorf("root %q base %q nodes:\n%s", doc.Root, doc.Base, strings.Join(nodeLines(doc), "\n"))
	}

	// An ignored or private folder never publishes its contents.
	if _, err := Build(Options{ProjectRoot: root, Paths: []string{"data/raw"}}); err == nil || !strings.Contains(err.Error(), "ignored") {
		t.Errorf("ignored folder: %v", err)
	}
	if _, err := Build(Options{ProjectRoot: root, Paths: []string{"data/private"}}); err == nil || !strings.Contains(err.Error(), "private") {
		t.Errorf("private folder: %v", err)
	}
	if _, err := Build(Options{ProjectRoot: root, Paths: []string{"data/private"}, Local: true}); err != nil {
		t.Errorf("local preview of a private folder: %v", err)
	}
	if _, err := Build(Options{ProjectRoot: root, Paths: []string{"../x"}}); err == nil {
		t.Error("a path outside the project was accepted")
	}
}

func TestNotesOpenHighlight(t *testing.T) {
	root := fixture(t, true)
	tree, err := Build(Options{ProjectRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	if err := tree.SetNotes(map[string]string{"R/": "Reusable  functions", "README.md": "Start here", "data/raw": "Raw extracts"}); err != nil {
		t.Fatal(err)
	}
	if err := tree.SetNotes(map[string]string{"data/raw/a.csv": "x"}); err == nil {
		t.Error("a note inside a stub was accepted")
	}
	if err := tree.SetNotes(map[string]string{"README.md": strings.Repeat("x", 501)}); err == nil {
		t.Error("a long note was accepted")
	}
	doc, err := tree.Document("shell", []string{"R/"}, []string{"README.md"})
	if err != nil {
		t.Fatal(err)
	}
	notes := map[string]string{}
	for _, n := range doc.Nodes {
		if n.Note != "" {
			notes[n.Path] = n.Note
		}
	}
	if notes["R"] != "Reusable functions" || notes["README.md"] != "Start here" || notes["data/raw"] != "Raw extracts" {
		t.Errorf("notes: %v", notes)
	}
	if strings.Join(doc.Open, ",") != "R" || strings.Join(doc.Highlight, ",") != "README.md" {
		t.Errorf("open %v highlight %v", doc.Open, doc.Highlight)
	}
	if _, err := tree.Document("shell", []string{"README.md"}, nil); err == nil {
		t.Error("open on a file was accepted")
	}
	if _, err := tree.Document("shell", nil, []string{"nope"}); err == nil {
		t.Error("highlight on a missing path was accepted")
	}
}

func TestRender(t *testing.T) {
	root := fixture(t, true)
	tree, err := Build(Options{ProjectRoot: root, Local: true})
	if err != nil {
		t.Fatal(err)
	}
	_ = tree.SetNotes(map[string]string{"R": "Reusable functions", "README.md": "Start here"})
	var local, published bytes.Buffer
	tree.Render(&local, true)
	tree.Render(&published, false)
	wantLocal := `my-project/
├── data/
│   ├── private/   private · 1 file
│   └── raw/       ignored by data/raw/ (.gitignore:1) · 2 files
├── outputs/
│   ├── table.csv
│   ├── draws.rds  ignored by *.rds (.gitignore:2)
│   └── fit.rds    ignored by *.rds (.gitignore:2)
├── R/             Reusable functions
│   └── clean.R
├── .gitignore
├── README.md      Start here
└── .env           ignored by .env (.gitignore:3)
`
	if local.String() != wantLocal {
		t.Errorf("local:\n%s\nwant:\n%s", local.String(), wantLocal)
	}
	if !strings.Contains(published.String(), "│   └── *.rds     ignored (.gitignore:2) · 2 files\n") || strings.Contains(published.String(), "fit.rds") {
		t.Errorf("published:\n%s", published.String())
	}
}

func TestPreviewListsTwentyIgnoredFiles(t *testing.T) {
	root := fixture(t, true)
	for i := range 25 {
		_ = os.WriteFile(filepath.Join(root, "outputs", "m"+thousandsless(100+i)+".rds"), nil, 0o600)
	}
	tree, err := Build(Options{ProjectRoot: root, Local: true})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	tree.Render(&out, true)
	if n := strings.Count(out.String(), "ignored by *.rds"); n != MaxListed+1 {
		t.Errorf("%d rows for *.rds:\n%s", n, out.String())
	}
	if !strings.Contains(out.String(), "… and 7 files") {
		t.Errorf("no overflow row:\n%s", out.String())
	}
}

func TestSymlinkIsAFile(t *testing.T) {
	root := fixture(t, true)
	if err := os.Symlink(filepath.Join(root, "data"), filepath.Join(root, "link")); err != nil {
		t.Skip(err)
	}
	tree, err := Build(Options{ProjectRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	doc, _ := tree.Document("shell", nil, nil)
	if got := strings.Join(nodeLines(doc), "\n"); !strings.Contains(got, "link file") || strings.Contains(got, "link/") {
		t.Errorf("nodes:\n%s", got)
	}
}

func TestRuleSourceNeverAbsolute(t *testing.T) {
	top := "/w/repo"
	gitDir := "/w/repo/.git"
	cases := map[string]string{
		".gitignore":            ".gitignore",
		"sub/.gitignore":        "sub/.gitignore",
		".git/info/exclude":     "info/exclude",
		"/home/ada/.config/git": "global",
	}
	for in, want := range cases {
		if got := ruleSource(in, top, gitDir, "/w/repo", "/w/repo"); got != want {
			t.Errorf("%s: %q, want %q", in, got, want)
		}
	}
	if got := ruleSource(".gitignore", top, gitDir, "/w/repo/sub", "/w/repo"); got != ".gitignore" {
		t.Errorf("parent .gitignore: %q", got)
	}
}

func TestPatterns(t *testing.T) {
	ps := parsePatterns("# comment\n\n*.rds\n!keep.rds\n/build/\ndocs/**/tmp\nlogs/**\n?.txt\n[ab].csv\n")
	cases := []struct {
		path  string
		dir   bool
		match bool
	}{
		{"x.rds", false, true},
		{"a/b/x.rds", false, true},
		{"keep.rds", false, false},
		{"build", true, true},
		{"build", false, false},
		{"src/build", true, false},
		{"docs/tmp", false, true},
		{"docs/a/b/tmp", false, true},
		{"logs", true, false},
		{"logs/x", false, true},
		{"a.txt", false, true},
		{"ab.txt", false, false},
		{"b.csv", false, true},
		{"c.csv", false, false},
	}
	for _, c := range cases {
		if got := match(ps, c.path, c.dir) != nil; got != c.match {
			t.Errorf("%s (dir %v): %v, want %v", c.path, c.dir, got, c.match)
		}
	}
}

func TestCount(t *testing.T) {
	for n, want := range map[int]string{1: "1 file", 12: "12 files", 1520: "1,520 files", 10000: "10,000+ files", 0: "0 files"} {
		if got := Count(n); got != want {
			t.Errorf("Count(%d) = %q", n, got)
		}
	}
}
