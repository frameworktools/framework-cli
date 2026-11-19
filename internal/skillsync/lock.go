package skillsync

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// LockSchema is the skills lock schema this version of fw writes. It reads
// schema 1 too, and migrates it in memory.
const LockSchema = 2

const lockFile = ".framework/skills.lock.json"

// Identity kinds.
const (
	KindBuiltin = "builtin"
	KindCloud   = "cloud"
	KindInline  = "inline"
	KindLocal   = "local"
)

// Channels: how a version arrived.
const (
	ChannelPackage = "package"
	ChannelCloud   = "cloud"
	ChannelInline  = "inline"
)

// Identity is what a skill is, apart from where its copies live.
type Identity struct {
	Kind    string `json:"kind"`
	SkillID string `json:"skill_id,omitempty"`
	Variant string `json:"variant"`
	Server  string `json:"server,omitempty"`
}

// Key is the lock key of an identity. A local entry's key names the first
// path it tracked, which the caller passes.
func (i Identity) Key(firstPath string) string {
	switch i.Kind {
	case KindCloud:
		return i.Server + "/" + i.SkillID + "@" + i.Variant
	case KindLocal:
		return "local:" + firstPath
	}
	return i.SkillID + "@" + i.Variant
}

// Base is the source version a sync last wrote: what push sends as its base.
type Base struct {
	Channel string
	Hash    string
	// Version is the Cloud version, 0 (null) for package and inline.
	Version int
	// Server delivered a cloud channel version; "" (null) otherwise.
	Server string
}

func (b Base) MarshalJSON() ([]byte, error) {
	var version *int
	var server *string
	if b.Version != 0 {
		version = &b.Version
	}
	if b.Server != "" {
		server = &b.Server
	}
	return json.Marshal(struct {
		Channel string  `json:"channel"`
		Hash    string  `json:"hash"`
		Version *int    `json:"version"`
		Server  *string `json:"server"`
	}{b.Channel, b.Hash, version, server})
}

