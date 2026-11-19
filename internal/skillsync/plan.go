package skillsync

import (
	"encoding/json"
	"path"
	"sort"
	"strings"
)

// Commands the planner plans.
const (
	CommandPull    = "pull"
	CommandUpgrade = "upgrade"
	CommandCopy    = "copy"
	CommandPush    = "push"
	CommandMove    = "move"
)

// Step actions.
const (
	ActionNone   = "none"
	ActionWrite  = "write"
	ActionCopy   = "copy"
	ActionRecord = "record"
	ActionMove   = "move"
	ActionPush   = "push"
)

// Statuses the planner gives that the pull vocabulary above doesn't list.
const (
	StatusPushed    = "pushed"
	StatusRefused   = "refused"
	StatusCancelled = "cancelled"
	StatusMoved     = "moved"
)

// Reason codes, shared with R and Python.
const (
	ReasonLocalEdits        = "local_edits"
	ReasonReplacedEdits     = "replaced_edits"
	ReasonNewerInProject    = "newer_in_project"
	ReasonCopiesDiffer      = "copies_differ"
	ReasonCopyAbsent        = "copy_absent"
	ReasonOtherIdentity     = "other_identity"
	ReasonOtherVariant      = "other_variant"
	ReasonCloudTracked      = "cloud_tracked"
	ReasonUntracked         = "untracked"
	ReasonFolderGone        = "folder_gone"
	ReasonAllGone           = "all_gone"
	ReasonNotInSetup        = "not_in_setup"
	ReasonOffline           = "offline"
	ReasonNotShipped        = "not_shipped"
	ReasonSetupOlder        = "setup_older"
	ReasonUnreadable        = "unreadable"
	ReasonChangedDuringPull = "changed_during_pull"
	ReasonCloudChanged      = "cloud_changed"
	ReasonNoBase            = "no_base"
	ReasonCancelled         = "cancelled"
	ReasonPathMoved         = "path_moved"
	ReasonLockMoved         = "lock_moved"
	ReasonRetargeted        = "retargeted"
)

// Hint keys, shared with R and Python.
const (
	HintOverwrite = "overwrite"
	HintCopy      = "copy"
	HintMove      = "move"
	HintRetarget  = "retarget"
	HintForce     = "force"
	HintSync      = "sync"
	HintLogin     = "login"
)

// Look is what the disk holds at one path.
type Look struct {
	// Hash is the copy's bundle hash; "" when Absent or Unreadable.
	Hash string
	// Absent is true when nothing is there.
	Absent bool
	// Unreadable is true for a folder that isn't a valid skill.
	Unreadable bool
}

// Disk is what the planner may ask about the project. Paths are
// project-relative with / separators. The command gathers it; the planner
// only reads it.
type Disk interface {
	Look(p string) Look
	FolderExists(dir string) bool
	// SkillFolders lists the folders in dir, sorted.
	SkillFolders(dir string) []string
	// Same reports whether two paths name one place, as a case alias does
	// on a case-insensitive volume.
	Same(a, b string) bool
}

// Source is one offering for one identity.
type Source struct {
	Name     string
	Identity Identity
	Channel  string
	Version  int
	Server   string
	Hash     string
	Known    []string
	// Bundle is the skill's files; nil when the package doesn't ship it.
	Bundle []File
}

// base is the lock base this source gives.
func (s Source) base() *Base {
	b := &Base{Channel: s.Channel, Hash: s.Hash}
	if s.Channel == ChannelCloud {
		b.Version, b.Server = s.Version, strings.TrimRight(s.Server, "/")
	}
	return b
}

// Remote is Cloud's head for one entry, for push.
type Remote struct {
	Version int
	Hash    string
	// ID is the skill Cloud answered with, when it differs from the entry's
	// (a built-in Cloud published).
	ID string
}

// Offer is what the command's sources returned.
type Offer struct {
	// Source is "cloud" when the setup was read, "package" for the offline
	// fallback or upgrade.
	Source     string
	Skills     []Source
	ByIdentity map[string]Source
	Remote     map[string]Remote
}

// PlanOptions are the command's options.
type PlanOptions struct {
	// Dirs are the enabled assistants' skills folders.
	Dirs      []string
	Overwrite bool
	Force     bool
	DryRun    bool
	Offline   bool
	// Scope is "all", or "builtin" for upgrade.
	Scope string

	// Copy: the source folder, the targets, and the skill names; All is
	// true when every skill in the source was asked for.
	CopyFrom  string
	CopyTo    []string
	CopyNames []string
	CopyAll   bool

	// Move: from and to (a skill path or a skills folder), and the identity
	// to retarget to, or nil.
	MoveFrom     string
	MoveTo       string
	MoveIdentity *Identity
}

