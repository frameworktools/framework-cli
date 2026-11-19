package cli

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/frameworktools/framework-cli/internal/engine"
	"github.com/frameworktools/framework-cli/internal/skillsync"
)

// flagsOf is copyFlags with each named flag given, even when empty.
func flagsOf(given map[string]string) copyFlags {
	f := copyFlags{}
	f.from, f.fromSet = given["from"]
	f.fromDir, f.fromDirSet = given["from-dir"]
	f.to, f.toSet = given["to"]
	f.toDir, f.toDirSet = given["to-dir"]
	return f
}

// copyFolders only reads flags; the project is checked in skillsync.Copy.
func TestSkillsCopyFolders(t *testing.T) {
	for _, c := range []struct {
		flags   map[string]string
		source  string
		targets []string
	}{
		{map[string]string{"from": "claude", "to": "agents"}, ".claude/skills", []string{".agents/skills"}},
		{map[string]string{"from": "codex", "to": "claude"}, ".agents/skills", []string{".claude/skills"}},
		{map[string]string{"from": "copilot", "to": "claude"}, ".agents/skills", []string{".claude/skills"}},
		{map[string]string{"from": "Claude", "to": "Codex"}, ".claude/skills", []string{".agents/skills"}},
		{map[string]string{"from": "claude"}, ".claude/skills", nil},
		{map[string]string{"from-dir": "./.claude/skills/", "to-dir": "some/other/skills"}, ".claude/skills", []string{"some/other/skills"}},
	} {
		source, targets, err := copyFolders(flagsOf(c.flags))
		if err != nil || source != c.source || !reflect.DeepEqual(targets, c.targets) {
			t.Errorf("%v: got %q %q %v", c.flags, source, targets, err)
		}
	}

	for _, c := range []struct {
		flags map[string]string
		want  string
	}{
		{map[string]string{}, "pass --from claude, --from agents, or --from-dir DIR"},
		{map[string]string{"from": "claude", "from-dir": "x"}, "pass --from or --from-dir, not both"},
		{map[string]string{"from": "claude", "to": "agents", "to-dir": "x"}, "pass --to or --to-dir, not both"},
		{map[string]string{"from": "claude", "to-dir": "x"}, "not one of each"},
		{map[string]string{"from-dir": "x", "to": "agents"}, "not one of each"},
		{map[string]string{"from": "claude", "to": "claude"}, "the source and target are both .claude/skills"},
		{map[string]string{"from": "codex", "to": "copilot"}, "the source and target are both .agents/skills"},
		{map[string]string{"from-dir": "a/skills", "to-dir": "a/skills/"}, "the source and target are both a/skills"},
		{map[string]string{"from": "cursor"}, "--from takes agents, claude, codex, copilot"},
		{map[string]string{"from": "claude", "to": "cursor"}, "--to takes agents, claude, codex, copilot"},
		{map[string]string{"from-dir": "../elsewhere", "to-dir": "x"}, "a folder inside the project"},
		{map[string]string{"from-dir": "x", "to-dir": "/abs/skills"}, "a folder inside the project"},
		{map[string]string{"from-dir": "x", "to-dir": "D:skills"}, "has a colon"},
		{map[string]string{"from-dir": "x", "to-dir": "skills/a:b"}, "has a colon"},
		{map[string]string{"from-dir": "a/x/../b", "to-dir": "x"}, "has a .. part"},
		{map[string]string{"from-dir": ".", "to-dir": "x"}, "a folder inside the project"},
		// Given but empty is an error, never the default
		{map[string]string{"from-dir": "x", "to-dir": ""}, "--to-dir takes a folder inside the project"},
		{map[string]string{"from-dir": ""}, "--from-dir takes a folder inside the project"},
		{map[string]string{"from": "claude", "to": ""}, "--to takes agents"},
		{map[string]string{"from": ""}, "--from takes agents"},
	} {
		if _, _, err := copyFolders(flagsOf(c.flags)); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v: got %v, want %q", c.flags, err, c.want)
		}
	}
}

// tree lists every file under dir with its contents, and x for an
// executable one. Skill files are written 644 or 755, as in a pull.
func tree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	files := os.DirFS(dir)
	_ = fs.WalkDir(files, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, _ := d.Info()
		data, _ := fs.ReadFile(files, p)
		mode := "-"
		if info.Mode().Perm()&0o111 != 0 {
			mode = "x"
		}
		out[p] = mode + " " + string(data)
		return nil
	})
	return out
}

// copyProject is a Stata project with skills in .claude/skills and an
// older, partly edited set in .agents/skills.
func copyProject(t *testing.T, h *harness) string {
	t.Helper()
	dir := stataSkillsProject(t, h.app.home, "http://127.0.0.1:1", false)
	h.app.cwd = dir
	claude := filepath.Join(dir, ".claude", "skills")
	agents := filepath.Join(dir, ".agents", "skills")
	// analysis: edited in .claude, with companion files and a script
	writeTestFile(t, filepath.Join(claude, "analysis", "SKILL.md"), skillText("analysis", "Edited"))
	writeTestFile(t, filepath.Join(claude, "analysis", "reference", "notes.md"), "notes\n")
	writeTestFile(t, filepath.Join(claude, "analysis", "scripts", "run.sh"), "#!/bin/sh\necho hi\n")
	if err := os.Chmod(filepath.Join(claude, "analysis", "scripts", "run.sh"), 0o755); err != nil { //nolint:gosec // a skill script, executable
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(agents, "analysis", "SKILL.md"), skillText("analysis", "Original"))
	writeTestFile(t, filepath.Join(agents, "analysis", "old.md"), "old companion\n")
	// figures: the same in both
	for _, d := range []string{claude, agents} {
		writeTestFile(t, filepath.Join(d, "figures", "SKILL.md"), skillText("figures", "Figures"))
	}
	// extra: only in .agents
	writeTestFile(t, filepath.Join(agents, "extra", "SKILL.md"), skillText("extra", "Extra"))
	writeTestFile(t, filepath.Join(dir, ".framework", "skills.lock.json"), `{"schema":1,"skills":{"package:analysis":{"paths":[".claude/skills/analysis",".agents/skills/analysis"],"variant":"stata","hash":"abc"}}}`+"\n")
	return dir
}

