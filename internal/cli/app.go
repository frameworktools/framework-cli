package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/frameworktools/framework-cli/internal/cloud"
	"github.com/frameworktools/framework-cli/internal/config"
	"github.com/frameworktools/framework-cli/internal/engine"
	"github.com/frameworktools/framework-cli/internal/keyring"
	"github.com/frameworktools/framework-cli/internal/project"
)

// app holds everything commands read from the outside world, so tests can
// replace it.
type app struct {
	getenv  config.Env
	dirs    config.Dirs
	home    string
	cwd     string
	keyring keyring.Store
	stdin   io.Reader
	reader  *bufio.Reader
	// interactive is true when stdin is a terminal.
	interactive bool
	// readSecret reads a line from the terminal without echoing it.
	readSecret func() (string, error)
	openURL    func(string) error
	sleep      func(context.Context, time.Duration) error
	engines    engine.Finder
	// run runs a language package with the terminal attached and returns
	// its exit status.
	run func(engine.Command) (int, error)
	// jsonOut is set by a command's --json flag. Errors are then printed as
	// JSON on stderr too.
	jsonOut bool
}

func newApp() (*app, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	dirs, err := config.Resolve()
	if err != nil {
		return nil, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	fd := int(os.Stdin.Fd()) // #nosec G115 -- file descriptors fit in int
	return &app{
		getenv:      os.Getenv,
		dirs:        dirs,
		home:        home,
		cwd:         cwd,
		keyring:     keyring.System,
		stdin:       os.Stdin,
		interactive: term.IsTerminal(fd),
		readSecret: func() (string, error) {
			b, err := term.ReadPassword(fd)
			fmt.Fprintln(os.Stderr)
			return string(b), err
		},
		openURL: openBrowser,
		sleep:   cloud.Sleep,
		engines: engine.System(os.Getenv, home),
		run:     runAttached,
	}, nil
}

func (a *app) tokens() cloud.Tokens {
	return cloud.Tokens{Getenv: a.getenv, Dirs: a.dirs, Keyring: a.keyring, Home: a.home}
}

func (a *app) projectRoot() string {
	return project.FindRoot(a.cwd)
}

func (a *app) server(flag string, scope cloud.Scope) (cloud.Server, error) {
	r := cloud.Resolver{Flag: flag, Getenv: a.getenv, Dirs: a.dirs}
	if scope == cloud.ProjectScope {
		r.ProjectDir = a.projectRoot()
	}
	return r.Resolve(scope)
}

// userClient returns a client for the user's server with their token.
func (a *app) userClient(flag string) (*cloud.Client, error) {
	server, err := a.server(flag, cloud.UserScope)
	if err != nil {
		return nil, err
	}
	found, ok := a.tokens().Lookup(server.URL)
	if !ok {
		return nil, &errNotLoggedIn{fmt.Sprintf("not logged in to %s. Run: %s", cloud.Host(server.URL), loginCommand(server.URL))}
	}
	return cloud.NewClient(server.URL, found.Token), nil
}

// readLine reads one line from stdin.
func (a *app) readLine() (string, error) {
	if a.reader == nil {
		a.reader = bufio.NewReader(a.stdin)
	}
	line, err := a.reader.ReadString('\n')
	if err != nil && (!errors.Is(err, io.EOF) || line == "") {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func loginCommand(url string) string {
	if cloud.IsDefault(url) {
		return "fw login"
	}
	return "fw login --url " + url
}

// explain adds a next step to API errors people can act on.
func explain(err error, url string) error {
	switch cloud.StatusOf(err) {
	case 401:
		return fmt.Errorf("%w. Your token is invalid or revoked. Run: %s", err, loginCommand(url))
	case 403:
		return fmt.Errorf("%w. This needs a user token from %s/tokens", err, url)
	}
	return err
}

// openBrowser opens an http(s) URL from the server. Other schemes are
// refused, so a server cannot make fw open a local file or program.
func openBrowser(url string) error {
	if !strings.HasPrefix(url, "https://") && !strings.HasPrefix(url, "http://") {
		return fmt.Errorf("not opening %q: not a web address", url)
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url) // #nosec G204 -- checked above
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url) // #nosec G204 -- checked above
	default:
		cmd = exec.Command("xdg-open", url) // #nosec G204 -- checked above
	}
	return cmd.Start()
}

func runAttached(c engine.Command) (int, error) {
	cmd := exec.Command(c.Program, c.Args...) // #nosec G204 -- programs fw found itself
	cmd.Dir = c.Dir
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	err := cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode(), nil
	}
	if err != nil {
		return 0, fmt.Errorf("running %s: %w", c.Program, err)
	}
	return 0, nil
}