// PlanInput is everything the planner reads.
type PlanInput struct {
	Command string
	Disk    Disk
	Lock    Lock
	Offer   Offer
	Options PlanOptions
}

// Hint is the machine form of a row's fix.
type Hint struct {
	Key string
	// From and To: copy (To lists the targets; From "" when the person
	// picks) and move (To has one path).
	From string
	To   []string
	Pick bool
	// Path: retarget and sync.
	Path     string
	Identity *Identity
}

func (h Hint) MarshalJSON() ([]byte, error) {
	m := map[string]any{"key": h.Key}
	switch h.Key {
	case HintCopy:
		var from any
		if h.From != "" {
			from = h.From
		}
		to := h.To
		if to == nil {
			to = []string{}
		}
		m["from"], m["to"], m["pick"] = from, to, h.Pick
	case HintMove:
		to := ""
		if len(h.To) > 0 {
			to = h.To[0]
		}
		m["from"], m["to"] = h.From, to
	case HintRetarget:
		m["path"], m["identity"] = h.Path, h.Identity
	case HintSync:
		m["path"] = h.Path
	}
	return json.Marshal(m)
}

// Step is what a command does, or would do, with one copy.
type Step struct {
	Path   string `json:"path"`
	Entry  string `json:"entry"`
	Name   string `json:"name"`
	Action string `json:"action"`
	Status string `json:"status"`
	Reason string `json:"reason_code"`
	Hint   *Hint  `json:"hint"`
	// From is the copy a copy or move step takes.
	From string `json:"from,omitempty"`
	// Backup is true for a write over a folder that is there.
	Backup bool `json:"backup,omitempty"`
	// Hash is what a write or copy installs.
	Hash string `json:"-"`
	// Fork marks a push of a built-in, which becomes your own skill.
	Fork  bool   `json:"-"`
	files []File // a write's bundle
	// remote is Cloud's head a push step was judged against, and forced
	// marks a push over a newer Cloud version.
	remote *Remote
	forced bool
}

// Row is one result row: the steps of one lock entry, or one step.
type Row struct {
	Name   string   `json:"name"`
	Status string   `json:"status"`
	Paths  []string `json:"paths"`
	Reason string   `json:"reason_code"`
	Hint   *Hint    `json:"hint"`
	Entry  string   `json:"-"`
}

// PlanResult is the plan: its steps, its rows, and the lock if every action
// succeeds.
type PlanResult struct {
	Steps     []Step
	Rows      []Row
	LockAfter Lock
}

// PlanError is a command the planner refuses before anything is written.
type PlanError struct {
	Code string
	Path string
	Key  string
}

// ReasonCode is the shared error code.
func (e *PlanError) ReasonCode() string { return e.Code }

func (e *PlanError) Error() string {
	switch e.Code {
	case "move_untracked":
		return e.Path + " isn't tracked in " + lockFile + ", so there is nothing to move; fw skills sync PATH tracks a folder"
	case "move_target_owned":
		return e.Path + " is tracked as a different skill, so fw won't move a copy there; pick another folder"
	case "move_target_exists":
		return e.Path + " already exists; move it aside first, or move only the lock with the folder already in place"
	case "move_nothing":
		return "neither " + e.Path + " nor its target exists, so there is nothing to move"
	case "move_identity_exists":
		return "the lock already tracks " + e.Key + "; move the copy into that entry's folder instead"
	case "move_ambiguous":
		return e.Path + " holds copies of more than one skill; name one skill's folder to change its identity"
	case "copy_target_owned":
		return e.Path + " is recorded as a different skill in " + lockFile + ", so fw won't copy over it; pick another folder"
	}
	return e.Code
}

// Plan is the one skill planner. It reads no files and makes no requests:
// the command gathers its inputs, calls it, then shows or applies its steps
// and saves LockAfter. Every skill command goes through it.
func Plan(in PlanInput) (PlanResult, error) {
	p := &planner{in: in, after: in.Lock.clone(), setup: map[string]bool{}}
	var err error
	switch in.Command {
	case CommandPull, CommandUpgrade:
		p.pull()
	case CommandCopy:
		err = p.copy()
	case CommandPush:
		p.push()
	case CommandMove:
		err = p.move()
	}
	if err != nil {
		return PlanResult{}, err
	}
	p.after.prune()
	return PlanResult{Steps: p.steps, Rows: buildRows(p.steps, in.Command), LockAfter: p.after}, nil
}

type planner struct {
	in    PlanInput
	steps []Step
	after Lock
	kept  []string
	setup map[string]bool
}

