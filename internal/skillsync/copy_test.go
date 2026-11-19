package skillsync

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/frameworktools/framework-cli/internal/cloud"
)

func writeRel(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	check(t, os.MkdirAll(filepath.Dir(p), 0755))
	check(t, os.WriteFile(p, []byte(body), 0644))
}

func readRel(t *testing.T, root, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func skillMD(name, body string) string { return "---\nname: " + name + "\n---\n" + body + "\n" }

// entriesIn lists a folder's entries, or nothing when it doesn't exist.
func entriesIn(t *testing.T, root, rel string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestAssistantSkillsDirs(t *testing.T) {
	for name, want := range map[string]string{"claude": ".claude/skills", "agents": ".agents/skills", "codex": ".agents/skills", "copilot": ".agents/skills", "cursor": ""} {
		if got := AssistantSkillsDir(name); got != want {
			t.Errorf("%s: %q, want %q", name, got, want)
		}
	}
	if got := strings.Join(AssistantSkillsDirs(), ","); got != ".agents/skills,.claude/skills" {
		t.Errorf("dirs: %s", got)
	}
}

func TestCleanFolder(t *testing.T) {
	for in, want := range map[string]string{"./a/b/": "a/b", `a\b`: "a/b", ".claude/skills": ".claude/skills", "a/./b": "a/b"} {
		if got, err := CleanFolder(in); err != nil || got != want {
			t.Errorf("%q: %q %v", in, got, err)
		}
	}
	// Refused as given, before cleaning: a/x/../b would clean to a/b
	for _, in := range []string{"", "/abs/skills", `\abs`, "D:skills", `D:\skills`, "c:/skills", "a:b", "skills/x:y", "skills/x:stream/y",
		"a/../b", "a/x/../b", "..", `..\x`, `a\..\b`, ".", "./"} {
		if got, err := CleanFolder(in); err == nil {
			t.Errorf("%q: accepted as %q", in, got)
		}
	}
}

// Diverged copies name the fix, and fw skills copy settles them without
// touching the lock.
func TestDivergedCopiesNameTheFix(t *testing.T) {
	root := t.TempDir()
	writeRel(t, root, ".claude/skills/x/SKILL.md", skillMD("x", "edited"))
	writeRel(t, root, ".agents/skills/x/SKILL.md", skillMD("x", "original"))
	lock := `{"schema":1,"skills":{".claude/skills/x":{"paths":[".claude/skills/x",".agents/skills/x"],"variant":"stata"}}}` + "\n"
	writeRel(t, root, ".framework/skills.lock.json", lock)
	client := cloud.NewClient("http://127.0.0.1:1", "token")
	opts := Options{Root: root, Path: ".claude/skills/x", Direction: "status", Variant: "stata"}

	_, err := Sync(context.Background(), client, opts)
	want := `assistant skill copies diverged: .claude/skills/x and .agents/skills/x differ. Pick one and copy it over the other: fw skills copy --from claude --to agents --skill "x"`
	if err == nil || err.Error() != want {
		t.Fatalf("got %v", err)
	}

	res, err := Copy(CopyOptions{Root: root, From: ".claude/skills", To: []string{".agents/skills"}})
	if err != nil || res.Counts[StatusCopied] != 1 || len(res.Backups) != 1 || res.Skills[0].Backup != res.Backups[0] {
		t.Fatalf("copy: %+v %v", res, err)
	}
	if readRel(t, root, ".framework/skills.lock.json") != lock {
		t.Error("the copy changed the lock")
	}
	// No longer diverged: with no Cloud identity, an edited skill is one to push
	if r, err := Sync(context.Background(), client, opts); err != nil || r.Action != "push" {
		t.Errorf("after the copy: %+v %v", r, err)
	}
}

// A custom folder gets --from-dir and --to-dir, with both paths, quoted
func TestDivergedHintForCustomFolders(t *testing.T) {
	two := []string{"vendor/skills/x", ".agents/skills/x"}
	n := copiesNote("", nil, &Entry{Paths: two}, two, nil, map[string]string{two[0]: "a", two[1]: "b"})
	cmd := `fw skills copy --from-dir "vendor/skills" --to-dir ".agents/skills" --skill "x"`
	if n.reason != "vendor/skills/x and .agents/skills/x differ. Pick one and copy it over the other: "+cmd || n.hint != cmd {
		t.Errorf("two: %+v", n)
	}
	// Three or more: every command needed
	three := []string{".claude/skills/x", ".agents/skills/x", "vendor/skills/x"}
	n = copiesNote("", nil, &Entry{Paths: three}, three, nil, map[string]string{three[0]: "a", three[1]: "b", three[2]: "c"})
	cmds := `fw skills copy --from claude --to agents --skill "x" && fw skills copy --from-dir ".claude/skills" --to-dir "vendor/skills" --skill "x"`
	if n.reason != ".claude/skills/x, .agents/skills/x, and vendor/skills/x differ. Pick one and copy it over the others: "+cmds || n.hint != cmds {
		t.Errorf("three: %+v", n)
	}
	if got := copyTo("my skills/it's", ".claude/skills/it's"); got != `fw skills copy --from-dir 'my skills' --to-dir ".claude/skills" --skill 'it'\''s'` {
		t.Errorf("quoting: %s", got)
	}
}

func TestCopyRefusesNestedFolders(t *testing.T) {
	root := t.TempDir()
	writeRel(t, root, "skills/x/SKILL.md", "x\n")
	for _, to := range []string{"skills/x", "skills/inner"} {
		if _, err := Copy(CopyOptions{Root: root, From: "skills", To: []string{to}}); err == nil || !strings.Contains(err.Error(), "inside each other") {
			t.Errorf("%s: %v", to, err)
		}
	}
	if _, err := Copy(CopyOptions{Root: root, From: "skills", To: []string{"./skills/"}}); err == nil || err.Error() != "the source and target are both skills" {
		t.Errorf("same: %v", err)
	}
	if _, err := Copy(CopyOptions{Root: root, From: "skills", To: []string{"a", "a/b"}}); err == nil || !strings.Contains(err.Error(), "the targets a and a/b") {
		t.Errorf("nested targets: %v", err)
	}
	if _, err := Copy(CopyOptions{Root: root, From: "skills", To: []string{"a/../skills"}}); err == nil || !strings.Contains(err.Error(), ".. part") {
		t.Errorf("a .. part: %v", err)
	}
	if entriesIn(t, root, ".framework") != nil {
		t.Error("a refused copy wrote .framework")
	}
}

// Any target in .framework, or holding it, is refused before anything is
// written: the sync lock, the lock file, the backups, .gitignore, and the
// rest of the project's Framework state.
func TestCopyRefusesSyncState(t *testing.T) {
	root := t.TempDir()
	writeRel(t, root, "skills/x/SKILL.md", skillMD("x", "x"))
	lock := `{"schema":1,"skills":{}}` + "\n"
	writeRel(t, root, ".framework/skills.lock.json", lock)
	for _, to := range []string{".framework/skills.sync.lock", ".framework/skills.lock.json", ".framework/skills-backups",
		".framework/skills-backups/old", ".framework/.gitignore", ".framework/anything/skills", ".framework", ".Framework/Skills.Lock.json", ".FRAMEWORK"} {
		for _, dry := range []bool{false, true} {
			_, err := Copy(CopyOptions{Root: root, From: "skills", To: []string{to}, DryRun: dry})
			if err == nil || !strings.Contains(err.Error(), "overlaps .framework") {
				t.Errorf("%s (dry run %v): %v", to, dry, err)
			}
		}
	}
	if readRel(t, root, ".framework/skills.lock.json") != lock || strings.Join(entriesIn(t, root, ".framework"), ",") != "skills.lock.json" {
		t.Errorf("a refused copy wrote to .framework: %v", entriesIn(t, root, ".framework"))
	}
}

// On a case-insensitive volume, Skills is skills: fw compares folders by
// identity, not by name.
func TestCopyComparesFolderIdentity(t *testing.T) {
	root := t.TempDir()
	writeRel(t, root, "skills/x/SKILL.md", skillMD("x", "x"))
	if _, err := os.Stat(filepath.Join(root, "SKILLS")); err != nil {
		t.Skip("this volume is case-sensitive")
	}
	for to, want := range map[string]string{"Skills": "the source and target are both skills", "SKILLS/inner": "inside each other"} {
		if _, err := Copy(CopyOptions{Root: root, From: "skills", To: []string{to}}); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", to, err)
		}
	}
	// The source inside the target, by another case
	writeRel(t, root, "outer/skills/x/SKILL.md", skillMD("x", "x"))
	if _, err := Copy(CopyOptions{Root: root, From: "outer/skills", To: []string{"OUTER"}}); err == nil || !strings.Contains(err.Error(), "inside each other") {
		t.Errorf("source inside target: %v", err)
	}
	check(t, os.MkdirAll(filepath.Join(root, "a"), 0755))
	if _, err := Copy(CopyOptions{Root: root, From: "skills", To: []string{"a", "A"}}); err == nil || !strings.Contains(err.Error(), "the targets a and A") {
		t.Errorf("two targets: %v", err)
	}
}

// A real copy takes the sync lock before it looks at the project at all:
// the source, the default targets, and the overlap checks. A dry run takes
// none.
func TestCopyTakesTheLockFirst(t *testing.T) {
	root := t.TempDir()
	writeRel(t, root, "src/x/SKILL.md", skillMD("x", "x"))
	check(t, os.MkdirAll(filepath.Join(root, ".framework", "skills.sync.lock"), 0700))
	for _, o := range []CopyOptions{
		{From: "nowhere", To: []string{"dst"}},    // a missing source
		{From: "src"},                             // no default target
		{From: "src", To: []string{"src/inner"}},  // nested
		{From: "src", To: []string{".framework"}}, // reserved, by name
	} {
		o.Root = root
		if _, err := Copy(o); err == nil || !strings.Contains(err.Error(), "skill sync is locked") {
			t.Errorf("%+v: %v", o, err)
		}
	}
	if _, err := Copy(CopyOptions{Root: root, From: "nowhere", To: []string{"dst"}, DryRun: true}); err == nil || err.Error() != "the source folder nowhere does not exist" {
		t.Errorf("dry run, missing source: %v", err)
	}
	_, err := Copy(CopyOptions{Root: root, From: "src", To: []string{"dst"}, Skills: []string{"nope"}})
	if err == nil || !strings.Contains(err.Error(), "skill sync is locked") {
		t.Errorf("real run: %v", err)
	}
	if _, err = Copy(CopyOptions{Root: root, From: "src", To: []string{"dst"}, Skills: []string{"nope"}, DryRun: true}); err == nil || err.Error() != "no skill named nope in src" {
		t.Errorf("dry run, unknown skill: %v", err)
	}
	res, err := Copy(CopyOptions{Root: root, From: "src", To: []string{"dst"}, DryRun: true})
	if err != nil || res.Counts[StatusCopied] != 1 || entriesIn(t, root, "dst") != nil {
		t.Errorf("dry run: %+v %v", res, err)
	}
}

// A skill is a folder with a regular SKILL.md; other folders are ignored,
// read errors are errors, and symlinked skills are refused by name.
func TestCopyDiscovery(t *testing.T) {
	root := t.TempDir()
	writeRel(t, root, "src/x/SKILL.md", skillMD("x", "x"))
	writeRel(t, root, "src/notes/README.md", "not a skill\n")
	writeRel(t, root, "src/README.md", "a file\n")
	writeRel(t, root, "dst/notes-too/todo.md", "not a skill either\n")
	res, err := Copy(CopyOptions{Root: root, From: "src", To: []string{"dst"}})
	if err != nil || len(res.Skills) != 1 || res.Skills[0].Name != "x" || strings.Join(entriesIn(t, root, "dst"), ",") != "notes-too,x" {
		t.Fatalf("notes: %+v %v %v", res, err, entriesIn(t, root, "dst"))
	}
	// A target folder with the skill's name but no SKILL.md isn't replaced
	writeRel(t, root, "src/notes-too/SKILL.md", skillMD("notes-too", "now a skill"))
	if _, err = Copy(CopyOptions{Root: root, From: "src", To: []string{"dst"}}); err == nil || !strings.Contains(err.Error(), "dst/notes-too is not a skill") {
		t.Errorf("not a skill: %v", err)
	}
	check(t, os.RemoveAll(filepath.Join(root, "src", "notes-too")))

	if runtime.GOOS != "windows" {
		writeRel(t, root, "elsewhere/linked/SKILL.md", skillMD("linked", "linked"))
		check(t, os.Symlink(filepath.Join(root, "elsewhere", "linked"), filepath.Join(root, "src", "linked")))
		for _, skills := range [][]string{nil, {"linked"}} {
			if _, err = Copy(CopyOptions{Root: root, From: "src", To: []string{"dst"}, Skills: skills}); err == nil || !strings.Contains(err.Error(), "src/linked is a symlink") {
				t.Errorf("symlinked skill %v: %v", skills, err)
			}
		}
		if _, err = Copy(CopyOptions{Root: root, From: "src", To: []string{"dst"}, Skills: []string{"x"}}); err != nil {
			t.Errorf("a symlink not asked for: %v", err)
		}
		check(t, os.Remove(filepath.Join(root, "src", "linked")))
	}

	if runtime.GOOS != "windows" && os.Geteuid() != 0 {
		check(t, os.Chmod(filepath.Join(root, "src"), 0o300))
		defer func() { check(t, os.Chmod(filepath.Join(root, "src"), 0o755)) }()
		if _, err = Copy(CopyOptions{Root: root, From: "src", To: []string{"dst"}, DryRun: true}); err == nil || !strings.Contains(err.Error(), "read src") {
			t.Errorf("unreadable source: %v", err)
		}
		if _, err = SkillFolders(root, "src"); err == nil {
			t.Error("SkillFolders hid a read error")
		}
	}
}

func TestCopySkillFilter(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a", "b", "c"} {
		writeRel(t, root, "src/"+name+"/SKILL.md", skillMD(name, name))
	}
	res, err := Copy(CopyOptions{Root: root, From: "src", To: []string{"dst"}, Skills: []string{"b", "a", "b"}, DryRun: true})
	if err != nil || len(res.Skills) != 2 || res.Skills[0].Name != "b" || res.Skills[1].Name != "a" {
		t.Errorf("order: %+v %v", res, err)
	}
	if _, err = Copy(CopyOptions{Root: root, From: "src", To: []string{"dst"}, Skills: []string{"x", "a", "y"}}); err == nil || err.Error() != "no skills named x, y in src" {
		t.Errorf("unknown: %v", err)
	}
}

