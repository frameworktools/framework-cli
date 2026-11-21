package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func readFixture(t *testing.T, name string) *notebook {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "snippets", name))
	if err != nil {
		t.Fatal(err)
	}
	nb, err := readNotebook(data)
	if err != nil {
		t.Fatal(err)
	}
	return nb
}

func boolPtr(b bool) *bool { return &b }

func TestSnippetCellBlocks(t *testing.T) {
	nb := readFixture(t, "analysis.ipynb")
	traceback := "\x1b[0;31mZeroDivisionError\x1b[0m Traceback\n\x1b[0;31mZeroDivisionError\x1b[0m: division by zero"
	tests := []struct {
		name, label string
		output      bool
		folded      *bool
		want        []snippetBlock
	}{
		{"tagged cell with every output type", "fit-ols", true, nil, []snippetBlock{
			{Type: "code", Text: "import sys\nprint('a')\nprint('b')\nprint('slow', file=sys.stderr)\nplt.plot([1, 2])\nmodel\ndf\n1 / 0"},
			{Type: "output", Text: "a\nb"},
			{Type: "message", Text: "slow"},
			{Type: "output", Text: "OLS results\nR2 0.41\n   x\n0  1"},
			{Type: "error", Text: traceback},
		}},
		{"folded marks output blocks only", "fit-ols", true, boolPtr(true), []snippetBlock{
			{Type: "code", Text: "import sys\nprint('a')\nprint('b')\nprint('slow', file=sys.stderr)\nplt.plot([1, 2])\nmodel\ndf\n1 / 0"},
			{Type: "output", Text: "a\nb", Folded: boolPtr(true)},
			{Type: "message", Text: "slow"},
			{Type: "output", Text: "OLS results\nR2 0.41\n   x\n0  1", Folded: boolPtr(true)},
			{Type: "error", Text: traceback},
		}},
		{"no output", "fit-ols", false, nil, []snippetBlock{
			{Type: "code", Text: "import sys\nprint('a')\nprint('b')\nprint('slow', file=sys.stderr)\nplt.plot([1, 2])\nmodel\ndf\n1 / 0"},
		}},
		{"Quarto label; option lines dropped", "summary", true, boolPtr(false), []snippetBlock{
			{Type: "code", Text: "print(1)"},
			{Type: "output", Text: "1", Folded: boolPtr(false)},
		}},
		{"cell magic line dropped", "magic", true, nil, []snippetBlock{
			{Type: "code", Text: "y = 2\nprint(y)"},
			{Type: "output", Text: "2"},
		}},
		{"run without output", "quiet", true, nil, []snippetBlock{
			{Type: "code", Text: "x = 1"},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := nb.findCell(tt.label)
			if err != nil {
				t.Fatal(err)
			}
			got := cellBlocks(c, tt.output, tt.folded)
			if !reflect.DeepEqual(got, tt.want) {
				g, _ := json.MarshalIndent(got, "", "  ")
				w, _ := json.MarshalIndent(tt.want, "", "  ")
				t.Errorf("blocks:\n%s\nwant:\n%s", g, w)
			}
		})
	}
}

func TestSnippetFindCellErrors(t *testing.T) {
	nb := readFixture(t, "analysis.ipynb")
	tests := []struct{ label, want string }{
		{"missing", `no code cell is tagged "missing"`},
		{"dup", `2 code cells match "dup" (cells 7, 8)`},
		{"unrun", `cell "unrun" has not been run. Run the notebook and save it`},
	}
	for _, tt := range tests {
		t.Run(tt.label, func(t *testing.T) {
			_, err := nb.findCell(tt.label)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %v, want %q", err, tt.want)
			}
		})
	}
}

func TestSnippetLanguage(t *testing.T) {
	tests := []struct{ file, want, err string }{
		{"analysis.ipynb", "python", ""},
		{"r.ipynb", "r", ""},
		{"bare.ipynb", "python", ""},
		{"julia.ipynb", "", "language is julia"},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			got, err := readFixture(t, tt.file).language()
			if got != tt.want || (tt.err == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), tt.err)) {
				t.Errorf("language %q, %v; want %q, %q", got, err, tt.want, tt.err)
			}
		})
	}
	data, _ := os.ReadFile(filepath.Join("testdata", "snippets", "v3.ipynb"))
	if _, err := readNotebook(data); err == nil || !strings.Contains(err.Error(), "format 4") {
		t.Errorf("format 3: %v", err)
	}
}