// copyState is one copy as the planner classifies it.
type copyState struct {
	path, state, hash string
}

func (p *planner) step(s Step) { p.steps = append(p.steps, s) }

// allRows gives each copy a none step with one status.
func (p *planner) allRows(key, name string, cs []copyState, status, reason string, h *Hint) {
	for _, c := range cs {
		p.step(Step{Path: c.path, Entry: key, Name: name, Action: ActionNone, Status: status, Reason: reason, Hint: h})
	}
}

// ownerOf is the key of the entry that tracks path, or "".
func (p *planner) ownerOf(lock Lock, x string) string {
	for _, k := range lock.keys() {
		for q := range lock.Skills[k].Copies {
			if p.same(q, x) {
				return k
			}
		}
	}
	return ""
}

func (p *planner) same(a, b string) bool { return a == b || p.in.Disk.Same(a, b) }

func (p *planner) inList(list []string, x string) bool {
	for _, q := range list {
		if p.same(q, x) {
			return true
		}
	}
	return false
}

// inDirs reports whether x is a folder in one of dirs.
func (p *planner) inDirs(x string) bool {
	for _, d := range p.in.Options.Dirs {
		if p.same(path.Dir(x), d) {
			return true
		}
	}
	return false
}

// tracked classifies a copy an entry tracks.
func (p *planner) tracked(x string, e *LockEntry, s *Source) copyState {
	l := p.in.Disk.Look(x)
	c := copyState{path: x, hash: l.Hash}
	switch {
	case l.Unreadable:
		c.state = "unreadable"
	case l.Absent && !p.in.Disk.FolderExists(path.Dir(x)):
		c.state = "gone"
	case l.Absent:
		c.state = "absent"
	case s != nil && l.Hash == s.Hash && !isOlder(s, e):
		c.state = "current"
	case e.Copies[x].Hash != "" && l.Hash == e.Copies[x].Hash:
		c.state = "clean"
	default:
		c.state = "edited"
	}
	return c
}

// untracked classifies a configured path no entry tracks.
func (p *planner) untracked(x string, e *LockEntry, s *Source) copyState {
	l := p.in.Disk.Look(x)
	c := copyState{path: x, hash: l.Hash}
	switch {
	case l.Unreadable:
		c.state = "unreadable"
	case l.Absent:
		c.state = "new"
	case len(e.Copies) > 0:
		c.state = "loose"
	case l.Hash == s.Hash:
		c.state = "current"
	case contains(e.Known, l.Hash) || contains(s.Known, l.Hash):
		c.state = "clean"
	default:
		c.state = "edited"
	}
	return c
}

// isOlder reports whether s is older than the version e last synced.
func isOlder(s *Source, e *LockEntry) bool {
	if s == nil || e == nil || e.Base == nil {
		return false
	}
	switch {
	case s.Channel == ChannelCloud && e.Base.Channel == ChannelCloud:
		return s.Version < e.Base.Version
	case s.Channel == ChannelPackage && e.Base.Channel == ChannelPackage:
		return !contains(s.Known, e.Base.Hash) && e.Base.Hash != s.Hash
	}
	return false
}

func pathsOf(cs []copyState) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.path
	}
	sort.Strings(out)
	return out
}

func filter(cs []copyState, states ...string) []copyState {
	var out []copyState
	for _, c := range cs {
		for _, s := range states {
			if c.state == s {
				out = append(out, c)
				break
			}
		}
	}
	return out
}

// copyHint is the copy hint from src (or "" when the person picks) to
// every other listed copy.
func copyHint(cs []copyState, src string) *Hint {
	var to []string
	for _, x := range pathsOf(cs) {
		if x != src {
			to = append(to, x)
		}
	}
	return &Hint{Key: HintCopy, From: src, To: to, Pick: src == ""}
}

func differ(present []copyState) bool {
	for _, c := range present {
		if c.hash != present[0].hash {
			return true
		}
	}
	return false
}