// failOn makes the nth install fail, after running before.
func failOn(t *testing.T, n int, before func()) {
	t.Helper()
	calls := 0
	copyInstall = func(backups *[]string, root string, paths []string, files []File, expected string) error {
		calls++
		if calls == n {
			if before != nil {
				before()
			}
			return errors.New("disk full")
		}
		return installInto(backups, root, paths, files, expected)
	}
	t.Cleanup(func() { copyInstall = installInto })
}

func rollbackProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, name := range []string{"a", "b", "c"} {
		writeRel(t, root, "src/"+name+"/SKILL.md", skillMD(name, "new "+name))
	}
	writeRel(t, root, "dst/a/SKILL.md", skillMD("a", "old a"))
	return root
}

// When an install fails, the copies made before it are put back
func TestCopyRollsBackAFailedBatch(t *testing.T) {
	root := rollbackProject(t)
	failOn(t, 3, nil)
	res, err := Copy(CopyOptions{Root: root, From: "src", To: []string{"dst"}})
	var ce *CopyError
	if !errors.As(err, &ce) || len(ce.Replaced) != 0 ||
		err.Error() != "copying src/c to dst/c failed: disk full. fw put back the 2 copies it had already made, so nothing changed" {
		t.Fatalf("%v", err)
	}
	if !strings.Contains(readRel(t, root, "dst/a/SKILL.md"), "old a") || strings.Join(entriesIn(t, root, "dst"), ",") != "a" {
		t.Errorf("not rolled back: %v", entriesIn(t, root, "dst"))
	}
	if entriesIn(t, root, ".framework/skills-backups") != nil || len(res.Backups) != 0 {
		t.Errorf("backups left: %v %v", entriesIn(t, root, ".framework/skills-backups"), res.Backups)
	}
}

// What can't be put back is listed with its backup
func TestCopyReportsWhatItCannotPutBack(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a read-only folder")
	}
	root := rollbackProject(t)
	dst := filepath.Join(root, "dst")
	failOn(t, 3, func() { check(t, os.Chmod(dst, 0o555)) })
	t.Cleanup(func() { _ = os.Chmod(dst, 0o755) })
	res, err := Copy(CopyOptions{Root: root, From: "src", To: []string{"dst"}})
	var ce *CopyError
	if !errors.As(err, &ce) || len(ce.Replaced) != 2 || ce.Replaced[0].To != "dst/a" || ce.Replaced[0].Backup == "" || ce.Replaced[1].To != "dst/b" || ce.Replaced[1].Backup != "" {
		t.Fatalf("%+v %v", ce, err)
	}
	backup := ce.Replaced[0].Backup
	for _, want := range []string{"copying src/c to dst/c failed: disk full", "These copies were replaced and could not be put back:",
		"  dst/a (the old copy is in " + backup + ")", "  dst/b (new; there was no old copy)"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in:\n%v", want, err)
		}
	}
	if !strings.Contains(readRel(t, root, backup+"/SKILL.md"), "old a") || len(res.Backups) != 1 || res.Backups[0] != backup {
		t.Errorf("backup: %v", res.Backups)
	}
}

// A failure after the stage folder exists removes it
func TestInstallRemovesTheStageOnFailure(t *testing.T) {
	root := t.TempDir()
	writeRel(t, root, "src/x/SKILL.md", skillMD("x", "new"))
	writeRel(t, root, "dst/x/SKILL.md", skillMD("x", "old"))
	// private() can't write .framework/.gitignore when it's a folder
	check(t, os.MkdirAll(filepath.Join(root, ".framework", ".gitignore"), 0755))
	if _, err := Copy(CopyOptions{Root: root, From: "src", To: []string{"dst"}}); err == nil {
		t.Fatal("the copy succeeded")
	}
	if got := strings.Join(entriesIn(t, root, "dst"), ","); got != "x" {
		t.Errorf("left in dst: %s", got)
	}
	if !strings.Contains(readRel(t, root, "dst/x/SKILL.md"), "old") {
		t.Error("the target changed")
	}
}

// .framework-skill.json, where file modes can't mark executables: always a
// list, and a single string from older writers still reads.
func TestWindowsExecutableMetadata(t *testing.T) {
	windowsModes = true
	t.Cleanup(func() { windowsModes = runtime.GOOS == "windows" })
	root := t.TempDir()
	writeRel(t, root, "src/x/SKILL.md", skillMD("x", "x"))
	writeRel(t, root, "src/x/notes.md", "notes\n")
	writeRel(t, root, "src/x/scripts/run.sh", "#!/bin/sh\n")
	writeRel(t, root, "src/x/.framework-skill.json", `{"executable_paths":"scripts/run.sh"}`)

	files, err := Scan(filepath.Join(root, "src", "x"))
	if err != nil {
		t.Fatal(err)
	}
	var executable []string
	for _, f := range files {
		if f.Executable {
			executable = append(executable, f.Path)
		}
	}
	if strings.Join(executable, ",") != "scripts/run.sh" {
		t.Errorf("legacy string: %v", executable)
	}

	if _, err = Copy(CopyOptions{Root: root, From: "src", To: []string{"dst"}}); err != nil {
		t.Fatal(err)
	}
	if got := readRel(t, root, "dst/x/.framework-skill.json"); got != `{"executable_paths":["scripts/run.sh"]}` {
		t.Errorf("written: %s", got)
	}
	a, _ := Scan(filepath.Join(root, "src", "x"))
	b, _ := Scan(filepath.Join(root, "dst", "x"))
	if ha, _ := Hash(a); ha == "" || ha != func() string { h, _ := Hash(b); return h }() {
		t.Error("the copy's executable files differ from the source's")
	}
	if _, err = readExecutablePaths([]byte(`{"executable_paths":3}`)); err == nil {
		t.Error("a number was accepted")
	}
}

// Without --to, the targets are the other assistant skills folders that
// exist, sorted; the source is checked first, and lookups that fail stop
// the copy.
func TestCopyDefaultTargets(t *testing.T) {
	root := t.TempDir()
	writeRel(t, root, "vendor/skills/x/SKILL.md", skillMD("x", "x"))
	if _, err := Copy(CopyOptions{Root: root, From: "missing/skills", DryRun: true}); err == nil || err.Error() != "the source folder missing/skills does not exist" {
		t.Errorf("missing source: %v", err)
	}
	if _, err := Copy(CopyOptions{Root: root, From: "vendor/skills", DryRun: true}); err == nil || !strings.Contains(err.Error(), "no other assistant skills folder") {
		t.Errorf("no target: %v", err)
	}
	check(t, os.MkdirAll(filepath.Join(root, ".claude", "skills"), 0755))
	check(t, os.MkdirAll(filepath.Join(root, ".agents", "skills"), 0755))
	res, err := Copy(CopyOptions{Root: root, From: "vendor/skills", DryRun: true})
	if err != nil || strings.Join(res.To, ",") != ".agents/skills,.claude/skills" {
		t.Errorf("defaults: %v %v", res.To, err)
	}
	res, err = Copy(CopyOptions{Root: root, From: ".claude/skills", DryRun: true})
	if err != nil || strings.Join(res.To, ",") != ".agents/skills" {
		t.Errorf("defaults from .claude: %v %v", res.To, err)
	}

	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		return
	}
	// .agents can't be looked into: an error, and .claude is left alone
	check(t, os.Chmod(filepath.Join(root, ".agents"), 0))
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(root, ".agents"), 0755) })
	if _, err = Copy(CopyOptions{Root: root, From: "vendor/skills"}); err == nil || !strings.Contains(err.Error(), "look up .agents/skills") {
		t.Errorf("untraversable .agents: %v", err)
	}
	if entriesIn(t, root, ".claude/skills") != nil {
		t.Error(".claude/skills was changed")
	}
}

// A target that exists but isn't a folder fails before anything is
// written, in a dry run too
func TestCopyTargetMustBeAFolder(t *testing.T) {
	root := t.TempDir()
	writeRel(t, root, "src/x/SKILL.md", skillMD("x", "x"))
	writeRel(t, root, "dst", "a file\n")
	for _, dry := range []bool{true, false} {
		if _, err := Copy(CopyOptions{Root: root, From: "src", To: []string{"dst"}, DryRun: dry}); err == nil || err.Error() != "the target dst is not a folder" {
			t.Errorf("dry run %v: %v", dry, err)
		}
	}
	if _, err := Copy(CopyOptions{Root: root, From: "src", To: []string{"dst/skills"}, DryRun: true}); err == nil || !strings.Contains(err.Error(), "dst") {
		t.Errorf("under a file: %v", err)
	}
	if got := readRel(t, root, "dst"); got != "a file\n" {
		t.Errorf("dst changed: %q", got)
	}
}

// A symlink that leads nowhere is an error, in the source and the target
func TestCopyRefusesDanglingSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	for _, dir := range []string{"src", "dst"} {
		root := t.TempDir()
		writeRel(t, root, "src/x/SKILL.md", skillMD("x", "x"))
		check(t, os.MkdirAll(filepath.Join(root, "dst"), 0755))
		check(t, os.Symlink(filepath.Join(root, "gone"), filepath.Join(root, dir, "broken")))
		if _, err := Copy(CopyOptions{Root: root, From: "src", To: []string{"dst"}, DryRun: true}); err == nil || !strings.Contains(err.Error(), dir+"/broken is a symlink that leads nowhere") {
			t.Errorf("%s: %v", dir, err)
		}
	}
}

// On a case-insensitive volume, a target's analysis is the source's
// Analysis: one row, never a copy and a missing row for one folder
func TestCopyMatchesTargetNamesByIdentity(t *testing.T) {
	root := t.TempDir()
	writeRel(t, root, "src/Analysis/SKILL.md", skillMD("Analysis", "new"))
	if _, err := os.Stat(filepath.Join(root, "SRC")); err != nil {
		t.Skip("this volume is case-sensitive")
	}
	writeRel(t, root, "dst/analysis/SKILL.md", skillMD("Analysis", "old"))
	res, err := Copy(CopyOptions{Root: root, From: "src", To: []string{"dst"}})
	if err != nil || len(res.Skills) != 1 || res.Skills[0].Status != StatusCopied || res.Counts[StatusMissing] != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	if !strings.Contains(readRel(t, root, "dst/analysis/SKILL.md"), "new") {
		t.Error("not copied")
	}
}

// Rebuilt folders keep their modes, such as a private .env.local/ that a
// replaced copy kept
func TestCopyKeepsFolderModes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no Unix modes")
	}
	root := t.TempDir()
	writeRel(t, root, "src/x/SKILL.md", skillMD("x", "new"))
	writeRel(t, root, "src/x/ref/notes.md", "notes\n")
	writeRel(t, root, "dst/x/SKILL.md", skillMD("x", "old"))
	writeRel(t, root, "dst/x/.env.local/secret", "s\n")
	writeRel(t, root, "dst/x/ref/old.md", "old\n")
	check(t, os.Chmod(filepath.Join(root, "dst/x/.env.local"), 0700))
	check(t, os.Chmod(filepath.Join(root, "dst/x/ref"), 0750))
	check(t, os.Chmod(filepath.Join(root, "dst/x"), 0750))
	if _, err := Copy(CopyOptions{Root: root, From: "src", To: []string{"dst"}}); err != nil {
		t.Fatal(err)
	}
	for rel, want := range map[string]os.FileMode{"dst/x": 0750, "dst/x/.env.local": 0700, "dst/x/ref": 0750} {
		info, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil || info.Mode().Perm() != want {
			t.Errorf("%s: %v %v, want %v", rel, info.Mode().Perm(), err, want)
		}
	}
	if readRel(t, root, "dst/x/.env.local/secret") != "s\n" || !strings.Contains(readRel(t, root, "dst/x/ref/notes.md"), "notes") {
		t.Error("contents")
	}
	// A new skill folder is 0755, not the stage's 0700
	writeRel(t, root, "src/y/SKILL.md", skillMD("y", "y"))
	if _, err := Copy(CopyOptions{Root: root, From: "src", To: []string{"dst"}, Skills: []string{"y"}}); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(filepath.Join(root, "dst", "y")); info.Mode().Perm() != 0755 {
		t.Errorf("new skill folder: %v", info.Mode().Perm())
	}
}

// When the installer moved the old copy aside, failed, and couldn't put it
// back, the error names the skill, the target, and the backup
func TestCopyNamesALostCopysBackup(t *testing.T) {
	root := t.TempDir()
	writeRel(t, root, "src/x/SKILL.md", skillMD("x", "new"))
	writeRel(t, root, "dst/x/SKILL.md", skillMD("x", "old"))
	calls := 0
	rename = func(from, to string) error {
		calls++
		if calls >= 2 { // the stage into place, then the backup back
			return errors.New("device busy")
		}
		return os.Rename(from, to)
	}
	t.Cleanup(func() { rename = os.Rename })
	_, err := Copy(CopyOptions{Root: root, From: "src", To: []string{"dst"}})
	var ce *CopyError
	if !errors.As(err, &ce) || len(ce.Replaced) != 1 || ce.Replaced[0].To != "dst/x" || ce.Replaced[0].Backup == "" {
		t.Fatalf("%+v %v", ce, err)
	}
	backup := ce.Replaced[0].Backup
	for _, want := range []string{"copying src/x to dst/x failed: device busy", "could not put dst/x back; the old copy is in " + backup,
		"  dst/x (the old copy is in " + backup + ")"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in:\n%v", want, err)
		}
	}
	if !strings.Contains(readRel(t, root, backup+"/SKILL.md"), "old") {
		t.Error("the backup lost the old copy")
	}
}