func TestSkillsCopy(t *testing.T) {
	h := newHarness(t, nil)
	dir := copyProject(t, h)
	agents := filepath.Join(dir, ".agents", "skills")
	backups := filepath.Join(dir, ".framework", "skills-backups")
	lockBefore := readFile(t, filepath.Join(dir, ".framework", "skills.lock.json"))

	// A dry run reports and writes nothing
	before := tree(t, dir)
	out, _, err := h.run(t, "", "skills", "copy", "--from", "claude", "--dry-run")
	want := []string{
		"Skills (dry run, nothing written) from .claude/skills: 1 to copy, 1 up to date, 1 not in .claude/skills (kept)",
		"  Would copy: .claude/skills/analysis -> .agents/skills/analysis",
		"  Not in .claude/skills (kept): .agents/skills/extra",
	}
	if err != nil || out != strings.Join(want, "\n")+"\n" {
		t.Fatalf("dry run: %v\n%s", err, out)
	}
	if !reflect.DeepEqual(before, tree(t, dir)) {
		t.Error("the dry run wrote files")
	}

	// The copy: the default target is .agents/skills, the only other folder
	out, _, err = h.run(t, "", "skills", "copy", "--from", "claude")
	want = []string{
		"Skills from .claude/skills: 1 copied, 1 up to date, 1 not in .claude/skills (kept)",
		"  Copied: .claude/skills/analysis -> .agents/skills/analysis",
		"    Backup: " + firstBackup(t, dir),
		"  Not in .claude/skills (kept): .agents/skills/extra",
	}
	if err != nil || out != strings.Join(want, "\n")+"\n" {
		t.Fatalf("copy: %v\n%s", err, out)
	}
	got := tree(t, filepath.Join(agents, "analysis"))
	src := tree(t, filepath.Join(dir, ".claude", "skills", "analysis"))
	if !reflect.DeepEqual(got, src) {
		t.Errorf("the copy differs from the source:\n%v\n%v", got, src)
	}
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(filepath.Join(agents, "analysis", "scripts", "run.sh")); err != nil || info.Mode().Perm()&0o111 == 0 {
			t.Errorf("run.sh lost its executable mode: %v", info.Mode())
		}
	}
	// One backup, of the replaced copy only
	entries, _ := os.ReadDir(backups)
	if len(entries) != 1 {
		t.Fatalf("backups: %v", entries)
	}
	old := tree(t, filepath.Join(backups, entries[0].Name()))
	if !strings.Contains(old["SKILL.md"], "Original") || old["old.md"] == "" {
		t.Errorf("backup: %v", old)
	}
	if !strings.Contains(readFile(t, filepath.Join(dir, ".framework", ".gitignore")), "skills-backups/\n") {
		t.Error(".framework/.gitignore doesn't keep backups out of Git")
	}
	// Nothing deleted, and the lock untouched
	if !strings.Contains(readFile(t, filepath.Join(agents, "extra", "SKILL.md")), "Extra") {
		t.Error("a skill not in the source was deleted")
	}
	if readFile(t, filepath.Join(dir, ".framework", "skills.lock.json")) != lockBefore {
		t.Error("the copy changed skills.lock.json")
	}
	if _, err := os.Stat(filepath.Join(dir, ".framework", "skills.sync.lock")); !os.IsNotExist(err) {
		t.Error("the sync lock was left behind")
	}

	// Again: everything matches, so nothing is written or backed up
	out, _, err = h.run(t, "", "skills", "copy", "--from", "claude", "--to", "codex", "--json")
	var res copyJSON
	if err != nil || json.Unmarshal([]byte(out), &res) != nil || res.Counts["copied"] != 0 || res.Counts["current"] != 2 || res.Counts["missing"] != 1 ||
		res.From != ".claude/skills" || !reflect.DeepEqual(res.To, []string{".agents/skills"}) || len(res.Backups) != 0 {
		t.Fatalf("second copy: %v\n%s", err, out)
	}
	if entries, _ = os.ReadDir(backups); len(entries) != 1 {
		t.Errorf("an identical copy was backed up: %v", entries)
	}
	// A missing skill has a null from in JSON, as in R and Python
	if !strings.Contains(strings.Join(strings.Fields(out), ""), `{"name":"extra","status":"missing","from":null,"to":".agents/skills/extra","backup":null}`) {
		t.Errorf("missing skill JSON:\n%s", out)
	}
}

