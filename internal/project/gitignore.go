package project

import (
	"os"
	"path/filepath"
	"strings"
)

// MissingGitignoreLines returns the lines of want that the project's
// .gitignore does not have yet, in order. Comments and blank lines in want
// are left out. Lines compare after trimming spaces.
//
// When the .gitignore already has rules, negations (!...) are left out of
// missing and returned in leftOut: appended after the project's own rules
// they would win over them, and could make files in folders the project
// ignores committable. A new or empty .gitignore gets them. R:
// .fw_gitignore_missing().
func MissingGitignoreLines(projectDir string, want []string) (missing, leftOut []string, err error) {
	data, err := os.ReadFile(filepath.Join(projectDir, ".gitignore"))
	if err != nil && !os.IsNotExist(err) {
		return nil, nil, err
	}
	have := map[string]bool{}
	hasRules := false
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		have[line] = true
		hasRules = hasRules || line != "" && !strings.HasPrefix(line, "#")
	}
	for _, line := range want {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || have[line] {
			continue
		}
		have[line] = true
		if hasRules && strings.HasPrefix(line, "!") {
			leftOut = append(leftOut, line)
			continue
		}
		missing = append(missing, line)
	}
	return missing, leftOut, nil
}

// AppendGitignore adds the lines of want that .gitignore lacks, under the
// comment header, and returns them and the negations it left out (see
// MissingGitignoreLines). It never changes or removes a line already there,
// and creates .gitignore when there is none.
func AppendGitignore(projectDir, header string, want []string) (added, leftOut []string, err error) {
	missing, leftOut, err := MissingGitignoreLines(projectDir, want)
	if err != nil || len(missing) == 0 {
		return missing, leftOut, err
	}
	path := filepath.Join(projectDir, ".gitignore")
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, nil, err
	}
	text := string(data)
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	if text != "" {
		text += "\n"
	}
	text += "# " + header + "\n" + strings.Join(missing, "\n") + "\n"
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil { // #nosec G306 -- .gitignore is not secret
		return nil, nil, err
	}
	return missing, leftOut, nil
}
