package project

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// LinkCloud adds FW_PROJECT_TOKEN and FW_CLOUD_URL to the project's .env,
// keeping any value already there, as R's setup does. An empty entry, as
// .env.example leaves, is filled in. It returns the keys it added. The file
// is created with mode 0600.
func LinkCloud(projectDir, token, url string) ([]string, error) {
	path := filepath.Join(projectDir, ".env")
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	text := string(data)
	var add []string
	var added []string
	for _, kv := range [][2]string{{"FW_PROJECT_TOKEN", token}, {"FW_CLOUD_URL", url}} {
		if hasKey(text, kv[0]) {
			continue
		}
		added = append(added, kv[0])
		if filled, ok := fillEmpty(text, kv[0], kv[1]); ok {
			text = filled
			continue
		}
		if filled, ok := fillPlaceholder(text, kv[0], kv[1]); ok {
			text = filled
			continue
		}
		add = append(add, kv[0]+"="+kv[1])
	}
	if len(added) == 0 {
		return nil, nil
	}
	if len(add) > 0 {
		if text != "" && !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		text += strings.Join(add, "\n") + "\n"
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		return nil, err
	}
	return added, nil
}

// hasKey reports whether .env sets key to a value.
func hasKey(text, key string) bool {
	for _, line := range strings.Split(text, "\n") {
		if value, ok := strings.CutPrefix(line, key+"="); ok && !emptyValue(value) {
			return true
		}
	}
	return false
}

func emptyValue(value string) bool {
	switch strings.TrimSpace(value) {
	case "", `""`, "''":
		return true
	}
	return false
}

// fillEmpty sets the first empty key= line to value.
func fillEmpty(text, key, value string) (string, bool) {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if rest, ok := strings.CutPrefix(line, key+"="); ok && emptyValue(strings.TrimSuffix(rest, "\r")) {
			lines[i] = key + "=" + value
			return strings.Join(lines, "\n"), true
		}
	}
	return text, false
}

// connectHint is the instruction Framework Cloud's project download puts
// above its commented "# FW_PROJECT_TOKEN=" line. It is stale once the token
// is written.
const (
	connectHintFirst = "# Framework Cloud. To connect this project, get a project token at"
	connectHintLast  = "# then remove the # in front of FW_PROJECT_TOKEN below and paste it after the ="
)

// fillPlaceholder replaces the first commented "# key=" line with key=value
// and drops the download's instruction comments above it. Every other line
// is kept as is.
func fillPlaceholder(text, key, value string) (string, bool) {
	lines := strings.Split(text, "\n")
	at := -1
	for i, line := range lines {
		rest, ok := strings.CutPrefix(strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "#")), key+"=")
		if strings.HasPrefix(strings.TrimSpace(line), "#") && ok && emptyValue(rest) {
			at = i
			break
		}
	}
	if at < 0 {
		return text, false
	}
	cr := ""
	if strings.HasSuffix(lines[at], "\r") {
		cr = "\r"
	}
	lines[at] = key + "=" + value + cr

	// The hint sits a few lines above, with only settings in between, as in
	// "# hint ...\nFW_CLOUD_URL=...\n# FW_PROJECT_TOKEN=".
	last := -1
	for i := at - 1; i >= 0 && i >= at-3; i-- {
		line := strings.TrimSpace(lines[i])
		if strings.HasPrefix(line, connectHintLast) {
			last = i
			break
		}
		if line == "" || strings.HasPrefix(line, "#") {
			break
		}
	}
	if last >= 0 {
		first := last
		for i := last - 1; i >= 0 && i >= last-3; i-- {
			line := strings.TrimSpace(lines[i])
			if !strings.HasPrefix(line, "#") {
				break
			}
			if strings.HasPrefix(line, connectHintFirst) {
				first = i
				break
			}
		}
		lines = append(lines[:first], lines[last+1:]...)
	}
	return strings.Join(lines, "\n"), true
}

// EnvTracked reports whether git tracks the project's .env, so a token
// written there would be committed. It is false outside a git repository or
// when git is not installed.
func EnvTracked(projectDir string) bool {
	cmd := exec.Command("git", "ls-files", "--error-unmatch", ".env")
	cmd.Dir = projectDir
	return cmd.Run() == nil
}

// GitignoresEnv reports whether git would ignore the project's .env. In a
// git repository git decides. Otherwise the project's .gitignore does: the
// last line about .env wins, so "!.env" after ".env" un-ignores it.
func GitignoresEnv(projectDir string) bool {
	cmd := exec.Command("git", "check-ignore", "-q", ".env")
	cmd.Dir = projectDir
	err := cmd.Run()
	if err == nil {
		return true
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return false
	}
	// No git, or not a repository
	data, err := os.ReadFile(filepath.Join(projectDir, ".gitignore"))
	if err != nil {
		return false
	}
	ignored := false
	for _, line := range strings.Split(string(data), "\n") {
		rule := strings.TrimSpace(line)
		negate := strings.HasPrefix(rule, "!")
		switch strings.TrimPrefix(rule, "!") {
		case ".env", "/.env", "*.env", ".env*", "**/.env":
			ignored = !negate
		}
	}
	return ignored
}

// IgnoreEnv makes sure git ignores the project's .env before a token is
// written there, appending a rule to .gitignore (and creating it) when
// needed, as the R and Python packages do. It reports whether it changed
// .gitignore.
func IgnoreEnv(projectDir string) (bool, error) {
	if GitignoresEnv(projectDir) {
		return false, nil
	}
	path := filepath.Join(projectDir, ".gitignore")
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	text := string(data)
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	if text != "" {
		text += "\n"
	}
	text += "# Secrets (added by Framework)\n.env\n"
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil { // #nosec G306 -- .gitignore is not secret
		return false, err
	}
	return true, nil
}
