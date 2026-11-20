package skillsync

import (
	"fmt"
	"strings"
)

// Commands names the fw commands a hint renders: which pull and push the
// person ran, so --overwrite and --force go with the right one.
type Commands struct {
	Pull, Push string
}

// DefaultCommands are the fw skills commands.
var DefaultCommands = Commands{Pull: "fw skills pull", Push: "fw skills push"}

// copyCommands are the fw skills copy commands a copy hint stands for: one
// per target, from its source.
func copyCommands(from string, to []string) []string {
	var out []string
	for _, t := range to {
		out = append(out, copyTo(from, t))
	}
	return out
}

// Command renders a hint as the exact fw command, or "" when the person
// must choose (a copy hint with pick).
func (h *Hint) Command(c Commands) string {
	if h == nil {
		return ""
	}
	switch h.Key {
	case HintOverwrite:
		return c.Pull + " --overwrite"
	case HintForce:
		return c.Push + " --force"
	case HintLogin:
		return "fw login"
	case HintSync:
		return "fw skills sync " + quoteArg(h.Path)
	case HintMove:
		to := ""
		if len(h.To) > 0 {
			to = h.To[0]
		}
		return "fw skills move " + quoteArg(h.From) + " " + quoteArg(to)
	case HintRetarget:
		return retargetCommand(h.Path, h.Identity)
	case HintCopy:
		if h.Pick || h.From == "" {
			return ""
		}
		return strings.Join(copyCommands(h.From, h.To), " && ")
	}
	return ""
}

// retargetCommand is the fw skills move command that switches a copy to id.
func retargetCommand(p string, id *Identity) string {
	cmd := "fw skills move " + quoteArg(p)
	if id == nil {
		return cmd
	}
	switch id.Kind {
	case KindBuiltin:
		cmd += " --builtin " + quoteArg(strings.TrimPrefix(id.SkillID, "framework:"))
	default:
		cmd += " --id " + quoteArg(id.SkillID)
	}
	return cmd + " --variant " + id.Variant
}

// describe is a row's reason, one or two plain sentences, and its hint as
// the exact command, for fw's output. Success rows have neither.
func describe(root string, r Row, c Commands, push bool) (string, string) {
	hint := r.Hint.Command(c)
	first := ""
	if len(r.Paths) > 0 {
		first = r.Paths[0]
	}
	with := func(lead, run string) (string, string) {
		if hint == "" {
			return lead, ""
		}
		return lead + " " + run + hint, hint
	}
	verb := "pull"
	if push {
		verb = "push"
	}
	switch r.Reason {
	case ReasonLocalEdits:
		return with(listPaths(r.Paths)+" "+has(r.Paths)+" local edits, which were kept.", "To replace them with the setup's version: ")
	case ReasonReplacedEdits:
		return listPaths(r.Paths) + " had local edits, which were backed up to .framework/skills-backups/ and replaced.", ""
	case ReasonNewerInProject:
		if r.Status == StatusEdited {
			return listPaths(r.Paths) + " " + has(r.Paths) + " local edits, which were kept. This project has a newer version than the setup names, so --overwrite leaves them too.", ""
		}
		return "This project has a newer version of " + r.Name + " than the setup names, so " + verb + " kept it.", ""
	case ReasonCopiesDiffer:
		lead := listPaths(r.Hint.copyPaths()) + " differ"
		if r.Hint == nil || r.Hint.Pick || r.Hint.From == "" {
			var options []string
			for _, from := range r.Hint.copyPaths() {
				options = append(options, strings.Join(copyCommands(from, others(r.Hint.copyPaths(), from)), " && "))
			}
			return lead + ", and more than one has local edits. Pick the one to keep and copy it over the others: " + strings.Join(options, ", or "), ""
		}
		return with(lead+": "+r.Hint.From+" has local edits.", "Copy it over the other copies: ")
	case ReasonCopyAbsent:
		missing := r.Hint.To
		is := "is"
		if len(missing) > 1 {
			is = "are"
		}
		lead := fmt.Sprintf("%s %s missing, and %s has local edits.", listPaths(missing), is, r.Hint.From)
		for _, m := range missing {
			if blocked := inTheWay(root, m); blocked != "" {
				lead += fmt.Sprintf(" %s is a file where %s's folder should be: move it aside first.", blocked, m)
			}
		}
		return with(lead, "Copy it there: ")
	case ReasonOtherIdentity, ReasonOtherVariant:
		what := "a different skill"
		if r.Reason == ReasonOtherVariant && r.Hint != nil && r.Hint.Identity != nil {
			what = "another variant of " + r.Name + " than the setup's " + r.Hint.Identity.Variant
		}
		return with(first+" is tracked as "+what+", so "+verb+" left it alone.", "To track it as the setup's "+r.Name+" instead: ")
	case ReasonCloudTracked:
		return r.Name + " was last updated from Framework Cloud, so the package copy didn't replace it.", ""
	case ReasonUntracked:
		if r.Hint != nil && r.Hint.Key == HintMove {
			return with(first+" isn't tracked, and "+r.Hint.From+", its tracked copy, is gone.", "If you moved it, record the move: ")
		}
		if push {
			return with(first+" isn't tracked, so push left it alone.", "To push it as a new skill: ")
		}
		return with(first+" isn't tracked, so pull left it alone.", "To track it with this skill: ")
	case ReasonFolderGone:
		var gone []string
		for _, p := range r.Paths {
			if r.Hint == nil || r.Hint.Key != HintMove || p == r.Hint.From {
				gone = append(gone, p)
			}
		}
		if r.Hint != nil && r.Hint.Key == HintMove {
			gone = []string{r.Hint.From}
		}
		lead := fmt.Sprintf("The folder of %s is gone, so %s didn't recreate it.", listPaths(gone), verb)
		return with(lead, "If you moved it, record the move: ")
	case ReasonAllGone:
		return with("Every tracked copy is gone.", "Sign in so pull can restore it from Framework Cloud: ")
	case ReasonNotInSetup:
		return r.Name + " is no longer in the project's setup, so pull left its folder alone.", ""
	case ReasonOffline:
		return "Framework Cloud wasn't reached, so " + r.Name + " was left as it is.", ""
	case ReasonNotShipped:
		return "fw doesn't ship " + r.Name + "; the language package does.", ""
	case ReasonSetupOlder:
		return with(listPaths(r.Paths)+" "+has(r.Paths)+" no copy, and the setup names an older version than this project has.", "Sign in to get the newer one: ")
	case ReasonUnreadable:
		var why []string
		for _, p := range r.Paths {
			if l := lookAt(root, p); l.Unreadable {
				why = append(why, p+" couldn't be read as a skill: "+unreadableWhy(root, p)+".")
			}
		}
		if len(why) == 0 {
			return listPaths(r.Paths) + " couldn't be read as a skill.", ""
		}
		return strings.Join(why, " "), ""
	case ReasonChangedDuringPull:
		return first + " changed while pull was running, so it was left alone.", ""
	case ReasonCloudChanged:
		return with("Pull first: "+r.Name+" changed on Framework Cloud since this project pulled it. Run "+c.Pull+", or replace it with", "")
	case ReasonNoBase:
		return with(r.Name+" has no version on Framework Cloud to push against.", "To send it anyway: ")
	case ReasonFirstPushPending:
		return with("A first push of "+r.Name+" stopped before Framework Cloud replied, so it may already be there. Check your skills on Framework Cloud: if it is, run fw skills sync PATH --id ID.", "To send it as a new skill anyway: ")
	case ReasonCancelled:
		return "Not pushed: you answered no.", ""
	case ReasonPathMoved, ReasonLockMoved, ReasonRetargeted:
		return "", ""
	}
	return "", hint
}

