package cloud

import (
	"github.com/frameworktools/framework-cli/internal/config"
	"github.com/frameworktools/framework-cli/internal/project"
)

// Scope decides which settings name the server.
//
// User calls (login, settings, new) skip the project's .env, so a user token
// never goes to a project's server by accident. Project calls (publish, the
// ledger) skip the login default, so a project token only goes to the server
// that issued it.
type Scope int

const (
	UserScope Scope = iota
	ProjectScope
)

// Server is a resolved server URL and where the setting came from.
type Server struct {
	URL    string
	Source string
}

// Resolver gathers the inputs for server resolution.
type Resolver struct {
	Flag       string      // --url
	Getenv     config.Env  // the process environment
	Dirs       config.Dirs // for the saved login default
	ProjectDir string      // project root for project-scoped calls, or ""
}

// Resolve picks the server for scope. User calls use the flag, then
// FW_CLOUD_URL, then the saved login default, then framework.pub. Project
// calls use the flag, then FW_CLOUD_URL in the project's .env, then the
// environment variable, then framework.pub.
func (r Resolver) Resolve(scope Scope) (Server, error) {
	type candidate struct{ url, source string }
	candidates := []candidate{{r.Flag, "the --url flag"}}
	if scope == ProjectScope && r.ProjectDir != "" {
		candidates = append(candidates, candidate{project.EnvValue(r.ProjectDir, "FW_CLOUD_URL"), "the project's .env"})
	}
	candidates = append(candidates, candidate{r.Getenv("FW_CLOUD_URL"), "the FW_CLOUD_URL environment variable"})
	if scope == UserScope {
		candidates = append(candidates, candidate{r.Dirs.SavedServer(), "your fw login default"})
	}
	candidates = append(candidates, candidate{DefaultURL, "the built-in default"})

	for _, c := range candidates {
		if c.url == "" {
			continue
		}
		url, err := Normalize(c.url)
		if err != nil {
			return Server{}, err
		}
		return Server{URL: url, Source: c.source}, nil
	}
	panic("unreachable: the built-in default is always set")
}
