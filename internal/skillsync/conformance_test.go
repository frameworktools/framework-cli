package skillsync

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// The shared skill sync scenarios (skills lock v2), copied from the R
// package with tools/syncassets. Each case builds a project, runs the
// command with Cloud mocked, and checks every field: the error, the steps,
// the rows, the disk, the backups, and the lock.
const scenarioDir = "../spec/testdata/conformance/skills"

// overrideDir holds local replacements for scenarios fw disagrees with,
// each named as the case it replaces. Every override is listed, with its
// reason, in the release notes of the change that adds it.
const overrideDir = "testdata/conformance-overrides"

// scenarioServer is the Cloud server the scenarios name.
const scenarioServer = "https://cloud.test"

type scenario struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	Command     string `json:"command"`
	Options     struct {
		Overwrite bool   `json:"overwrite"`
		DryRun    bool   `json:"dry_run"`
		Offline   bool   `json:"offline"`
		Confirm   *bool  `json:"confirm"`
		Force     bool   `json:"force"`
		From      string `json:"from"`
		To        string `json:"to"`
		SkillID   string `json:"skill_id"`
		Variant   string `json:"variant"`
		Builtin   string `json:"builtin"`
	} `json:"options"`
	Project struct {
		Variant string   `json:"variant"`
		Dirs    []string `json:"dirs"`
	} `json:"project"`
	Folders  []string          `json:"folders"`
	Disk     map[string]string `json:"disk"`
	Lock     json.RawMessage   `json:"lock"`
	Offer    scenarioOffer     `json:"offer"`
	Expected struct {
		Error   *string           `json:"error"`
		Steps   []any             `json:"steps"`
		Rows    []any             `json:"rows"`
		Disk    map[string]string `json:"disk"`
		Backups []string          `json:"backups"`
		Lock    json.RawMessage   `json:"lock"`
		Failed  []string          `json:"failed"`
	} `json:"expected"`
}

type scenarioSource struct {
	Name     string   `json:"name"`
	Identity Identity `json:"identity"`
	Channel  string   `json:"channel"`
	Version  *int     `json:"version"`
	Server   *string  `json:"server"`
	Content  *string  `json:"content"`
	Hash     string   `json:"hash"`
	Known    []string `json:"known"`
}

type scenarioOffer struct {
	Source     string                    `json:"source"`
	Skills     []scenarioSource          `json:"skills"`
	ByIdentity map[string]scenarioSource `json:"by_identity"`
	Remote     map[string]struct {
		Version int    `json:"version"`
		Hash    string `json:"hash"`
	} `json:"remote"`
	PushResponse map[string]struct {
		SkillID string `json:"skill_id"`
		Version int    `json:"version"`
		Hash    string `json:"hash"`
		Server  string `json:"server"`
	} `json:"push_response"`
}

// labelText is the SKILL.md a content label stands for.
func labelText(label string) string {
	return "---\nname: fixture\ndescription: Skill scenario fixture.\n---\n\n" + label + "\n"
}

// labels maps content labels to bundle hashes and back.
type labels struct{ hash, label map[string]string }

func (l *labels) of(label string) string {
	if h, ok := l.hash[label]; ok {
		return h
	}
	h, err := Hash(BundleFromText(labelText(label), nil))
	if err != nil {
		panic(err)
	}
	l.hash[label], l.label[h] = h, label
	return h
}

// substitute replaces every "@label" string with that label's hash.
func (l *labels) substitute(raw []byte) []byte {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		panic(err)
	}
	var walk func(any) any
	walk = func(x any) any {
		switch t := x.(type) {
		case string:
			if strings.HasPrefix(t, "@") {
				return l.of(t[1:])
			}
		case []any:
			for i := range t {
				t[i] = walk(t[i])
			}
		case map[string]any:
			for k := range t {
				t[k] = walk(t[k])
			}
		}
		return x
	}
	out, _ := json.Marshal(walk(v))
	return out
}

func loadScenarios(t *testing.T) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	for _, dir := range []string{scenarioDir, overrideDir} {
		found, err := filepath.Glob(filepath.Join(dir, "*.json"))
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range found {
			data, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			out[strings.TrimSuffix(filepath.Base(f), ".json")] = data
		}
	}
	return out
}