// copyPaths is every path a copy hint names: its source and its targets.
func (h *Hint) copyPaths() []string {
	if h == nil {
		return nil
	}
	var out []string
	if h.From != "" {
		out = append(out, h.From)
	}
	return append(out, h.To...)
}

func others(list []string, x string) []string {
	var out []string
	for _, p := range list {
		if p != x {
			out = append(out, p)
		}
	}
	return out
}

func has(paths []string) string {
	if len(paths) > 1 {
		return "have"
	}
	return "has"
}

// SkillStatus is one result row as fw prints it.
type SkillStatus struct {
	Name   string   `json:"name"`
	Status string   `json:"status"`
	Paths  []string `json:"paths"`
	// Reason says why, in a plain sentence, on every row that isn't a
	// success (added, restored, updated, current)
	Reason string `json:"reason,omitempty"`
	// Hint is the exact command that fixes it, when there is one
	Hint string `json:"hint,omitempty"`
	// ReasonCode, HintKey, and HintArgs are the machine forms R and Python
	// share. HintArgs repeats the key with the hint's arguments.
	ReasonCode *string `json:"reason_code"`
	HintKey    *string `json:"hint_key"`
	HintArgs   *Hint   `json:"hint_args,omitempty"`
	// SkillID and Version are Cloud's answer to a push.
	SkillID string `json:"skill_id,omitempty"`
	Version int    `json:"version,omitempty"`
}

// statuses turns rows into fw's result rows.
func statuses(root string, rows []Row, c Commands, push bool) []SkillStatus {
	out := []SkillStatus{}
	for _, r := range rows {
		s := SkillStatus{Name: r.Name, Status: r.Status, Paths: r.Paths, HintArgs: r.Hint}
		if r.Reason != "" {
			code := r.Reason
			s.ReasonCode = &code
		}
		if r.Hint != nil {
			key := r.Hint.Key
			s.HintKey = &key
		}
		s.Reason, s.Hint = describe(root, r, c, push)
		if s.Paths == nil {
			s.Paths = []string{}
		}
		out = append(out, s)
	}
	return out
}

// unreadableWhy says why a folder isn't a valid skill.
func unreadableWhy(root, p string) string {
	target, err := safePath(root, p)
	if err == nil {
		_, err = Scan(target)
	}
	if err == nil {
		return "it isn't a folder"
	}
	return err.Error()
}
