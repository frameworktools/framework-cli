package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPiecesLists(t *testing.T) {
	h, _ := loggedIn(t)

	out, _, err := h.run(t, "", "package-sets", "list", "--lang", "r")
	if err != nil || !strings.Contains(out, "framework/tidyverse-analysis") || !strings.Contains(out, "dplyr, ggplot2, readr, tidyr and 1 more") || lastPiecesQuery != "language=r" {
		t.Errorf("package sets: %v %q\n%s", err, lastPiecesQuery, out)
	}
	out, _, err = h.run(t, "", "tools", "list")
	if err != nil || !strings.Contains(out, "quarto_slides") || !strings.Contains(out, "r, python") || lastPiecesQuery != "" {
		t.Errorf("tools: %v\n%s", err, out)
	}
	out, _, err = h.run(t, "", "tools", "list", "--lang", "stata")
	if err != nil || !strings.Contains(out, "Stata projects have no tools") {
		t.Errorf("stata tools: %v\n%s", err, out)
	}
	out, _, err = h.run(t, "", "skills", "list", "--sets")
	if err != nil || !strings.Contains(out, "framework/ggplot2") || !strings.Contains(out, "ggplot2-exploring") {
		t.Errorf("skill sets: %v\n%s", err, out)
	}

	out, _, err = h.run(t, "", "structures", "list", "--json")
	var got struct {
		Server     string           `json:"server"`
		Structures []map[string]any `json:"structures"`
	}
	if err != nil || json.Unmarshal([]byte(out), &got) != nil || len(got.Structures) != 1 || got.Structures[0]["visibility"] != "public" || got.Server == "" {
		t.Errorf("structures --json: %v\n%s", err, out)
	}
	if !strings.HasPrefix(out, "{\n  \"server\"") {
		t.Errorf("server should come first:\n%s", out)
	}

	h.app.jsonOut = false
	if _, _, err := h.run(t, "", "tools", "list", "--lang", "julia"); exitCode(err) != exitUsage {
		t.Errorf("bad --lang: %v", err)
	}
}
