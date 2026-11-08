package cli

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/frameworktools/framework-cli/internal/cloud"
	"github.com/frameworktools/framework-cli/internal/skillsync"
)

func TestExitCode(t *testing.T) {
	cases := []struct {
		err  error
		want int
	}{
		{nil, exitOK},
		{errors.New("boom"), exitFailure},
		{&errNotLoggedIn{"x"}, exitAuth},
		{fmt.Errorf("wrapped: %w", &cloud.APIError{Status: 401}), exitAuth},
		{&cloud.APIError{Status: 403}, exitAuth},
		{&cloud.APIError{Status: 404}, exitNotFound},
		{&cloud.APIError{Status: 422}, exitFailure},
		{cloud.ErrDeviceDenied, exitLogin},
		{cloud.ErrDeviceExpired, exitLogin},
		{usageError("x"), exitUsage},
		{errors.New(`unknown command "nope" for "fw"`), exitUsage},
	}
	for _, c := range cases {
		if got := exitCode(c.err); got != c.want {
			t.Errorf("exitCode(%v) = %d; want %d", c.err, got, c.want)
		}
	}
}

func TestReportJSON(t *testing.T) {
	var buf bytes.Buffer
	report(&buf, &app{jsonOut: true}, &cloud.APIError{Status: 404, Message: "You have no project named 'x'."})
	var got jsonError
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil || got.Code != exitNotFound || !strings.Contains(got.Error, "no project named") {
		t.Errorf("report: %q %v", buf.String(), err)
	}
	buf.Reset()
	report(&buf, &app{jsonOut: true}, &exitError{code: exitPrivate, reason: "private_path", err: errors.New("private")})
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil || got.Code != exitPrivate || got.Reason != "private_path" {
		t.Errorf("report with a reason: %q %v", buf.String(), err)
	}
	buf.Reset()
	report(&buf, &app{}, errors.New("boom"))
	if buf.String() != "fw: boom\n" {
		t.Errorf("text report: %q", buf.String())
	}
}

func TestUsageErrorsExit2(t *testing.T) {
	h := newHarness(t, nil)
	for _, args := range [][]string{{"status", "--nope"}, {"project", "show"}, {"status", "extra"}} {
		_, _, err := h.run(t, "", args...)
		if exitCode(err) != exitUsage {
			t.Errorf("%v: exit %d (%v); want %d", args, exitCode(err), err, exitUsage)
		}
	}
}

func decode(t *testing.T, out string, v any) {
	t.Helper()
	if err := json.Unmarshal([]byte(out), v); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
}

