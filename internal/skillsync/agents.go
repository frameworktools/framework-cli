package skillsync

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// The descriptions R's AGENTS.md gives Framework's own skills. Other skills
// use the description in their SKILL.md front matter.
var skillDescriptions = map[string]string{
	"framework-workflow":       "scaffold() initialization, critical rules, creating notebooks/scripts",
	"framework-data":           "reading and saving data (data_read/data_save are mandatory)",
	"framework-packages":       "adding and managing R packages (package_add, never install.packages)",
	"framework-outputs":        "results, caching, database queries, publishing",
	"framework-sensitive-data": "PII/PHI handling and private/public directory rules",
	"ggplot2-exploring":        "quick, accurate ggplot2 figures for understanding data",
	"ggplot2-publishing":       "polished ggplot2 figures for papers, reports, and slides",
}

var skillsHeading = regexp.MustCompile(`^## Skills\b`)

// SkillsTable is the body of AGENTS.md's Skills section, as R writes it.
func SkillsTable(root, dir string, names []string) string {
	lines := []string{
		"Detailed instructions live in skill files, loaded on demand. Claude Code discovers",
		"them automatically; other agents should read the relevant SKILL.md before working",
		"on a matching task.",
		"",
		"| Skill | Covers | Path |",
		"|-------|--------|------|",
	}
	for _, name := range names {
		desc, ok := skillDescriptions[name]
		if !ok {
			desc = frontmatterDescription(filepath.Join(root, filepath.FromSlash(dir), name, "SKILL.md"))
		}
		lines = append(lines, fmt.Sprintf("| `%s` | %s | `%s/%s/SKILL.md` |", name, desc, dir, name))
	}
	return strings.Join(lines, "\n")
}

// frontmatterDescription is a SKILL.md's description on one line, with
// pipes escaped so the table holds.
func frontmatterDescription(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	if len(lines) < 2 || strings.TrimSpace(lines[0]) != "---" {
		return ""
	}
	for i := 1; i < len(lines) && strings.TrimSpace(lines[i]) != "---"; i++ {
		if rest, ok := strings.CutPrefix(lines[i], "description:"); ok {
			text := strings.Trim(strings.TrimSpace(rest), `"'`)
			// A folded or literal block: the indented lines that follow
			if text == ">" || text == "|" || text == ">-" || text == "|-" {
				text = ""
				for j := i + 1; j < len(lines) && strings.HasPrefix(lines[j], " "); j++ {
					text += " " + strings.TrimSpace(lines[j])
				}
			}
			return strings.ReplaceAll(strings.Join(strings.Fields(text), " "), "|", `\|`)
		}
	}
	return ""
}

// RefreshSkillsTable rewrites the Skills section of the project's AI context
// file (AGENTS.md, or CLAUDE.md in older projects) to list names in dir. A
// file without a Skills section is left alone. It returns the file it
// changed, or "".
func RefreshSkillsTable(root, dir string, names []string) (string, error) {
	file := "AGENTS.md"
	if _, err := os.Stat(filepath.Join(root, file)); err != nil {
		file = "CLAUDE.md"
	}
	path := filepath.Join(root, file)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", nil
	}
	text := string(data)
	lines := strings.Split(text, "\n")
	start := -1
	for i, line := range lines {
		if skillsHeading.MatchString(line) {
			start = i
			break
		}
	}
	if start < 0 {
		return "", nil
	}
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "## ") || strings.HasPrefix(lines[i], "# ") {
			end = i
			break
		}
	}
	body := append([]string{lines[start], ""}, strings.Split(SkillsTable(root, dir, names), "\n")...)
	if end < len(lines) {
		body = append(body, "")
	}
	out := strings.Join(append(append(append([]string{}, lines[:start]...), body...), lines[end:]...), "\n")
	if out == text {
		return "", nil
	}
	return file, os.WriteFile(path, []byte(out), 0644)
}
