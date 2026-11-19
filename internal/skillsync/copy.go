package skillsync

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// assistantSkillsDirs maps each assistant to the skills folder it reads, as
// in the R package. Codex and Copilot share .agents/skills, which fw also
// calls agents.
var assistantSkillsDirs = map[string]string{
	"claude":  ".claude/skills",
	"agents":  ".agents/skills",
	"codex":   ".agents/skills",
	"copilot": ".agents/skills",
}

// AssistantSkillsDir is the skills folder an assistant reads, or "" for a
// name fw doesn't know.
func AssistantSkillsDir(name string) string {
	return assistantSkillsDirs[name]
}

// AssistantNames lists the names AssistantSkillsDir knows, sorted.
func AssistantNames() []string {
	names := make([]string, 0, len(assistantSkillsDirs))
	for name := range assistantSkillsDirs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// AssistantSkillsDirs lists the distinct assistant skills folders, sorted.
func AssistantSkillsDirs() []string {
	var dirs []string
	for _, dir := range assistantSkillsDirs {
		if !contains(dirs, dir) {
			dirs = append(dirs, dir)
		}
	}
	sort.Strings(dirs)
	return dirs
}

// assistantName is the assistant name fw skills copy takes for dir: claude
// or agents, or "" for another folder.
func assistantName(dir string) string {
	switch dir {
	case assistantSkillsDirs["claude"]:
		return "claude"
	case assistantSkillsDirs["agents"]:
		return "agents"
	}
	return ""
}

var plainArg = regexp.MustCompile(`^[A-Za-z0-9_./:-]+$`)

// quoteArg quotes a command argument for the shell: double quotes for plain
// text, which also work in cmd and PowerShell, else single quotes.
func quoteArg(s string) string {
	if plainArg.MatchString(s) {
		return `"` + s + `"`
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// copyTo is the fw skills copy command that copies source to target, with
// the target folder named.
func copyTo(source, target string) string {
	from, to, name := path.Dir(source), path.Dir(target), path.Base(source)
	if a, b := assistantName(from), assistantName(to); a != "" && b != "" {
		return "fw skills copy --from " + a + " --to " + b + " --skill " + quoteArg(name)
	}
	return "fw skills copy --from-dir " + quoteArg(from) + " --to-dir " + quoteArg(to) + " --skill " + quoteArg(name)
}

// SkillError is a skill problem with the exact command that fixes it, as
// sync reports diverged copies.
type SkillError struct{ Msg, Hint string }

func (e *SkillError) Error() string { return e.Msg }

// listPaths is "a and b", or "a, b, and c".
func listPaths(paths []string) string {
	if len(paths) < 3 {
		return strings.Join(paths, " and ")
	}
	return strings.Join(paths[:len(paths)-1], ", ") + ", and " + paths[len(paths)-1]
}

// inTheWay is the first part of rel that exists but isn't a folder, as a
// file where a skills folder should be, or "".
func inTheWay(root, rel string) string {
	parts := strings.Split(rel, "/")
	for i := range parts {
		p := strings.Join(parts[:i+1], "/")
		info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(p)))
		if err != nil {
			return ""
		}
		if !info.IsDir() {
			return p
		}
	}
	return ""
}

// CleanFolder checks a project-relative folder as given, then cleans it to
// the form fw records: forward slashes, no ./ and no trailing slash.
// Absolute paths, drive letters, and any part that is .. or holds a colon
// are refused, on every platform.
func CleanFolder(dir string) (string, error) {
	switch {
	case dir == "":
		return "", errors.New("the folder is empty")
	case strings.HasPrefix(dir, "/") || strings.HasPrefix(dir, `\`) || filepath.IsAbs(dir):
		return "", fmt.Errorf("%s is not relative to the project", dir)
	case strings.Contains(dir, ":"):
		return "", fmt.Errorf("%s has a colon; name a folder relative to the project, without a drive or stream", dir)
	}
	for _, part := range strings.FieldsFunc(dir, func(r rune) bool { return r == '/' || r == '\\' }) {
		if part == ".." {
			return "", fmt.Errorf("%s has a .. part; name the folder from the project root", dir)
		}
	}
	clean := path.Clean(strings.ReplaceAll(dir, `\`, "/"))
	if clean == "." {
		return "", fmt.Errorf("%s is the project itself; name a skills folder inside it", dir)
	}
	return clean, nil
}

// lookup is a project-relative path's file info, or nil when it doesn't
// exist. Any other failure is an error.
func lookup(root, rel string) (os.FileInfo, error) {
	info, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("look up %s: %w", rel, err)
	}
	return info, nil
}

// within reports whether inner is outer or inside it. Besides the names,
// it compares filesystem identity: inner and each of its parents that
// exists against outer, so a case-insensitive volume's Skills matches
// skills.
func within(root, inner, outer string) (bool, error) {
	if inner == outer || strings.HasPrefix(inner, outer+"/") {
		return true, nil
	}
	o, err := lookup(root, outer)
	if err != nil || o == nil {
		return false, err
	}
	for p := inner; p != "." && p != "/"; p = path.Dir(p) {
		i, err := lookup(root, p)
		if err != nil {
			return false, err
		}
		if i != nil && os.SameFile(i, o) {
			return true, nil
		}
	}
	return false, nil
}

// sameFolder reports whether a and b are one folder, by name or identity.
func sameFolder(root, a, b string) (bool, error) {
	if a == b {
		return true, nil
	}
	x, err := lookup(root, a)
	if err != nil || x == nil {
		return false, err
	}
	y, err := lookup(root, b)
	if err != nil || y == nil {
		return false, err
	}
	return os.SameFile(x, y), nil
}

// overlaps reports whether two cleaned project-relative folders are the
// same folder or one is inside the other.
func overlaps(root, a, b string) (bool, error) {
	if in, err := within(root, a, b); in || err != nil {
		return in, err
	}
	return within(root, b, a)
}

// reserved is the folder that holds skill sync state, and the rest of
// Framework's project state: no copy goes into it.
const reserved = ".framework"

// foldWithin is within by name alone, ignoring case.
func foldWithin(inner, outer string) bool {
	inner, outer = strings.ToLower(inner), strings.ToLower(outer)
	return inner == outer || strings.HasPrefix(inner, outer+"/")
}

// isReserved reports whether dir is .framework, is inside it, or holds it.
func isReserved(root, dir string) (bool, error) {
	if foldWithin(dir, reserved) || foldWithin(reserved, dir) {
		return true, nil
	}
	return overlaps(root, dir, reserved)
}

// CopyOptions describe one copy: a project, the skills folder to copy
// from, the folders to copy to (every other assistant skills folder the
// project has, when empty), and the skills to copy (all, when empty).
// Folders are project-relative.
type CopyOptions struct {
	Root   string
	From   string
	To     []string
	Skills []string
	DryRun bool
}

// CopyItem is what a copy did, or would do, with one skill in one target.
// From is empty for a skill missing from the source, and null in JSON.
type CopyItem struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	From   string `json:"from"`
	To     string `json:"to"`
	Backup string `json:"backup,omitempty"`
}

// MarshalJSON writes an empty From, and an empty Backup, as null, as R and
// Python do.
func (c CopyItem) MarshalJSON() ([]byte, error) {
	var from, backup *string
	if c.From != "" {
		from = &c.From
	}
	if c.Backup != "" {
		backup = &c.Backup
	}
	return json.Marshal(struct {
		Name   string  `json:"name"`
		Status string  `json:"status"`
		From   *string `json:"from"`
		To     string  `json:"to"`
		Backup *string `json:"backup"`
	}{c.Name, c.Status, from, c.To, backup})
}

// CopyResult lists each skill in each target, target by target, and the
// folders copied to.
type CopyResult struct {
	From    string         `json:"from"`
	To      []string       `json:"to"`
	Skills  []CopyItem     `json:"skills"`
	Counts  map[string]int `json:"counts"`
	Backups []string       `json:"backups"`
	// Steps are the plan's steps.
	Steps []Step `json:"-"`
}

// Copy statuses. A skill in a target but not in the source is missing; copy
// leaves it alone.
const (
	StatusCopied  = "copied"
	StatusMissing = "missing"
)

// CopyError is a copy that failed partway. Replaced lists the copies that
// were replaced and could not be put back, with their backups.
type CopyError struct {
	Err      error
	Replaced []CopyItem
}

func (e *CopyError) Error() string {
	if len(e.Replaced) == 0 {
		return e.Err.Error()
	}
	lines := []string{e.Err.Error(), "These copies were replaced and could not be put back:"}
	for _, it := range e.Replaced {
		line := "  " + it.To
		if it.Backup != "" {
			line += " (the old copy is in " + it.Backup + ")"
		} else {
			line += " (new; there was no old copy)"
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func (e *CopyError) Unwrap() error { return e.Err }

// discover lists the skills in a folder: its immediate subfolders that hold
// a regular SKILL.md file, sorted. Other subfolders are ignored, and so are
// dot-folders, where installs stage their files. links lists symlinked
// subfolders, and subfolders whose SKILL.md is a symlink, which copy
// doesn't support. A symlink that leads nowhere is an error. A folder that
// doesn't exist has no skills.
func discover(root, dir string) (skills, links []string, err error) {
	abs, err := safePath(root, dir)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", dir, err)
	}
	entries, err := os.ReadDir(abs)
	if os.IsNotExist(err) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("read %s: %w", dir, err)
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if e.Type()&os.ModeSymlink != 0 {
			info, err := os.Stat(filepath.Join(abs, name))
			switch {
			case os.IsNotExist(err):
				return nil, nil, fmt.Errorf("%s/%s is a symlink that leads nowhere; remove it or point it at a real folder", dir, name)
			case err != nil:
				return nil, nil, fmt.Errorf("look up %s/%s: %w", dir, name, err)
			case info.IsDir():
				links = append(links, name)
			}
			continue
		}
		if !e.IsDir() {
			continue
		}
		info, err := os.Lstat(filepath.Join(abs, name, "SKILL.md"))
		switch {
		case os.IsNotExist(err):
		case err != nil:
			return nil, nil, fmt.Errorf("read %s/%s: %w", dir, name, err)
		case info.Mode()&os.ModeSymlink != 0:
			links = append(links, name)
		case info.Mode().IsRegular():
			skills = append(skills, name)
		}
	}
	return skills, links, nil
}

func unsupported(dir, name string) error {
	return fmt.Errorf("%s/%s is a symlink, or holds a symlinked SKILL.md; fw doesn't copy symlinked skills. Replace it with a real folder", dir, name)
}

// samePath reports whether two project-relative paths name one place: by
// spelling, by filesystem identity when both exist, and when neither does,
// by a case-folded final name in folders that are the same place. So a
// lock path whose folder was deleted still matches its case alias.
func samePath(root, a, b string) bool {
	if a == b {
		return true
	}
	// Lstat: resolving a name never follows a link
	x, errX := os.Lstat(filepath.Join(root, filepath.FromSlash(a)))
	y, errY := os.Lstat(filepath.Join(root, filepath.FromSlash(b)))
	switch {
	case errX == nil && errY == nil:
		return os.SameFile(x, y)
	case !notThere(errX) || !notThere(errY):
		return false
	case !strings.EqualFold(path.Base(a), path.Base(b)):
		return false
	}
	da, db := path.Dir(a), path.Dir(b)
	if da == "." || db == "." {
		return da == db
	}
	return samePath(root, da, db)
}

// diskSpelling is rel as the filesystem spells it: each part that exists
// takes its directory entry's name, so a case alias such as .AGENTS/skills
// comes back as .agents/skills. Parts that don't exist keep their spelling.
func diskSpelling(root, rel string) string {
	parts := strings.Split(rel, "/")
	dir := root
	for i, part := range parts {
		entries, err := os.ReadDir(dir)
		if err != nil {
			break
		}
		typed, err := os.Lstat(filepath.Join(dir, part))
		if err != nil {
			break
		}
		for _, e := range entries {
			if e.Name() == part {
				break
			}
			if strings.EqualFold(e.Name(), part) {
				if onDisk, err := os.Lstat(filepath.Join(dir, e.Name())); err == nil && os.SameFile(onDisk, typed) {
					parts[i] = e.Name()
					break
				}
			}
		}
		dir = filepath.Join(dir, parts[i])
	}
	return strings.Join(parts, "/")
}

// copyInstall installs one skill; tests replace it to fail partway.
var copyInstall = installInto

// lockSync takes the skill sync lock, and returns its release.
func lockSync(root string) (func(), error) {
	lock, err := safePath(root, ".framework/skills.sync.lock")
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Dir(lock), 0755); err != nil {
		return nil, err
	}
	if err = os.Mkdir(lock, 0700); err != nil {
		return nil, errors.New("skill sync is locked; if the previous process stopped, remove .framework/skills.sync.lock and retry")
	}
	return func() { _ = os.Remove(lock) }, nil
}

// copyTargets checks the source and the targets, or with none named, finds
// every other assistant skills folder the project has, sorted by path.
func copyTargets(root, from string, named []string) ([]string, error) {
	if _, err := safePath(root, from); err != nil {
		return nil, fmt.Errorf("%s: %w", from, err)
	}
	if r, err := isReserved(root, from); err != nil {
		return nil, err
	} else if r {
		return nil, fmt.Errorf("%s overlaps .framework, which holds the project's Framework state; copy from a skills folder", from)
	}
	info, err := lookup(root, from)
	switch {
	case err != nil:
		return nil, err
	case info == nil:
		return nil, fmt.Errorf("the source folder %s does not exist", from)
	case !info.IsDir():
		return nil, fmt.Errorf("the source %s is not a folder", from)
	}
	if len(named) == 0 {
		for _, dir := range AssistantSkillsDirs() {
			// Lstat, so a symlink, even one that leads nowhere, is an error
			// rather than a folder left out
			info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(dir)))
			switch {
			case os.IsNotExist(err):
				continue
			case err != nil:
				return nil, fmt.Errorf("look up %s: %w", dir, err)
			case info.Mode()&os.ModeSymlink != 0:
				return nil, fmt.Errorf("%s is a symlink; fw only copies into real skills folders", dir)
			case !info.IsDir():
				return nil, fmt.Errorf("%s is not a folder; fw only copies into real skills folders", dir)
			}
			// The source itself is no target; a folder inside it, or around
			// it, is an error
			if same, err := sameFolder(root, dir, from); err != nil {
				return nil, err
			} else if same {
				continue
			}
			if over, err := overlaps(root, dir, from); err != nil {
				return nil, err
			} else if over {
				return nil, fmt.Errorf("%s and %s are inside each other; pick folders side by side", from, dir)
			}
			named = append(named, dir)
		}
		if len(named) == 0 {
			return nil, fmt.Errorf("this project has no other assistant skills folder to copy %s to; name one with --to or --to-dir", from)
		}
	}
	var targets []string
	for _, to := range named {
		if _, err := safePath(root, to); err != nil {
			return nil, fmt.Errorf("%s: %w", to, err)
		}
		// A file anywhere above the target, checked first and directly, so
		// every OS refuses it the same way: Windows reports a path under a
		// file as not found, where others say "not a directory"
		if blocked := inTheWay(root, to); blocked != "" && blocked != to {
			return nil, fmt.Errorf("the target %s is under %s, which is a file, not a folder", to, blocked)
		}
		if r, err := isReserved(root, to); err != nil {
			return nil, err
		} else if r {
			return nil, fmt.Errorf("%s overlaps .framework, which holds the project's Framework state; pick another folder", to)
		}
		if same, err := sameFolder(root, to, from); err != nil {
			return nil, err
		} else if same {
			return nil, fmt.Errorf("the source and target are both %s", from)
		}
		if over, err := overlaps(root, to, from); err != nil {
			return nil, err
		} else if over {
			return nil, fmt.Errorf("%s and %s are inside each other; pick folders side by side", from, to)
		}
		for _, other := range targets {
			if over, err := overlaps(root, to, other); err != nil {
				return nil, err
			} else if over {
				return nil, fmt.Errorf("the targets %s and %s are the same folder, or one is inside the other", other, to)
			}
		}
		if info, err := lookup(root, to); err != nil {
			return nil, err
		} else if info != nil && !info.IsDir() {
			return nil, fmt.Errorf("the target %s is not a folder", to)
		}
		targets = append(targets, to)
	}
	return targets, nil
}

// Copy makes each target folder's copy of the source's skills match the
// source: SKILL.md and its companion files, with executable modes. It
// backs up each copy it replaces to .framework/skills-backups/, skips copies
// that already match, and never deletes a skill. Files a skill excludes
// from sync, such as .env, are not copied and stay in the target. Skills
// only in a target are listed, not read or changed. When the source skill
// is tracked, a real copy records each target in its lock entry, keeping
// the entry's hash, so fw skills push still sees an edited skill and every
// copy moves with it; an untracked source leaves the lock alone. A target
// the lock records as another skill's copy is refused. A real run takes
// the sync lock before it looks at the project; a dry run takes no lock
// and writes nothing.
func Copy(o CopyOptions) (CopyResult, error) {
	res := CopyResult{To: []string{}, Counts: map[string]int{StatusCopied: 0, StatusCurrent: 0, StatusMissing: 0}, Skills: []CopyItem{}, Backups: []string{}}
	root, err := filepath.Abs(o.Root)
	if err != nil {
		return res, err
	}
	from, err := CleanFolder(o.From)
	if err != nil {
		return res, err
	}
	var named []string
	for _, raw := range o.To {
		to, err := CleanFolder(raw)
		if err != nil {
			return res, err
		}
		named = append(named, to)
	}
	var requested []string
	for _, name := range o.Skills {
		if !contains(requested, name) {
			requested = append(requested, name)
		}
	}

	if !o.DryRun {
		release, err := lockSync(root)
		if err != nil {
			return res, err
		}
		defer release()
	}
	targets, err := copyTargets(root, from, named)
	if err != nil {
		return res, err
	}
	// The folders as the filesystem spells them, so the lock and the
	// output name them as pull, push, and sync will see them
	from = diskSpelling(root, from)
	for i := range targets {
		targets[i] = diskSpelling(root, targets[i])
	}
	res.From, res.To = from, targets

	present, links, err := discover(root, from)
	if err != nil {
		return res, err
	}
	names := present
	if len(requested) > 0 {
		names = nil
		var unknown []string
		for _, name := range requested {
			switch {
			case contains(links, name):
				return res, unsupported(from, name)
			case contains(present, name):
				names = append(names, name)
			default:
				unknown = append(unknown, name)
			}
		}
		switch len(unknown) {
		case 0:
		case 1:
			return res, fmt.Errorf("no skill named %s in %s", unknown[0], from)
		default:
			return res, fmt.Errorf("no skills named %s in %s", strings.Join(unknown, ", "), from)
		}
	} else if len(links) > 0 {
		return res, unsupported(from, links[0])
	}

	// Read and check everything before writing anything
	bundles := map[string][]File{}
	for _, name := range names {
		files, err := Scan(filepath.Join(root, filepath.FromSlash(from), name))
		if err != nil {
			return res, fmt.Errorf("%s/%s: %w", from, name, err)
		}
		if _, err = Hash(files); err != nil {
			return res, fmt.Errorf("%s/%s: %w", from, name, err)
		}
		bundles[name] = files
	}
	for _, to := range targets {
		_, targetLinks, err := discover(root, to)
		if err != nil {
			return res, err
		}
		for _, name := range names {
			relative := to + "/" + name
			if contains(targetLinks, name) {
				return res, unsupported(to, name)
			}
			target, err := safePath(root, relative)
			if err != nil {
				return res, fmt.Errorf("%s: %w", relative, err)
			}
			info, err := lookup(root, relative)
			if err != nil {
				return res, err
			}
			if info != nil {
				// The SKILL.md check, not the listing, so another case of the
				// name on a case-insensitive volume counts as this skill
				md, err := os.Lstat(filepath.Join(target, "SKILL.md"))
				if err != nil && !os.IsNotExist(err) {
					return res, fmt.Errorf("look up %s/SKILL.md: %w", relative, err)
				}
				if !info.IsDir() || err != nil || !md.Mode().IsRegular() {
					return res, fmt.Errorf("%s is not a skill (it has no SKILL.md); move it aside and copy again", relative)
				}
				if _, err := Scan(target); err != nil {
					return res, fmt.Errorf("%s: %w", relative, err)
				}
			}
		}
		if len(requested) == 0 && len(targetLinks) > 0 {
			return res, unsupported(to, targetLinks[0])
		}
	}

	// The plan: a target the lock records as another skill's copy is never
	// copied over, and a tracked source's entry gains each target, with the
	// source's record
	lock, _, err := loadLock(root)
	if err != nil {
		return res, err
	}
	disk := NewDisk(root)
	plan, err := Plan(PlanInput{Command: CommandCopy, Disk: disk, Lock: lock,
		Options: PlanOptions{CopyFrom: from, CopyTo: targets, CopyNames: names, CopyAll: len(requested) == 0}})
	if err != nil {
		return res, err
	}
	// Each target as the filesystem spells it, to its last part: ANALYSIS
	// on disk is recorded and shown as ANALYSIS
	for i := range plan.Steps {
		st := &plan.Steps[i]
		spelled := diskSpelling(root, st.Path)
		if spelled == st.Path {
			continue
		}
		if e := plan.LockAfter.Skills[st.Entry]; e != nil && st.Entry != "" {
			if r, ok := e.Copies[st.Path]; ok {
				delete(e.Copies, st.Path)
				e.Copies[spelled] = r
			}
		}
		st.Path = spelled
	}
	// Only skill folders, with a SKILL.md, are listed as missing from the
	// source
	listed := map[string]bool{}
	for _, to := range targets {
		existing, _, err := discover(root, to)
		if err != nil {
			return res, err
		}
		for _, n := range existing {
			listed[to+"/"+n] = true
		}
	}
	kept := plan.Steps[:0]
	for _, st := range plan.Steps {
		if st.Status != StatusMissing || listed[st.Path] {
			kept = append(kept, st)
		}
	}
	plan.Steps = kept
	res.Steps = plan.Steps
	type step struct {
		item    int // index in res.Skills
		files   []File
		current string
	}
	var steps []step
	for _, st := range plan.Steps {
		item := CopyItem{Name: st.Name, Status: st.Status, To: st.Path}
		if st.Status != StatusMissing {
			item.From = st.From
			steps = append(steps, step{len(res.Skills), bundles[st.Name], disk.Look(st.Path).Hash})
		}
		res.Skills = append(res.Skills, item)
	}
	for _, it := range res.Skills {
		res.Counts[it.Status]++
	}
	if o.DryRun {
		return res, nil
	}

	var done []int
	for _, s := range steps {
		item := &res.Skills[s.item]
		if item.Status != StatusCopied {
			continue
		}
		var backups []string
		if err = copyInstall(&backups, root, []string{item.To}, s.files, s.current); err != nil {
			cause := fmt.Errorf("copying %s to %s failed: %w", item.From, item.To, err)
			// The installer moved this copy aside and couldn't put it back
			var lost *RestoreError
			var failed []CopyItem
			if errors.As(err, &lost) {
				failed = []CopyItem{{Name: item.Name, Status: item.Status, From: item.From, To: item.To, Backup: lost.Backup}}
			}
			return res, rollback(root, &res, done, failed, cause)
		}
		if len(backups) > 0 {
			item.Backup = backups[0]
			res.Backups = append(res.Backups, backups[0])
		}
		done = append(done, s.item)
	}
	if _, err = saveIfChanged(root, lock, plan.LockAfter); err != nil {
		return res, fmt.Errorf("copied, but could not record the copies in %s: %w", lockFile, err)
	}
	return res, nil
}

// rollback puts back the copies a failed batch already made, newest first.
// What it can't put back, it lists in the error with its backup, after
// failed: the copy whose install failed and lost its old copy.
func rollback(root string, res *CopyResult, done []int, failed []CopyItem, cause error) error {
	var stuck []CopyItem
	for i := len(done) - 1; i >= 0; i-- {
		it := res.Skills[done[i]]
		target := filepath.Join(root, filepath.FromSlash(it.To))
		err := os.RemoveAll(target)
		if err == nil && it.Backup != "" {
			err = os.Rename(filepath.Join(root, filepath.FromSlash(it.Backup)), target)
		}
		if err != nil {
			stuck = append([]CopyItem{it}, stuck...)
		}
	}
	stuck = append(stuck, failed...)
	res.Backups = res.Backups[:0]
	for _, it := range stuck {
		if it.Backup != "" {
			res.Backups = append(res.Backups, it.Backup)
		}
	}
	if len(stuck) == 0 && len(done) > 0 {
		copies := "copies"
		if len(done) == 1 {
			copies = "copy"
		}
		cause = fmt.Errorf("%w. fw put back the %d %s it had already made, so nothing changed", cause, len(done), copies)
	}
	return &CopyError{Err: cause, Replaced: stuck}
}
