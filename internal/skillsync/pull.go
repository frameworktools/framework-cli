package skillsync

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
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
	// Fallback marks a pull of fw's package skills because Framework Cloud
	// wasn't reached. It never replaces a skill the lock ties to Cloud.
	Fallback bool
	// Restore holds, by lock key, the files of a tracked skill whose copies
	// are all gone, fetched by its own identity at the lock's version, for
	// a skill the setup can't supply (such as your fork of a built-in)
	Restore map[string][]File
}

// MissingEntry is a tracked Cloud skill with no copy left: pull can put it
// back from Framework Cloud at the lock's version.
type MissingEntry struct {
	Key, ID, Variant, Server, Hash string
	Version                        int
}

// MissingEntries lists the tracked Cloud skills none of whose copies is
// left, for the caller to fetch before a pull.
func MissingEntries(root string) ([]MissingEntry, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	state, err := load(root)
	if err != nil {
		return nil, err
	}
	var out []MissingEntry
	for k, e := range state.Skills {
		if e.ID == "" || len(e.Paths) == 0 {
			continue
		}
		present, _, _, err := inspectCopies(root, &state, e)
		if err != nil {
			return nil, err
		}
		if len(present) == 0 {
			out = append(out, MissingEntry{Key: k, ID: e.ID, Variant: e.Variant, Server: e.Server, Hash: e.Hash, Version: e.Version})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

// SkillStatus is what a pull did, or would do, with one skill.
type SkillStatus struct {
	Name   string   `json:"name"`
	Status string   `json:"status"`
	Paths  []string `json:"paths"`
	// Reason says why, in a plain sentence, on every row that isn't a
	// success (added, restored, updated, current)
	Reason string `json:"reason,omitempty"`
	// Hint is the exact command that fixes it, when there is one
	Hint string `json:"hint,omitempty"`
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
	// StatusDiverged is a skill whose tracked copies differ, or one of
	// whose tracked copies is missing: pull writes none of its copies
	StatusDiverged = "diverged"
	// StatusRestored is a tracked skill none of whose copies were left,
	// put back by pull
	StatusRestored = "restored"
	// StatusMissing (shared with copy) is, in a pull, a tracked skill with
	// no copy left that pull can't put back; its hint says what would
	// StatusUntracked is a copy in a configured folder that no lock entry
	// records, left alone; its hint says how to track it
	StatusUntracked = "untracked"
	// StatusKept is a skill left alone because its folder belongs to a
	// different skill, or the setup names a different identity
	StatusKept = "kept"
	// StatusFailed is a skill whose entry couldn't be read
	StatusFailed = "failed"
)

var statusRank = map[string]int{StatusFailed: 7, StatusKept: 5, StatusMissing: 6, StatusUntracked: 5, StatusCurrent: 0, StatusUnavailable: 1, StatusAdded: 2, StatusRestored: 2, StatusUpdated: 3, StatusReplaced: 4, StatusEdited: 5, StatusDiverged: 6}

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
	// List the skill folders before writing anything, so a folder fw can't
	// read stops the pull before it installs a skill or saves the lock. A
	// folder is one of the setup's skills when it is that skill's folder by
	// filesystem identity, as ANALYSIS is analysis on a case-insensitive
	// volume
	removed := map[string][]string{}
	var names []string
	for _, dir := range o.Dirs {
		folders, err := SkillFolders(root, dir)
		if err != nil {
			return res, err
		}
		for _, name := range folders {
			setup := false
			for _, w := range o.Skills {
				setup = setup || samePath(root, dir+"/"+name, dir+"/"+w.Name)
			}
			if setup {
				continue
			}
			if _, seen := removed[name]; !seen {
				names = append(names, name)
			}
			removed[name] = append(removed[name], dir+"/"+name)
		}
	}
	// Pull works per lock entry. A setup skill's entry is found by its
	// identity, never by its name or a configured folder. Pull plans that
	// entry's folders, custom ones included, so its copies move together,
	// and the configured folders no other entry records. A folder another
	// entry records belongs to that skill: never planned, written, or
	// judged here. A folder already there that no entry records never joins
	// one, whatever its name or bytes. Plan against the lock as it was
	// before the pull, so the first install can't change how the next copy
	// compares. Paths match by filesystem identity throughout.
	claimed := map[*Entry]bool{}
	plans := make([]skillPlan, len(o.Skills))
	for i, w := range o.Skills {
		sp, err := planSkill(root, &state, w, o)
		if err != nil {
			// That skill's entry couldn't be read: a failed row, nothing
			// written for it, and the other skills go on
			sp = skillPlan{status: StatusFailed, paths: []string{}, note: note{reason: "Its copies couldn't be read: " + err.Error() + "."}}
		}
		if sp.entry != nil {
			claimed[sp.entry] = true
		}
		plans[i] = sp
	}
	changed := false
	for i, w := range o.Skills {
		sp := &plans[i]
		status := SkillStatus{Name: w.Name, Paths: []string{}}
		var loose []SkillStatus
		var why note
		for j := range sp.steps {
			step := &sp.steps[j]
			if step.status == StatusUntracked {
				// A loose copy gets its own row; the skill's row is its
				// tracked copies'
				loose = append(loose, SkillStatus{Name: w.Name, Status: StatusUntracked, Paths: []string{step.relative}, Reason: step.reason, Hint: step.hint})
				continue
			}
			if !o.DryRun {
				wrote, err := applyPull(root, &state, step, &res.Backups)
				if err != nil {
					return res, fmt.Errorf("%s: %w", step.relative, err)
				}
				changed = changed || wrote
			}
			status.Paths = append(status.Paths, step.relative)
			if status.Status == "" || statusRank[step.status] > statusRank[status.Status] {
				status.Status, why = step.status, note{step.reason, step.hint}
			}
		}
		if sp.status != "" {
			// Nothing written or verified: an explicit status, never current
			status.Status, status.Paths = sp.status, sp.paths
		}
		if sp.note.reason != "" {
			why = sp.note
		}
		if status.Status == "" {
			status.Status = StatusKept
		}
		if !success(status.Status) {
			status.Reason, status.Hint = why.reason, why.hint
		}
		if status.Status != StatusKept || len(status.Paths) > 0 || len(loose) == 0 {
			res.Skills = append(res.Skills, status)
		}
		res.Skills = append(res.Skills, loose...)
		res.Skills = append(res.Skills, sp.extra...)
	}
	// Every other tracked skill is checked too, whatever the setup names:
	// diverged and missing entries are reported, each on its own
	keys := make([]string, 0, len(state.Skills))
	for k := range state.Skills {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	taking := func(p string) bool {
		for _, sp := range plans {
			for _, st := range sp.steps {
				if (st.status == StatusAdded || st.status == StatusRestored) && samePath(root, st.relative, p) {
					return true
				}
			}
		}
		return false
	}
	for _, k := range keys {
		e := state.Skills[k]
		if claimed[e] {
			continue
		}
		if len(e.Paths) == 0 {
			// Its last folder went to another skill; it keeps its identity
			if e.ID != "" {
				res.Skills = append(res.Skills, SkillStatus{Name: e.ID, Status: StatusMissing, Paths: []string{},
					Reason: "Every tracked copy is gone. Its last pushed version is Cloud skill " + e.ID + "."})
			}
			continue
		}
		// A path the setup's skill is taking over isn't this skill's to
		// report or put back
		var left []string
		for _, p := range e.Paths {
			if !taking(p) {
				left = append(left, p)
			}
		}
		if len(left) == 0 {
			continue
		}
		if len(left) < len(e.Paths) {
			e = &Entry{Paths: left, ID: e.ID, Version: e.Version, Server: e.Server, Variant: e.Variant, Hash: e.Hash, Origin: e.Origin}
		}
		name := path.Base(e.Paths[0])
		present, _, n, err := inspectCopies(root, &state, e)
		if err != nil {
			res.Skills = append(res.Skills, SkillStatus{Name: name, Status: StatusFailed, Paths: e.Paths, Reason: "Its copies couldn't be read: " + err.Error() + "."})
			continue
		}
		switch {
		case n.reason != "":
			res.Skills = append(res.Skills, SkillStatus{Name: name, Status: StatusDiverged, Paths: e.Paths, Reason: n.reason, Hint: n.hint})
		case len(present) == 0:
			m := missingNote(e, false)
			res.Skills = append(res.Skills, SkillStatus{Name: name, Status: StatusMissing, Paths: e.Paths, Reason: m.reason, Hint: m.hint})
		default:
			continue
		}
		for _, p := range e.Paths {
			dir := path.Dir(p)
			for i := 0; i < len(names); i++ {
				n := names[i]
				if !samePath(root, dir+"/"+n, p) {
					continue
				}
				kept := removed[n][:0]
				for _, q := range removed[n] {
					if !samePath(root, q, p) {
						kept = append(kept, q)
					}
				}
				removed[n] = kept
				if len(kept) == 0 {
					delete(removed, n)
					names = append(names[:i], names[i+1:]...)
					i--
				}
			}
		}
	}
	if changed && !o.DryRun {
		if err = save(root, state); err != nil {
			return res, err
		}
	}
	sort.Strings(names)
	for _, name := range names {
		res.Skills = append(res.Skills, SkillStatus{Name: name, Status: StatusRemoved, Paths: removed[name],
			Reason: name + " is no longer in the project's setup, so pull left its folder alone."})
	}
	for _, s := range res.Skills {
		res.Counts[s.Status]++
	}
	return res, nil
}

// SkillFolders lists the skill folders in a skills folder, sorted. A
// folder that doesn't exist has none; one fw can't read is an error.
func SkillFolders(root, dir string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(dir)))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// pullStep is what a pull plans for one skill in one folder.
type pullStep struct {
	relative string
	w        Wanted
	status   string
	// reason and hint go with the status, as for a copy fw left alone
	reason, hint string
	// current is the copy's hash when planned, "" when it didn't exist
	current, fresh string
	// key and variant are where the lock records the skill; record is
	// false for an inline skill without a source, which isn't tracked
	key, variant string
	record       bool
}

// wantedKey is the lock key a skill gets when the lock has no entry for it.
func wantedKey(w Wanted, dirs []string) string {
	switch {
	case w.Sync != nil:
		return "account:" + w.Sync.Server + ":" + w.Sync.ID + ":" + w.Sync.Variant
	case w.Package:
		return "package:" + w.Name
	case len(dirs) > 0:
		return dirs[0] + "/" + w.Name
	}
	return w.Name
}

// skillEntry is a setup skill's one lock entry, found by its identity:
// for a Cloud skill its server, ID, and variant; for a package skill its
// key or its framework: origin. Never by the skill's name or a configured
// folder, so a same-named independent skill isn't taken for it. The entry
// is nil for a skill the lock doesn't have yet; key is where it goes.
func skillEntry(state State, w Wanted, dirs []string, variant string) (string, *Entry) {
	key := wantedKey(w, dirs)
	if !w.Package && w.Sync == nil {
		return key, nil // inline skills aren't recorded
	}
	// Never by the map key alone: a stale key can look canonical. Only the
	// entry's own metadata identifies it
	keys := make([]string, 0, len(state.Skills))
	for k := range state.Skills {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		e := state.Skills[k]
		switch {
		case w.Sync != nil:
			if e.ID == w.Sync.ID && e.Server == w.Sync.Server && e.Variant == w.Sync.Variant {
				return k, e
			}
		case (e.ID == "framework:"+w.Name || (e.Origin != nil && e.Origin.ID == "framework:"+w.Name)) &&
			(e.Variant == variant || e.Variant == ""):
			return k, e
		}
	}
	// A new entry's key is never one another entry holds
	return freeKey(state, key), nil
}

// skillPlan is what a pull does with one setup skill: its steps, or an
// explicit status (with paths) when it writes and verifies nothing, and
// rows of their own for untracked copies.
type skillPlan struct {
	key    string
	entry  *Entry
	steps  []pullStep
	note   note
	status string
	paths  []string
	extra  []SkillStatus
}

// plannable reports whether a tracked path's folder is still there, so
// pull can write it. A folder that was removed, renamed, or replaced by a
// file isn't: pull never recreates an assistant's tree.
func plannable(root, rel string) bool {
	info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(path.Dir(rel))))
	return err == nil && info.IsDir()
}

