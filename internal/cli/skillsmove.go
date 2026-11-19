package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/frameworktools/framework-cli/internal/cloud"
	"github.com/frameworktools/framework-cli/internal/skillsync"
	"github.com/spf13/cobra"
)

// moveJSON is fw skills move --json.
type moveJSON struct {
	DryRun  bool                 `json:"dry_run"`
	Skills  []skillsync.MoveItem `json:"skills"`
	Message string               `json:"message"`
}

func newSkillsMoveCmd(a *app) *cobra.Command {
	var id, variant, builtin string
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "move FROM [TO]",
		Short: "Move a tracked skill to another folder, or change which skill it tracks",
		Long: "Moves a skill's copy to another folder and records the move in\n" +
			".framework/skills.lock.json, so pull and push keep treating it as the same\n" +
			"skill. FROM and TO are a skill folder, such as .claude/skills/analysis, or a\n" +
			"skills folder, whose tracked copies all move. claude stands for .claude/skills\n" +
			"and agents for .agents/skills (codex and copilot read it too). Other folders\n" +
			"are relative to the project. When you already moved the folder by hand, fw\n" +
			"records the move and leaves the files alone.\n\n" +
			"--id, --builtin, and --variant change which skill a copy tracks: your Cloud\n" +
			"skill with that ID, a built-in Framework skill by name, or another variant of\n" +
			"the same skill. The copy keeps its files. An unedited copy takes the new\n" +
			"skill's version at the next pull; push refuses an edited one until --force.\n\n" +
			"fw refuses, and changes nothing, when FROM isn't tracked, when TO exists or\n" +
			"belongs to another skill, or when the lock already tracks the new identity.\n" +
			"--dry-run writes nothing.",
		Example: "  fw skills move claude agents\n" +
			"  fw skills move .claude/skills/analysis .agents/skills/analysis\n" +
			"  fw skills move .claude/skills/framework-data --id 5d0c2a7e-... --dry-run\n" +
			"  fw skills move .claude/skills/analysis --variant stata",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := a.projectRoot()
			if root == "" {
				return errors.New("not inside a Framework project (no .framework folder, settings.yml, settings.yaml, settings.do, or Framework config.yml found)")
			}
			switch variant {
			case "", "r", "python", "stata":
			default:
				return usageError("--variant takes r, python, or stata")
			}
			if id != "" && builtin != "" {
				return usageError("pass --id or --builtin, not both")
			}
			from, err := moveFolder(args[0])
			if err != nil {
				return err
			}
			to := ""
			if len(args) == 2 {
				if to, err = moveFolder(args[1]); err != nil {
					return err
				}
			}
			if to == "" && id == "" && builtin == "" && variant == "" {
				return usageError("name where to move " + from + ", or the skill it should track with --id, --builtin, or --variant")
			}
			o := skillsync.MoveOptions{Root: root, From: from, To: to, SkillID: id, Builtin: builtin, Variant: variant, DryRun: dryRun}
			if id != "" && !strings.HasPrefix(id, "framework:") && !strings.HasPrefix(id, "inline:") {
				url, _ := cmd.Parent().PersistentFlags().GetString("url")
				srv, err := a.server(url, cloud.ProjectScope)
				if err != nil {
					return err
				}
				o.Server = srv.URL
			}
			res, err := skillsync.Move(o)
			if err != nil {
				return err
			}
			out := moveJSON{DryRun: dryRun, Skills: res.Skills, Message: moveSummary(res.Skills, dryRun)}
			if a.jsonOut {
				return writeJSON(cmd.OutOrStdout(), out)
			}
			printMove(cmd.OutOrStdout(), out)
			return nil
		},
	}
	cmd.Flags().StringVar(&id, "id", "", "track the copy as this skill: your Cloud skill's ID, or framework:NAME for a built-in")
	cmd.Flags().StringVar(&builtin, "builtin", "", "track the copy as this built-in Framework skill, such as framework-data")
	cmd.Flags().StringVar(&variant, "variant", "", "track the copy as this variant: r, python, or stata (default: its current one)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "report what would change without writing anything")
	cmd.Flags().BoolVar(&a.jsonOut, "json", false, jsonHelp)
	return cmd
}

// moveFolder is a move argument as a project-relative path: an assistant
// name stands for its skills folder.
func moveFolder(arg string) (string, error) {
	if dir := skillsync.AssistantSkillsDir(strings.ToLower(arg)); dir != "" {
		return dir, nil
	}
	clean, err := skillsync.CleanFolder(arg)
	if err != nil {
		return "", usageError(fmt.Sprintf("fw skills move takes an assistant (%s) or a folder inside the project, relative to its root: %v",
			strings.Join(skillsync.AssistantNames(), ", "), err))
	}
	return clean, nil
}

// moveSummary is the one-line summary.
func moveSummary(items []skillsync.MoveItem, dryRun bool) string {
	prefix := "Skills moved: "
	if dryRun {
		prefix = "Skills (dry run, nothing written): "
	}
	switch n := len(items); {
	case n == 0:
		return prefix + "none"
	case dryRun && n == 1:
		return prefix + "1 to move"
	case dryRun:
		return fmt.Sprintf("%s%d to move", prefix, n)
	}
	return fmt.Sprintf("%s%d", prefix, len(items))
}

// printMove prints the summary, then a line per copy.
func printMove(w io.Writer, out moveJSON) {
	fmt.Fprintln(w, out.Message)
	for _, it := range out.Skills {
		switch it.ReasonCode {
		case skillsync.ReasonPathMoved:
			fmt.Fprintf(w, "  Moved: %s -> %s\n", *it.From, it.To)
		case skillsync.ReasonLockMoved:
			fmt.Fprintf(w, "  Recorded the move: %s -> %s (the folder was already there)\n", *it.From, it.To)
		case skillsync.ReasonRetargeted:
			fmt.Fprintf(w, "  Now tracks %s: %s\n", it.Skill, it.To)
		}
	}
}
