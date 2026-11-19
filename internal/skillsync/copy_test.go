package skillsync

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/frameworktools/framework-cli/internal/cloud"
)

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
	// Under a file, on every OS (Windows reports such a path as not found),
	// in a dry run too
	for _, to := range []string{"dst/skills", "dst/a/b"} {
		for _, dry := range []bool{true, false} {
			_, err := Copy(CopyOptions{Root: root, From: "src", To: []string{to}, DryRun: dry})
			if err == nil || err.Error() != "the target "+to+" is under dst, which is a file, not a folder" {
				t.Errorf("under a file, %s (dry run %v): %v", to, dry, err)
			}
		}
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
func cloudSkill(t *testing.T, version int, body string) (Source, string) {
	t.Helper()
	files := BundleFromText(skillMD("x", body), nil)
	h, err := Hash(files)
	check(t, err)
	return CloudSource("x", SyncSource{ID: "acct-1", Version: version, Hash: h, Variant: "stata", Server: "https://cloud.test"}, files, ""), h
}

// pullSkills pulls a setup that names these skills.
func pullSkills(root string, dirs []string, skills ...Source) (PullResult, error) {
	return Pull(PullOptions{Root: root, Dirs: dirs, Offer: Offer{Source: "cloud", Skills: skills}})
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
// lockPaths lists the copies the lock tracks for an entry, sorted.
func lockPaths(t *testing.T, root, key string) []string {
	t.Helper()
	lock, err := ReadLock(root)
	check(t, err)
	if e := lock.Skills[key]; e != nil {
		return e.paths()
	}
	return nil
}

// R round 8, 4: a case alias of the source folder finds the lock's entry
// by filesystem identity, so the target still joins it
func TestCopyFromACaseAliasJoinsTheEntry(t *testing.T) {
	root := t.TempDir()
	v1 := PackageSource("x", "stata", BundleFromText(skillMD("x", "v1"), nil), nil)
	if _, err := pullSkills(root, []string{".claude/skills"}, v1); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, ".CLAUDE")); err != nil {
		t.Skip("this volume is case-sensitive")
	}
	if _, err := Copy(CopyOptions{Root: root, From: ".Claude/skills", To: []string{".agents/skills"}}); err != nil {
		t.Fatal(err)
	}
	if paths := lockPaths(t, root, "framework:x@stata"); strings.Join(paths, ",") != ".agents/skills/x,.claude/skills/x" {
		t.Errorf("paths: %v", paths)
	}
	// Copying back to the alias of a recorded copy is that copy, not another skill
	if _, err := Copy(CopyOptions{Root: root, From: ".agents/skills", To: []string{".CLAUDE/skills"}}); err != nil {
		t.Errorf("back to an alias: %v", err)
	}
	if paths := lockPaths(t, root, "framework:x@stata"); len(paths) != 2 {
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
	v1 := PackageSource("x", "stata", BundleFromText(skillMD("x", "v1"), nil), nil)
	if _, err := pullSkills(root, []string{".claude/skills"}, v1); err != nil {
		t.Fatal(err)
	}
	check(t, os.MkdirAll(filepath.Join(root, ".agents", "skills"), 0755))
	res, err := Copy(CopyOptions{Root: root, From: ".CLAUDE/skills", To: []string{".AGENTS/skills"}})
	if err != nil || res.From != ".claude/skills" || strings.Join(res.To, ",") != ".agents/skills" || res.Skills[0].To != ".agents/skills/x" {
		t.Fatalf("%+v %v", res, err)
	}
	if paths := lockPaths(t, root, "framework:x@stata"); strings.Join(paths, ",") != ".agents/skills/x,.claude/skills/x" {
		t.Errorf("recorded: %v", paths)
	}

	// A lock that already has an alias spelling still matches the real folder
	writeRel(t, root, ".framework/skills.lock.json", strings.Replace(readRel(t, root, ".framework/skills.lock.json"), ".agents/skills/x", ".AGENTS/skills/x", 1))
	dirs := []string{".claude/skills", ".agents/skills"}
	if pp, err := PlanPush(PushOptions{Root: root, Dirs: dirs}); err != nil || len(pp.Steps) != 0 {
		t.Errorf("push calls it untracked: %+v %v", pp, err)
	}
	// Found, sync asks Cloud for the entry's skill (framework:x); an
	// untracked path would ask for nothing and be pushed as a new skill
	_, err = Sync(context.Background(), cloud.NewClient("http://127.0.0.1:1", "t"), Options{Root: root, Path: ".agents/skills/x", Direction: "status", Variant: "stata"})
	if err == nil || !strings.Contains(err.Error(), "skills/sync/framework:x") {
		t.Errorf("sync didn't find the entry: %v", err)
	}
	pulled, err := pullSkills(root, dirs, v1)
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

// CLI 3: copying onto a folder spelled differently on disk records the
// disk's spelling, to the last part, and pull doesn't call it removed
func TestCopyRecordsTheLastPartsDiskSpelling(t *testing.T) {
	root := t.TempDir()
	caseInsensitive(t, root)
	v1 := PackageSource("analysis", "stata", BundleFromText(skillMD("analysis", "v1"), nil), nil)
	if _, err := pullSkills(root, []string{".claude/skills"}, v1); err != nil {
		t.Fatal(err)
	}
	writeRel(t, root, ".agents/skills/ANALYSIS/SKILL.md", skillMD("analysis", "old"))
	res, err := Copy(CopyOptions{Root: root, From: ".claude/skills", To: []string{".agents/skills"}})
	if err != nil || res.Skills[0].To != ".agents/skills/ANALYSIS" {
		t.Fatalf("%+v %v", res, err)
	}
	if paths := lockPaths(t, root, "framework:analysis@stata"); strings.Join(paths, ",") != ".agents/skills/ANALYSIS,.claude/skills/analysis" {
		t.Errorf("recorded: %v", paths)
	}
	pulled, err := pullSkills(root, []string{".claude/skills", ".agents/skills"}, v1)
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
	if !errors.As(err, &se) || !strings.Contains(err.Error(), "vendor/skills is a file where vendor/skills/x's folder should be: move it aside first. Copy it there: fw skills copy") {
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
