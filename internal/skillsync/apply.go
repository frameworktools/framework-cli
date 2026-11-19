package skillsync

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// pullInstall installs one planned copy; tests replace it.
var pullInstall = installInto

// applied is what applying a plan did.
type applied struct {
	steps   []Step
	lock    Lock
	backups []string
	saved   bool
}

// applyWrites carries out a pull plan's write, copy, and record steps in
// path order, under the sync lock the caller holds. Right before each write
// or copy it looks at the copy again: one that changed since the plan is
// left alone, and its step becomes edited (changed_during_pull). The lock
// saved is the plan's, less what was declined: a declined copy keeps its
// old record, and its entry its old base. A lock that comes out unchanged
// is not rewritten.
func applyWrites(root string, disk Disk, before Lock, plan PlanResult) (applied, error) {
	out := applied{steps: append([]Step{}, plan.Steps...)}
	order := make([]int, len(out.steps))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return out.steps[order[a]].Path < out.steps[order[b]].Path })
	declined := map[int]bool{}
	var failure error
	for _, i := range order {
		st := &out.steps[i]
		if st.Action != ActionWrite && st.Action != ActionCopy {
			continue
		}
		if failure != nil {
			declined[i] = true
			continue
		}
		was := disk.Look(st.Path)
		now := lookAt(root, st.Path)
		files := st.files
		ok := now == was
		if ok && st.Action == ActionCopy {
			// The sibling copy, as planned
			from, err := safePath(root, st.From)
			if err == nil {
				files, err = Scan(from)
			}
			if err != nil {
				ok = false
			} else if h, _ := Hash(files); h != disk.Look(st.From).Hash {
				ok = false
			}
		}
		if !ok {
			declined[i] = true
			st.Action, st.Status, st.Reason, st.Hint, st.Backup = ActionNone, StatusEdited, ReasonChangedDuringPull, nil, false
			continue
		}
		if err := pullInstall(&out.backups, root, []string{st.Path}, files, was.Hash); err != nil {
			declined[i] = true
			if errors.Is(err, errChanged) {
				// The installer declined: the copy changed as it worked
				st.Action, st.Status, st.Reason, st.Hint, st.Backup = ActionNone, StatusEdited, ReasonChangedDuringPull, nil, false
				continue
			}
			failure = fmt.Errorf("%s: %w", st.Path, err)
		}
	}
	out.lock = plan.LockAfter.clone()
	for i := range declined {
		decline(&out.lock, before, plan.Steps[i])
	}
	out.lock.prune()
	saved, err := saveIfChanged(root, before, out.lock)
	out.saved = saved
	return out, errors.Join(failure, err)
}

// decline takes back one step's change from lock: the copy keeps its old
// record, and the entry its old base and pending push, while its known
// hashes stay.
func decline(lock *Lock, before Lock, st Step) {
	f := lock.Skills[st.Entry]
	if f == nil {
		return
	}
	old := before.Skills[st.Entry]
	if old == nil {
		delete(f.Copies, st.Path)
		f.Base = nil
		if len(f.Copies) == 0 {
			delete(lock.Skills, st.Entry)
		}
		return
	}
	if r, ok := old.Copies[st.Path]; ok {
		f.Copies[st.Path] = r
	} else {
		delete(f.Copies, st.Path)
	}
	f.Base, f.Pending = old.clone().Base, old.clone().Pending
}

// applyMoves renames each moved folder, then saves the lock. Errors were
// all checked by the planner, before this writes anything.
func applyMoves(root string, before Lock, plan PlanResult) error {
	for _, st := range plan.Steps {
		if st.Action != ActionMove {
			continue
		}
		src, err := safePath(root, st.From)
		if err != nil {
			return err
		}
		dst, err := safePath(root, st.Path)
		if err != nil {
			return err
		}
		if err = os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
			return err
		}
		if err = rename(src, dst); err != nil {
			return fmt.Errorf("moving %s to %s: %w", st.From, st.Path, err)
		}
	}
	_, err := saveIfChanged(root, before, plan.LockAfter)
	return err
}
