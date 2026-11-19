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
	"path"
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

// freeKey is base, or base with a number, whichever no entry uses.
func freeKey(state State, base string) string {
	key := base
	for i := 2; ; i++ {
		if _, taken := state.Skills[key]; !taken {
			return key
		}
		key = fmt.Sprintf("%s#%d", base, i)
	}
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
	if err == nil {
		mergeSplit(&state)
	}
	return state, err
}

// mergeSplit joins legacy entries that are one skill split in two: the
// same server, skill_id, and variant, or for a built-in without an ID the
// same name, origin, and variant. The merged entry has every path, and the
// newest version's record, so divergence is judged across all its copies.
func mergeSplit(state *State) {
	keys := make([]string, 0, len(state.Skills))
	for k := range state.Skills {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	identity := func(e *Entry) string {
		switch {
		case e.ID != "":
			return "id\x00" + e.Server + "\x00" + e.ID + "\x00" + e.Variant
		case e.Origin != nil && len(e.Paths) > 0:
			return "origin\x00" + path.Base(e.Paths[0]) + "\x00" + e.Origin.ID + "\x00" + e.Variant
		}
		return ""
	}
	first := map[string]string{}
	for _, k := range keys {
		e := state.Skills[k]
		id := identity(e)
		if id == "" {
			continue
		}
		into, seen := first[id]
		if !seen {
			first[id] = k
			continue
		}
		kept := state.Skills[into]
		paths := kept.Paths
		for _, p := range e.Paths {
			if !contains(paths, p) {
				paths = append(paths, p)
			}
		}
		if e.Version > kept.Version {
			kept = e
			state.Skills[into] = e
		}
		kept.Paths = paths
		delete(state.Skills, k)
	}
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
	// The path as given must hold no link before any case alias is
	// resolved: a symlinked skill is refused, as it always was
	if _, err = safePath(root, opts.Path); err != nil {
		return result, err
	}
	server := strings.TrimRight(client.URL, "/")
	// A new entry's key is never one in use: map keys aren't paths
	key := freeKey(state, diskSpelling(root, opts.Path))
	entry := &Entry{Paths: []string{diskSpelling(root, opts.Path)}, Variant: opts.Variant}
	tracked := false
	for k, e := range state.Skills {
		for _, p := range e.Paths {
			if samePath(root, p, opts.Path) {
				key, entry, tracked = k, e, true
			}
		}
	}
	// Adopting: --id names a skill whose entry has no copy left, as after
	// an assistant folder was renamed. That entry moves to this path; this
	// is the explicit join, never inferred from a name or bytes
	if !tracked && opts.ID != "" {
		keys := make([]string, 0, len(state.Skills))
		for k := range state.Skills {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			e := state.Skills[k]
			named := e.ID == opts.ID || (e.ID == "" && e.Origin != nil && e.Origin.ID == opts.ID)
			if !named || e.Variant != opts.Variant || (e.Server != "" && e.Server != server) {
				continue
			}
			for _, p := range e.Paths {
				if h, ok, err := copyHash(root, p); err != nil {
					return result, err
				} else if ok && h != "" {
					return result, fmt.Errorf("%s is already tracked at %s; copy it from there with fw skills copy instead", opts.ID, p)
				}
			}
			// Re-keyed with its new path, so the vacated path's key is free
			// for whatever skill goes there next
			e.Paths = []string{diskSpelling(root, opts.Path)}
			delete(state.Skills, k)
			key, entry = freeKey(state, e.Paths[0]), e
			break
		}
	}
	if entry.Server != "" && entry.Server != server {
		return result, fmt.Errorf("skill tracks another server")
	}
	if opts.ID != "" && entry.ID != "" && opts.ID != entry.ID {
		return result, fmt.Errorf("skill identity mismatch")
	}
	// Every recorded copy must match: the first that exists is the one sync
	// sends, and a missing copy is named so fw skills copy can recreate it
	var files []File
	localHash := ""
	var present, missing []string
	hashes := map[string]string{}
	for _, relative := range entry.Paths {
		target, e := safePath(root, relative)
		if e != nil {
			return result, e
		}
		if _, e = os.Stat(target); notThere(e) {
			missing = append(missing, relative)
			continue
		} else if e != nil {
			return result, e
		}
		b, e := Scan(target)
		if e != nil {
			return result, e
		}
		h, _ := Hash(b)
		if len(present) == 0 {
			files, localHash = b, h
		}
		present = append(present, relative)
		hashes[relative] = h
	}
	if n := copiesNote(root, &state, entry, present, missing, hashes); n.reason != "" {
		return result, copiesError(n)
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