func TestStatusJSON(t *testing.T) {
	srv := fakeCloud(t)
	h := newHarness(t, nil)

	out, _, err := h.run(t, "", "status", "--url", srv.URL, "--json")
	var st statusJSON
	decode(t, out, &st)
	if exitCode(err) != exitAuth || st.LoggedIn || st.Server != srv.URL || !strings.Contains(st.Problem, "not logged in") {
		t.Errorf("logged out: exit %d %+v", exitCode(err), st)
	}

	if _, _, err := h.run(t, "user-tok\n", "login", "--url", srv.URL, "--with-token", "--yes"); err != nil {
		t.Fatal(err)
	}
	h.app.jsonOut = false
	out, _, err = h.run(t, "", "status", "--json")
	st = statusJSON{}
	decode(t, out, &st)
	if err != nil || !st.LoggedIn || st.Account == nil || st.Account.Email != "ada@example.edu" || st.TokenSource == "" {
		t.Errorf("logged in: %v %+v", err, st)
	}
	if strings.Contains(out, "user-tok") {
		t.Error("status --json printed the token")
	}

	// A rejected token exits 3 too.
	if err := h.mem.Set("framework-cloud:"+strings.TrimPrefix(srv.URL, "http://"), "token", "bad-tok"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.run(t, "", "status"); exitCode(err) != exitAuth {
		t.Errorf("rejected token: exit %d", exitCode(err))
	}
}

func loggedIn(t *testing.T) (*harness, string) {
	t.Helper()
	srv := fakeCloud(t)
	h := newHarness(t, nil)
	if _, _, err := h.run(t, "user-tok\n", "login", "--url", srv.URL, "--with-token", "--yes"); err != nil {
		t.Fatal(err)
	}
	return h, srv.URL
}

func TestCloudJSON(t *testing.T) {
	h, url := loggedIn(t)

	out, _, err := h.run(t, "", "projects", "list", "--json")
	var list struct {
		Server   string        `json:"server"`
		Projects []projectJSON `json:"projects"`
	}
	decode(t, out, &list)
	if err != nil || list.Server != url || len(list.Projects) != 1 || list.Projects[0].Slug != "vaccine-study" || list.Projects[0].Type != "project_sensitive" {
		t.Errorf("projects list: %v %+v", err, list)
	}

	out, _, err = h.run(t, "", "project", "show", "vaccine-study", "--json")
	var p cloud.ProjectDetail
	decode(t, out, &p)
	if err != nil || p.Language != "stata" || p.Blueprint == nil || p.Blueprint.Key != "framework/project_sensitive" {
		t.Errorf("project show: %v %+v", err, p)
	}
	out, _, err = h.run(t, "", "project", "show", "vaccine-study")
	if err != nil || !strings.Contains(out, "framework/project_sensitive v2") || !strings.Contains(out, "fw new --from vaccine-study") {
		t.Errorf("project show text: %v\n%s", err, out)
	}
	if _, _, err := h.run(t, "", "project", "show", "nope"); exitCode(err) != exitNotFound || !strings.Contains(err.Error(), "no project named 'nope'") {
		t.Errorf("missing project: exit %d %v", exitCode(err), err)
	}

	out, _, err = h.run(t, "", "blueprints", "list", "--json")
	var bp struct {
		Blueprints []cloud.Blueprint `json:"blueprints"`
	}
	decode(t, out, &bp)
	if err != nil || len(bp.Blueprints) != 1 || bp.Blueprints[0].Key != "framework/project" || bp.Blueprints[0].Version != 3 {
		t.Errorf("blueprints: %v %+v", err, bp)
	}
	h.app.jsonOut = false
	if out, _, err := h.run(t, "", "blueprints", "list"); err != nil || !strings.Contains(out, "framework/project") {
		t.Errorf("blueprints text: %v\n%s", err, out)
	}
}

func TestNotLoggedInExit3(t *testing.T) {
	srv := fakeCloud(t)
	h := newHarness(t, nil)
	for _, args := range [][]string{{"projects", "list"}, {"project", "show", "x"}, {"blueprints", "list"}} {
		if _, _, err := h.run(t, "", append(args, "--url", srv.URL)...); exitCode(err) != exitAuth {
			t.Errorf("%v: exit %d %v", args, exitCode(err), err)
		}
	}
}

func TestLoginWithoutTerminal(t *testing.T) {
	srv := fakeCloud(t)
	h := newHarness(t, nil) // interactive is false, as for an agent
	opened := false
	h.app.openURL = func(string) error { opened = true; return nil }

	if _, _, err := h.run(t, "", "login", "--url", srv.URL); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Errorf("without --yes: %v", err)
	}
	out, errOut, err := h.run(t, "", "login", "--url", srv.URL, "--yes", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var code struct {
		UserCode        string `json:"user_code"`
		VerificationURL string `json:"verification_url"`
	}
	line := errOut[strings.LastIndex(strings.TrimRight(errOut, "\n"), "\n")+1:]
	decode(t, line, &code)
	if code.UserCode != "ABCD-EFGH" || !strings.Contains(code.VerificationURL, "ABCD-EFGH") {
		t.Errorf("device code on stderr: %q", errOut)
	}
	var st statusJSON
	decode(t, out, &st)
	if !st.LoggedIn || st.Account.Email != "ada@example.edu" || strings.Contains(out+errOut, "user-tok") {
		t.Errorf("login --json: %s", out)
	}
	if opened {
		t.Error("opened a browser without a terminal")
	}
	if _, _, err := h.run(t, "", "login", "--url", srv.URL, "--yes", "--paste"); exitCode(err) != exitUsage {
		t.Errorf("--paste without terminal: exit %d %v", exitCode(err), err)
	}
}

// linkedProject makes a project folder linked to url with the fake cloud's
// project token, and runs fw from it.
func linkedProject(t *testing.T, h *harness, url string) string {
	t.Helper()
	return linkedProjectAt(t, h, url, "study")
}

func linkedProjectAt(t *testing.T, h *harness, url, name string) string {
	t.Helper()
	// t.TempDir() names the folder after the test, so a test named
	// TestXPrivate would warn on every upload. Use a neutral folder then.
	base := h.app.home
	if isPrivatePath(base) {
		dir, err := os.MkdirTemp("", "fwtest-")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(dir) })
		if isPrivatePath(dir) {
			t.Skip("the temporary folder's path contains \"private\", so every upload would warn")
		}
		base = dir
	}
	root := filepath.Join(base, name)
	write(t, root, "settings.yml", "default: {}\n")
	write(t, root, ".env", "FW_PROJECT_TOKEN=proj-tok\nFW_CLOUD_URL="+url+"\n")
	h.app.cwd = root
	return root
}

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func zipNames(t *testing.T, data string) []string {
	t.Helper()
	zr, err := zip.NewReader(strings.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	sort.Strings(names)
	return names
}

func TestPublish(t *testing.T) {
	srv := fakeCloud(t)
	h := newHarness(t, nil)
	root := linkedProject(t, h, srv.URL)
	write(t, root, "outputs/report.html", "<html>report</html>")
	write(t, root, "notebooks/analysis.html", `<html><img src="analysis_files/figure.png"></html>`)
	write(t, root, "notebooks/analysis_files/figure.png", "png")
	write(t, root, "notebooks/split.html", `<html><link href="demo_files/style.css"></html>`)
	write(t, root, "notebooks/demo_files/style.css", "css")
	write(t, root, "notebooks/unused_files/x.css", "css")
	write(t, root, "_site/index.html", "<html>site</html>")
	write(t, root, "_site/about/index.html", "<html>about</html>")
	write(t, root, "inputs/private/table.html", "<html>secret</html>")
	write(t, root, "outputs/Private-notes.html", "<html>notes</html>")
	write(t, root, "notes.qmd", "---\n")

	// Without a terminal, publishing needs --yes.
	if _, errOut, err := h.run(t, "", "publish", "outputs/report.html"); exitCode(err) != exitUsage || !strings.Contains(errOut, "report.html") {
		t.Errorf("without --yes: exit %d %v\n%s", exitCode(err), err, errOut)
	}

	out, _, err := h.run(t, "", "publish", "outputs/report.html", "--yes", "--slug", "report", "--readable")
	if err != nil || !strings.Contains(out, "Link: https://x/d/abc") {
		t.Fatalf("single file: %v\n%s", err, out)
	}
	if lastUpload["field"] != "file" || lastUpload["data"] != "<html>report</html>" || lastUpload["slug"] != "report" || lastUpload["readable"] != "1" {
		t.Errorf("single file upload: %+v", lastUpload)
	}

	out, _, err = h.run(t, "", "publish", "notebooks/analysis.html", "--yes", "--json")
	var doc cloud.Document
	decode(t, out, &doc)
	if err != nil || doc.Version != 2 {
		t.Fatalf("quarto bundle: %v %+v", err, doc)
	}
	if lastUpload["field"] != "bundle" || lastUpload["entrypoint"] != "analysis.html" || lastUpload["readable"] != "" ||
		strings.Join(zipNames(t, lastUpload["data"]), ",") != "analysis.html,analysis_files/figure.png" {
		t.Errorf("quarto bundle upload: %+v %v", lastUpload, zipNames(t, lastUpload["data"]))
	}
	// Quarto names the folder after the source, so -o gives a different
	// HTML name. Only folders the HTML refers to are sent.
	if _, _, err := h.run(t, "", "publish", "notebooks/split.html", "--yes"); err != nil {
		t.Fatal(err)
	}
	if lastUpload["entrypoint"] != "split.html" || strings.Join(zipNames(t, lastUpload["data"]), ",") != "demo_files/style.css,split.html" {
		t.Errorf("renamed render: %+v %v", lastUpload, zipNames(t, lastUpload["data"]))
	}

	h.app.jsonOut = false
	if _, _, err := h.run(t, "", "publish", "_site", "--yes", "--readable=false"); err != nil {
		t.Fatal(err)
	}
	if lastUpload["entrypoint"] != "index.html" || lastUpload["readable"] != "0" ||
		strings.Join(zipNames(t, lastUpload["data"]), ",") != "about/index.html,index.html" {
		t.Errorf("folder bundle: %+v %v", lastUpload, zipNames(t, lastUpload["data"]))
	}
	if _, _, err := h.run(t, "", "publish", "_site", "--yes", "--entrypoint", "missing.html"); err == nil {
		t.Error("published a folder without its entry point")
	}
	if _, _, err := h.run(t, "", "publish", "_site", "--yes", "--entrypoint", "../x.html"); exitCode(err) != exitUsage {
		t.Errorf("entrypoint outside the folder: %v", err)
	}

	// A path containing "private" in any case needs --include-private
	// without a terminal or with --yes or --json.
	for _, args := range [][]string{
		{"publish", "inputs/private/table.html", "--yes"},
		{"publish", "outputs/Private-notes.html", "--yes", "--json"},
		{"publish", "inputs/private/table.html"},
	} {
		lastUpload = nil
		_, _, err := h.run(t, "", args...)
		if exitCode(err) != exitPrivate || !strings.Contains(err.Error(), "--include-private") || lastUpload != nil {
			t.Errorf("%v: exit %d %v %+v", args, exitCode(err), err, lastUpload)
		}
	}
	h.app.jsonOut = false
	if _, errOut, err := h.run(t, "", "publish", "inputs/private/table.html", "--yes", "--include-private"); err != nil || lastUpload == nil || !strings.Contains(errOut, "inputs/private/table.html") {
		t.Errorf("--include-private: %v\n%s", err, errOut)
	}
	lastUpload = nil
	if _, _, err := h.run(t, "", "publish", "inputs/private/table.html", "--yes", "--allow-private"); err != nil || lastUpload == nil {
		t.Errorf("--allow-private from 0.2.0: %v", err)
	}

	// At a terminal, fw warns and asks once, defaulting to No.
	h.app.interactive = true
	lastUpload = nil
	if _, errOut, err := h.run(t, "\n", "publish", "inputs/private/table.html"); err == nil || lastUpload != nil || !strings.Contains(errOut, "Warning:") {
		t.Errorf("declined private upload: %v\n%s", err, errOut)
	}
	if _, errOut, err := h.run(t, "y\n", "publish", "inputs/private/table.html"); err != nil || lastUpload == nil ||
		!strings.Contains(errOut, "Upload them anyway?") || strings.Contains(errOut, "Upload it?") {
		t.Errorf("accepted private upload: %v\n%s", err, errOut)
	}
	h.app.interactive = false

	if _, _, err := h.run(t, "", "publish", "notes.qmd", "--yes"); err == nil || !strings.Contains(err.Error(), "render") {
		t.Errorf("source file: %v", err)
	}

	// Outside a linked project there is no project token to publish with.
	h.app.cwd = h.app.home
	if _, _, err := h.run(t, "", "publish", filepath.Join(root, "outputs/report.html"), "--yes"); err == nil {
		t.Error("published outside a project")
	}
}

func TestPublishWarnsPrivateInsideBundle(t *testing.T) {
	srv := fakeCloud(t)
	h := newHarness(t, nil)
	root := linkedProject(t, h, srv.URL)
	write(t, root, "outputs/site/index.html", "<html></html>")
	write(t, root, "outputs/site/private/rows.html", "<html></html>")
	lastUpload = nil
	if _, _, err := h.run(t, "", "publish", "outputs/site", "--yes"); exitCode(err) != exitPrivate || !strings.Contains(err.Error(), "outputs/site/private/rows.html") || lastUpload != nil {
		t.Errorf("bundle with a private folder: %v", err)
	}
	// Every file in a folder is checked; the warning lists five.
	for i := range 6 {
		write(t, root, fmt.Sprintf("outputs/site/private/more%d.html", i), "<html></html>")
	}
	_, _, err := h.run(t, "", "publish", "outputs/site", "--yes")
	if err == nil || !strings.Contains(err.Error(), "7 files to upload") || !strings.Contains(err.Error(), " ... and 2 more") {
		t.Errorf("long list: %v", err)
	}
}

func TestPushSpec(t *testing.T) {
	srv := fakeCloud(t)
	h := newHarness(t, nil)
	root := linkedProject(t, h, srv.URL)

	if _, _, err := h.run(t, "", "project", "push-spec"); err == nil || !strings.Contains(err.Error(), "no .framework/spec.json") {
		t.Errorf("no spec: %v", err)
	}
	spec := specWith(t, map[string]any{"name": "Study", "language": "r", "security": map[string]any{"sensitive": true}})
	write(t, root, ".framework/spec.json", spec)
	out, errOut, err := h.run(t, "", "project", "push-spec")
	if err != nil || !strings.Contains(out, "recorded the new spec") || strings.Contains(out, "version") || lastSpec != spec ||
		!strings.Contains(errOut, "19 sections") || !strings.Contains(errOut, "sensitive project") {
		t.Errorf("push-spec: %v\n%s\n%s\nsent %s", err, out, errOut, lastSpec)
	}

	write(t, root, ".framework/spec.json", specWith(t, map[string]any{"env": map[string]any{"DB_PASSWORD": "hunter2"}}))
	if _, _, err := h.run(t, "", "project", "push-spec"); err == nil || !strings.Contains(err.Error(), "DB_PASSWORD looks secret") {
		t.Errorf("rejected spec: %v", err)
	}
}

// push-spec checks the spec against spec.v1 first. The keys upgrade() in
// an older upgrade() wrote are left out with a warning; anything else stops it
// with the keys named and nothing sent.
func TestPushSpecChecksSchema(t *testing.T) {
	srv := fakeCloud(t)
	h := newHarness(t, nil)
	root := linkedProject(t, h, srv.URL)

	stale := specWith(t, map[string]any{"scaffold": map[string]any{"seed_on_scaffold": false, "seed": "", "positron": true},
		"source": map[string]any{"origin": "r", "upgraded_from": "1.1.0"}})
	write(t, root, ".framework/spec.json", stale)
	lastSpec = ""
	out, errOut, err := h.run(t, "", "project", "push-spec", "--json")
	if err != nil || !strings.Contains(out, `"changed": true`) || strings.Contains(lastSpec, "positron") || strings.Contains(lastSpec, "upgraded_from") ||
		!strings.Contains(lastSpec, `"seed_on_scaffold":false`) || !strings.Contains(lastSpec, `"origin":"r"`) ||
		!strings.Contains(errOut, "Warning: .framework/spec.json has scaffold.positron and source.upgraded_from") || !strings.Contains(errOut, "fw project upgrade --apply") {
		t.Errorf("stale keys: %v\n%s\n%s\nsent %s", err, out, errOut, lastSpec)
	}
	if readTestFile(t, filepath.Join(root, ".framework", "spec.json")) != stale {
		t.Error("the file changed")
	}

	bad := specWith(t, map[string]any{"ai": map[string]any{"enabled": false, "assistants": map[string]any{}, "agents_md": "none", "model": "x"}, "author": nil})
	write(t, root, ".framework/spec.json", bad)
	lastSpec = ""
	_, errOut, err = h.run(t, "", "project", "push-spec", "--json")
	var e *exitError
	if !errors.As(err, &e) || e.code != exitFailure || lastSpec != "" || e.reason != "spec_invalid" || e.hint != "fw project upgrade --apply" ||
		strings.Join(e.problems, "|") != "ai.model: not part of spec.v1|author: missing" || !strings.Contains(e.Error(), "Nothing was sent") {
		t.Errorf("bad spec: %v\n%s", err, errOut)
	}
}

func TestPrivatePaths(t *testing.T) {
	for p, want := range map[string]bool{
		"/private/tmp/x/report.html":                 false, // macOS's /tmp
		"/private/var/folders/T/study/a.html":        false,
		"/private/tmp/x/private/data.csv":            true,
		"/Users/ada/proj/Private/out.html":           true,
		"/Users/ada/PRIVATE-study/a.html":            true,
		"/Users/ada/proj/outputs/a.html":             false,
		"/privateer/a.html":                          true,
		"/private/var/folders/proj/Private/out.html": true,
		"/srv/my_private_notes.html":                 true,
	} {
		if got := isPrivatePath(p); got != want {
			t.Errorf("isPrivatePath(%q) = %v; want %v", p, got, want)
		}
	}
}

func TestPushSpecPrivatePath(t *testing.T) {
	srv := fakeCloud(t)
	h := newHarness(t, nil)
	root := linkedProjectAt(t, h, srv.URL, "PRIVATE-study")
	write(t, root, ".framework/spec.json", adoptedSpec)
	lastSpec = ""
	if _, _, err := h.run(t, "", "project", "push-spec"); exitCode(err) != exitPrivate || lastSpec != "" || !strings.Contains(err.Error(), "PRIVATE-study") {
		t.Errorf("private path without a terminal: exit %d %v", exitCode(err), err)
	}
	if _, _, err := h.run(t, "", "project", "push-spec", "--include-private"); err != nil || lastSpec == "" {
		t.Errorf("--include-private: %v", err)
	}
	h.app.interactive = true
	lastSpec = ""
	if _, errOut, err := h.run(t, "y\n", "project", "push-spec"); err != nil || lastSpec == "" || !strings.Contains(errOut, "Warning:") {
		t.Errorf("confirmed at a terminal: %v\n%s", err, errOut)
	}
}

func TestFileArtifactUpload(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"plot.png", "table.csv", "model.bin"} {
		write(t, dir, name, "original bytes")
		path := filepath.Join(dir, name)
		upload, files, err := planUpload(path, "")
		if err != nil || upload.Name != name || upload.Bundle || string(upload.Data) != "original bytes" || len(files) != 1 {
			t.Fatalf("%s: upload=%+v files=%v error=%v", name, upload, files, err)
		}
	}
}

