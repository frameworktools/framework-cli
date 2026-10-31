package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestDocsSite checks docs/site in the repository and builds the archive,
// so a broken guide fails CI before a release.
func TestDocsSite(t *testing.T) {
	data, err := build(filepath.Join("..", "..", "docs", "site"), "0.0.0-test")
	if err != nil {
		t.Fatal(err)
	}
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	var names []string
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, h.Name)
	}
	sort.Strings(names)
	got := strings.Join(names, "\n")
	for _, want := range []string{"docs-site/site.yml", "docs-site/reference.json", "docs-site/guides/agents.md"} {
		if !strings.Contains(got, want) {
			t.Errorf("archive lacks %s:\n%s", want, got)
		}
	}
	for _, n := range names {
		if !strings.HasPrefix(n, "docs-site/") {
			t.Errorf("%s is outside docs-site/", n)
		}
	}
}

func TestFrontMatterProblems(t *testing.T) {
	if _, _, err := splitFrontMatter("# Title\n"); err == nil {
		t.Error("accepted a guide without front matter")
	}
	fm, body, err := splitFrontMatter("---\nid: x\ntitle: X\ndescription: d\nsection: guides\nposition: 1\n---\n\n## A\n")
	if err != nil || fm.ID != "x" || *fm.Position != 1 || !strings.Contains(body, "## A") {
		t.Errorf("%+v %q %v", fm, body, err)
	}
}