// planSkill plans one setup skill against its own entry.
func planSkill(root string, state *State, w Wanted, o PullOptions) (skillPlan, error) {
	variant := o.Variant
	if variant == "" {
		variant = "r"
	}
	if w.Sync != nil {
		variant = w.Sync.Variant
	}
	key, entry := skillEntry(*state, w, o.Dirs, variant)
	sp := skillPlan{key: key, entry: entry}
	var owned []*Entry
	var ownedPaths []string
	for _, dir := range o.Dirs {
		relative := dir + "/" + w.Name
		if owner := ownerOf(root, state, relative); owner != nil && owner != entry {
			// Another entry's copy there is that skill's. A path an entry
			// with a Cloud identity records stays that skill's even with no
			// copy: pull never takes it. Only an entry without one, and no
			// copy there, doesn't keep the setup's skill out of it
			_, there, err := copyHash(root, relative)
			if err != nil {
				return sp, err
			}
			if there || owner.ID != "" {
				owned, ownedPaths = append(owned, owner), append(ownedPaths, relative)
				continue
			}
		}
		if entry != nil && records(root, entry, relative) {
			continue // planned below, in the entry's spelling
		}
		step, err := planPull(root, relative, w, o, key, entry)
		if err != nil {
			return sp, fmt.Errorf("%s: %w", relative, err)
		}
		sp.steps = append(sp.steps, step)
	}
	var gone []string
	if entry != nil {
		for _, p := range entry.Paths {
			if !plannable(root, p) {
				gone = append(gone, p)
				continue
			}
			step, err := planPull(root, p, w, o, key, entry)
			if err != nil {
				return sp, fmt.Errorf("%s: %w", p, err)
			}
			sp.steps = append(sp.steps, step)
		}
	}
	if entry != nil {
		present, _, _, err := inspectCopies(root, state, entry)
		if err != nil {
			return sp, err
		}
		if len(present) == 0 && len(entry.Paths) > 0 {
			return planMissing(root, w, o, sp, gone), nil
		}
	}
	n, err := checkCopies(root, state, entry, sp.steps, o.Overwrite)
	if err != nil {
		return sp, err
	}
	sp.note = n
	if len(sp.steps) == 0 {
		// Every configured folder belongs to another entry
		sp.status, sp.paths = StatusKept, ownedPaths
		other := "another skill"
		if len(owned) > 0 && owned[0].ID != "" {
			other = owned[0].ID
		}
		sp.note = keptNote(ownedPaths[0], other)
		if len(owned) > 0 && owned[0].ID != "" {
			kept := "kept-skills/" + w.Name
			if _, there, _ := copyHash(root, ownedPaths[0]); there {
				// Adopting first moves the entry off the path; pull then
				// gives the path to the setup's skill
				cmd := "fw skills sync " + quoteArg(kept) + " --id " + quoteArg(other)
				sp.note = note{reason: fmt.Sprintf("%s belongs to a different skill (%s), so pull left it alone; to put the setup's %s there, move it to %s, run %s to keep its link to %s, then pull again.",
					ownedPaths[0], other, w.Name, kept, cmd, other), hint: cmd}
			} else {
				cmd := "fw skills sync " + quoteArg(ownedPaths[0]) + " --resolve remote"
				sp.note = note{reason: fmt.Sprintf("%s belongs to a different skill (%s), so pull left it alone; its copy is gone, and %s puts it back.",
					ownedPaths[0], other, cmd), hint: cmd}
			}
		}
	}
	return sp, nil
}