func (p *planner) pull() {
	o, lock := p.in.Options, p.in.Lock
	var accounted []string
	for i := range p.in.Offer.Skills {
		s := &p.in.Offer.Skills[i]
		key := s.Identity.Key("")
		e := lock.Skills[key]
		var extra []string
		for _, d := range o.Dirs {
			x := d + "/" + s.Name
			owner := p.ownerOf(lock, x)
			if owner == key {
				continue // planned as one of the entry's copies
			}
			if owner != "" {
				accounted = append(accounted, x)
				if p.in.Disk.Look(x).Absent {
					continue // the owner's entry plans it (restored or missing)
				}
				reason := ReasonOtherIdentity
				if o := lock.Skills[owner].Identity; o.SkillID == s.Identity.SkillID && o.Variant != s.Identity.Variant {
					reason = ReasonOtherVariant
				}
				id := s.Identity
				p.step(Step{Path: x, Entry: owner, Name: s.Name, Action: ActionNone, Status: StatusKept, Reason: reason,
					Hint: &Hint{Key: HintRetarget, Path: x, Identity: &id}})
				p.kept = append(p.kept, x)
				continue
			}
			extra = append(extra, x)
		}
		p.setup[key] = true
		p.entry(key, e, s, extra)
		if e != nil {
			accounted = append(accounted, e.paths()...)
		}
		accounted = append(accounted, extra...)
	}
	for _, key := range lock.keys() {
		e := lock.Skills[key]
		if p.setup[key] || (o.Scope == "builtin" && e.Identity.Kind != KindBuiltin) {
			continue
		}
		var s *Source
		if found, ok := p.in.Offer.ByIdentity[key]; ok {
			s = &found
		}
		p.unlisted(key, e, s)
		accounted = append(accounted, e.paths()...)
	}
	if p.in.Offer.Source == "cloud" && p.in.Command == CommandPull {
		for _, d := range o.Dirs {
			for _, name := range p.in.Disk.SkillFolders(d) {
				x := d + "/" + name
				if !p.inList(accounted, x) {
					p.step(Step{Path: x, Name: name, Action: ActionNone, Status: StatusRemoved, Reason: ReasonNotInSetup})
				}
			}
		}
	}
}

func addedOrRestored(c copyState) string {
	if c.state == "absent" {
		return StatusRestored
	}
	return StatusAdded
}

// entry plans one setup skill against its own entry.
func (p *planner) entry(key string, e *LockEntry, s *Source, extra []string) {
	o := p.in.Options
	if e == nil {
		e = &LockEntry{Identity: s.Identity, Known: []string{}, Copies: map[string]Record{}}
	}
	var cs []copyState
	for _, x := range e.paths() {
		cs = append(cs, p.tracked(x, e, s))
	}
	for _, x := range extra {
		cs = append(cs, p.untracked(x, e, s))
	}
	name := s.Name
	if len(filter(cs, "unreadable")) > 0 {
		p.allRows(key, name, cs, StatusFailed, ReasonUnreadable, nil)
		return
	}
	if s.Bundle == nil {
		p.allRows(key, name, cs, StatusUnavailable, ReasonNotShipped, nil)
		return
	}
	// The package's copy never writes over a built-in tracked on Cloud. A
	// Cloud setup that names the package copy may.
	if p.in.Offer.Source == "package" && e.Base != nil && e.Base.Channel == ChannelCloud {
		if o.Offline {
			p.allRows(key, name, cs, StatusUnavailable, ReasonOffline, nil)
		} else {
			p.allRows(key, name, cs, StatusKept, ReasonCloudTracked, nil)
		}
		return
	}
	older := isOlder(s, e)
	present := filter(cs, "current", "clean", "edited")
	edited := filter(present, "edited")
	holes := filter(cs, "absent", "new")
	gone := filter(cs, "gone")
	loose := filter(cs, "loose")
	for _, c := range loose {
		var h *Hint
		switch {
		case len(filter(present, "current", "clean", "edited")) > 0:
			h = &Hint{Key: HintCopy, From: present[0].path, To: []string{c.path}}
		case len(gone) > 0:
			h = &Hint{Key: HintMove, From: gone[0].path, To: []string{c.path}}
		}
		p.step(Step{Path: c.path, Entry: key, Name: name, Action: ActionNone, Status: StatusUntracked, Reason: ReasonUntracked, Hint: h})
	}
	for _, c := range gone {
		var h *Hint
		if twin := p.looseTwin(loose, c.path); twin != "" {
			h = &Hint{Key: HintMove, From: c.path, To: []string{twin}}
		}
		p.step(Step{Path: c.path, Entry: key, Name: name, Action: ActionNone, Status: StatusMissing, Reason: ReasonFolderGone, Hint: h})
	}
	both := append(append([]copyState{}, present...), holes...)
	if len(edited) > 0 {
		if differ(present) {
			src := ""
			if len(edited) == 1 {
				src = edited[0].path
			}
			p.allRows(key, name, both, StatusDiverged, ReasonCopiesDiffer, copyHint(both, src))
			return
		}
		if len(holes) > 0 && (older || !o.Overwrite) {
			p.allRows(key, name, both, StatusDiverged, ReasonCopyAbsent, &Hint{Key: HintCopy, From: edited[0].path, To: pathsOf(holes)})
			return
		}
		if older { // overwrite never moves to an older version
			p.allRows(key, name, present, StatusEdited, ReasonNewerInProject, nil)
			return
		}
		if !o.Overwrite {
			p.allRows(key, name, present, StatusEdited, ReasonLocalEdits, &Hint{Key: HintOverwrite})
			return
		}
		for _, c := range present {
			if c.state == "current" {
				p.step(Step{Path: c.path, Entry: key, Name: name, Action: ActionRecord, Status: StatusCurrent})
			} else {
				p.step(Step{Path: c.path, Entry: key, Name: name, Action: ActionWrite, Status: StatusReplaced, Reason: ReasonReplacedEdits,
					Backup: true, Hash: s.Hash, files: s.Bundle})
			}
		}
		for _, c := range holes {
			p.step(Step{Path: c.path, Entry: key, Name: name, Action: ActionWrite, Status: addedOrRestored(c), Hash: s.Hash, files: s.Bundle})
		}
		p.finish(key, e, s)
		return
	}
	if older {
		for _, c := range present {
			p.step(Step{Path: c.path, Entry: key, Name: name, Action: ActionNone, Status: StatusCurrent, Reason: ReasonNewerInProject})
		}
		for _, c := range holes {
			if len(present) > 0 {
				p.step(Step{Path: c.path, Entry: key, Name: name, Action: ActionCopy, Status: addedOrRestored(c), From: present[0].path, Hash: present[0].hash})
			} else {
				p.step(Step{Path: c.path, Entry: key, Name: name, Action: ActionNone, Status: StatusUnavailable, Reason: ReasonSetupOlder, Hint: &Hint{Key: HintLogin}})
			}
		}
		p.finish(key, e, nil) // copies recorded; base unchanged
		return
	}
	for _, c := range present {
		if c.state == "current" {
			action := ActionNone
			if r, ok := e.Copies[c.path]; !ok || r.Hash != s.Hash || !sameBase(e.Base, s.base()) {
				action = ActionRecord
			}
			p.step(Step{Path: c.path, Entry: key, Name: name, Action: action, Status: StatusCurrent})
		} else {
			p.step(Step{Path: c.path, Entry: key, Name: name, Action: ActionWrite, Status: StatusUpdated, Backup: true, Hash: s.Hash, files: s.Bundle})
		}
	}
	for _, c := range holes {
		p.step(Step{Path: c.path, Entry: key, Name: name, Action: ActionWrite, Status: addedOrRestored(c), Hash: s.Hash, files: s.Bundle})
	}
	p.finish(key, e, s)
}

