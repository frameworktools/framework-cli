package skillsync

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// SyncSource is where a Cloud skill came from: the sync block of a layer
// entry.
type SyncSource struct {
	ID      string `json:"skill_id"`
	Version int    `json:"version"`
	Hash    string `json:"hash"`
	Variant string `json:"variant"`
	Server  string `json:"server"`
	// KnownHashes is every version's hash of the skill for this variant,
	// when Cloud sends it: an unedited older copy is one of them.
	KnownHashes []string `json:"known_hashes,omitempty"`
}

// PackageSource is a built-in the language package ships: framework:<name>,
// on the package channel. files is nil when fw doesn't ship it. known lists
// the hashes of every version the package shipped.
func PackageSource(name, variant string, files []File, known []string) Source {
	s := Source{Name: name, Identity: Identity{Kind: KindBuiltin, SkillID: "framework:" + name, Variant: variant},
		Channel: ChannelPackage, Bundle: files}
	if files != nil {
		s.Hash, _ = Hash(files)
	}
	s.Known = sortedUnique(known, []string{s.Hash})
	return s
}

// CloudSource is a skill a Cloud setup delivers at a version. A
// framework:<name> skill is a built-in on the cloud channel; any other is a
// Cloud skill keyed by its server. server is the project's, for a sync block
// that names none.
func CloudSource(name string, sync SyncSource, files []File, server string, known ...string) Source {
	if sync.Server != "" {
		server = sync.Server
	}
	server = strings.TrimRight(server, "/")
	id := Identity{Kind: KindCloud, SkillID: sync.ID, Variant: sync.Variant, Server: server}
	if strings.HasPrefix(sync.ID, "framework:") {
		id = Identity{Kind: KindBuiltin, SkillID: sync.ID, Variant: sync.Variant}
	}
	h := sync.Hash
	if files != nil {
		h, _ = Hash(files)
	}
	return Source{Name: name, Identity: id, Channel: ChannelCloud, Version: sync.Version, Server: server, Hash: h,
		Known: sortedUnique(known, sync.KnownHashes, []string{sync.Hash, h}), Bundle: files}
}

// InlineSource is setup text with no Cloud identity: inline:<name>.
func InlineSource(name, variant string, files []File) Source {
	h, _ := Hash(files)
	return Source{Name: name, Identity: Identity{Kind: KindInline, SkillID: "inline:" + name, Variant: variant},
		Channel: ChannelInline, Hash: h, Known: []string{h}, Bundle: files}
}

// PullOptions describe one pull or upgrade: the project, its skills folders
// (the first is the one AGENTS.md lists), and what the sources offer.
type PullOptions struct {
	Root string
	Dirs []string
	// Offer is the setup's skills (Source "cloud"), or the package's
	// (Source "package": an upgrade, or the offline fallback), with any
	// tracked skills fetched by their own identity.
	Offer Offer
	// Command is pull (the default) or upgrade, which plans built-ins only.
	Command   string
	Overwrite bool
	DryRun    bool
	// Offline marks the fallback pull when Framework Cloud wasn't reached.
	Offline bool
	// Commands render the hints; DefaultCommands when empty.
	Commands Commands
}

// PullResult lists the result rows, in the setup's order, then the rest.
type PullResult struct {
	Skills  []SkillStatus  `json:"skills"`
	Counts  map[string]int `json:"counts"`
	Backups []string       `json:"backups,omitempty"`
	// Steps and Rows are the plan as applied; Saved is true when the lock
	// was written.
	Steps []Step `json:"-"`
	Rows  []Row  `json:"-"`
	Saved bool   `json:"-"`
}