// keptNote is the reason for a folder that belongs to a different skill.
func keptNote(p, other string) note {
	return note{reason: fmt.Sprintf("%s belongs to a different skill (%s), so pull left it alone.", p, other)}
}

// untrackedNote is the reason for a loose copy, with the command that
// tracks it with this skill, when there is one.
func untrackedNote(p, command string) note {
	if command == "" {
		return note{reason: p + " isn't tracked, so pull left it alone."}
	}
	return note{reason: p + " isn't tracked, so pull left it alone. To track it with this skill: " + command, hint: command}
}

// planMissing plans a tracked skill none of whose copies is left. When a
// tracked folder is gone, pull writes nothing for it: the entry gets a
// missing row, and each untracked copy in a configured folder a row of its
// own. Otherwise pull puts the skill back from its own identity: the
// setup's copy when that is the entry's skill, or the files fetched for the
// entry at the lock's version; failing both, it reports it missing. Loose
// copies keep their own rows throughout.
func planMissing(root string, w Wanted, o PullOptions, sp skillPlan, gone []string) skillPlan {
	entry := sp.entry
	var loose []pullStep
	for _, st := range sp.steps {
		if st.status == StatusUntracked {
			loose = append(loose, st)
		}
	}
	if len(gone) > 0 {
		sp.steps, sp.status, sp.paths = nil, StatusMissing, entry.Paths
		sp.note = note{reason: fmt.Sprintf("Every tracked copy is gone: %s no longer exists.", listPaths(gone))}
		if entry.ID != "" {
			sp.note.reason += " Its last pushed version is Cloud skill " + entry.ID + "."
		}
		for _, u := range loose {
			n := untrackedNote(u.relative, adoptCommand(entry, u.relative))
			if n.hint == "" {
				n.reason += " To track it with this skill, move it back to " + gone[0] + " and pull again."
			}
			sp.extra = append(sp.extra, SkillStatus{Name: w.Name, Status: StatusUntracked, Paths: []string{u.relative}, Reason: n.reason, Hint: n.hint})
		}
		return sp
	}
	supplied := true
	for _, st := range sp.steps {
		if records(root, entry, st.relative) && st.status != StatusAdded {
			supplied = false
		}
	}
	if supplied {
		for i := range sp.steps {
			if sp.steps[i].status == StatusAdded && records(root, entry, sp.steps[i].relative) {
				sp.steps[i].status = StatusRestored
			}
		}
		return sp
	}
	files, ok := o.Restore[sp.key]
	if !ok {
		sp.steps, sp.status, sp.paths = loose, StatusMissing, entry.Paths
		sp.note = missingNote(entry, true)
		return sp
	}
	h, _ := Hash(files)
	steps := loose
	for _, st := range sp.steps {
		if !records(root, entry, st.relative) {
			continue // configured folders wait until the skill is back
		}
		st.status, st.record, st.current, st.fresh, st.reason, st.hint = StatusRestored, false, "", h, "", ""
		st.w = Wanted{Name: w.Name, Files: files}
		steps = append(steps, st)
	}
	sp.steps, sp.note = steps, note{}
	return sp
}

