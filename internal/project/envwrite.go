package project

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// LinkCloud adds FW_PROJECT_TOKEN and FW_CLOUD_URL to the project's .env,
// keeping any value already there, as R's setup does. It returns the keys it
// added. The file is created with mode 0600.
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
		add = append(add, kv[0]+"="+kv[1])
		added = append(added, kv[0])
	}
	if len(add) == 0 {
		return nil, nil
	}
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	text += strings.Join(add, "\n") + "\n"
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		return nil, err
	}
	return added, nil
}

func hasKey(text, key string) bool {
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, key+"=") {
			return true
		}
	}
	return false
}

// EnvTracked reports whether git tracks the project's .env, so a token
// written there would be committed. It is false outside a git repository or
// when git is not installed.
func EnvTracked(projectDir string) bool {
	cmd := exec.Command("git", "ls-files", "--error-unmatch", ".env")
	cmd.Dir = projectDir
	return cmd.Run() == nil
}

// GitignoresEnv reports whether the project's .gitignore has a line that
// ignores .env.
func GitignoresEnv(projectDir string) bool {
	data, err := os.ReadFile(filepath.Join(projectDir, ".gitignore"))
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		switch strings.TrimSpace(line) {
		case ".env", "/.env", "*.env", ".env*", "**/.env":
			return true
		}
	}
	return false
}