func TestSkillScenarios(t *testing.T) {
	cases := loadScenarios(t)
	if len(cases) < 63 {
		t.Fatalf("found %d scenarios in %s; copy them with tools/syncassets", len(cases), scenarioDir)
	}
	names := sortedKeys(cases)
	for _, name := range names {
		raw := cases[name]
		t.Run(name, func(t *testing.T) {
			l := &labels{hash: map[string]string{}, label: map[string]string{}}
			var sc scenario
			if err := json.Unmarshal(l.substitute(raw), &sc); err != nil {
				t.Fatal(err)
			}
			runScenario(t, l, sc)
		})
	}
}

func (l *labels) source(s scenarioSource) Source {
	src := Source{Name: s.Name, Identity: s.Identity, Channel: s.Channel, Hash: s.Hash, Known: s.Known}
	if s.Version != nil {
		src.Version = *s.Version
	}
	if s.Server != nil {
		src.Server = *s.Server
	}
	if s.Content != nil {
		src.Bundle = BundleFromText(labelText(*s.Content), nil)
		l.of(*s.Content)
	}
	return src
}

// assistantDir is a scenario's folder option: an assistant name, or a path.
func assistantDir(s string) string {
	if d := AssistantSkillsDir(s); d != "" {
		return d
	}
	return s
}