func TestSnippetLinks(t *testing.T) {
	tests := []struct {
		in   []string
		want []snippetLink
		err  string
	}{
		{[]string{"notebook=https://example.org/nb", "model=ols-model", "model=https://example.org/m"}, []snippetLink{
			{Rel: "notebook", URL: "https://example.org/nb"},
			{Rel: "model", Artifact: "ols-model"},
			{Rel: "model", URL: "https://example.org/m"},
		}, ""},
		{[]string{"notebook=http://example.org"}, nil, "notebook link takes an https URL"},
		{[]string{"notebook=ols-model"}, nil, "notebook link takes an https URL"},
		{[]string{"model=has space"}, nil, "model link takes an artifact slug"},
		{[]string{"figure=https://example.org"}, nil, "--link takes notebook=URL or model=SLUG"},
		{[]string{"notebook"}, nil, "--link takes notebook=URL or model=SLUG"},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.in, ","), func(t *testing.T) {
			got, err := parseSnippetLinks(tt.in)
			if tt.err != "" {
				if err == nil || !strings.Contains(err.Error(), tt.err) {
					t.Errorf("error %v, want %q", err, tt.err)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(got, tt.want) {
				t.Errorf("links %+v, %v", got, err)
			}
		})
	}
}

func TestSnippetLocationInGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	top := t.TempDir()
	write(t, top, "study/notebooks/analysis.ipynb", "{}")
	gitT(t, top, "init", "-q", "-b", "main")
	gitT(t, top, "config", "user.name", "Ada")
	gitT(t, top, "config", "user.email", "ada@example.edu")
	gitT(t, top, "config", "commit.gpgsign", "false")
	gitT(t, top, "add", "-A")
	gitT(t, top, "commit", "-q", "-m", "Start")
	head := gitT(t, top, "rev-parse", "HEAD")
	file, commit := snippetLocation(filepath.Join(top, "study"), filepath.Join(top, "study", "notebooks", "analysis.ipynb"))
	if file != "study/notebooks/analysis.ipynb" || commit != head {
		t.Errorf("location %q %q, want commit %q", file, commit, head)
	}
}

func TestSnippetPublish(t *testing.T) {
	srv := fakeCloud(t)
	h := newHarness(t, nil)
	root := linkedProject(t, h, srv.URL)
	fixture, err := os.ReadFile(filepath.Join("testdata", "snippets", "analysis.ipynb"))
	if err != nil {
		t.Fatal(err)
	}
	write(t, root, "notebooks/analysis.ipynb", string(fixture))
	write(t, root, "settings.yml", "default:\n  publish:\n    repository: https://github.com/acme/study\n")

	out, errOut, err := h.run(t, "", "snippet", "publish", "notebooks/analysis.ipynb", "--cell", "fit-ols",
		"--folded", "--link", "notebook=https://example.org/analysis", "--link", "model=ols-model", "--yes")
	if err != nil {
		t.Fatal(err, errOut)
	}
	if !strings.Contains(out, "Published analysis, version 2.") || !strings.Contains(errOut, "cell fit-ols: code and 4 output blocks") {
		t.Errorf("output:\n%s\n%s", out, errOut)
	}
	if lastUpload["kind"] != "code_snippet" || lastUpload["name"] != "code_snippet.json" || lastUpload["slug"] != "fit-ols" || lastUpload["title"] != "fit-ols" {
		t.Errorf("upload fields: %+v", lastUpload)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(lastUpload["data"]), &doc); err != nil {
		t.Fatal(err)
	}
	source := doc["source"].(map[string]any)
	if doc["schema"] != "framework.artifact/v1" || doc["kind"] != "code_snippet" ||
		source["language"] != "python" || source["file"] != "notebooks/analysis.ipynb" || source["label"] != "fit-ols" ||
		source["repository"] != "https://github.com/acme/study" || source["lines"] != nil {
		t.Errorf("document: %s", lastUpload["data"])
	}
	snippet := doc["snippet"].(map[string]any)
	if _, ok := snippet["width"]; ok || len(snippet["blocks"].([]any)) != 5 {
		t.Errorf("snippet: %v", snippet)
	}
	links := doc["display"].(map[string]any)["links"].([]any)
	if len(links) != 2 || links[1].(map[string]any)["artifact"] != "ols-model" {
		t.Errorf("links: %v", links)
	}

	// Flags override the defaults; a repository is sent only when given.
	write(t, root, "settings.yml", "default: {}\n")
	if _, errOut, err := h.run(t, "", "snippet", "publish", "notebooks/analysis.ipynb", "--cell", "summary",
		"--slug", "one", "--title", "One", "--no-output", "--yes"); err != nil {
		t.Fatal(err, errOut)
	}
	if lastUpload["slug"] != "one" || lastUpload["title"] != "One" || strings.Contains(lastUpload["data"], "repository") ||
		strings.Contains(lastUpload["data"], `"output"`) || strings.Contains(lastUpload["data"], "display") {
		t.Errorf("summary upload: %+v", lastUpload)
	}

	// An unchanged snippet keeps its version.
	fakeUnchanged = true
	t.Cleanup(func() { fakeUnchanged = false })
	out, _, err = h.run(t, "", "snippet", "publish", "notebooks/analysis.ipynb", "--cell", "summary", "--yes")
	if err != nil || !strings.Contains(out, "Unchanged, still version 2: https://x/d/abc") || strings.Contains(out, "Published") {
		t.Errorf("unchanged: %v\n%s", err, out)
	}

	usage := [][]string{
		{"snippet", "publish", "notebooks/analysis.ipynb", "--yes"},
		{"snippet", "publish", "notebooks/analysis.ipynb", "--cell", "summary", "--folded", "--unfolded", "--yes"},
		{"snippet", "publish", "notebooks/analysis.ipynb", "--cell", "summary", "--repository", "http://github.com/acme/study", "--yes"},
		{"snippet", "publish", "notebooks/analysis.ipynb", "--cell", "summary", "--link", "notebook=x", "--yes"},
	}
	for _, args := range usage {
		if _, _, err := h.run(t, "", args...); exitCode(err) != exitUsage {
			t.Errorf("%v: exit %d %v", args, exitCode(err), err)
		}
	}
	if _, _, err := h.run(t, "", "snippet", "publish", "notebooks/analysis.ipynb", "--cell", "unrun", "--yes"); err == nil || !strings.Contains(err.Error(), "has not been run") {
		t.Errorf("unrun cell: %v", err)
	}
}

