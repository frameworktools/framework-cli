package adopt

import (
	"fmt"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// privateFolderName reports whether a folder name marks it private:
// "private" or "..._private", in any case, as in R's
// .fw_is_private_folder_name().
func privateFolderName(name string) bool {
	lower := strings.ToLower(name)
	return lower == "private" || strings.HasSuffix(lower, "_private")
}

func privatePath(p string) bool {
	return slices.ContainsFunc(strings.Split(p, "/"), privateFolderName)
}

var (
	candidateKey   = regexp.MustCompile(`^(inputs|outputs)_`)
	restrictedName = regexp.MustCompile(`data|export|extract|report|result|output|raw|table|figure|submission|download|cohort`)
	outputName     = regexp.MustCompile(`report|result|output|export|table|figure`)
)

// RestrictedCandidates lists the mapped folders that may hold restricted
// data but aren't private, for a sensitive adopt to offer to mark private:
// folders with inputs_*/outputs_* keys (not private or public) or the keys
// data, cache, and scratch, in rule order; then other folders whose name
// says data, export, extract, report, result, output, raw, table, figure,
// submission, download, or cohort, by path. Folders with a private-named
// segment don't count. R: .adopt_restricted_candidates().
func (r Rules) RestrictedCandidates(m Mapping) []string {
	var out []string
	add := func(p string) {
		if !slices.Contains(out, p) && !privatePath(p) {
			out = append(out, p)
		}
	}
	done := map[string]bool{}
	for _, rule := range r.Rules {
		k := rule.Key
		p, ok := m.Directories[k]
		if !ok || done[k] {
			continue
		}
		done[k] = true
		if (candidateKey.MatchString(k) || k == "data" || k == "cache" || k == "scratch") && !strings.Contains(k, "private") && !strings.Contains(k, "public") {
			add(p)
		}
	}
	var extras []string
	for _, e := range m.ExtraDirectories {
		if restrictedName.MatchString(strings.ToLower(path.Base(e.Path))) {
			extras = append(extras, e.Path)
		}
	}
	sort.Strings(extras)
	for _, p := range extras {
		add(p)
	}
	return out
}

// MarkPrivate maps a folder to a private key, keeping its path:
// inputs_raw becomes inputs_private_raw, outputs_reports becomes
// outputs_private_reports, and any other folder becomes
// outputs_private_<name> when its name says report, result, output,
// export, table, or figure, and inputs_private_<name> otherwise, numbered
// when taken. A folder the mapping doesn't have is an error. R:
// .adopt_mark_private().
func (r Rules) MarkPrivate(m Mapping, folder string) (Mapping, error) {
	folder = strings.TrimRight(strings.TrimPrefix(folder, "./"), "/")
	var inDirs, inExtras []string
	for k, v := range m.Directories {
		if v == folder {
			inDirs = append(inDirs, k)
		}
	}
	for k, e := range m.ExtraDirectories {
		if e.Path == folder {
			inExtras = append(inExtras, k)
		}
	}
	sort.Strings(inDirs)
	sort.Strings(inExtras)
	if len(inDirs)+len(inExtras) == 0 {
		var all []string
		for _, row := range r.Rows(m) {
			if !strings.HasPrefix(row[1], "render_dirs.") {
				all = append(all, row[0])
			}
		}
		return m, fmt.Errorf("'%s' isn't a folder adopt maps here, so it can't be marked private. Folders: %s", folder, strings.Join(all, ", "))
	}
	old := append(inDirs, inExtras...)[0]
	if strings.Contains(old, "private") && !strings.Contains(old, "public") {
		return m, nil
	}
	slug := extraKey(path.Base(folder))
	var key string
	switch {
	case len(inDirs) > 0 && candidateKey.MatchString(old):
		key = candidateKey.ReplaceAllString(old, "${1}_private_")
	case outputName.MatchString(strings.ToLower(path.Base(folder))):
		key = "outputs_private_" + slug
	default:
		key = "inputs_private_" + slug
	}
	taken := map[string]bool{}
	for k := range m.Directories {
		taken[k] = true
	}
	for k := range m.ExtraDirectories {
		taken[k] = true
	}
	key = numbered(key, taken)
	for _, k := range inDirs {
		delete(m.Directories, k)
	}
	for _, k := range inExtras {
		delete(m.ExtraDirectories, k)
	}
	m.Directories[key] = folder
	if !slices.Contains(m.MarkedPrivate, folder) {
		m.MarkedPrivate = append(m.MarkedPrivate, folder)
	}
	return m, nil
}
