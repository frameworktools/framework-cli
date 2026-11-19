package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/frameworktools/framework-cli/internal/skillsync"
	"github.com/spf13/cobra"
)

// copyJSON is fw skills copy --json.
type copyJSON struct {
	From    string               `json:"from"`
	To      []string             `json:"to"`
	DryRun  bool                 `json:"dry_run"`
	Skills  []skillsync.CopyItem `json:"skills"`
	Counts  map[string]int       `json:"counts"`
	Backups []string             `json:"backups"`
	Message string               `json:"message"`
}

func newSkillsCopyCmd(a *app) *cobra.Command {
	var from, to, fromDir, toDir string
	var skills []string
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "copy",
		Short: "Copy skills from one assistant's skills folder to the others",
		Long: "Copies skills from one skills folder in the project to others, so every\n" +
			"assistant reads the same copy. Use it after you edit a skill in one folder:\n" +
			"until the copies match, skill sync stops with \"assistant skill copies\n" +
			"diverged\".\n\n" +
			"--from and --to name an assistant: claude is .claude/skills, and agents is\n" +
			".agents/skills (codex and copilot read it too). --from-dir and --to-dir take\n" +
			"folders relative to the project instead. Without --to or --to-dir, fw copies\n" +
			"to every other assistant skills folder the project has.\n\n" +
			"A skill is a folder with a SKILL.md file; folders whose names start with a dot\n" +
			"are skipped. fw copies SKILL.md and its syncable companion files, with their\n" +
			"executable modes. Files sync excludes, such as .env, are not copied and stay\n" +
			"in the target. fw moves each copy it replaces to .framework/skills-backups/\n" +
			"first. Other files that only the replaced copy had are gone from it, and kept\n" +
			"in the backup. fw skips copies that already match and never deletes a skill.\n" +
			"Skills only in a target are listed, not read or changed. No target may be in\n" +
			".framework/. When the source skill is tracked in .framework/skills.lock.json,\n" +
			"fw records each copy there as a copy of it, keeping its version, so pull moves\n" +
			"every copy together and fw skills push still sends an edited skill. A folder\n" +
			"the lock records as a different skill is never copied over. --skill copies\n" +
			"only the skills it names. --dry-run writes nothing.\n\n" +
			"Known limits: use one skill per folder name in a project. Copy skills between\n" +
			"assistants with fw skills copy, not by hand: a copy made by hand isn't tracked,\n" +
			"and pull leaves it alone. On macOS, folder names that differ only in letter\n" +
			"case are the same folder; pick one spelling. When a command refuses, it has\n" +
			"changed nothing. Follow its message.",
		Example: "  fw skills copy --from claude --to agents\n" +
			"  fw skills copy --from-dir .claude/skills --to-dir some/other/skills\n" +
			"  fw skills copy --from claude --skill ggplot2-figures --dry-run",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			root := a.projectRoot()
			if root == "" {
				return errors.New("not inside a Framework project (no .framework folder, settings.yml, settings.yaml, settings.do, or Framework config.yml found)")
			}
			f := cmd.Flags()
			source, targets, err := copyFolders(copyFlags{from: from, fromDir: fromDir, to: to, toDir: toDir,
				fromSet: f.Changed("from"), fromDirSet: f.Changed("from-dir"), toSet: f.Changed("to"), toDirSet: f.Changed("to-dir")})
			if err != nil {
				return err
			}
			res, err := skillsync.Copy(skillsync.CopyOptions{Root: root, From: source, To: targets, Skills: skills, DryRun: dryRun})
			if err != nil {
				return err
			}
			// The folders as the filesystem spells them
			out := copyJSON{From: res.From, To: res.To, DryRun: dryRun, Skills: res.Skills, Counts: res.Counts, Backups: res.Backups,
				Message: copySummary(res.From, res.Counts, dryRun)}
			if a.jsonOut {
				return writeJSON(cmd.OutOrStdout(), out)
			}
			printCopy(cmd.OutOrStdout(), out)
			return nil
		},
	}
	cmd.Flags().StringVar(&from, "from", "", "the assistant to copy from: claude or agents (codex and copilot also mean agents)")
	cmd.Flags().StringVar(&to, "to", "", "the assistant to copy to (default: every other assistant skills folder in the project)")
	cmd.Flags().StringVar(&fromDir, "from-dir", "", "the skills folder to copy from, relative to the project")
	cmd.Flags().StringVar(&toDir, "to-dir", "", "the skills folder to copy to, relative to the project")
	cmd.Flags().StringArrayVar(&skills, "skill", nil, "copy only this skill (repeatable; default: every skill in the source)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "report what would change without writing anything")
	cmd.Flags().BoolVar(&a.jsonOut, "json", false, jsonHelp)
	return cmd
}