// With one recorded copy missing, the hint copies from the one that exists
// and names the missing side, so the copy recreates it
func TestMissingCopyHint(t *testing.T) {
	root := t.TempDir()
	writeRel(t, root, ".agents/skills/x/SKILL.md", skillMD("x", "x"))
	writeRel(t, root, ".framework/skills.lock.json", `{"schema":1,"skills":{"k":{"paths":[".claude/skills/x",".agents/skills/x"],"variant":"stata"}}}`+"\n")
	_, err := Sync(context.Background(), cloud.NewClient("http://127.0.0.1:1", "token"), Options{Root: root, Path: ".claude/skills/x", Direction: "status", Variant: "stata"})
	cmd := `fw skills copy --from agents --to claude --skill "x"`
	want := "assistant skill copies diverged: .claude/skills/x is missing, and .agents/skills/x is the only copy. Copy it back: " + cmd
	var se *SkillError
	if err == nil || err.Error() != want || !errors.As(err, &se) || se.Hint != cmd {
		t.Errorf("got  %v\nwant %s", err, want)
	}
	p := []string{".claude/skills/x"}
	n := copiesNote("", nil, &Entry{Paths: []string{p[0], "vendor/skills/x"}}, p, []string{"vendor/skills/x"}, map[string]string{p[0]: "a"})
	cmd = `fw skills copy --from-dir ".claude/skills" --to-dir "vendor/skills" --skill "x"`
	if n.reason != "vendor/skills/x is missing, and .claude/skills/x is the only copy. Copy it back: "+cmd || n.hint != cmd {
		t.Errorf("custom: %+v", n)
	}
}

// An edit to any recorded copy is a local change, not only the first
func TestLocalChangesChecksEveryCopy(t *testing.T) {
	root := t.TempDir()
	writeRel(t, root, ".claude/skills/x/SKILL.md", skillMD("x", "x"))
	writeRel(t, root, ".agents/skills/x/SKILL.md", skillMD("x", "x"))
	files, err := Scan(filepath.Join(root, ".claude", "skills", "x"))
	check(t, err)
	h, _ := Hash(files)
	writeRel(t, root, ".framework/skills.lock.json", `{"schema":1,"skills":{"k":{"paths":[".claude/skills/x",".agents/skills/x"],"variant":"stata","hash":"`+h+`"}}}`+"\n")
	if changes, _, err := LocalChanges(root, nil); err != nil || len(changes) != 0 {
		t.Fatalf("unedited: %+v %v", changes, err)
	}
	writeRel(t, root, ".agents/skills/x/SKILL.md", skillMD("x", "edited in agents"))
	changes, _, err := LocalChanges(root, nil)
	if err != nil || len(changes) != 1 || changes[0].Path != ".agents/skills/x" {
		t.Errorf("second copy edited: %+v %v", changes, err)
	}
}

// A skills folder pull can't list stops it before it installs anything or
// saves the lock
func TestPullListsBeforeWriting(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs an unreadable folder")
	}
	root := t.TempDir()
	check(t, os.MkdirAll(filepath.Join(root, ".claude", "skills"), 0755))
	check(t, os.MkdirAll(filepath.Join(root, ".agents", "skills"), 0755))
	check(t, os.Chmod(filepath.Join(root, ".agents", "skills"), 0o300))
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(root, ".agents", "skills"), 0755) })
	w := Wanted{Name: "x", Package: true, Files: BundleFromText(skillMD("x", "x"), nil)}
	_, err := Pull(PullOptions{Root: root, Dirs: []string{".claude/skills", ".agents/skills"}, Skills: []Wanted{w}, Variant: "stata"})
	if err == nil || !strings.Contains(err.Error(), "read .agents/skills") {
		t.Fatalf("got %v", err)
	}
	if entriesIn(t, root, ".claude/skills") != nil {
		t.Error("pull installed a skill before failing")
	}
	if _, err := os.Stat(filepath.Join(root, ".framework", "skills.lock.json")); !os.IsNotExist(err) {
		t.Error("pull saved the lock before failing")
	}
}

// Two unedited copies of a built-in skill both move from v1 to v2: the
// first install mustn't make the second look edited
func TestPullUpdatesEveryUneditedCopy(t *testing.T) {
	root := t.TempDir()
	dirs := []string{".claude/skills", ".agents/skills"}
	v1 := Wanted{Name: "framework-stata", Package: true, Files: BundleFromText(skillMD("framework-stata", "v1"), nil)}
	v2 := Wanted{Name: "framework-stata", Package: true, Files: BundleFromText(skillMD("framework-stata", "v2"), nil)}
	if res, err := Pull(PullOptions{Root: root, Dirs: dirs, Skills: []Wanted{v1}, Variant: "stata"}); err != nil || res.Counts[StatusAdded] != 1 {
		t.Fatalf("v1: %+v %v", res, err)
	}
	for _, dry := range []bool{true, false} {
		res, err := Pull(PullOptions{Root: root, Dirs: dirs, Skills: []Wanted{v2}, Variant: "stata", DryRun: dry})
		if err != nil || res.Counts[StatusUpdated] != 1 || res.Counts[StatusEdited] != 0 {
			t.Errorf("v2 (dry run %v): %+v %v", dry, res, err)
		}
	}
	for _, dir := range dirs {
		if !strings.Contains(readRel(t, root, dir+"/framework-stata/SKILL.md"), "v2") {
			t.Errorf("%s was not updated", dir)
		}
	}
	h, _ := Hash(v2.Files)
	if lock := readRel(t, root, ".framework/skills.lock.json"); !strings.Contains(lock, `"hash": "`+h+`"`) {
		t.Errorf("lock:\n%s", lock)
	}
}

// A copy edited after the plan, before its install, is kept and reported
// as edited
func TestPullKeepsACopyEditedDuringThePull(t *testing.T) {
	root := t.TempDir()
	dirs := []string{".claude/skills", ".agents/skills"}
	v1 := Wanted{Name: "x", Package: true, Files: BundleFromText(skillMD("x", "v1"), nil)}
	v2 := Wanted{Name: "x", Package: true, Files: BundleFromText(skillMD("x", "v2"), nil)}
	if _, err := Pull(PullOptions{Root: root, Dirs: dirs, Skills: []Wanted{v1}, Variant: "stata"}); err != nil {
		t.Fatal(err)
	}
	edited := false
	pullInstall = func(backups *[]string, root string, paths []string, files []File, expected string) error {
		if !edited {
			edited = true
			writeRel(t, root, ".agents/skills/x/SKILL.md", skillMD("x", "mine"))
		}
		return installInto(backups, root, paths, files, expected)
	}
	t.Cleanup(func() { pullInstall = installInto })
	res, err := Pull(PullOptions{Root: root, Dirs: dirs, Skills: []Wanted{v2}, Variant: "stata"})
	if err != nil || res.Skills[0].Status != StatusEdited {
		t.Fatalf("%+v %v", res, err)
	}
	if !strings.Contains(readRel(t, root, ".claude/skills/x/SKILL.md"), "v2") || !strings.Contains(readRel(t, root, ".agents/skills/x/SKILL.md"), "mine") {
		t.Error("the edit was overwritten, or .claude wasn't updated")
	}
}

// An assistant skills folder that is a broken symlink or a file is an
// error, not a target left out; so is one inside the source
func TestCopyDefaultTargetsMustBeFolders(t *testing.T) {
	for name, setup := range map[string]func(root string){
		"dangling symlink": func(root string) {
			check(t, os.MkdirAll(filepath.Join(root, ".agents"), 0755))
			check(t, os.Symlink(filepath.Join(root, "gone"), filepath.Join(root, ".agents", "skills")))
		},
		"file": func(root string) { writeRel(t, root, ".agents/skills", "a file\n") },
	} {
		if name == "dangling symlink" && runtime.GOOS == "windows" {
			continue
		}
		root := t.TempDir()
		writeRel(t, root, "src/x/SKILL.md", skillMD("x", "x"))
		check(t, os.MkdirAll(filepath.Join(root, ".claude", "skills"), 0755))
		setup(root)
		for _, dry := range []bool{true, false} {
			if _, err := Copy(CopyOptions{Root: root, From: "src", DryRun: dry}); err == nil || !strings.Contains(err.Error(), ".agents/skills is") {
				t.Errorf("%s (dry run %v): %v", name, dry, err)
			}
		}
		if entriesIn(t, root, ".claude/skills") != nil {
			t.Errorf("%s: .claude/skills was written", name)
		}
	}
	root := t.TempDir()
	writeRel(t, root, ".agents/x/SKILL.md", skillMD("x", "x"))
	check(t, os.MkdirAll(filepath.Join(root, ".agents", "skills"), 0755))
	check(t, os.MkdirAll(filepath.Join(root, ".claude", "skills"), 0755))
	if _, err := Copy(CopyOptions{Root: root, From: ".agents"}); err == nil || err.Error() != ".agents and .agents/skills are inside each other; pick folders side by side" {
		t.Errorf("nested default: %v", err)
	}
	if entriesIn(t, root, ".claude/skills") != nil {
		t.Error("nested default: .claude/skills was written")
	}
}

