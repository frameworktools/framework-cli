---
id: agents
title: AI agents and scripts
description: Structured JSON output, exit codes, non-interactive flags, and device-code sign-in
section: guides
position: 1
---

`fw` supports non-interactive execution for AI coding agents and CI scripts: inspection and sync commands accept `--json`, failure modes map to distinct exit codes, and device-code sign-in keeps API tokens out of agent transcripts.

## Non-interactive session

Without an interactive terminal, pass `--dir` to `fw new` and `--yes` to commands that modify remote state or local setup:

```sh
fw login --yes --json                 # human approves the verification URL
fw projects list --json
fw structures list --lang r --json    # also tools, package-sets, and skills
fw new --from vaccine-study --dir vaccine-study && cd vaccine-study
fw project bootstrap
fw project push-spec
fw publish outputs/notebooks/results.html --slug results --yes --json
fw publish outputs/notebooks/results.html --to github --yes --json
```

## Browser-approved sign-in

When `fw login` runs without a terminal, pass `--yes` after reviewing the data-sharing notice. `fw` emits a device verification code and URL on stderr, waits for approval in a signed-in browser, and stores the resulting token directly in the system keyring so the agent never handles the secret.

```sh
fw login --yes --json
```

With `--json`, stderr receives a single JSON object to present to the user:

```json
{"user_code": "QGMK-JE6Q", "verification_url": "https://framework.pub/device?code=QGMK-JE6Q", "expires_in": 1200}
```

Verification codes expire after 20 minutes. Once approved, stdout emits the same account summary object as `fw status --json`.

> [!WARNING]
> Never paste an API token into an agent prompt or pass a token as a command-line flag. `fw` rejects token flags because command arguments appear in shell history and process tables. In CI or headless automation, pipe a token on standard input: `fw login --with-token --yes < token.txt`.

## JSON output

The following commands accept `--json`: `login`, `status`, `projects list`, `project show`, `blueprints list`, `structures list`, `structures import`, `tools list`, `package-sets list`, `skills list`, `skills sync`, `skills push`, `adopt`, `project link --create`, `project status`, `project push-spec`, `publish`, and (in Stata projects) `project upgrade`, `skills pull`, `data pull`, `data push`, `pull`, and `push`.

Structured JSON writes to stdout while progress and prompts write to stderr. Token values are never included in JSON responses.

```sh
fw projects list --json | jq -r '.projects[].slug'
```

## Exit codes

| Code | Condition |
|---|---|
| `0` | Success |
| `1` | Runtime or server error |
| `2` | Invalid flags or arguments |
| `3` | Unauthenticated, or token rejected by the server |
| `4` | Requested project or resource not found |
| `5` | Browser device-code login denied or expired |
| `6` | Upload path contains `private` and requires `--include-private` |

With `--json`, errors also emit one JSON line on stderr:

```json
{"error": "You have no project named 'nope'. (HTTP 404)", "exit_code": 4}
```

Exit code `6` adds `"reason": "private_path"`. Show the user the flagged paths and pass `--include-private` only after explicit confirmation.

## Confirmation flags

`fw` asks for confirmation before connecting a machine (`fw login`), uploading artifacts (`fw publish`), publishing new skill versions (`fw skills push`), adopting directories (`fw adopt`, `fw structures import`), and syncing packages, skills, or data definitions (`fw pull`, `fw push`, `fw data pull`, `fw data push`). Without a terminal, pass `--yes` after inspecting the dry-run or preview output.

- `fw project push-spec` prompts only when the project path contains `private`.
- `fw new` with Cloud component flags (`--blueprint`, `--structure`, etc.) keeps the project local when no terminal is attached unless `--cloud` is passed.
- `fw new NAME --lang stata` while signed in exits `1` without a terminal if the server is unreachable and no cached defaults exist; pass `--offline` to use built-in defaults.

## Project skills

