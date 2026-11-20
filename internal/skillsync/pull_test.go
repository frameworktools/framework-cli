package skillsync

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// pkg is a package skill whose history lists the earlier bodies.
func pkg(t *testing.T, name, body string, earlier ...string) Source {
	t.Helper()
	var known []string
	for _, b := range earlier {
		h, _ := Hash(BundleFromText(skillMD(name, b), nil))
		known = append(known, h)
	}
	return PackageSource(name, "stata", BundleFromText(skillMD(name, body), nil), known)
}

// A copy edited between the plan and the write is left alone: its step
// becomes edited (changed_during_pull), and it keeps its old record while
// the other copy moves on
func TestPullKeepsACopyEditedDuringThePull(t *testing.T) {
	root := t.TempDir()
	dirs := []string{".agents/skills", ".claude/skills"}
	if _, err := pullSkills(root, dirs, pkg(t, "x", "v1")); err != nil {
		t.Fatal(err)
	}
	before, _ := ReadLock(root)
	old := before.Skills["framework:x@stata"].Copies[".claude/skills/x"].Hash
	defer func() { pullInstall = installInto }()
	pullInstall = func(backups *[]string, root string, paths []string, files []File, expected string) error {
		if paths[0] == ".agents/skills/x" {
			// Someone edits the other copy while the first is written
			writeRel(t, root, ".claude/skills/x/SKILL.md", skillMD("x", "mine"))
		}
		return installInto(backups, root, paths, files, expected)
	}
	res, err := pullSkills(root, dirs, pkg(t, "x", "v2", "v1"))
	if err != nil {
		t.Fatal(err)
	}
	var reasons []string
	for _, s := range res.Steps {
		reasons = append(reasons, s.Path+":"+s.Status+":"+s.Reason)
	}
	if strings.Join(reasons, ",") != ".agents/skills/x:updated:,.claude/skills/x:edited:changed_during_pull" {
		t.Errorf("steps: %v", reasons)
	}
	if !strings.Contains(readRel(t, root, ".claude/skills/x/SKILL.md"), "mine") {
		t.Error("the edit was overwritten")
	}
	after, _ := ReadLock(root)
	e := after.Skills["framework:x@stata"]
	if e.Copies[".claude/skills/x"].Hash != old || e.Copies[".agents/skills/x"].Hash == old || e.Base.Hash != old {
		t.Errorf("lock: %+v", e)
	}
}

// A write that fails partway saves a lock that says which copies moved
func TestPullFailurePartwaySavesAnAccurateLock(t *testing.T) {
	root := t.TempDir()
	dirs := []string{".agents/skills", ".claude/skills"}
	if _, err := pullSkills(root, dirs, pkg(t, "x", "v1")); err != nil {
		t.Fatal(err)
	}
	before, _ := ReadLock(root)
	old := before.Skills["framework:x@stata"].Copies[".claude/skills/x"].Hash
	defer func() { pullInstall = installInto }()
	broken := errors.New("disk full")
	pullInstall = func(backups *[]string, root string, paths []string, files []File, expected string) error {
		if paths[0] == ".claude/skills/x" {
			return broken
		}
		return installInto(backups, root, paths, files, expected)
	}
	if _, err := pullSkills(root, dirs, pkg(t, "x", "v2", "v1")); !errors.Is(err, broken) {
		t.Fatalf("got %v", err)
	}
	after, _ := ReadLock(root)
	e := after.Skills["framework:x@stata"]
	if e.Copies[".claude/skills/x"].Hash != old || e.Copies[".agents/skills/x"].Hash == old {
		t.Errorf("lock: %+v", e.Copies)
	}
}

