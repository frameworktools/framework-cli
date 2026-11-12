// Package skillsync implements the skill bundle v1 protocol used by R and Python.
package skillsync

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"github.com/frameworktools/framework-cli/internal/cloud"
)

type File struct {
	Path       string `json:"path"`
	Content    string `json:"content"`
	Executable bool   `json:"executable"`
}
type Snapshot struct {
	ID      string `json:"skill_id"`
	Version int    `json:"version"`
	Hash    string `json:"hash"`
	Bundle  []File `json:"bundle"`
	Variant string `json:"variant"`
	System  bool   `json:"system"`
}
type Origin struct {
	ID   string `json:"skill_id"`
	Hash string `json:"hash"`
}
type Pending struct {
	RequestID string `json:"request_id"`
	Hash      string `json:"hash"`
	Basis     string `json:"basis"`
}
type Entry struct {
	Paths   []string `json:"paths"`
	Variant string   `json:"variant"`
	Server  string   `json:"server,omitempty"`
	ID      string   `json:"skill_id,omitempty"`
	Version int      `json:"version,omitempty"`
	Hash    string   `json:"hash,omitempty"`
	Origin  *Origin  `json:"origin,omitempty"`
	Pending *Pending `json:"pending,omitempty"`
}
type State struct {
	Schema int               `json:"schema"`
	Skills map[string]*Entry `json:"skills"`
}
type Options struct {
	Root, Path, Direction, ID, Variant, Resolve string
	DryRun                                      bool
}
type Result struct {
	Action     string   `json:"action"`
	ID         string   `json:"skill_id,omitempty"`
	Version    int      `json:"version,omitempty"`
	LocalHash  string   `json:"local_hash,omitempty"`
	RemoteHash string   `json:"remote_hash,omitempty"`
	Paths      []string `json:"paths"`
	// Backups lists where a pull moved the replaced folders, relative to
	// the project.
	Backups []string `json:"backups,omitempty"`
}

func excluded(s string) bool {
	s = strings.ToLower(s)
	switch s {
	case ".git", ".framework", ".framework-skill.json", "__pycache__", "node_modules", ".env", "id_rsa":
		return true
	}
	return strings.HasPrefix(s, ".env.") || strings.HasSuffix(s, ".pem") || strings.HasSuffix(s, ".key")
}

var pathPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`)

func Validate(files []File) error {
	if len(files) > 256 {
		return fmt.Errorf("a skill supports at most 256 files")
	}
	seen := map[string]bool{}
	total := 0
	skill := false
	for _, f := range files {
		if len(f.Path) > 240 || !pathPattern.MatchString(f.Path) {
			return fmt.Errorf("invalid skill path %q: file names in a skill use letters, digits, dots, dashes, and underscores", f.Path)
		}
		for _, part := range strings.Split(f.Path, "/") {
			if part == "." || part == ".." || excluded(part) {
				return fmt.Errorf("excluded skill path %q", f.Path)
			}
		}
		lower := strings.ToLower(f.Path)
		if seen[lower] {
			return fmt.Errorf("duplicate skill path")
		}
		seen[lower] = true
		raw, err := base64.StdEncoding.DecodeString(f.Content)
		if err != nil {
			return err
		}
		total += len(raw)
		if len(raw) > 4<<20 || total > 16<<20 {
			return fmt.Errorf("skill bundle exceeds the size limit")
		}
		if f.Path == "SKILL.md" {
			if f.Executable {
				return fmt.Errorf("SKILL.md cannot be executable")
			}
			skill = true
		}
	}
	if !skill {
		return fmt.Errorf("SKILL.md is required")
	}
	for a := range seen {
		for b := range seen {
			if strings.HasPrefix(b, a+"/") {
				return fmt.Errorf("file and directory path collision")
			}
		}
	}
	return nil
}
func Hash(files []File) (string, error) {
	if err := Validate(files); err != nil {
		return "", err
	}
	files = append([]File(nil), files...)
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	var manifest strings.Builder
	manifest.WriteString("framework-skill-bundle-v1\n")
	for _, f := range files {
		raw, _ := base64.StdEncoding.DecodeString(f.Content)
		sum := sha256.Sum256(raw)
		mode := "644"
		if f.Executable {
			mode = "755"
		}
		fmt.Fprintf(&manifest, "%s\t%x\t%s\t%d\n", f.Path, sum, mode, len(raw))
	}
	sum := sha256.Sum256([]byte(manifest.String()))
	return hex.EncodeToString(sum[:]), nil
}
func safePath(root, relative string) (string, error) {
	if relative == "" || filepath.IsAbs(relative) || strings.Contains(relative, `\`) {
		return "", fmt.Errorf("skill paths must be project-relative")
	}
	p := root
	for _, part := range strings.Split(relative, "/") {
		if part == "." || part == ".." || part == "" {
			return "", fmt.Errorf("invalid skill path")
		}
		p = filepath.Join(p, part)
		info, err := os.Lstat(p)
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("skill paths cannot contain symlinks")
		}
		if err != nil && !os.IsNotExist(err) {
			return "", err
		}
	}
	return p, nil
}
func Scan(root string) ([]File, error) {
	reader, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	files := []File{}
	executablePaths := map[string]bool{}
	if runtime.GOOS == "windows" {
		raw, readErr := reader.ReadFile(".framework-skill.json")
		if readErr == nil {
			var metadata struct {
				ExecutablePaths []string `json:"executable_paths"`
			}
			if err = json.Unmarshal(raw, &metadata); err != nil {
				return nil, err
			}
			for _, name := range metadata.ExecutablePaths {
				executablePaths[name] = true
			}
		} else if !os.IsNotExist(readErr) {
			return nil, readErr
		}
	}
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("skill symlinks are unsupported")
		}
		if p == root {
			return nil
		}
		if excluded(d.Name()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() > 4<<20 {
			return fmt.Errorf("invalid or oversized skill file")
		}
		relative, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		data, err := reader.ReadFile(relative)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		executable := info.Mode()&0111 != 0
		if runtime.GOOS == "windows" {
			executable = executablePaths[filepath.ToSlash(rel)]
		}
		files = append(files, File{filepath.ToSlash(rel), base64.StdEncoding.EncodeToString(data), executable})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, Validate(files)
}
func uuid() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}
func load(root string) (State, error) {
	state := State{Schema: 1, Skills: map[string]*Entry{}}
	path, err := safePath(root, ".framework/skills.lock.json")
	if err != nil {
		return state, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	err = json.Unmarshal(data, &state)
	if err == nil && (state.Schema != 1 || state.Skills == nil) {
		err = fmt.Errorf("unsupported skill lock file")
	}
	return state, err
}
func save(root string, state State) error {
	path, err := safePath(root, ".framework/skills.lock.json")
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	if err = private(root); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".skill-state-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(append(data, '\n')); err != nil {
		return errors.Join(err, f.Close())
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

type swap struct {
	target, stage, backup string
	existed               bool
}

func install(root string, paths []string, files []File, expected string) error {
	return installInto(nil, root, paths, files, expected)
}

// installInto is install that also appends to backups where it moved each
// replaced folder, relative to root. The list means nothing after an error,
// since the backups are then moved back.
func installInto(backups *[]string, root string, paths []string, files []File, expected string) (err error) {
	if err = Validate(files); err != nil {
		return
	}
	swaps := []swap{}
	defer func() {
		if err != nil {
			for i := len(swaps) - 1; i >= 0; i-- {
				s := swaps[i]
				if _, e := os.Stat(s.backup); e == nil {
					if cleanup := os.RemoveAll(s.target); cleanup != nil {
						err = errors.Join(err, cleanup)
						continue
					}
					if rollback := os.Rename(s.backup, s.target); rollback != nil {
						err = errors.Join(err, fmt.Errorf("restore backup %s: %w", s.backup, rollback))
					}
				} else if _, e = os.Stat(s.stage); os.IsNotExist(e) && !s.existed {
					err = errors.Join(err, os.RemoveAll(s.target))
				}
				err = errors.Join(err, os.RemoveAll(s.stage))
			}
		}
	}()
	for _, relative := range paths {
		target, e := safePath(root, relative)
		if e != nil {
			return e
		}
		existed := false
		if _, e = os.Stat(target); e == nil {
			existed = true
			old, e := Scan(target)
			if e != nil {
				return e
			}
			h, _ := Hash(old)
			if h != expected {
				return fmt.Errorf("local skill changed during sync")
			}
		} else if !os.IsNotExist(e) {
			return e
		}
		if e = os.MkdirAll(filepath.Dir(target), 0755); e != nil {
			return e
		}
		stage, e := os.MkdirTemp(filepath.Dir(target), ".skill-stage-")
		if e != nil {
			return e
		}
		backup, e := safePath(root, ".framework/skills-backups/"+uuid())
		if e != nil {
			return e
		}
		if existed {
			if e = private(root); e != nil {
				return e
			}
			if e = os.MkdirAll(filepath.Dir(backup), 0700); e != nil {
				return e
			}
		}
		s := swap{target, stage, backup, existed}
		swaps = append(swaps, s)
		// Preserve excluded local material. Symlinks anywhere in the tree fail closed.
		if existed {
			reader, openErr := os.OpenRoot(target)
			if openErr != nil {
				return openErr
			}
			e = filepath.WalkDir(target, func(p string, d fs.DirEntry, e error) error {
				if e != nil {
					return e
				}
				if d.Type()&os.ModeSymlink != 0 {
					return fmt.Errorf("skill symlinks are unsupported")
				}
				rel, _ := filepath.Rel(target, p)
				dest := filepath.Join(stage, rel)
				if d.IsDir() {
					return os.MkdirAll(dest, 0755)
				}
				info, e := d.Info()
				if e != nil {
					return e
				}
				if !info.Mode().IsRegular() {
					return fmt.Errorf("only regular files are supported")
				}
				relative, e := filepath.Rel(target, p)
				if e != nil {
					return e
				}
				data, e := reader.ReadFile(relative)
				if e != nil {
					return e
				}
				return os.WriteFile(dest, data, info.Mode().Perm())
			})
			closeErr := reader.Close()
			if e != nil || closeErr != nil {
				return errors.Join(e, closeErr)
			}
			old, _ := Scan(target)
			keep := map[string]bool{}
			for _, f := range files {
				keep[f.Path] = true
			}
			for _, f := range old {
				if !keep[f.Path] {
					removed := filepath.Join(stage, filepath.FromSlash(f.Path))
					if e = os.Remove(removed); e != nil {
						return e
					}
					for parent := filepath.Dir(removed); parent != stage; parent = filepath.Dir(parent) {
						entries, e := os.ReadDir(parent)
						if e != nil || len(entries) > 0 {
							break
						}
						if e = os.Remove(parent); e != nil {
							break
						}
					}
				}
			}
		}
		for _, f := range files {
			dest, e := safePath(stage, f.Path)
			if e != nil {
				return e
			}
			if e = os.MkdirAll(filepath.Dir(dest), 0755); e != nil {
				return e
			}
			raw, _ := base64.StdEncoding.DecodeString(f.Content)
			mode := os.FileMode(0644)
			if f.Executable {
				mode = 0755
			}
			if e = os.WriteFile(dest, raw, mode); e != nil {
				return e
			}
			if e = os.Chmod(dest, mode); e != nil {
				return e
			}
		}
		if runtime.GOOS == "windows" {
			executablePaths := []string{}
			for _, f := range files {
				if f.Executable {
					executablePaths = append(executablePaths, f.Path)
				}
			}
			modePath := filepath.Join(stage, ".framework-skill.json")
			if len(executablePaths) > 0 {
				raw, marshalErr := json.Marshal(map[string]any{"executable_paths": executablePaths})
				if marshalErr != nil {
					return marshalErr
				}
				if e = os.WriteFile(modePath, raw, 0600); e != nil {
					return e
				}
			} else if _, statErr := os.Stat(modePath); statErr == nil {
				if e = os.Remove(modePath); e != nil {
					return e
				}
			}
		}
		staged, e := Scan(stage)
		if e != nil {
			return e
		}
		a, _ := Hash(staged)
		b, _ := Hash(files)
		if a != b {
			return fmt.Errorf("staged skill verification failed")
		}
	}
	for _, s := range swaps {
		if s.existed {
			current, e := Scan(s.target)
			if e != nil {
				return e
			}
			h, _ := Hash(current)
			if h != expected {
				return fmt.Errorf("local skill changed during sync")
			}
		}
		if s.existed {
			if err = os.Rename(s.target, s.backup); err != nil {
				return
			}
			if backups != nil {
				rel, _ := filepath.Rel(root, s.backup)
				*backups = append(*backups, filepath.ToSlash(rel))
			}
		}
		if err = os.Rename(s.stage, s.target); err != nil {
			return
		}
	}
	return nil
}

func Sync(ctx context.Context, client *cloud.Client, opts Options) (Result, error) {
	result := Result{}
	if opts.Direction == "" {
		opts.Direction = "auto"
	}
	if opts.Variant == "" {
		opts.Variant = "r"
	}
	if !strings.Contains("|auto|push|pull|status|", "|"+opts.Direction+"|") || !strings.Contains("|r|python|stata|", "|"+opts.Variant+"|") {
		return result, fmt.Errorf("invalid skill sync direction or variant")
	}
	if opts.Resolve != "" && opts.Resolve != "local" && opts.Resolve != "remote" {
		return result, fmt.Errorf("invalid conflict resolution")
	}
	root, err := filepath.Abs(opts.Root)
	if err != nil {
		return result, err
	}
	// A dry run or status check writes nothing, not even the lock.
	if !opts.DryRun && opts.Direction != "status" {
		lock, err := safePath(root, ".framework/skills.sync.lock")
		if err != nil {
			return result, err
		}
		if err = os.MkdirAll(filepath.Dir(lock), 0755); err != nil {
			return result, err
		}
		if err = os.Mkdir(lock, 0700); err != nil {
			return result, fmt.Errorf("skill sync is locked; if the previous process stopped, remove .framework/skills.sync.lock and retry")
		}
		defer os.Remove(lock)
	}
	state, err := load(root)
	if err != nil {
		return result, err
	}
	key := opts.Path
	entry := &Entry{Paths: []string{opts.Path}, Variant: opts.Variant}
	for k, e := range state.Skills {
		for _, p := range e.Paths {
			if p == opts.Path {
				key, entry = k, e
			}
		}
	}
	server := strings.TrimRight(client.URL, "/")
	if entry.Server != "" && entry.Server != server {
		return result, fmt.Errorf("skill tracks another server")
	}
	if opts.ID != "" && entry.ID != "" && opts.ID != entry.ID {
		return result, fmt.Errorf("skill identity mismatch")
	}
	var files []File
	localHash := ""
	for i, relative := range entry.Paths {
		target, e := safePath(root, relative)
		if e != nil {
			return result, e
		}
		var b []File
		h := ""
		if _, e = os.Stat(target); e == nil {
			b, e = Scan(target)
			if e != nil {
				return result, e
			}
			h, _ = Hash(b)
		} else if !os.IsNotExist(e) {
			return result, e
		}
		if i == 0 {
			files, localHash = b, h
		} else if h != localHash {
			return result, fmt.Errorf("assistant skill copies diverged; reconcile aliases before syncing")
		}
	}
	id := entry.ID
	if id == "" {
		id = opts.ID
	}
	remote := Snapshot{}
	hasRemote := false
	route := ""
	if id != "" {
		route = "/api/v1/skills/sync/" + url.PathEscape(id) + "?variant=" + entry.Variant
	} else if entry.Origin != nil {
		route = "/api/v1/skills/sync/" + url.PathEscape(entry.Origin.ID) + "?variant=" + entry.Variant + "&hash=" + entry.Origin.Hash
	}
	if route != "" {
		err = client.Do(ctx, "GET", route, nil, &remote)
		switch {
		case err == nil:
			h, e := Hash(remote.Bundle)
			if e != nil || h != remote.Hash {
				return result, fmt.Errorf("cloud skill verification failed")
			}
			hasRemote = true
		case id == "" && cloud.StatusOf(err) == 409:
			// Cloud never published this built-in copy (fw shipped its own
			// text): an edited one is pushed as a new skill of your own
			remote = Snapshot{}
		default:
			return result, err
		}
	}
	base := ""
	if entry.ID != "" {
		base = entry.Hash
	} else if entry.Origin != nil {
		base = entry.Origin.Hash
	}
	action := "conflict"
	switch {
	case !hasRemote:
		switch {
		case files == nil:
			action = "missing"
		case entry.ID == "" && base != "" && localHash == base:
			action = "unchanged"
		default:
			action = "push"
		}
	case localHash == remote.Hash:
		if entry.ID == "" {
			action = "adopt"
		} else {
			action = "unchanged"
		}
	case localHash == "":
		if base == "" && opts.ID != "" {
			action = "pull"
		}
	case base == "":
	case localHash == base:
		action = "pull"
	case remote.Hash == base:
		action = "push"
	}
	if action == "conflict" && opts.Resolve != "" && hasRemote {
		if opts.Resolve == "remote" {
			action = "pull"
		} else if opts.Resolve == "local" && files != nil {
			action = "push"
		}
	}
	if (opts.Direction == "push" || opts.Direction == "pull") && (action == "push" || action == "pull") && opts.Direction != action {
		action = "blocked"
	}
	result = Result{Action: action, ID: remote.ID, Version: remote.Version, LocalHash: localHash, RemoteHash: remote.Hash, Paths: entry.Paths}
	if opts.DryRun || opts.Direction == "status" || action == "conflict" || action == "blocked" || action == "missing" {
		return result, nil
	}
	if action == "push" {
		expected := entry.Version
		if expected == 0 || (opts.Resolve == "local" && !remote.System) {
			expected = remote.Version
		}
		basis := fmt.Sprintf("%d:%s:%s", expected, remote.ID, entry.Variant)
		if entry.Pending == nil || entry.Pending.Hash != localHash || entry.Pending.Basis != basis {
			entry.Pending = &Pending{uuid(), localHash, basis}
		}
		state.Skills[key] = entry
		if err = save(root, state); err != nil {
			return result, err
		}
		var identity any
		if hasRemote {
			identity = remote.ID
		}
		body := map[string]any{"request_id": entry.Pending.RequestID, "skill_id": identity, "expected_version": expected, "variant": entry.Variant, "bundle": files, "client": map[string]string{"app": "framework-cli"}}
		if err = client.Do(ctx, "POST", "/api/v1/skills/sync", body, &remote); err != nil {
			return result, err
		}
		hasRemote = true
	}
	if hasRemote {
		h, e := Hash(remote.Bundle)
		if e != nil || h != remote.Hash {
			return result, fmt.Errorf("cloud skill verification failed")
		}
		if action == "pull" {
			var backups []string
			if err = installInto(&backups, root, entry.Paths, remote.Bundle, localHash); err != nil {
				return result, err
			}
			result.Backups = backups
		}
		entry.Server, entry.ID, entry.Version, entry.Hash, entry.Pending = server, remote.ID, remote.Version, remote.Hash, nil
		state.Skills[key] = entry
		if err = save(root, state); err != nil {
			return result, err
		}
		result.ID, result.Version = remote.ID, remote.Version
	}
	return result, nil
}

func private(root string) error {
	path, err := safePath(root, ".framework/.gitignore")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	text := string(data)
	for _, line := range []string{"skills-backups/", "skills.sync.lock/"} {
		found := false
		for _, existing := range strings.Split(text, "\n") {
			if existing == line {
				found = true
			}
		}
		if !found {
			text = strings.TrimRight(text, "\n") + "\n" + line + "\n"
		}
	}
	return os.WriteFile(path, []byte(text), 0644)
}

// InstallSource records the exact scaffolded source before any local edits.
func InstallSource(root, relative, canonical, variant string, files []File, snapshot *Snapshot, server string) error {
	lock, err := safePath(root, ".framework/skills.sync.lock")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(lock), 0755); err != nil {
		return err
	}
	if err = os.Mkdir(lock, 0700); err != nil {
		return fmt.Errorf("skill sync is locked")
	}
	defer os.Remove(lock)
	state, err := load(root)
	if err != nil {
		return err
	}
	hash, err := Hash(files)
	if err != nil {
		return err
	}
	key := relative
	if canonical != "" {
		key = "package:" + strings.TrimPrefix(canonical, "framework:")
	}
	if snapshot != nil {
		key = "account:" + server + ":" + snapshot.ID + ":" + variant
	}
	for existingKey, existing := range state.Skills {
		for _, p := range existing.Paths {
			if p == relative {
				key = existingKey
			}
		}
	}
	entry := state.Skills[key]
	if entry == nil {
		entry = &Entry{Paths: []string{}, Variant: variant}
	}
	if snapshot != nil && hash != snapshot.Hash {
		return fmt.Errorf("cloud skill verification failed")
	}
	if canonical != "" && entry.ID != "" && !strings.HasPrefix(entry.ID, "framework:") {
		return nil
	}
	target, err := safePath(root, relative)
	if err != nil {
		return err
	}
	current := ""
	if _, err = os.Stat(target); err == nil {
		local, e := Scan(target)
		if e != nil {
			return e
		}
		current, _ = Hash(local)
		base := entry.Hash
		if base == "" {
			base = hash
		}
		if current != base {
			return fmt.Errorf("preserved edited skill %s", relative)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if current != hash {
		if err = install(root, []string{relative}, files, current); err != nil {
			return err
		}
	}
	found := false
	for _, p := range entry.Paths {
		if p == relative {
			found = true
		}
	}
	if !found {
		entry.Paths = append(entry.Paths, relative)
	}
	entry.Hash = hash
	if canonical != "" {
		entry.Origin = &Origin{canonical, hash}
	}
	if snapshot != nil {
		if hash != snapshot.Hash {
			return fmt.Errorf("cloud skill verification failed")
		}
		entry.ID, entry.Version, entry.Server = snapshot.ID, snapshot.Version, server
	}
	state.Skills[key] = entry
	return save(root, state)
}