// A companion folder the new copy replaces with a file gets the file's
// mode, not the folder's
func TestCopyReplacesAFolderWithAFile(t *testing.T) {
	root := t.TempDir()
	writeRel(t, root, "src/x/SKILL.md", skillMD("x", "new"))
	writeRel(t, root, "src/x/ref", "now a file\n")
	writeRel(t, root, "dst/x/SKILL.md", skillMD("x", "old"))
	writeRel(t, root, "dst/x/ref/notes.md", "in a folder\n")
	if _, err := Copy(CopyOptions{Root: root, From: "src", To: []string{"dst"}}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(root, "dst", "x", "ref"))
	if err != nil || info.IsDir() || readRel(t, root, "dst/x/ref") != "now a file\n" {
		t.Fatalf("ref: %v %v", info, err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0644 {
		t.Errorf("ref mode: %v", info.Mode().Perm())
	}
}

// Rebuilt folders keep setgid and sticky bits
func TestCopyKeepsSpecialFolderBits(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no Unix modes")
	}
	root := t.TempDir()
	writeRel(t, root, "src/x/SKILL.md", skillMD("x", "new"))
	writeRel(t, root, "dst/x/SKILL.md", skillMD("x", "old"))
	writeRel(t, root, "dst/x/.env.shared/keep", "k\n")
	shared := filepath.Join(root, "dst", "x", ".env.shared")
	want := os.FileMode(0o775) | os.ModeSticky | os.ModeSetgid
	check(t, os.Chmod(shared, want))
	if info, _ := os.Stat(shared); info.Mode()&dirModeBits != want {
		// The system dropped a bit (setgid for a group the user isn't in)
		want = info.Mode() & dirModeBits
		if want&(os.ModeSticky|os.ModeSetgid) == 0 {
			t.Skip("this system won't set sticky or setgid bits here")
		}
	}
	if _, err := Copy(CopyOptions{Root: root, From: "src", To: []string{"dst"}}); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(shared); err != nil || info.Mode()&dirModeBits != want {
		t.Errorf("mode: %v %v, want %v", info.Mode()&dirModeBits, err, want)
	}
}

// cloudSkill is a Cloud skill at a version, for pull tests.
func cloudSkill(t *testing.T, version int, body string) (Wanted, string) {
	t.Helper()
	files := BundleFromText(skillMD("x", body), nil)
	h, err := Hash(files)
	check(t, err)
	return Wanted{Name: "x", Files: files, Known: []string{h},
		Sync: &Source{ID: "acct-1", Version: version, Hash: h, Variant: "stata", Server: "https://cloud.test"}}, h
}

// Rule P in pull: copies that differ, or a missing copy, make the skill
// diverged. Pull writes no copy, leaves the lock alone, and gives the hint.
func TestPullReportsDivergedCopies(t *testing.T) {
	dirs := []string{".claude/skills", ".agents/skills"}
	v1, _ := cloudSkill(t, 1, "v1")
	_, h2 := cloudSkill(t, 2, "v2")
	setup := func(t *testing.T) (string, string) {
		root := t.TempDir()
		writeRel(t, root, ".claude/skills/x/SKILL.md", skillMD("x", "v2"))
		writeRel(t, root, ".agents/skills/x/SKILL.md", skillMD("x", "v1"))
		lock := `{"schema":1,"skills":{"account:https://cloud.test:acct-1:stata":{"paths":[".claude/skills/x",".agents/skills/x"],"variant":"stata",` +
			`"server":"https://cloud.test","skill_id":"acct-1","version":2,"hash":"` + h2 + `"}}}` + "\n"
		writeRel(t, root, ".framework/skills.lock.json", lock)
		return root, lock
	}

	// v2 in .claude, .agents put back to v1, the setup naming v1
	root, lock := setup(t)
	for _, dry := range []bool{true, false} {
		res, err := Pull(PullOptions{Root: root, Dirs: dirs, Skills: []Wanted{v1}, Variant: "stata", DryRun: dry})
		if err != nil || res.Skills[0].Status != StatusDiverged || res.Counts[StatusCurrent] != 0 ||
			res.Skills[0].Hint != `fw skills copy --from claude --to agents --skill "x"` {
			t.Fatalf("dry run %v: %+v %v", dry, res, err)
		}
	}
	if readRel(t, root, ".framework/skills.lock.json") != lock || !strings.Contains(readRel(t, root, ".claude/skills/x/SKILL.md"), "v2") ||
		!strings.Contains(readRel(t, root, ".agents/skills/x/SKILL.md"), "v1") {
		t.Error("a diverged pull wrote")
	}

	// A missing copy: the hint copies from the copy that exists
	root, lock = setup(t)
	check(t, os.RemoveAll(filepath.Join(root, ".agents", "skills", "x")))
	res, err := Pull(PullOptions{Root: root, Dirs: dirs, Skills: []Wanted{v1}, Variant: "stata"})
	if err != nil || res.Skills[0].Status != StatusDiverged || !strings.Contains(res.Skills[0].Hint, `fw skills copy --from claude --to agents --skill "x"`) {
		t.Fatalf("missing: %+v %v", res, err)
	}
	if readRel(t, root, ".framework/skills.lock.json") != lock || entriesIn(t, root, ".agents/skills") != nil {
		t.Error("a pull with a missing copy wrote")
	}
}

// Pull never moves a lock entry to an older version
func TestPullNeverDowngradesTheLock(t *testing.T) {
	root := t.TempDir()
	v1, _ := cloudSkill(t, 1, "v1")
	_, h2 := cloudSkill(t, 2, "v2")
	writeRel(t, root, ".claude/skills/x/SKILL.md", skillMD("x", "v1"))
	lock := `{"schema":1,"skills":{"account:https://cloud.test:acct-1:stata":{"paths":[".claude/skills/x"],"variant":"stata",` +
		`"server":"https://cloud.test","skill_id":"acct-1","version":2,"hash":"` + h2 + `"}}}` + "\n"
	writeRel(t, root, ".framework/skills.lock.json", lock)
	if _, err := Pull(PullOptions{Root: root, Dirs: []string{".claude/skills"}, Skills: []Wanted{v1}, Variant: "stata"}); err != nil {
		t.Fatal(err)
	}
	if readRel(t, root, ".framework/skills.lock.json") != lock {
		t.Errorf("the lock moved to v1:\n%s", readRel(t, root, ".framework/skills.lock.json"))
	}
}

// Rule Q: a new assistant folder next to your own copy gets the copy hint,
// not the packaged copy, and the lock keeps the skill's Cloud identity
func TestPullDoesNotFillANewFolderOverYourCopy(t *testing.T) {
	dirs := []string{".claude/skills", ".agents/skills"}
	shipped := Wanted{Name: "framework-stata", Package: true, Files: BundleFromText(skillMD("framework-stata", "shipped"), nil)}
	shippedHash, _ := Hash(shipped.Files)
	mine := BundleFromText(skillMD("framework-stata", "mine"), nil)
	mineHash, _ := Hash(mine)
	overwrite := map[string]bool{"own skill": true}
	for name, entry := range map[string]string{
		// Pushed as your own skill
		"own skill": `"skill_id":"own-1","version":1,"server":"https://cloud.test","hash":"` + mineHash + `","origin":{"skill_id":"framework:framework-stata","hash":"` + shippedHash + `"}`,
		// Still the built-in, but edited
		"edited": `"origin":{"skill_id":"framework:framework-stata","hash":"` + shippedHash + `"},"hash":"` + shippedHash + `"`,
	} {
		root := t.TempDir()
		writeRel(t, root, ".claude/skills/framework-stata/SKILL.md", skillMD("framework-stata", "mine"))
		check(t, os.MkdirAll(filepath.Join(root, ".agents", "skills"), 0755))
		lock := `{"schema":1,"skills":{"package:framework-stata":{"paths":[".claude/skills/framework-stata"],"variant":"stata",` + entry + `}}}` + "\n"
		writeRel(t, root, ".framework/skills.lock.json", lock)
		res, err := Pull(PullOptions{Root: root, Dirs: dirs, Skills: []Wanted{shipped}, Variant: "stata", Overwrite: overwrite[name]})
		if err != nil || res.Skills[0].Status != StatusEdited ||
			!strings.Contains(res.Skills[0].Hint, `fw skills copy --from claude --to agents --skill "framework-stata"`) {
			t.Fatalf("%s: %+v %v", name, res, err)
		}
		if entriesIn(t, root, ".agents/skills") != nil {
			t.Errorf("%s: the packaged copy went into .agents", name)
		}
		if name == "own skill" && readRel(t, root, ".framework/skills.lock.json") != lock {
			t.Errorf("%s: the lock changed:\n%s", name, readRel(t, root, ".framework/skills.lock.json"))
		}
	}

	// With --overwrite an edited built-in is replaced, so the new folder gets
	// the shipped copy too
	root := t.TempDir()
	writeRel(t, root, ".claude/skills/framework-stata/SKILL.md", skillMD("framework-stata", "mine"))
	check(t, os.MkdirAll(filepath.Join(root, ".agents", "skills"), 0755))
	writeRel(t, root, ".framework/skills.lock.json", `{"schema":1,"skills":{"package:framework-stata":{"paths":[".claude/skills/framework-stata"],"variant":"stata",`+
		`"origin":{"skill_id":"framework:framework-stata","hash":"`+shippedHash+`"},"hash":"`+shippedHash+`"}}}`+"\n")
	res, err := Pull(PullOptions{Root: root, Dirs: dirs, Skills: []Wanted{shipped}, Variant: "stata", Overwrite: true})
	if err != nil || res.Skills[0].Hint != "" || !strings.Contains(readRel(t, root, ".agents/skills/framework-stata/SKILL.md"), "shipped") ||
		!strings.Contains(readRel(t, root, ".claude/skills/framework-stata/SKILL.md"), "shipped") {
		t.Errorf("overwrite: %+v %v", res, err)
	}

	// An unedited built-in fills the new folder as before
	root = t.TempDir()
	if _, err := Pull(PullOptions{Root: root, Dirs: dirs[:1], Skills: []Wanted{shipped}, Variant: "stata"}); err != nil {
		t.Fatal(err)
	}
	check(t, os.MkdirAll(filepath.Join(root, ".agents", "skills"), 0755))
	res, err = Pull(PullOptions{Root: root, Dirs: dirs, Skills: []Wanted{shipped}, Variant: "stata"})
	if err != nil || res.Skills[0].Status != StatusAdded || res.Skills[0].Hint != "" ||
		!strings.Contains(readRel(t, root, ".agents/skills/framework-stata/SKILL.md"), "shipped") {
		t.Errorf("unedited: %+v %v", res, err)
	}
}

// Rule P in push: a missing tracked copy is a change, so push reports it;
// a copy fw can't read is listed rather than failing every skill
func TestLocalChangesMissingAndUnreadableCopies(t *testing.T) {
	root := t.TempDir()
	writeRel(t, root, ".claude/skills/x/SKILL.md", skillMD("x", "x"))
	files, err := Scan(filepath.Join(root, ".claude", "skills", "x"))
	check(t, err)
	h, _ := Hash(files)
	writeRel(t, root, ".claude/skills/y/SKILL.md", skillMD("y", "y"))
	writeRel(t, root, ".claude/skills/y/my notes.md", "a name sync refuses\n")
	writeRel(t, root, ".framework/skills.lock.json", `{"schema":1,"skills":{`+
		`"kx":{"paths":[".claude/skills/x",".agents/skills/x"],"variant":"stata","hash":"`+h+`"},`+
		`"ky":{"paths":[".claude/skills/y"],"variant":"stata","hash":"`+h+`"}}}`+"\n")
	changes, _, err := LocalChanges(root, nil)
	if err != nil || len(changes) != 2 || changes[0].Path != ".claude/skills/x" || changes[1].Path != ".claude/skills/y" {
		t.Errorf("%+v %v", changes, err)
	}
}

// A --from-dir in .framework is refused too
func TestCopyRefusesASourceInFramework(t *testing.T) {
	root := t.TempDir()
	writeRel(t, root, ".framework/skills-backups/old/x/SKILL.md", skillMD("x", "x"))
	for _, from := range []string{".framework/skills-backups/old", ".framework", ".Framework/skills-backups/old"} {
		if _, err := Copy(CopyOptions{Root: root, From: from, To: []string{"dst"}, DryRun: true}); err == nil || !strings.Contains(err.Error(), "overlaps .framework") {
			t.Errorf("%s: %v", from, err)
		}
	}
}

// lockEntries reads the lock's entries.
func lockEntries(t *testing.T, root string) map[string]*Entry {
	t.Helper()
	state, err := load(root)
	check(t, err)
	return state.Skills
}

// Item 1, as revised in round 12 to match main: --overwrite resets an
// edited copy of your own Cloud skill to that same skill's version, after
// a backup; without it the edit is kept. A new folder gets the copy hint
func TestPullOwnCloudSkillAtTheSameVersion(t *testing.T) {
	dirs := []string{".claude/skills", ".agents/skills"}
	w, h1 := cloudSkill(t, 1, "v1")
	w.Sync.ID = "own-1"
	for _, overwrite := range []bool{false, true} {
		root := t.TempDir()
		writeRel(t, root, ".claude/skills/x/SKILL.md", skillMD("x", "edited"))
		check(t, os.MkdirAll(filepath.Join(root, ".agents", "skills"), 0755))
		writeRel(t, root, ".framework/skills.lock.json", `{"schema":1,"skills":{"account:https://cloud.test:own-1:stata":{"paths":[".claude/skills/x"],"variant":"stata",`+
			`"server":"https://cloud.test","skill_id":"own-1","version":1,"hash":"`+h1+`"}}}`+"\n")
		res, err := Pull(PullOptions{Root: root, Dirs: dirs, Skills: []Wanted{w}, Variant: "stata", Overwrite: overwrite})
		if err != nil || !strings.Contains(res.Skills[0].Hint, `fw skills copy --from claude --to agents --skill "x"`) || entriesIn(t, root, ".agents/skills") != nil {
			t.Fatalf("overwrite %v: %+v %v", overwrite, res, err)
		}
		want := "edited"
		if overwrite {
			want = "v1"
			if len(res.Backups) != 1 || !strings.Contains(readRel(t, root, res.Backups[0]+"/SKILL.md"), "edited") {
				t.Errorf("no backup of the edit: %v", res.Backups)
			}
		}
		if !strings.Contains(readRel(t, root, ".claude/skills/x/SKILL.md"), want) {
			t.Errorf("overwrite %v: .claude isn't %s", overwrite, want)
		}
		if e := lockEntries(t, root)["account:https://cloud.test:own-1:stata"]; e == nil || e.ID != "own-1" {
			t.Errorf("overwrite %v: identity: %+v", overwrite, e)
		}
	}
}

// Item 2 and S4: a package-fallback pull leaves a Cloud-tracked skill alone,
// reports it unavailable, and keeps skill_id, version, and server
func TestFallbackPullLeavesCloudSkillsAlone(t *testing.T) {
	shipped := Wanted{Name: "framework-stata", Package: true, Files: BundleFromText(skillMD("framework-stata", "shipped"), nil)}
	for _, body := range []string{"shipped", "cloud v4"} {
		root := t.TempDir()
		writeRel(t, root, ".claude/skills/framework-stata/SKILL.md", skillMD("framework-stata", body))
		files, _ := Scan(filepath.Join(root, ".claude", "skills", "framework-stata"))
		h, _ := Hash(files)
		lock := `{"schema":1,"skills":{"account:https://cloud.test:framework:framework-stata:stata":{"paths":[".claude/skills/framework-stata"],` +
			`"variant":"stata","server":"https://cloud.test","skill_id":"framework:framework-stata","version":4,"hash":"` + h + `"}}}` + "\n"
		writeRel(t, root, ".framework/skills.lock.json", lock)
		res, err := Pull(PullOptions{Root: root, Dirs: []string{".claude/skills"}, Skills: []Wanted{shipped}, Variant: "stata", Fallback: true, Overwrite: true})
		if err != nil || res.Skills[0].Status != StatusUnavailable || res.Skills[0].Reason != "Framework Cloud wasn't reached, so framework-stata was left as it is." {
			t.Fatalf("%s: %+v %v", body, res, err)
		}
		if readRel(t, root, ".framework/skills.lock.json") != lock || !strings.Contains(readRel(t, root, ".claude/skills/framework-stata/SKILL.md"), body) {
			t.Errorf("%s: the fallback pull wrote", body)
		}
	}
}

// S3: the identity guards hold for an inline skill and a packaged one in a
// Cloud pull too
func TestPullKeepsIdentityWhateverTheSource(t *testing.T) {
	_, h2 := cloudSkill(t, 2, "v2")
	for name, w := range map[string]Wanted{
		"inline":  {Name: "x", Files: BundleFromText(skillMD("x", "inline"), nil)},
		"package": {Name: "x", Package: true, Files: BundleFromText(skillMD("x", "packaged"), nil)},
	} {
		root := t.TempDir()
		writeRel(t, root, ".claude/skills/x/SKILL.md", skillMD("x", "v2"))
		lock := `{"schema":1,"skills":{"account:https://cloud.test:acct-1:stata":{"paths":[".claude/skills/x"],"variant":"stata",` +
			`"server":"https://cloud.test","skill_id":"acct-1","version":2,"hash":"` + h2 + `"}}}` + "\n"
		writeRel(t, root, ".framework/skills.lock.json", lock)
		if _, err := Pull(PullOptions{Root: root, Dirs: []string{".claude/skills"}, Skills: []Wanted{w}, Variant: "stata", Overwrite: true}); err != nil {
			t.Fatal(err)
		}
		if readRel(t, root, ".framework/skills.lock.json") != lock || !strings.Contains(readRel(t, root, ".claude/skills/x/SKILL.md"), "v2") {
			t.Errorf("%s: the Cloud skill was replaced", name)
		}
	}
}

// S2: a new folder joins the skill's one lock entry, whatever its key
func TestPullKeepsOneEntryPerSkill(t *testing.T) {
	root := t.TempDir()
	shipped := Wanted{Name: "x", Package: true, Files: BundleFromText(skillMD("x", "shipped"), nil)}
	h, _ := Hash(shipped.Files)
	writeRel(t, root, ".claude/skills/x/SKILL.md", skillMD("x", "shipped"))
	writeRel(t, root, ".framework/skills.lock.json", `{"schema":1,"skills":{"older-key":{"paths":[".claude/skills/x"],"variant":"stata",`+
		`"origin":{"skill_id":"framework:x","hash":"`+h+`"},"hash":"`+h+`"}}}`+"\n")
	check(t, os.MkdirAll(filepath.Join(root, ".agents", "skills"), 0755))
	if _, err := Pull(PullOptions{Root: root, Dirs: []string{".claude/skills", ".agents/skills"}, Skills: []Wanted{shipped}, Variant: "stata"}); err != nil {
		t.Fatal(err)
	}
	entries := lockEntries(t, root)
	if len(entries) != 1 || entries["older-key"] == nil || strings.Join(entries["older-key"].Paths, ",") != ".claude/skills/x,.agents/skills/x" {
		t.Errorf("entries: %v", entries)
	}
}

// Item 3 and S5: with every tracked copy gone, push lists the skill as
// missing and pull puts it back as restored, in one folder or two
func TestAllCopiesGone(t *testing.T) {
	shipped := Wanted{Name: "x", Package: true, Files: BundleFromText(skillMD("x", "shipped"), nil)}
	h, _ := Hash(shipped.Files)
	for _, dirs := range [][]string{{".claude/skills"}, {".claude/skills", ".agents/skills"}} {
		root := t.TempDir()
		paths := `"` + strings.Join(func() []string {
			var p []string
			for _, d := range dirs {
				p = append(p, d+"/x")
			}
			return p
		}(), `","`) + `"`
		for _, d := range dirs {
			check(t, os.MkdirAll(filepath.Join(root, filepath.FromSlash(d)), 0755))
		}
		writeRel(t, root, ".framework/skills.lock.json", `{"schema":1,"skills":{"package:x":{"paths":[`+paths+`],"variant":"stata",`+
			`"origin":{"skill_id":"framework:x","hash":"`+h+`"},"hash":"`+h+`"}}}`+"\n")
		changes, _, err := LocalChanges(root, dirs)
		if err != nil || len(changes) != 1 || !changes[0].Missing {
			t.Errorf("%v push: %+v %v", dirs, changes, err)
		}
		for _, dry := range []bool{true, false} {
			res, err := Pull(PullOptions{Root: root, Dirs: dirs, Skills: []Wanted{shipped}, Variant: "stata", DryRun: dry})
			if err != nil || res.Skills[0].Status != StatusRestored || res.Counts[StatusAdded] != 0 {
				t.Errorf("%v pull (dry run %v): %+v %v", dirs, dry, res, err)
			}
		}
		for _, d := range dirs {
			if !strings.Contains(readRel(t, root, d+"/x/SKILL.md"), "shipped") {
				t.Errorf("%s was not restored", d)
			}
		}
	}
}

// Item 4: a fallback pull reports a diverged tracked skill the package
// skills don't include, with its hint, instead of leaving it out
func TestFallbackPullReportsDivergedSkills(t *testing.T) {
	root := t.TempDir()
	writeRel(t, root, ".claude/skills/analysis/SKILL.md", skillMD("analysis", "a"))
	writeRel(t, root, ".agents/skills/analysis/SKILL.md", skillMD("analysis", "b"))
	writeRel(t, root, ".framework/skills.lock.json", `{"schema":1,"skills":{"account:https://cloud.test:own-2:stata":{"paths":[".claude/skills/analysis",".agents/skills/analysis"],`+
		`"variant":"stata","server":"https://cloud.test","skill_id":"own-2","version":1,"hash":"zz"}}}`+"\n")
	shipped := Wanted{Name: "framework-stata", Package: true, Files: BundleFromText(skillMD("framework-stata", "s"), nil)}
	res, err := Pull(PullOptions{Root: root, Dirs: []string{".claude/skills", ".agents/skills"}, Skills: []Wanted{shipped}, Variant: "stata", Fallback: true, DryRun: true})
	if err != nil || len(res.Skills) != 2 || res.Skills[1].Name != "analysis" || res.Skills[1].Status != StatusDiverged ||
		res.Skills[1].Hint != `fw skills copy --from claude --to agents --skill "analysis"` || res.Counts[StatusRemoved] != 0 {
		t.Errorf("%+v %v", res, err)
	}
}

// Rule J: copying a tracked skill to a custom folder records it in the
// skill's entry, and pull then moves both copies from v1 to v2
func TestCustomFolderCopyMovesWithItsSkill(t *testing.T) {
	root := t.TempDir()
	v1 := Wanted{Name: "x", Package: true, Files: BundleFromText(skillMD("x", "v1"), nil)}
	v2 := Wanted{Name: "x", Package: true, Files: BundleFromText(skillMD("x", "v2"), nil)}
	if _, err := Pull(PullOptions{Root: root, Dirs: []string{".claude/skills"}, Skills: []Wanted{v1}, Variant: "stata"}); err != nil {
		t.Fatal(err)
	}
	lock := readRel(t, root, ".framework/skills.lock.json")
	if _, err := Copy(CopyOptions{Root: root, From: ".claude/skills", To: []string{"vendor/skills"}, DryRun: true}); err != nil {
		t.Fatal(err)
	}
	if readRel(t, root, ".framework/skills.lock.json") != lock {
		t.Error("a dry run changed the lock")
	}
	if _, err := Copy(CopyOptions{Root: root, From: ".claude/skills", To: []string{"vendor/skills"}}); err != nil {
		t.Fatal(err)
	}
	entries := lockEntries(t, root)
	if len(entries) != 1 || strings.Join(entries["package:x"].Paths, ",") != ".claude/skills/x,vendor/skills/x" {
		t.Fatalf("entries: %v", entries)
	}
	res, err := Pull(PullOptions{Root: root, Dirs: []string{".claude/skills"}, Skills: []Wanted{v2}, Variant: "stata"})
	if err != nil || res.Skills[0].Status != StatusUpdated {
		t.Fatalf("%+v %v", res, err)
	}
	for _, p := range []string{".claude/skills/x", "vendor/skills/x"} {
		if !strings.Contains(readRel(t, root, p+"/SKILL.md"), "v2") {
			t.Errorf("%s is not at v2", p)
		}
	}
}

// Rule J: an untracked copy is never joined by its bytes, and an untracked
// source's copy leaves the lock alone
func TestNoJoinByNameOrBytes(t *testing.T) {
	root := t.TempDir()
	v1 := Wanted{Name: "x", Package: true, Files: BundleFromText(skillMD("x", "v1"), nil)}
	if _, err := Pull(PullOptions{Root: root, Dirs: []string{".claude/skills"}, Skills: []Wanted{v1}, Variant: "stata"}); err != nil {
		t.Fatal(err)
	}
	// An identical copy made by hand
	writeRel(t, root, ".agents/skills/x/SKILL.md", skillMD("x", "v1"))
	res, err := Pull(PullOptions{Root: root, Dirs: []string{".claude/skills", ".agents/skills"}, Skills: []Wanted{v1}, Variant: "stata"})
	// The loose copy gets its own untracked row; the tracked one is current
	if err != nil || len(res.Skills) != 2 || res.Skills[0].Status != StatusCurrent || res.Skills[1].Status != StatusUntracked ||
		!strings.HasPrefix(res.Skills[1].Reason, ".agents/skills/x isn't tracked, so pull left it alone.") {
		t.Fatalf("%+v %v", res, err)
	}
	if paths := lockEntries(t, root)["package:x"].Paths; strings.Join(paths, ",") != ".claude/skills/x" {
		t.Errorf("joined by bytes: %v", paths)
	}
	// An untracked source
	writeRel(t, root, "loose/y/SKILL.md", skillMD("y", "y"))
	lock := readRel(t, root, ".framework/skills.lock.json")
	if _, err := Copy(CopyOptions{Root: root, From: "loose", To: []string{"other"}}); err != nil {
		t.Fatal(err)
	}
	if readRel(t, root, ".framework/skills.lock.json") != lock {
		t.Error("copying an untracked skill changed the lock")
	}
}

// Rule J: a standalone skill with the same name keeps its own identity:
// copy won't write over it, and pull leaves it and its entry alone
func TestStandaloneSkillWithTheSameName(t *testing.T) {
	root := t.TempDir()
	w, h1 := cloudSkill(t, 1, "v1")
	w.Sync.ID = "own-1"
	writeRel(t, root, ".claude/skills/x/SKILL.md", skillMD("x", "v1"))
	writeRel(t, root, "vendor/skills/x/SKILL.md", skillMD("x", "someone else's"))
	lock := `{"schema":1,"skills":{` +
		`"account:https://cloud.test:own-1:stata":{"paths":[".claude/skills/x"],"variant":"stata","server":"https://cloud.test","skill_id":"own-1","version":1,"hash":"` + h1 + `"},` +
		`"account:https://cloud.test:own-2:stata":{"paths":["vendor/skills/x"],"variant":"stata","server":"https://cloud.test","skill_id":"own-2","version":3,"hash":"other"}}}` + "\n"
	writeRel(t, root, ".framework/skills.lock.json", lock)
	for _, dry := range []bool{true, false} {
		if _, err := Copy(CopyOptions{Root: root, From: ".claude/skills", To: []string{"vendor/skills"}, DryRun: dry}); err == nil ||
			!strings.Contains(err.Error(), "vendor/skills/x is recorded as a different skill") {
			t.Errorf("copy (dry run %v): %v", dry, err)
		}
	}
	if _, err := Pull(PullOptions{Root: root, Dirs: []string{".claude/skills", "vendor/skills"}, Skills: []Wanted{w}, Variant: "stata"}); err != nil {
		t.Fatal(err)
	}
	if readRel(t, root, ".framework/skills.lock.json") != lock || !strings.Contains(readRel(t, root, "vendor/skills/x/SKILL.md"), "someone else's") {
		t.Error("the standalone skill, or its entry, changed")
	}
}

// Rule J: a renamed copy gets an explanation, never a command that would
// copy the wrong folder
func TestRenamedCopyGetsNoCommand(t *testing.T) {
	root := t.TempDir()
	writeRel(t, root, ".claude/skills/x/SKILL.md", skillMD("x", "a"))
	writeRel(t, root, ".agents/skills/y/SKILL.md", skillMD("x", "b"))
	writeRel(t, root, ".framework/skills.lock.json", `{"schema":1,"skills":{"k":{"paths":[".claude/skills/x",".agents/skills/y"],"variant":"stata"}}}`+"\n")
	client := cloud.NewClient("http://127.0.0.1:1", "token")
	opts := Options{Root: root, Path: ".claude/skills/x", Direction: "status", Variant: "stata"}
	if _, err := Sync(context.Background(), client, opts); err == nil || strings.Contains(err.Error(), "fw skills copy --") ||
		!strings.Contains(err.Error(), "by hand: fw skills copy can't") {
		t.Errorf("differ: %v", err)
	}
	check(t, os.RemoveAll(filepath.Join(root, ".agents", "skills", "y")))
	if _, err := Sync(context.Background(), client, opts); err == nil || strings.Contains(err.Error(), "fw skills copy --") ||
		!strings.Contains(err.Error(), "Copy its files back by hand") {
		t.Errorf("missing: %v", err)
	}
}

// R round 8, 1: two independent same-named skills, one per folder, are
// judged per entry: never diverged, and no hint copies one over the other
func TestSameNamedSkillsAreJudgedPerEntry(t *testing.T) {
	root := t.TempDir()
	w, h1 := cloudSkill(t, 1, "mine")
	w.Sync.ID = "own-1"
	writeRel(t, root, ".claude/skills/x/SKILL.md", skillMD("x", "mine"))
	writeRel(t, root, ".agents/skills/x/SKILL.md", skillMD("x", "theirs"))
	files, _ := Scan(filepath.Join(root, ".agents", "skills", "x"))
	h2, _ := Hash(files)
	lock := `{"schema":1,"skills":{` +
		`"account:https://cloud.test:own-1:stata":{"paths":[".claude/skills/x"],"variant":"stata","server":"https://cloud.test","skill_id":"own-1","version":1,"hash":"` + h1 + `"},` +
		`"account:https://cloud.test:own-2:stata":{"paths":[".agents/skills/x"],"variant":"stata","server":"https://cloud.test","skill_id":"own-2","version":1,"hash":"` + h2 + `"}}}` + "\n"
	writeRel(t, root, ".framework/skills.lock.json", lock)
	dirs := []string{".claude/skills", ".agents/skills"}
	for _, fallback := range []bool{false, true} {
		res, err := Pull(PullOptions{Root: root, Dirs: dirs, Skills: []Wanted{w}, Variant: "stata", Fallback: fallback})
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range res.Skills {
			if s.Status == StatusDiverged || strings.Contains(s.Hint, "fw skills copy --") {
				t.Errorf("fallback %v: %+v", fallback, s)
			}
		}
	}
	if changes, _, err := LocalChanges(root, dirs); err != nil || len(changes) != 0 {
		t.Errorf("push: %+v %v", changes, err)
	}
	if _, err := Sync(context.Background(), cloud.NewClient("http://127.0.0.1:1", "t"), Options{Root: root, Path: ".claude/skills/x", Direction: "status", Variant: "stata"}); err != nil &&
		strings.Contains(err.Error(), "diverged") {
		t.Errorf("sync: %v", err)
	}
	if readRel(t, root, ".framework/skills.lock.json") != lock || !strings.Contains(readRel(t, root, ".agents/skills/x/SKILL.md"), "theirs") {
		t.Error("one skill was written over the other, or the lock changed")
	}
}

// R round 8, 2: fw has no read-time merge; entries for the same skill ID
// in different variants (R and Python) stay separate through a pull
func TestVariantsNeverMerge(t *testing.T) {
	root := t.TempDir()
	w, _ := cloudSkill(t, 1, "r")
	w.Sync.Variant = "r"
	writeRel(t, root, ".claude/skills/x/SKILL.md", skillMD("x", "r"))
	writeRel(t, root, "py/skills/x/SKILL.md", skillMD("x", "python"))
	lock := `{"schema":1,"skills":{` +
		`"account:https://cloud.test:acct-1:python":{"paths":["py/skills/x"],"variant":"python","server":"https://cloud.test","skill_id":"acct-1","version":1,"hash":"p"}}}` + "\n"
	writeRel(t, root, ".framework/skills.lock.json", lock)
	// Only .agents is configured, so the R skill's entry is looked up by
	// identity: the Python entry doesn't match it
	if _, err := Pull(PullOptions{Root: root, Dirs: []string{".agents/skills"}, Skills: []Wanted{w}, Variant: "r"}); err != nil {
		t.Fatal(err)
	}
	entries := lockEntries(t, root)
	py := entries["account:https://cloud.test:acct-1:python"]
	if py == nil || strings.Join(py.Paths, ",") != "py/skills/x" || py.Variant != "python" || !strings.Contains(readRel(t, root, "py/skills/x/SKILL.md"), "python") {
		t.Errorf("the Python entry changed: %+v", entries)
	}
}

// R round 8, 3: with no configured folder tracked (the project switched to
// Codex only), pull still finds the entry by identity and moves every
// tracked copy, custom folders included, to v3
func TestPullMovesEveryTrackedCopyAfterSwitchingAssistants(t *testing.T) {
	root := t.TempDir()
	_, h2 := cloudSkill(t, 2, "v2")
	w3, _ := cloudSkill(t, 3, "v3")
	for _, p := range []string{".claude/skills/x", "vendor/skills/x"} {
		writeRel(t, root, p+"/SKILL.md", skillMD("x", "v2"))
	}
	writeRel(t, root, ".framework/skills.lock.json", `{"schema":1,"skills":{"an-older-key":{"paths":[".claude/skills/x","vendor/skills/x"],`+
		`"variant":"stata","server":"https://cloud.test","skill_id":"acct-1","version":2,"hash":"`+h2+`"}}}`+"\n")
	// .agents, the new folder, gets the copy hint (rule Q); the tracked
	// copies move
	res, err := Pull(PullOptions{Root: root, Dirs: []string{".agents/skills"}, Skills: []Wanted{w3}, Variant: "stata"})
	if err != nil || len(res.Backups) != 2 || !strings.Contains(res.Skills[0].Hint, "--to agents") {
		t.Fatalf("%+v %v", res, err)
	}
	for _, p := range []string{".claude/skills/x", "vendor/skills/x"} {
		if !strings.Contains(readRel(t, root, p+"/SKILL.md"), "v3") {
			t.Errorf("%s is not at v3", p)
		}
	}
	if e := lockEntries(t, root)["an-older-key"]; e == nil || e.Version != 3 {
		t.Errorf("lock: %+v", e)
	}
}

// R round 8, 3: when one tracked copy can't move with the others, the
// skill is diverged and nothing is written for it
func TestPullMovesAllCopiesOrNone(t *testing.T) {
	entry := &Entry{Paths: []string{".claude/skills/x", "vendor/skills/x"}}
	steps := []pullStep{
		{relative: ".claude/skills/x", status: StatusUpdated, record: true},
		{relative: "vendor/skills/x", status: StatusEdited},
		{relative: ".agents/skills/x", status: StatusAdded, record: true},
	}
	n := allOrNothing(t.TempDir(), entry, steps, note{})
	if !strings.Contains(n.reason, "vendor/skills/x can't move") || n.hint != "" {
		t.Errorf("note: %+v", n)
	}
	for _, st := range steps {
		if st.status != StatusDiverged || st.record {
			t.Errorf("%s: %s %v", st.relative, st.status, st.record)
		}
	}
	// All moving together is left alone
	steps = []pullStep{{relative: ".claude/skills/x", status: StatusUpdated}, {relative: "vendor/skills/x", status: StatusUpdated}}
	if allOrNothing(t.TempDir(), entry, steps, note{}).reason != "" || steps[1].status != StatusUpdated {
		t.Error("copies moving together were stopped")
	}
}

// R round 8, 4: a case alias of the source folder finds the lock's entry
// by filesystem identity, so the target still joins it
func TestCopyFromACaseAliasJoinsTheEntry(t *testing.T) {
	root := t.TempDir()
	v1 := Wanted{Name: "x", Package: true, Files: BundleFromText(skillMD("x", "v1"), nil)}
	if _, err := Pull(PullOptions{Root: root, Dirs: []string{".claude/skills"}, Skills: []Wanted{v1}, Variant: "stata"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, ".CLAUDE")); err != nil {
		t.Skip("this volume is case-sensitive")
	}
	if _, err := Copy(CopyOptions{Root: root, From: ".Claude/skills", To: []string{".agents/skills"}}); err != nil {
		t.Fatal(err)
	}
	if paths := lockEntries(t, root)["package:x"].Paths; strings.Join(paths, ",") != ".claude/skills/x,.agents/skills/x" {
		t.Errorf("paths: %v", paths)
	}
	// Copying back to the alias of a recorded copy is that copy, not another skill
	if _, err := Copy(CopyOptions{Root: root, From: ".agents/skills", To: []string{".CLAUDE/skills"}}); err != nil {
		t.Errorf("back to an alias: %v", err)
	}
	if paths := lockEntries(t, root)["package:x"].Paths; len(paths) != 2 {
		t.Errorf("an alias was recorded twice: %v", paths)
	}
}

func caseInsensitive(t *testing.T, root string) {
	t.Helper()
	check(t, os.MkdirAll(filepath.Join(root, "probe-case"), 0755))
	if _, err := os.Stat(filepath.Join(root, "PROBE-CASE")); err != nil {
		t.Skip("this volume is case-sensitive")
	}
	check(t, os.Remove(filepath.Join(root, "probe-case")))
}

// Round 10, 1: a copy to a case alias is recorded, and shown, in the
// filesystem's spelling; push, pull, and sync match paths by identity, so
// the real folder is never called untracked
func TestCaseAliasesAreOnePath(t *testing.T) {
	root := t.TempDir()
	caseInsensitive(t, root)
	v1 := Wanted{Name: "x", Package: true, Files: BundleFromText(skillMD("x", "v1"), nil)}
	if _, err := Pull(PullOptions{Root: root, Dirs: []string{".claude/skills"}, Skills: []Wanted{v1}, Variant: "stata"}); err != nil {
		t.Fatal(err)
	}
	check(t, os.MkdirAll(filepath.Join(root, ".agents", "skills"), 0755))
	res, err := Copy(CopyOptions{Root: root, From: ".CLAUDE/skills", To: []string{".AGENTS/skills"}})
	if err != nil || res.From != ".claude/skills" || strings.Join(res.To, ",") != ".agents/skills" || res.Skills[0].To != ".agents/skills/x" {
		t.Fatalf("%+v %v", res, err)
	}
	if paths := lockEntries(t, root)["package:x"].Paths; strings.Join(paths, ",") != ".claude/skills/x,.agents/skills/x" {
		t.Errorf("recorded: %v", paths)
	}

	// A lock that already has an alias spelling still matches the real folder
	writeRel(t, root, ".framework/skills.lock.json", strings.Replace(readRel(t, root, ".framework/skills.lock.json"), ".agents/skills/x", ".AGENTS/skills/x", 1))
	dirs := []string{".claude/skills", ".agents/skills"}
	if _, untracked, err := LocalChanges(root, dirs); err != nil || len(untracked) != 0 {
		t.Errorf("push calls it untracked: %v %v", untracked, err)
	}
	// Found, sync asks Cloud for the entry's skill (framework:x); an
	// untracked path would ask for nothing and be pushed as a new skill
	_, err = Sync(context.Background(), cloud.NewClient("http://127.0.0.1:1", "t"), Options{Root: root, Path: ".agents/skills/x", Direction: "status", Variant: "stata"})
	if err == nil || !strings.Contains(err.Error(), "skills/sync/framework:x") {
		t.Errorf("sync didn't find the entry: %v", err)
	}
	pulled, err := Pull(PullOptions{Root: root, Dirs: dirs, Skills: []Wanted{v1}, Variant: "stata"})
	if err != nil || pulled.Skills[0].Status != StatusCurrent || pulled.Skills[0].Hint != "" || len(pulled.Skills[0].Paths) != 2 {
		t.Errorf("pull: %+v %v", pulled, err)
	}
}

// Round 10, 2: a path the lock gives another entry is refused even when
// its folder was deleted, by spelling or by a case alias
func TestCopyRefusesADeletedPathOfAnotherEntry(t *testing.T) {
	root := t.TempDir()
	_, h1 := cloudSkill(t, 1, "v1")
	writeRel(t, root, ".claude/skills/analysis/SKILL.md", skillMD("analysis", "v1"))
	check(t, os.MkdirAll(filepath.Join(root, "lab", "skills"), 0755))
	lock := `{"schema":1,"skills":{` +
		`"account:https://cloud.test:acct-1:stata":{"paths":[".claude/skills/analysis"],"variant":"stata","server":"https://cloud.test","skill_id":"acct-1","version":1,"hash":"` + h1 + `"},` +
		`"account:https://cloud.test:other-1:stata":{"paths":["lab/skills/analysis"],"variant":"stata","server":"https://cloud.test","skill_id":"other-1","version":1,"hash":"o"}}}` + "\n"
	writeRel(t, root, ".framework/skills.lock.json", lock)
	targets := []string{"lab/skills"}
	if _, err := os.Stat(filepath.Join(root, "LAB")); err == nil {
		targets = append(targets, "LAB/skills", "Lab/Skills")
	}
	for _, to := range targets {
		for _, dry := range []bool{true, false} {
			if _, err := Copy(CopyOptions{Root: root, From: ".claude/skills", To: []string{to}, DryRun: dry}); err == nil ||
				!strings.Contains(err.Error(), "is recorded as a different skill") {
				t.Errorf("%s (dry run %v): %v", to, dry, err)
			}
		}
	}
	if entriesIn(t, root, "lab/skills") != nil || readRel(t, root, ".framework/skills.lock.json") != lock {
		t.Error("a refused copy wrote")
	}
}

// independentSkills is a setup skill acct-1 in .claude and an independent
// same-named skill other-1 in .agents, with the project now on Codex only.
func independentSkills(t *testing.T) (root string, w Wanted) {
	t.Helper()
	root = t.TempDir()
	_, h1 := cloudSkill(t, 1, "v1")
	w, _ = cloudSkill(t, 2, "v2")
	writeRel(t, root, ".claude/skills/x/SKILL.md", skillMD("x", "v1"))
	writeRel(t, root, ".agents/skills/x/SKILL.md", skillMD("x", "other"))
	files, _ := Scan(filepath.Join(root, ".agents", "skills", "x"))
	ho, _ := Hash(files)
	writeRel(t, root, ".framework/skills.lock.json", `{"schema":1,"skills":{`+
		`"account:https://cloud.test:acct-1:stata":{"paths":[".claude/skills/x"],"variant":"stata","server":"https://cloud.test","skill_id":"acct-1","version":1,"hash":"`+h1+`"},`+
		`"account:https://cloud.test:other-1:stata":{"paths":[".agents/skills/x"],"variant":"stata","server":"https://cloud.test","skill_id":"other-1","version":1,"hash":"`+ho+`"}}}`+"\n")
	return root, w
}

// statusOf finds an entry's row by one of its paths.
func statusOf(res PullResult, p string) string {
	for _, s := range res.Skills {
		if contains(s.Paths, p) {
			return s.Status
		}
	}
	return ""
}

// Round 10, 3: after switching to Codex, pull finds the setup skill by its
// identity and updates its copy in .claude; the independent same-named
// skill in .agents is never planned, written, or judged for it
func TestPullAfterSwitchingUsesTheSetupSkillsEntry(t *testing.T) {
	codex := []string{".agents/skills"}

	// Update the setup skill; the other keeps its copy and its entry
	root, w := independentSkills(t)
	other := lockEntries(t, root)["account:https://cloud.test:other-1:stata"]
	res, err := Pull(PullOptions{Root: root, Dirs: codex, Skills: []Wanted{w}, Variant: "stata"})
	if err != nil || statusOf(res, ".claude/skills/x") != StatusUpdated {
		t.Fatalf("update: %+v %v", res, err)
	}
	entries := lockEntries(t, root)
	if !strings.Contains(readRel(t, root, ".claude/skills/x/SKILL.md"), "v2") || entries["account:https://cloud.test:acct-1:stata"].Version != 2 ||
		!strings.Contains(readRel(t, root, ".agents/skills/x/SKILL.md"), "other") ||
		strings.Join(entries["account:https://cloud.test:other-1:stata"].Paths, ",") != strings.Join(other.Paths, ",") ||
		entries["account:https://cloud.test:other-1:stata"].Hash != other.Hash {
		t.Errorf("update: %+v", entries)
	}

	// Delete the other's only copy: pull doesn't put the setup skill there,
	// and push reports the other as missing, on its own
	root, w = independentSkills(t)
	check(t, os.RemoveAll(filepath.Join(root, ".agents", "skills", "x")))
	// (The vacated path doesn't block the setup skill, round 12, but the
	// copy hint holds it back, as for any new folder next to a Cloud skill)
	res, err = Pull(PullOptions{Root: root, Dirs: codex, Skills: []Wanted{w}, Variant: "stata"})
	if err != nil || !strings.Contains(readRel(t, root, ".claude/skills/x/SKILL.md"), "v2") || entriesIn(t, root, ".agents/skills") != nil ||
		statusOf(res, ".agents/skills/x") == "" {
		t.Errorf("delete one: %+v %v", res, err)
	}
	changes, _, err := LocalChanges(root, codex)
	if err != nil || len(changes) != 1 || !changes[0].Missing || changes[0].Key != "account:https://cloud.test:other-1:stata" {
		t.Errorf("delete one, push: %+v %v", changes, err)
	}

	// Diverge the other (two copies that differ): it's reported diverged,
	// and the setup skill still updates
	root, w = independentSkills(t)
	writeRel(t, root, "vendor/skills/x/SKILL.md", skillMD("x", "other, edited"))
	writeRel(t, root, ".framework/skills.lock.json", strings.Replace(readRel(t, root, ".framework/skills.lock.json"),
		`"paths":[".agents/skills/x"]`, `"paths":[".agents/skills/x","vendor/skills/x"]`, 1))
	res, err = Pull(PullOptions{Root: root, Dirs: codex, Skills: []Wanted{w}, Variant: "stata"})
	if err != nil || statusOf(res, ".claude/skills/x") != StatusUpdated || statusOf(res, ".agents/skills/x") != StatusDiverged {
		t.Errorf("diverge one: %+v %v", res, err)
	}
	if !strings.Contains(readRel(t, root, ".claude/skills/x/SKILL.md"), "v2") || !strings.Contains(readRel(t, root, ".agents/skills/x/SKILL.md"), "other") {
		t.Error("diverge one: wrong copy written")
	}
}

// Python (a): fw skills sync through a case alias finds a lock path that
// is itself written in an alias spelling
func TestSyncMatchesAliasSpelledLockPaths(t *testing.T) {
	root := t.TempDir()
	caseInsensitive(t, root)
	for _, p := range []string{".claude/skills/x", ".agents/skills/x"} {
		writeRel(t, root, p+"/SKILL.md", skillMD("x", "x"))
	}
	writeRel(t, root, ".framework/skills.lock.json", `{"schema":1,"skills":{"k":{"paths":[".claude/skills/x",".AGENTS/skills/x"],"variant":"stata"}}}`+"\n")
	r, err := Sync(context.Background(), cloud.NewClient("http://127.0.0.1:1", "t"), Options{Root: root, Path: ".Agents/Skills/X", Direction: "status", Variant: "stata"})
	if err != nil || len(r.Paths) != 2 {
		t.Errorf("%+v %v", r, err)
	}
}

// R (a): a symlinked skill path is refused, as on origin/main, before any
// case alias is resolved, and resolution never follows the link
func TestSyncRefusesASymlinkedSkill(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	root := t.TempDir()
	writeRel(t, root, ".claude/skills/x/SKILL.md", skillMD("x", "x"))
	writeRel(t, root, ".framework/skills.lock.json", `{"schema":1,"skills":{"k":{"paths":[".claude/skills/x"],"variant":"stata"}}}`+"\n")
	check(t, os.MkdirAll(filepath.Join(root, ".agents", "skills"), 0755))
	check(t, os.Symlink(filepath.Join(root, ".claude", "skills", "x"), filepath.Join(root, ".agents", "skills", "x")))
	paths := []string{".agents/skills/x"}
	if _, err := os.Stat(filepath.Join(root, ".AGENTS")); err == nil {
		paths = append(paths, ".AGENTS/skills/x")
	}
	for _, p := range paths {
		if _, err := Sync(context.Background(), cloud.NewClient("http://127.0.0.1:1", "t"), Options{Root: root, Path: p, Direction: "status", Variant: "stata"}); err == nil ||
			!strings.Contains(err.Error(), "symlinks") {
			t.Errorf("%s: %v", p, err)
		}
	}
	if samePath(root, ".agents/skills/x", ".claude/skills/x") {
		t.Error("samePath followed the link")
	}
}

// Python (b) and R (b): an entry whose tracked folder is gone (deleted,
// replaced by a file, or renamed) doesn't abort the pull. The entry gets
// its own missing row, an untracked copy its own row with an adopt hint,
// neither says current, and nothing is written
func TestPullWithATrackedFolderGone(t *testing.T) {
	w, h1 := cloudSkill(t, 1, "v1")
	lock := `{"schema":1,"skills":{"account:https://cloud.test:acct-1:stata":{"paths":[".claude/skills/x"],"variant":"stata","server":"https://cloud.test","skill_id":"acct-1","version":1,"hash":"` + h1 + `"}}}` + "\n"
	for _, mode := range []string{"deleted", "file", "skills file", "renamed"} {
		root := t.TempDir()
		writeRel(t, root, ".claude/skills/x/SKILL.md", skillMD("x", "v1"))
		writeRel(t, root, ".framework/skills.lock.json", lock)
		switch mode {
		case "deleted":
			check(t, os.RemoveAll(filepath.Join(root, ".claude")))
		case "file":
			check(t, os.RemoveAll(filepath.Join(root, ".claude")))
			writeRel(t, root, ".claude", "a file")
		case "skills file":
			check(t, os.RemoveAll(filepath.Join(root, ".claude", "skills")))
			writeRel(t, root, ".claude/skills", "a file")
		case "renamed":
			check(t, os.Rename(filepath.Join(root, ".claude"), filepath.Join(root, ".agents")))
		}
		for _, dry := range []bool{true, false} {
			res, err := Pull(PullOptions{Root: root, Dirs: []string{".agents/skills"}, Skills: []Wanted{w}, Variant: "stata", DryRun: dry})
			if err != nil {
				t.Fatalf("%s: %v", mode, err)
			}
			if res.Skills[0].Status != StatusMissing || strings.Join(res.Skills[0].Paths, ",") != ".claude/skills/x" ||
				!strings.Contains(res.Skills[0].Reason, "Every tracked copy is gone: .claude/skills/x no longer exists.") || res.Counts[StatusCurrent] != 0 {
				t.Errorf("%s: %+v", mode, res.Skills)
			}
			if mode == "renamed" && (len(res.Skills) != 2 || res.Skills[1].Status != StatusUntracked ||
				!strings.Contains(res.Skills[1].Hint, `fw skills sync ".agents/skills/x" --id "acct-1"`)) {
				t.Errorf("renamed: %+v", res.Skills)
			}
		}
		if readRel(t, root, ".framework/skills.lock.json") != lock || (mode != "renamed" && entriesIn(t, root, ".agents/skills") != nil) {
			t.Errorf("%s: pull wrote", mode)
		}
	}
}

// R (b): the adopt hint does what it says: sync --id moves the entry whose
// folders are gone to the copy, and the next pull is current
func TestAdoptHintMovesTheEntry(t *testing.T) {
	root := t.TempDir()
	w, h1 := cloudSkill(t, 1, "v1")
	writeRel(t, root, ".agents/skills/x/SKILL.md", skillMD("x", "v1"))
	writeRel(t, root, ".framework/skills.lock.json", `{"schema":1,"skills":{"account:https://cloud.test:acct-1:stata":{"paths":[".claude/skills/x"],"variant":"stata","server":"https://cloud.test","skill_id":"acct-1","version":1,"hash":"`+h1+`"}}}`+"\n")
	head := Snapshot{ID: "acct-1", Version: 1, Hash: h1, Bundle: w.Files, Variant: "stata"}
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) { check(t, json.NewEncoder(rw).Encode(head)) }))
	defer server.Close()
	writeRel(t, root, ".framework/skills.lock.json", strings.ReplaceAll(readRel(t, root, ".framework/skills.lock.json"), "https://cloud.test", server.URL))
	w.Sync.Server = server.URL
	r, err := Sync(context.Background(), cloud.NewClient(server.URL, "t"), Options{Root: root, Path: ".agents/skills/x", ID: "acct-1", Variant: "stata"})
	if err != nil || r.Action != "unchanged" {
		t.Fatalf("%+v %v", r, err)
	}
	entries := lockEntries(t, root)
	if len(entries) != 1 || entries[".agents/skills/x"] == nil || strings.Join(entries[".agents/skills/x"].Paths, ",") != ".agents/skills/x" {
		t.Fatalf("entries, re-keyed with the new path: %+v", entries)
	}
	res, err := Pull(PullOptions{Root: root, Dirs: []string{".agents/skills"}, Skills: []Wanted{w}, Variant: "stata"})
	if err != nil || res.Skills[0].Status != StatusCurrent || len(res.Skills) != 1 {
		t.Errorf("%+v %v", res, err)
	}
	// An ID whose entry still has a copy isn't adopted elsewhere
	writeRel(t, root, "vendor/skills/x/SKILL.md", skillMD("x", "v1"))
	if _, err := Sync(context.Background(), cloud.NewClient(server.URL, "t"), Options{Root: root, Path: "vendor/skills/x", ID: "acct-1", Variant: "stata", DryRun: true}); err == nil ||
		!strings.Contains(err.Error(), "already tracked at .agents/skills/x") {
		t.Errorf("second adopt: %v", err)
	}
}