func TestSkillsCopySkillFilterAndFolders(t *testing.T) {
	h := newHarness(t, nil)
	dir := copyProject(t, h)

	// --skill copies only the skills it names, and doesn't list the others
	writeTestFile(t, filepath.Join(dir, ".agents", "skills", "figures", "SKILL.md"), skillText("figures", "Edited in agents"))
	out, _, err := h.run(t, "", "skills", "copy", "--from", "agents", "--skill", "figures")
	if err != nil || out != "Skills from .agents/skills: 1 copied\n  Copied: .agents/skills/figures -> .claude/skills/figures\n    Backup: "+firstBackup(t, dir)+"\n" {
		t.Fatalf("--skill: %v\n%s", err, out)
	}
	if !strings.Contains(readFile(t, filepath.Join(dir, ".claude", "skills", "figures", "SKILL.md")), "Edited in agents") ||
		!strings.Contains(readFile(t, filepath.Join(dir, ".claude", "skills", "analysis", "SKILL.md")), "Edited") {
		t.Error("--skill copied the wrong skills")
	}
	if _, err := os.Stat(filepath.Join(dir, ".claude", "skills", "extra")); !os.IsNotExist(err) {
		t.Error("--skill figures copied extra")
	}
	if _, _, err = h.run(t, "", "skills", "copy", "--from", "claude", "--skill", "nope"); err == nil || err.Error() != "no skill named nope in .claude/skills" {
		t.Errorf("unknown skill: %v", err)
	}

	// Folders: the target is created when it doesn't exist
	out, _, err = h.run(t, "", "skills", "copy", "--from-dir", ".claude/skills", "--to-dir", "some/other/skills", "--skill", "analysis", "--skill", "figures")
	if err != nil || !strings.HasPrefix(out, "Skills from .claude/skills: 2 copied\n") ||
		!strings.Contains(readFile(t, filepath.Join(dir, "some", "other", "skills", "analysis", "reference", "notes.md")), "notes") {
		t.Fatalf("--to-dir: %v\n%s", err, out)
	}

	// Sync state is refused, and nothing is written there
	if _, _, err = h.run(t, "", "skills", "copy", "--from-dir", ".claude/skills", "--to-dir", ".framework/skills.sync.lock"); err == nil || !strings.Contains(err.Error(), "overlaps .framework") {
		t.Errorf("sync lock: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".framework", "skills.sync.lock")); !os.IsNotExist(err) {
		t.Error("the copy left .framework/skills.sync.lock")
	}

	// A missing source
	if _, _, err = h.run(t, "", "skills", "copy", "--from-dir", "nowhere/skills"); err == nil || err.Error() != "the source folder nowhere/skills does not exist" {
		t.Errorf("missing source: %v", err)
	}
}

func firstBackup(t *testing.T, dir string) string {
	t.Helper()
	entries, _ := os.ReadDir(filepath.Join(dir, ".framework", "skills-backups"))
	names := []string{}
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	if len(names) != 1 {
		t.Fatalf("backups: %v", names)
	}
	return ".framework/skills-backups/" + names[0]
}

// --to-dir= with no folder is an error, not every assistant folder
func TestSkillsCopyEmptyFolderFlag(t *testing.T) {
	h := newHarness(t, nil)
	dir := copyProject(t, h)
	before := tree(t, dir)
	for _, args := range [][]string{{"--from-dir", ".claude/skills", "--to-dir="}, {"--from-dir="}, {"--from", "claude", "--to="}} {
		_, _, err := h.run(t, "", append([]string{"skills", "copy"}, args...)...)
		if err == nil || !strings.Contains(err.Error(), "takes") {
			t.Errorf("%v: %v", args, err)
		}
	}
	if !reflect.DeepEqual(before, tree(t, dir)) {
		t.Error("an empty flag wrote files")
	}
}

// fw skills push sees an edit to the second recorded copy, and reports the
// diverged skill as failed with the fix
func TestSkillsPushReportsAnEditedSecondCopy(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, ".claude", "skills", "x", "SKILL.md"), skillText("x", "Same"))
	writeTestFile(t, filepath.Join(root, ".agents", "skills", "x", "SKILL.md"), skillText("x", "Same"))
	files, err := skillsync.Scan(filepath.Join(root, ".claude", "skills", "x"))
	if err != nil {
		t.Fatal(err)
	}
	h, _ := skillsync.Hash(files)
	writeTestFile(t, filepath.Join(root, ".framework", "skills.lock.json"),
		`{"schema":1,"skills":{"k":{"paths":[".claude/skills/x",".agents/skills/x"],"variant":"stata","hash":"`+h+`"}}}`+"\n")
	writeTestFile(t, filepath.Join(root, ".agents", "skills", "x", "SKILL.md"), skillText("x", "Edited in agents"))

	pp, err := skillsync.PlanPush(skillsync.PushOptions{Root: root, Dirs: []string{".claude/skills", ".agents/skills"}})
	if err != nil {
		t.Fatal(err)
	}
	// v2: the edited copy is the one to keep, so the hint copies it over
	// the unedited one
	items := pp.Rows()
	if len(items) != 1 || items[0].Status != "failed" || !strings.Contains(items[0].Reason, "differ") ||
		items[0].Hint != `fw skills copy --from agents --to claude --skill "x"` {
		t.Errorf("items: %+v", items)
	}
}

