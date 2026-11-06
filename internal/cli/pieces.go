package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/frameworktools/framework-cli/internal/cloud"
)

// pieceKind describes one list of blueprint pieces on Framework Cloud.
type pieceKind struct {
	use, short, long, example string
	path, field, noun         string
	header                    string
	row                       func(cloud.Piece) string
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func listText(items []string, limit int) string {
	if len(items) == 0 {
		return "-"
	}
	if len(items) > limit {
		return strings.Join(items[:limit], ", ") + fmt.Sprintf(" and %d more", len(items)-limit)
	}
	return strings.Join(items, ", ")
}

var (
	structuresKind = pieceKind{
		use: "structures", short: "List project structures on Framework Cloud",
		long: "Lists the project structures you can start a project from: Framework's,\n" +
			"your own, and the ones shared with you. A structure is the folders and\n" +
			"Framework's core files. Pass a key to fw new --structure.",
		example: "  fw structures list\n  fw structures list --lang stata --json",
		path:    "structures", field: "structures", noun: "project structures",
		header: "KEY\tNAME\tTYPE\tVERSION",
		row: func(p cloud.Piece) string {
			return fmt.Sprintf("%s\t%s\t%s\t%d", p.Key, p.Name, orDash(p.Type), p.Version)
		},
	}
	toolsKind = pieceKind{
		use: "tools", short: "List the tools a project can turn on",
		long: "Lists the tools a project can turn on, such as Quarto, targets, renv, uv,\n" +
			"and Jupyter, with the languages they work with and the tools they need.\n" +
			"Pass an id to fw new --tool. Stata projects have no tools.",
		example: "  fw tools list\n  fw tools list --lang python --json",
		path:    "tools", field: "tools", noun: "tools",
		header: "KEY\tNAME\tLANGUAGES\tNEEDS",
		row: func(p cloud.Piece) string {
			return fmt.Sprintf("%s\t%s\t%s\t%s", p.Key, p.Name, listText(p.Language, 3), listText(p.Requires, 3))
		},
	}
	packageSetsKind = pieceKind{
		use: "package-sets", short: "List package sets on Framework Cloud",
		long: "Lists named sets of packages for one language, such as a tidyverse\n" +
			"analysis set. Pass a key to fw new --package-set.",
		example: "  fw package-sets list --lang r\n  fw package-sets list --json",
		path:    "package-sets", field: "package_sets", noun: "package sets",
		header: "KEY\tNAME\tLANGUAGE\tPACKAGES",
		row: func(p cloud.Piece) string {
			return fmt.Sprintf("%s\t%s\t%s\t%s", p.Key, p.Name, listText(p.Language, 3), listText(p.Packages, 4))
		},
	}
	skillsKind = pieceKind{
		use: "skills", short: "List skills and skill sets for AI assistants",
		long: "Lists the skills fw new can add for AI assistants. --sets lists skill\n" +
			"sets instead: named groups of skills, with the packages they need. Pass\n" +
			"keys to fw new --skill or --skill-set.",
		example: "  fw skills list\n  fw skills list --sets --lang r --json",
		path:    "skills", field: "skills", noun: "skills",
		header: "KEY\tNAME\tVERSION",
		row: func(p cloud.Piece) string {
			return fmt.Sprintf("%s\t%s\t%d", p.Key, p.Name, p.Version)
		},
	}
	skillSetsKind = pieceKind{
		path: "skill-sets", field: "skill_sets", noun: "skill sets",
		header: "KEY\tNAME\tLANGUAGE\tSKILLS",
		row: func(p cloud.Piece) string {
			return fmt.Sprintf("%s\t%s\t%s\t%s", p.Key, p.Name, listText(p.Language, 3), listText(p.Skills, 3))
		},
	}
)

func newPiecesCmd(a *app, kind pieceKind) *cobra.Command {
	var urlFlag, lang string
	var sets bool
	cmd := &cobra.Command{Use: kind.use, Short: kind.short}
	if kind.use == "skills" {
		cmd.Short = "List skills for AI assistants and sync your own"
		cmd.AddCommand(newSkillSyncCmd(a))
	}
	cmd.PersistentFlags().StringVar(&urlFlag, "url", "", "Framework Cloud server")
	list := &cobra.Command{
		Use: "list", Short: kind.short, Long: kind.long, Example: kind.example,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			switch lang {
			case "", "r", "python", "stata":
			default:
				return usageError("--lang takes r, python, or stata")
			}
			k := kind
			if sets {
				k = skillSetsKind
			}
			client, err := a.userClient(urlFlag)
			if err != nil {
				return err
			}
			pieces, err := client.Pieces(cmd.Context(), k.path, k.field, lang)
			if err != nil {
				return explain(err, client.URL)
			}
			out := cmd.OutOrStdout()
			if a.jsonOut {
				return writePiecesJSON(out, client.URL, k.field, pieces)
			}
			if len(pieces) == 0 {
				if k.field == "tools" && lang == "stata" {
					fmt.Fprintln(out, "Stata projects have no tools.")
					return nil
				}
				fmt.Fprintf(out, "No %s on %s.\n", k.noun, cloud.Host(client.URL))
				return nil
			}
			w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, k.header)
			for _, p := range pieces {
				fmt.Fprintln(w, k.row(p))
			}
			return w.Flush()
		},
	}
	list.Flags().StringVar(&lang, "lang", "", "only pieces for this language: r, python, or stata")
	if kind.field == "skills" {
		list.Flags().BoolVar(&sets, "sets", false, "list skill sets instead of single skills")
	}
	list.Flags().BoolVar(&a.jsonOut, "json", false, jsonHelp)
	cmd.AddCommand(list)
	if kind.field == "structures" {
		cmd.Aliases = []string{"structure"}
		cmd.AddCommand(newStructureImportCmd(a))
	}
	return cmd
}

// writePiecesJSON prints {"server": ..., "<field>": [...]} with the server's
// objects as they came.
func writePiecesJSON(w interface{ Write([]byte) (int, error) }, server, field string, pieces []cloud.Piece) error {
	raws := make([]json.RawMessage, 0, len(pieces))
	for _, p := range pieces {
		raws = append(raws, p.Raw)
	}
	s, _ := json.Marshal(server)
	list, err := json.Marshal(raws)
	if err != nil {
		return err
	}
	doc := `{"server":` + string(s) + `,` + strconv.Quote(field) + `:` + string(list) + `}`
	var buf bytes.Buffer
	if err := json.Indent(&buf, []byte(doc), "", "  "); err != nil {
		return err
	}
	buf.WriteByte('\n')
	_, err = w.Write(buf.Bytes())
	return err
}