func (b *Base) UnmarshalJSON(data []byte) error {
	var raw struct {
		Channel string  `json:"channel"`
		Hash    string  `json:"hash"`
		Version *int    `json:"version"`
		Server  *string `json:"server"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*b = Base{Channel: raw.Channel, Hash: raw.Hash}
	if raw.Version != nil {
		b.Version = *raw.Version
	}
	if raw.Server != nil {
		b.Server = strings.TrimRight(*raw.Server, "/")
	}
	return nil
}

// Record is the hash a sync last wrote at one copy's path. "" is no
// record: the copy counts as edited.
type Record struct{ Hash string }

func (r Record) MarshalJSON() ([]byte, error) {
	if r.Hash == "" {
		return []byte(`{"hash":null}`), nil
	}
	return json.Marshal(map[string]string{"hash": r.Hash})
}

func (r *Record) UnmarshalJSON(data []byte) error {
	var raw struct {
		Hash *string `json:"hash"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	r.Hash = ""
	if raw.Hash != nil {
		r.Hash = *raw.Hash
	}
	return nil
}

// LockEntry is one skill identity in the lock, with its copies.
type LockEntry struct {
	Identity Identity          `json:"identity"`
	Base     *Base             `json:"base"`
	Known    []string          `json:"known"`
	Copies   map[string]Record `json:"copies"`
	Pending  *Pending          `json:"pending,omitempty"`
}

// Lock is .framework/skills.lock.json, schema 2.
type Lock struct {
	Schema int                   `json:"schema"`
	Skills map[string]*LockEntry `json:"skills"`
}

// LockSchemaError is a lock written by a newer client.
type LockSchemaError struct{ Schema int }

func (e *LockSchemaError) Error() string {
	return fmt.Sprintf("This project's skills lock (%s) uses schema %d, which this version of fw can't read. Update fw to the latest release.", lockFile, e.Schema)
}

// ReasonCode is the shared error code.
func (e *LockSchemaError) ReasonCode() string { return "lock_schema_unsupported" }

// newLock is an empty lock.
func newLock() Lock { return Lock{Schema: LockSchema, Skills: map[string]*LockEntry{}} }

func (e *LockEntry) clone() *LockEntry {
	c := *e
	if e.Base != nil {
		b := *e.Base
		c.Base = &b
	}
	if e.Pending != nil {
		p := *e.Pending
		c.Pending = &p
	}
	c.Known = append([]string{}, e.Known...)
	c.Copies = make(map[string]Record, len(e.Copies))
	for p, r := range e.Copies {
		c.Copies[p] = r
	}
	return &c
}

func (l Lock) clone() Lock {
	c := Lock{Schema: LockSchema, Skills: make(map[string]*LockEntry, len(l.Skills))}
	for k, e := range l.Skills {
		c.Skills[k] = e.clone()
	}
	return c
}

// keys lists the lock's keys, sorted.
func (l Lock) keys() []string {
	keys := make([]string, 0, len(l.Skills))
	for k := range l.Skills {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// paths lists an entry's copy paths, sorted by byte value.
func (e *LockEntry) paths() []string {
	out := make([]string, 0, len(e.Copies))
	for p := range e.Copies {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// sortedUnique sorts hashes by byte value and drops duplicates and blanks.
func sortedUnique(lists ...[]string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, list := range lists {
		for _, h := range list {
			if h != "" && !seen[h] {
				seen[h] = true
				out = append(out, h)
			}
		}
	}
	sort.Strings(out)
	return out
}

// equal reports whether two locks hold the same entries.
func (l Lock) equal(o Lock) bool {
	a, _ := json.Marshal(l.Skills)
	b, _ := json.Marshal(o.Skills)
	return bytes.Equal(a, b)
}

// v1Lock and v1Entry are schema 1, read only to migrate.
type v1Lock struct {
	Schema int                 `json:"schema"`
	Skills map[string]*v1Entry `json:"skills"`
}

type v1Entry struct {
	Paths   []string `json:"paths"`
	Variant string   `json:"variant"`
	Server  string   `json:"server,omitempty"`
	ID      string   `json:"skill_id,omitempty"`
	Version int      `json:"version,omitempty"`
	Hash    string   `json:"hash,omitempty"`
	Origin  *struct {
		ID   string `json:"skill_id"`
		Hash string `json:"hash"`
	} `json:"origin,omitempty"`
	Pending *Pending `json:"pending,omitempty"`
}

// parseLock reads a lock file's bytes as schema 2, migrating schema 1.
func parseLock(data []byte) (Lock, error) {
	var head struct {
		Schema *int `json:"schema"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return Lock{}, fmt.Errorf("%s: %w", lockFile, err)
	}
	switch {
	case head.Schema == nil || *head.Schema < 1:
		return Lock{}, fmt.Errorf("unsupported skill lock file")
	case *head.Schema > LockSchema:
		return Lock{}, &LockSchemaError{Schema: *head.Schema}
	case *head.Schema == 1:
		var v1 v1Lock
		if err := json.Unmarshal(data, &v1); err != nil || v1.Skills == nil {
			return Lock{}, fmt.Errorf("unsupported skill lock file")
		}
		return migrate(v1), nil
	}
	var lock Lock
	if err := json.Unmarshal(data, &lock); err != nil || lock.Skills == nil {
		return Lock{}, fmt.Errorf("unsupported skill lock file")
	}
	return normalize(lock), nil
}

// migrate maps a schema 1 lock to schema 2. It is pure, and never rejects a
// v1 lock.
func migrate(v1 v1Lock) Lock {
	mergeV1(&v1)
	lock := newLock()
	for _, k := range sortedKeys(v1.Skills) {
		e := v1.Skills[k]
		variant := e.Variant
		if variant == "" {
			variant = "r"
		}
		n := &LockEntry{Known: []string{}, Copies: map[string]Record{}, Pending: e.Pending}
		originHash := ""
		if e.Origin != nil {
			originHash = e.Origin.Hash
		}
		switch {
		case strings.HasPrefix(e.ID, "framework:"):
			n.Identity = Identity{Kind: KindBuiltin, SkillID: e.ID, Variant: variant}
			n.Base = &Base{Channel: ChannelCloud, Hash: e.Hash, Version: e.Version, Server: strings.TrimRight(e.Server, "/")}
		case e.ID != "":
			n.Identity = Identity{Kind: KindCloud, SkillID: e.ID, Variant: variant, Server: strings.TrimRight(e.Server, "/")}
			n.Base = &Base{Channel: ChannelCloud, Hash: e.Hash, Version: e.Version, Server: strings.TrimRight(e.Server, "/")}
		case e.Origin != nil && strings.HasPrefix(e.Origin.ID, "framework:"):
			n.Identity = Identity{Kind: KindBuiltin, SkillID: e.Origin.ID, Variant: variant}
			n.Base = &Base{Channel: ChannelPackage, Hash: originHash}
		default:
			n.Identity = Identity{Kind: KindLocal, Variant: variant}
		}
		if n.Base != nil && n.Base.Hash == "" {
			n.Base = nil
		}
		record := e.Hash
		if record == "" {
			record = originHash
		}
		for _, p := range e.Paths {
			n.Copies[p] = Record{Hash: record}
		}
		n.Known = sortedUnique([]string{e.Hash, originHash})
		first := ""
		if len(e.Paths) > 0 {
			first = e.Paths[0]
		}
		key := n.Identity.Key(first)
		if n.Identity.Kind == KindLocal && first == "" {
			key = "local:" + k
		}
		addEntry(&lock, key, n)
	}
	return lock
}

// normalize rebuilds a schema 2 lock's keys from its identities, merging
// any two entries that turn out to be one identity.
func normalize(in Lock) Lock {
	lock := newLock()
	for _, k := range in.keys() {
		e := in.Skills[k]
		if e == nil {
			continue
		}
		if e.Copies == nil {
			e.Copies = map[string]Record{}
		}
		e.Known = sortedUnique(e.Known)
		if e.Identity.Variant == "" {
			e.Identity.Variant = "r"
		}
		e.Identity.Server = strings.TrimRight(e.Identity.Server, "/")
		key := k
		if e.Identity.Kind == KindLocal {
			if !strings.HasPrefix(k, "local:") {
				first := ""
				if ps := e.paths(); len(ps) > 0 {
					first = ps[0]
				}
				key = "local:" + first
			}
		} else {
			key = e.Identity.Key("")
		}
		addEntry(&lock, key, e)
	}
	return lock
}

// addEntry adds e under key, merging it into an entry already there.
func addEntry(lock *Lock, key string, e *LockEntry) {
	have, ok := lock.Skills[key]
	if !ok {
		lock.Skills[key] = e
		return
	}
	for p, r := range e.Copies {
		if _, taken := have.Copies[p]; !taken {
			have.Copies[p] = r
		}
	}
	have.Known = sortedUnique(have.Known, e.Known)
	if have.Base == nil || (e.Base != nil && e.Base.Version > have.Base.Version) {
		have.Base = e.Base
	}
	if have.Pending == nil {
		have.Pending = e.Pending
	}
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// mergeV1 joins v1 entries that are one skill split in two: the same
// server, skill_id, and variant, or for a built-in without an ID the same
// name, origin, and variant. The merged entry has every path, and the
// newest version's record.
func mergeV1(state *v1Lock) {
	identity := func(e *v1Entry) string {
		switch {
		case e.ID != "":
			return "id\x00" + e.Server + "\x00" + e.ID + "\x00" + e.Variant
		case e.Origin != nil && len(e.Paths) > 0:
			return "origin\x00" + baseName(e.Paths[0]) + "\x00" + e.Origin.ID + "\x00" + e.Variant
		}
		return ""
	}
	first := map[string]string{}
	for _, k := range sortedKeys(state.Skills) {
		e := state.Skills[k]
		id := identity(e)
		if id == "" {
			continue
		}
		into, seen := first[id]
		if !seen {
			first[id] = k
			continue
		}
		kept := state.Skills[into]
		paths := kept.Paths
		for _, p := range e.Paths {
			if !contains(paths, p) {
				paths = append(paths, p)
			}
		}
		if e.Version > kept.Version {
			kept = e
			state.Skills[into] = e
		}
		kept.Paths = paths
		delete(state.Skills, k)
	}
}

func baseName(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

// loadLock reads the project's lock, migrated to schema 2. No file is an
// empty lock. raw is the file as read, nil when there is none.
func loadLock(root string) (Lock, []byte, error) {
	path, err := safePath(root, lockFile)
	if err != nil {
		return Lock{}, nil, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return newLock(), nil, nil
	}
	if err != nil {
		return Lock{}, nil, err
	}
	lock, err := parseLock(data)
	return lock, data, err
}

// ReadLock reads the project's skills lock as schema 2.
func ReadLock(root string) (Lock, error) {
	lock, _, err := loadLock(root)
	return lock, err
}

// saveLock writes the lock as schema 2, atomically.
func saveLock(root string, lock Lock) error {
	path, err := safePath(root, lockFile)
	if err != nil {
		return err
	}
	lock.Schema = LockSchema
	for _, e := range lock.Skills {
		e.Known = sortedUnique(e.Known)
		if e.Copies == nil {
			e.Copies = map[string]Record{}
		}
	}
	data, err := json.MarshalIndent(lock, "", "  ")
	if err != nil {
		return err
	}
	if err = private(root); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".skill-state-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(append(data, '\n')); err != nil {
		return errors.Join(err, f.Close())
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

// saveIfChanged writes after when it differs from before. A lock that
// comes out unchanged is not rewritten, so a schema 1 file stays as it is.
func saveIfChanged(root string, before, after Lock) (bool, error) {
	if before.equal(after) {
		return false, nil
	}
	return true, saveLock(root, after)
}

// prune drops entries with no copies, except cloud entries, which stay so
// a move can attach a copy to them later.
func (l Lock) prune() {
	for k, e := range l.Skills {
		if len(e.Copies) == 0 && e.Identity.Kind != KindCloud {
			delete(l.Skills, k)
		}
	}
}