// missingNote is the reason a tracked skill with no copy left stays
// missing, and the command that puts it back, when there is one.
func missingNote(e *Entry, inSetup bool) note {
	switch {
	case e.ID != "" && len(e.Paths) > 0:
		cmd := "fw skills sync " + quoteArg(e.Paths[0]) + " --resolve remote"
		return note{reason: "Every tracked copy is gone. Restore it from Framework Cloud: " + cmd, hint: cmd}
	case e.ID != "":
		return note{reason: "Every tracked copy is gone. Its last pushed version is Cloud skill " + e.ID + "."}
	case inSetup:
		return note{reason: "Every tracked copy is gone, and the project's setup has no copy of this skill to put back."}
	}
	return note{reason: "Every tracked copy is gone, and it isn't in the project's setup any more."}
}

// pushMissingNote says what puts back a tracked skill with no copy left.
// When its folders are gone and an untracked copy sits in a configured
// folder, as after renaming .claude to .agents, that is adopting the copy:
// pull won't restore it there.
func pushMissingNote(root string, state *State, e *Entry, dirs []string) note {
	name := path.Base(e.Paths[0])
	var gone []string
	for _, p := range e.Paths {
		if !plannable(root, p) {
			gone = append(gone, p)
		}
	}
	if len(gone) > 0 {
		lead := fmt.Sprintf("Every tracked copy is gone: %s no longer exists.", listPaths(gone))
		for _, dir := range dirs {
			p := dir + "/" + name
			if _, there, err := copyHash(root, p); err == nil && there && ownerOf(root, state, p) == nil {
				if cmd := adoptCommand(e, p); cmd != "" {
					return note{reason: lead + " To track " + p + " with this skill: " + cmd, hint: cmd}
				}
			}
		}
		return note{reason: lead + " Bring the folder back and pull again."}
	}
	if e.ID != "" {
		return missingNote(e, false)
	}
	return note{reason: "Every tracked copy is gone. fw skills pull puts it back while the project's setup names it.", hint: "fw skills pull"}
}

