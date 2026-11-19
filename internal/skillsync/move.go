package skillsync

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"
)

// MoveOptions describe fw skills move: a copy (or every tracked copy in a
// skills folder) to move to another folder, and the identity to switch it
// to. Folders are project-relative.
type MoveOptions struct {
	Root string
	From string
	// To is the copy's new path, or the skills folder its copies move to;
	// "" to change only the identity.
	To string
	// SkillID, Builtin, and Variant name the identity to switch to: a Cloud
	// skill, a built-in by name, or another variant of the same skill.
	SkillID, Builtin, Variant string
	// Server is the project's Framework Cloud server, for a Cloud skill ID.
	Server string
	DryRun bool
}

// MoveItem is one copy a move moved or switched.
type MoveItem struct {
	Name       string  `json:"name"`
	Status     string  `json:"status"`
	From       *string `json:"from"`
	To         string  `json:"to"`
	ReasonCode string  `json:"reason_code"`
	Skill      string  `json:"skill"`
}

// MoveResult is what a move did, or would do.
type MoveResult struct {
	Skills []MoveItem `json:"skills"`
	Steps  []Step     `json:"-"`
	Rows   []Row      `json:"-"`
}

// Move moves tracked copies to another folder and records the move in the
// lock, or switches a copy's identity, or both. A folder already moved by
// hand is recorded without touching the disk. It refuses before writing
// anything: an untracked source, a target another skill tracks, a target
// that exists, nothing at either end, or an identity the lock already has.
// A retargeted entry keeps its records, so its unedited copies take the new
// identity's version at the next pull; push refuses its edited copies
// until --force. A dry run writes nothing.
func Move(o MoveOptions) (MoveResult, error) {
	res := MoveResult{Skills: []MoveItem{}}
	root, err := filepath.Abs(o.Root)
	if err != nil {
		return res, err
	}
	from, err := CleanFolder(o.From)
	if err != nil {
		return res, err
	}
	to := ""
	if o.To != "" {
		if to, err = CleanFolder(o.To); err != nil {
			return res, err
		}
		if r, err := isReserved(root, to); err != nil {
			return res, err
		} else if r {
			return res, fmt.Errorf("%s overlaps .framework, which holds the project's Framework state; pick another folder", to)
		}
	}
	if to == "" && o.SkillID == "" && o.Builtin == "" && o.Variant == "" {
		return res, fmt.Errorf("name where to move %s, or the identity to switch it to with --id, --builtin, or --variant", from)
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
	disk := NewDisk(root)
	var identity *Identity
	if o.SkillID != "" || o.Builtin != "" || o.Variant != "" {
		current, err := moveEntry(disk, lock, from)
		if err != nil {
			return res, err
		}
		id, err := moveIdentity(o, current)
		if err != nil {
			return res, err
		}
		identity = &id
	}
	plan, err := Plan(PlanInput{Command: CommandMove, Disk: disk, Lock: lock,
		Options: PlanOptions{MoveFrom: from, MoveTo: to, MoveIdentity: identity}})
	if err != nil {
		return res, err
	}
	res.Steps, res.Rows = plan.Steps, plan.Rows
	for _, st := range plan.Steps {
		it := MoveItem{Name: st.Name, Status: st.Status, To: st.Path, ReasonCode: st.Reason, Skill: st.Entry}
		if st.From != "" {
			f := st.From
			it.From = &f
		}
		res.Skills = append(res.Skills, it)
	}
	if o.DryRun {
		return res, nil
	}
	return res, applyMoves(root, lock, plan)
}

// moveEntry is the one entry a move's source names: the entry tracking a
// copy, or the one entry with copies in a skills folder.
func moveEntry(disk Disk, lock Lock, from string) (*LockEntry, error) {
	var found *LockEntry
	for _, k := range lock.keys() {
		e := lock.Skills[k]
		for _, p := range e.paths() {
			if disk.Same(p, from) || disk.Same(path.Dir(p), from) {
				if found != nil && found != e {
					return nil, &PlanError{Code: "move_ambiguous", Path: from}
				}
				found = e
			}
		}
	}
	if found == nil {
		return nil, &PlanError{Code: "move_untracked", Path: from}
	}
	return found, nil
}

// moveIdentity is the identity a move switches to: a built-in by name, a
// skill ID (framework:<name> is a built-in, inline:<name> setup text, any
// other a Cloud skill on the project's server), or the same skill in
// another variant.
func moveIdentity(o MoveOptions, current *LockEntry) (Identity, error) {
	variant := o.Variant
	if variant == "" {
		variant = current.Identity.Variant
	}
	switch variant {
	case "r", "python", "stata":
	default:
		return Identity{}, fmt.Errorf("--variant takes r, python, or stata")
	}
	id := o.SkillID
	if o.Builtin != "" {
		if id != "" {
			return Identity{}, fmt.Errorf("pass --id or --builtin, not both")
		}
		id = "framework:" + strings.TrimPrefix(o.Builtin, "framework:")
	}
	switch {
	case id == "":
		next := current.Identity
		next.Variant = variant
		return next, nil
	case strings.HasPrefix(id, "framework:"):
		return Identity{Kind: KindBuiltin, SkillID: id, Variant: variant}, nil
	case strings.HasPrefix(id, "inline:"):
		return Identity{Kind: KindInline, SkillID: id, Variant: variant}, nil
	}
	server := strings.TrimRight(o.Server, "/")
	if server == "" {
		return Identity{}, fmt.Errorf("switching to Cloud skill %s needs the project's Framework Cloud server; link the project first", id)
	}
	return Identity{Kind: KindCloud, SkillID: id, Variant: variant, Server: server}, nil
}
