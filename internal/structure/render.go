package structure

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

// MaxListed is how many ignored files a preview names per folder and rule.
const MaxListed = 20

// noteColumnMax is the widest a tree line gets before its note column; a
// wider line puts its note two spaces after it.
const noteColumnMax = 48

type line struct{ left, right string }

// Render prints the tree. A local preview names ignored files, up to
// MaxListed per folder and rule. Otherwise it prints the tree as it
// publishes, with one row per rule in place of the names.
func (t *Tree) Render(w io.Writer, local bool) {
	lines := []line{{left: t.Root + "/"}}
	var walk func(e *Entry, indent string)
	walk = func(e *Entry, indent string) {
		type row struct {
			line
			child *Entry
		}
		var rows []row
		for _, c := range e.Children {
			switch {
			case c.Type == Ignored && local:
				shown := c.Names[:min(len(c.Names), MaxListed)]
				for _, name := range shown {
					rows = append(rows, row{line: line{left: name, right: "ignored by " + ruleText(c.Ignored)}})
				}
				if more := len(c.Names) - len(shown); more > 0 {
					rows = append(rows, row{line: line{left: "… " + thousands(more) + " more", right: "ignored by " + ruleText(c.Ignored)}})
				}
			case c.Type == Ignored:
				rows = append(rows, row{line: line{left: c.Name, right: annotate(c)}})
			case c.Type == Dir:
				rows = append(rows, row{line: line{left: c.Name + "/", right: annotate(c)}, child: c})
			default:
				rows = append(rows, row{line: line{left: c.Name, right: c.Note}})
			}
		}
		for i, r := range rows {
			branch, next := "├── ", "│   "
			if i == len(rows)-1 {
				branch, next = "└── ", "    "
			}
			lines = append(lines, line{left: indent + branch + r.left, right: r.right})
			if r.child != nil && !r.child.Stub {
				walk(r.child, indent+next)
			}
		}
	}
	walk(t.Top, "")

	width := 0
	for _, l := range lines {
		if n := utf8.RuneCountInString(l.left); l.right != "" && n <= noteColumnMax && n > width {
			width = n
		}
	}
	for _, l := range lines {
		if l.right == "" {
			fmt.Fprintln(w, l.left)
			continue
		}
		pad := max(width-utf8.RuneCountInString(l.left), 0) + 2
		fmt.Fprintln(w, l.left+strings.Repeat(" ", pad)+l.right)
	}
}

// annotate is a folder's or group's note column: its note, why its
// contents aren't listed, and how many files they are.
func annotate(e *Entry) string {
	var parts []string
	if e.Note != "" {
		parts = append(parts, e.Note)
	}
	if e.Type == Ignored {
		return strings.Join(append(parts, "ignored by "+ruleText(e.Ignored), Count(e.Hidden)), " · ")
	}
	if !e.Stub {
		return strings.Join(parts, " · ")
	}
	if e.Ignored != nil {
		parts = append(parts, "ignored by "+ruleText(e.Ignored))
	}
	if e.Private {
		parts = append(parts, "private")
	}
	return strings.Join(append(parts, Count(e.Hidden)), " · ")
}

// ruleText is "*.rds (.gitignore:7)".
func ruleText(r *Rule) string {
	if s := sourceText(r); s != "" {
		return r.Rule + " (" + s + ")"
	}
	return r.Rule
}

func sourceText(r *Rule) string {
	source := r.Source
	switch source {
	case "global":
		source = "global excludes"
	case "info/exclude":
		source = ".git/info/exclude"
	}
	if r.Line > 0 {
		source += ":" + strconv.Itoa(r.Line)
	}
	return source
}

// Count is "1 file", "1,520 files", or "10,000+ files" at the cap.
func Count(n int) string {
	switch {
	case n >= MaxHidden:
		return thousands(MaxHidden) + "+ files"
	case n == 1:
		return "1 file"
	}
	return thousands(n) + " files"
}

func thousands(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
