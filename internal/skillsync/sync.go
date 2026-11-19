// Package skillsync implements the skill bundle v1 protocol used by R and Python.
package skillsync

import (
	"bytes"
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
	"syscall"

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
	// KnownHashes is every version's hash of the skill for this variant,
	// when Cloud sends it.
	KnownHashes []string `json:"known_hashes,omitempty"`
}
type Pending struct {
	RequestID string `json:"request_id"`
	Hash      string `json:"hash"`
	Basis     string `json:"basis"`
}
type Options struct {
	Root, Path, Direction, ID, Variant, Resolve string
	DryRun                                      bool
	// ProjectToken, when set, names the project the push comes from: Cloud
	// then lists a pushed copy of a built-in skill in the project's setup.
	ProjectToken string
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

// hashBytes is a file's bytes as hashed. Text files hash with LF line
// endings, so a CRLF checkout (git's core.autocrlf on Windows) matches its LF
// twin. A file with a NUL byte is binary and hashes raw. R, Python, and Cloud
// normalize the same way.
func hashBytes(raw []byte) []byte {
	if bytes.IndexByte(raw, 0) >= 0 {
		return raw
	}
	return bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n"))
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
		raw = hashBytes(raw)
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
		if notThere(err) {
			// Nothing further down exists, so nothing there is a link
			return filepath.Join(root, filepath.FromSlash(relative)), nil
		}
		if err != nil {
			return "", err
		}
	}
	return p, nil
}

// notThere is a lookup error that means the path doesn't exist: missing,
// or under a file, as when an assistant folder was replaced by one.
func notThere(err error) bool {
	return err != nil && (os.IsNotExist(err) || errors.Is(err, syscall.ENOTDIR))
}

// windowsModes is true where file modes can't mark a file executable, so
// .framework-skill.json lists executable files instead. Tests switch it on.
var windowsModes = runtime.GOOS == "windows"

// readExecutablePaths reads .framework-skill.json's executable_paths: a JSON
// array, or a single string as older writers left it.
func readExecutablePaths(raw []byte) (map[string]bool, error) {
	var metadata struct {
		ExecutablePaths json.RawMessage `json:"executable_paths"`
	}
	if err := json.Unmarshal(raw, &metadata); err != nil {
		return nil, fmt.Errorf(".framework-skill.json: %w", err)
	}
	paths := map[string]bool{}
	if len(metadata.ExecutablePaths) == 0 || string(metadata.ExecutablePaths) == "null" {
		return paths, nil
	}
	var list []string
	if err := json.Unmarshal(metadata.ExecutablePaths, &list); err != nil {
		var one string
		if json.Unmarshal(metadata.ExecutablePaths, &one) != nil {
			return nil, fmt.Errorf(".framework-skill.json: executable_paths must be a list of file paths")
		}
		list = []string{one}
	}
	for _, name := range list {
		paths[name] = true
	}
	return paths, nil
}

