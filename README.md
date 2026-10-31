# fw

`fw` is the command-line helper for [Framework](https://framework.pub). It
works with Framework Cloud (framework.pub or a self-hosted server) and with
Framework projects. The R and Python packages work fully without it.

The design is in [docs/plan.md](docs/plan.md).

## Install

Once the first release is out:

```sh
brew install frameworktools/tap/fw
```

On Linux, HPC clusters, and Windows, download the archive for your platform
from [Releases](https://github.com/frameworktools/framework-cli/releases) and
put `fw` in a folder on your PATH.

## Commands

```
fw login | logout | status          Framework Cloud login, shared with R and Python
fw projects list                    your projects on Framework Cloud
fw project show SLUG                one project's details
fw blueprints list                  blueprints you can start a project from
fw new                              create an R, Python, or Stata project
fw project link | status            the current project's cloud link and runtime
fw project bootstrap                prepare a cloned project
fw project push-spec                send the project's spec to Framework Cloud
fw publish PATH                     publish a rendered notebook or HTML file
fw config path | sync               configuration folders
fw doctor                           runtimes and settings fw would use
```

Run `fw help <command>` for details. The full documentation is at
[cli.framework.pub](https://cli.framework.pub), built from `docs/site`.

### Scripts and AI agents

Commands that report information take `--json`. Without a terminal,
`fw login --yes` prints a code and link for a person to approve, so the
token never passes through the agent. Exit codes: 0 success, 1 failure,
2 bad flags or arguments, 3 not logged in or token rejected, 4 not found,
5 browser login denied or expired.

## Development

```sh
go test ./...
golangci-lint run
go build -o fw ./cmd/fw
```

The JSON Schemas and merge fixtures are copied from the framework R package
and pinned in `assets.lock.json`. Change them in the R repository, then copy
them again:

```sh
go run ./tools/syncassets --from ../framework --ref <tag or commit>
go run ./tools/syncassets --check --from ../framework
```

The docs site lives in `docs/site`. `go test` checks it, and each release
attaches `docs-site.tar.gz` (built with `go run ./tools/docsite`) for
framework-cloud to import.
