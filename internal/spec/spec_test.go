package spec

import (
	"strings"
	"testing"
)

func TestParseAndLanguage(t *testing.T) {
	cases := map[string]string{
		`{"spec_version":1,"name":"A","layers":[]}`: "r",
		`{"spec_version":1,"name":"A","layers":[{"kind":"settings","layer":{"language":"python"}},{"kind":"project","key":"a","layer":{}}]}`:               "python",
		`{"spec_version":1,"name":"A","layers":[{"kind":"base","layer":{"language":"python"}},{"kind":"project","key":"a","layer":{"language":"stata"}}]}`: "stata",
	}
	for doc, want := range cases {
		env, err := Parse([]byte(doc))
		if err != nil {
			t.Fatalf("%s: %v", doc, err)
		}
		if got := env.Language(); got != want {
			t.Errorf("%s: language %q; want %q", doc, got, want)
		}
	}
}

func TestParseRejects(t *testing.T) {
	bad := []string{
		`not json`,
		`{"spec_version":2,"name":"A","layers":[]}`,
		`{"spec_version":1,"layers":[]}`,
		`{"spec_version":1,"name":"A","layers":[{"kind":"project","layer":{"language":"julia"}}]}`,
		`{"spec_version":1,"name":"A","layers":[{"kind":"project","layer":{"directories":{"x":"../escape"}}}]}`,
	}
	for _, doc := range bad {
		if _, err := Parse([]byte(doc)); err == nil {
			t.Errorf("%s: want an error", doc)
		}
	}
}

func TestMarshalRoundTrip(t *testing.T) {
	doc := `{"spec_version":1,"name":"A","layers":[{"kind":"project","key":"a","version":3,"layer":{"language":"r","directories":{"notebooks":"nb"}}}]}`
	env, err := Parse([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	out, err := env.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Parse(out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"version": 3`) || !strings.Contains(string(out), `"notebooks": "nb"`) {
		t.Errorf("lost data:\n%s", out)
	}
}
