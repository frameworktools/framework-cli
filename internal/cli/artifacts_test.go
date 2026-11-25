package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestArtifactsFlattenWithProjectToken(t *testing.T) {
	srv := fakeCloud(t)
	h := newHarness(t, nil)
	linkedProject(t, h, srv.URL)
	lastFlatten = ""
	out, errOut, err := h.run(t, "", "artifacts", "flatten", "effect", "--yes")
	if err != nil {
		t.Fatal(err, errOut)
	}
	if lastFlatten != "proj-tok effect " {
		t.Errorf("request: %q", lastFlatten)
	}
	if out != "Flattened effect to version 1; removed 3 older versions.\nManage: https://x/artifacts/7\n" {
		t.Errorf("stdout:\n%s", out)
	}
	if !strings.Contains(errOut, "in this project") || !strings.Contains(errOut, "for good") {
		t.Errorf("stderr:\n%s", errOut)
	}

	out, _, err = h.run(t, "", "artifacts", "flatten", "effect", "--yes", "--json")
	var res map[string]any
	if err != nil || json.Unmarshal([]byte(out), &res) != nil || res["removed"] != float64(3) || res["artifact_url"] != "https://x/artifacts/7" {
		t.Errorf("json: %v\n%s", err, out)
	}
}

func TestArtifactsFlattenConfirms(t *testing.T) {
	srv := fakeCloud(t)
	h := newHarness(t, nil)
	linkedProject(t, h, srv.URL)
	lastFlatten = ""
	// Without a terminal, --yes is required.
	if _, _, err := h.run(t, "", "artifacts", "flatten", "effect"); exitCode(err) != exitUsage || lastFlatten != "" {
		t.Errorf("no terminal: %v %q", err, lastFlatten)
	}
	h.app.interactive = true
	if _, errOut, err := h.run(t, "\n", "artifacts", "flatten", "effect"); err == nil || !strings.Contains(err.Error(), "nothing was changed") || lastFlatten != "" || !strings.Contains(errOut, "[y/N]") {
		t.Errorf("default No: %v %q", err, lastFlatten)
	}
	if _, _, err := h.run(t, "y\n", "artifacts", "flatten", "effect"); err != nil || lastFlatten != "proj-tok effect " {
		t.Errorf("yes: %v %q", err, lastFlatten)
	}
}

func TestArtifactsFlattenWithLogin(t *testing.T) {
	srv := fakeCloud(t)
	h := newHarness(t, nil)
	if _, _, err := h.run(t, "user-tok\n", "login", "--url", srv.URL, "--with-token", "--yes"); err != nil {
		t.Fatal(err)
	}
	if _, errOut, err := h.run(t, "", "artifacts", "flatten", "effect", "--project", "study", "--yes"); err != nil || lastFlatten != "user-tok effect study" || !strings.Contains(errOut, "in project study") {
		t.Errorf("--project: %v %q %s", err, lastFlatten, errOut)
	}
	_, _, err := h.run(t, "", "artifacts", "flatten", "shared", "--yes")
	if err == nil || !strings.Contains(err.Error(), "--project") {
		t.Errorf("409: %v", err)
	}
	_, _, err = h.run(t, "", "artifacts", "flatten", "missing", "--yes")
	if err == nil || !strings.Contains(err.Error(), "No artifact missing") {
		t.Errorf("404: %v", err)
	}
	_, _, err = h.run(t, "", "artifacts", "flatten", "external", "--yes")
	if err == nil || !strings.Contains(err.Error(), "External publications can't be flattened.") {
		t.Errorf("422: %v", err)
	}
	// A linked project with --project uses the login, not the project token.
	linkedProject(t, h, srv.URL)
	if _, _, err := h.run(t, "", "artifacts", "flatten", "effect", "--project", "other", "--yes"); err != nil || lastFlatten != "user-tok effect other" {
		t.Errorf("project and --project: %v %q", err, lastFlatten)
	}
}