// looseTwin is a loose copy with the gone copy's name, or "".
func (p *planner) looseTwin(loose []copyState, gone string) string {
	for _, c := range loose {
		if strings.EqualFold(path.Base(c.path), path.Base(gone)) {
			return c.path
		}
	}
	return ""
}

func sameBase(a, b *Base) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// finish sets lock_after for one entry when any of its steps writes,
// copies, or records.
func (p *planner) finish(key string, e *LockEntry, s *Source) {
	var mine []Step
	for _, st := range p.steps {
		if st.Entry == key {
			mine = append(mine, st)
		}
	}
	changes := false
	for _, st := range mine {
		changes = changes || st.Action == ActionWrite || st.Action == ActionCopy || st.Action == ActionRecord
	}
	if !changes {
		return
	}
	f := e.clone()
	for _, st := range mine {
		switch st.Action {
		case ActionWrite, ActionRecord:
			f.Copies[st.Path] = Record{Hash: s.Hash}
		case ActionCopy:
			f.Copies[st.Path] = Record{Hash: e.Copies[st.From].Hash}
		}
	}
	if s != nil {
		f.Base = s.base()
		f.Known = sortedUnique(e.Known, s.Known, []string{s.Hash})
		f.Pending = nil
	}
	p.after.Skills[key] = f
}

