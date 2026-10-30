package spec

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// The layer merge shared by every engine. It follows R's
// .spec_merge_layers(), and the fixtures in testdata/conformance/merge pin
// down the behavior the R package, the Python package, and fw must share.

// DefaultsLabel labels an engine's own defaults, layer 0. It never counts
// toward conflicts.
const DefaultsLabel = "package defaults"

// Labeled is one layer ready to merge: a label for messages and a decoded
// JSON object.
type Labeled struct {
	Label string
	Layer map[string]any
}

// Block is one layer's gitignore text.
type Block struct {
	Label string `json:"label"`
	Text  string `json:"text"`
}

// Conflict is a key that more than one layer set to different values.
type Conflict struct {
	Path   string `json:"path"`
	Layers string `json:"layers"`
}

// Merged is the result of merging layers.
type Merged struct {
	Spec      map[string]any
	Gitignore []Block
	Conflicts []Conflict
}

// Entries whose values are replaced whole when a later layer sets them.
var atomicMaps = map[string]bool{
	"files": true, "skills": true, "extra_directories": true,
	"packages/default_packages": true,
	"connections/databases":     true, "connections/storage_buckets": true,
}

// Label names an entry the way R does: kind (or "layer N", 1-based), then
// key, then vN.
func (e Entry) Label(index int) string {
	parts := []string{e.Kind}
	if e.Kind == "" {
		parts[0] = fmt.Sprintf("layer %d", index+1)
	}
	if e.Key != "" {
		parts = append(parts, e.Key)
	}
	if e.Version != nil {
		parts = append(parts, fmt.Sprintf("v%d", *e.Version))
	}
	return strings.Join(parts, " ")
}

// DecodeObject decodes a JSON object, keeping numbers exact.
func DecodeObject(raw []byte) (map[string]any, error) {
	if len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		return map[string]any{}, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var out map[string]any
	if err := dec.Decode(&out); err != nil {
		return nil, err
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}

// LabeledLayers decodes the envelope's layers with their labels.
func (e Envelope) LabeledLayers() ([]Labeled, error) {
	out := make([]Labeled, 0, len(e.Layers))
	for i, entry := range e.Layers {
		layer, err := DecodeObject(entry.Layer)
		if err != nil {
			return nil, fmt.Errorf("layer %d: %w", i+1, err)
		}
		out = append(out, Labeled{Label: entry.Label(i), Layer: layer})
	}
	return out, nil
}

type setter struct {
	label string
	value string // canonical JSON, or removedMarker
}

const removedMarker = "<removed>"

type mergeState struct {
	spec    map[string]any
	blocks  []Block
	paths   []string // in the order first set, for stable conflict output
	setters map[string][]setter
}

// Merge applies layers in order.
func Merge(layers []Labeled) (Merged, error) {
	st := &mergeState{spec: map[string]any{}, setters: map[string][]setter{}}
	for _, l := range layers {
		layer := cloneMap(l.Layer)
		if git, ok := layer["git"].(map[string]any); ok {
			if text := gitignoreText(git["gitignore_content"]); text != "" {
				st.blocks = append(st.blocks, Block{Label: l.Label, Text: text})
			}
			delete(git, "gitignore_content")
		}
		st.merge(st.spec, layer, nil, l.Label)
	}
	return Merged{Spec: st.spec, Gitignore: st.blocks, Conflicts: st.conflicts()}, nil
}

func gitignoreText(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []any:
		lines := make([]string, 0, len(t))
		for _, line := range t {
			if s, ok := line.(string); ok {
				lines = append(lines, s)
			}
		}
		return strings.Join(lines, "\n")
	}
	return ""
}

func (st *mergeState) record(path, label, value string) {
	if _, seen := st.setters[path]; !seen {
		st.paths = append(st.paths, path)
	}
	st.setters[path] = append(st.setters[path], setter{label, value})
}

func (st *mergeState) merge(base, layer map[string]any, path []string, label string) {
	parent := strings.Join(path, "/")
	keys := make([]string, 0, len(layer))
	for k := range layer {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, key := range keys {
		value := layer[key]
		keyPath := strings.Join(append(append([]string{}, path...), key), "/")

		if value == nil {
			delete(base, key)
			st.record(keyPath, label, removedMarker)
			continue
		}

		obj, isObject := value.(map[string]any)
		if !atomicMaps[parent] && isObject {
			if existing, ok := asMergeable(base[key]); ok {
				st.merge(existing, obj, append(append([]string{}, path...), key), label)
				base[key] = existing
				continue
			}
		}
		if isEmpty(value) {
			// An empty object or array changes nothing, but makes sure the
			// section exists
			if _, ok := base[key]; !ok {
				base[key] = value
			}
			continue
		}
		base[key] = value
		st.record(keyPath, label, canonical(value))
	}
}

// asMergeable returns base as an object to merge into: a missing value or an
// empty array becomes a new object, an object is used as is, and anything
// else (a scalar or a non-empty array) is replaced instead.
func asMergeable(v any) (map[string]any, bool) {
	switch t := v.(type) {
	case nil:
		return map[string]any{}, true
	case map[string]any:
		return t, true
	case []any:
		if len(t) == 0 {
			return map[string]any{}, true
		}
	}
	return nil, false
}

func isEmpty(v any) bool {
	switch t := v.(type) {
	case map[string]any:
		return len(t) == 0
	case []any:
		return len(t) == 0
	}
	return false
}

func canonical(v any) string {
	data, err := json.Marshal(v) // map keys are sorted
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(data)
}

func (st *mergeState) conflicts() []Conflict {
	var out []Conflict
	for _, path := range st.paths {
		var labels []string
		seenLabel := map[string]bool{}
		seenValue := map[string]bool{}
		for _, s := range st.setters[path] {
			if s.label == DefaultsLabel {
				continue
			}
			if !seenLabel[s.label] {
				seenLabel[s.label] = true
				labels = append(labels, s.label)
			}
			seenValue[s.value] = true
		}
		if len(labels) > 1 && len(seenValue) > 1 {
			out = append(out, Conflict{Path: path, Layers: strings.Join(labels, ", ")})
		}
	}
	return out
}

func cloneMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = cloneValue(v)
	}
	return out
}

func cloneValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return cloneMap(t)
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = cloneValue(x)
		}
		return out
	}
	return v
}
