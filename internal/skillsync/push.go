package skillsync

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// PushRequest is one entry's push: what POST /api/v1/skills/sync sends.
type PushRequest struct {
	Key             string
	Name            string
	RequestID       string
	SkillID         string // "" for a new skill
	ExpectedVersion int
	Variant         string
	Bundle          []File
	// Fork marks a built-in pushed as your own skill; Forced, a push over a
	// newer Cloud version, or one with no base.
	Fork, Forced bool
}

// PushOptions describe one push.
type PushOptions struct {
	Root  string
	Dirs  []string
	Force bool
	// Remote fetches Cloud's head for a lock entry: found is false when
	// Cloud has no such skill (a built-in it never published).
	Remote func(key string, e LockEntry) (r Remote, found bool, err error)
	// Commands render the hints; DefaultCommands when empty.
	Commands Commands
}

// PushPlan is a planned push, ready to show, confirm, and apply.
type PushPlan struct {
	root     string
	lock     Lock
	disk     Disk
	plan     PlanResult
	commands Commands
	// failures are entries Cloud couldn't be asked about, or that failed to
	// push, by key; answers are Cloud's answers to each push.
	failures map[string]string
	answers  map[string]Snapshot
	Steps    []Step
}

// PlanPush plans fw skills push: which tracked skills to send, refuse, or
// report. It asks Cloud for the head of each skill it would push, and
// writes nothing.
func PlanPush(o PushOptions) (*PushPlan, error) {
	root, err := filepath.Abs(o.Root)
	if err != nil {
		return nil, err
	}
	lock, _, err := loadLock(root)
	if err != nil {
		return nil, err
	}
	if err = checkDirs(root, o.Dirs); err != nil {
		return nil, err
	}
	c := o.Commands
	if c.Push == "" {
		c = DefaultCommands
	}
	disk := NewDisk(root)
	in := PlanInput{Command: CommandPush, Disk: disk, Lock: lock, Offer: Offer{Source: "cloud", Remote: map[string]Remote{}},
		Options: PlanOptions{Dirs: o.Dirs, Force: o.Force}}
	first, err := Plan(in)
	if err != nil {
		return nil, err
	}
	pp := &PushPlan{root: root, lock: lock, disk: disk, commands: c, failures: map[string]string{}, answers: map[string]Snapshot{}}
	// Only a skill push would send is checked against Cloud
	asked := map[string]bool{}
	for _, s := range first.Steps {
		if s.Entry == "" || asked[s.Entry] || (s.Status != StatusPushed && s.Status != StatusRefused) || o.Remote == nil {
			continue
		}
		asked[s.Entry] = true
		r, found, err := o.Remote(s.Entry, *lock.Skills[s.Entry])
		switch {
		case err != nil:
			pp.failures[s.Entry] = err.Error()
		case found:
			in.Offer.Remote[s.Entry] = r
		}
	}
	if pp.plan, err = Plan(in); err != nil {
		return nil, err
	}
	pp.Steps = pp.plan.Steps
	for i := range pp.Steps {
		if _, bad := pp.failures[pp.Steps[i].Entry]; bad {
			st := &pp.Steps[i]
			st.Action, st.Status, st.Reason, st.Hint = ActionNone, StatusFailed, "", nil
		}
	}
	return pp, nil
}

// Pending reports whether the plan sends anything.
func (pp *PushPlan) Pending() bool {
	for _, s := range pp.Steps {
		if s.Action == ActionPush {
			return true
		}
	}
	return false
}

// Acts reports whether applying the plan changes anything: a push, or a
// lock record when Cloud already has the bytes.
func (pp *PushPlan) Acts() bool {
	for _, s := range pp.Steps {
		if s.Action == ActionPush || s.Action == ActionRecord {
			return true
		}
	}
	return false
}

// Cancel turns every push into cancelled: the person answered no.
func (pp *PushPlan) Cancel() {
	for i := range pp.Steps {
		if st := &pp.Steps[i]; st.Action == ActionPush {
			st.Action, st.Status, st.Reason = ActionNone, StatusCancelled, ReasonCancelled
		}
	}
}

// Rows are the plan's result rows as fw prints them. A row Cloud couldn't
// be asked about says why.
func (pp *PushPlan) Rows() []SkillStatus {
	rows := buildRows(pp.Steps, CommandPush)
	out := statuses(pp.root, rows, pp.commands, true)
	for i, r := range rows {
		if msg, bad := pp.failures[r.Entry]; bad && r.Status == StatusFailed {
			out[i].Reason = msg
		}
		if snap, ok := pp.answers[r.Entry]; ok && r.Status == StatusPushed {
			out[i].SkillID, out[i].Version = snap.ID, snap.Version
		}
	}
	return out
}

