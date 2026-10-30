package cli

import (
	"bytes"
	"context"
	"encoding/json"
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
			_ = json.NewEncoder(w).Encode(map[string]string{"kind": "project", "project": "study"})
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
		if r.PathValue("slug") != "vaccine-study" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"You have no project named 'x'."}`))
			return
		}
		body := layers
		if stataLayers {
			body = strings.Replace(layers, `"language":"r"`, `"language":"stata"`, 1)
		}
		_, _ = w.Write([]byte(body))
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
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

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
