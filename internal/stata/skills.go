package stata

import (
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"io/fs"
	"sort"

	"github.com/frameworktools/framework-cli/internal/skillsync"
)

// skillHistory is the bundle hash of every version of each skill fw ships,
// written by tools/skillhistory.
//
//go:embed skill-history.json
var skillHistory []byte

// KnownHashes lists the bundle hash of every version fw has shipped of a
// Stata skill: a copy holding one is an unedited older version.
func KnownHashes(name string) []string {
	var h struct {
		Skills map[string]map[string][]string `json:"skills"`
	}
	if json.Unmarshal(skillHistory, &h) != nil {
		return nil
	}
	return h.Skills[name]["stata"]
}

// PackageSource is a skill fw ships for Stata projects, as the planner's
// source: nil files when fw doesn't ship it.
func PackageSource(name string) skillsync.Source {
	return skillsync.PackageSource(name, "stata", ShippedSkill(name), KnownHashes(name))
}

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

// SetupSkills turns a resolved Stata spec's skills into the planner's
// sources, in name order: a package skill fw ships, a Cloud skill at the
// version the setup names, or inline setup text. server is the project's
// Framework Cloud server, for a sync block that names none.
func SetupSkills(v View, server string) []skillsync.Source {
	names := make([]string, 0, len(v.Skills))
	for name := range v.Skills {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]skillsync.Source, 0, len(names))
	for _, name := range names {
		entry := v.Skills[name]
		files := entry.Bundle
		if len(files) == 0 {
			files = skillsync.BundleFromText(entry.Content, entry.Files)
		}
		switch {
		case entry.Package:
			out = append(out, PackageSource(name))
		case entry.Sync != nil:
			variant := entry.Sync.Variant
			if variant == "" {
				variant = "stata"
			}
			out = append(out, skillsync.CloudSource(name, skillsync.SyncSource{ID: entry.Sync.ID, Version: entry.Sync.Version,
				Hash: entry.Sync.Hash, Variant: variant, Server: entry.Sync.Server, KnownHashes: entry.Sync.KnownHashes}, files, server))
		default:
			out = append(out, skillsync.InlineSource(name, "stata", files))
		}
	}
	return out
}
