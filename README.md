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
fw structures | tools | package-sets | skills list
                                    pieces a project can be built from
fw new                              create an R, Python, or Stata project
fw project link | status            the current project's cloud link and runtime
fw adopt [DIR]                      make an existing folder a Framework project
fw structures import [DIR]          save a folder's layout as a structure
fw project bootstrap                prepare a cloned project
fw project push-spec                send the project's spec to Framework Cloud
fw pull | push                      bring a project's packages, skills, and data registry up to date; send them back
fw data pull | push                 bring a project's data catalog up to date; send it back
fw skills pull | push               bring a project's skills up to date; send edited ones back
fw skills sync PATH                 synchronize one skill directory
fw publish PATH [--to github]       publish a rendered notebook or HTML file
fw config path | sync               configuration folders
fw doctor                           runtimes and settings fw would use
```

Run `fw help <command>` for details. The full documentation is at
[cli.framework.pub](https://cli.framework.pub), built from `docs/site`.

### Project skills

```sh
fw skills pull --dry-run
fw skills pull
fw skills push
```

`fw skills pull` reads the project's current setup on Framework Cloud with its
project token. It adds missing skills, updates unedited ones to their latest
versions, keeps skills you edited (`--overwrite` replaces them after a backup),
and lists skills no longer in the setup without deleting them. Without a token
or a connection, it uses the package skills for the project's type. R and
Python projects run their package's `pull_skills()`. `fw skills push` sends
edited skills to Framework Cloud as new versions of your own skills, after
asking. It refuses when the Cloud copy changed since the last pull; `--force`
overrides that. An edited built-in skill becomes your own copy.

### Pull and push

```sh
fw pull --dry-run
fw pull
fw push
```

`fw pull` and `fw push` sync everything that syncs with Framework Cloud in one
go: the project's packages, its skills, and its data registry. `fw pull` lists
the changes grouped by part (Packages, Skills, Data registry) and asks once
before it applies them. It never removes anything local, and it keeps local
edits such as version pins, edited skills, and data definitions unless you
pass `--overwrite`. `fw push` shows what would change on Framework Cloud and
asks once before it sends the package list, edited skills, and data registry.
It refuses a part that changed on Framework Cloud since the last pull ("Pull
first: fw pull"); `--force` overrides that. `--remove` also removes packages
and data entries the project doesn't have. When one part fails, the others
still go through, and fw names which parts were applied and which weren't.
R and Python projects run their package's `pull_all()` and `push_all()`.
Packages aren't managed for Stata projects, so for those, fw syncs skills and
the data registry. `fw skills pull | push` and `fw data pull | push` sync one
part.

### Skill sync

```sh
fw skills sync .agents/skills/my-skill --variant python --dry-run
fw skills sync .agents/skills/my-skill --variant python
```

Sync shares project state with R and Python. It preserves fork ancestry and
stops on concurrent edits. Explicit `--resolve local` or `--resolve remote`
selects a conflict result. Replaced local files remain in project backups.
[Skill sync guide](https://framework.pub/docs/skill-sync).

### Scripts and AI agents

Commands that report information take `--json`. Without a terminal,
`fw login --yes` prints a code and link for a person to approve, so the
token never passes through the agent. Exit codes: 0 success, 1 failure,
2 bad flags or arguments, 3 not logged in or token rejected, 4 not found,
5 browser login denied or expired, 6 an upload has "private" in a path
and needs `--include-private`.

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

fw is the Stata engine, so it generates the Stata cases of R's shared
`create/` fixtures. After changing Stata creation, regenerate them and send
them to the R repository:

```sh
go run ./tools/createfixtures --out ../framework/inst/conformance/create/stata
```

The docs site lives in `docs/site`. `go test` checks it, and each release
attaches `docs-site.tar.gz` (built with `go run ./tools/docsite`) for
framework-cloud to import.