// A Stata pull that can't look into an assistant's folder stops before it
// installs into the others
func TestSkillsPullStopsOnAnUnreadableAssistantFolder(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a folder fw can't look into")
	}
	h := newHarness(t, nil)
	dir := stataSkillsProject(t, h.app.home, "http://127.0.0.1:1", false)
	h.app.cwd = dir
	enableCodex(t, dir)
	agents := filepath.Join(dir, ".agents")
	if err := os.MkdirAll(filepath.Join(agents, "skills"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(agents, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(agents, 0o700) }) //nolint:gosec // a folder, opened again so the test can remove it
	_, _, err := h.run(t, "", "skills", "pull")
	if err == nil || !strings.Contains(err.Error(), "look up .agents/skills") {
		t.Fatalf("got %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".claude", "skills")); !os.IsNotExist(err) {
		t.Error("pull installed into .claude/skills before failing")
	}
	if _, err := os.Stat(filepath.Join(dir, ".framework", "skills.lock.json")); !os.IsNotExist(err) {
		t.Error("pull saved the lock before failing")
	}
}

// fw skills push --dry-run: a missing tracked copy fails that skill with
// the copy hint, a copy with a name sync refuses fails only that skill,
// another edit still goes through, and each problem prints once
func TestSkillsPushDivergedAndUnreadableCopies(t *testing.T) {
	c := newSkillsCloud(t)
	h := newHarness(t, map[string]string{"FW_CLOUD_TOKEN": "user-tok"})
	dir := stataSkillsProject(t, h.app.home, c.server.URL, true)
	h.app.cwd = dir
	skills := filepath.Join(dir, ".claude", "skills")
	for _, name := range []string{"x", "y", "z"} {
		writeTestFile(t, filepath.Join(skills, name, "SKILL.md"), skillText(name, "Same"))
	}
	hashOf := func(name string) string {
		files, err := skillsync.Scan(filepath.Join(skills, name))
		if err != nil {
			t.Fatal(err)
		}
		h, _ := skillsync.Hash(files)
		return h
	}
	hx, hz := hashOf("x"), hashOf("z")
	// v2: x is missing a copy beside an edited one; y can't be read; z is a
	// Cloud skill whose edit pushes
	writeTestFile(t, filepath.Join(dir, ".framework", "skills.lock.json"), `{"schema":1,"skills":{`+
		`"kx":{"paths":[".claude/skills/x",".agents/skills/x"],"variant":"stata","hash":"`+hx+`"},`+
		`"ky":{"paths":[".claude/skills/y"],"variant":"stata","hash":"`+hz+`"},`+
		`"kz":{"paths":[".claude/skills/z"],"variant":"stata","server":"`+c.server.URL+`","skill_id":"acct-1","version":1,"hash":"`+hz+`"}}}`+"\n")
	writeTestFile(t, filepath.Join(skills, "x", "SKILL.md"), skillText("x", "Edited"))
	if err := os.MkdirAll(filepath.Join(dir, ".agents", "skills"), 0o750); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(skills, "y", "my notes.md"), "a name sync refuses\n")
	writeTestFile(t, filepath.Join(skills, "z", "SKILL.md"), skillText("z", "Edited"))

	out, _, err := h.run(t, "", "skills", "push", "--dry-run")
	var exit *exitError
	if !errors.As(err, &exit) || exit.code != 1 {
		t.Fatalf("exit: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Skills pushed (dry run, nothing sent): 1 to push, 2 failed") {
		t.Errorf("summary:\n%s", out)
	}
	if n := strings.Count(out, ".agents/skills/x is missing"); n != 1 || !strings.Contains(out, `fw skills copy --from claude --to agents --skill "x"`) {
		t.Errorf("the divergence printed %d times:\n%s", n, out)
	}
	if n := strings.Count(out, "my notes.md"); n != 1 {
		t.Errorf("the bad name printed %d times:\n%s", n, out)
	}
}

// enableCodex turns Codex on next to Claude in the project's spec.
func enableCodex(t *testing.T, dir string) {
	t.Helper()
	writeTestFile(t, filepath.Join(dir, ".framework", "spec.json"),
		`{"spec_version":1,"language":"stata","type":"project","ai":{"assistants":{"claude":true,"codex":true}}}`)
}

// trackTwoCopies writes skill x into .claude and .agents with a lock entry
// for both, and returns the copies' hash.
func trackTwoCopies(t *testing.T, dir string) {
	t.Helper()
	for _, d := range []string{".claude", ".agents"} {
		writeTestFile(t, filepath.Join(dir, d, "skills", "x", "SKILL.md"), skillText("x", "Same"))
	}
	files, err := skillsync.Scan(filepath.Join(dir, ".claude", "skills", "x"))
	if err != nil {
		t.Fatal(err)
	}
	h, _ := skillsync.Hash(files)
	writeTestFile(t, filepath.Join(dir, ".framework", "skills.lock.json"),
		`{"schema":1,"skills":{"kx":{"paths":[".claude/skills/x",".agents/skills/x"],"variant":"stata","hash":"`+h+`"}}}`+"\n")
}

// Item 3: with every copy gone, fw skills push says so and exits 1
func TestSkillsPushAllCopiesGone(t *testing.T) {
	c := newSkillsCloud(t)
	h := newHarness(t, map[string]string{"FW_CLOUD_TOKEN": "user-tok"})
	dir := stataSkillsProject(t, h.app.home, c.server.URL, true)
	h.app.cwd = dir
	trackTwoCopies(t, dir)
	for _, d := range []string{".claude", ".agents"} {
		if err := os.RemoveAll(filepath.Join(dir, d, "skills", "x")); err != nil {
			t.Fatal(err)
		}
	}
	out, _, err := h.run(t, "", "skills", "push", "--dry-run")
	var exit *exitError
	if !errors.As(err, &exit) || exit.code != 1 || !strings.Contains(out, "Every tracked copy is gone.") ||
		!strings.Contains(out, "1 missing") || strings.Contains(out, "nothing to push") {
		t.Fatalf("%v\n%s", err, out)
	}
}

// Item 4: a fallback skills pull (no project token) reports a tracked
// skill it can't judge offline as unavailable (v2: offline, every tracked
// skill the package doesn't name is reported, diverged or not)
func TestSkillsPullFallbackReportsDiverged(t *testing.T) {
	h := newHarness(t, nil)
	dir := stataSkillsProject(t, h.app.home, "http://127.0.0.1:1", false)
	h.app.cwd = dir
	trackTwoCopies(t, dir)
	writeTestFile(t, filepath.Join(dir, ".agents", "skills", "x", "SKILL.md"), skillText("x", "Edited in agents"))
	out, _, err := h.run(t, "", "skills", "pull", "--dry-run")
	if err != nil || !strings.Contains(out, "1 not available") ||
		!strings.Contains(out, "Unavailable: x — Framework Cloud wasn't reached, so x was left as it is.") {
		t.Fatalf("%v\n%s", err, out)
	}
}

// Item 5: an assistant turned on only in the Cloud setup gets its skills
// folder on pull
func TestSkillsPullUsesTheCloudSetupsAssistants(t *testing.T) {
	c := newSkillsCloud(t)
	c.assistants = map[string]any{"codex": true}
	h := newHarness(t, map[string]string{"FW_CLOUD_TOKEN": "user-tok"})
	dir := stataSkillsProject(t, h.app.home, c.server.URL, true)
	h.app.cwd = dir
	if _, _, err := h.run(t, "", "skills", "pull"); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{".claude", ".agents"} {
		if _, err := os.Stat(filepath.Join(dir, d, "skills", "framework-stata", "SKILL.md")); err != nil {
			t.Errorf("%s/skills: %v", d, err)
		}
	}
}

// Item 7: fw push --yes prints a planning failure once; fw push --json has
// skills []; fw pull --yes --json reports a real run throughout
func TestSyncOutputConsistency(t *testing.T) {
	h, _, _, dir := stataSyncProject(t, map[string]string{"FW_CLOUD_TOKEN": "user-tok"})
	out, _, err := h.run(t, "", "push", "--dry-run", "--json")
	if err != nil || !strings.Contains(out, `"skills": []`) {
		t.Errorf("push --dry-run --json: %v\n%s", err, out)
	}

	out, _, err = h.run(t, "", "pull", "--yes", "--json")
	var pulled syncPullJSON
	if err != nil || json.Unmarshal([]byte(out), &pulled) != nil || pulled.DryRun || pulled.Skills == nil || pulled.Skills.DryRun {
		t.Errorf("pull --yes --json: %v\n%s", err, out)
	}
	out, _, err = h.run(t, "", "pull", "--yes", "--json")
	if err != nil || json.Unmarshal([]byte(out), &pulled) != nil || pulled.Skills == nil || pulled.Skills.DryRun {
		t.Errorf("pull --yes --json with nothing to pull: %v\n%s", err, out)
	}

	trackTwoCopies(t, dir)
	writeTestFile(t, filepath.Join(dir, ".agents", "skills", "x", "SKILL.md"), skillText("x", "Edited in agents"))
	out, stderr, err := h.run(t, "", "push", "--yes")
	if err == nil {
		t.Fatalf("push --yes succeeded:\n%s", out)
	}
	all := out + stderr + err.Error()
	if n := strings.Count(all, "has local edits. Copy it over"); n != 1 || !strings.Contains(err.Error(), "x (see above)") {
		t.Errorf("printed %d times:\n%s", n, all)
	}
}

// Item 2: with an R package that has the old skill sync (no skills_copy),
// data commands run, fw pull and fw push do only their data part and
// refuse packages and skills, and the skill commands refuse
func TestROldSkillSync(t *testing.T) {
	h := newHarness(t, map[string]string{"FW_CLOUD_TOKEN": "user-tok"})
	f := &fakeEngines{}
	f.install(h, "Rscript")
	h.app.engines.Probe = func(c engine.Command) bool { return !strings.Contains(strings.Join(c.Args, " "), `"skills_copy"`) }
	r := filepath.Join(h.app.home, "r")
	writeTestFile(t, filepath.Join(r, "settings.yml"), "default:\n  project_type: project\n")
	h.app.cwd = r
	ran := func() string { return f.ran[len(f.ran)-1].Args[len(f.ran[len(f.ran)-1].Args)-1] }

	if _, _, err := h.run(t, "", "data", "pull", "--yes"); err != nil || len(f.ran) != 1 || !strings.Contains(ran(), "pull_data_registry(") {
		t.Fatalf("fw data pull: %v %v", err, f.ran)
	}
	for _, c := range []struct{ cmd, data string }{{"pull", "pull_data_registry("}, {"push", "push_data_registry("}} {
		before := len(f.ran)
		_, stderr, err := h.run(t, "", c.cmd, "--yes")
		var exit *exitError
		if !errors.As(err, &exit) || exit.code != 1 || len(f.ran) != before+1 || !strings.Contains(ran(), c.data) ||
			!strings.Contains(err.Error(), "Done: Data registry. Not done: Packages and Skills") || !strings.Contains(err.Error(), "skills_copy()") ||
			!strings.Contains(stderr, "Packages and Skills: refused.") {
			t.Errorf("fw %s: %v %v\n%s", c.cmd, err, f.ran, stderr)
		}
		for _, cmd := range f.ran {
			if strings.Contains(strings.Join(cmd.Args, " "), "_all(") {
				t.Errorf("fw %s ran the old %s", c.cmd, cmd)
			}
		}
	}
	before := len(f.ran)
	for _, args := range [][]string{{"skills", "pull"}, {"skills", "push", "--dry-run"}} {
		if _, _, err := h.run(t, "", args...); err == nil || !strings.Contains(err.Error(), "older skill sync") {
			t.Errorf("fw %v: %v", args, err)
		}
	}
	if len(f.ran) != before {
		t.Errorf("a skill command ran R: %v", f.ran[before:])
	}
}

// Items 3 and 5, under rule J: fw skills copy itself records the copied
// folder in the skill's entry; pull then keeps it, the real run's JSON
// never says dry run, push sees both copies, and a later one-sided edit
// fails as diverged
func TestCopyRecordsTheCopiedFolder(t *testing.T) {
	h, _, _, dir := stataSyncProject(t, map[string]string{"FW_CLOUD_TOKEN": "user-tok"})
	if _, _, err := h.run(t, "", "pull", "--yes"); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(dir, ".framework", "skills.lock.json")
	writeTestFile(t, filepath.Join(dir, ".claude", "skills", "analysis", "SKILL.md"), skillText("analysis", "Edited"))
	if _, _, err := h.run(t, "", "skills", "copy", "--from", "claude", "--to", "agents", "--skill", "analysis", "--dry-run"); err != nil ||
		strings.Contains(readFile(t, lockPath), `".agents/skills/analysis"`) {
		t.Fatalf("dry run: %v", err)
	}
	if _, _, err := h.run(t, "", "skills", "copy", "--from", "claude", "--to", "agents", "--skill", "analysis"); err != nil {
		t.Fatal(err)
	}
	if lock := readFile(t, lockPath); !strings.Contains(lock, `".agents/skills/analysis"`) {
		t.Fatalf("the copy didn't record the folder:\n%s", lock)
	}
	out, _, err := h.run(t, "", "pull", "--yes", "--json")
	var res syncPullJSON
	if err != nil || json.Unmarshal([]byte(out), &res) != nil || res.Skills == nil || res.Skills.DryRun || strings.Contains(res.Skills.Message, "dry run") {
		t.Fatalf("pull: %v\n%s", err, out)
	}
	out, _, err = h.run(t, "", "skills", "push", "--dry-run")
	if err != nil || strings.Contains(out, "not tracked") || !strings.Contains(out, "1 to push") {
		t.Errorf("push: %v\n%s", err, out)
	}
	writeTestFile(t, filepath.Join(dir, ".agents", "skills", "analysis", "SKILL.md"), skillText("analysis", "Edited again, one side"))
	out, _, err = h.run(t, "", "skills", "push", "--dry-run")
	var exit *exitError
	// v2: the copy took the edited source's record, so both copies count as
	// edited, and the person picks which to keep
	if !errors.As(err, &exit) || exit.code != 1 || !strings.Contains(out, "differ, and more than one has local edits") {
		t.Errorf("one-sided edit: %v\n%s", err, out)
	}
}

// Rule J end to end, v2: turning on an assistant fills its folder with the
// unedited skills. A later one-sided edit fails push, and the hint it
// prints is a command that works: run it, and push sends the edit
func TestPrintedHintThenPullThenPush(t *testing.T) {
	h, _, _, dir := stataSyncProject(t, map[string]string{"FW_CLOUD_TOKEN": "user-tok"})
	if _, _, err := h.run(t, "", "pull", "--yes"); err != nil {
		t.Fatal(err)
	}
	enableCodex(t, dir)
	out, _, err := h.run(t, "", "skills", "pull")
	if err != nil || !strings.Contains(out, "2 added") {
		t.Fatalf("pull: %v\n%s", err, out)
	}
	out, _, err = h.run(t, "", "skills", "push", "--dry-run")
	if err != nil || strings.Contains(out, "not tracked") || !strings.Contains(out, "nothing to push") {
		t.Errorf("push: %v\n%s", err, out)
	}
	writeTestFile(t, filepath.Join(dir, ".agents", "skills", "analysis", "SKILL.md"), skillText("analysis", "One side"))
	out, _, err = h.run(t, "", "skills", "push", "--dry-run")
	var exit *exitError
	i := strings.Index(out, "fw skills copy ")
	if !errors.As(err, &exit) || exit.code != 1 || i < 0 {
		t.Fatalf("one-sided edit: %v\n%s", err, out)
	}
	hint := strings.TrimSuffix(strings.SplitN(out[i:], "\n", 2)[0], ".")
	if hint != `fw skills copy --from agents --to claude --skill "analysis"` {
		t.Fatalf("hint: %s", hint)
	}
	args := strings.Fields(strings.ReplaceAll(strings.TrimPrefix(hint, "fw "), `"`, ""))
	if _, _, err = h.run(t, "", args...); err != nil {
		t.Fatalf("%s: %v", hint, err)
	}
	out, _, err = h.run(t, "", "skills", "push", "--dry-run")
	if err != nil || !strings.Contains(out, "1 to push") {
		t.Errorf("push after the copy: %v\n%s", err, out)
	}
}

// Item 4: fw push --dry-run --json fails, and lists the skill, when its
// copies diverged, as fw skills push does
func TestPushDryRunFailsOnDivergedSkills(t *testing.T) {
	h, _, _, dir := stataSyncProject(t, map[string]string{"FW_CLOUD_TOKEN": "user-tok"})
	trackTwoCopies(t, dir)
	writeTestFile(t, filepath.Join(dir, ".agents", "skills", "x", "SKILL.md"), skillText("x", "Edited in agents"))
	out, _, err := h.run(t, "", "push", "--dry-run", "--json")
	var exit *exitError
	var res syncPushJSON
	if !errors.As(err, &exit) || exit.code != 1 || json.Unmarshal([]byte(out), &res) != nil ||
		!strings.HasPrefix(res.Failed["skills"], "x: .agents/skills/x and .claude/skills/x differ") {
		t.Errorf("%v\n%s", err, out)
	}
}

// Python (c): fw pull --json puts diverged skills in the top-level failed
// and exits 1, dry run or not, as fw push does
func TestPullFailsOnDivergedSkills(t *testing.T) {
	h, _, _, dir := stataSyncProject(t, map[string]string{"FW_CLOUD_TOKEN": "user-tok"})
	trackTwoCopies(t, dir)
	writeTestFile(t, filepath.Join(dir, ".agents", "skills", "x", "SKILL.md"), skillText("x", "Edited in agents"))
	for _, args := range [][]string{{"pull", "--dry-run", "--json"}, {"pull", "--yes", "--json"}} {
		out, _, err := h.run(t, "", args...)
		var exit *exitError
		var res syncPullJSON
		if !errors.As(err, &exit) || exit.code != 1 || json.Unmarshal([]byte(out), &res) != nil ||
			!strings.HasPrefix(res.Failed["skills"], "x: .agents/skills/x and .claude/skills/x differ") {
			t.Errorf("%v: %v\n%s", args, err, out)
		}
	}
}

// CLI 2: the only copy of a personal fork is deleted. Push says it's gone;
// pull, signed in, restores the fork itself by its own identity; not
// signed in, pull says to sign in
func TestRestoreAPersonalFork(t *testing.T) {
	c := newSkillsCloud(t)
	// Signed in, with the server chosen, as fw login leaves it
	h := newHarness(t, map[string]string{"FW_CLOUD_TOKEN": "user-tok", "FW_CLOUD_URL": c.server.URL})
	dir := stataSkillsProject(t, h.app.home, c.server.URL, true)
	h.app.cwd = dir
	mine := snapshot("own-1", 1, skillText("framework-stata", "Mine"))
	c.own = &mine
	path := filepath.Join(dir, ".claude", "skills", "framework-stata", "SKILL.md")
	writeTestFile(t, filepath.Join(dir, ".framework", "skills.lock.json"), `{"schema":1,"skills":{"package:framework-stata":{"paths":[".claude/skills/framework-stata"],`+
		`"variant":"stata","server":"`+c.server.URL+`","skill_id":"own-1","version":1,"hash":"`+mine.Hash+`","origin":{"skill_id":"framework:framework-stata","hash":"x"}}}}`+"\n")
	if err := os.MkdirAll(filepath.Join(dir, ".claude", "skills"), 0o700); err != nil {
		t.Fatal(err)
	}

	out, _, err := h.run(t, "", "skills", "push", "--dry-run")
	if err == nil || !strings.Contains(out, "Missing: framework-stata — Every tracked copy is gone.") {
		t.Fatalf("push: %v\n%s", err, out)
	}

	out, _, err = h.run(t, "", "skills", "pull")
	if err != nil || !strings.Contains(out, "1 restored") || readFile(t, path) != skillText("framework-stata", "Mine") {
		t.Fatalf("pull: %v\n%s", err, out)
	}

	// Not signed in, pull can't fetch it: missing, with what would
	if err = os.RemoveAll(filepath.Dir(path)); err != nil {
		t.Fatal(err)
	}
	h2 := newHarness(t, nil)
	h2.app.cwd = dir
	out, _, err = h2.run(t, "", "skills", "pull", "--dry-run")
	if err != nil || !strings.Contains(out, "Missing: framework-stata — Every tracked copy is gone. Sign in so pull can restore it from Framework Cloud: fw login") {
		t.Errorf("not signed in: %v\n%s", err, out)
	}
}

// runHint runs a printed fw command as a shell would.
func runHint(t *testing.T, h *harness, hint string) (string, error) {
	t.Helper()
	args := strings.Fields(strings.ReplaceAll(strings.TrimPrefix(hint, "fw "), `"`, ""))
	out, _, err := h.run(t, "", args...)
	return out, err
}

// Round 12, 2: --overwrite, as the printed hint says, resets an edited
// copy of your own Cloud skill to that skill's Cloud version, after a backup
func TestOverwriteResetsYourOwnSkill(t *testing.T) {
	c := newSkillsCloud(t)
	h := newHarness(t, map[string]string{"FW_CLOUD_TOKEN": "user-tok"})
	dir := stataSkillsProject(t, h.app.home, c.server.URL, true)
	h.app.cwd = dir
	if _, _, err := h.run(t, "", "skills", "pull"); err != nil {
		t.Fatal(err)
	}
	analysis := filepath.Join(dir, ".claude", "skills", "analysis", "SKILL.md")
	writeTestFile(t, analysis, skillText("analysis", "Mine"))
	out, _, err := h.run(t, "", "skills", "pull")
	hint := "fw skills pull --overwrite"
	if err != nil || !strings.Contains(out, "Edited: analysis — .claude/skills/analysis has local edits, which were kept. To replace them with the setup's version: "+hint) {
		t.Fatalf("%v\n%s", err, out)
	}
	if out, err = runHint(t, h, hint); err != nil || !strings.Contains(out, "1 replaced (backed up)") || readFile(t, analysis) != skillText("analysis", "First") {
		t.Errorf("%s: %v\n%s", hint, err, out)
	}
}

// Round 12, 4: a Codex-only project isn't held up by a file where
// Claude's disabled skills folder would be
func TestPullIgnoresADisabledAssistantsFolder(t *testing.T) {
	h := newHarness(t, nil)
	dir := stataSkillsProject(t, h.app.home, "http://127.0.0.1:1", false)
	h.app.cwd = dir
	writeTestFile(t, filepath.Join(dir, ".framework", "spec.json"), `{"spec_version":1,"language":"stata","type":"project","ai":{"assistants":{"codex":true}}}`)
	writeTestFile(t, filepath.Join(dir, ".claude", "skills"), "a file")
	if out, _, err := h.run(t, "", "skills", "pull"); err != nil || !strings.Contains(readFile(t, filepath.Join(dir, ".agents", "skills", "framework-stata", "SKILL.md")), "framework-stata") {
		t.Errorf("%v\n%s", err, out)
	}
}

// Round 12, 5a: after renaming .claude to .agents, push names fw skills
// move, not a pull that wouldn't restore it; run as printed, it records
// each copy where it now is, and push then has nothing missing
func TestRenamedFolderPushHintAdopts(t *testing.T) {
	c := newSkillsCloud(t)
	h := newHarness(t, map[string]string{"FW_CLOUD_TOKEN": "user-tok", "FW_CLOUD_URL": c.server.URL})
	dir := stataSkillsProject(t, h.app.home, c.server.URL, true)
	h.app.cwd = dir
	if _, _, err := h.run(t, "", "skills", "pull"); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, ".claude"), filepath.Join(dir, ".agents")); err != nil {
		t.Fatal(err)
	}
	// Switched to Codex, locally and in the Cloud setup
	writeTestFile(t, filepath.Join(dir, ".framework", "spec.json"), `{"spec_version":1,"language":"stata","type":"project","ai":{"assistants":{"codex":true}}}`)
	c.assistants = map[string]any{"claude": false, "codex": true}
	out, _, _ := h.run(t, "", "skills", "push", "--dry-run")
	if strings.Contains(out, "fw skills pull puts it back") {
		t.Errorf("push names a pull that wouldn't restore it:\n%s", out)
	}
	var hints []string
	for _, hint := range regexp.MustCompile(`fw skills move "[^"]+" "[^"]+"`).FindAllString(out, -1) {
		if !contains(hints, hint) {
			hints = append(hints, hint)
		}
	}
	if len(hints) != 2 {
		t.Fatalf("hints %v in:\n%s", hints, out)
	}
	for _, hint := range hints {
		if out, err := runHint(t, h, hint); err != nil {
			t.Fatalf("%s: %v\n%s", hint, err, out)
		}
	}
	out, _, err := h.run(t, "", "skills", "push", "--dry-run")
	if err != nil || strings.Contains(out, "missing") || strings.Contains(out, "not tracked") {
		t.Errorf("after adopting: %v\n%s", err, out)
	}
	out, _, err = h.run(t, "", "skills", "pull", "--dry-run")
	if err != nil || !strings.Contains(out, "2 up to date") {
		t.Errorf("pull after adopting: %v\n%s\n%s", err, out, readFile(t, filepath.Join(dir, ".framework", "skills.lock.json")))
	}
}