// adoptCommand is the fw skills sync --id command that tracks a copy as
// an entry whose folders are gone, or "" for an entry with no identity.
// It moves the entry to the copy, then syncs it with Framework Cloud.
func adoptCommand(e *Entry, copyPath string) string {
	id := e.ID
	if id == "" && e.Origin != nil {
		id = e.Origin.ID
	}
	if id == "" {
		return ""
	}
	return "fw skills sync " + quoteArg(copyPath) + " --id " + quoteArg(id)
}

// planPull decides what a pull does with one skill in one folder. It reads
// the state and changes nothing. entry is the skill's lock entry, or nil.
func planPull(root, relative string, w Wanted, o PullOptions, key string, entry *Entry) (pullStep, error) {
	step := pullStep{relative: relative, w: w, key: key}
	if w.Package && w.Files == nil {
		step.status, step.reason = StatusUnavailable, "fw doesn't ship "+w.Name+"; the language package does."
		return step, nil
	}
	fresh, err := Hash(w.Files)
	if err != nil {
		return step, err
	}
	step.fresh = fresh
	step.variant = o.Variant
	if step.variant == "" {
		step.variant = "r"
	}
	if w.Sync != nil {
		step.variant = w.Sync.Variant
	}
	e := entry
	if e == nil {
		e = &Entry{}
	}
	tracks := records(root, e, relative)
	target, err := safePath(root, relative)
	if err != nil {
		return step, err
	}
	if _, err = os.Stat(target); err == nil {
		local, err := Scan(target)
		if err != nil {
			// Unreadable as a skill (a symlink, an oversized file): leave it
			step.status, step.reason = StatusFailed, fmt.Sprintf("%s couldn't be read as a skill: %v.", relative, err)
			return step, nil
		}
		step.current, _ = Hash(local)
	} else if !notThere(err) {
		return step, err
	}

	// A folder already there that the skill's entry doesn't record isn't
	// this skill's copy as far as the lock knows: never joined by its name
	// or its bytes, and never written. fw skills copy makes it a copy.
	if entry != nil && !tracks && step.current != "" {
		step.status, step.reason = StatusUntracked, untrackedNote(relative, "").reason
		return step, nil
	}

	// An entry tied to a Cloud skill keeps its identity and version, and
	// its content is only ever replaced by a newer version of that skill
	identified := e.ID != ""
	switch {
	case identified && w.Sync == nil && o.Fallback:
		step.status, step.reason = StatusUnavailable, "Framework Cloud wasn't reached, so "+w.Name+" was left as it is."
		return step, nil
	case identified && w.Sync == nil:
		// The setup names fw's packaged copy; the lock, a Cloud skill
		step.status, step.reason = StatusKept, keptNote(relative, e.ID).reason
		switch {
		case step.current == fresh:
			step.status, step.reason = StatusCurrent, ""
		case step.current == "" && !tracks:
			// An empty folder this entry doesn't record: a new folder,
			// which the new-folder rule explains
			step.status, step.reason = StatusEdited, ""
		}
		return step, nil
	case identified && e.ID != w.Sync.ID:
		// Tied to another Cloud skill: pull never replaces that identity
		step.status, step.reason = StatusKept, keptNote(relative, e.ID).reason
		if step.current == "" && !tracks {
			step.status, step.reason = StatusEdited, "" // a new folder
		}
		return step, nil
	case identified && e.Version > w.Sync.Version:
		// Newer than the setup names, as after fw skills push. Without a
		// login the setup's older one is all a pull has: keep it
		step.status = StatusEdited
		switch step.current {
		case "":
			step.reason = fmt.Sprintf("%s is missing, and this project has version %d, newer than the setup's %d, so pull didn't install the older one.", relative, e.Version, w.Sync.Version)
		case e.Hash:
			step.status = StatusCurrent
		default:
			step.reason = fmt.Sprintf("%s has local edits, which were kept. This project has version %d, newer than the setup's %d, so --overwrite leaves them too.", relative, e.Version, w.Sync.Version)
		}
		return step, nil
	}
	// From here the setup's copy is this same skill, at the same version or
	// newer: an edited copy is kept, and Overwrite resets it to that
	// version after a backup, discarding the local edits

	status := StatusUpdated
	switch {
	case step.current == "":
		status = StatusAdded
	case step.current == fresh:
		status = StatusCurrent
	case tracks && e.Hash != "" && step.current == e.Hash:
	case contains(w.Known, step.current):
	default:
		status = StatusEdited
	}
	if status == StatusEdited {
		if !o.Overwrite {
			step.status, step.hint = StatusEdited, "fw skills pull --overwrite"
			step.reason = relative + " has local edits, which were kept. --overwrite replaces them with the Cloud version."
			return step, nil
		}
		status = StatusReplaced
		step.reason = relative + " had local edits, which were backed up and replaced with the Cloud version."
	}
	step.status = status
	step.record = w.Package || w.Sync != nil
	return step, nil
}

