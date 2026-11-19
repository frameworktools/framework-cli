// Command skillhistory writes internal/stata/skill-history.json: the bundle
// hash of every version of each skill fw ships, from git history and the
// working tree. Skills lock v2 counts a copy holding any of them as an
// unedited older version. Run it after changing a shipped skill:
//
//	go run ./tools/skillhistory
//	go run ./tools/skillhistory --check   # the file is current
package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/frameworktools/framework-cli/internal/skillsync"
)

const (
	skillsDir = "internal/stata/skills"
	output    = "internal/stata/skill-history.json"
	variant   = "stata"
)

// History is skill-history.json, schema 2: bundle hashes per skill and
// variant.
type History struct {
	Schema int                            `json:"schema"`
	Skills map[string]map[string][]string `json:"skills"`
}

func git(root string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...) // #nosec G204 -- fixed git subcommands
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// bundleHash is the hash of a skill that is one SKILL.md.
func bundleHash(text []byte) (string, error) {
	return skillsync.Hash([]skillsync.File{{Path: "SKILL.md", Content: base64.StdEncoding.EncodeToString(text)}})
}

func build(root string) (History, error) {
	h := History{Schema: 2, Skills: map[string]map[string][]string{}}
	entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(skillsDir)))
	if err != nil {
		return h, err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		rel := skillsDir + "/" + name + "/SKILL.md"
		seen := map[string]bool{}
		add := func(text []byte) error {
			sum, err := bundleHash(text)
			if err == nil {
				seen[sum] = true
			}
			return err
		}
		current, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			return h, err
		}
		if err = add(current); err != nil {
			return h, fmt.Errorf("%s: %w", rel, err)
		}
		out, err := git(root, "log", "--format=%H", "--", rel)
		if err != nil {
			return h, err
		}
		for _, commit := range strings.Fields(string(out)) {
			text, err := git(root, "show", commit+":"+rel)
			if err != nil {
				continue // deleted in that commit
			}
			if err = add(text); err != nil {
				return h, fmt.Errorf("%s at %s: %w", rel, commit, err)
			}
		}
		hashes := make([]string, 0, len(seen))
		for sum := range seen {
			hashes = append(hashes, sum)
		}
		sort.Strings(hashes)
		h.Skills[name] = map[string][]string{variant: hashes}
	}
	return h, nil
}

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
	checkOnly := flag.Bool("check", false, "verify the file instead of writing it")
	flag.Parse()
	root, err := repoRoot()
	if err == nil {
		var h History
		if h, err = build(root); err == nil {
			var data []byte
			data, err = json.MarshalIndent(h, "", "  ")
			data = append(data, '\n')
			target := filepath.Join(root, filepath.FromSlash(output))
			if *checkOnly {
				old, readErr := os.ReadFile(target)
				if readErr != nil || !bytes.Equal(old, data) {
					fmt.Fprintln(os.Stderr, output+" is out of date. Run: go run ./tools/skillhistory")
					os.Exit(1)
				}
				fmt.Println(output + " is current")
				return
			}
			if err == nil {
				err = os.WriteFile(target, data, 0o644) // #nosec G306 -- a source file in the repo
			}
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "skillhistory:", err)
		os.Exit(1)
	}
}