`fw skills pull` synchronizes project skills with the project's setup on Framework Cloud. It installs missing skills, updates unedited skills to their newest versions, preserves locally edited skills (`--overwrite` backs them up to `.framework/skills-backups/` before replacing them), lists skills removed from Cloud without deleting local files, and refreshes the Skills table in `AGENTS.md`. Without `FW_PROJECT_TOKEN` or network access, `fw` installs the built-in package skills for the project type. R and Python projects delegate to `pull_skills()`.

```sh
fw skills pull --dry-run --json
fw skills pull
```

Summary output follows `Skills: 2 added, 3 updated, 5 up to date, 1 locally edited (kept)`.

`fw skills push` uploads locally edited skills to Framework Cloud as new versions of your personal skills (edited built-in skills fork into personal copies). If a skill changed on Framework Cloud since the last pull, `fw` exits `1` with `Pull first` unless `--force` is passed (preserving earlier Cloud versions in history). Untracked skill directories are listed so you can register them with `fw skills sync PATH`. Run `--dry-run` first and confirm with the user before passing `--yes`.

```sh
fw skills push --dry-run
fw skills push --yes --json
```

## Combined pull and push

`fw pull` and `fw push` synchronize project packages, skills, and data registry definitions in one step.

`fw pull` groups incoming updates by section (`Packages`, `Skills`, `Data registry`) and prompts once (`Apply these changes?`). It appends and installs new packages, updates unedited skills, writes new data entries to `settings.yml`, and records synced revisions in `.framework/setup.lock.json` and `.framework/data.lock.json`. Local files are never deleted, and local edits (version pins, edited skills, modified data entries) are preserved unless `--overwrite` is passed.

```text
Changes from Framework Cloud:
Packages: not managed for Stata projects
Skills:
  2 to add
Data registry (revision 7):
  Entries to add: inputs.final.analysis
```

`fw push` previews outgoing changes and notes their scope (new setups, ZIP downloads, and **Update on GitHub** use the synced package list and data registry; pushed skill versions apply wherever the skill is attached). Any section modified on Framework Cloud since the last pull is refused with `Pull first: fw pull` unless `--force` is set. Pass `--remove` to delete Cloud packages and data entries absent locally. Pushing edited skills uses your personal `fw login` token, while packages and data registry entries use `FW_PROJECT_TOKEN`.

```sh
fw pull --dry-run
fw pull --yes
fw push --dry-run
fw push --yes
```

If one section fails, `fw` still applies the remaining sections and exits `1` with a per-section report (`fw push did not finish. Pushed: Skills. Already in step: Packages. Not pushed: Data registry (...)`). R and Python projects delegate to `pull_all()` and `push_all()`. For Stata projects, Cloud sync covers skills and the data registry while Stata packages live in `settings.yml` for `fw_packages`.

## Data registry

A project's Cloud data registry mirrors the `data` catalog in `settings.yml` (keys such as `inputs.raw.survey`). Only metadata definitions synchronize: dot-separated names, relative paths, types, delimiters, `locked` and `private` flags, descriptions, and Stata names. Data files and local file checksums never leave the machine.

`fw data pull` lists entries to add, entries that differ locally, and local-only entries, writes new entries to `settings.yml` (or overwrites differing ones with `--overwrite`), and updates `.framework/data.lock.json`. `fw data push` previews registry changes on Framework Cloud, refuses concurrent upstream edits without `--force`, and removes Cloud-only entries only when `--remove` is set.

```sh
fw data pull --dry-run
fw data pull --yes
fw data push --dry-run
fw data push --yes
```

R and Python projects delegate to `pull_data_registry()` and `push_data_registry()`; `fw` updates Stata `settings.yml` files directly.

## Upload boundaries and private paths

`fw` never uploads project files in the background. Network writes happen only on explicit commands (`fw publish`, `fw project push-spec`, `fw push`, `fw skills push`, `fw data push`, `fw structures import`, `fw project link --create`), and `fw publish` uploads only the target path. `fw publish --to github` commits and pushes only `published/<slug>/` and the `published/` index, returning `{"url", "commit", "pushed", "pages_enabled"}` under `--json`. Always remind users that GitHub Pages sites are public even on private repositories. Sensitive projects also configure AI assistant deny rules blocking reads of `.env` and private directories.
