package spec

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
)

// StaleKeys are keys that an older upgrade() in the framework R package
// wrote into specs and spec.v1 doesn't allow: an old scaffold.positron
// setting and the upgrade's own provenance. Framework Cloud refuses a spec
// that has them. upgrade() in the framework newer than 2.0.1 drops them
// from the file.
var StaleKeys = []string{"scaffold.positron", "source.upgraded_from"}

// Problem is one way a spec breaks spec.v1: where, as a dot path ("" for the
// top level), and what.
type Problem struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

func (p Problem) String() string {
	if p.Path == "" {
		return p.Message
	}
	return p.Path + ": " + p.Message
}

// Checked is a spec checked against spec.v1 before it goes to Framework
// Cloud.
type Checked struct {
	// Data is the spec to send: as read, or without the stale keys when
	// Dropped names any.
	Data []byte
	// Dropped lists the StaleKeys the spec had, which Data leaves out.
	Dropped []string
	// Problems lists what still breaks spec.v1, in path order. Framework
	// Cloud would refuse the spec.
	Problems []Problem
}

// CheckSpec checks a resolved spec, such as .framework/spec.json, against
// spec.v1, as Framework Cloud does when it receives one. Keys in StaleKeys
// are dropped first and reported in Dropped; anything else that breaks the
// schema is a Problem. The error is for JSON that can't be read.
func CheckSpec(data []byte) (Checked, error) {
	c := Checked{Data: data}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var doc any
	if err := dec.Decode(&doc); err != nil {
		return c, fmt.Errorf("not valid JSON: %w", err)
	}
	root, ok := doc.(map[string]any)
	if !ok {
		c.Problems = []Problem{{Message: "the spec must be a JSON object"}}
		return c, nil
	}
	for _, key := range StaleKeys {
		section, field, _ := strings.Cut(key, ".")
		if obj, ok := root[section].(map[string]any); ok {
			if _, has := obj[field]; has {
				delete(obj, field)
				c.Dropped = append(c.Dropped, key)
			}
		}
	}
	if len(c.Dropped) > 0 {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(root); err != nil {
			return c, err
		}
		c.Data = bytes.TrimRight(buf.Bytes(), "\n")
	}

	// The validator wants its own decoding of numbers
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(c.Data))
	if err != nil {
		return c, err
	}
	if err := specSchema.Validate(inst); err != nil {
		var verr *jsonschema.ValidationError
		if !errors.As(err, &verr) {
			return c, err
		}
		c.Problems = schemaProblems(verr)
	}
	return c, nil
}

// schemaProblems lists a validation error's leaves, once each.
func schemaProblems(verr *jsonschema.ValidationError) []Problem {
	seen := map[Problem]bool{}
	var out []Problem
	add := func(p Problem) {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	var walk func(e *jsonschema.ValidationError)
	walk = func(e *jsonschema.ValidationError) {
		// A bad key name's causes are about the name, at no location
		if k, ok := e.ErrorKind.(*kind.PropertyNames); ok {
			p := append(namesLocation(e), k.Property)
			add(Problem{Path: strings.Join(p, "."), Message: "not an allowed key name"})
			return
		}
		if len(e.Causes) > 0 {
			for _, c := range e.Causes {
				walk(c)
			}
			return
		}
		at := strings.Join(e.InstanceLocation, ".")
		under := func(key string) string {
			if at == "" {
				return key
			}
			return at + "." + key
		}
		switch k := e.ErrorKind.(type) {
		case *kind.AdditionalProperties:
			for _, p := range k.Properties {
				add(Problem{Path: under(p), Message: "not part of spec.v1"})
			}
		case *kind.Required:
			for _, p := range k.Missing {
				add(Problem{Path: under(p), Message: "missing"})
			}
		default:
			msg := e.Error()
			if _, rest, ok := strings.Cut(msg, ": "); ok && strings.HasPrefix(msg, "at ") {
				msg = rest
			}
			add(Problem{Path: at, Message: msg})
		}
	}
	walk(verr)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// namesLocation is where a propertyNames error is. The validator's
// InstanceLocation for it can be another value's (jsonschema v6.0.3 reuses
// the slice), so it comes from the schema location when that is a plain
// path of properties, as it is in spec.v1 outside its definitions.
func namesLocation(e *jsonschema.ValidationError) []string {
	_, frag, _ := strings.Cut(e.SchemaURL, "#")
	parts := strings.Split(strings.TrimPrefix(frag, "/"), "/")
	if len(parts) < 1 || parts[len(parts)-1] != "propertyNames" || len(parts)%2 != 1 {
		return slices.Clone(e.InstanceLocation)
	}
	var loc []string
	for i := 0; i+1 < len(parts); i += 2 {
		if parts[i] != "properties" {
			return slices.Clone(e.InstanceLocation)
		}
		loc = append(loc, parts[i+1])
	}
	return loc
}
