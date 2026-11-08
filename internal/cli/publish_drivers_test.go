package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNamedFolderPublish(t *testing.T) {
	h := newHarness(t, nil)
	srv := fakeCloud(t)
	root := linkedProject(t, h, srv.URL)
	dest := t.TempDir()
	write(t, root, "settings.yml", "publish:\n  default: lab\n  destinations:\n    lab:\n      driver: folder\n      path: "+filepath.ToSlash(dest)+"\n      private: true\n")
	write(t, root, "figure.svg", `<svg xmlns="http://www.w3.org/2000/svg"></svg>`)
	out, errOut, err := h.run(t, "", "publish", "figure.svg", "--yes", "--json")
	if err != nil {
		t.Fatal(err, errOut)
	}
	var r struct {
		Driver  string `json:"driver"`
		Version int    `json:"version"`
	}
	if json.Unmarshal([]byte(out), &r) != nil || r.Driver != "folder" || r.Version != 1 {
		t.Fatal(out)
	}
	page, e := os.ReadFile(filepath.Join(dest, "figure/index.html"))
	if e != nil || !strings.Contains(string(page), "figure.svg") {
		t.Fatal(e, string(page))
	}
	archive, e := os.ReadFile(filepath.Join(dest, "figure/v/1/artifact.json"))
	if e != nil || !strings.Contains(string(archive), "framework.bundle/v1") {
		t.Fatal(e)
	}
	for _, flags := range [][]string{{"--password", "secret"}, {"--visibility", "unlisted"}, {"--visibility", "public"}} {
		args := append([]string{"publish", "figure.svg", "--yes"}, flags...)
		if _, _, e := h.run(t, "", args...); e == nil {
			t.Fatalf("accepted unsupported policy %v", flags)
		}
	}
	if _, e := os.Stat(filepath.Join(dest, "figure/v/2")); !os.IsNotExist(e) {
		t.Fatal("preflight mutated destination")
	}
}
func TestPublishDestinationPreference(t *testing.T) {
	root := t.TempDir()
	write(t, root, "settings.yml", "publish:\n  default: pages\n  destinations:\n    pages: {driver: github}\n")
	d, e := resolvePublish(root, "")
	if e != nil || d.Driver != "github" {
		t.Fatal(d, e)
	}
	d, e = resolvePublish(root, "cloud")
	if e != nil || d.Driver != "cloud" {
		t.Fatal(d, e)
	}
	if _, e = resolvePublish(root, "unknown"); e == nil {
		t.Fatal("unknown destination accepted")
	}
}

