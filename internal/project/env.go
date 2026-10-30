// Package project reads Framework projects on disk.
package project

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// EnvValue returns key's value from the project's .env file, or "" when the
// file or key is missing. It follows R's .fw_project_env_value(): the first
// line starting with KEY= wins, and surrounding quotes are dropped.
func EnvValue(projectDir, key string) string {
	f, err := os.Open(filepath.Join(projectDir, ".env"))
	if err != nil {
		return ""
	}
	defer f.Close()

	prefix := key + "="
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(line, prefix))
		value = strings.TrimPrefix(strings.TrimPrefix(value, `"`), `'`)
		value = strings.TrimSuffix(strings.TrimSuffix(value, `"`), `'`)
		return value
	}
	return ""
}
