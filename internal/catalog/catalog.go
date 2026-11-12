// Package catalog reads and edits a project's data catalog: the data
// section of settings.yml, whose dot-notation names (inputs.raw.survey) map
// to data files. fw data pull and push move its entries to and from
// Framework Cloud's data registry. Only the definitions move; the files and
// their hashes stay local.
//
// The R package's R/data_registry.R and framework-py's data_registry.py
// follow the same rules.
package catalog

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Fields are the entry fields that travel to and from Framework Cloud, in
// the order they are written. Any other field of a local entry stays local.
var Fields = []string{"path", "type", "delimiter", "locked", "private", "description", "stata_names"}

// Types are the file types data_read() reads, with the aliases it takes.
var Types = []string{
	"csv", "tsv", "rds", "excel", "xlsx", "xls", "parquet", "stata", "dta",
	"spss", "sav", "zsav", "spss_por", "por", "sas", "sas7bdat", "sas7bcat", "sas_xpt", "xpt",
}

// Delimiters are the named delimiters for delimited text.
var Delimiters = []string{"comma", "tab", "semicolon", "space"}

// NamePattern is a dot-notation catalog name.
var NamePattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_-]*([.][A-Za-z0-9_][A-Za-z0-9_-]*)*$`)

// Row is one catalog entry as the registry API carries it.
type Row struct {
	Name        string            `json:"name"`
	Path        string            `json:"path"`
	Type        *string           `json:"type,omitempty"`
	Delimiter   *string           `json:"delimiter,omitempty"`
	Locked      bool              `json:"locked"`
	Private     *bool             `json:"private,omitempty"`
	Description *string           `json:"description,omitempty"`
	StataNames  map[string]string `json:"stata_names,omitempty"`
}

// Same reports whether two rows define the entry the same way.
func Same(a, b Row) bool { return key(a) == key(b) }

func key(r Row) string {
	s := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	priv := ""
	if r.Private != nil {
		priv = fmt.Sprint(*r.Private)
	}
	names := make([]string, 0, len(r.StataNames))
	for k, v := range r.StataNames {
		names = append(names, k+"="+v)
	}
	sort.Strings(names)
	return strings.Join([]string{r.Path, s(r.Type), s(r.Delimiter), fmt.Sprint(r.Locked), priv, s(r.Description), strings.Join(names, ";")}, "\x00")
}

// Diff lists how to differs from from, one item per field: "path a -> b"
// for the short fields, and "description changed" or "stata_names changed"
// for the long ones, so two definitions that Describe the same way still
// show what changed.
func Diff(from, to Row) string {
	a := strings.Split(key(from), "\x00")
	b := strings.Split(key(to), "\x00")
	var parts []string
	for i, field := range Fields {
		if a[i] == b[i] {
			continue
		}
		if field == "description" || field == "stata_names" {
			parts = append(parts, field+" changed")
			continue
		}
		parts = append(parts, fmt.Sprintf("%s %s -> %s", field, orNone(a[i]), orNone(b[i])))
	}
	return strings.Join(parts, ", ")
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

// Problems lists what is wrong with a spec's data section (name -> entry),
// as R's spec_check() reports it.
func Problems(data map[string]any) []string {
	var out []string
	names := make([]string, 0, len(data))
	for n := range data {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		where := "data." + name
		if !NamePattern.MatchString(name) {
			out = append(out, fmt.Sprintf("data: invalid catalog name '%s'", name))
		}
		entry, ok := data[name].(map[string]any)
		if !ok {
			out = append(out, where+": must be an entry with a path")
			continue
		}
		for k := range entry {
			if !contains(Fields, k) {
				out = append(out, fmt.Sprintf("%s: unknown field '%s'", where, k))
			}
		}
		path, _ := entry["path"].(string)
		switch {
		case path == "":
			out = append(out, where+": missing path")
		case !safePath(path):
			out = append(out, fmt.Sprintf("%s: path '%s' must be relative and stay inside the project", where, path))
		}
		if v, has := entry["type"]; has {
			if s, ok := v.(string); !ok || !contains(Types, s) {
				out = append(out, where+": type must be one of "+strings.Join(Types, ", "))
			}
		}
		if v, has := entry["delimiter"]; has {
			if s, ok := v.(string); !ok || !contains(Delimiters, s) {
				out = append(out, where+": delimiter must be comma, tab, semicolon, or space")
			}
		}
		for _, flag := range []string{"locked", "private"} {
			if v, has := entry[flag]; has {
				if _, ok := v.(bool); !ok {
					out = append(out, fmt.Sprintf("%s.%s must be true or false", where, flag))
				}
			}
		}
	}
	// A name that is also a group of other names can't be written nested
	for _, name := range names {
		for _, other := range names {
			if strings.HasPrefix(other, name+".") {
				out = append(out, fmt.Sprintf("data: '%s' is an entry, so '%s' can't be inside it", name, other))
				break
			}
		}
	}
	return out
}

func safePath(p string) bool {
	if strings.HasPrefix(p, "/") || strings.HasPrefix(p, "~") || strings.Contains(p, `\`) || regexp.MustCompile(`^[A-Za-z]:`).MatchString(p) {
		return false
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return false
		}
	}
	return true
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// Nest turns a spec's data section into the nested catalog the settings
// hold, as a YAML mapping node with names in order: inputs.raw.survey
// becomes inputs: raw: survey:.
func Nest(data map[string]any) *yaml.Node {
	root := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	names := make([]string, 0, len(data))
	for n := range data {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		entry, ok := data[name].(map[string]any)
		if !ok {
			continue
		}
		setEntry(root, strings.Split(name, "."), entryNode(nil, rowFromMap(name, entry)))
	}
	return root
}

func rowFromMap(name string, m map[string]any) Row {
	r := Row{Name: name}
	r.Path, _ = m["path"].(string)
	if s, ok := m["type"].(string); ok {
		r.Type = &s
	}
	if s, ok := m["delimiter"].(string); ok {
		r.Delimiter = &s
	}
	r.Locked, _ = m["locked"].(bool)
	if b, ok := m["private"].(bool); ok {
		r.Private = &b
	}
	if s, ok := m["description"].(string); ok {
		r.Description = &s
	}
	if names, ok := m["stata_names"].(map[string]any); ok && len(names) > 0 {
		r.StataNames = map[string]string{}
		for k, v := range names {
			r.StataNames[k] = fmt.Sprint(v)
		}
	}
	return r
}

// File is a project's data catalog as read from disk, ready to edit.
type File struct {
	path string     // the file holding the catalog
	doc  *yaml.Node // its document
	data *yaml.Node // the catalog mapping inside it
}

// ErrNoSettings is a project without settings.yml.
var ErrNoSettings = errors.New("no settings.yml")

// Open reads the data catalog of the project at root: inline in
// settings.yml (under default: when it has environments), or in the split
// file its data key names, such as settings/data.yml.
func Open(root string) (*File, error) {
	settings := ""
	for _, name := range []string{"settings.yml", "settings.yaml"} {
		if _, err := os.Stat(filepath.Join(root, name)); err == nil {
			settings = filepath.Join(root, name)
			break
		}
	}
	if settings == "" {
		return nil, ErrNoSettings
	}
	doc, err := readDoc(settings)
	if err != nil {
		return nil, err
	}
	config := top(doc)
	if def := get(config, "default"); def != nil && def.Kind == yaml.MappingNode {
		config = def
	}
	data := get(config, "data")
	if data != nil && data.Kind == yaml.ScalarNode && data.Tag != "!!null" {
		split := data.Value
		if !filepath.IsAbs(split) {
			split = filepath.Join(root, filepath.FromSlash(split))
		}
		sdoc, err := readDoc(split)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		if sdoc == nil {
			sdoc = &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
		}
		holder := top(sdoc)
		if def := get(holder, "default"); def != nil && def.Kind == yaml.MappingNode && get(def, "data") != nil {
			holder = def
		}
		inner := get(holder, "data")
		if inner == nil && len(holder.Content) == 0 {
			inner = set(holder, "data", &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"})
		}
		if inner == nil {
			// A split file without the data key holds the catalog itself
			inner = holder
		}
		inner = asMapping(holder, "data", inner)
		return &File{path: split, doc: sdoc, data: inner}, nil
	}
	if data == nil {
		data = set(config, "data", &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"})
	}
	data = asMapping(config, "data", data)
	return &File{path: settings, doc: doc, data: data}, nil
}

func readDoc(path string) (*yaml.Node, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // a project's own settings
	if err != nil {
		return nil, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	if doc.Kind == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
	}
	return &doc, nil
}

func top(doc *yaml.Node) *yaml.Node {
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		doc.Content = []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}
	}
	return doc.Content[0]
}

// asMapping makes the catalog a block mapping, replacing a null or empty
// flow mapping (data: {}) in its parent.
func asMapping(parent *yaml.Node, key string, node *yaml.Node) *yaml.Node {
	if node.Kind == yaml.MappingNode {
		if len(node.Content) == 0 {
			node.Style = 0
		}
		return node
	}
	return set(parent, key, &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"})
}

func get(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// set replaces key's value in mapping m, or adds it at the end.
func set(m *yaml.Node, key string, value *yaml.Node) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			// Comments on the old value stay with the key
			value.HeadComment, value.LineComment = m.Content[i+1].HeadComment, m.Content[i+1].LineComment
			m.Content[i+1] = value
			return value
		}
	}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, value)
	return value
}

// Rows are the catalog's entries as registry rows, by name: nested, mixed
// (raw.survey:), and flat (inputs.raw.survey:) keys, and path shorthands.
// Entries without a path are left out.
func (f *File) Rows() map[string]Row {
	out := map[string]Row{}
	walk(f.data, "", out)
	return out
}

func walk(m *yaml.Node, prefix string, out map[string]Row) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		k, v := m.Content[i].Value, m.Content[i+1]
		name := k
		if prefix != "" {
			name = prefix + "." + k
		}
		switch {
		case v.Kind == yaml.ScalarNode && v.Tag == "!!str":
			out[name] = Row{Name: name, Path: v.Value}
		case v.Kind == yaml.MappingNode && get(v, "path") != nil:
			var m map[string]any
			if v.Decode(&m) == nil {
				out[name] = rowFromMap(name, m)
			}
		case v.Kind == yaml.MappingNode && !pathless(v):
			walk(v, name, out)
		}
	}
}

// pathless is a mapping of entry fields without a path, rather than a
// group: every key is an entry field, and no value looks like a file path.
func pathless(m *yaml.Node) bool {
	fields := []string{"path", "type", "delimiter", "locked", "private", "description"}
	if len(m.Content) == 0 {
		return false
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		k, v := m.Content[i].Value, m.Content[i+1]
		if !contains(fields, k) {
			return false
		}
		if k == "description" {
			continue
		}
		if v.Kind != yaml.ScalarNode {
			return false
		}
		if strings.ContainsAny(v.Value, `/\`) || filepath.Ext(v.Value) != "" {
			return false
		}
	}
	return true
}

// find returns the keys that lead to a name's entry: nested keys first,
// then mixed and flat ones, as data_read() looks them up. nil when absent.
func find(m *yaml.Node, parts []string) []string {
	if len(parts) == 0 {
		return nil
	}
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for j := 1; j <= len(parts); j++ {
		k := strings.Join(parts[:j], ".")
		child := get(m, k)
		if child == nil || child.Tag == "!!null" {
			continue
		}
		if j == len(parts) {
			if child.Kind == yaml.ScalarNode || (child.Kind == yaml.MappingNode && get(child, "path") != nil) {
				return []string{k}
			}
			continue
		}
		if rest := find(child, parts[j:]); rest != nil {
			return append([]string{k}, rest...)
		}
	}
	return nil
}

// Set writes a row into the catalog where its entry already is, else nested
// by name. Fields that don't travel are kept.
func (f *File) Set(r Row) {
	parts := strings.Split(r.Name, ".")
	keys := find(f.data, parts)
	var before *yaml.Node
	if keys != nil {
		node := f.data
		for _, k := range keys {
			node = get(node, k)
		}
		before = node
	} else {
		keys = parts
	}
	setEntry(f.data, keys, entryNode(before, r))
}

func setEntry(m *yaml.Node, keys []string, entry *yaml.Node) {
	if len(keys) == 1 {
		set(m, keys[0], entry)
		return
	}
	child := get(m, keys[0])
	if child == nil || child.Kind != yaml.MappingNode {
		child = set(m, keys[0], &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"})
	}
	child.Style = 0
	setEntry(child, keys[1:], entry)
}

// entryNode is a row as a block mapping, keeping the local-only fields of
// the entry it replaces.
func entryNode(before *yaml.Node, r Row) *yaml.Node {
	n := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	str := func(s string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s} }
	boolean := func(b bool) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: fmt.Sprint(b)} }
	add := func(k string, v *yaml.Node) { n.Content = append(n.Content, str(k), v) }
	add("path", str(r.Path))
	if r.Type != nil {
		add("type", str(*r.Type))
	}
	if r.Delimiter != nil {
		add("delimiter", str(*r.Delimiter))
	}
	if r.Locked {
		add("locked", boolean(true))
	}
	if r.Private != nil {
		add("private", boolean(*r.Private))
	}
	if r.Description != nil {
		add("description", str(*r.Description))
	}
	if len(r.StataNames) > 0 {
		names := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		keys := make([]string, 0, len(r.StataNames))
		for k := range r.StataNames {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			names.Content = append(names.Content, str(k), str(r.StataNames[k]))
		}
		add("stata_names", names)
	}
	if before != nil && before.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(before.Content); i += 2 {
			if !contains(Fields, before.Content[i].Value) {
				n.Content = append(n.Content, before.Content[i], before.Content[i+1])
			}
		}
	}
	return n
}

// Save writes the file back. Comments and the other keys are kept; yaml.v3
// re-indents the file with two spaces.
func (f *File) Save() error {
	var b bytes.Buffer
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(f.doc); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(f.path), 0o755); err != nil { //nolint:gosec // a project folder
		return err
	}
	return os.WriteFile(f.path, b.Bytes(), 0o644) //nolint:gosec // a project file, shared through git
}

// Render is the nested catalog as YAML lines under a data: key, indented
// by indent spaces, for a new settings.yml.
func Render(data map[string]any, indent int) (string, error) {
	var b bytes.Buffer
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	doc := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: "data"}, Nest(data),
	}}
	if err := enc.Encode(doc); err != nil {
		return "", err
	}
	if err := enc.Close(); err != nil {
		return "", err
	}
	lines := strings.Split(strings.TrimSuffix(b.String(), "\n"), "\n")
	pad := strings.Repeat(" ", indent)
	for i, l := range lines {
		lines[i] = pad + l
	}
	return strings.Join(lines, "\n"), nil
}
