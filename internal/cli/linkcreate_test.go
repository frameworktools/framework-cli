package cli

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/frameworktools/framework-cli/internal/engine"
)

// fakeProjects is a Framework Cloud with state, for fw project link
// --create: projects made with POST /projects, their structures and specs,
// and the requests in order. "user-tok" is the user; each project's token
// is "tok-SLUG".
type fakeProjects struct {
	mu         sync.Mutex
	projects   map[string]map[string]any // slug: name, structure, ...
	specs      map[string]string
	structures []string // bodies of POST /structures
	choices    []string // bodies of POST /projects
	requests   []string // "METHOD path"
}

func newFakeProjects(t *testing.T) (*fakeProjects, *httptest.Server) {
	t.Helper()
	f := &fakeProjects{projects: map[string]map[string]any{}, specs: map[string]string{}}
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
	mux.HandleFunc("PATCH /api/v1/projects/{slug}", user(func(w http.ResponseWriter, r *http.Request) {
		p := f.projects[r.PathValue("slug")]
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		p["name"] = body["name"]
		_ = json.NewEncoder(w).Encode(p)
	}))
	mux.HandleFunc("POST /api/v1/structures", user(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.structures = append(f.structures, string(body))
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"key":"ada/old-study-layout","name":"old-study layout","url":"https://x/structures/1"}`))
	}))
	mux.HandleFunc("POST /api/v1/projects", user(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.choices = append(f.choices, string(body))
		var c struct{ Name, Language, Structure string }
		_ = json.Unmarshal(body, &c)
		slug := strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(c.Name), "-"), "-")
		if f.projects[slug] != nil {
			fail(w, http.StatusUnprocessableEntity, "You already have a project with this name.")
			return
		}
		f.projects[slug] = map[string]any{"slug": slug, "name": c.Name, "language": c.Language, "structure": c.Structure}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"slug": slug, "name": c.Name, "language": c.Language, "url": "https://x/projects/" + slug, "token": "tok-" + slug})
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

const adoptedSpec = `{"spec_version":1,"name":"old-study","type":"bare","language":"stata",
"directories":{"do":"do","inputs_raw":"data/raw"},"extra_directories":{"irb":{"label":"IRB","path":"IRB"}},
"security":{"sensitive":false},"git":{"use_git":false},"ai":{"enabled":false},"source":{"origin":"cli","adopted":true}}`

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
	if res.Slug != "old-study" || !res.Created || !res.Linked || !res.SpecChanged || res.Structure != "ada/old-study-layout" {
		t.Errorf("result: %+v", res)
	}
	// The adopted folders became a structure, and the project was made from it.
	if len(f.structures) != 1 || !strings.Contains(f.structures[0], `"inputs_raw":"data/raw"`) || !strings.Contains(f.structures[0], `"irb":{"label":"IRB","path":"IRB"}`) {
		t.Errorf("structures: %v", f.structures)
	}
	var choices map[string]any
	_ = json.Unmarshal([]byte(f.choices[0]), &choices)
	if choices["name"] != "old-study" || choices["language"] != "stata" || choices["structure"] != "ada/old-study-layout" ||
		choices["settings"].(map[string]any)["sensitive"] != false || choices["settings"].(map[string]any)["git"] != false {
		t.Errorf("choices: %s", f.choices[0])
	}
	if f.specs["old-study"] != strings.TrimSpace(adoptedSpec) && !strings.Contains(f.specs["old-study"], `"adopted":true`) {
		t.Errorf("spec: %s", f.specs["old-study"])
	}
	// .env keeps what was there and gains the link; git ignores it.
	env := readTestFile(t, filepath.Join(dir, ".env"))
	if !strings.HasPrefix(env, "# mine\nDB_HOST=localhost\n") || !strings.Contains(env, "FW_PROJECT_TOKEN=tok-old-study\n") || !strings.Contains(env, "FW_CLOUD_URL="+srv.URL+"\n") {
		t.Errorf(".env:\n%s", env)
	}
	if ignore := readTestFile(t, filepath.Join(dir, ".gitignore")); !strings.Contains(ignore, ".env") {
		t.Errorf(".gitignore:\n%s", ignore)
	}
	if info, _ := os.Stat(filepath.Join(dir, ".env")); info.Mode().Perm() != 0o600 {
		t.Errorf(".env mode %v", info.Mode())
	}

	// Again: already linked, nothing changes and nothing is sent.
	before := len(f.requests)
	envBefore := env
	out, _, err = h.run(t, "", "project", "link", "--create")
	if err != nil || !strings.Contains(out, "Already linked to old-study. Nothing changed.") {
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

// A project made by Framework uses the built-in structure for its type, and
// --slug and --name set both.
func TestLinkCreateBuiltinAndSlug(t *testing.T) {
	f, srv := newFakeProjects(t)
	h := newHarness(t, map[string]string{"FW_CLOUD_TOKEN": "user-tok", "FW_CLOUD_URL": srv.URL})
	spec := `{"spec_version":1,"name":"Survey","type":"project_sensitive","language":"r","directories":{"inputs_private_raw":"inputs/private/raw"},"security":{"sensitive":true},"source":{"origin":"r"}}`
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
	if len(f.structures) != 0 || !strings.Contains(f.choices[0], `"structure":"framework/project_sensitive"`) || !strings.Contains(f.choices[0], `"sensitive":true`) {
		t.Errorf("choices: %v structures %v", f.choices, f.structures)
	}
	p := f.projects["survey-2026"]
	if p == nil || p["name"] != "Survey 2026 (final)" || !strings.Contains(out, "Created survey-2026") {
		t.Errorf("projects %v\n%s", f.projects, out)
	}
	if !strings.Contains(readTestFile(t, filepath.Join(dir, ".env")), "FW_PROJECT_TOKEN=tok-survey-2026") {
		t.Error("not linked")
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
	if len(f.choices) != 0 || len(f.structures) != 0 {
		t.Errorf("created something: %v %v", f.choices, f.structures)
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
	if len(f.choices) != 0 || !strings.Contains(readTestFile(t, filepath.Join(dir, ".env")), "FW_PROJECT_TOKEN=tok-old-study") {
		t.Errorf("terminal: choices %v", f.choices)
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
	if res.Language != "stata" || res.Cloud.Slug != "old-study" || !res.Cloud.Linked || f.specs["old-study"] == "" || len(f.structures) != 1 {
		t.Errorf("result %+v, structures %v", res, f.structures)
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
	before := len(f.choices)
	if _, _, err := h.run(t, "", "adopt", r, "--lang", "r", "--yes", "--cloud"); err == nil || len(f.choices) != before {
		t.Errorf("already adopted: %v %d", err, len(f.choices))
	}
}
