package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseGitHubRemote(t *testing.T) {
	for url, want := range map[string]string{
		"https://github.com/Acme/study.git":              "Acme/study",
		"https://github.com/Acme/study":                  "Acme/study",
		"https://x-access-token@github.com/Acme/study/":  "Acme/study",
		"git@github.com:Acme/study.git":                  "Acme/study",
		"ssh://git@github.com/Acme/acme.github.io.git":   "Acme/acme.github.io",
		"https://gitlab.com/Acme/study.git":              "",
		"https://github.com/Acme":                        "",
		"https://github.com.evil.example/Acme/study.git": "",
	} {
		owner, repo, ok := parseGitHubRemote(url)
		got := ""
		if ok {
			got = owner + "/" + repo
		}
		if got != want {
			t.Errorf("%s: got %q, want %q", url, got, want)
		}
	}
	if got := pagesSiteURL("Acme", "study"); got != "https://acme.github.io/study/" {
		t.Errorf("project site: %s", got)
	}
	if got := pagesSiteURL("Acme", "acme.github.io"); got != "https://acme.github.io/" {
		t.Errorf("user site: %s", got)
	}
	for name, want := range map[string]string{"analysis.html": "analysis", "Final Report (v2).html": "final-report-v2", "_site": "site"} {
		if got := pagesSlug(name); got != want {
			t.Errorf("pagesSlug(%q) = %q, want %q", name, got, want)
		}
	}
}

