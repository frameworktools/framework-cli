package skillsync

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frameworktools/framework-cli/internal/cloud"
)

func TestCRLFHash(t *testing.T) {
	data, err := os.ReadFile("testdata/skill-bundle-crlf.json")
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		LF           []File `json:"lf"`
		CRLF         []File `json:"crlf"`
		Hash         string `json:"hash"`
		BinaryLF     []File `json:"binary_lf"`
		BinaryLFHash string `json:"binary_lf_hash"`
	}
	check(t, json.Unmarshal(data, &f))
	for name, c := range map[string]struct {
		files []File
		want  string
	}{"lf": {f.LF, f.Hash}, "crlf": {f.CRLF, f.Hash}, "binary_lf": {f.BinaryLF, f.BinaryLFHash}} {
		if hash, err := Hash(c.files); err != nil || hash != c.want {
			t.Errorf("%s: hash %s expected %s: %v", name, hash, c.want, err)
		}
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
	if err != nil || result.Action != "push" || head.Version != 4 || len(result.Backups) != 0 {
		t.Fatalf("resolve %+v %v", result, err)
	}

	// --resolve remote replaces the local files and names the backup
	check(t, os.WriteFile(filepath.Join(target, "SKILL.md"), []byte("Second local change\n"), 0644))
	remote = append([]File{}, head.Bundle...)
	remote[0].Content = base64.StdEncoding.EncodeToString([]byte("Third web change\n"))
	head.Bundle = remote
	head.Version++
	head.Hash, _ = Hash(remote)
	opts.Resolve = "remote"
	result, err = Sync(context.Background(), client, opts)
	if err != nil || result.Action != "pull" || len(result.Backups) != 1 || !strings.HasPrefix(result.Backups[0], ".framework/skills-backups/") {
		t.Fatalf("resolve remote %+v %v", result, err)
	}
	raw, _ = os.ReadFile(filepath.Join(root, filepath.FromSlash(result.Backups[0]), "SKILL.md"))
	if string(raw) != "Second local change\n" {
		t.Errorf("backup holds %q", raw)
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

func TestSyncDryRunWritesNothingAndErrorsNameFiles(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "skills", "x")
	check(t, os.MkdirAll(target, 0755))
	check(t, os.WriteFile(filepath.Join(target, "SKILL.md"), []byte("---\nname: x\n---\n"), 0644))
	head := Snapshot{ID: "me:x", Version: 3, Bundle: []File{{Path: "SKILL.md", Content: base64.StdEncoding.EncodeToString([]byte("remote\n"))}}}
	head.Hash, _ = Hash(head.Bundle)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		check(t, json.NewEncoder(w).Encode(head))
	}))
	defer server.Close()
	client := cloud.NewClient(server.URL, "token")
	for _, opts := range []Options{{Root: root, Path: "skills/x", ID: "me:x", DryRun: true}, {Root: root, Path: "skills/x", ID: "me:x", Direction: "status"}} {
		result, err := Sync(context.Background(), client, opts)
		if err != nil || result.Version != 3 || result.ID != "me:x" {
			t.Errorf("%+v: %+v %v", opts, result, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".framework")); !os.IsNotExist(err) {
		t.Error("a dry run or status check created .framework")
	}
	check(t, os.WriteFile(filepath.Join(target, "my notes.md"), []byte("x"), 0644))
	if _, err := Scan(target); err == nil || !strings.Contains(err.Error(), `"my notes.md"`) {
		t.Errorf("error does not name the file: %v", err)
	}
}

// A built-in skill whose shipped text Cloud never published: Cloud answers
// 409 to its origin, and an edited copy is pushed as a new skill.
func TestSyncPushesAnUnpublishedBuiltinAsNew(t *testing.T) {
	root := t.TempDir()
	relative := ".claude/skills/framework-stata"
	target := filepath.Join(root, filepath.FromSlash(relative))
	check(t, os.MkdirAll(target, 0755))
	check(t, os.WriteFile(filepath.Join(target, "SKILL.md"), []byte("---\nname: framework-stata\ndescription: Stata\n---\nShipped\n"), 0644))
	files, err := Scan(target)
	check(t, err)
	shipped, _ := Hash(files)
	writeRel(t, root, lockFile, `{"schema":1,"skills":{"package:framework-stata":{"paths":["`+relative+`"],"variant":"stata","hash":"`+shipped+
		`","origin":{"skill_id":"framework:framework-stata","hash":"`+shipped+`"}}}}`)
	var posts []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "GET" {
			w.WriteHeader(409)
			check(t, json.NewEncoder(w).Encode(map[string]string{"message": "The installed source version is unavailable."}))
			return
		}
		var body map[string]any
		check(t, json.NewDecoder(r.Body).Decode(&body))
		posts = append(posts, body)
		var bundle []File
		raw, _ := json.Marshal(body["bundle"])
		check(t, json.Unmarshal(raw, &bundle))
		snap := Snapshot{ID: "own-id", Version: 1, Bundle: bundle, Variant: "stata"}
		snap.Hash, _ = Hash(bundle)
		check(t, json.NewEncoder(w).Encode(snap))
	}))
	defer server.Close()
	client := cloud.NewClient(server.URL, "token")
	opts := Options{Root: root, Path: relative, Direction: "push", Variant: "stata"}
	result, err := Sync(context.Background(), client, opts)
	if err != nil || result.Action != "unchanged" || len(posts) != 0 {
		t.Fatalf("unedited: %+v %v", result, err)
	}
	check(t, os.WriteFile(filepath.Join(target, "SKILL.md"), []byte("---\nname: framework-stata\ndescription: Stata\n---\nMine\n"), 0644))
	result, err = Sync(context.Background(), client, opts)
	if err != nil || result.Action != "push" || len(posts) != 1 || posts[0]["skill_id"] != nil || posts[0]["expected_version"] != float64(0) {
		t.Fatalf("edited: %+v %v %v", result, err, posts)
	}
	// The built-in becomes your own Cloud skill, in a v2 lock
	lock, err := ReadLock(root)
	if e := lock.Skills[server.URL+"/own-id@stata"]; err != nil || e == nil || e.Copies[relative].Hash == "" || len(lock.Skills) != 1 {
		t.Fatalf("the lock does not track the new skill: %+v %v", lock, err)
	}
	if !strings.Contains(readRel(t, root, lockFile), `"schema": 2`) {
		t.Error("the lock was not written as schema 2")
	}
}