// Python (d): copy reads and checks the lock before writing any file
func TestCopyChecksTheLockFirst(t *testing.T) {
	root := t.TempDir()
	writeRel(t, root, "src/x/SKILL.md", skillMD("x", "new"))
	writeRel(t, root, "dst/x/SKILL.md", skillMD("x", "old"))
	writeRel(t, root, ".framework/skills.lock.json", "{not json")
	if _, err := Copy(CopyOptions{Root: root, From: "src", To: []string{"dst"}}); err == nil {
		t.Fatal("copied with an unreadable lock")
	}
	if !strings.Contains(readRel(t, root, "dst/x/SKILL.md"), "old") || entriesIn(t, root, ".framework/skills-backups") != nil {
		t.Error("the target was overwritten")
	}
}

// CLI 1: the setup names a different Cloud skill (acct-2) under the name
// the lock tracks as acct-1: the file is kept, with an explicit status and
// the reason, never current with no paths
func TestPullKeepsADifferentCloudSkillsFolder(t *testing.T) {
	root := t.TempDir()
	_, h1 := cloudSkill(t, 1, "acct-1's")
	w, _ := cloudSkill(t, 1, "acct-2's")
	w.Sync.ID = "acct-2"
	writeRel(t, root, ".claude/skills/x/SKILL.md", skillMD("x", "acct-1's"))
	lock := `{"schema":1,"skills":{"account:https://cloud.test:acct-1:stata":{"paths":[".claude/skills/x"],"variant":"stata","server":"https://cloud.test","skill_id":"acct-1","version":1,"hash":"` + h1 + `"}}}` + "\n"
	writeRel(t, root, ".framework/skills.lock.json", lock)
	res, err := Pull(PullOptions{Root: root, Dirs: []string{".claude/skills"}, Skills: []Wanted{w}, Variant: "stata", Overwrite: true})
	if err != nil || res.Skills[0].Status != StatusKept || strings.Join(res.Skills[0].Paths, ",") != ".claude/skills/x" ||
		!strings.HasPrefix(res.Skills[0].Reason, ".claude/skills/x belongs to a different skill (acct-1), so pull left it alone") {
		t.Fatalf("%+v %v", res, err)
	}
	if !strings.Contains(readRel(t, root, ".claude/skills/x/SKILL.md"), "acct-1's") || readRel(t, root, ".framework/skills.lock.json") != lock {
		t.Error("the file or the lock changed")
	}
}