// copyHash hashes a copy: "" when it doesn't exist, and a value no bundle
// hashes to when fw can't read it as a skill.
func copyHash(root, relative string) (string, bool, error) {
	target, err := safePath(root, relative)
	if err != nil {
		return "", false, err
	}
	if _, err = os.Stat(target); notThere(err) {
		return "", false, nil
	} else if err != nil {
		return "", false, err
	}
	files, err := Scan(target)
	if err != nil {
		return "unreadable:" + relative, true, nil
	}
	h, _ := Hash(files)
	return h, true, nil
}

// inspectCopies hashes an entry's tracked copies. The note is set when
// they diverged: they differ, or one is missing while another exists.
func inspectCopies(root string, state *State, entry *Entry) (present []string, hashes map[string]string, n note, err error) {
	hashes = map[string]string{}
	var missing []string
	for _, p := range entry.Paths {
		h, ok, err := copyHash(root, p)
		if err != nil {
			return nil, nil, note{}, fmt.Errorf("%s: %w", p, err)
		}
		if ok {
			present = append(present, p)
			hashes[p] = h
		} else {
			missing = append(missing, p)
		}
	}
	return present, hashes, copiesNote(root, state, entry, present, missing, hashes), nil
}

// checkCopies applies the rules for a skill's copies together, before
// pull writes anything, and returns the note when one applies.
//
// Diverged: when its tracked copies differ, or one is missing, pull
// writes no copy and leaves its lock entry alone.
//
// Restored: when none of its tracked copies is left, a copy pull puts back
// is reported as restored, not added.
//
// A new folder: pull won't fill an assistant folder that has no copy yet
// with the setup's copy when the existing copy is edited or tied to a
// Cloud skill. Copy that copy there instead. With overwrite, an edited
// copy that isn't tied to Cloud is replaced too, so the new folder gets
// the setup's copy like the others.
//
// A loose copy, in a folder the entry doesn't record, gets the command
// that tracks it with this skill.
func checkCopies(root string, state *State, entry *Entry, steps []pullStep, overwrite bool) (n note, err error) {
	if entry == nil {
		return note{}, nil
	}
	present, hashes, n, err := inspectCopies(root, state, entry)
	if err != nil || n.reason != "" {
		for i := range steps {
			if n.reason != "" && steps[i].status != StatusUntracked {
				steps[i].status, steps[i].record = StatusDiverged, false
			}
		}
		return n, err
	}
	if len(present) == 0 {
		for i := range steps {
			if steps[i].status == StatusAdded && records(root, entry, steps[i].relative) {
				steps[i].status = StatusRestored
			}
		}
		return note{}, nil
	}
	source := present[0]
	for i := range steps {
		if st := &steps[i]; st.status == StatusUntracked {
			command := ""
			if copyable(root, state, entry, source, st.relative) {
				command = copyTo(source, st.relative)
			}
			u := untrackedNote(st.relative, command)
			st.reason, st.hint = u.reason, u.hint
		}
	}
	defer func() {
		if err == nil {
			n = allOrNothing(root, entry, steps, n)
		}
	}()
	edited := hashes[source] != entry.Hash
	identified := entry.ID != ""
	if !identified && (!edited || overwrite) {
		return note{}, nil
	}
	for i := range steps {
		// A folder with no copy that the lock doesn't track: one being
		// added, or one planning already left empty
		st := &steps[i]
		if st.current != "" || records(root, entry, st.relative) || (st.status != StatusAdded && st.status != StatusEdited) {
			continue
		}
		st.status, st.record, st.reason, st.hint = StatusEdited, false, "", ""
		if n.reason == "" {
			n = newFolderNote(source, st.relative, copyable(root, state, entry, source, st.relative))
		}
	}
	return n, nil
}

