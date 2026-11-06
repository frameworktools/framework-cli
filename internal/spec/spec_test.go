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

func TestPublishingLayerSchema(t *testing.T) {
	valid := `{"spec_version":1,"name":"A","layers":[{"kind":"settings","layer":{"publish":{"default":"pages","destinations":{"pages":{"driver":"github"},"lab":{"driver":"folder","path":"/Volumes/lab"}}}}}]}`
	env, e := Parse([]byte(valid))
	if e != nil {
		t.Fatal(e)
	}
	out, e := env.Marshal()
	if e != nil || !strings.Contains(string(out), `"publish"`) {
		t.Fatal(e, string(out))
	}
	for _, bad := range []string{strings.Replace(valid, `"driver":"github"`, `"driver":"ftp"`, 1), strings.Replace(valid, `"default":"pages"`, `"default":42`, 1)} {
		if _, e := Parse([]byte(bad)); e == nil {
			t.Fatal("invalid publishing configuration accepted", bad)
		}
	}
}

func TestPublishingLayersRejectStoredSecrets(t *testing.T) {
	for _, options := range []string{`"secret_key":"literal"`, `"credentials":{"token":"literal"}`, `"endpoint":"https://user:password@example.org"`} {
		doc := `{"spec_version":1,"name":"A","layers":[{"kind":"settings","layer":{"publish":{"destinations":{"bucket":{"driver":"s3",` + options + `}}}}}]}`
		if _, e := Parse([]byte(doc)); e == nil {
			t.Fatal("saved secret accepted", options)
		}
	}
	doc := `{"spec_version":1,"name":"A","layers":[{"kind":"settings","layer":{"publish":{"destinations":{"bucket":{"driver":"s3","secret_key":"env(\"BUCKET_SECRET\")"}}}}}]}`
	if _, e := Parse([]byte(doc)); e != nil {
		t.Fatal(e)
	}
}
