package skillsync

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Source is where a Cloud skill came from: the sync block of a layer entry.
type Source struct {
	ID      string `json:"skill_id"`
	Version int    `json:"version"`
	Hash    string `json:"hash"`
	Variant string `json:"variant"`
	Server  string `json:"server"`
}

// Wanted is one skill in the project's setup, ready to install.
type Wanted struct {
	Name string
	// Package marks a skill the language package ships, recorded in the
	// lock as framework:<name>. Files is nil when fw cannot supply it.
	Package bool
	// Files is the skill to install: the shipped copy, the Cloud bundle,
	// or an inline SKILL.md and its companion files.
	Files []File
	// Sync is set for a Cloud skill.
	Sync *Source
	// Known lists hashes of earlier versions an unedited copy may hold,
	// such as the version the project's layers name.
	Known []string
}

// PullOptions describe one pull: the project, its skills folders (the
// first is the one AGENTS.md lists), and the skills its setup names.
type PullOptions struct {
	Root      string
	Dirs      []string
	Skills    []Wanted
	Overwrite bool
	DryRun    bool
	// Variant is the project's language (r, python, or stata), recorded
	// for package skills.
	Variant string
}

// SkillStatus is what a pull did, or would do, with one skill.
type SkillStatus struct {
	Name   string   `json:"name"`
	Status string   `json:"status"`
	Paths  []string `json:"paths"`
}

// PullResult lists each skill's status, in the setup's order, then the
// skills no longer in the setup.
type PullResult struct {
	Skills  []SkillStatus  `json:"skills"`
	Counts  map[string]int `json:"counts"`
	Backups []string       `json:"backups,omitempty"`
}

// Statuses, and the order that decides a skill's status across folders.
const (
	StatusAdded       = "added"
	StatusUpdated     = "updated"
	StatusCurrent     = "current"
	StatusEdited      = "edited"
	StatusReplaced    = "replaced"
	StatusRemoved     = "removed"
	StatusUnavailable = "unavailable"
)

var statusRank = map[string]int{StatusCurrent: 0, StatusUnavailable: 1, StatusAdded: 2, StatusUpdated: 3, StatusReplaced: 4, StatusEdited: 5}

// BundleFromText turns SKILL.md text and companion files into a bundle.
func BundleFromText(content string, files map[string]string) []File {
	bundle := []File{{Path: "SKILL.md", Content: base64.StdEncoding.EncodeToString([]byte(content))}}
	for p, c := range files {
		if p != "SKILL.md" && p != ".framework-skill.json" {
			bundle = append(bundle, File{Path: p, Content: base64.StdEncoding.EncodeToString([]byte(c))})
		}
	}
	sort.Slice(bundle, func(i, j int) bool { return bundle[i].Path < bundle[j].Path })
	return bundle
}