// Round 13, scenario 12: the setup names a different Cloud skill (acct-1)
// under the name the lock tracks as acct-0. Pull keeps the folder, with the
// retarget command. Moving acct-0's copy aside with fw skills move keeps
// its Cloud ID at the new folder, and pull then puts acct-1 in place
func TestMovedSkillKeepsItsCloudID(t *testing.T) {
	c := newSkillsCloud(t)
	old := snapshot("acct-0", 1, skillText("analysis", "Old"))
	c.own = &old
	h := newHarness(t, map[string]string{"FW_CLOUD_TOKEN": "user-tok", "FW_CLOUD_URL": c.server.URL})
	dir := stataSkillsProject(t, h.app.home, c.server.URL, true)
	h.app.cwd = dir
	writeTestFile(t, filepath.Join(dir, ".claude", "skills", "analysis", "SKILL.md"), skillText("analysis", "Old"))
	lockPath := filepath.Join(dir, ".framework", "skills.lock.json")
	writeTestFile(t, lockPath, `{"schema":1,"skills":{"account:`+c.server.URL+`:acct-0:stata":{"paths":[".claude/skills/analysis"],"variant":"stata",`+
		`"server":"`+c.server.URL+`","skill_id":"acct-0","version":1,"hash":"`+old.Hash+`"}}}`+"\n")

	out, _, err := h.run(t, "", "skills", "pull", "--dry-run")
	if err != nil || !strings.Contains(out, `Kept: analysis — .claude/skills/analysis is tracked as a different skill, so pull left it alone. `+
		`To track it as the setup's analysis instead: fw skills move ".claude/skills/analysis" --id "acct-1" --variant stata`) {
		t.Fatalf("no instruction: %v\n%s", err, out)
	}
	if out, _, err = h.run(t, "", "skills", "move", ".claude/skills/analysis", "kept-skills/analysis"); err != nil || !strings.Contains(out, "Moved: .claude/skills/analysis -> kept-skills/analysis") {
		t.Fatalf("move: %v\n%s", err, out)
	}
	if out, _, err = h.run(t, "", "skills", "pull"); err != nil {
		t.Fatalf("pull: %v\n%s", err, out)
	}
	lock, err := skillsync.ReadLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for _, e := range lock.Skills {
		var paths []string
		for p := range e.Copies {
			paths = append(paths, p)
		}
		ids[e.Identity.SkillID] = strings.Join(paths, ",")
	}
	if ids["acct-0"] != "kept-skills/analysis" || ids["acct-1"] != ".claude/skills/analysis" {
		t.Errorf("entries: %v", ids)
	}
	if readFile(t, filepath.Join(dir, ".claude", "skills", "analysis", "SKILL.md")) != skillText("analysis", "First") {
		t.Error("acct-1 wasn't installed")
	}
	if out, _, err = h.run(t, "", "skills", "pull", "--dry-run"); err != nil || strings.Contains(out, "acct-0") {
		t.Errorf("acct-0 still reported: %v\n%s", err, out)
	}
}

