package publish

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBundleAndVersions(t *testing.T) {
	src := filepath.Join(t.TempDir(), "model.json")
	raw := `{"schema":"framework.artifact/v1","kind":"model","source":{"language":"r"},"title":"</script><script>alert(1)</script>"}`
	if err := os.WriteFile(src, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	build := func() *Bundle {
		b, e := Build(map[string]string{src: "model.json"}, "fuel-model", "Fuel <model>", "", "model", "model.json", "fw test", `<title>__FRAMEWORK_TITLE__</title><script type="application/json">__FRAMEWORK_ARTIFACT__</script>`)
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { _ = os.RemoveAll(b.Dir) })
		return b
	}
	b := build()
	if b.Manifest.Representation == nil || *b.Manifest.Representation != "representation.json" {
		t.Fatal(b.Manifest)
	}
	page, _ := os.ReadFile(filepath.Join(b.Dir, "index.html"))
	if strings.Contains(string(page), "</script><script>") || !strings.Contains(string(page), "Fuel &lt;model&gt;") {
		t.Fatal(string(page))
	}
	for _, f := range b.Manifest.Files {
		data, e := os.ReadFile(filepath.Join(b.Dir, f.Path))
		if e != nil || Hash(data) != f.SHA256 || int64(len(data)) != f.Bytes {
			t.Fatalf("invalid file hash: %+v", f)
		}
	}
	root := t.TempDir()
	r, e := Folder(b, root, "https://example.org/research")
	if e != nil || r.Version != 1 || r.VersionURL != "https://example.org/research/fuel-model/v/1/" {
		t.Fatal(r, e)
	}
	// Static shell embeds timestamp, so use the same built bundle to test idempotency.
	r, e = Folder(b, root, "")
	if e != nil || r.Version != 1 {
		t.Fatal(r, e)
	}
	if err := os.WriteFile(src, []byte(`{"schema":"framework.artifact/v1","kind":"model","value":2}`), 0600); err != nil {
		t.Fatal(err)
	}
	r, e = Folder(build(), root, "")
	if e != nil || r.Version != 2 {
		t.Fatal(r, e)
	}
	archive, _ := os.ReadFile(filepath.Join(root, "fuel-model/v/1/model.json"))
	if string(archive) != raw {
		t.Fatal("archive changed")
	}
	var index []Manifest
	data, _ := os.ReadFile(filepath.Join(root, "index.json"))
	if json.Unmarshal(data, &index) != nil || len(index) != 1 {
		t.Fatal(string(data))
	}
}
func TestSlugAndSymlinks(t *testing.T) {
	for input, want := range map[string]string{"Fuel_Model.v2": "fuel-model-v2", "  A / B  ": "a-b", "_site": "site"} {
		if got := Slug(input); got != want {
			t.Fatal(got, want)
		}
	}
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Skip(err)
	}
	_, err := Folder(&Bundle{Dir: t.TempDir(), Manifest: Manifest{Slug: "escape"}}, root, "")
	if err == nil {
		t.Fatal("followed destination symlink")
	}
}
