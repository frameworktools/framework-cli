package skillsync

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/frameworktools/framework-cli/internal/cloud"
)

func check(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Error(err)
	}
}

func TestSharedHash(t *testing.T) {
	data, err := os.ReadFile("testdata/skill-bundle-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Bundle []File `json:"bundle"`
		Hash   string `json:"hash"`
	}
	check(t, json.Unmarshal(data, &f))
	hash, err := Hash(f.Bundle)
	if err != nil || hash != f.Hash {
		t.Fatalf("hash %s expected %s: %v", hash, f.Hash, err)
	}
	for _, path := range []string{"../escape", ".env", "x/.env.local", "secret.key", "SKILL.md/child"} {
		files := append(append([]File{}, f.Bundle...), File{Path: path})
		if Validate(files) == nil {
			t.Fatalf("accepted %s", path)
		}
	}
}
func TestSyncRoundTripAndConflict(t *testing.T) {
	root := t.TempDir()
	relative := ".agents/skills/example"
	target := filepath.Join(root, filepath.FromSlash(relative))
	check(t, os.MkdirAll(target, 0755))
	check(t, os.WriteFile(filepath.Join(target, "SKILL.md"), []byte("---\nname: example\ndescription: Examples\n---\nFirst\n"), 0644))
	var head Snapshot
	writes := 0
	receipts := map[string]Snapshot{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Error("missing auth")
		}
		if r.Method == "GET" {
			check(t, json.NewEncoder(w).Encode(head))
			return
		}
		var body struct {
			RequestID string `json:"request_id"`
			Expected  int    `json:"expected_version"`
			Bundle    []File `json:"bundle"`
		}
		check(t, json.NewDecoder(r.Body).Decode(&body))
		if old, ok := receipts[body.RequestID]; ok {
			check(t, json.NewEncoder(w).Encode(old))
			return
		}
		if body.Expected != head.Version {
			w.WriteHeader(409)
			check(t, json.NewEncoder(w).Encode(map[string]string{"message": "conflict"}))
			return
		}
		writes++
		head = Snapshot{ID: "private-id", Version: head.Version + 1, Bundle: body.Bundle}
		head.Hash, _ = Hash(head.Bundle)
		receipts[body.RequestID] = head
		check(t, json.NewEncoder(w).Encode(head))
	}))
	defer server.Close()
	client := cloud.NewClient(server.URL, "token")
	opts := Options{Root: root, Path: relative, Variant: "r", DryRun: true}
	result, err := Sync(context.Background(), client, opts)
	if err != nil || result.Action != "push" || writes != 0 {
		t.Fatalf("dry run %+v %v", result, err)
	}
	if _, err = os.Stat(filepath.Join(root, ".framework/skills.lock.json")); !os.IsNotExist(err) {
		t.Fatal("dry run wrote state")
	}
	opts.DryRun = false
	result, err = Sync(context.Background(), client, opts)
	if err != nil || result.Version != 1 {
		t.Fatalf("push %+v %v", result, err)
	}
	remote := append([]File{}, head.Bundle...)
	remote[0].Content = base64.StdEncoding.EncodeToString([]byte("Web change\n"))
	head.Bundle = remote
	head.Version++
	head.Hash, _ = Hash(remote)
	result, err = Sync(context.Background(), client, opts)
	if err != nil || result.Action != "pull" {
		t.Fatalf("pull %+v %v", result, err)
	}
	raw, _ := os.ReadFile(filepath.Join(target, "SKILL.md"))
	if string(raw) != "Web change\n" {
		t.Fatal("pull did not install exact bytes")
	}
	check(t, os.WriteFile(filepath.Join(target, "SKILL.md"), []byte("Local change\n"), 0644))
	remote = append([]File{}, remote...)
	remote[0].Content = base64.StdEncoding.EncodeToString([]byte("Another web change\n"))
	head.Bundle = remote
	head.Version++
	head.Hash, _ = Hash(remote)
	result, err = Sync(context.Background(), client, opts)
	if err != nil || result.Action != "conflict" {
		t.Fatalf("conflict %+v %v", result, err)
	}
	raw, _ = os.ReadFile(filepath.Join(target, "SKILL.md"))
	if string(raw) != "Local change\n" {
		t.Fatal("conflict overwrote local bytes")
	}
	opts.Resolve = "local"
	result, err = Sync(context.Background(), client, opts)
	if err != nil || result.Action != "push" || head.Version != 4 {
		t.Fatalf("resolve %+v %v", result, err)
	}
}
func TestInstallBinaryAndDeletion(t *testing.T) {
	root := t.TempDir()
	data, _ := os.ReadFile("testdata/skill-bundle-v1.json")
	var f struct {
		Bundle []File `json:"bundle"`
	}
	check(t, json.Unmarshal(data, &f))
	if err := install(root, []string{".agents/skills/example"}, f.Bundle, ""); err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(root, ".agents/skills/example")
	raw, _ := os.ReadFile(filepath.Join(folder, "scripts/run.bin"))
	if len(raw) != 5 || raw[1] != 255 {
		t.Fatal("binary changed")
	}
	check(t, os.WriteFile(filepath.Join(folder, ".env"), []byte("local secret"), 0600))
	old, _ := Hash(f.Bundle)
	files := []File{}
	for _, file := range f.Bundle {
		if file.Path != "scripts/run.bin" {
			files = append(files, file)
		}
	}
	if err := install(root, []string{".agents/skills/example"}, files, old); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(folder, "scripts/run.bin")); !os.IsNotExist(err) {
		t.Fatal("file not removed")
	}
	if _, err := os.Stat(filepath.Join(folder, ".env")); err != nil {
		t.Fatal("excluded local material was deleted")
	}
}
