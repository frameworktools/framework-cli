# fw

`fw` is the command-line tool for [Framework](https://framework.pub). It connects local projects to Framework Cloud (`framework.pub` or a self-hosted server), scaffolds Stata projects, and publishes rendered artifacts. The R and Python packages work without it.

See [docs/plan.md](docs/plan.md) for the design notes.

## Installation

Install with Homebrew on macOS and Linux:

```sh
brew install frameworktools/tap/fw
```

On Linux, HPC clusters, and Windows, download the platform archive from [Releases](https://github.com/frameworktools/framework-cli/releases) and place `fw` in a directory on `PATH`.

## Commands

```text
fw login | logout | status          Framework Cloud login, shared with R and Python
fw projects list                    list projects on Framework Cloud
fw project show SLUG                inspect one project
fw blueprints list                  list available blueprints
fw structures | tools | package-sets | skills list
                                    list blueprint components
fw new                              create an R, Python, or Stata project
fw project link | status            inspect or configure the project cloud link
fw project upgrade                  update an older project to the current layout
fw adopt [DIR]                      adopt an existing directory as a Framework project
fw structures import [DIR]          save a directory layout as a structure
fw project bootstrap                prepare a cloned project
fw project push-spec                send .framework/spec.json to Framework Cloud
fw pull | push                      sync packages, skills, and data registry entries
fw data pull | push                 sync the project data catalog
fw skills pull | push               sync project skills
fw skills sync PATH                 sync one skill directory
fw publish PATH [--to github]       publish an artifact or rendered report
fw config path | sync               inspect or copy configuration directories
fw doctor                           report detected runtimes, paths, and server
```

Run `fw help <command>` for flags and examples. Full documentation is at [cli.framework.pub](https://cli.framework.pub), built from `docs/site`.

### Project skills

```sh
fw skills pull --dry-run
fw skills pull
fw skills push
```

`fw skills pull` reads the project setup from Framework Cloud using `FW_PROJECT_TOKEN`. It adds missing skills, updates unedited skills to their latest versions, preserves locally edited skills (`--overwrite` backs up and replaces them), and lists skills removed from the Cloud setup without deleting local files. Without a token or network connection, `fw` installs the built-in skills for the project type. In R and Python projects, `fw` delegates to `pull_skills()`.

`fw skills push` uploads locally edited skills to Framework Cloud as new versions after confirmation. If a skill changed on Framework Cloud since the last pull, `fw` stops until you pull or pass `--force`. Pushing an edited built-in skill saves a personal copy under your account.

### Pull and push

```sh
fw pull --dry-run
fw pull
fw push
```

`fw pull` and `fw push` synchronize project packages, skills, and data registry definitions with Framework Cloud. `fw pull` groups incoming changes under `Packages`, `Skills`, and `Data registry` and asks once before applying them. It preserves local files, version pins, edited skills, and local catalog definitions unless you pass `--overwrite`.

`fw push` previews outgoing changes and asks once before sending the package list, edited skills, and data registry. If any section changed on Framework Cloud since the last pull, `fw` refuses that section with `Pull first: fw pull` unless you pass `--force`. Pass `--remove` to delete Cloud packages and data entries that no longer exist locally. When one section fails, `fw` applies the remaining sections and reports the outcome for each. R and Python projects delegate to `pull_all()` and `push_all()`. Stata projects manage packages locally through `settings.yml` and `fw_packages`, so `fw pull` and `fw push` synchronize skills and the data registry for Stata. Use `fw skills pull | push` or `fw data pull | push` to synchronize a single section.

### Single-skill sync

```sh
fw skills sync .agents/skills/my-skill --variant python --dry-run
fw skills sync .agents/skills/my-skill --variant python
```

`fw skills sync` shares lock state with R and Python, preserves fork ancestry, and stops on concurrent edits. Pass `--resolve local` or `--resolve remote` to settle a conflict. Replaced local files move to `.framework/skills-backups/`. See the [Skill sync guide](https://framework.pub/docs/skill-sync).

### Automation and AI agents

Commands that return structured state accept `--json`. Without an interactive terminal, `fw login --yes` prints a verification code and approval URL on stderr so the token is stored directly in the system keyring without passing through the agent.

Exit codes: `0` success, `1` failure, `2` invalid flags or arguments, `3` unauthenticated or rejected token, `4` resource not found, `5` browser login denied or expired, `6` upload path contains `private` without `--include-private`.

## Development

```sh
go test ./...
golangci-lint run
go build -o fw ./cmd/fw
```

JSON Schemas and merge fixtures originate in the `framework` R repository and are pinned in `assets.lock.json`. Update them in the R repository first, then sync:

```sh
go run ./tools/syncassets --from ../framework --ref <tag or commit>
go run ./tools/syncassets --check --from ../framework
```

`fw` implements the Stata project scaffolder and generates the Stata cases for the shared `create/` conformance fixtures. Regenerate them after changing Stata project creation:

```sh
go run ./tools/createfixtures --out ../framework/inst/conformance/create/stata
```

`go test ./...` validates `docs/site`, and each release builds `docs-site.tar.gz` with `go run ./tools/docsite` for Framework Cloud to import.