func TestExternalTrackingMetadataOnly(t *testing.T) {
	var tracked map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer proj-tok" {
			t.Errorf("wrong authorization")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/me":
			_, _ = w.Write([]byte(`{"project":"study"}`))
		case "/api/v1/projects/study/artifacts/external":
			if r.Method != "POST" {
				t.Error(r.Method)
			}
			_ = json.NewDecoder(r.Body).Decode(&tracked)
			_, _ = w.Write([]byte(`{"id":1}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	h := newHarness(t, nil)
	root := linkedProject(t, h, srv.URL)
	dest := t.TempDir()
	write(t, root, "settings.yml", "publish:\n  default: lab\n  destinations:\n    lab: {driver: folder, path: "+filepath.ToSlash(dest)+", visibility: private}\n")
	write(t, root, "report.html", "<p>secret contents</p>")
	if _, stderr, e := h.run(t, "", "publish", "report.html", "--yes"); e != nil {
		t.Fatal(e, stderr)
	}
	data, _ := json.Marshal(tracked)
	if tracked["driver"] != "folder" || tracked["version"] != float64(1) || len(tracked["sha256"].(string)) != 64 || strings.Contains(string(data), dest) || strings.Contains(string(data), "secret contents") {
		t.Fatal(string(data))
	}
	hash := tracked["sha256"]
	if _, _, e := h.run(t, "", "publish", "report.html", "--yes"); e != nil {
		t.Fatal(e)
	}
	if tracked["version"] != float64(1) || tracked["sha256"] != hash {
		t.Fatal("idempotent receipt changed", tracked)
	}
}
func TestS3VisibilityMismatchStopsBeforeSync(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	bin := t.TempDir()
	log := filepath.Join(bin, "calls")
	script := "#!/bin/sh\necho \"$*\" >> '" + log + "'\necho '{\"PolicyStatus\":{\"IsPublic\":false}}'\n"
	if e := os.WriteFile(filepath.Join(bin, "aws"), []byte(script), 0700); e != nil { // #nosec G306 -- the fake aws CLI must be executable
		t.Fatal(e)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	h := newHarness(t, nil)
	srv := fakeCloud(t)
	root := linkedProject(t, h, srv.URL)
	write(t, root, "settings.yml", "publish:\n  default: bucket\n  destinations:\n    bucket: {driver: s3, bucket: results, visibility: public}\n")
	write(t, root, "report.html", "<p>results</p>")
	if _, _, e := h.run(t, "", "publish", "report.html", "--yes"); e == nil || !strings.Contains(e.Error(), "bucket policy") {
		t.Fatal(e)
	}
	calls, _ := os.ReadFile(log)
	if strings.Contains(string(calls), "s3 sync") {
		t.Fatal("delivery started before visibility check", string(calls))
	}
}

func TestPublishingSettingsProfilesAndSpecFallback(t *testing.T) {
	root := t.TempDir()
	write(t, root, "settings.yml", "default:\n  publish:\n    default: pages\n    destinations:\n      pages: {driver: github}\n")
	d, e := resolvePublish(root, "")
	if e != nil || d.Driver != "github" {
		t.Fatal(d, e)
	}
	write(t, root, "settings.yml", "default: {}\n")
	write(t, root, ".framework/spec.json", `{"publish":{"default":"lab","destinations":{"lab":{"driver":"folder","path":"/mounted/share","visibility":"private"}}}}`)
	d, e = resolvePublish(root, "")
	if e != nil || d.Driver != "folder" || d.Path != "/mounted/share" || d.Visibility != "private" {
		t.Fatal(d, e)
	}
}
func TestStaticOriginalFilenameColon(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("colon filenames are not supported on Windows")
	}
	root := t.TempDir()
	write(t, root, "javascript:figure.svg", `<svg xmlns="http://www.w3.org/2000/svg"></svg>`)
	b, e := buildStatic(staticPublishOptions{path: filepath.Join(root, "javascript:figure.svg")})
	if e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(b.Dir)
	if b.Manifest.Original != "./javascript:figure.svg" {
		t.Fatal(b.Manifest.Original)
	}
	if _, e = os.Stat(filepath.Join(b.Dir, "javascript:figure.svg")); e != nil {
		t.Fatal(e)
	}
}

func TestDestinationVisibilityAndCredentialCompatibility(t *testing.T) {
	root := t.TempDir()
	for yamlText, want := range map[string]string{
		"driver: folder\nprivate: true\n":                     "private",
		"driver: folder\nprivate: false\n":                    "public",
		"driver: folder\nprivate: true\nvisibility: public\n": "public",
	} {
		write(t, root, "settings.yml", "publish:\n  default: lab\n  destinations:\n    lab:\n      "+strings.ReplaceAll(strings.TrimSpace(yamlText), "\n", "\n      ")+"\n")
		d, e := resolvePublish(root, "")
		if e != nil || d.Visibility != want {
			t.Fatal(d, e, want)
		}
	}
	for _, options := range []string{"password: literal-secret", "credentials: {secret_key: literal-secret}", "access_key_id: literal-secret", "endpoint: 'https://user:password@example.org'",
		`secret_key: 'env("RESULTS_SECRET", "literal-secret")'`, "session_token: 12345"} {
		write(t, root, "settings.yml", "publish:\n  default: bucket\n  destinations:\n    bucket: {driver: s3, "+options+"}\n")
		if _, e := resolvePublish(root, ""); e == nil || strings.Contains(e.Error(), "literal-secret") {
			t.Fatal("credential literal accepted or exposed", options, e)
		}
	}
	// Framework Cloud's rule: env("X") and env("X", "") pass, either quote.
	for _, ref := range []string{`'env("RESULTS_SECRET")'`, `"env('RESULTS_SECRET', '')"`, `'env("RESULTS_SECRET", "")'`} {
		write(t, root, "settings.yml", "publish:\n  default: bucket\n  destinations:\n    bucket: {driver: s3, credentials: {secret_key: "+ref+"}}\n")
		if _, e := resolvePublish(root, ""); e != nil {
			t.Fatal("environment reference rejected", ref, e)
		}
	}
	write(t, root, "settings.yml", "default: {}\n")
	write(t, root, ".framework/spec.json", `{"publish":{"default":"bucket","destinations":{"bucket":{"driver":"s3","secret_key":"literal-secret"}}}}`)
	if _, e := resolvePublish(root, ""); e == nil {
		t.Fatal("spec credential literal accepted")
	}
}

func TestDestinationAWSCredentialsUseEnvironment(t *testing.T) {
	t.Setenv("RESULTS_ACCESS", "example-access")
	t.Setenv("RESULTS_SECRET", "example-secret")
	var d publishDestination
	raw := `{"driver":"s3","credentials":{"access_key_id":"env(\"RESULTS_ACCESS\")","secret_access_key":"env(\"RESULTS_SECRET\")"}}`
	if e := json.Unmarshal([]byte(raw), &d); e != nil {
		t.Fatal(e)
	}
	env, e := awsEnvironment(d)
	if e != nil {
		t.Fatal(e)
	}
	found := map[string]string{}
	for _, value := range env {
		key, text, ok := strings.Cut(value, "=")
		if ok {
			found[key] = text
		}
	}
	if found["AWS_ACCESS_KEY_ID"] != "example-access" || found["AWS_SECRET_ACCESS_KEY"] != "example-secret" {
		t.Fatal("destination credentials were not passed to AWS CLI")
	}
	// env("X", "") takes its empty default when X is unset; env("X") is an error.
	if v, e := resolveEnvOption(`env("FW_TEST_UNSET_VARIABLE", "")`); e != nil || v != "" {
		t.Errorf("empty default: %q %v", v, e)
	}
	if _, e := resolveEnvOption(`env('FW_TEST_UNSET_VARIABLE')`); e == nil {
		t.Error("unset without a default: no error")
	}
}

// A relative folder path starts at the project root, and ~ is the home
// folder, wherever fw runs in the project.
func TestFolderDestinationPathResolution(t *testing.T) {
	h := newHarness(t, nil)
	srv := fakeCloud(t)
	root := linkedProject(t, h, srv.URL)
	write(t, root, "settings.yml", "publish:\n  destinations:\n    rel: {driver: folder, path: _published, visibility: private}\n    tilde: {driver: folder, path: ~/fw-site, visibility: private}\n")
	write(t, root, "outputs/t.csv", "a,b\n1,2\n")
	write(t, root, "scripts/sub/keep.R", "")
	h.app.cwd = filepath.Join(root, "scripts", "sub")
	if _, errOut, e := h.run(t, "", "publish", "../../outputs/t.csv", "--to", "rel", "--yes"); e != nil {
		t.Fatal(e, errOut)
	}
	if _, e := os.Stat(filepath.Join(root, "_published", "t", "artifact.json")); e != nil {
		t.Errorf("relative path: %v", e)
	}
	if _, e := os.Stat(filepath.Join(root, "scripts", "sub", "_published")); !os.IsNotExist(e) {
		t.Errorf("published under the current folder: %v", e)
	}
	if _, errOut, e := h.run(t, "", "publish", "../../outputs/t.csv", "--to", "tilde", "--yes"); e != nil {
		t.Fatal(e, errOut)
	}
	if _, e := os.Stat(filepath.Join(h.app.home, "fw-site", "t", "artifact.json")); e != nil {
		t.Errorf("~ path: %v", e)
	}
	if _, e := os.Stat(filepath.Join(h.app.cwd, "~")); !os.IsNotExist(e) {
		t.Errorf("made a folder named ~: %v", e)
	}
	p, home, abs := filepath.Join(root, "p"), filepath.Join(root, "h"), filepath.Join(root, "abs")
	for in, want := range map[string]string{"~": home, "~/a/b": filepath.Join(home, "a", "b"), "rel": filepath.Join(p, "rel"), abs: abs, "~x": filepath.Join(p, "~x")} {
		if got := destinationPath(p, home, in); got != want {
			t.Errorf("destinationPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStaticPublishChecksAndSummary(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"Unauthenticated."}`))
	}))
	defer srv.Close()
	h := newHarness(t, nil)
	root := linkedProject(t, h, srv.URL)
	dest := t.TempDir()
	write(t, root, "settings.yml", "publish:\n  destinations:\n    lab: {driver: folder, path: "+filepath.ToSlash(dest)+", visibility: private}\n    noviz: {driver: folder, path: "+filepath.ToSlash(dest)+"}\n")
	write(t, root, "t.csv", "a\n1\n")

	// No visibility: say what to add.
	if _, _, e := h.run(t, "", "publish", "t.csv", "--to", "noviz", "--yes"); exitCode(e) != exitUsage || !strings.Contains(e.Error(), "visibility: private") {
		t.Errorf("no visibility: %v", e)
	}
	// Unknown kinds and --entrypoint with a file are refused.
	if _, _, e := h.run(t, "", "publish", "t.csv", "--to", "lab", "--kind", "bogus", "--yes"); exitCode(e) != exitUsage || !strings.Contains(e.Error(), "--kind takes html") {
		t.Errorf("--kind bogus: %v", e)
	}
	if _, _, e := h.run(t, "", "publish", "t.csv", "--to", "lab", "--entrypoint", "x.html", "--yes"); exitCode(e) != exitUsage {
		t.Errorf("--entrypoint with a file: %v", e)
	}
	// Without a terminal, the summary shows before the refusal.
	if _, errOut, e := h.run(t, "", "publish", "t.csv", "--to", "lab"); exitCode(e) != exitUsage || !strings.Contains(errOut, "Publishing t.csv (") || !strings.Contains(errOut, filepath.Base(dest)) {
		t.Errorf("summary: %v\n%s", e, errOut)
	}
	// A rejected token: the warning says why tracking failed.
	_, errOut, e := h.run(t, "", "publish", "t.csv", "--to", "lab", "--kind", "table", "--yes")
	if e != nil || !strings.Contains(errOut, "cloud tracking failed: ") || !strings.Contains(errOut, "invalid or revoked") {
		t.Errorf("tracking warning: %v\n%s", e, errOut)
	}
}

func TestS3DeclaredVisibilityCheckedFirst(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	bin := t.TempDir()
	log := filepath.Join(bin, "calls")
	script := "#!/bin/sh\necho \"$*\" >> '" + log + "'\necho '{\"PolicyStatus\":{\"IsPublic\":true}}'\n"
	if e := os.WriteFile(filepath.Join(bin, "aws"), []byte(script), 0700); e != nil { // #nosec G306 -- the fake aws CLI must be executable
		t.Fatal(e)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	h := newHarness(t, nil)
	srv := fakeCloud(t)
	root := linkedProject(t, h, srv.URL)
	write(t, root, "settings.yml", "publish:\n  default: bucket\n  destinations:\n    bucket: {driver: s3, bucket: results}\n")
	write(t, root, "report.html", "<p>results</p>")
	if _, _, e := h.run(t, "", "publish", "report.html", "--yes"); e == nil || !strings.Contains(e.Error(), "visibility: public") {
		t.Fatal(e)
	}
	if calls, _ := os.ReadFile(log); len(calls) > 0 {
		t.Fatal("called AWS before checking the settings:\n", string(calls))
	}
}