// unlisted plans a lock entry the setup doesn't name.
func (p *planner) unlisted(key string, e *LockEntry, s *Source) {
	var cs []copyState
	for _, x := range e.paths() {
		cs = append(cs, p.tracked(x, e, s))
	}
	if len(cs) == 0 {
		return
	}
	name := path.Base(cs[0].path)
	if len(filter(cs, "unreadable")) > 0 {
		p.allRows(key, name, cs, StatusFailed, ReasonUnreadable, nil)
		return
	}
	if p.in.Options.Offline {
		p.allRows(key, name, cs, StatusUnavailable, ReasonOffline, nil)
		return
	}
	present := filter(cs, "current", "clean", "edited")
	holes := filter(cs, "absent")
	if len(present) == 0 {
		if s != nil && len(holes) > 0 { // fetched by its own identity
			for _, c := range cs {
				if c.state == "absent" {
					p.step(Step{Path: c.path, Entry: key, Name: name, Action: ActionWrite, Status: StatusRestored, Hash: s.Hash, files: s.Bundle})
				} else {
					p.step(Step{Path: c.path, Entry: key, Name: name, Action: ActionNone, Status: StatusMissing, Reason: ReasonFolderGone})
				}
			}
			p.finish(key, e, s)
			return
		}
		var h *Hint
		if e.Identity.Kind == KindCloud {
			h = &Hint{Key: HintLogin}
		}
		p.allRows(key, name, cs, StatusMissing, ReasonAllGone, h)
		return
	}
	edited := filter(present, "edited")
	if len(edited) > 0 && (differ(present) || len(holes) > 0) {
		src := ""
		if len(edited) == 1 {
			src = edited[0].path
		}
		reason := ReasonCopyAbsent
		if differ(present) {
			reason = ReasonCopiesDiffer
		}
		both := append(append([]copyState{}, present...), holes...)
		p.allRows(key, name, both, StatusDiverged, reason, copyHint(both, src))
		return
	}
	for _, c := range cs {
		// A folder a setup skill reported as kept is not also removed
		if p.inDirs(c.path) && !p.inList(p.kept, c.path) {
			p.step(Step{Path: c.path, Entry: key, Name: name, Action: ActionNone, Status: StatusRemoved, Reason: ReasonNotInSetup})
		}
	}
}

// copy plans fw skills copy.
func (p *planner) copy() error {
	o, lock := p.in.Options, p.in.Lock
	d := p.in.Disk
	for _, t := range o.CopyTo {
		for _, n := range o.CopyNames {
			src, dst := o.CopyFrom+"/"+n, t+"/"+n
			e, owner := p.ownerOf(lock, src), p.ownerOf(lock, dst)
			if owner != "" && e != "" && owner != e {
				return &PlanError{Code: "copy_target_owned", Path: dst}
			}
			from, to := d.Look(src), d.Look(dst)
			st := Step{Path: dst, Entry: e, Name: n, Action: ActionCopy, Status: StatusCopied, From: src, Backup: !to.Absent, Hash: from.Hash}
			if !to.Absent && to.Hash == from.Hash && !to.Unreadable {
				st.Action, st.Status = ActionNone, StatusCurrent
			}
			p.step(st)
			if e != "" { // a tracked source: the target joins its entry
				entry := p.after.Skills[e]
				rec := recordAt(p, lock.Skills[e], src)
				for q := range entry.Copies {
					if q != dst && p.same(q, dst) {
						delete(entry.Copies, q)
					}
				}
				entry.Copies[dst] = rec
			}
		}
		if o.CopyAll {
			for _, n := range d.SkillFolders(t) {
				if !p.inList(namesUnder(t, o.CopyNames), t+"/"+n) {
					p.step(Step{Path: t + "/" + n, Name: n, Action: ActionNone, Status: StatusMissing})
				}
			}
		}
	}
	return nil
}

func namesUnder(dir string, names []string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = dir + "/" + n
	}
	return out
}

// recordAt is an entry's record for x, matched by filesystem identity.
func recordAt(p *planner, e *LockEntry, x string) Record {
	if r, ok := e.Copies[x]; ok {
		return r
	}
	for q, r := range e.Copies {
		if p.same(q, x) {
			return r
		}
	}
	return Record{}
}

