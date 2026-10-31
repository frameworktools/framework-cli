package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frameworktools/framework-cli/internal/spec"
)

func TestReferenceMatchesSchema(t *testing.T) {
	data, err := Reference("v0.2.0")
	if err != nil {
		t.Fatal(err)
	}
	if err := spec.ValidateDocsReference(data); err != nil {
		t.Fatal(err)
	}
	var ref reference
	if err := json.Unmarshal(data, &ref); err != nil {
		t.Fatal(err)
	}
	if ref.Version != "0.2.0" {
		t.Errorf("version %q", ref.Version)
	}

	names := map[string]bool{}
	for _, e := range ref.Entries {
		names[e.Name] = true
	}
	guides := guideIDs(t)
	categories := map[string]bool{}
	for _, c := range ref.Categories {
		categories[c.ID] = true
		for _, name := range c.Common {
			if !names[name] {
				t.Errorf("category %s lists %q, which is not a command", c.ID, name)
			}
		}
	}
	for _, e := range ref.Entries {
		if !categories[e.Category] {
			t.Errorf("%s has no category; add it to refCategoryOf", e.Name)
		}
		if e.Since == "" {
			t.Errorf("%s has no release; add it to refSince", e.Name)
		}
		for _, see := range e.SeeAlso {
			switch {
			case strings.HasPrefix(see, "guide:"):
				if !guides[strings.TrimPrefix(see, "guide:")] {
					t.Errorf("%s: see_also %s has no guide in docs/site/guides", e.Name, see)
				}
			case strings.HasPrefix(see, "r:"), strings.HasPrefix(see, "py:"):
			case !names[see]:
				t.Errorf("%s: see_also %q is not a command", e.Name, see)
			}
		}
	}
	for name := range refCategoryOf {
		if !names[name] {
			t.Errorf("refCategoryOf lists %q, which is not a command", name)
		}
	}

	// The reference shows the agent-facing details.
	var publish refEntry
	for _, e := range ref.Entries {
		if e.Name == "publish" {
			publish = e
		}
	}
	text, _ := json.Marshal(publish)
	for _, want := range []string{`"PATH"`, `--include-private`, `"env":"FW_CLOUD_URL"`, `Exit codes`, `private_url`} {
		if !strings.Contains(string(text), want) {
			t.Errorf("publish entry lacks %s:\n%s", want, text)
		}
	}
}

// guideIDs reads the id of every guide in docs/site/guides.
func guideIDs(t *testing.T) map[string]bool {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("..", "..", "docs", "site", "guides", "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(data), "\n") {
			if id, ok := strings.CutPrefix(line, "id: "); ok {
				ids[strings.TrimSpace(id)] = true
				break
			}
		}
	}
	return ids
}