// CLI 3: copying onto a folder spelled differently on disk records the
// disk's spelling, to the last part, and pull doesn't call it removed
func TestCopyRecordsTheLastPartsDiskSpelling(t *testing.T) {
	root := t.TempDir()
	caseInsensitive(t, root)
	v1 := Wanted{Name: "analysis", Package: true, Files: BundleFromText(skillMD("analysis", "v1"), nil)}
	if _, err := Pull(PullOptions{Root: root, Dirs: []string{".claude/skills"}, Skills: []Wanted{v1}, Variant: "stata"}); err != nil {
		t.Fatal(err)
	}
	writeRel(t, root, ".agents/skills/ANALYSIS/SKILL.md", skillMD("analysis", "old"))
	res, err := Copy(CopyOptions{Root: root, From: ".claude/skills", To: []string{".agents/skills"}})
	if err != nil || res.Skills[0].To != ".agents/skills/ANALYSIS" {
		t.Fatalf("%+v %v", res, err)
	}
	if paths := lockEntries(t, root)["package:analysis"].Paths; strings.Join(paths, ",") != ".claude/skills/analysis,.agents/skills/ANALYSIS" {
		t.Errorf("recorded: %v", paths)
	}
	pulled, err := Pull(PullOptions{Root: root, Dirs: []string{".claude/skills", ".agents/skills"}, Skills: []Wanted{v1}, Variant: "stata"})
	if err != nil || pulled.Counts[StatusRemoved] != 0 || len(pulled.Skills) != 1 || pulled.Skills[0].Status != StatusCurrent {
		t.Errorf("pull: %+v %v", pulled, err)
	}
}