// Entries lists the entries a push would send, by key, with their names.
func (pp *PushPlan) Entries() []PushRequest {
	var out []PushRequest
	seen := map[string]bool{}
	for _, s := range pp.Steps {
		if s.Action != ActionPush || seen[s.Entry] {
			continue
		}
		seen[s.Entry] = true
		out = append(out, PushRequest{Key: s.Entry, Name: s.Name, Fork: s.Fork, Forced: s.forced})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// PushSender sends one push and returns Cloud's answer.
type PushSender func(PushRequest) (Snapshot, error)

// Apply sends each planned push, one request per entry, under the sync
// lock, and records Cloud's answer in the lock: the pushed version becomes
// the entry's base and every copy's record. A pushed built-in, or a skill
// without a Cloud identity, becomes the Cloud skill Cloud answered with.
// An adopt (Cloud already had the bytes) is recorded too. Each entry that
// fails is reported failed; the others go on.
func (pp *PushPlan) Apply(server string, send PushSender) error {
	server = strings.TrimRight(server, "/")
	release, err := lockSync(pp.root)
	if err != nil {
		return err
	}
	defer release()
	// The lock as it is now, with any adopts the plan records
	lock, _, err := loadLock(pp.root)
	if err != nil {
		return err
	}
	before := lock.clone()
	for _, s := range pp.Steps {
		if s.Action == ActionRecord && s.Status == StatusCurrent {
			if f, ok := pp.plan.LockAfter.Skills[s.Entry]; ok {
				lock.Skills[s.Entry] = f.clone()
			}
		}
	}
	if _, err = saveIfChanged(pp.root, before, lock); err != nil {
		return err
	}
	for _, req := range pp.Entries() {
		failure := pp.pushOne(&lock, req, server, send)
		for i := range pp.Steps {
			if st := &pp.Steps[i]; st.Entry == req.Key && st.Action == ActionPush && failure != "" {
				st.Action, st.Status, st.Reason, st.Hint = ActionNone, StatusFailed, "", nil
			}
		}
		if failure != "" {
			pp.failures[req.Key] = failure
		}
	}
	return nil
}

// pushOne sends one entry. It returns why it failed, or "".
func (pp *PushPlan) pushOne(lock *Lock, req PushRequest, server string, send PushSender) string {
	e := lock.Skills[req.Key]
	if e == nil {
		return "it is no longer in " + lockFile
	}
	var planned string
	for _, s := range pp.Steps {
		if s.Entry == req.Key && s.Action == ActionPush {
			planned = s.Hash
		}
	}
	// The copy to send, as planned
	var files []File
	for _, p := range e.paths() {
		if l := lookAt(pp.root, p); !l.Absent {
			if l.Hash != planned {
				return p + " changed since push checked it, so it wasn't sent; run push again"
			}
			target, err := safePath(pp.root, p)
			if err == nil {
				files, err = Scan(target)
			}
			if err != nil {
				return err.Error()
			}
			break
		}
	}
	remote, hasRemote := pp.plan.inRemote(req.Key)
	req.Variant, req.Bundle = e.Identity.Variant, files
	switch {
	case e.Identity.Kind == KindCloud:
		req.SkillID = e.Identity.SkillID
		req.ExpectedVersion = remote.Version
		if e.Base != nil && !pp.forced(req.Key) {
			req.ExpectedVersion = e.Base.Version
		}
	case e.Identity.Kind == KindBuiltin && hasRemote:
		// Cloud published this built-in: pushing forks it
		req.SkillID, req.ExpectedVersion = remote.ID, remote.Version
		if req.SkillID == "" {
			req.SkillID = e.Identity.SkillID
		}
	}
	basis := fmt.Sprintf("%d:%s:%s", req.ExpectedVersion, req.SkillID, req.Variant)
	if e.Pending == nil || e.Pending.Hash != planned || e.Pending.Basis != basis {
		e.Pending = &Pending{RequestID: uuid(), Hash: planned, Basis: basis}
	}
	req.RequestID = e.Pending.RequestID
	if err := saveLock(pp.root, *lock); err != nil {
		return err.Error()
	}
	snap, err := send(req)
	if err != nil {
		return err.Error()
	}
	if h, err := Hash(snap.Bundle); snap.Bundle != nil && (err != nil || h != snap.Hash) {
		return "Framework Cloud's answer failed verification"
	}
	pp.answers[req.Key] = snap
	recordPush(lock, req.Key, snap, server, func(p string) bool { return lookAt(pp.root, p).Hash == snap.Hash })
	if err := saveLock(pp.root, *lock); err != nil {
		return err.Error()
	}
	return ""
}

// recordPush makes Cloud's answer the entry's base and every copy's record.
// An entry that wasn't this Cloud skill becomes it, under a new key.
// Only copies whose files are the pushed bytes take the new record; a copy
// whose folder is gone keeps its own.
func recordPush(lock *Lock, key string, snap Snapshot, server string, holds func(string) bool) {
	e := lock.Skills[key]
	f := e.clone()
	for p := range f.Copies {
		if holds(p) {
			f.Copies[p] = Record{Hash: snap.Hash}
		}
	}
	f.Base = &Base{Channel: ChannelCloud, Hash: snap.Hash, Version: snap.Version, Server: server}
	f.Pending = nil
	if e.Identity.Kind == KindCloud && e.Identity.SkillID == snap.ID {
		f.Known = sortedUnique(f.Known, []string{snap.Hash})
		lock.Skills[key] = f
		return
	}
	f.Identity = Identity{Kind: KindCloud, SkillID: snap.ID, Variant: e.Identity.Variant, Server: server}
	f.Known = []string{snap.Hash}
	delete(lock.Skills, key)
	addEntry(lock, f.Identity.Key(""), f)
}

// inRemote is Cloud's head the plan used for an entry.
func (r PlanResult) inRemote(key string) (Remote, bool) {
	for _, s := range r.Steps {
		if s.Entry == key && s.remote != nil {
			return *s.remote, true
		}
	}
	return Remote{}, false
}

// forced reports whether the entry is pushed over a newer Cloud version.
func (pp *PushPlan) forced(key string) bool {
	for _, s := range pp.Steps {
		if s.Entry == key && s.forced {
			return true
		}
	}
	return false
}
