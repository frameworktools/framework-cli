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

The following commands accept `--json`: `login`, `status`, `projects list`, `project show`, `blueprints list`, `structures list`, `structures import`, `tools list`, `package-sets list`, `skills list`, `skills sync`, `skills push`, `skills copy`, `adopt`, `project link --create`, `project status`, `project push-spec`, `publish`, and (in Stata projects) `project upgrade`, `skills pull`, `data pull`, `data push`, `pull`, and `push`.

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

`fw skills pull` synchronizes project skills with the project's setup on Framework Cloud. It installs missing skills, updates unedited skills to their newest versions, preserves locally edited skills (`--overwrite` backs them up to `.framework/skills-backups/` before replacing them), lists skills removed from Cloud without deleting local files, and refreshes the Skills table in `AGENTS.md`. Without `FW_PROJECT_TOKEN` or network access, `fw` installs the built-in package skills for the project type. R and Python projects delegate to `pull_skills()`. When a skill's tracked copies differ, or one is missing, `fw` writes none of its copies, leaves its lock entry alone, and reports it as diverged with the `fw skills copy` command that settles it; `fw skills push` reports it as failed with the same command. When a new assistant folder has no copy of a skill yet and your copy is edited or your own skill, `pull` leaves the folder empty and gives the `fw skills copy` command that fills it from your copy. `pull` never moves a skill to an older version in the lock, or changes its Cloud identity, whatever the source; without Framework Cloud, a skill tied to Cloud is left alone and reported as unavailable. A new assistant folder next to a copy tied to a Cloud skill gets the same `fw skills copy` hint. When none of a tracked skill's copies is left, `fw skills push` reports it as missing and exits `1`. `pull` puts it back as restored from the skill's own identity (a Cloud skill at the lock's version, which needs `fw login`), or reports it as missing with the command that would. When a tracked folder is gone, as after renaming `.claude` to `.agents`, `pull` writes nothing for that skill: the lock entry gets a missing row, and the untracked copy a row of its own with the `fw skills sync PATH --id ID` command that moves the entry to it. `fw pull` lists diverged, missing, and failed skills in `failed` and exits `1`, as `fw push` does. A folder that belongs to a different skill, or a skill the setup names under a different identity, is `kept`; a copy the lock doesn't track is `untracked`, on its own row, and the tracked copies keep their own status. With `--json`, every row that isn't added, restored, updated, or current has `reason`, one plain sentence, and `hint`, the exact command that fixes it, when there is one. Assistants turned on in the Cloud setup get their skills folders on `pull`.

```sh
fw skills pull --dry-run --json
fw skills pull
```

Summary output follows `Skills: 2 added, 3 updated, 5 up to date, 1 locally edited (kept)`.

`fw skills push` uploads locally edited skills to Framework Cloud as new versions of your personal skills (edited built-in skills fork into personal copies). If a skill changed on Framework Cloud since the last pull, `fw` exits `1` with `Pull first` unless `--force` is passed (preserving earlier Cloud versions in history). Untracked skill directories are listed so you can register them with `fw skills sync PATH`. Push statuses are pushed (in a dry run, would be pushed), refused, missing (every copy gone), untracked, cancelled, and failed. Run `--dry-run` first and confirm with the user before passing `--yes`.

```sh
fw skills push --dry-run
fw skills push --yes --json
```

A project can hold the same skill in `.claude/skills/` and `.agents/skills/`. When the copies differ, skill sync, `fw skills push`, and `fw push` stop with `assistant skill copies diverged`, naming both copies. The message gives the `fw skills copy` command that fixes it. `fw skills copy` makes the copies match: for each skill (a folder with a `SKILL.md` file), it copies `SKILL.md` and its syncable companion files, with their executable modes, from one folder to the others. Files sync excludes, such as `.env`, are not copied and stay in the target. `--from` and `--to` take `claude` or `agents` (`codex` and `copilot` also mean `agents`); `--from-dir` and `--to-dir` take project-relative folders. Without `--to`, `fw` copies to every other assistant skills folder in the project. `fw` backs up each replaced copy to `.framework/skills-backups/`; other files that only the replaced copy had are gone from it, and kept in the backup. It skips copies that already match and never deletes a skill. When the source skill is tracked, `fw` records each copy in its entry in `.framework/skills.lock.json`, keeping its version, so `pull` moves every copy together and a later push still sends the edited skill. A folder the lock records as a different skill is never copied over. `pull` and sync never treat a folder as a copy because of its name or contents: only `fw skills copy` records one. When the copies have different folder names, the message explains the fix in words instead of giving a command. Skills only in a target are listed, not read or changed, and folders whose names start with a dot are skipped. An assistant skills folder that is a symlink or a file is an error, and nothing is written. No target may be in `.framework/`, and a flag given with no value, such as `--to-dir=`, is an error. In `--json`, a skill only in a target has `"from": null`, and every row has `backup`, the replaced copy's backup, or null. If a copy fails partway, `fw` puts back the copies it already made, or lists each one it couldn't put back with its backup. Ask the user which copy to keep before copying.

```sh
fw skills copy --from claude --dry-run --json
fw skills copy --from claude --skill ggplot2-figures
```

### Known limits

- Use one skill per folder name in a project.
- Copy skills between assistants with `fw skills copy`, not by hand. A copy made by hand isn't tracked, and `pull` leaves it alone.
- On macOS, folder names that differ only in letter case are the same folder. Pick one spelling.
- When a command refuses, it has changed nothing. Follow its message.

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
