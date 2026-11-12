package stata

import (
	"encoding/base64"
	"io/fs"
	"sort"

	"github.com/frameworktools/framework-cli/internal/skillsync"
)

// AssistantSkillsDir is the skills folder an assistant reads, or "".
func AssistantSkillsDir(name string) string {
	return assistantTable[name].skillsDir
}

// ShippedSkill is a skill fw ships for Stata projects, as a bundle, or nil
// when fw has none by that name.
func ShippedSkill(name string) []skillsync.File {
	shipped, err := fs.Sub(skillFiles, "skills/"+name)
	if err != nil {
		return nil
	}
	var bundle []skillsync.File
	err = fs.WalkDir(shipped, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || p == ".framework-skill.json" {
			return err
		}
		data, err := fs.ReadFile(shipped, p)
		bundle = append(bundle, skillsync.File{Path: p, Content: base64.StdEncoding.EncodeToString(data)})
		return err
	})
	if err != nil || len(bundle) == 0 {
		return nil
	}
	return bundle
}

// PackageSkills are the skills a Stata project of this type gets without
// Framework Cloud: framework-stata, and framework-sensitive-data for
// sensitive projects. A bare project gets none.
func PackageSkills(kind string, sensitive bool) []string {
	var names []string
	if kind != "bare" {
		names = append(names, "framework-stata")
	}
	if sensitive || kind == "project_sensitive" {
		names = append(names, "framework-sensitive-data")
	}
	sort.Strings(names)
	return names
}

// WantedSkills turns a resolved Stata spec's skills into what a skills pull
// installs, in name order.
func WantedSkills(v View) []skillsync.Wanted {
	names := make([]string, 0, len(v.Skills))
	for name := range v.Skills {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]skillsync.Wanted, 0, len(names))
	for _, name := range names {
		entry := v.Skills[name]
		w := skillsync.Wanted{Name: name}
		switch {
		case entry.Package:
			w.Package, w.Files = true, ShippedSkill(name)
		case len(entry.Bundle) > 0:
			w.Files = entry.Bundle
		default:
			w.Files = skillsync.BundleFromText(entry.Content, entry.Files)
		}
		if entry.Sync != nil {
			w.Sync = &skillsync.Source{ID: entry.Sync.ID, Version: entry.Sync.Version, Hash: entry.Sync.Hash, Variant: entry.Sync.Variant, Server: entry.Sync.Server}
			w.Known = []string{entry.Sync.Hash}
		}
		out = append(out, w)
	}
	return out
}