// A skills folder fw can't read stops the pull before it writes anything
func TestPullListsBeforeWriting(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("folder permissions don't block reads on Windows")
	}
	root := t.TempDir()
	agents := filepath.Join(root, ".agents", "skills")
	check(t, os.MkdirAll(agents, 0o755))
	check(t, os.Chmod(agents, 0))
	t.Cleanup(func() { _ = os.Chmod(agents, 0o700) }) //nolint:gosec // a folder, opened again so the test can remove it
	if _, err := os.ReadDir(agents); err == nil {
		t.Skip("running with permissions that read any folder")
	}
	if _, err := pullSkills(root, []string{".claude/skills", ".agents/skills"}, pkg(t, "x", "v1")); err == nil {
		t.Fatal("pulled past an unreadable folder")
	}
	if entriesIn(t, root, ".claude/skills") != nil || entriesIn(t, root, ".framework") != nil {
		t.Error("pull wrote before failing")
	}
}

// A lock from a newer client stops every command with the upgrade advice,
// and a v1 lock that nothing changes is left as it is
func TestLockSchemas(t *testing.T) {
	root := t.TempDir()
	writeRel(t, root, lockFile, `{"schema":3,"skills":{}}`)
	_, err := pullSkills(root, []string{".claude/skills"}, pkg(t, "x", "v1"))
	var se *LockSchemaError
	if !errors.As(err, &se) || se.ReasonCode() != "lock_schema_unsupported" || !strings.Contains(err.Error(), "uses schema 3") || !strings.Contains(err.Error(), "Update fw") {
		t.Errorf("pull: %v", err)
	}
	for name, run := range map[string]func() error{
		"copy": func() error {
			_, err := Copy(CopyOptions{Root: root, From: ".claude/skills", To: []string{".agents/skills"}})
			return err
		},
		"move": func() error { _, err := Move(MoveOptions{Root: root, From: "claude", To: "agents"}); return err },
		"push": func() error { _, err := PlanPush(PushOptions{Root: root}); return err },
	} {
		writeRel(t, root, ".claude/skills/x/SKILL.md", skillMD("x", "v1"))
		if err := run(); !errors.As(err, &se) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if readRel(t, root, lockFile) != `{"schema":3,"skills":{}}` {
		t.Error("the newer lock was rewritten")
	}
	// A lock with no schema is no lock fw knows
	writeRel(t, root, lockFile, `{"skills":{}}`)
	if _, err := ReadLock(root); err == nil || errors.As(err, &se) {
		t.Errorf("no schema: %v", err)
	}
}

// A push reuses an unfinished push's request ID when it sends the same
// bytes against the same version, so Cloud can answer it once
func TestPushReusesAPendingRequest(t *testing.T) {
	root := t.TempDir()
	writeRel(t, root, ".claude/skills/x/SKILL.md", skillMD("x", "edited"))
	files, _ := Scan(filepath.Join(root, ".claude", "skills", "x"))
	h, _ := Hash(files)
	writeRel(t, root, lockFile, `{"schema":1,"skills":{"a":{"paths":[".claude/skills/x"],"variant":"stata","server":"https://cloud.test",`+
		`"skill_id":"acct-1","version":2,"hash":"old","pending":{"request_id":"req-1","hash":"`+h+`","basis":"2:acct-1:stata"}}}}`)
	pp, err := PlanPush(PushOptions{Root: root, Dirs: []string{".claude/skills"},
		Remote: func(string, LockEntry) (Remote, bool, error) { return Remote{Version: 2, Hash: "old"}, true, nil }})
	if err != nil || !pp.Pending() {
		t.Fatalf("%+v %v", pp, err)
	}
	var sent PushRequest
	err = pp.Apply("https://cloud.test", func(req PushRequest) (Snapshot, error) {
		sent = req
		return Snapshot{ID: "acct-1", Version: 3, Hash: h}, nil
	})
	if err != nil || sent.RequestID != "req-1" || sent.ExpectedVersion != 2 || sent.SkillID != "acct-1" {
		t.Errorf("sent %+v: %v", sent, err)
	}
	lock, _ := ReadLock(root)
	if e := lock.Skills["https://cloud.test/acct-1@stata"]; e == nil || e.Pending != nil || e.Base.Version != 3 {
		t.Errorf("lock: %+v", e)
	}
}

// A setup's sync.known_hashes are known hashes of the Cloud skill: a copy
// installed before the lock that holds one is an unedited older version
func TestCloudKnownHashesRecognizeAnOlderCopy(t *testing.T) {
	root := t.TempDir()
	writeRel(t, root, ".claude/skills/x/SKILL.md", skillMD("x", "v1"))
	old, _ := Hash(BundleFromText(skillMD("x", "v1"), nil))
	files := BundleFromText(skillMD("x", "v2"), nil)
	h, _ := Hash(files)
	src := CloudSource("x", SyncSource{ID: "acct-1", Version: 2, Hash: h, Variant: "stata", KnownHashes: []string{old, h}}, files, "https://cloud.test")
	res, err := pullSkills(root, []string{".claude/skills"}, src)
	if err != nil || len(res.Rows) != 1 || res.Rows[0].Status != StatusUpdated {
		t.Fatalf("%+v %v", res.Rows, err)
	}
	lock, _ := ReadLock(root)
	if e := lock.Skills["https://cloud.test/acct-1@stata"]; e == nil || len(e.Known) != 2 {
		t.Errorf("lock: %+v", e)
	}
}

// foldDisk is an empty project on a case-insensitive volume.
type foldDisk struct{}

func (foldDisk) Look(string) Look             { return Look{Absent: true} }
func (foldDisk) FolderExists(string) bool     { return true }
func (foldDisk) SkillFolders(string) []string { return nil }
func (foldDisk) Same(a, b string) bool        { return strings.EqualFold(a, b) }

// Two setup names that are one folder on a case-insensitive volume (macOS,
// Windows) stop the pull with paths_alias, before anything is written
func TestSetupNamesThatAliasStop(t *testing.T) {
	_, err := Plan(PlanInput{Command: CommandPull, Disk: foldDisk{}, Lock: newLock(),
		Offer:   Offer{Source: "cloud", Skills: []Source{pkg(t, "Demo", "a"), pkg(t, "demo", "b")}},
		Options: PlanOptions{Dirs: []string{".claude/skills"}}})
	var pe *PlanError
	if !errors.As(err, &pe) || pe.ReasonCode() != "paths_alias" || !strings.Contains(err.Error(), ".claude/skills/Demo") {
		t.Errorf("planner: %v", err)
	}

	root := t.TempDir()
	caseInsensitive(t, root)
	_, err = pullSkills(root, []string{".claude/skills"}, pkg(t, "Demo", "a"), pkg(t, "demo", "b"))
	if !errors.As(err, &pe) || pe.ReasonCode() != "paths_alias" || entriesIn(t, root, ".claude") != nil {
		t.Errorf("pull: %v", err)
	}
}

// A second pull with nothing new leaves the lock's bytes as they are
func TestLockBytesStableAcrossPulls(t *testing.T) {
	root := t.TempDir()
	dirs := []string{".agents/skills", ".claude/skills"}
	srcs := []Source{pkg(t, "a", "v1"), pkg(t, "b", "v1")}
	if _, err := pullSkills(root, dirs, srcs...); err != nil {
		t.Fatal(err)
	}
	first := readRel(t, root, lockFile)
	res, err := pullSkills(root, dirs, srcs...)
	if err != nil || res.Saved || readRel(t, root, lockFile) != first {
		t.Errorf("rewritten: saved %v %v\n%s", res.Saved, err, readRel(t, root, lockFile))
	}
}

// A lock changed between planning and confirming stops the push before
// anything is sent or saved
func TestPushStopsWhenTheLockChanged(t *testing.T) {
	root := t.TempDir()
	writeRel(t, root, ".claude/skills/x/SKILL.md", skillMD("x", "edited"))
	writeRel(t, root, lockFile, `{"schema":1,"skills":{"a":{"paths":[".claude/skills/x"],"variant":"stata","server":"https://cloud.test","skill_id":"acct-1","version":2,"hash":"old"}}}`)
	pp, err := PlanPush(PushOptions{Root: root, Dirs: []string{".claude/skills"},
		Remote: func(string, LockEntry) (Remote, bool, error) { return Remote{Version: 2, Hash: "old"}, true, nil }})
	if err != nil || !pp.Pending() {
		t.Fatalf("%+v %v", pp, err)
	}
	changed := `{"schema":1,"skills":{"a":{"paths":[".agents/skills/x"],"variant":"stata","server":"https://cloud.test","skill_id":"acct-1","version":2,"hash":"old"}}}`
	writeRel(t, root, lockFile, changed)
	sent := false
	err = pp.Apply("https://cloud.test", func(PushRequest) (Snapshot, error) { sent = true; return Snapshot{}, nil })
	var pe *PlanError
	if !errors.As(err, &pe) || pe.ReasonCode() != "lock_changed" || sent || readRel(t, root, lockFile) != changed {
		t.Errorf("%v (sent %v)", err, sent)
	}
}

// A move whose second rename fails moves the first folder back and leaves
// the lock as it was; a folder it can't move back is named
func TestMoveUndoesAPartialMove(t *testing.T) {
	setup := func(t *testing.T) string {
		root := t.TempDir()
		for _, n := range []string{"a", "b"} {
			writeRel(t, root, ".claude/skills/"+n+"/SKILL.md", skillMD(n, n))
		}
		writeRel(t, root, lockFile, `{"schema":1,"skills":{"a":{"paths":[".claude/skills/a"],"variant":"r","hash":"x"},"b":{"paths":[".claude/skills/b"],"variant":"r","hash":"y"}}}`)
		return root
	}
	t.Cleanup(func() { rename = os.Rename })
	for _, stuck := range []bool{false, true} {
		root := setup(t)
		lock := readRel(t, root, lockFile)
		calls := 0
		rename = func(from, to string) error {
			calls++
			if calls == 2 || (stuck && calls == 3) {
				return errors.New("disk full")
			}
			return os.Rename(from, to)
		}
		_, err := Move(MoveOptions{Root: root, From: ".claude/skills", To: ".agents/skills"})
		rename = os.Rename
		if readRel(t, root, lockFile) != lock {
			t.Errorf("stuck %v: the lock was saved", stuck)
		}
		if stuck {
			if err == nil || !strings.Contains(err.Error(), "couldn't be moved back: .agents/skills/a (it belongs at .claude/skills/a)") {
				t.Errorf("stuck: %v", err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), "moved back the folders") || entriesIn(t, root, ".agents/skills") != nil ||
			strings.Join(entriesIn(t, root, ".claude/skills"), ",") != "a,b" {
			t.Errorf("undo: %v %v", err, entriesIn(t, root, ".agents/skills"))
		}
	}
}

// Absent names that differ only in case are one path only where the volume
// folds case: on a case-sensitive one (Linux), Demo and demo are two
// folders, and two setup names never falsely raise paths_alias
func TestSamePathFoldsOnlyOnCaseInsensitiveVolumes(t *testing.T) {
	root := t.TempDir()
	check(t, os.MkdirAll(filepath.Join(root, ".claude", "skills"), 0o750))
	_, err := os.Stat(filepath.Join(root, ".CLAUDE", "SKILLS"))
	folds := err == nil
	if foldsCase(filepath.Join(root, ".claude", "skills")) != folds {
		t.Fatalf("foldsCase says %v; the volume says %v", !folds, folds)
	}
	if got := samePath(root, ".claude/skills/Demo", ".claude/skills/demo"); got != folds {
		t.Errorf("samePath: %v on a volume that folds case: %v", got, folds)
	}
	_, err = pullSkills(root, []string{".claude/skills"}, pkg(t, "Demo", "a"), pkg(t, "demo", "b"))
	var pe *PlanError
	if aliased := errors.As(err, &pe) && pe.ReasonCode() == "paths_alias"; aliased != folds {
		t.Errorf("paths_alias %v (%v) on a volume that folds case: %v", aliased, err, folds)
	}
}

// A new skill whose second copy was declined keeps its entry and base for
// the copy that was written
func TestDeclineKeepsANewEntrysBase(t *testing.T) {
	root := t.TempDir()
	defer func() { pullInstall = installInto }()
	pullInstall = func(backups *[]string, root string, paths []string, files []File, expected string) error {
		if paths[0] == ".claude/skills/x" {
			return errChanged
		}
		return installInto(backups, root, paths, files, expected)
	}
	if _, err := pullSkills(root, []string{".agents/skills", ".claude/skills"}, pkg(t, "x", "v1")); err != nil {
		t.Fatal(err)
	}
	lock, _ := ReadLock(root)
	e := lock.Skills["framework:x@stata"]
	if e == nil || e.Base == nil || len(e.Copies) != 1 || e.Copies[".agents/skills/x"].Hash != e.Base.Hash {
		t.Errorf("lock: %+v", e)
	}
}

// After a copy is deleted from .claude/skills (the folder still there) and
// a same-named folder sits untracked in .agents/skills, push gives the move
// hint on both rows
func TestPushTwinHintWithTheOldFolderThere(t *testing.T) {
	root := t.TempDir()
	check(t, os.MkdirAll(filepath.Join(root, ".claude", "skills"), 0o750))
	writeRel(t, root, ".agents/skills/mine/SKILL.md", skillMD("mine", "e1"))
	writeRel(t, root, lockFile, `{"schema":1,"skills":{"a":{"paths":[".claude/skills/mine"],"variant":"r","server":"https://cloud.test","skill_id":"acct-1","version":1,"hash":"x"}}}`)
	pp, err := PlanPush(PushOptions{Root: root, Dirs: []string{".agents/skills"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range pp.Steps {
		if s.Hint == nil || s.Hint.Key != HintMove || s.Hint.From != ".claude/skills/mine" || s.Hint.To[0] != ".agents/skills/mine" {
			t.Errorf("%s %s: %+v", s.Path, s.Status, s.Hint)
		}
	}
	if len(pp.Steps) != 2 {
		t.Errorf("steps: %+v", pp.Steps)
	}
}

// A move with a retarget judges each copy where it is now: the moved
// folder's hash, read at its old place before the rename, joins known
func TestMoveAndRetargetKnownHashes(t *testing.T) {
	root := t.TempDir()
	writeRel(t, root, ".claude/skills/demo/SKILL.md", skillMD("demo", "d1"))
	files, _ := Scan(filepath.Join(root, ".claude", "skills", "demo"))
	h, _ := Hash(files)
	writeRel(t, root, lockFile, `{"schema":1,"skills":{"package:demo":{"paths":[".claude/skills/demo"],"variant":"r","hash":"`+h+`","origin":{"skill_id":"framework:demo","hash":"`+h+`"}}}}`)
	res, err := Move(MoveOptions{Root: root, From: ".claude/skills/demo", To: ".agents/skills/demo", SkillID: "uuid-fork", Server: "https://cloud.test"})
	if err != nil || len(res.Steps) != 2 || res.Steps[0].Reason != ReasonPathMoved || res.Steps[1].Reason != ReasonRetargeted {
		t.Fatalf("%+v %v", res.Steps, err)
	}
	lock, _ := ReadLock(root)
	e := lock.Skills["https://cloud.test/uuid-fork@r"]
	if e == nil || strings.Join(e.Known, ",") != h || e.Copies[".agents/skills/demo"].Hash != h || e.Base != nil {
		t.Errorf("lock: %+v", e)
	}
}
