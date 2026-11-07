package project

import (
	"os"
	"path/filepath"
	"strings"
)

// MissingGitignoreLines returns the lines of want that the project's
// .gitignore does not have yet, in order. Comments and blank lines in want
// are left out. Lines compare after trimming spaces.
func MissingGitignoreLines(projectDir string, want []string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(projectDir, ".gitignore"))
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	have := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		have[strings.TrimSpace(line)] = true
	}
	var missing []string
	for _, line := range want {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || have[line] {
			continue
		}
		have[line] = true
		missing = append(missing, line)
	}
	return missing, nil
}

// AppendGitignore adds the lines of want that .gitignore lacks, under the
// comment header, and returns them. It never changes or removes a line
// already there, and creates .gitignore when there is none.
func AppendGitignore(projectDir, header string, want []string) ([]string, error) {
	missing, err := MissingGitignoreLines(projectDir, want)
	if err != nil || len(missing) == 0 {
		return missing, err
	}
	path := filepath.Join(projectDir, ".gitignore")
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
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
		return nil, err
	}
	return missing, nil
}