// allOrNothing keeps an entry's tracked copies together: when some would
// move to the setup's version and another can't, the skill is diverged and
// nothing is written for it, so the lock never advances past a stale copy.
func allOrNothing(root string, entry *Entry, steps []pullStep, n note) note {
	moving, stale := false, ""
	for _, st := range steps {
		if !records(root, entry, st.relative) {
			continue
		}
		switch st.status {
		case StatusAdded, StatusRestored, StatusUpdated, StatusReplaced:
			moving = true
		case StatusEdited, StatusKept, StatusFailed, StatusUnavailable, StatusDiverged:
			if stale == "" {
				stale = st.relative
			}
		}
	}
	if !moving || stale == "" {
		return n
	}
	for i := range steps {
		if steps[i].status != StatusUntracked {
			steps[i].status, steps[i].record = StatusDiverged, false
		}
	}
	return note{reason: fmt.Sprintf("%s can't move to the setup's version with the skill's other copies, so pull wrote none of them. Bring it in step with the others, then pull again.", stale)}
}

// newFolderNote says how to fill a new assistant folder from your copy.
func newFolderNote(source, missing string, ok bool) note {
	lead := fmt.Sprintf("%s has no copy yet, and %s has local edits or is tied to a Cloud skill, so pull didn't fill it.", missing, source)
	if !ok {
		return note{reason: lead + " Copy its files there by hand: " + noCopy + "."}
	}
	cmd := copyTo(source, missing)
	return note{reason: lead + " Copy it there: " + cmd, hint: cmd}
}

// success is a status that needs no reason.
func success(status string) bool {
	switch status {
	case StatusAdded, StatusRestored, StatusUpdated, StatusCurrent:
		return true
	}
	return false
}

// pullInstall installs one planned copy; tests replace it.
var pullInstall = installInto

