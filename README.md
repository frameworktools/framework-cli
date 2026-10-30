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
fw new                              create an R, Python, or Stata project
fw projects list                    your projects on Framework Cloud
fw project link                     link the current project to Framework Cloud
fw config path | sync               configuration folders
fw doctor                           runtimes and settings fw would use
```

Run `fw help <command>` for details.

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