func runScenario(t *testing.T, l *labels, sc scenario) {
	root := t.TempDir()
	for _, f := range sc.Folders {
		check(t, os.MkdirAll(filepath.Join(root, filepath.FromSlash(f)), 0755))
	}
	for p, label := range sc.Disk {
		dir := filepath.Join(root, filepath.FromSlash(p))
		check(t, os.MkdirAll(dir, 0755))
		if label == "!unreadable" {
			check(t, os.WriteFile(filepath.Join(dir, "README.txt"), []byte("not a skill\n"), 0644))
			continue
		}
		l.of(label)
		check(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(labelText(label)), 0644))
	}
	lockPath := filepath.Join(root, ".framework", "skills.lock.json")
	var lockBefore []byte
	if len(sc.Lock) > 0 && string(sc.Lock) != "null" {
		var v any
		check(t, json.Unmarshal(sc.Lock, &v))
		lockBefore, _ = json.MarshalIndent(v, "", "  ")
		check(t, os.MkdirAll(filepath.Dir(lockPath), 0755))
		check(t, os.WriteFile(lockPath, lockBefore, 0644))
	}
	offer := Offer{Source: sc.Offer.Source, ByIdentity: map[string]Source{}, Remote: map[string]Remote{}}
	for _, s := range sc.Offer.Skills {
		offer.Skills = append(offer.Skills, l.source(s))
	}
	for k, s := range sc.Offer.ByIdentity {
		offer.ByIdentity[k] = l.source(s)
	}
	for k, r := range sc.Offer.Remote {
		offer.Remote[k] = Remote{Version: r.Version, Hash: r.Hash}
	}

	var steps []Step
	var rows []Row
	var backups, failed []string
	var err error
	o := sc.Options
	switch sc.Command {
	case "pull", "pull_all", "upgrade":
		command := CommandPull
		if sc.Command == "upgrade" {
			command = CommandUpgrade
		}
		var res PullResult
		res, err = Pull(PullOptions{Root: root, Dirs: sc.Project.Dirs, Offer: offer, Command: command,
			Overwrite: o.Overwrite, DryRun: o.DryRun, Offline: o.Offline})
		steps, rows = res.Steps, res.Rows
		failed = []string{}
		for _, r := range rows {
			if r.Status == StatusDiverged || r.Status == StatusMissing || r.Status == StatusFailed {
				failed = append(failed, r.Name)
			}
		}
	case "copy":
		var res CopyResult
		res, err = Copy(CopyOptions{Root: root, From: assistantDir(o.From), To: []string{assistantDir(o.To)}, DryRun: o.DryRun})
		steps, rows = res.Steps, buildRows(res.Steps, CommandCopy)
		for _, it := range res.Skills {
			if it.Backup != "" {
				backups = append(backups, it.To)
			}
		}
	case "move":
		to := o.To
		if to != "" {
			to = assistantDir(to)
		}
		var res MoveResult
		res, err = Move(MoveOptions{Root: root, From: assistantDir(o.From), To: to, SkillID: o.SkillID, Builtin: o.Builtin,
			Variant: o.Variant, Server: scenarioServer, DryRun: o.DryRun})
		steps, rows = res.Steps, res.Rows
	case "push", "push_all":
		var pp *PushPlan
		pp, err = PlanPush(PushOptions{Root: root, Dirs: sc.Project.Dirs, Force: o.Force,
			Remote: func(key string, _ LockEntry) (Remote, bool, error) {
				r, ok := offer.Remote[key]
				return r, ok, nil
			}})
		if err == nil {
			if pp.Acts() && !o.DryRun {
				if pp.Pending() && o.Confirm != nil && !*o.Confirm {
					pp.Cancel()
				} else {
					err = pp.Apply(scenarioServer, func(req PushRequest) (Snapshot, error) {
						r, ok := sc.Offer.PushResponse[req.Key]
						if !ok {
							return Snapshot{}, fmt.Errorf("no push response for %s", req.Key)
						}
						return Snapshot{ID: r.SkillID, Version: r.Version, Hash: r.Hash}, nil
					})
				}
			}
			steps = pp.Steps
			rows = buildRows(steps, CommandPush)
			failed = []string{}
			for _, r := range rows {
				if r.Status == StatusFailed || r.Status == StatusMissing {
					failed = append(failed, r.Name)
				}
			}
		}
	default:
		t.Fatalf("unknown command %s", sc.Command)
	}
	if sc.Command != "copy" {
		for _, s := range steps {
			if (s.Action == ActionWrite || s.Action == ActionCopy) && s.Backup && !o.DryRun {
				backups = append(backups, s.Path)
			}
		}
	}

	if sc.Expected.Error != nil {
		code := ""
		var pe *PlanError
		var se *LockSchemaError
		switch {
		case errors.As(err, &pe):
			code = pe.Code
		case errors.As(err, &se):
			code = se.ReasonCode()
		}
		if code != *sc.Expected.Error {
			t.Errorf("error: got %v (code %q), want %s", err, code, *sc.Expected.Error)
		}
	} else if err != nil {
		t.Fatalf("error: %v", err)
	}
	if sc.Expected.Error == nil {
		compareJSON(t, "steps", l.stepsJSON(steps), sc.Expected.Steps, stepOrder)
		compareJSON(t, "rows", rowsJSON(rows), sc.Expected.Rows, rowOrder)
	}

	// Disk: every skill folder after the command
	if got := l.diskLabels(t, root); !reflect.DeepEqual(got, orEmpty(sc.Expected.Disk)) {
		t.Errorf("disk:\n got %v\nwant %v", got, sc.Expected.Disk)
	}
	sort.Strings(backups)
	want := append([]string{}, sc.Expected.Backups...)
	sort.Strings(want)
	if strings.Join(backups, ",") != strings.Join(want, ",") {
		t.Errorf("backups: got %v, want %v", backups, want)
	}
	if n := len(entriesIn(t, root, ".framework/skills-backups")); n != len(want) {
		t.Errorf("backup folders: %d, want %d", n, len(want))
	}
	if sc.Expected.Failed != nil && strings.Join(failed, ",") != strings.Join(sc.Expected.Failed, ",") {
		t.Errorf("failed: got %v, want %v", failed, sc.Expected.Failed)
	}

	after, readErr := os.ReadFile(lockPath)
	if string(sc.Expected.Lock) == `"unchanged"` {
		if lockBefore == nil && readErr == nil {
			t.Errorf("lock: written, want no lock file:\n%s", after)
		} else if lockBefore != nil && string(after) != string(lockBefore) {
			t.Errorf("lock: rewritten, want unchanged:\n%s", after)
		}
		return
	}
	if readErr != nil {
		t.Fatalf("lock: %v", readErr)
	}
	var got, exp any
	check(t, json.Unmarshal(after, &got))
	check(t, json.Unmarshal(sc.Expected.Lock, &exp))
	if !reflect.DeepEqual(normalizeLock(got), normalizeLock(exp)) {
		g, _ := json.MarshalIndent(got, "", "  ")
		e, _ := json.MarshalIndent(exp, "", "  ")
		t.Errorf("lock:\n got %s\nwant %s", l.relabel(string(g)), l.relabel(string(e)))
	}
}