func TestPublishArtifactDetails(t *testing.T) {
	srv := fakeCloud(t)
	h := newHarness(t, nil)
	root := linkedProject(t, h, srv.URL)
	write(t, root, "outputs/plot.png", "png bytes")
	out, _, err := h.run(t, "", "publish", "outputs/plot.png", "--yes", "--to", "cloud",
		"--slug", "effect", "--title", "Effect", "--description", "Adjusted estimate",
		"--kind", "image", "--password", "review-password", "--visibility", "unlisted")
	if err != nil {
		t.Fatalf("publish: %v, %s", err, out)
	}
	for name, want := range map[string]string{"name": "plot.png", "data": "png bytes", "slug": "effect",
		"title": "Effect", "description": "Adjusted estimate", "kind": "image", "password": "review-password", "visibility": "unlisted"} {
		if lastUpload[name] != want {
			t.Errorf("%s = %q, want %q", name, lastUpload[name], want)
		}
	}
}

func TestPublishCollection(t *testing.T) {
	for _, mode := range []string{"success", "attach-failure", "missing", "public", "public-no-visibility", "public-private-readable"} {
		t.Run(mode, func(t *testing.T) {
			uploads := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/v1/collections":
					visibility := "unlisted"
					if strings.HasPrefix(mode, "public") {
						visibility = "public"
					}
					if mode == "missing" {
						fmt.Fprint(w, `{"collections":[]}`)
					} else {
						fmt.Fprintf(w, `{"collections":[{"id":7,"slug":"findings","title":"Findings","visibility":%q}]}`, visibility)
					}
				case "/api/v1/documents":
					uploads++
					fmt.Fprint(w, `{"id":14,"slug":"figure","version":2,"url":"https://x/d/figure"}`)
				case "/api/v1/collections/7/items":
					var body struct {
						DocumentID int  `json:"document_id"`
						Share      bool `json:"share_unlisted"`
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.DocumentID != 14 || !body.Share {
						t.Errorf("attachment payload: %+v %v", body, err)
					}
					if mode == "attach-failure" {
						w.WriteHeader(422)
						fmt.Fprint(w, `{"message":"Denied"}`)
					} else {
						fmt.Fprint(w, `{"id":1}`)
					}
				default:
					t.Errorf("unexpected route: %s", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer srv.Close()
			h := newHarness(t, nil)
			root := linkedProject(t, h, srv.URL)
			write(t, root, "figure.png", "png")
			args := []string{"publish", "figure.png", "--yes", "--json", "--collection", "findings", "--share-unlisted", "--visibility", "unlisted"}
			if mode == "public-no-visibility" {
				// No --visibility: refused before uploading, not after.
				args = args[:len(args)-2]
			}
			if mode == "public-private-readable" {
				// Audit 2026-11-05: an explicit --visibility wins over --readable,
				// as on the server, so a private artifact is refused before upload.
				args = append(args[:len(args)-2], "--visibility", "private", "--readable")
			}
			out, errOut, err := h.run(t, "", args...)
			if mode == "missing" || strings.HasPrefix(mode, "public") {
				if err == nil || uploads != 0 {
					t.Fatalf("preflight: %v uploads=%d", err, uploads)
				}
				if strings.HasPrefix(mode, "public") && !strings.Contains(err.Error(), "--visibility public") {
					t.Errorf("public collection: %v", err)
				}
				return
			}
			if err != nil || uploads != 1 {
				t.Fatalf("publish: %v uploads=%d", err, uploads)
			}
			var doc cloud.Document
			decode(t, out, &doc)
			if doc.CollectionAttached == nil || *doc.CollectionAttached != (mode == "success") {
				t.Errorf("status: %s", out)
			}
			if mode == "attach-failure" && (!strings.Contains(errOut, "Artifact published at https://x/d/figure") || doc.CollectionWarning == "") {
				t.Errorf("partial failure: %s %s", out, errOut)
			}
		})
	}
}

func TestPublishClearsFieldsAndNamesFolders(t *testing.T) {
	srv := fakeCloud(t)
	h := newHarness(t, nil)
	root := linkedProject(t, h, srv.URL)
	write(t, root, "figure.png", "png")
	if _, _, err := h.run(t, "", "publish", "figure.png", "--yes", "--password", "", "--description", ""); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"password", "description"} {
		if lastUpload[name+"_present"] != "true" || lastUpload[name] != "" {
			t.Errorf("explicit clear %s: %+v", name, lastUpload)
		}
	}
	for _, name := range []string{"book-a", "book-b"} {
		write(t, root, name+"/index.html", "<p>book</p>")
		if _, _, err := h.run(t, "", "publish", name, "--yes"); err != nil {
			t.Fatal(err)
		}
		if lastUpload["slug"] != name {
			t.Errorf("folder slug = %q, want %q", lastUpload["slug"], name)
		}
	}
}

func TestBlueprintsListLanguages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"blueprints":[{"key":"framework/project","name":"Project","language":"r","languages":["r","python","stata"],"type":"project","version":3},{"key":"me/py","name":"Py","language":"python","type":"project","version":1}]}`)
	}))
	defer srv.Close()
	h := newHarness(t, map[string]string{"FW_CLOUD_TOKEN": "user-tok", "FW_CLOUD_URL": srv.URL})
	out, _, err := h.run(t, "", "blueprints", "list")
	if err != nil || !strings.Contains(out, "LANGUAGES") || !strings.Contains(out, "r, python, stata") {
		t.Errorf("text: %v\n%s", err, out)
	}
	out, _, err = h.run(t, "", "blueprints", "list", "--lang", "stata")
	if err != nil || !strings.Contains(out, "framework/project") || strings.Contains(out, "me/py") {
		t.Errorf("--lang stata: %v\n%s", err, out)
	}
	if _, _, err := h.run(t, "", "blueprints", "list", "--lang", "julia"); exitCode(err) != exitUsage {
		t.Errorf("--lang julia: %v", err)
	}
}

// A server without browser login, and no terminal: name --with-token
// instead of trying to read a hidden token from a pipe.
func TestLoginWithoutDeviceFlowOrTerminal(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	h := newHarness(t, nil)
	h.app.readSecret = func() (string, error) {
		t.Error("read a hidden token without a terminal")
		return "", errors.New("operation not supported by device")
	}
	_, _, err := h.run(t, "", "login", "--url", srv.URL, "--yes")
	if exitCode(err) != exitUsage || !strings.Contains(err.Error(), "fw login --with-token --yes") || !strings.Contains(err.Error(), "/tokens") {
		t.Errorf("got exit %d: %v", exitCode(err), err)
	}
	_, _, err = h.run(t, "", "login", "--url", srv.URL, "--yes", "--with-token", "--paste")
	if exitCode(err) != exitUsage || !strings.Contains(err.Error(), "Use one") {
		t.Errorf("both token flags: %v", err)
	}
	if _, errOut, err := h.run(t, "", "login", "--yes", "--url", "https://exa mple.org"); err == nil || !strings.Contains(err.Error(), "space") || strings.Contains(errOut, "Logging in connects") {
		t.Errorf("URL with a space: %v\n%s", err, errOut)
	}
}

func TestSkillSyncPathsAndOutput(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "scripts")
	for in, want := range map[string]string{"skills/x": "scripts/skills/x", "../skills/x/": "skills/x", "./../skills/./x": "skills/x", filepath.Join(root, "skills", "x"): "skills/x"} {
		got, err := skillPath(root, sub, in)
		if err != nil || got != want {
			t.Errorf("skillPath(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"../..", "..", "../../elsewhere"} {
		if _, err := skillPath(root, sub, in); exitCode(err) != exitUsage {
			t.Errorf("skillPath(%q): %v", in, err)
		}
	}
	for _, c := range []struct {
		r    skillsync.Result
		want string
	}{
		{skillsync.Result{Action: "push"}, "push: skills/x (new on Framework Cloud)"},
		{skillsync.Result{Action: "conflict", ID: "me:x", Version: 4}, "conflict: skills/x (me:x, version 4)"},
		{skillsync.Result{Action: "missing"}, "missing: skills/x"},
	} {
		if got := skillResultText(c.r, "skills/x"); got != c.want {
			t.Errorf("got %q, want %q", got, c.want)
		}
	}

	// The variant follows the project's language, and bad flags are usage errors.
	var variant string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		variant = r.URL.Query().Get("variant")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"skill_id":"me:x","version":2,"hash":"bad","bundle":[]}`)
	}))
	defer srv.Close()
	h := newHarness(t, map[string]string{"FW_CLOUD_TOKEN": "user-tok", "FW_CLOUD_URL": srv.URL})
	proj := filepath.Join(h.app.home, "study")
	write(t, proj, ".framework/spec.json", `{"language":"stata"}`)
	write(t, proj, "skills/x/SKILL.md", "---\nname: x\n---\n")
	h.app.cwd = filepath.Join(proj, "skills")
	_, _, _ = h.run(t, "", "skills", "sync", "x/", "--id", "me:x", "--dry-run")
	if variant != "stata" {
		t.Errorf("variant %q in a Stata project", variant)
	}
	if _, err := os.Stat(filepath.Join(proj, ".framework", "skills.sync.lock")); !os.IsNotExist(err) {
		t.Error("dry run left a lock")
	}
	for _, args := range [][]string{{"--direction", "sideways"}, {"--variant", "julia"}, {"--resolve", "both"}} {
		if _, _, err := h.run(t, "", append([]string{"skills", "sync", "x"}, args...)...); exitCode(err) != exitUsage {
			t.Errorf("%v: %v", args, err)
		}
	}
}