// push plans fw skills push.
func (p *planner) push() {
	o, lock := p.in.Options, p.in.Lock
	for _, key := range lock.keys() {
		e := lock.Skills[key]
		var cs []copyState
		for _, x := range e.paths() {
			cs = append(cs, p.tracked(x, e, nil))
		}
		if len(cs) == 0 {
			continue
		}
		name := path.Base(cs[0].path)
		if len(filter(cs, "unreadable")) > 0 {
			p.allRows(key, name, cs, StatusFailed, ReasonUnreadable, nil)
			continue
		}
		present := filter(cs, "clean", "edited")
		if len(present) == 0 {
			reason, h := ReasonAllGone, (*Hint)(nil)
			if twin := p.untrackedTwin(e); twin != "" {
				reason, h = ReasonFolderGone, &Hint{Key: HintMove, From: cs[0].path, To: []string{twin}}
			}
			p.allRows(key, name, cs, StatusMissing, reason, h)
			continue
		}
		edited := filter(present, "edited")
		if len(edited) == 0 {
			continue // nothing to push
		}
		absent := filter(cs, "absent")
		if differ(present) || len(absent) > 0 {
			src := ""
			if len(edited) == 1 {
				src = edited[0].path
			}
			reason := ReasonCopyAbsent
			if differ(present) {
				reason = ReasonCopiesDiffer
			}
			p.allRows(key, name, cs, StatusFailed, reason, copyHint(append(append([]copyState{}, present...), absent...), src))
			continue
		}
		local := present[0].hash
		r, hasRemote := p.in.Offer.Remote[key]
		forced := false
		all := func(action, status, reason string, h *Hint, fork bool) {
			for _, c := range cs {
				st := Step{Path: c.path, Entry: key, Name: name, Action: action, Status: status, Reason: reason, Hint: h, Hash: local, Fork: fork, forced: forced}
				if hasRemote {
					st.remote = &r
				}
				p.step(st)
			}
		}
		switch {
		case hasRemote && r.Hash == local:
			// Cloud already has it: nothing is sent, and the entry adopts
			// Cloud's head
			all(ActionRecord, StatusCurrent, "", nil, false)
			f := e.clone()
			for x := range f.Copies {
				f.Copies[x] = Record{Hash: local}
			}
			server := e.Identity.Server
			if e.Base != nil && e.Base.Server != "" {
				server = e.Base.Server
			}
			f.Base = &Base{Channel: ChannelCloud, Hash: local, Version: r.Version, Server: server}
			f.Known, f.Pending = sortedUnique(f.Known, []string{local}), nil
			p.after.Skills[key] = f
		case e.Identity.Kind == KindBuiltin:
			all(ActionPush, StatusPushed, "", nil, true) // becomes your own skill
		case e.Base == nil && !o.Force:
			all(ActionNone, StatusRefused, ReasonNoBase, &Hint{Key: HintForce}, false)
		case e.Base == nil:
			forced = true
			all(ActionPush, StatusPushed, "", nil, false)
		case !hasRemote || r.Version == e.Base.Version:
			all(ActionPush, StatusPushed, "", nil, false)
		case !o.Force:
			all(ActionNone, StatusRefused, ReasonCloudChanged, &Hint{Key: HintForce}, false)
		default:
			forced = true
			all(ActionPush, StatusPushed, "", nil, false)
		}
	}
	for _, d := range o.Dirs {
		for _, n := range p.in.Disk.SkillFolders(d) {
			x := d + "/" + n
			if p.ownerOf(lock, x) != "" {
				continue
			}
			h := &Hint{Key: HintSync, Path: x}
			if from := p.goneCopyNamed(n); from != "" {
				h = &Hint{Key: HintMove, From: from, To: []string{x}}
			}
			p.step(Step{Path: x, Name: n, Action: ActionNone, Status: StatusUntracked, Reason: ReasonUntracked, Hint: h})
		}
	}
}

// untrackedTwin is a folder in a configured skills folder, named as one of
// e's copies, that no entry tracks, or "".
func (p *planner) untrackedTwin(e *LockEntry) string {
	for _, x := range e.paths() {
		for _, d := range p.in.Options.Dirs {
			twin := d + "/" + path.Base(x)
			if l := p.in.Disk.Look(twin); !l.Absent && p.ownerOf(p.in.Lock, twin) == "" {
				return twin
			}
		}
	}
	return ""
}

// goneCopyNamed is a tracked copy named name whose folder is gone, or "".
func (p *planner) goneCopyNamed(name string) string {
	for _, k := range p.in.Lock.keys() {
		for _, x := range p.in.Lock.Skills[k].paths() {
			if path.Base(x) == name && p.in.Disk.Look(x).Absent && !p.in.Disk.FolderExists(path.Dir(x)) {
				return x
			}
		}
	}
	return ""
}

