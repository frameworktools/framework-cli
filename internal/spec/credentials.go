package spec

import (
	"regexp"
	"sort"
	"strings"
)

// credentialKey matches a settings key that holds a credential: Framework
// Cloud's list (password, secret, token, access_key, api_key, private_key,
// credential) plus passwd and pwd, as R's .fw_credential_key() has it.
var credentialKey = regexp.MustCompile(`(?i)password|passwd|pwd|secret|token|access_?key|api_?key|private_?key|credential`)

// CredentialKey reports whether a settings key holds a credential. Hyphens
// count as underscores (access-key).
func CredentialKey(key string) bool {
	return credentialKey.MatchString(strings.ReplaceAll(key, "-", "_"))
}

// envCall matches env("NAME") and env("NAME", "default"), with single or
// double quotes and spaces allowed. Go's regexp has no backreferences, so
// each quote style is its own alternative.
var envCall = regexp.MustCompile(`^\s*env\(\s*(?:"([^'"]*)"|'([^'"]*)')\s*(?:,\s*(?:"(.*?)"|'(.*?)')\s*)?\)\s*$`)

// EnvCall reads a value of the form env("NAME") or env("NAME", "default"):
// the variable's name, its default, and whether a default was given. ok is
// false for any other value. R: .fw_env_call().
func EnvCall(value string) (name, def string, hasDefault, ok bool) {
	m := envCall.FindStringSubmatchIndex(value)
	if m == nil {
		return "", "", false, false
	}
	group := func(i int) (string, bool) {
		if m[2*i] < 0 {
			return "", false
		}
		return value[m[2*i]:m[2*i+1]], true
	}
	if name, ok = group(1); !ok {
		name, _ = group(2)
	}
	if def, hasDefault = group(3); !hasDefault {
		def, hasDefault = group(4)
	}
	return name, def, hasDefault, true
}

var sysGetenv = regexp.MustCompile(`^Sys\.getenv\(\s*(?:"[^"]*"|'[^']*')\s*(?:,\s*(?:unset\s*=\s*)?(?:"(.*?)"|'(.*?)')\s*)?\)$`)

// LiteralSecret reports whether a credential's value is written out rather
// than read from the environment. Missing, empty, a mapping or list, a
// boolean, env("NAME"), env("NAME", ""), and !expr Sys.getenv("NAME") with
// no or an empty default pass; a non-empty default (env("NAME", "x")),
// any other text, and numbers are written out. The rule Framework Cloud
// applies to specs. R: .fw_literal_secret().
func LiteralSecret(value any) bool {
	switch v := value.(type) {
	case nil, bool, map[string]any, []any:
		return false
	case string:
		if v == "" {
			return false
		}
		if _, def, _, ok := EnvCall(v); ok {
			return def != ""
		}
		if code, ok := strings.CutPrefix(v, "!expr"); ok && strings.TrimLeft(code, " \t") != code {
			m := sysGetenv.FindStringSubmatch(strings.TrimSpace(code))
			if m == nil {
				return true
			}
			return m[1] != "" || m[2] != ""
		}
		return true
	}
	return true
}

// ConnectionSecrets lists, as dot paths, the credential keys under any key
// named connections that hold written-out secrets. Values are never
// returned.
func ConnectionSecrets(doc any) []string {
	var found []string
	var walk func(node any, at string, inside bool)
	walk = func(node any, at string, inside bool) {
		switch n := node.(type) {
		case map[string]any:
			for k, v := range n {
				here := k
				if at != "" {
					here = at + "." + k
				}
				within := inside || k == "connections"
				if within && CredentialKey(k) && LiteralSecret(v) {
					found = append(found, here)
					continue
				}
				walk(v, here, within)
			}
		case []any:
			for _, v := range n {
				walk(v, at, inside)
			}
		}
	}
	walk(doc, "", false)
	sort.Strings(found)
	return found
}