func TestPublishKindCodeSnippet(t *testing.T) {
	srv := fakeCloud(t)
	h := newHarness(t, nil)
	root := linkedProject(t, h, srv.URL)
	write(t, root, "code_snippet.json", `{"schema":"framework.artifact/v1","kind":"code_snippet","source":{"language":"stata"},"snippet":{"blocks":[{"type":"code","text":"regress y x"}]}}`)
	if _, errOut, err := h.run(t, "", "publish", "code_snippet.json", "--kind", "code_snippet", "--slug", "ols", "--yes"); err != nil {
		t.Fatal(err, errOut)
	}
	if lastUpload["kind"] != "code_snippet" {
		t.Errorf("upload: %+v", lastUpload)
	}
}

func TestPublishKindDag(t *testing.T) {
	srv := fakeCloud(t)
	h := newHarness(t, nil)
	root := linkedProject(t, h, srv.URL)
	write(t, root, "dag.json", `{"schema":"framework.artifact/v1","kind":"dag","dagitty":"dag { x [exposure] y [outcome] x -> y }"}`)
	if _, errOut, err := h.run(t, "", "publish", "dag.json", "--kind", "dag", "--slug", "xy", "--yes"); err != nil {
		t.Fatal(err, errOut)
	}
	if lastUpload["kind"] != "dag" {
		t.Errorf("upload: %+v", lastUpload)
	}
}

func TestPublishDisplay(t *testing.T) {
	srv := fakeCloud(t)
	h := newHarness(t, nil)
	root := linkedProject(t, h, srv.URL)
	write(t, root, "model.dagitty", "dag { x [exposure] y [outcome] x -> y }")
	if _, errOut, err := h.run(t, "", "publish", "model.dagitty", "--display", "legend=off", "--display", "dagitty=on", "--yes"); err != nil {
		t.Fatal(err, errOut)
	}
	if lastUpload["display"] != `{"dagitty":true,"legend":false}` {
		t.Errorf("upload: %+v", lastUpload)
	}
	if _, _, err := h.run(t, "", "publish", "model.dagitty", "--display", "sparkles=off", "--yes"); exitCode(err) != exitUsage || !strings.Contains(err.Error(), "NAME one of title") {
		t.Errorf("unknown setting: %v", err)
	}
	if _, _, err := h.run(t, "", "publish", "model.dagitty", "--display", "legend=maybe", "--yes"); exitCode(err) != exitUsage || !strings.Contains(err.Error(), "takes on or off") {
		t.Errorf("bad value: %v", err)
	}
	// Without --display nothing is sent, so the stored settings stay
	if _, _, err := h.run(t, "", "publish", "model.dagitty", "--yes"); err != nil {
		t.Fatal(err)
	}
	if _, ok := lastUpload["display"]; ok {
		t.Errorf("display sent without --display: %+v", lastUpload)
	}
}

func TestPublishPrintsShareAndManageLinks(t *testing.T) {
	srv := fakeCloud(t)
	h := newHarness(t, nil)
	root := linkedProject(t, h, srv.URL)
	write(t, root, "model.dagitty", "dag { x -> y }")
	out, errOut, err := h.run(t, "", "publish", "model.dagitty", "--yes")
	if err != nil {
		t.Fatal(err, errOut)
	}
	want := "Published analysis, version 2.\nShare:  https://x/go/abcd\nManage: https://x/artifacts/7\n"
	if out != want {
		t.Errorf("output:\n%s\nwant:\n%s", out, want)
	}
	out, _, err = h.run(t, "", "publish", "model.dagitty", "--yes", "--json")
	if err != nil || !strings.Contains(out, `"embed_link": "https://x/go/abcd"`) || !strings.Contains(out, `"artifact_url": "https://x/artifacts/7"`) {
		t.Errorf("json: %q %v", out, err)
	}
}
