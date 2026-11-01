// Command syncassets copies shared files from the framework R package and
// checks that the copies are current.
//
// The R repository holds the canonical JSON Schemas and merge conformance
// fixtures. This tool copies them into framework-cli and records each file's
// source path, the R commit, and its SHA-256 in assets.lock.json. Copying is a
// manual step; go test runs the check, so CI fails when a copy drifts.
//
//	go run ./tools/syncassets --from ../framework --ref v2.0.0   # copy at a tag
//	go run ./tools/syncassets --from ../framework                # copy the working tree
//	go run ./tools/syncassets --check                            # copies match the lock
//	go run ./tools/syncassets --check --from ../framework        # ...and match the source
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

const (
	lockName = "assets.lock.json"
	worktree = "worktree"
)

// asset maps a source glob in the R repository to a folder in this repo.
type asset struct{ pattern, dest string }

var assets = []asset{
	{"inst/schema/*.json", "internal/spec/schema"},
	{"inst/conformance/README.md", "internal/spec/testdata/conformance"},
	{"inst/conformance/merge/*.json", "internal/spec/testdata/conformance/merge"},
	{"inst/conformance/create/stata/*.json", "internal/spec/testdata/conformance/create/stata"},
	{"inst/conformance/resolve/*.json", "internal/spec/testdata/conformance/resolve"},
	{"inst/conformance/adopt/*.json", "internal/spec/testdata/conformance/adopt"},
	{"inst/adopt/rules.json", "internal/adopt"},
}

// Lock is the contents of assets.lock.json. Files is keyed by the copy's path
// in this repo, with forward slashes.
type Lock struct {
	Source string           `json:"source"`
	Ref    string           `json:"ref"`
	Commit string           `json:"commit"`
	Files  map[string]Entry `json:"files"`
}

// Entry records where one copied file came from.
type Entry struct {
	Source string `json:"source"`
	SHA256 string `json:"sha256"`
}

func sum(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func git(repo string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...) // #nosec G204 -- fixed git subcommands
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// sources lists the R repository paths that match assets, in order.
func sources(repo, ref string) ([]string, error) {
	var names []string
	if ref == worktree {
		for _, a := range assets {
			found, err := filepath.Glob(filepath.Join(repo, filepath.FromSlash(a.pattern)))
			if err != nil {
				return nil, err
			}
			sort.Strings(found)
			for _, f := range found {
				rel, err := filepath.Rel(repo, f)
				if err != nil {
					return nil, err
				}
				names = append(names, filepath.ToSlash(rel))
			}
		}
	} else {
		out, err := git(repo, "ls-tree", "-r", "--name-only", ref)
		if err != nil {
			return nil, err
		}
		tracked := strings.Split(strings.TrimSpace(string(out)), "\n")
		for _, a := range assets {
			for _, name := range tracked {
				if ok, _ := path.Match(a.pattern, name); ok {
					names = append(names, name)
				}
			}
		}
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("no shared files found in %s at %s", repo, ref)
	}
	return names, nil
}

func readSource(repo, ref, name string) ([]byte, error) {
	if ref == worktree {
		return os.ReadFile(filepath.Join(repo, filepath.FromSlash(name)))
	}
	return git(repo, "show", ref+":"+name)
}

func destination(name string) string {
	for _, a := range assets {
		if ok, _ := path.Match(a.pattern, name); ok {
			return a.dest + "/" + path.Base(name)
		}
	}
	return ""
}

func readLock(root string) (Lock, error) {
	var lock Lock
	data, err := os.ReadFile(filepath.Join(root, lockName))
	if err != nil {
		return lock, err
	}
	if err := json.Unmarshal(data, &lock); err != nil {
		return lock, fmt.Errorf("%s: %w", lockName, err)
	}
	return lock, nil
}

// sync copies the shared files from repo at ref into root and rewrites the lock.
func sync(root, repo, ref string) (int, error) {
	names, err := sources(repo, ref)
	if err != nil {
		return 0, err
	}
	files := map[string]Entry{}
	for _, name := range names {
		data, err := readSource(repo, ref, name)
		if err != nil {
			return 0, err
		}
		dest := destination(name)
		target := filepath.Join(root, filepath.FromSlash(dest))
		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			return 0, err
		}
		if err := os.WriteFile(target, data, 0o644); err != nil { // #nosec G306 -- source files in the repo
			return 0, err
		}
		files[dest] = Entry{Source: name, SHA256: sum(data)}
	}

	if old, err := readLock(root); err == nil {
		for dest := range old.Files {
			if _, ok := files[dest]; !ok {
				if err := os.Remove(filepath.Join(root, filepath.FromSlash(dest))); err != nil && !errors.Is(err, fs.ErrNotExist) {
					return 0, err
				}
			}
		}
	}

	rev := ref
	if ref == worktree {
		rev = "HEAD"
	}
	commit, err := git(repo, "rev-parse", rev+"^{commit}")
	if err != nil {
		return 0, err
	}
	lock := Lock{Source: "framework (R package)", Ref: ref, Commit: strings.TrimSpace(string(commit)), Files: files}
	data, err := json.MarshalIndent(lock, "", "  ")
	if err != nil {
		return 0, err
	}
	return len(files), os.WriteFile(filepath.Join(root, lockName), append(data, '\n'), 0o644) // #nosec G306 -- source file in the repo
}