// applyPull carries out a planned step under the sync lock. Right before
// installing, it hashes the copy again: one that changed since the plan is
// edited, and kept. A declined install is never reported as done. It
// returns whether the lock state changed.
func applyPull(root string, state *State, step *pullStep, backups *[]string) (bool, error) {
	switch step.status {
	case StatusAdded, StatusRestored, StatusUpdated, StatusReplaced:
		target, err := safePath(root, step.relative)
		if err != nil {
			return false, err
		}
		now := ""
		if _, err = os.Stat(target); err == nil {
			local, err := Scan(target)
			if err != nil {
				step.status, step.record = StatusEdited, false
				return false, nil
			}
			now, _ = Hash(local)
		} else if !notThere(err) {
			return false, err
		}
		if now != step.current {
			step.status, step.record = StatusEdited, false
			return false, nil
		}
		if err = pullInstall(backups, root, []string{step.relative}, step.w.Files, step.current); err != nil {
			if errors.Is(err, errChanged) {
				// The installer declined: the copy changed as it worked
				step.status, step.record = StatusEdited, false
				return false, nil
			}
			return false, err
		}
		// Another entry that still listed this path, with no copy there,
		// lets go of it, and goes when it has no path left
		let := false
		for k, e := range state.Skills {
			if k == step.key || e.ID != "" || !records(root, e, step.relative) {
				continue
			}
			kept := e.Paths[:0]
			for _, p := range e.Paths {
				if !samePath(root, p, step.relative) {
					kept = append(kept, p)
				}
			}
			e.Paths, let = kept, true
			// An entry with a Cloud identity stays, with no paths, so moving
			// its copy elsewhere can keep its link; others go
			if len(kept) == 0 && e.ID == "" {
				delete(state.Skills, k)
			}
		}
		if let && !step.record {
			return true, nil
		}
	case StatusCurrent:
	default:
		return false, nil
	}
	if !step.record {
		return step.status != StatusCurrent, nil
	}
	// The planned entry, live: an earlier step may have recorded it already
	key, entry := step.key, state.Skills[step.key]
	if entry == nil {
		entry = &Entry{Paths: []string{}, Variant: step.variant}
	}
	w := step.w
	// Whatever the source, an entry tied to a Cloud skill keeps its
	// identity, and never moves to an older version
	if entry.ID != "" && (w.Sync == nil || entry.ID != w.Sync.ID || entry.Version > w.Sync.Version) {
		return false, nil
	}
	before, _ := json.Marshal(entry)
	_, known := state.Skills[key]
	if !records(root, entry, step.relative) {
		entry.Paths = append(entry.Paths, diskSpelling(root, step.relative))
	}
	entry.Hash, entry.Pending = step.fresh, nil
	if w.Sync != nil {
		entry.ID, entry.Version, entry.Server, entry.Variant, entry.Origin = w.Sync.ID, w.Sync.Version, w.Sync.Server, w.Sync.Variant, nil
	} else {
		entry.Origin = &Origin{ID: "framework:" + w.Name, Hash: step.fresh}
		if entry.Variant == "" {
			entry.Variant = step.variant
		}
	}
	state.Skills[key] = entry
	// A lock that would come out the same isn't rewritten
	after, _ := json.Marshal(entry)
	return !known || string(before) != string(after), nil
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
	// Missing is true when no tracked copy is left: there is nothing to
	// push, and push says so
	Missing bool
	// ID is the Cloud skill the entry tracks, if any
	ID string
	// Reason and Hint, for a missing skill, say what puts it back, as that
	// command actually works for this entry
	Reason, Hint string
}

// LocalChanges lists tracked skills edited since they were installed or
// last synced, and skill folders in dirs the lock doesn't track. Every
// recorded copy counts: an edit to any of them is a change, and sync then
// reports copies that differ.
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
	var changes []LocalChange
	for _, k := range keys {
		e := state.Skills[k]
		// Every tracked copy counts. A copy that changed, copies that differ,
		// or a missing copy next to one that exists make the skill one to
		// look at: sync then pushes it, or reports it diverged. A copy fw
		// can't read is listed too, so sync fails that skill alone.
		var present []string
		hashes := map[string]string{}
		missing := false
		changed := ""
		for _, p := range e.Paths {
			target, err := safePath(root, p)
			if err != nil {
				return nil, nil, err
			}
			if _, err = os.Stat(target); notThere(err) {
				missing = true
				continue
			} else if err != nil {
				return nil, nil, err
			}
			files, err := Scan(target)
			if err != nil {
				changed = p
				break
			}
			h, _ := Hash(files)
			if h != e.Hash && changed == "" {
				changed = p
			}
			present = append(present, p)
			hashes[p] = h
		}
		if changed == "" && len(present) == 0 && len(e.Paths) > 0 {
			changes = append(changes, LocalChange{Key: k, Name: filepath.Base(e.Paths[0]), Path: e.Paths[0], Variant: e.Variant, Missing: true, ID: e.ID,
				Reason: pushMissingNote(root, &state, e, dirs).reason, Hint: pushMissingNote(root, &state, e, dirs).hint})
			continue
		}
		if changed == "" && len(present) > 0 {
			diverged := missing
			for _, p := range present[1:] {
				diverged = diverged || hashes[p] != hashes[present[0]]
			}
			if diverged {
				changed = present[0]
			}
		}
		if changed != "" {
			changes = append(changes, LocalChange{Key: k, Name: filepath.Base(changed), Path: changed, Variant: e.Variant,
				Builtin: e.ID == "" && e.Origin != nil && strings.HasPrefix(e.Origin.ID, "framework:")})
		}
	}
	var untracked []string
	for _, dir := range dirs {
		folders, err := SkillFolders(root, dir)
		if err != nil {
			return nil, nil, err
		}
		for _, name := range folders {
			if ownerOf(root, &state, dir+"/"+name) == nil {
				untracked = append(untracked, dir+"/"+name)
			}
		}
	}
	return changes, untracked, nil
}