// Statuses.
const (
	StatusAdded       = "added"
	StatusUpdated     = "updated"
	StatusCurrent     = "current"
	StatusEdited      = "edited"
	StatusReplaced    = "replaced"
	StatusRemoved     = "removed"
	StatusUnavailable = "unavailable"
	// StatusDiverged is a skill whose tracked copies differ, or one of
	// whose copies is absent beside an edited one: nothing is written
	StatusDiverged = "diverged"
	// StatusRestored is a tracked copy that was absent, put back
	StatusRestored = "restored"
	// StatusUntracked is a copy in a configured folder no lock entry
	// records, left alone
	StatusUntracked = "untracked"
	// StatusKept is a folder another identity tracks, or a built-in last
	// delivered by Cloud, left alone
	StatusKept = "kept"
	// StatusFailed is a skill a copy of which couldn't be read
	StatusFailed = "failed"
)

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

// Pull brings the project's skills in line with the offer, through the
// planner: it adds missing skills, updates unedited ones, keeps edited ones
// (replacing them with Overwrite, after a backup), and reports skill folders
// the setup no longer names. It never deletes a skill. A dry run plans the
// same and writes nothing.
func Pull(o PullOptions) (PullResult, error) {
	res := PullResult{Counts: map[string]int{}}
	root, err := filepath.Abs(o.Root)
	if err != nil {
		return res, err
	}
	if !o.DryRun {
		release, err := lockSync(root)
		if err != nil {
			return res, err
		}
		defer release()
	}
	lock, _, err := loadLock(root)
	if err != nil {
		return res, err
	}
	// Every skills folder is read before anything is written
	if err = checkDirs(root, o.Dirs); err != nil {
		return res, err
	}
	command, scope := CommandPull, "all"
	if o.Command == CommandUpgrade {
		command, scope = CommandUpgrade, "builtin"
	}
	disk := NewDisk(root)
	plan, err := Plan(PlanInput{Command: command, Disk: disk, Lock: lock, Offer: o.Offer,
		Options: PlanOptions{Dirs: o.Dirs, Overwrite: o.Overwrite, DryRun: o.DryRun, Offline: o.Offline, Scope: scope}})
	if err != nil {
		return res, err
	}
	steps := plan.Steps
	var applyErr error
	if !o.DryRun && acts(plan.Steps) {
		done, err := applyWrites(root, disk, lock, plan)
		steps, res.Backups, res.Saved, applyErr = done.steps, done.backups, done.saved, err
	}
	if command == CommandUpgrade {
		// Upgrade ignores folders the package no longer names
		kept := steps[:0:0]
		for _, s := range steps {
			if s.Status != StatusRemoved {
				kept = append(kept, s)
			}
		}
		steps = kept
	}
	c := o.Commands
	if c.Pull == "" {
		c = DefaultCommands
	}
	res.Steps, res.Rows = steps, buildRows(steps, command)
	res.Skills = statuses(root, res.Rows, c, false)
	for _, s := range res.Skills {
		res.Counts[s.Status]++
	}
	return res, applyErr
}

// acts reports whether a plan changes anything: any step whose action is
// not none, so a record-only plan (a metadata update) is applied.
func acts(steps []Step) bool {
	for _, s := range steps {
		if s.Action != ActionNone {
			return true
		}
	}
	return false
}

// MissingEntry is a tracked Cloud skill none of whose copies is there:
// pull can put it back from Framework Cloud by its own identity.
type MissingEntry struct {
	Key, ID, Variant, Server string
}

// MissingEntries lists the tracked Cloud skills all of whose copies are
// absent, for the caller to fetch, with a login, before a pull.
func MissingEntries(root string) ([]MissingEntry, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	lock, _, err := loadLock(root)
	if err != nil {
		return nil, err
	}
	var out []MissingEntry
	for _, k := range lock.keys() {
		e := lock.Skills[k]
		if e.Identity.Kind != KindCloud || len(e.Copies) == 0 {
			continue
		}
		absent := true
		for p := range e.Copies {
			absent = absent && lookAt(root, p).Absent
		}
		if absent {
			out = append(out, MissingEntry{Key: k, ID: e.Identity.SkillID, Variant: e.Identity.Variant, Server: e.Identity.Server})
		}
	}
	return out, nil
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

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
