// Command fw is the command-line helper for Framework.
package main

import (
	"os"

	"github.com/frameworktools/framework-cli/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
