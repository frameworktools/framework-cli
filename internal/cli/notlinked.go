package cli

import (
	"github.com/frameworktools/framework-cli/internal/project"
)

// linkHint is how to link a project to Framework Cloud. A fresh clone has
// no .env, since git ignores it, so it has no project token.
const linkHint = "Link it with: fw project link --from SLUG. fw projects list shows the slugs."

// createHint follows linkHint where a project may not be on Framework
// Cloud yet.
const createHint = " A project not on Framework Cloud yet: fw project link --create."

// notLinked is what fw says wherever a project has no project token. A
// non-empty effect, such as "nothing was changed", says what that meant.
func notLinked(effect string) string {
	s := "This project isn't linked to Framework Cloud (no FW_PROJECT_TOKEN in the environment or .env)"
	if effect != "" {
		s += ", so " + effect
	}
	return s + ". " + linkHint
}

// errNotLinked is a command that changed nothing for want of a project
// token.
type errNotLinked struct{}

func (errNotLinked) Error() string { return notLinked("nothing was changed") }

// projectToken is the project's token: FW_PROJECT_TOKEN in the
// environment, else in the project's .env. Empty when there is none.
func (a *app) projectToken(root string) string {
	if token := a.getenv("FW_PROJECT_TOKEN"); token != "" {
		return token
	}
	return project.EnvValue(root, "FW_PROJECT_TOKEN")
}