// check lists the ways the copies differ from the lock and, when repo is
// given, from the R repository at the locked ref.
func check(root, repo string) ([]string, Lock, error) {
	lock, err := readLock(root)
	if err != nil {
		return nil, lock, err
	}
	if len(lock.Files) == 0 {
		return nil, lock, fmt.Errorf("%s lists no files. Run go run ./tools/syncassets --from <R repo>", lockName)
	}
	dests := make([]string, 0, len(lock.Files))
	for dest := range lock.Files {
		dests = append(dests, dest)
	}
	sort.Strings(dests)

	var problems []string
	for _, dest := range dests {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(dest)))
		switch {
		case errors.Is(err, fs.ErrNotExist):
			problems = append(problems, "missing: "+dest)
		case err != nil:
			return nil, lock, err
		case sum(data) != lock.Files[dest].SHA256:
			problems = append(problems, "edited locally: "+dest+" (change it in the R repository and copy it again)")
		}
	}

	// A copied asset folder must not hold extra files the lock does not list.
	for _, a := range assets {
		matches, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(a.dest), path.Base(a.pattern)))
		if err != nil {
			return nil, lock, err
		}
		for _, m := range matches {
			rel, err := filepath.Rel(root, m)
			if err != nil {
				return nil, lock, err
			}
			if _, ok := lock.Files[filepath.ToSlash(rel)]; !ok {
				problems = append(problems, "not in "+lockName+": "+filepath.ToSlash(rel))
			}
		}
	}

	if repo != "" {
		ref := lock.Ref
		if ref == worktree {
			fmt.Printf("Note: the copies came from an uncommitted R working tree (%s).\n", lock.Commit)
		} else {
			ref = lock.Commit
		}
		names, err := sources(repo, ref)
		if err != nil {
			return nil, lock, err
		}
		expected := map[string]string{}
		for _, name := range names {
			expected[destination(name)] = name
		}
		for dest, name := range expected {
			if _, ok := lock.Files[dest]; !ok {
				problems = append(problems, "new in R at "+lock.Ref+", not copied: "+name)
			}
		}
		for _, dest := range dests {
			name, ok := expected[dest]
			if !ok {
				problems = append(problems, "no longer in R at "+lock.Ref+": "+lock.Files[dest].Source)
				continue
			}
			data, err := readSource(repo, ref, name)
			if err != nil {
				return nil, lock, err
			}
			if sum(data) != lock.Files[dest].SHA256 {
				problems = append(problems, "differs from R at "+lock.Ref+": "+name)
			}
		}
	}
	sort.Strings(problems)
	return problems, lock, nil
}

// repoRoot is the folder holding go.mod, found from the current directory.
func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("run this inside the framework-cli repository")
		}
		dir = parent
	}
}

func main() {
	from := flag.String("from", "", "path to the R repository")
	ref := flag.String("ref", worktree, "git ref to copy from (default: the working tree)")
	checkOnly := flag.Bool("check", false, "verify the copies instead of copying")
	flag.Parse()

	root, err := repoRoot()
	if err == nil && *from != "" {
		*from, err = filepath.Abs(*from)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "syncassets:", err)
		os.Exit(1)
	}

	if *checkOnly {
		problems, lock, err := check(root, *from)
		if err != nil {
			fmt.Fprintln(os.Stderr, "syncassets:", err)
			os.Exit(1)
		}
		for _, p := range problems {
			fmt.Println(p)
		}
		if len(problems) > 0 {
			os.Exit(1)
		}
		fmt.Printf("%d shared files match %s (R ref %s, commit %s)\n", len(lock.Files), lockName, lock.Ref, lock.Commit)
		return
	}
	if *from == "" {
		fmt.Fprintln(os.Stderr, "syncassets: --from is required when copying")
		os.Exit(2)
	}
	n, err := sync(root, *from, *ref)
	if err != nil {
		fmt.Fprintln(os.Stderr, "syncassets:", err)
		os.Exit(1)
	}
	fmt.Printf("Copied %d files from %s at %s\n", n, *from, *ref)
}