// move plans fw skills move.
func (p *planner) move() error {
	o, lock, d := p.in.Options, p.in.Lock, p.in.Disk
	type pair struct{ src, dst string }
	var pairs []pair
	var single string // the entry of a single skill path
	if owner := p.ownerOf(lock, o.MoveFrom); owner != "" {
		single = owner
		if o.MoveTo != "" {
			pairs = append(pairs, pair{o.MoveFrom, o.MoveTo})
		}
	} else {
		// A skills folder: each tracked copy under it moves
		for _, k := range lock.keys() {
			for _, x := range lock.Skills[k].paths() {
				if p.same(path.Dir(x), o.MoveFrom) {
					if o.MoveTo != "" {
						pairs = append(pairs, pair{x, o.MoveTo + "/" + path.Base(x)})
					}
					if single == "" || single == k {
						single = k
					} else {
						single = "\x00many"
					}
				}
			}
		}
		if single == "" {
			return &PlanError{Code: "move_untracked", Path: o.MoveFrom}
		}
	}
	for _, pr := range pairs {
		e := p.ownerOf(lock, pr.src)
		if e == "" {
			return &PlanError{Code: "move_untracked", Path: pr.src}
		}
		if owner := p.ownerOf(lock, pr.dst); owner != "" && owner != e {
			return &PlanError{Code: "move_target_owned", Path: pr.dst}
		}
		from, to := d.Look(pr.src), d.Look(pr.dst)
		if !from.Absent && !to.Absent && !p.same(pr.src, pr.dst) {
			return &PlanError{Code: "move_target_exists", Path: pr.dst}
		}
		if from.Absent && to.Absent {
			return &PlanError{Code: "move_nothing", Path: pr.src}
		}
		action, reason := ActionMove, ReasonPathMoved
		if from.Absent {
			action, reason = ActionRecord, ReasonLockMoved
		}
		p.step(Step{Path: pr.dst, Entry: e, Name: path.Base(pr.dst), Action: action, Status: StatusMoved, Reason: reason, From: pr.src})
		entry := p.after.Skills[e]
		rec := recordAt(p, lock.Skills[e], pr.src)
		for q := range entry.Copies {
			if p.same(q, pr.src) {
				delete(entry.Copies, q)
			}
		}
		entry.Copies[pr.dst] = rec
	}
	if o.MoveIdentity != nil { // retarget one entry
		if single == "\x00many" {
			return &PlanError{Code: "move_ambiguous", Path: o.MoveFrom}
		}
		e := p.after.Skills[single]
		id := *o.MoveIdentity
		first := ""
		if ps := e.paths(); len(ps) > 0 {
			first = ps[0]
		}
		newKey := id.Key(first)
		if _, taken := lock.Skills[newKey]; taken {
			return &PlanError{Code: "move_identity_exists", Key: newKey}
		}
		var clean []string
		for _, x := range e.paths() {
			r := e.Copies[x]
			look := d.Look(x)
			if look.Absent {
				// A copy just moved: judge it where it was
				for _, st := range p.steps {
					if st.Path == x && st.From != "" {
						look = d.Look(st.From)
					}
				}
			}
			if r.Hash != "" && look.Hash == r.Hash {
				clean = append(clean, r.Hash)
			}
		}
		f := &LockEntry{Identity: id, Known: sortedUnique(clean), Copies: e.Copies}
		delete(p.after.Skills, single)
		p.after.Skills[newKey] = f
		for _, x := range f.paths() {
			p.step(Step{Path: x, Entry: newKey, Name: path.Base(x), Action: ActionRecord, Status: StatusMoved, Reason: ReasonRetargeted})
		}
	}
	return nil
}

// statusOrder decides a row's status: the first of its steps' statuses.
var statusOrder = []string{StatusFailed, StatusDiverged, StatusMissing, StatusUnavailable, StatusKept, StatusUntracked,
	StatusEdited, StatusReplaced, StatusUpdated, StatusRestored, StatusAdded, StatusCurrent,
	StatusRefused, StatusCancelled, StatusPushed, StatusMoved, StatusCopied, StatusRemoved}

func rank(status string) int {
	for i, s := range statusOrder {
		if s == status {
			return i
		}
	}
	return len(statusOrder)
}

// ownRow reports whether a step gets a row of its own in pull, upgrade, and
// push.
func ownRow(s Step) bool {
	return s.Status == StatusUntracked || s.Status == StatusKept || s.Status == StatusRemoved || s.Entry == ""
}

// buildRows groups steps into result rows.
func buildRows(steps []Step, command string) []Row {
	rows := []Row{}
	single := command == CommandCopy || command == CommandMove
	groups := map[string]int{}
	var members [][]Step
	for _, s := range steps {
		if single || ownRow(s) {
			rows = append(rows, Row{Name: s.Name, Status: s.Status, Paths: []string{s.Path}, Reason: s.Reason, Hint: s.Hint, Entry: s.Entry})
			members = append(members, nil)
			continue
		}
		i, ok := groups[s.Entry]
		if !ok {
			i = len(rows)
			groups[s.Entry] = i
			rows = append(rows, Row{Name: s.Name, Entry: s.Entry})
			members = append(members, nil)
		}
		members[i] = append(members[i], s)
	}
	for i, m := range members {
		if m == nil {
			continue
		}
		sort.SliceStable(m, func(a, b int) bool { return m[a].Path < m[b].Path })
		best := m[0]
		for _, s := range m {
			if rank(s.Status) < rank(best.Status) {
				best = s
			}
		}
		r := &rows[i]
		r.Status, r.Reason, r.Hint = best.Status, best.Reason, best.Hint
		for _, s := range m {
			r.Paths = append(r.Paths, s.Path)
		}
		sort.Strings(r.Paths)
	}
	return rows
}