func orEmpty(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

// relabel shows hashes as their labels, for readable failures.
func (l *labels) relabel(s string) string {
	for h, label := range l.label {
		s = strings.ReplaceAll(s, h, "@"+label)
	}
	return s
}

// diskLabels lists every skill folder in the project as a content label.
func (l *labels) diskLabels(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if d.IsDir() && (rel == ".framework" || strings.HasPrefix(d.Name(), ".skill-")) {
			return filepath.SkipDir
		}
		if !d.IsDir() {
			return nil
		}
		if _, err := os.Stat(filepath.Join(p, "SKILL.md")); err == nil {
			files, err := Scan(p)
			if err != nil {
				t.Errorf("%s: %v", rel, err)
				return nil
			}
			h, _ := Hash(files)
			label, ok := l.label[h]
			if !ok {
				label = "?" + h
			}
			out[rel] = label
			return filepath.SkipDir
		}
		if _, err := os.Stat(filepath.Join(p, "README.txt")); err == nil {
			out[rel] = "!unreadable"
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func (l *labels) stepsJSON(steps []Step) []any {
	out := []any{}
	for _, s := range steps {
		m := map[string]any{"path": s.Path, "entry": nilIfEmpty(s.Entry), "name": s.Name, "action": s.Action,
			"status": s.Status, "reason_code": nilIfEmpty(s.Reason), "hint": hintJSON(s.Hint)}
		if s.Action == ActionWrite {
			m["content"] = l.label[s.Hash]
		}
		if s.From != "" {
			m["from"] = s.From
		}
		if s.Backup {
			m["backup"] = true
		}
		out = append(out, m)
	}
	return out
}

func rowsJSON(rows []Row) []any {
	out := []any{}
	for _, r := range rows {
		paths := append([]string{}, r.Paths...)
		out = append(out, map[string]any{"name": r.Name, "status": r.Status, "paths": paths,
			"reason_code": nilIfEmpty(r.Reason), "hint": hintJSON(r.Hint)})
	}
	return out
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func hintJSON(h *Hint) any {
	if h == nil {
		return nil
	}
	data, _ := json.Marshal(h)
	var v any
	_ = json.Unmarshal(data, &v)
	return v
}

func stepOrder(v any) string { return v.(map[string]any)["path"].(string) }

func rowOrder(v any) string {
	m := v.(map[string]any)
	return fmt.Sprint(m["name"], "\x00", m["paths"])
}

// compareJSON compares two lists as parsed JSON, sorted by key, with every
// "paths", "to", and "known" list sorted.
func compareJSON(t *testing.T, what string, got []any, want []any, key func(any) string) {
	t.Helper()
	norm := func(list []any) []any {
		data, _ := json.Marshal(list)
		var out []any
		_ = json.Unmarshal(data, &out)
		if out == nil {
			out = []any{}
		}
		for i := range out {
			out[i] = sortLists(out[i])
		}
		sort.SliceStable(out, func(i, j int) bool { return key(out[i]) < key(out[j]) })
		return out
	}
	g, w := norm(got), norm(want)
	if !reflect.DeepEqual(g, w) {
		a, _ := json.MarshalIndent(g, "", "  ")
		b, _ := json.MarshalIndent(w, "", "  ")
		t.Errorf("%s:\n got %s\nwant %s", what, a, b)
	}
}

// sortLists sorts every "paths", "to", and "known" list in v.
func sortLists(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, x := range t {
			if list, ok := x.([]any); ok && (k == "paths" || k == "to" || k == "known") {
				sort.Slice(list, func(i, j int) bool { return fmt.Sprint(list[i]) < fmt.Sprint(list[j]) })
			}
			t[k] = sortLists(t[k])
		}
	case []any:
		for i := range t {
			t[i] = sortLists(t[i])
		}
	}
	return v
}

func normalizeLock(v any) any { return sortLists(v) }
