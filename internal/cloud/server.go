package cloud

import (
	"errors"
	"fmt"
	"strings"

	"github.com/frameworktools/framework-cli/internal/config"
	"github.com/frameworktools/framework-cli/internal/project"
)

// Scope decides which settings name the server.
//
// User calls (settings, new, logout) skip the project's .env, so a user token
// never goes to a project's server by accident. Project calls (publish, sync,
// the ledger) read the project's .env first, so a project token goes to the
// server that issued it. Login is a user call that may fall back to
// framework.pub, because fw login names the server and asks before
// connecting. No other call falls back to framework.pub: using the public
// cloud is always the user's choice.
type Scope int

const (
	UserScope Scope = iota
	ProjectScope
	LoginScope
)

// ErrNoServer means nothing names a server: no flag, no FW_CLOUD_URL, and
// no saved login.
var ErrNoServer = errors.New("no Framework Cloud server is chosen")

// ErrAmbiguousServer means a project call found several saved logins and
// nothing saying which one the project uses.
var ErrAmbiguousServer = errors.New("this project does not say which Framework Cloud server it uses")

// Server is a resolved server URL and where the setting came from.
type Server struct {
	URL    string
	Source string
}

// Resolver gathers the inputs for server resolution.
type Resolver struct {
	Flag       string      // --url
	Getenv     config.Env  // the process environment
	Dirs       config.Dirs // for the saved logins
	ProjectDir string      // project root for project-scoped calls, or ""
}

const loginSource = "your fw login default"

// Resolve picks the server for scope:
//
//   - user calls: the flag, FW_CLOUD_URL, then the server of the last login
//   - login: the same, then framework.pub, which fw login names and confirms
//   - project calls: the flag, FW_CLOUD_URL in the project's .env, the
//     environment variable, then the saved login. When the saved logins name
//     different servers, a project call fails instead of guessing.
//
// With nothing set, every call but login returns ErrNoServer.
func (r Resolver) Resolve(scope Scope) (Server, error) {
	type candidate struct{ url, source string }
	candidates := []candidate{{r.Flag, "the --url flag"}}
	if scope == ProjectScope && r.ProjectDir != "" {
		candidates = append(candidates, candidate{project.EnvValue(r.ProjectDir, "FW_CLOUD_URL"), "the project's .env"})
	}
	candidates = append(candidates, candidate{r.Getenv("FW_CLOUD_URL"), "the FW_CLOUD_URL environment variable"})
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

	saved, err := r.savedServers()
	if err != nil {
		return Server{}, err
	}
	switch {
	case len(saved) == 1 || (len(saved) > 1 && scope != ProjectScope):
		return Server{URL: saved[0], Source: loginSource}, nil
	case len(saved) > 1:
		hosts := make([]string, len(saved))
		for i, s := range saved {
			hosts[i] = Host(s)
		}
		return Server{}, fmt.Errorf("%w: its .env has no FW_CLOUD_URL, and you are logged in to %s. Add FW_CLOUD_URL=<server> to the project's .env, set FW_CLOUD_URL, or pass --url",
			ErrAmbiguousServer, strings.Join(hosts, " and "))
	case scope == LoginScope:
		return Server{URL: DefaultURL, Source: "the fw login default"}, nil
	case scope == ProjectScope:
		return Server{}, fmt.Errorf("%w. This project's .env has no FW_CLOUD_URL and you have not logged in. Add FW_CLOUD_URL=<server> to .env, set FW_CLOUD_URL, pass --url, or log in with fw login (framework.pub) or fw login --url <server>", ErrNoServer)
	default:
		return Server{}, fmt.Errorf("%w. Log in with fw login (framework.pub) or fw login --url <server>, set FW_CLOUD_URL, or pass --url", ErrNoServer)
	}
}

// savedServers returns the distinct servers saved by logins, the most
// recent first.
func (r Resolver) savedServers() ([]string, error) {
	var urls []string
	seen := map[string]bool{}
	for _, raw := range r.Dirs.SavedServers() {
		url, err := Normalize(raw)
		if err != nil {
			return nil, err
		}
		if !seen[url] {
			seen[url] = true
			urls = append(urls, url)
		}
	}
	return urls, nil
}
