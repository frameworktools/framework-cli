package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/frameworktools/framework-cli/internal/engine"
)

// fakeProjects is a Framework Cloud with state, for fw project link
// --create, with the spec API of framework-cloud ef3224d: POST /projects
// with {"spec", "name", "slug", "adopted"} stores the spec as received.
// It records the projects, their specs, the create bodies, and the requests
// in order. "user-tok" is the user; each project's token is "tok-SLUG".
// With old set, it answers as a server from before the spec API: it ignores
// spec and slug, makes the slug from the name, and reports no type.
type fakeProjects struct {
	mu       sync.Mutex
	projects map[string]map[string]any // slug: name, language, type, adopted
	trashed  map[string]bool
	specs    map[string]string
	creates  []string // bodies of POST /projects
	requests []string // "METHOD path"
	old      bool
}

func newFakeProjects(t *testing.T) (*fakeProjects, *httptest.Server) {
	t.Helper()
	f := &fakeProjects{projects: map[string]map[string]any{}, trashed: map[string]bool{}, specs: map[string]string{}}
	nonSlug := regexp.MustCompile(`[^a-z0-9]+`)
	auth := func(r *http.Request) string { return strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ") }
	fail := func(w http.ResponseWriter, status int, msg string) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"message": msg})
	}
	mux := http.NewServeMux()
	user := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.requests = append(f.requests, r.Method+" "+r.URL.Path)
			if auth(r) != "user-tok" {
				fail(w, http.StatusForbidden, "This endpoint requires a user token.")
				return
			}
			next(w, r)
		}
	}
	mux.HandleFunc("GET /api/v1/me", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		tok := auth(r)
		if slug, ok := strings.CutPrefix(tok, "tok-"); ok && f.projects[slug] != nil {
			_ = json.NewEncoder(w).Encode(map[string]any{"kind": "project", "project": slug})
			return
		}
		if tok == "user-tok" {
			_ = json.NewEncoder(w).Encode(map[string]any{"kind": "user", "email": "ada@example.edu"})
			return
		}
		fail(w, http.StatusUnauthorized, "Unauthenticated.")
	})
	mux.HandleFunc("GET /api/v1/projects/{slug}", user(func(w http.ResponseWriter, r *http.Request) {
		p := f.projects[r.PathValue("slug")]
		if p == nil {
			fail(w, http.StatusNotFound, "You have no project named '"+r.PathValue("slug")+"'.")
			return
		}
		_ = json.NewEncoder(w).Encode(p)
	}))
	mux.HandleFunc("POST /api/v1/projects", user(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.creates = append(f.creates, string(body))
		var c struct {
			Spec    map[string]any `json:"spec"`
			Name    string         `json:"name"`
			Slug    string         `json:"slug"`
			Adopted bool           `json:"adopted"`
		}
		_ = json.Unmarshal(body, &c)
		invalid := func(field, msg string) {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_ = json.NewEncoder(w).Encode(map[string]any{"message": msg, "errors": map[string][]string{field: {msg}}})
		}
		if f.old {
			slug := strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(c.Name), "-"), "-")
			if f.projects[slug] != nil {
				invalid("name", "You already have a project with this name.")
				return
			}
			f.projects[slug] = map[string]any{"slug": slug, "name": c.Name, "language": "r"}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"slug": slug, "name": c.Name, "language": "r", "url": "https://x/projects/" + slug, "token": "tok-" + slug})
			return
		}
		if c.Spec == nil {
			invalid("spec", "The spec must be a JSON object.")
			return
		}
		name := c.Name
		if name == "" {
			name, _ = c.Spec["name"].(string)
		}
		slug := c.Slug
		if slug == "" {
			slug = strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(name), "-"), "-")
		}
		if f.projects[slug] != nil {
			invalid("slug", "You already have a project at "+slug+".")
			return
		}
		if f.trashed[slug] {
			invalid("slug", "You used "+slug+" for a project you deleted. Choose a different slug.")
			return
		}
		kind, _ := c.Spec["type"].(string)
		language, _ := c.Spec["language"].(string)
		f.projects[slug] = map[string]any{"slug": slug, "name": name, "language": language, "type": kind, "adopted": c.Adopted}
		// Stored as received, so it counts as the last spec
		raw, _ := json.Marshal(c.Spec)
		f.specs[slug] = string(raw)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"slug": slug, "name": name, "language": language, "type": kind, "adopted": c.Adopted,
			"url": "https://x/projects/" + slug, "token": "tok-" + slug, "github": nil})
	}))
	mux.HandleFunc("POST /api/v1/projects/{slug}/pull-tokens", user(func(w http.ResponseWriter, r *http.Request) {
		if f.projects[r.PathValue("slug")] == nil {
			fail(w, http.StatusNotFound, "no such project")
			return
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"token": "tok-" + r.PathValue("slug"), "url": ""})
	}))
	mux.HandleFunc("PUT /api/v1/project/spec", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.requests = append(f.requests, r.Method+" "+r.URL.Path)
		slug, ok := strings.CutPrefix(auth(r), "tok-")
		if !ok || f.projects[slug] == nil {
			fail(w, http.StatusForbidden, "This endpoint requires a project token.")
			return
		}
		var body struct {
			Spec json.RawMessage `json:"spec"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		changed := f.specs[slug] != string(body.Spec)
		f.specs[slug] = string(body.Spec)
		_ = json.NewEncoder(w).Encode(map[string]any{"received_at": "2026-11-07T10:00:00Z", "spec_version": 1, "changed": changed})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return f, srv
}

// adoptedProject writes a project as fw adopt leaves it: settings and an
// adopted spec, in a git-less folder.
func adoptedProject(t *testing.T, root, name, spec string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	writeTestFile(t, filepath.Join(dir, "settings.yml"), "default:\n  project_name: "+name+"\n")
	writeTestFile(t, filepath.Join(dir, ".framework", "spec.json"), spec)
	return dir
}

// adoptedSpec matches spec.v1, as the specs fw adopt and the packages
// write do.
const adoptedSpec = `{"spec_version":1,"name":"old-study","type":"bare","language":"stata",
"author":{"name":"Ada","email":"ada@example.edu","affiliation":""},
"directories":{"do":"do","inputs_raw":"data/raw"},"extra_directories":{"irb":{"label":"IRB","path":"IRB"}},
"render_dirs":{},"packages":{"use_renv":false,"default_packages":{}},"scaffold":{"seed_on_scaffold":false,"seed":""},"connections":{"databases":{},"storage_buckets":{}},
"security":{"sensitive":false},"git":{"use_git":false,"hooks":{},"gitignore_content":""},"ai":{"enabled":false,"assistants":{},"agents_md":"none"},
"env":{},"data_hooks":{},"files":{},"skills":{},"source":{"origin":"cli","adopted":true}}`

// specWith is adoptedSpec with top-level fields replaced, or removed when
// nil.
func specWith(t *testing.T, fields map[string]any) string {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(adoptedSpec), &doc); err != nil {
		t.Fatal(err)
	}
	for k, v := range fields {
		if v == nil {
			delete(doc, k)
		} else {
			doc[k] = v
		}
	}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestLinkCreate(t *testing.T) {
	f, srv := newFakeProjects(t)
	h := newHarness(t, map[string]string{"FW_CLOUD_TOKEN": "user-tok", "FW_CLOUD_URL": srv.URL})
	dir := adoptedProject(t, h.app.home, "old-study", adoptedSpec)
	writeTestFile(t, filepath.Join(dir, ".env"), "# mine\nDB_HOST=localhost\n")
	h.app.cwd = dir

	out, errOut, err := h.run(t, "", "project", "link", "--create", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	var res createdLink
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if res.Slug != "old-study" || !res.Created || !res.Linked || !res.SpecChanged || res.Type != "bare" || !res.Adopted {
		t.Errorf("result: %+v", res)
	}
	// One request creates the project from the spec as it is on disk, marked
	// adopted; no structure, and no separate push.
	var body struct {
		Spec    json.RawMessage `json:"spec"`
		Name    string          `json:"name"`
		Slug    string          `json:"slug"`
		Adopted bool            `json:"adopted"`
	}
	if len(f.creates) != 1 || json.Unmarshal([]byte(f.creates[0]), &body) != nil || body.Name != "old-study" || body.Slug != "old-study" || !body.Adopted ||
		string(body.Spec) != compactJSON(t, adoptedSpec) || strings.Contains(f.creates[0], "project_type") {
		t.Errorf("create: %s", f.creates)
	}
	for _, r := range f.requests {
		if r == "PUT /api/v1/project/spec" || r == "POST /api/v1/structures" {
			t.Errorf("sent %s", r)
		}
	}
	// .env keeps what was there and gains the link; git ignores it.
	env := readTestFile(t, filepath.Join(dir, ".env"))
	if !strings.HasPrefix(env, "# mine\nDB_HOST=localhost\n") || !strings.Contains(env, "FW_PROJECT_TOKEN=tok-old-study\n") || !strings.Contains(env, "FW_CLOUD_URL="+srv.URL+"\n") {
		t.Errorf(".env:\n%s", env)
	}
	if ignore := readTestFile(t, filepath.Join(dir, ".gitignore")); !strings.Contains(ignore, ".env") {
		t.Errorf(".gitignore:\n%s", ignore)
	}
	// Windows has no Unix modes; the file inherits the folder's ACL.
	if info, _ := os.Stat(filepath.Join(dir, ".env")); runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf(".env mode %v", info.Mode())
	}

	// Again: already linked, nothing changes and nothing is sent.
	before := len(f.requests)
	envBefore := env
	out, _, err = h.run(t, "", "project", "link", "--create")
	if err != nil || !strings.Contains(out, "Already linked to old-study. Nothing changed.") || !strings.Contains(out, "fw project push-spec") {
		t.Errorf("second run: %v\n%s", err, out)
	}
	if len(f.requests) != before || len(f.projects) != 1 || readTestFile(t, filepath.Join(dir, ".env")) != envBefore {
		t.Errorf("second run sent %v", f.requests[before:])
	}
	out, _, err = h.run(t, "", "project", "link", "--create", "--json")
	if err != nil || !strings.Contains(out, `"linked": true`) || !strings.Contains(out, `"created": false`) {
		t.Errorf("second run --json: %v\n%s", err, out)
	}
}

// A project made by Framework is sent with its own spec too, not marked
// adopted; --slug and --name go with it, and empty maps stay {}.
func TestLinkCreateSlugAndName(t *testing.T) {
	f, srv := newFakeProjects(t)
	h := newHarness(t, map[string]string{"FW_CLOUD_TOKEN": "user-tok", "FW_CLOUD_URL": srv.URL})
	spec := specWith(t, map[string]any{"name": "Survey", "type": "project_sensitive", "language": "r", "directories": map[string]any{"inputs_private_raw": "inputs/private/raw"},
		"extra_directories": map[string]any{}, "tools": map[string]any{"quarto": true, "renv": false}, "security": map[string]any{"sensitive": true}, "source": map[string]any{"origin": "r"}})
	dir := adoptedProject(t, h.app.home, "survey", spec)
	h.app.cwd = dir

	if _, _, err := h.run(t, "", "project", "link", "--create", "--slug", "Not A Slug"); exitCode(err) != exitUsage {
		t.Errorf("bad slug: %v", err)
	}
	if _, _, err := h.run(t, "", "project", "link", "--slug", "x"); exitCode(err) != exitUsage {
		t.Errorf("--slug without --create: %v", err)
	}
	out, errOut, err := h.run(t, "", "project", "link", "--create", "--slug", "survey-2026", "--name", "Survey 2026 (final)")
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	if len(f.creates) != 1 || strings.Contains(f.creates[0], `"adopted"`) || !strings.Contains(f.creates[0], `"data_hooks":{}`) || !strings.Contains(f.creates[0], `"tools":{"quarto":true,"renv":false}`) {
		t.Errorf("create: %v", f.creates)
	}
	p := f.projects["survey-2026"]
	if p == nil || p["name"] != "Survey 2026 (final)" || p["type"] != "project_sensitive" || !strings.Contains(out, "Created survey-2026") {
		t.Errorf("projects %v\n%s", f.projects, out)
	}
	if !strings.Contains(readTestFile(t, filepath.Join(dir, ".env")), "FW_PROJECT_TOKEN=tok-survey-2026") {
		t.Error("not linked")
	}
}

// link --create refuses a spec that breaks spec.v1 before creating
// anything, and leaves out the keys an older upgrade() wrote.
func TestLinkCreateChecksSchema(t *testing.T) {
	f, srv := newFakeProjects(t)
	h := newHarness(t, map[string]string{"FW_CLOUD_TOKEN": "user-tok", "FW_CLOUD_URL": srv.URL})
	dir := adoptedProject(t, h.app.home, "old-study", specWith(t, map[string]any{"directories": map[string]any{"Bad Key": "x"}}))
	h.app.cwd = dir
	_, _, err := h.run(t, "", "project", "link", "--create")
	if err == nil || !strings.Contains(err.Error(), "directories.Bad Key: not an allowed key name") || !strings.Contains(err.Error(), "then run fw project link --create again") || len(f.requests) != 0 {
		t.Errorf("bad spec: %v %v", err, f.requests)
	}

	writeTestFile(t, filepath.Join(dir, ".framework", "spec.json"), specWith(t, map[string]any{"source": map[string]any{"origin": "r", "upgraded_from": "1.1.0"}}))
	_, errOut, err := h.run(t, "", "project", "link", "--create")
	if err != nil || len(f.creates) != 1 || strings.Contains(f.creates[0], "upgraded_from") || !strings.Contains(errOut, "has source.upgraded_from, which") {
		t.Errorf("stale key: %v\n%s\n%v", err, errOut, f.creates)
	}
}

// A server from before the spec API makes the project from the user's
// defaults; fw then sends the spec separately and says so.
func TestLinkCreateOlderServer(t *testing.T) {
	f, srv := newFakeProjects(t)
	f.old = true
	h := newHarness(t, map[string]string{"FW_CLOUD_TOKEN": "user-tok", "FW_CLOUD_URL": srv.URL})
	dir := adoptedProject(t, h.app.home, "old-study", adoptedSpec)
	h.app.cwd = dir
	out, errOut, err := h.run(t, "", "project", "link", "--create")
	if err != nil || !strings.Contains(errOut, "older than the spec API") || !strings.Contains(out, "Sent .framework/spec.json.") || f.specs["old-study"] == "" {
		t.Errorf("%v\n%s%s", err, out, errOut)
	}
}

// A slug the user already has: fw creates nothing and offers --from.
func TestLinkCreateSlugTaken(t *testing.T) {
	f, srv := newFakeProjects(t)
	f.projects["old-study"] = map[string]any{"slug": "old-study", "name": "Old study"}
	h := newHarness(t, map[string]string{"FW_CLOUD_TOKEN": "user-tok", "FW_CLOUD_URL": srv.URL})
	dir := adoptedProject(t, h.app.home, "old-study", adoptedSpec)
	h.app.cwd = dir

	_, _, err := h.run(t, "", "project", "link", "--create")
	var exit *exitError
	if !errors.As(err, &exit) || exit.reason != "slug_taken" || exit.hint != "fw project link --from old-study" || !strings.Contains(err.Error(), "fw project link --from old-study") {
		t.Errorf("no terminal: %#v", err)
	}
	if len(f.projects) != 1 {
		t.Errorf("created something: %v", f.projects)
	}
	if _, err := os.Stat(filepath.Join(dir, ".env")); err == nil {
		t.Error("wrote .env")
	}

	// At a terminal, yes links to the existing project instead.
	h.app.interactive = true
	out, errOut, err := h.run(t, "y\n", "project", "link", "--create")
	if err != nil || !strings.Contains(errOut, "Link this folder to it instead?") || !strings.Contains(out, "fw project push-spec") {
		t.Errorf("terminal: %v\n%s%s", err, out, errOut)
	}
	if len(f.projects) != 1 || !strings.Contains(readTestFile(t, filepath.Join(dir, ".env")), "FW_PROJECT_TOKEN=tok-old-study") {
		t.Errorf("terminal: projects %v", f.projects)
	}

	// A slug held by a project in the Trash says so.
	h.app.interactive = false
	f.trashed["gone"] = true
	other := adoptedProject(t, t.TempDir(), "gone", adoptedSpec)
	h.app.cwd = other
	_, _, err = h.run(t, "", "project", "link", "--create", "--slug", "gone")
	if !errors.As(err, &exit) || exit.reason != "slug_in_trash" || !strings.Contains(err.Error(), "Trash") {
		t.Errorf("trash: %v", err)
	}
}

func TestLinkCreateNeedsSpecAndUntrackedEnv(t *testing.T) {
	_, srv := newFakeProjects(t)
	h := newHarness(t, map[string]string{"FW_CLOUD_TOKEN": "user-tok", "FW_CLOUD_URL": srv.URL})
	dir := filepath.Join(h.app.home, "old")
	writeTestFile(t, filepath.Join(dir, "settings.yml"), "default: {}\n")
	h.app.cwd = dir
	if _, _, err := h.run(t, "", "project", "link", "--create"); err == nil || !strings.Contains(err.Error(), "fw project upgrade --apply") {
		t.Errorf("no spec: %v", err)
	}
	if _, _, err := h.run(t, "", "project", "link", "--create", "--public"); exitCode(err) != exitUsage || !strings.Contains(err.Error(), "private") {
		t.Errorf("--public: %v", err)
	}
}

// fw adopt --cloud adopts, then creates and links the Cloud project, with
// one JSON result.
func TestAdoptCloud(t *testing.T) {
	f, srv := newFakeProjects(t)
	h := newHarness(t, map[string]string{"FW_CLOUD_TOKEN": "user-tok", "FW_CLOUD_URL": srv.URL})
	dir := oldStudy(t, h.app.home)

	out, errOut, err := h.run(t, "", "adopt", dir, "--yes", "--cloud", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	var res struct {
		Language string      `json:"language"`
		Created  []string    `json:"created"`
		Cloud    createdLink `json:"cloud"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if res.Language != "stata" || res.Cloud.Slug != "old-study" || !res.Cloud.Linked || !res.Cloud.Adopted || f.specs["old-study"] == "" {
		t.Errorf("result %+v", res)
	}
	if !strings.Contains(readTestFile(t, filepath.Join(dir, ".env")), "FW_PROJECT_TOKEN=tok-old-study") {
		t.Error("not linked")
	}

	// R: the package writes the spec, then fw creates the project from it.
	r := oldStudy(t, t.TempDir())
	engines := &fakeEngines{}
	engines.install(h, "Rscript")
	h.app.run = func(engine.Command) (int, error) {
		writeTestFile(t, filepath.Join(r, "settings.yml"), "default: {}\n")
		writeTestFile(t, filepath.Join(r, ".framework", "spec.json"), strings.Replace(adoptedSpec, `"language":"stata"`, `"language":"r"`, 1))
		return 0, nil
	}
	if _, errOut, err := h.run(t, "", "adopt", r, "--lang", "r", "--yes", "--cloud", "--slug", "old-study-r"); err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	if f.projects["old-study-r"] == nil || f.projects["old-study-r"]["language"] != "r" {
		t.Errorf("projects: %v", f.projects)
	}

	// Adopt failing creates nothing on the cloud.
	before := len(f.creates)
	if _, _, err := h.run(t, "", "adopt", r, "--lang", "r", "--yes", "--cloud"); err == nil || len(f.creates) != before {
		t.Errorf("already adopted: %v %d", err, len(f.creates))
	}
}

func compactJSON(t *testing.T, s string) string {
	t.Helper()
	var b bytes.Buffer
	if err := json.Compact(&b, []byte(s)); err != nil {
		t.Fatal(err)
	}
	return b.String()
}