// fw skills move --id, as pull's retarget hint prints it, switches the
// copy to the setup's skill; the next pull updates the unedited copy
func TestRetargetHintThenPull(t *testing.T) {
	c := newSkillsCloud(t)
	old := snapshot("acct-0", 1, skillText("analysis", "Old"))
	c.own = &old
	h := newHarness(t, map[string]string{"FW_CLOUD_TOKEN": "user-tok", "FW_CLOUD_URL": c.server.URL})
	dir := stataSkillsProject(t, h.app.home, c.server.URL, true)
	h.app.cwd = dir
	writeTestFile(t, filepath.Join(dir, ".claude", "skills", "analysis", "SKILL.md"), skillText("analysis", "Old"))
	writeTestFile(t, filepath.Join(dir, ".framework", "skills.lock.json"), `{"schema":1,"skills":{"a":{"paths":[".claude/skills/analysis"],"variant":"stata",`+
		`"server":"`+c.server.URL+`","skill_id":"acct-0","version":1,"hash":"`+old.Hash+`"}}}`+"\n")
	out, _, err := h.run(t, "", "skills", "pull", "--dry-run")
	i := strings.Index(out, "fw skills move ")
	if err != nil || i < 0 {
		t.Fatalf("no hint: %v\n%s", err, out)
	}
	if out, err = runHint(t, h, strings.SplitN(out[i:], "\n", 2)[0]); err != nil || !strings.Contains(out, "Now tracks") {
		t.Fatalf("move: %v\n%s", err, out)
	}
	if out, _, err = h.run(t, "", "skills", "pull"); err != nil || !strings.Contains(out, "1 updated") ||
		readFile(t, filepath.Join(dir, ".claude", "skills", "analysis", "SKILL.md")) != skillText("analysis", "First") {
		t.Fatalf("pull: %v\n%s", err, out)
	}
}
