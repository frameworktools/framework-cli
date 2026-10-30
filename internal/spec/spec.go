// Package spec reads and validates Framework layers files, the format
// framework.pub sends and the language packages create projects from.
package spec

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/dlclark/regexp2"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

//go:embed schema/*.json
var schemaFiles embed.FS

// Languages lists the values of the spec's language field.
var Languages = []string{"r", "python", "stata"}

// Envelope is a layers.v1 document. Layers stay raw JSON: fw passes them to
// the language packages unchanged.
type Envelope struct {
	SpecVersion int             `json:"spec_version"`
	Name        string          `json:"name"`
	Author      json.RawMessage `json:"author,omitempty"`
	Layers      []Entry         `json:"layers"`
	Source      json.RawMessage `json:"source,omitempty"`
}

// Entry is one layer with its label.
type Entry struct {
	Kind    string          `json:"kind"`
	Key     string          `json:"key,omitempty"`
	Version *int            `json:"version,omitempty"`
	Layer   json.RawMessage `json:"layer"`
}

// Language returns the project's language: the value from the last layer
// that sets it, or "r" when none does.
func (e Envelope) Language() string {
	language := "r"
	for _, entry := range e.Layers {
		var layer struct {
			Language string `json:"language"`
		}
		if json.Unmarshal(entry.Layer, &layer) == nil && layer.Language != "" {
			language = layer.Language
		}
	}
	return language
}

var layersSchema, specSchema = func() (*jsonschema.Schema, *jsonschema.Schema) {
	compiler := jsonschema.NewCompiler()
	// The path patterns use lookaheads, which Go's regexp lacks. JSON Schema
	// patterns are ECMAScript regular expressions.
	compiler.UseRegexpEngine(func(pattern string) (jsonschema.Regexp, error) {
		re, err := regexp2.Compile(pattern, regexp2.ECMAScript)
		if err != nil {
			return nil, err
		}
		return ecmaRegexp{re}, nil
	})
	for _, name := range []string{"layer.v1.json", "layers.v1.json", "spec.v1.json"} {
		data, err := schemaFiles.ReadFile("schema/" + name)
		if err != nil {
			panic(err)
		}
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
		if err != nil {
			panic(fmt.Sprintf("schema %s: %v", name, err))
		}
		id, _ := doc.(map[string]any)["$id"].(string)
		if err := compiler.AddResource(id, doc); err != nil {
			panic(err)
		}
	}
	return compiler.MustCompile("https://framework.pub/schema/layers/v1.json"),
		compiler.MustCompile("https://framework.pub/schema/spec/v1.json")
}()

// ValidateResolved checks a resolved spec against spec.v1.
func ValidateResolved(resolved map[string]any) error {
	// Round-trip through JSON so the validator sees plain JSON values
	data, err := json.Marshal(resolved)
	if err != nil {
		return err
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return err
	}
	if err := specSchema.Validate(doc); err != nil {
		var verr *jsonschema.ValidationError
		if errors.As(err, &verr) {
			return fmt.Errorf("the resolved spec does not match the spec schema:\n%s", indent(verr.Error()))
		}
		return err
	}
	return nil
}

type ecmaRegexp struct{ re *regexp2.Regexp }

func (r ecmaRegexp) MatchString(s string) bool {
	ok, err := r.re.MatchString(s)
	return err == nil && ok
}

func (r ecmaRegexp) String() string { return r.re.String() }

// Parse validates data against layers.v1 and decodes it.
func Parse(data []byte) (Envelope, error) {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return Envelope{}, fmt.Errorf("not valid JSON: %w", err)
	}
	if err := layersSchema.Validate(doc); err != nil {
		var verr *jsonschema.ValidationError
		if errors.As(err, &verr) {
			return Envelope{}, fmt.Errorf("does not match the layers schema:\n%s", indent(verr.Error()))
		}
		return Envelope{}, err
	}
	var env Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return Envelope{}, err
	}
	return env, nil
}

// ReadFile reads and validates a layers file.
func ReadFile(path string) (Envelope, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Envelope{}, err
	}
	env, err := Parse(data)
	if err != nil {
		return Envelope{}, fmt.Errorf("%s %w", path, err)
	}
	return env, nil
}

// Marshal encodes the envelope for a layers file.
func (e Envelope) Marshal() ([]byte, error) {
	data, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func indent(s string) string {
	return "  " + strings.ReplaceAll(strings.TrimSpace(s), "\n", "\n  ")
}