// gitT runs git in dir and fails the test if it fails.
func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput() // #nosec G204 -- test fixtures
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// pagesRepo makes the linked project a git repository whose origin looks
// like GitHub but pushes to a local bare repository. It returns a function
// that runs git in the bare repository.
func pagesRepo(t *testing.T, root string) func(...string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	bare := filepath.Join(t.TempDir(), "remote.git")
	gitIn := func(dir string, args ...string) string { return gitT(t, dir, args...) }
	gitIn(filepath.Dir(bare), "init", "-q", "--bare", "-b", "main", bare)
	gitIn(root, "init", "-q", "-b", "main")
	gitIn(root, "config", "user.name", "Ada")
	gitIn(root, "config", "user.email", "ada@example.edu")
	gitIn(root, "config", "commit.gpgsign", "false")
	gitIn(root, "remote", "add", "origin", "https://github.com/Acme/study.git")
	gitIn(root, "config", "url."+filepath.ToSlash(bare)+".insteadOf", "https://github.com/Acme/study.git")
	write(t, root, ".gitignore", ".env\n")
	gitIn(root, "add", "-A")
	gitIn(root, "commit", "-q", "-m", "Start")
	gitIn(root, "push", "-q", "-u", "origin", "main")
	gitIn(root, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	return func(args ...string) string { return gitIn(bare, args...) }
}

func TestPublishToGitHub(t *testing.T) {
	srv := fakeCloud(t)
	h := newHarness(t, nil)
	root := linkedProject(t, h, srv.URL)
	write(t, root, "notebooks/analysis.html", `<html><img src="analysis_files/figure.png"></html>`)
	write(t, root, "notebooks/analysis_files/figure.png", "png")
	remote := pagesRepo(t, root)
	gitRoot := func(args ...string) string { return gitT(t, root, args...) }
	// Something the user staged stays out of fw's commit.
	write(t, root, "notes.txt", "draft")
	gitRoot("add", "notes.txt")

	// Not logged in: the URL comes from the remote and Pages is unknown.
	out, errOut, err := h.run(t, "", "publish", "notebooks/analysis.html", "--to", "github", "--yes", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	var res struct {
		URL          string  `json:"url"`
		Commit       *string `json:"commit"`
		Pushed       bool    `json:"pushed"`
		PagesEnabled *bool   `json:"pages_enabled"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err, out)
	}
	if res.URL != "https://acme.github.io/study/analysis/" || res.Commit == nil || !res.Pushed || res.PagesEnabled != nil {
		t.Errorf("result: %s", out)
	}
	if !strings.Contains(errOut, "public, even when the repository is private") {
		t.Errorf("no public note:\n%s", errOut)
	}
	if got := remote("show", "main:published/analysis/analysis_files/figure.png"); got != "png" {
		t.Errorf("pushed figure: %q", got)
	}
	if files := gitRoot("show", "--name-only", "--format=%s", "HEAD"); !strings.Contains(files, "published/analysis/v/1/artifact.json") || strings.Contains(files, "notes.txt") {
		t.Errorf("commit:\n%s", files)
	}
	if staged := gitRoot("diff", "--cached", "--name-only"); staged != "notes.txt" {
		t.Errorf("staged after publishing: %q", staged)
	}

	// Again with nothing new: no commit.
	h.app.jsonOut = false
	out, _, err = h.run(t, "", "publish", "notebooks/analysis.html", "--to", "github", "--yes")
	if err != nil || !strings.Contains(out, "No change") {
		t.Errorf("unchanged: %v\n%s", err, out)
	}

	// The folder is replaced whole, and a logged-in run asks the cloud.
	write(t, root, "published/analysis/stale.html", "old")
	write(t, root, "notebooks/analysis.html", `<html>new</html>`)
	if _, _, err := h.run(t, "user-tok\n", "login", "--url", srv.URL, "--with-token", "--yes"); err != nil {
		t.Fatal(err)
	}
	fakePagesURL = "https://pages.example/study"
	t.Cleanup(func() { fakePagesURL = "" })
	out, _, err = h.run(t, "", "publish", "notebooks/analysis.html", "--to", "github", "--yes", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"url": "https://pages.example/study/analysis/"`) || !strings.Contains(out, `"pages_enabled": true`) {
		t.Errorf("logged in: %s", out)
	}
	if files := remote("ls-tree", "-r", "--name-only", "main", "published/analysis"); strings.Contains(files, "published/analysis/analysis_files/figure.png") || !strings.Contains(files, "published/analysis/v/1/analysis_files/figure.png") || !strings.Contains(files, "published/analysis/v/2/artifact.json") {
		t.Errorf("replaced folder holds:\n%s", files)
	}

	// Pages off: warn and still push. Another branch: warn and still push.
	fakePagesURL = ""
	gitRoot("checkout", "-q", "-b", "draft")
	write(t, root, "notebooks/analysis.html", `<html>newer</html>`)
	h.app.jsonOut = false
	out, errOut, err = h.run(t, "", "publish", "notebooks/analysis.html", "--to", "github", "--yes")
	if err != nil || !strings.Contains(errOut, "GitHub Pages is off") || !strings.Contains(errOut, "deploys only from main") ||
		!strings.Contains(out, "Published analysis") {
		t.Errorf("draft branch with Pages off: %v\n%s\n%s", err, out, errOut)
	}
	if got := remote("show", "draft:published/analysis/index.html"); got != "<html>newer</html>" {
		t.Errorf("draft push: %q", got)
	}

	// Newer servers report pages_url in /me for the project token, so no
	// login is needed.
	if _, _, err := h.run(t, "", "logout", "--url", srv.URL); err != nil {
		t.Fatal(err)
	}
	var on any = "https://acme.github.io/study/"
	fakeMePages = &on
	t.Cleanup(func() { fakeMePages = nil })
	write(t, root, "notebooks/analysis.html", `<html>newest</html>`)
	h.app.jsonOut = false
	out, _, err = h.run(t, "", "publish", "notebooks/analysis.html", "--to", "github", "--yes", "--json")
	if err != nil || !strings.Contains(out, `"pages_enabled": true`) || !strings.Contains(out, `"url": "https://acme.github.io/study/analysis/"`) {
		t.Errorf("pages_url from /me: %v\n%s", err, out)
	}
	var off any
	fakeMePages = &off
	write(t, root, "notebooks/analysis.html", `<html>latest</html>`)
	out, _, err = h.run(t, "", "publish", "notebooks/analysis.html", "--to", "github", "--yes", "--json")
	if err != nil || !strings.Contains(out, `"pages_enabled": false`) {
		t.Errorf("pages_url null in /me: %v\n%s", err, out)
	}
	fakeMePages = nil

	// The usual checks still apply.
	write(t, root, "inputs/private/table.html", "<html></html>")
	if _, _, err := h.run(t, "", "publish", "inputs/private/table.html", "--to", "github", "--yes"); exitCode(err) != exitPrivate {
		t.Errorf("private path: %v", err)
	}
	for _, args := range [][]string{
		{"publish", "notebooks/analysis.html", "--to", "github", "--password", "x"},
		{"publish", "notebooks/analysis.html", "--to", "ftp"},
		{"publish", "notebooks/analysis.html", "--to", "github", "--slug", "../up"},
	} {
		if _, _, err := h.run(t, "", args...); exitCode(err) != exitUsage {
			t.Errorf("%v: exit %d %v", args, exitCode(err), err)
		}
	}
}

func TestPublishToGitHubNeedsGitHubRemote(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	srv := fakeCloud(t)
	h := newHarness(t, nil)
	root := linkedProject(t, h, srv.URL)
	write(t, root, "report.html", "<html></html>")
	if _, _, err := h.run(t, "", "publish", "report.html", "--to", "github", "--yes"); err == nil || !strings.Contains(err.Error(), "git repository") {
		t.Errorf("no repository: %v", err)
	}
	gitT(t, root, "init", "-q", "-b", "main")
	gitT(t, root, "remote", "add", "origin", "https://gitlab.com/acme/study.git")
	if _, _, err := h.run(t, "", "publish", "report.html", "--to", "github", "--yes"); err == nil || !strings.Contains(err.Error(), "Create the project on GitHub first") {
		t.Errorf("GitLab remote: %v", err)
	}
}

func TestPagesSiteAggregateSize(t *testing.T) {
	root := t.TempDir()
	write(t, root, "index.html", "index")
	write(t, root, "first/index.html", "latest")
	write(t, root, "first/v/1/index.html", "archive-one")
	write(t, root, "first/v/2/index.html", "archive-two")
	write(t, root, "second/index.html", "another-artifact")
	got, e := pagesSiteSize(root)
	want := int64(len("indexlatestarchive-onearchive-twoanother-artifact"))
	if e != nil || got != want {
		t.Fatal(got, want, e)
	}
	outside := filepath.Join(t.TempDir(), "outside")
	if e := os.Symlink(outside, filepath.Join(root, "escape")); e == nil {
		if _, e = pagesSiteSize(root); e == nil {
			t.Fatal("site symlink was accepted")
		}
	}
}
func TestPagesSiteSizeLimits(t *testing.T) {
	// Mock aggregate sizes exercise exact boundaries without large files.
	for _, test := range []struct {
		size          int64
		warn, blocked bool
	}{{799_999_999, false, false}, {800_000_000, true, false}, {999_999_999, true, false}, {1_000_000_000, true, false}, {1_000_000_001, false, true}} {
		var out bytes.Buffer
		e := pagesSiteSizePolicy(test.size, &out)
		if (e != nil) != test.blocked || strings.Contains(out.String(), "Warning:") != test.warn {
			t.Fatalf("size %d: err=%v, stderr=%q", test.size, e, out.String())
		}
		if e != nil && !strings.Contains(e.Error(), "nothing was committed or pushed") {
			t.Fatal(e)
		}
	}
}

// R and Python projects ignore *_files/. fw must still commit a Quarto
// page's _files folder, and only the files it wrote.
func TestPublishToGitHubCommitsIgnoredFiles(t *testing.T) {
	srv := fakeCloud(t)
	h := newHarness(t, nil)
	root := linkedProject(t, h, srv.URL)
	write(t, root, "notebooks/report.html", `<html><link href="report_files/site.css"><img src="report_files/fig.png"></html>`)
	write(t, root, "notebooks/report_files/fig.png", "png")
	write(t, root, "notebooks/report_files/site.css", "css")
	remote := pagesRepo(t, root)
	write(t, root, ".gitignore", ".env\n*_files/\n.DS_Store\n")
	gitT(t, root, "add", ".gitignore")
	gitT(t, root, "commit", "-q", "-m", "Ignore")
	// An ignored file fw did not write stays out.
	write(t, root, "published/report/.DS_Store", "junk")

	if _, errOut, err := h.run(t, "", "publish", "notebooks/report.html", "--to", "github", "--yes"); err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	files := remote("ls-tree", "-r", "--name-only", "main", "published")
	for _, want := range []string{
		"published/report/index.html", "published/report/report_files/fig.png", "published/report/report_files/site.css",
		"published/report/v/1/report_files/fig.png", "published/report/artifact.json", "published/index.json",
	} {
		if !strings.Contains(files, want) {
			t.Errorf("pushed tree lacks %s:\n%s", want, files)
		}
	}
	if strings.Contains(files, ".DS_Store") {
		t.Errorf("committed a file fw did not write:\n%s", files)
	}

	g := gitRepo{dir: root}
	missing, err := g.notStaged([]string{"published/report/index.html", "published/report/[x].html"})
	if err != nil || len(missing) != 1 || missing[0] != "published/report/[x].html" {
		t.Errorf("notStaged: %v %v", missing, err)
	}
}