// copyFlags are fw skills copy's folder flags, and whether each was given.
// A flag given with an empty value is an error, never the default.
type copyFlags struct {
	from, fromDir, to, toDir             string
	fromSet, fromDirSet, toSet, toDirSet bool
}

// copyFolders turns fw skills copy's flags into project-relative folders:
// the source, then the target, or none for every other assistant skills
// folder. It only reads the flags: skillsync.Copy checks the project, after
// it takes the sync lock.
func copyFolders(f copyFlags) (string, []string, error) {
	switch {
	case f.fromSet && f.fromDirSet:
		return "", nil, usageError("pass --from or --from-dir, not both")
	case f.toSet && f.toDirSet:
		return "", nil, usageError("pass --to or --to-dir, not both")
	case !f.fromSet && !f.fromDirSet:
		return "", nil, usageError("pass --from claude, --from agents, or --from-dir DIR")
	case (f.fromSet && f.toDirSet) || (f.fromDirSet && f.toSet):
		return "", nil, usageError("use assistant names (--from and --to) or folders (--from-dir and --to-dir), not one of each")
	}
	var source, target string
	var err error
	if f.fromSet {
		if source, err = assistantFolder("--from", f.from); err != nil {
			return "", nil, err
		}
		if f.toSet {
			if target, err = assistantFolder("--to", f.to); err != nil {
				return "", nil, err
			}
		}
	} else {
		if source, err = projectFolder("--from-dir", f.fromDir); err != nil {
			return "", nil, err
		}
		if f.toDirSet {
			if target, err = projectFolder("--to-dir", f.toDir); err != nil {
				return "", nil, err
			}
		}
	}
	if target == source {
		return "", nil, usageError(fmt.Sprintf("the source and target are both %s; pick another folder to copy to", source))
	}
	if target == "" {
		return source, nil, nil
	}
	return source, []string{target}, nil
}

// assistantFolder is the skills folder an assistant name stands for.
func assistantFolder(flag, name string) (string, error) {
	if dir := skillsync.AssistantSkillsDir(strings.ToLower(name)); dir != "" {
		return dir, nil
	}
	return "", usageError(fmt.Sprintf("%s takes %s; for another folder, use %s-dir", flag, strings.Join(skillsync.AssistantNames(), ", "), flag))
}

// projectFolder checks a project-relative folder as given, then cleans it
// to the form fw records.
func projectFolder(flag, dir string) (string, error) {
	clean, err := skillsync.CleanFolder(dir)
	if err != nil {
		return "", usageError(fmt.Sprintf("%s takes a folder inside the project, relative to its root: %v", flag, err))
	}
	return clean, nil
}

// copySummary is the one-line summary, with the parts that aren't zero.
func copySummary(source string, counts map[string]int, dryRun bool) string {
	copied := "copied"
	prefix := "Skills from " + source + ": "
	if dryRun {
		copied = "to copy"
		prefix = "Skills (dry run, nothing written) from " + source + ": "
	}
	parts := []string{}
	for _, p := range []struct{ key, label string }{
		{skillsync.StatusCopied, copied}, {skillsync.StatusCurrent, "up to date"}, {skillsync.StatusMissing, "not in " + source + " (kept)"},
	} {
		if n := counts[p.key]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, p.label))
		}
	}
	if len(parts) == 0 {
		return prefix + "none"
	}
	return prefix + strings.Join(parts, ", ")
}

// printCopy prints the summary, then a line for each skill copied or kept.
// Skills already up to date are only counted.
func printCopy(w io.Writer, out copyJSON) {
	fmt.Fprintln(w, out.Message)
	for _, s := range out.Skills {
		switch {
		case s.Status == skillsync.StatusCopied && out.DryRun:
			fmt.Fprintf(w, "  Would copy: %s -> %s\n", s.From, s.To)
		case s.Status == skillsync.StatusCopied:
			fmt.Fprintf(w, "  Copied: %s -> %s\n", s.From, s.To)
			if s.Backup != "" {
				fmt.Fprintf(w, "    Backup: %s\n", s.Backup)
			}
		case s.Status == skillsync.StatusMissing:
			fmt.Fprintf(w, "  Not in %s (kept): %s\n", out.From, s.To)
		}
	}
}