// copyArgs reads a printed fw skills copy command, as a shell would.
func copyArgs(t *testing.T, root, command string) CopyOptions {
	t.Helper()
	f := strings.Fields(strings.ReplaceAll(command, `"`, ""))
	if len(f) < 3 || f[0] != "fw" || f[1] != "skills" || f[2] != "copy" {
		t.Fatalf("not a copy command: %s", command)
	}
	o := CopyOptions{Root: root}
	for i := 3; i+1 < len(f); i += 2 {
		switch f[i] {
		case "--from":
			o.From = AssistantSkillsDir(f[i+1])
		case "--to":
			o.To = append(o.To, AssistantSkillsDir(f[i+1]))
		case "--from-dir":
			o.From = f[i+1]
		case "--to-dir":
			o.To = append(o.To, f[i+1])
		case "--skill":
			o.Skills = append(o.Skills, f[i+1])
		}
	}
	return o
}

// Round 12, 1: adopting re-keys the entry with its new path, so a skill
// that later goes into the vacated path, by pull or by sync, gets its own
// entry and both survive
func TestAdoptedEntryKeepsItsOwnKey(t *testing.T) {
	w1, h1 := cloudSkill(t, 1, "v1")
	head := Snapshot{ID: "acct-1", Version: 1, Hash: h1, Bundle: w1.Files, Variant: "stata"}
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			var body struct {
				Bundle []File `json:"bundle"`
			}
			check(t, json.NewDecoder(r.Body).Decode(&body))
			h, _ := Hash(body.Bundle)
			check(t, json.NewEncoder(rw).Encode(Snapshot{ID: "new-1", Version: 1, Hash: h, Bundle: body.Bundle, Variant: "stata"}))
			return
		}
		check(t, json.NewEncoder(rw).Encode(head))
	}))
	defer server.Close()
	client := cloud.NewClient(server.URL, "t")
	for _, then := range []string{"pull", "sync"} {
		root := t.TempDir()
		// An entry fw skills sync made, keyed by its path, now gone
		writeRel(t, root, ".agents/skills/x/SKILL.md", skillMD("x", "v1"))
		check(t, os.MkdirAll(filepath.Join(root, ".claude", "skills"), 0755))
		writeRel(t, root, ".framework/skills.lock.json", `{"schema":1,"skills":{".claude/skills/x":{"paths":[".claude/skills/x"],"variant":"stata",`+
			`"server":"`+server.URL+`","skill_id":"acct-1","version":1,"hash":"`+h1+`"}}}`+"\n")
		if _, err := Sync(context.Background(), client, Options{Root: root, Path: ".agents/skills/x", ID: "acct-1", Variant: "stata"}); err != nil {
			t.Fatal(err)
		}
		switch then {
		case "pull":
			w2, _ := cloudSkill(t, 1, "acct-2's")
			w2.Sync.ID, w2.Sync.Server = "acct-2", server.URL
			if _, err := Pull(PullOptions{Root: root, Dirs: []string{".claude/skills"}, Skills: []Wanted{w2}, Variant: "stata"}); err != nil {
				t.Fatal(err)
			}
		case "sync":
			writeRel(t, root, ".claude/skills/x/SKILL.md", skillMD("x", "a new skill"))
			if _, err := Sync(context.Background(), client, Options{Root: root, Path: ".claude/skills/x", Variant: "stata"}); err != nil {
				t.Fatal(err)
			}
		}
		ids := map[string]string{}
		for _, e := range lockEntries(t, root) {
			ids[e.ID] = strings.Join(e.Paths, ",")
		}
		other := map[string]string{"pull": "acct-2", "sync": "new-1"}[then]
		if len(ids) != 2 || ids["acct-1"] != ".agents/skills/x" || ids[other] != ".claude/skills/x" {
			t.Errorf("%s: entries %v", then, ids)
		}
	}
}