func Scan(root string) ([]File, error) {
	reader, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	files := []File{}
	executablePaths := map[string]bool{}
	if windowsModes {
		raw, readErr := reader.ReadFile(".framework-skill.json")
		if readErr == nil {
			if executablePaths, err = readExecutablePaths(raw); err != nil {
				return nil, err
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
		if windowsModes {
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

type swap struct {
	target, stage, backup string
	existed               bool
}

// RestoreError is an install that failed after it moved the old copy aside,
// and then could not put it back. Target and Backup are project-relative.
type RestoreError struct {
	Target, Backup string
	Err            error
}

func (e *RestoreError) Error() string {
	return fmt.Sprintf("could not put %s back; the old copy is in %s: %v", e.Target, e.Backup, e.Err)
}

func (e *RestoreError) Unwrap() error { return e.Err }

// dirModeBits are the mode bits a rebuilt folder keeps: its permissions,
// and the setuid, setgid, and sticky bits.
const dirModeBits = os.ModePerm | os.ModeSetuid | os.ModeSetgid | os.ModeSticky

// errChanged is the installer declining: the copy it was to replace changed
// since the caller hashed it.
var errChanged = errors.New("local skill changed during sync")

// rename is os.Rename; tests replace it to fail.
var rename = os.Rename

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
					restore := os.RemoveAll(s.target)
					if restore == nil {
						restore = rename(s.backup, s.target)
					}
					if restore != nil {
						target, _ := filepath.Rel(root, s.target)
						backup, _ := filepath.Rel(root, s.backup)
						err = errors.Join(err, &RestoreError{Target: filepath.ToSlash(target), Backup: filepath.ToSlash(backup), Err: restore})
						err = errors.Join(err, os.RemoveAll(s.stage))
						continue
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
				return errChanged
			}
		} else if !os.IsNotExist(e) {
			return e
		}
		if e = os.MkdirAll(filepath.Dir(target), 0755); e != nil {
			return e
		}
		backup, e := safePath(root, ".framework/skills-backups/"+uuid())
		if e != nil {
			return e
		}
		stage, e := os.MkdirTemp(filepath.Dir(target), ".skill-stage-")
		if e != nil {
			return e
		}
		// Registered at once, so any later failure removes the stage
		s := swap{target, stage, backup, existed}
		swaps = append(swaps, s)
		if existed {
			if e = private(root); e != nil {
				return e
			}
			if e = os.MkdirAll(filepath.Dir(backup), 0700); e != nil {
				return e
			}
		}
		// Rebuilt folders keep their modes; a new skill folder gets 0755
		dirModes := map[string]os.FileMode{stage: 0755}
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
				info, e := d.Info()
				if e != nil {
					return e
				}
				if d.IsDir() {
					dirModes[dest] = info.Mode() & dirModeBits
					return os.MkdirAll(dest, 0700)
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
		if windowsModes {
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
		// Deepest first, so a read-only folder is set after its contents
		dirs := make([]string, 0, len(dirModes))
		for dir := range dirModes {
			dirs = append(dirs, dir)
		}
		sort.Slice(dirs, func(i, j int) bool { return len(dirs[i]) > len(dirs[j]) })
		for _, dir := range dirs {
			// Only onto a path that is still a folder: the new copy may have
			// emptied it away, or put a file in its place
			if info, statErr := os.Lstat(dir); statErr != nil || !info.IsDir() {
				continue
			}
			if e = os.Chmod(dir, dirModes[dir]); e != nil {
				return e
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
				return errChanged
			}
		}
		if s.existed {
			if err = rename(s.target, s.backup); err != nil {
				return
			}
			if backups != nil {
				rel, _ := filepath.Rel(root, s.backup)
				*backups = append(*backups, filepath.ToSlash(rel))
			}
		}
		if err = rename(s.stage, s.target); err != nil {
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
		release, err := lockSync(root)
		if err != nil {
			return result, err
		}
		defer release()
	}
	lock, _, err := loadLock(root)
	if err != nil {
		return result, err
	}
	before := lock.clone()
	// The path as given must hold no link before any case alias is
	// resolved: a symlinked skill is refused, as it always was
	if _, err = safePath(root, opts.Path); err != nil {
		return result, err
	}
	server := strings.TrimRight(client.URL, "/")
	disk := NewDisk(root)
	key := ""
	for _, k := range lock.keys() {
		for p := range lock.Skills[k].Copies {
			if disk.Same(p, opts.Path) {
				key = k
			}
		}
	}
	var entry *LockEntry
	if key != "" {
		entry = lock.Skills[key].clone()
	} else {
		spelled := diskSpelling(root, opts.Path)
		entry = &LockEntry{Identity: Identity{Kind: KindLocal, Variant: opts.Variant}, Known: []string{}, Copies: map[string]Record{spelled: {}}}
		key = entry.Identity.Key(spelled)
		if opts.ID != "" {
			// The ID's entry may track copies elsewhere: those are its copies,
			// and moving one here is fw skills move, never a join by ID
			for _, k := range lock.keys() {
				e := lock.Skills[k]
				if e.Identity.SkillID != opts.ID || e.Identity.Variant != opts.Variant {
					continue
				}
				if ps := e.paths(); len(ps) > 0 {
					cmd := "fw skills move " + quoteArg(ps[0]) + " " + quoteArg(spelled)
					return result, &SkillError{Msg: fmt.Sprintf("%s is already tracked at %s. If you moved it here, record the move: %s", opts.ID, ps[0], cmd), Hint: cmd}
				}
			}
		}
	}
	if entry.Identity.Kind == KindCloud && entry.Identity.Server != server {
		return result, fmt.Errorf("skill tracks another server")
	}
	if opts.ID != "" && (entry.Identity.Kind == KindCloud || entry.Identity.Kind == KindBuiltin) && opts.ID != entry.Identity.SkillID {
		return result, fmt.Errorf("skill identity mismatch")
	}
	// Every tracked copy must match: the first that exists is the one sync
	// sends, and an absent copy is named so fw skills copy can recreate it
	var files []File
	localHash := ""
	var states []copyState
	for _, p := range entry.paths() {
		target, e := safePath(root, p)
		if e != nil {
			return result, e
		}
		if _, e = os.Stat(target); notThere(e) {
			states = append(states, copyState{path: p, state: "absent"})
			continue
		} else if e != nil {
			return result, e
		}
		b, e := Scan(target)
		if e != nil {
			return result, e
		}
		h, _ := Hash(b)
		if files == nil {
			files, localHash = b, h
		}
		st := "edited"
		if r := entry.Copies[p].Hash; r != "" && r == h {
			st = "clean"
		}
		states = append(states, copyState{path: p, state: st, hash: h})
	}
	if row, bad := copiesProblem(states); bad {
		reason, hint := describe(root, row, DefaultCommands, false)
		return result, &SkillError{Msg: "assistant skill copies diverged: " + reason, Hint: hint}
	}
	// Tied to a Cloud skill: a Cloud skill, or a built-in Cloud delivered
	cloudTied := entry.Identity.Kind == KindCloud || (entry.Identity.Kind == KindBuiltin && entry.Base != nil && entry.Base.Channel == ChannelCloud)
	base := ""
	if entry.Base != nil {
		base = entry.Base.Hash
	}
	id := ""
	if cloudTied {
		id = entry.Identity.SkillID
	} else if entry.Identity.Kind != KindBuiltin {
		id = opts.ID
	}
	remote := Snapshot{}
	hasRemote := false
	route := ""
	variant := entry.Identity.Variant
	switch {
	case id != "":
		route = "/api/v1/skills/sync/" + url.PathEscape(id) + "?variant=" + variant
	case entry.Identity.Kind == KindBuiltin:
		route = "/api/v1/skills/sync/" + url.PathEscape(entry.Identity.SkillID) + "?variant=" + variant + "&hash=" + base
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
	action := "conflict"
	switch {
	case !hasRemote:
		switch {
		case files == nil:
			action = "missing"
		case !cloudTied && base != "" && localHash == base:
			action = "unchanged"
		default:
			action = "push"
		}
	case localHash == remote.Hash:
		if !cloudTied {
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
	result = Result{Action: action, ID: remote.ID, Version: remote.Version, LocalHash: localHash, RemoteHash: remote.Hash, Paths: entry.paths()}
	if opts.DryRun || opts.Direction == "status" || action == "conflict" || action == "blocked" || action == "missing" {
		return result, nil
	}
	if action == "push" {
		expected := 0
		if cloudTied && entry.Base != nil {
			expected = entry.Base.Version
		}
		if expected == 0 || (opts.Resolve == "local" && !remote.System) {
			expected = remote.Version
		}
		basis := fmt.Sprintf("%d:%s:%s", expected, remote.ID, variant)
		if entry.Pending == nil || entry.Pending.Hash != localHash || entry.Pending.Basis != basis {
			entry.Pending = &Pending{uuid(), localHash, basis}
		}
		lock.Skills[key] = entry
		if err = saveLock(root, lock); err != nil {
			return result, err
		}
		var identity any
		if hasRemote {
			identity = remote.ID
		}
		body := map[string]any{"request_id": entry.Pending.RequestID, "skill_id": identity, "expected_version": expected, "variant": variant, "bundle": files, "client": map[string]string{"app": "framework-cli"}}
		if opts.ProjectToken != "" {
			body["project_token"] = opts.ProjectToken
		}
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
			if err = installInto(&backups, root, entry.paths(), remote.Bundle, localHash); err != nil {
				return result, err
			}
			result.Backups = backups
		}
		lock.Skills[key] = entry
		recordSynced(&lock, key, remote, server)
		if _, err = saveIfChanged(root, before, lock); err != nil {
			return result, err
		}
		result.ID, result.Version = remote.ID, remote.Version
	}
	return result, nil
}

// copiesProblem is the row for copies sync can't treat as one: copies that
// differ, or one absent beside another, with the copy that fixes it.
func copiesProblem(states []copyState) (Row, bool) {
	present := filter(states, "clean", "edited")
	absent := filter(states, "absent")
	if len(present) == 0 || (!differ(present) && len(absent) == 0) {
		return Row{}, false
	}
	edited := filter(present, "edited")
	src := present[0].path
	if len(edited) == 1 {
		src = edited[0].path
	} else if len(edited) > 1 && differ(present) {
		src = ""
	}
	all := append(append([]copyState{}, present...), absent...)
	row := Row{Status: StatusDiverged, Reason: ReasonCopiesDiffer, Hint: copyHint(all, src), Paths: pathsOf(all)}
	if !differ(present) {
		row.Reason = ReasonCopyAbsent
		row.Hint = &Hint{Key: HintCopy, From: src, To: pathsOf(absent)}
	}
	return row, true
}

// recordSynced makes a Cloud version the entry's base and every copy's
// record. An entry that wasn't that Cloud skill becomes it.
func recordSynced(lock *Lock, key string, snap Snapshot, server string) {
	e := lock.Skills[key]
	id := Identity{Kind: KindCloud, SkillID: snap.ID, Variant: e.Identity.Variant, Server: server}
	if strings.HasPrefix(snap.ID, "framework:") {
		id = Identity{Kind: KindBuiltin, SkillID: snap.ID, Variant: e.Identity.Variant}
	}
	f := e.clone()
	for p := range f.Copies {
		f.Copies[p] = Record{Hash: snap.Hash}
	}
	f.Base = &Base{Channel: ChannelCloud, Hash: snap.Hash, Version: snap.Version, Server: server}
	f.Known, f.Pending = sortedUnique(f.Known, []string{snap.Hash}), nil
	if id != e.Identity {
		f.Identity = id
		delete(lock.Skills, key)
		addEntry(lock, id.Key(""), f)
		return
	}
	lock.Skills[key] = f
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

// Install puts a new project's setup skills in its skills folders and
// records each in the lock, through the planner, as a pull would. A copy
// already there with other content is kept, and reported as an error.
func Install(root string, dirs []string, sources []Source) error {
	res, err := Pull(PullOptions{Root: root, Dirs: dirs, Offer: Offer{Source: "package", Skills: sources}})
	if err != nil {
		return err
	}
	for _, s := range res.Skills {
		switch s.Status {
		case StatusEdited, StatusDiverged, StatusFailed, StatusKept:
			return fmt.Errorf("preserved edited skill %s", strings.Join(s.Paths, ", "))
		}
	}
	return nil
}