// Pull installs missing skills, updates unedited ones, keeps edited ones
// (replacing them with Overwrite, after a backup), and reports skill folders
// the setup no longer names. It never deletes a skill.
func Pull(o PullOptions) (PullResult, error) {
	res := PullResult{Counts: map[string]int{}}
	root, err := filepath.Abs(o.Root)
	if err != nil {
		return res, err
	}
	if !o.DryRun {
		lock, err := safePath(root, ".framework/skills.sync.lock")
		if err != nil {
			return res, err
		}
		if err = os.MkdirAll(filepath.Dir(lock), 0755); err != nil {
			return res, err
		}
		if err = os.Mkdir(lock, 0700); err != nil {
			return res, fmt.Errorf("skill sync is locked; if the previous process stopped, remove .framework/skills.sync.lock and retry")
		}
		defer os.Remove(lock)
	}
	state, err := load(root)
	if err != nil {
		return res, err
	}
	named := map[string]bool{}
	changed := false
	for _, w := range o.Skills {
		named[w.Name] = true
		status := SkillStatus{Name: w.Name, Status: StatusCurrent, Paths: []string{}}
		for _, dir := range o.Dirs {
			relative := dir + "/" + w.Name
			s, wrote, err := pullOne(root, &state, relative, w, o, &res.Backups)
			if err != nil {
				return res, fmt.Errorf("%s: %w", relative, err)
			}
			changed = changed || wrote
			status.Paths = append(status.Paths, relative)
			if statusRank[s] > statusRank[status.Status] {
				status.Status = s
			}
		}
		res.Skills = append(res.Skills, status)
	}
	if changed && !o.DryRun {
		if err = save(root, state); err != nil {
			return res, err
		}
	}
	removed := map[string][]string{}
	var names []string
	for _, dir := range o.Dirs {
		for _, name := range SkillFolders(root, dir) {
			if named[name] {
				continue
			}
			if _, seen := removed[name]; !seen {
				names = append(names, name)
			}
			removed[name] = append(removed[name], dir+"/"+name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		res.Skills = append(res.Skills, SkillStatus{Name: name, Status: StatusRemoved, Paths: removed[name]})
	}
	for _, s := range res.Skills {
		res.Counts[s.Status]++
	}
	return res, nil
}

// SkillFolders lists the skill folders in a skills folder, sorted.
func SkillFolders(root, dir string) []string {
	entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(dir)))
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

// entryFor finds the lock entry recording a path, or makes a key for a new one.
func entryFor(state *State, relative, key, variant string) (string, *Entry, bool) {
	for k, e := range state.Skills {
		for _, p := range e.Paths {
			if p == relative {
				return k, e, true
			}
		}
	}
	if e, ok := state.Skills[key]; ok {
		return key, e, false
	}
	return key, &Entry{Paths: []string{}, Variant: variant}, false
}

func pullOne(root string, state *State, relative string, w Wanted, o PullOptions, backups *[]string) (string, bool, error) {
	if w.Package && w.Files == nil {
		return StatusUnavailable, false, nil
	}
	fresh, err := Hash(w.Files)
	if err != nil {
		return "", false, err
	}
	variant := o.Variant
	if variant == "" {
		variant = "r"
	}
	key := relative
	switch {
	case w.Sync != nil:
		variant = w.Sync.Variant
		key = "account:" + w.Sync.Server + ":" + w.Sync.ID + ":" + w.Sync.Variant
	case w.Package:
		key = "package:" + w.Name
	}
	key, entry, tracks := entryFor(state, relative, key, variant)
	target, err := safePath(root, relative)
	if err != nil {
		return "", false, err
	}
	current := ""
	if _, err = os.Stat(target); err == nil {
		local, err := Scan(target)
		if err != nil {
			// Unreadable as a skill (a symlink, an oversized file): leave it
			return StatusEdited, false, nil
		}
		current, _ = Hash(local)
	} else if !os.IsNotExist(err) {
		return "", false, err
	}

	status := StatusUpdated
	switch {
	case current == "":
		status = StatusAdded
	case current == fresh:
		status = StatusCurrent
	case w.Sync != nil && tracks && entry.ID == w.Sync.ID && current == entry.Hash && entry.Version > w.Sync.Version:
		// A newer version than the setup names, as after fw skills push.
		// Without a login the setup's older one is all a pull has: keep it.
		return StatusCurrent, false, nil
	case w.Package && entry.ID != "" && !strings.HasPrefix(entry.ID, "framework:"):
		// The person's own copy of a built-in skill
		status = StatusEdited
	case tracks && entry.Hash != "" && current == entry.Hash:
	case contains(w.Known, current):
	default:
		status = StatusEdited
	}
	if status == StatusEdited {
		if !o.Overwrite || (w.Package && entry.ID != "" && !strings.HasPrefix(entry.ID, "framework:")) {
			return StatusEdited, false, nil
		}
		status = StatusReplaced
	}
	// Inline skills without a source aren't tracked in the lock
	record := w.Package || w.Sync != nil
	if o.DryRun {
		return status, false, nil
	}
	if status != StatusCurrent {
		if err = installInto(backups, root, []string{relative}, w.Files, current); err != nil {
			return "", false, err
		}
	}
	if !record {
		return status, status != StatusCurrent, nil
	}
	if !contains(entry.Paths, relative) {
		entry.Paths = append(entry.Paths, relative)
	}
	entry.Hash, entry.Pending = fresh, nil
	if w.Package {
		entry.Origin = &Origin{ID: "framework:" + w.Name, Hash: fresh}
		entry.ID, entry.Version, entry.Server = "", 0, ""
		if entry.Variant == "" {
			entry.Variant = variant
		}
	} else {
		entry.ID, entry.Version, entry.Server, entry.Variant, entry.Origin = w.Sync.ID, w.Sync.Version, w.Sync.Server, w.Sync.Variant, nil
	}
	state.Skills[key] = entry
	return status, true, nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// LocalChange is a tracked skill whose files differ from what the lock
// recorded: a candidate for push.
type LocalChange struct {
	Key     string
	Name    string
	Path    string
	Variant string
	// Builtin is true for a Framework skill: pushing makes it the person's own.
	Builtin bool
}

// LocalChanges lists tracked skills edited since they were installed or
// last synced, and skill folders in dirs the lock doesn't track.
func LocalChanges(root string, dirs []string) ([]LocalChange, []string, error) {
	state, err := load(root)
	if err != nil {
		return nil, nil, err
	}
	keys := make([]string, 0, len(state.Skills))
	for k := range state.Skills {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	tracked := map[string]bool{}
	var changes []LocalChange
	for _, k := range keys {
		e := state.Skills[k]
		for _, p := range e.Paths {
			tracked[p] = true
		}
		for _, p := range e.Paths {
			target, err := safePath(root, p)
			if err != nil {
				return nil, nil, err
			}
			if _, err = os.Stat(target); err != nil {
				continue
			}
			files, err := Scan(target)
			if err != nil {
				return nil, nil, fmt.Errorf("%s: %w", p, err)
			}
			if h, _ := Hash(files); h != e.Hash {
				changes = append(changes, LocalChange{Key: k, Name: filepath.Base(p), Path: p, Variant: e.Variant,
					Builtin: e.ID == "" && e.Origin != nil && strings.HasPrefix(e.Origin.ID, "framework:")})
			}
			break
		}
	}
	var untracked []string
	for _, dir := range dirs {
		for _, name := range SkillFolders(root, dir) {
			if !tracked[dir+"/"+name] {
				untracked = append(untracked, dir+"/"+name)
			}
		}
	}
	return changes, untracked, nil
}
