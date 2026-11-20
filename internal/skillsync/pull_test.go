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
