package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/frameworktools/framework-cli/internal/config"
	"github.com/frameworktools/framework-cli/internal/keyring"
)

// fakeCloud answers the endpoints fw uses. Tokens: "user-tok" is a user,
// "proj-tok" is a project.
func fakeCloud(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	auth := func(r *http.Request) string { return strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ") }
	mux.HandleFunc("GET /api/v1/me", func(w http.ResponseWriter, r *http.Request) {
		switch auth(r) {
		case "user-tok":
			_ = json.NewEncoder(w).Encode(map[string]string{"kind": "user", "email": "ada@example.edu"})
		case "proj-tok":
			me := map[string]any{"kind": "project", "project": "study"}
			if fakeMePages != nil {
				me["pages_url"] = *fakeMePages
			}
			_ = json.NewEncoder(w).Encode(me)
		default:
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"message":"Unauthenticated."}`))
		}
	})
	mux.HandleFunc("GET /api/v1/projects", func(w http.ResponseWriter, r *http.Request) {
		if auth(r) != "user-tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"projects":[{"name":"Vaccine study","slug":"vaccine-study","project_type":"project_sensitive","language":"stata","created_at":"2026-10-30T12:00:00Z"}]}`))
	})
	mux.HandleFunc("POST /api/v1/cli/device", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"device_code":"dev","user_code":"ABCD-EFGH","verification_url":"x/device","verification_url_complete":"x/device?code=ABCD-EFGH","interval":5,"expires_in":600}`))
	})
	mux.HandleFunc("POST /api/v1/cli/device/token", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"token":"user-tok","url":"x"}`))
	})
	layers := `{"spec_version":1,"name":"Vaccine Study","layers":[{"kind":"base","key":"project","layer":{"type":"project"}},{"kind":"project","key":"vaccine-study","layer":{"language":"r"}}]}`
	mux.HandleFunc("GET /api/v2/projects/{slug}/layers", func(w http.ResponseWriter, r *http.Request) {
		if auth(r) != "user-tok" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if r.PathValue("slug") != "vaccine-study" && r.PathValue("slug") != "survey" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"You have no project named 'x'."}`))
			return
		}
		body := layers
		if stataLayers {
			body = strings.Replace(layers, `"language":"r"`, `"language":"stata"`, 1)
		}
		_, _ = w.Write([]byte(body)) // #nosec G705 -- fixed test bodies
	})
	mux.HandleFunc("POST /api/v2/projects/{slug}/pull-tokens", func(w http.ResponseWriter, r *http.Request) {
		if auth(r) != "user-tok" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		minted++
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"token":"minted-tok","url":"https://issuer.example.edu"}`))
	})
	mux.HandleFunc("GET /api/v2/projects/pull", func(w http.ResponseWriter, r *http.Request) {
		if auth(r) != "proj-tok" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"message":"This endpoint requires a project token."}`))
			return
		}
		_, _ = w.Write([]byte(strings.Replace(layers, `"language":"r"`, `"language":"python"`, 1)))
	})
	mux.HandleFunc("GET /api/v2/projects/{slug}", func(w http.ResponseWriter, r *http.Request) {
		if auth(r) != "user-tok" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"message":"This needs a user token."}`))
			return
		}
		if r.PathValue("slug") == "study" {
			pages := "null"
			if fakePagesURL != "" {
				pages = `"` + fakePagesURL + `"`
			}
			_, _ = w.Write([]byte(`{"slug":"study","name":"Study","github":{"repository":"Acme/study","url":"https://github.com/Acme/study","private":true,"scaffold_status":"done","pages_url":` + pages + `}}`))
			return
		}
		if r.PathValue("slug") != "vaccine-study" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"You have no project named 'nope'."}`))
			return
		}
		_, _ = w.Write([]byte(`{"slug":"vaccine-study","name":"Vaccine study","description":"","language":"stata","type":"project_sensitive","blueprint":{"key":"framework/project_sensitive","version":2},"github":null,"created_at":"2026-10-30T12:00:00Z","updated_at":"2026-10-31T09:00:00Z","spec_received_at":null}`))
	})
	pieces := map[string]string{
		"structures":   `{"structures":[{"key":"framework/project","name":"Project","description":"Folders for an analysis","type":"project","version":2,"system":true,"visibility":"public"}]}`,
		"tools":        `{"tools":[{"key":"quarto","name":"Quarto","description":"Render documents","language":["r","python"],"requires":[],"version":1},{"key":"quarto_slides","name":"Quarto slides","language":["r","python"],"requires":["quarto"],"version":1}]}`,
		"package-sets": `{"package_sets":[{"key":"framework/tidyverse-analysis","name":"Tidyverse analysis","language":"r","packages":["dplyr","ggplot2","readr","tidyr","purrr"],"version":1}]}`,
		"skill-sets":   `{"skill_sets":[{"key":"framework/ggplot2","name":"ggplot2","language":"r","skills":["ggplot2-exploring"],"packages":["ggplot2"],"version":1}]}`,
		"skills":       `{"skills":[{"key":"framework/ggplot2-exploring","name":"Exploring with ggplot2","version":3}]}`,
	}
	mux.HandleFunc("GET /api/v2/{kind}", func(w http.ResponseWriter, r *http.Request) {
		body, ok := pieces[r.PathValue("kind")]
		if !ok || auth(r) != "user-tok" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		lastPiecesQuery = r.URL.RawQuery
		if r.URL.Query().Get("language") == "stata" {
			body = `{"` + strings.ReplaceAll(r.PathValue("kind"), "-", "_") + `":[]}`
		}
		_, _ = w.Write([]byte(body)) // #nosec G705 -- fixed test bodies
	})
	// resolve and create build layers from choices; lastChoices records the
	// body. A tool on Stata gets 422, as on the real server.
	choicesHandler := func(create bool) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if auth(r) != "user-tok" {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			body, _ := io.ReadAll(r.Body)
			lastChoices = string(body)
			var c struct {
				Name     string          `json:"name"`
				Language string          `json:"language"`
				Tools    map[string]bool `json:"tools"`
			}
			_ = json.Unmarshal(body, &c)
			if c.Language == "stata" && len(c.Tools) > 0 {
				w.WriteHeader(http.StatusUnprocessableEntity)
				_, _ = w.Write([]byte(`{"message":"Stata projects have no tools.","errors":{"tools":["Stata projects have no tools."]}}`))
				return
			}
			if create {
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(`{"slug":"survey","name":"Survey","language":"r","url":"https://x/projects/survey","token":"created-tok"}`))
				return
			}
			language := c.Language
			if language == "" {
				language = "r"
			}
			w.Header().Set("Framework-Defaults-Revision", "rev-1")
			if c.Name == "" {
				c.Name = "Untitled project"
				w.Header().Add("Framework-Notice", "Your default blueprint does not support "+language+".")
			}
			out, _ := json.Marshal(map[string]any{"spec_version": 1, "name": c.Name, "layers": []any{
				map[string]any{"kind": "base", "key": "framework/project", "layer": map[string]any{"type": "project"}},
				map[string]any{"kind": "project", "layer": map[string]any{"language": language}},
			}})
			_, _ = w.Write(out)
		}
	}
	mux.HandleFunc("POST /api/v2/structures", func(w http.ResponseWriter, r *http.Request) {
		if auth(r) != "user-tok" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		body, _ := io.ReadAll(r.Body)
		lastStructure = string(body)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"key":"ada/old-study","name":"Old Study","url":"https://x/structures/ada/old-study"}`))
	})
	mux.HandleFunc("POST /api/v2/resolve", choicesHandler(false))
	mux.HandleFunc("POST /api/v2/projects", choicesHandler(true))
	mux.HandleFunc("GET /api/v2/blueprints", func(w http.ResponseWriter, r *http.Request) {
		if auth(r) != "user-tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"blueprints":[{"key":"framework/project","name":"Project","description":"A standard project","language":null,"type":"project","version":3,"visibility":"public","system":true,"facets":["analysis"]}]}`))
	})
	mux.HandleFunc("POST /api/v1/documents", func(w http.ResponseWriter, r *http.Request) {
		if auth(r) != "proj-tok" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"message":"This endpoint requires a project token."}`))
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 64<<20)
		if err := r.ParseMultipartForm(64 << 20); err != nil { // #nosec G120 -- test server, body capped above
			w.WriteHeader(http.StatusUnprocessableEntity)
			return
		}
		lastUpload = map[string]string{}
		for _, name := range []string{"slug", "readable", "entrypoint", "title", "description", "kind", "password", "visibility"} {
			lastUpload[name] = r.FormValue(name)
		}
		for _, name := range []string{"password", "description"} {
			if _, ok := r.MultipartForm.Value[name]; ok {
				lastUpload[name+"_present"] = "true"
			}
		}
		for _, field := range []string{"file", "bundle"} {
			if f, hdr, err := r.FormFile(field); err == nil {
				data, _ := io.ReadAll(f)
				lastUpload["field"], lastUpload["name"], lastUpload["data"] = field, hdr.Filename, string(data)
			}
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"slug":"analysis","version":2,"url":"https://x/d/abc","private_url":"https://x/d/abc","readable_url":null,"version_url":"https://x/d/abc/v2","raw_url":"https://x/d/abc/raw","notice":null}`))
	})
	mux.HandleFunc("PUT /api/v2/project/spec", func(w http.ResponseWriter, r *http.Request) {
		if auth(r) != "proj-tok" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		var body struct {
			Spec json.RawMessage `json:"spec"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if strings.Contains(string(body.Spec), "hunter2") {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"message":"The spec is invalid.","errors":{"spec":["env.DB_PASSWORD looks secret and must be empty."]}}`))
			return
		}
		lastSpec = string(body.Spec)
		_, _ = w.Write([]byte(`{"received_at":"2026-10-31T10:00:00Z","spec_version":1,"changed":true}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// lastUpload and lastSpec record what the fake cloud last received.
var (
	lastUpload map[string]string
	lastSpec   string
)

// lastStructure is the body of the last structure upload.
var lastStructure string

// lastChoices is the body of the last resolve or create request.
var lastChoices string

// lastPiecesQuery is the query string of the last list of pieces.
var lastPiecesQuery string

// fakePagesURL is the Pages address the fake cloud reports for the
// project "study"; empty means Pages is off.
var fakePagesURL string

// fakeMePages, when set, is what /me reports as pages_url for the project
// token, as servers from 49bd585 on do. Nil leaves the field out.
var fakeMePages *any

// minted counts pull tokens the fake cloud has issued.
var minted int

// stataLayers makes the fake cloud's project a Stata project.
var stataLayers bool

type harness struct {
	app *app
	mem keyring.Memory
}

func newHarness(t *testing.T, env map[string]string) *harness {
	t.Helper()
	home := t.TempDir()
	mem := keyring.Memory{}
	return &harness{mem: mem, app: &app{
		getenv:     func(k string) string { return env[k] },
		dirs:       config.Dirs{FW: filepath.Join(home, "fw"), R: filepath.Join(home, "r")},
		home:       home,
		cwd:        home,
		keyring:    mem,
		stdin:      strings.NewReader(""),
		readSecret: func() (string, error) { return "", nil },
		openURL:    func(string) error { return nil },
		sleep:      func(context.Context, time.Duration) error { return nil },
	}}
}

func (h *harness) run(t *testing.T, stdin string, args ...string) (string, string, error) {
	t.Helper()
	h.app.stdin = strings.NewReader(stdin)
	h.app.reader = nil
	var out, errOut bytes.Buffer
	root := newRootCmd(h.app)
	root.SetArgs(args)
	root.SetOut(&out)
	root.SetErr(&errOut)
	err := root.Execute()
	return out.String(), errOut.String(), err
}

func TestLoginStatusProjectsLogout(t *testing.T) {
	srv := fakeCloud(t)
	h := newHarness(t, nil)

	out, errOut, err := h.run(t, "user-tok\n", "login", "--url", srv.URL, "--with-token", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "as ada@example.edu") || !strings.Contains(errOut, "/privacy") {
		t.Errorf("login output:\n%s\n%s", out, errOut)
	}
	if strings.Contains(out+errOut, "user-tok") {
		t.Error("login printed the token")
	}
	service := "framework-cloud:" + strings.TrimPrefix(srv.URL, "http://")
	if v, _ := h.mem.Get(service, "token"); v != "user-tok" {
		t.Errorf("keyring has %q", v)
	}
	if got := h.app.dirs.SavedServer(); got != srv.URL {
		t.Errorf("saved server %q", got)
	}

	// The saved default is used without --url from now on
	out, _, err = h.run(t, "", "status")
	if err != nil || !strings.Contains(out, "from your fw login default") || !strings.Contains(out, "as ada@example.edu") {
		t.Errorf("status: %v\n%s", err, out)
	}
	if strings.Contains(out, "user-tok") {
		t.Error("status printed the token")
	}

	out, _, err = h.run(t, "", "projects", "list")
	if err != nil || !strings.Contains(out, "vaccine-study") || !strings.Contains(out, "stata") || !strings.Contains(out, "2026-10-30") {
		t.Errorf("projects list: %v\n%s", err, out)
	}

	out, _, err = h.run(t, "", "logout")
	if err != nil || !strings.Contains(out, "Logged out") {
		t.Errorf("logout: %v\n%s", err, out)
	}
	out, _, err = h.run(t, "", "status")
	if err == nil || !strings.Contains(out, "Not logged in") {
		t.Errorf("status after logout: %v\n%s", err, out)
	}
}

func TestLoginNeedsConsent(t *testing.T) {
	srv := fakeCloud(t)
	h := newHarness(t, nil)
	_, _, err := h.run(t, "user-tok\n", "login", "--url", srv.URL, "--with-token")
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("got %v", err)
	}
	if len(h.mem) != 0 {
		t.Error("stored a token without consent")
	}
}

func TestLoginInteractiveDecline(t *testing.T) {
	srv := fakeCloud(t)
	h := newHarness(t, nil)
	h.app.interactive = true
	_, _, err := h.run(t, "n\n", "login", "--url", srv.URL)
	if err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("got %v", err)
	}
}

func TestLoginBrowserFlow(t *testing.T) {
	srv := fakeCloud(t)
	h := newHarness(t, nil)
	h.app.interactive = true
	var opened string
	h.app.openURL = func(u string) error { opened = u; return nil }
	_, errOut, err := h.run(t, "y\n", "login", "--url", srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if opened != "x/device?code=ABCD-EFGH" || !strings.Contains(errOut, "ABCD-EFGH") {
		t.Errorf("opened %q\n%s", opened, errOut)
	}
}

func TestLoginRejectsProjectToken(t *testing.T) {
	srv := fakeCloud(t)
	h := newHarness(t, nil)
	_, _, err := h.run(t, "proj-tok\n", "login", "--url", srv.URL, "--with-token", "--yes")
	if err == nil || !strings.Contains(err.Error(), "project token") {
		t.Fatalf("got %v", err)
	}
	if len(h.mem) != 0 {
		t.Error("stored a project token")
	}
}

func TestLoginBadToken(t *testing.T) {
	srv := fakeCloud(t)
	h := newHarness(t, nil)
	_, _, err := h.run(t, "nope\n", "login", "--url", srv.URL, "--with-token", "--yes")
	if err == nil || !strings.Contains(err.Error(), "fw login --url") {
		t.Fatalf("got %v", err)
	}
}

func TestStatusShowsProjectServer(t *testing.T) {
	srv := fakeCloud(t)
	h := newHarness(t, map[string]string{"FW_CLOUD_TOKEN": "user-tok", "FW_CLOUD_URL": srv.URL})
	proj := filepath.Join(h.app.home, "study")
	if err := os.MkdirAll(filepath.Join(proj, ".framework", "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	env := "FW_PROJECT_TOKEN=p\nFW_CLOUD_URL=https://other.example.edu\n"
	if err := os.WriteFile(filepath.Join(proj, ".env"), []byte(env), 0o600); err != nil {
		t.Fatal(err)
	}
	h.app.cwd = filepath.Join(proj, ".framework", "sub")
	out, _, err := h.run(t, "", "status")
	if err != nil || !strings.Contains(out, "linked to https://other.example.edu") {
		t.Errorf("%v\n%s", err, out)
	}
}