// Round 12, 3, as P2 has it: a path an entry with another Cloud identity
// records stays that skill's, copy or not. With acct-1's copy moved out
// and not adopted, the setup's acct-2 isn't installed there, acct-1's
// entry keeps its paths, and the hint puts acct-1 itself back
func TestTheSetupsSkillTakesAVacatedPath(t *testing.T) {
	_, h1 := cloudSkill(t, 1, "acct-1's")
	w, _ := cloudSkill(t, 1, "acct-2's")
	w.Sync.ID = "acct-2"
	root := t.TempDir()
	writeRel(t, root, ".claude/skills/x/SKILL.md", skillMD("x", "acct-1's"))
	lock := `{"schema":1,"skills":{"account:https://cloud.test:acct-1:stata":{"paths":[".claude/skills/x"],"variant":"stata",` +
		`"server":"https://cloud.test","skill_id":"acct-1","version":1,"hash":"` + h1 + `"}}}` + "\n"
	writeRel(t, root, ".framework/skills.lock.json", lock)
	dirs := []string{".claude/skills"}
	res, err := Pull(PullOptions{Root: root, Dirs: dirs, Skills: []Wanted{w}, Variant: "stata"})
	if err != nil || !strings.Contains(res.Skills[0].Reason, `move it to kept-skills/x, run fw skills sync "kept-skills/x" --id "acct-1" to keep its link to acct-1, then pull again`) ||
		res.Skills[0].Hint != `fw skills sync "kept-skills/x" --id "acct-1"` {
		t.Fatalf("%+v %v", res, err)
	}
	check(t, os.Rename(filepath.Join(root, ".claude", "skills", "x"), filepath.Join(root, "moved-x")))
	for _, dry := range []bool{true, false} {
		res, err = Pull(PullOptions{Root: root, Dirs: dirs, Skills: []Wanted{w}, Variant: "stata", DryRun: dry})
		if err != nil || res.Skills[0].Status == StatusAdded || res.Skills[0].Hint != `fw skills sync ".claude/skills/x" --resolve remote` {
			t.Fatalf("dry run %v: %+v %v", dry, res, err)
		}
	}
	if entriesIn(t, root, ".claude/skills") != nil || readRel(t, root, ".framework/skills.lock.json") != lock {
		t.Error("the setup's skill took acct-1's path")
	}
}

// Round 12, 5b: when a file blocks a copy's folder, the message says what
// is in the way before giving the command, and the command then works
func TestMissingCopyBlockedByAFile(t *testing.T) {
	root := t.TempDir()
	writeRel(t, root, ".claude/skills/x/SKILL.md", skillMD("x", "x"))
	writeRel(t, root, "vendor/skills", "a file")
	writeRel(t, root, ".framework/skills.lock.json", `{"schema":1,"skills":{"k":{"paths":[".claude/skills/x","vendor/skills/x"],"variant":"stata"}}}`+"\n")
	client := cloud.NewClient("http://127.0.0.1:1", "t")
	opts := Options{Root: root, Path: ".claude/skills/x", Direction: "status", Variant: "stata"}
	_, err := Sync(context.Background(), client, opts)
	var se *SkillError
	if !errors.As(err, &se) || !strings.Contains(err.Error(), "vendor/skills is a file where vendor/skills/x's folder should be: move it aside first. Copy it back: fw skills copy") {
		t.Fatalf("%v", err)
	}
	command := se.Hint
	check(t, os.Remove(filepath.Join(root, "vendor", "skills")))
	if _, err = Copy(copyArgs(t, root, command)); err != nil {
		t.Fatalf("%s: %v", command, err)
	}
	if _, err = Sync(context.Background(), client, opts); err != nil {
		t.Errorf("after the copy: %v", err)
	}
}

// P1: a map key that looks canonical isn't trusted: only the entry's own
// metadata (server, skill_id, variant; or a built-in's origin) matches it
func TestStaleKeysDontMatch(t *testing.T) {
	root := t.TempDir()
	writeRel(t, root, ".claude/skills/y/SKILL.md", skillMD("y", "y"))
	writeRel(t, root, ".claude/skills/z/SKILL.md", skillMD("z", "z"))
	lock := `{"schema":1,"skills":{` +
		`"package:x":{"paths":[".claude/skills/y"],"variant":"stata","origin":{"skill_id":"framework:y","hash":"y"},"hash":"y"},` +
		`"account:https://cloud.test:acct-1:stata":{"paths":[".claude/skills/z"],"variant":"stata","server":"https://cloud.test","skill_id":"acct-9","version":1,"hash":"z"}}}` + "\n"
	writeRel(t, root, ".framework/skills.lock.json", lock)
	pkg := Wanted{Name: "x", Package: true, Files: BundleFromText(skillMD("x", "packaged x"), nil)}
	cl, _ := cloudSkill(t, 1, "cloud x")
	cl.Name = "w"
	cl.Files = BundleFromText(skillMD("w", "cloud w"), nil)
	cl.Sync.Hash, _ = Hash(cl.Files)
	if _, err := Pull(PullOptions{Root: root, Dirs: []string{".claude/skills"}, Skills: []Wanted{pkg, cl}, Variant: "stata"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readRel(t, root, ".claude/skills/y/SKILL.md"), "y") || !strings.Contains(readRel(t, root, ".claude/skills/z/SKILL.md"), "z") {
		t.Error("a stale-keyed entry's copy was written")
	}
	entries := lockEntries(t, root)
	if strings.Join(entries["package:x"].Paths, ",") != ".claude/skills/y" || entries["account:https://cloud.test:acct-1:stata"].ID != "acct-9" {
		t.Errorf("a stale-keyed entry changed: %+v", entries)
	}
	if !strings.Contains(readRel(t, root, ".claude/skills/x/SKILL.md"), "packaged x") || !strings.Contains(readRel(t, root, ".claude/skills/w/SKILL.md"), "cloud w") {
		t.Error("the setup's skills weren't installed in their own folders")
	}
}

// P2: both copies of a personal skill are gone and the setup names a
// different ID: pull never takes the paths from it. The setup's skill
// isn't installed there, the entry keeps its paths, and it's reported
// missing with what puts it back
func TestPullNeverTakesAPathFromAnotherCloudSkill(t *testing.T) {
	root := t.TempDir()
	w, _ := cloudSkill(t, 1, "acct-2's")
	w.Sync.ID = "acct-2"
	for _, d := range []string{".claude/skills", ".agents/skills"} {
		check(t, os.MkdirAll(filepath.Join(root, filepath.FromSlash(d)), 0755))
	}
	lock := `{"schema":1,"skills":{"account:https://cloud.test:own-1:stata":{"paths":[".claude/skills/x",".agents/skills/x"],"variant":"stata",` +
		`"server":"https://cloud.test","skill_id":"own-1","version":1,"hash":"h"}}}` + "\n"
	writeRel(t, root, ".framework/skills.lock.json", lock)
	for _, dry := range []bool{true, false} {
		res, err := Pull(PullOptions{Root: root, Dirs: []string{".claude/skills", ".agents/skills"}, Skills: []Wanted{w}, Variant: "stata", DryRun: dry})
		if err != nil || len(res.Skills) != 2 || res.Skills[0].Status == StatusAdded || res.Skills[1].Status != StatusMissing ||
			!strings.Contains(res.Skills[0].Reason, "belongs to a different skill (own-1)") || !strings.Contains(res.Skills[1].Hint, "--resolve remote") {
			t.Fatalf("dry run %v: %+v %v", dry, res, err)
		}
	}
	if entriesIn(t, root, ".claude/skills") != nil || entriesIn(t, root, ".agents/skills") != nil || readRel(t, root, ".framework/skills.lock.json") != lock {
		t.Error("pull took own-1's paths")
	}
}

// P3: legacy entries that are one skill split in two are merged on read,
// so its copies are judged together: an edit in one half makes the skill
// diverged, and matching halves move together into one entry
func TestSplitEntriesMergeOnRead(t *testing.T) {
	w2, _ := cloudSkill(t, 2, "v2")
	_, h1 := cloudSkill(t, 1, "v1")
	split := `{"schema":1,"skills":{` +
		`"a":{"paths":[".claude/skills/x"],"variant":"stata","server":"https://cloud.test","skill_id":"acct-1","version":1,"hash":"` + h1 + `"},` +
		`"b":{"paths":[".agents/skills/x"],"variant":"stata","server":"https://cloud.test","skill_id":"acct-1","version":1,"hash":"` + h1 + `"}}}` + "\n"
	dirs := []string{".claude/skills", ".agents/skills"}
	root := t.TempDir()
	writeRel(t, root, ".claude/skills/x/SKILL.md", skillMD("x", "v1"))
	writeRel(t, root, ".agents/skills/x/SKILL.md", skillMD("x", "edited"))
	writeRel(t, root, ".framework/skills.lock.json", split)
	res, err := Pull(PullOptions{Root: root, Dirs: dirs, Skills: []Wanted{w2}, Variant: "stata"})
	if err != nil || res.Skills[0].Status != StatusDiverged || !strings.Contains(readRel(t, root, ".claude/skills/x/SKILL.md"), "v1") {
		t.Errorf("one half edited: %+v %v", res, err)
	}
	if changes, _, err := LocalChanges(root, dirs); err != nil || len(changes) != 1 {
		t.Errorf("push: %+v %v", changes, err)
	}

	root = t.TempDir()
	writeRel(t, root, ".claude/skills/x/SKILL.md", skillMD("x", "v1"))
	writeRel(t, root, ".agents/skills/x/SKILL.md", skillMD("x", "v1"))
	writeRel(t, root, ".framework/skills.lock.json", split)
	if _, err := Pull(PullOptions{Root: root, Dirs: dirs, Skills: []Wanted{w2}, Variant: "stata"}); err != nil {
		t.Fatal(err)
	}
	entries := lockEntries(t, root)
	if len(entries) != 1 || len(entries["a"].Paths) != 2 || entries["a"].Version != 2 ||
		!strings.Contains(readRel(t, root, ".agents/skills/x/SKILL.md"), "v2") || !strings.Contains(readRel(t, root, ".claude/skills/x/SKILL.md"), "v2") {
		t.Errorf("halves in step: %+v", entries)
	}

	// A built-in split the same way merges by name, origin, and variant
	root = t.TempDir()
	v1 := Wanted{Name: "x", Package: true, Files: BundleFromText(skillMD("x", "v1"), nil)}
	v2 := Wanted{Name: "x", Package: true, Files: BundleFromText(skillMD("x", "v2"), nil)}
	hp, _ := Hash(v1.Files)
	writeRel(t, root, ".claude/skills/x/SKILL.md", skillMD("x", "v1"))
	writeRel(t, root, ".agents/skills/x/SKILL.md", skillMD("x", "v1"))
	writeRel(t, root, ".framework/skills.lock.json", `{"schema":1,"skills":{`+
		`"p1":{"paths":[".claude/skills/x"],"variant":"stata","origin":{"skill_id":"framework:x","hash":"`+hp+`"},"hash":"`+hp+`"},`+
		`"p2":{"paths":[".agents/skills/x"],"variant":"stata","origin":{"skill_id":"framework:x","hash":"`+hp+`"},"hash":"`+hp+`"}}}`+"\n")
	if _, err := Pull(PullOptions{Root: root, Dirs: dirs, Skills: []Wanted{v2}, Variant: "stata"}); err != nil {
		t.Fatal(err)
	}
	if entries := lockEntries(t, root); len(entries) != 1 || len(entries["p1"].Paths) != 2 || !strings.Contains(readRel(t, root, ".agents/skills/x/SKILL.md"), "v2") {
		t.Errorf("built-in: %+v", entries)
	}
}
