---
id: agents
title: AI agents and scripts
description: JSON output, exit codes, and browser-approved login
section: guides
position: 1
---

AI coding agents and scripts can run fw as well as people. Commands that report information take `--json`, failures have
distinct exit codes, and the login keeps the token away from the agent.

## Example session

Without a terminal, `fw new` needs `--dir` for the project folder.

```sh
fw login --yes --json                 # a person approves the link
fw projects list --json
fw structures list --lang r --json    # also tools, package-sets, and skills
fw new --from vaccine-study --dir vaccine-study && cd vaccine-study
fw project bootstrap
fw project push-spec
fw publish outputs/notebooks/results.html --slug results --yes --json
fw publish outputs/notebooks/results.html --to github --yes --json
```

## Login without a terminal

When fw has no terminal, `fw login` needs `--yes`, given after reading the
notice it prints. It then prints a code and a link on stderr and waits. A
person opens the link in a browser where they are signed in and approves
it. fw stores the token in the keyring. The agent never sees it.

```sh
fw login --yes --json
```

With `--json`, the code arrives on stderr as one line. Show it to the
person:

```json
{"user_code": "QGMK-JE6Q", "verification_url": "https://framework.pub/device?code=QGMK-JE6Q", "expires_in": 600}
```

The code expires after 10 minutes. When it is approved, stdout gets the same
object as `fw status --json`.

> [!WARNING]
> Never paste a token into an agent's prompt or onto a command line. fw does
> not accept tokens as arguments, because arguments end up in shell history
> and process lists. In automation, pipe a token from a file:
> `fw login --with-token --yes < token.txt`.

## JSON output

These commands take `--json`: `login`, `status`, `projects list`,
`project show`, `blueprints list`, `project status`, `publish`,
`project push-spec`, `skills pull` and `setup pull` (Stata projects), `skills push`, and `setup push` (Stata projects). The output goes to stdout as one JSON document, and
messages for people go to stderr. Each command's reference page lists its
fields. Token values never appear in the output.

```sh
fw projects list --json | jq -r '.projects[].slug'
```

## Exit codes

| Code | Meaning |
|------|---------|
| 0 | Success |
| 1 | Failure |
| 2 | Bad flags or arguments |
| 3 | Not logged in, or the server rejected the token |
| 4 | The server has no such project or resource |
| 5 | Browser login was denied, or its code expired |
| 6 | An upload has "private" in a path and needs `--include-private` |

With `--json`, a failure also prints one JSON line on stderr:

```json
{"error": "You have no project named 'nope'. (HTTP 404)", "exit_code": 4}
```

Exit code 6 also carries `"reason": "private_path"`. Show the person which
files have "private" in their path, and rerun with `--include-private` only
if they confirm the upload.

## Confirmation prompts

fw asks before it connects a machine (`fw login`), before it uploads
(`fw publish`), before it sends new skill versions (`fw skills push`), and
before it applies or sends a project's setup (`fw setup pull`,
`fw setup push`). Without a terminal it cannot ask, so these need `--yes`.
fw prints what it is about to do first, so an agent can show it to the
person. `fw project push-spec` asks only when the project's path contains
"private". `fw new` with cloud choices such as `--blueprint` never creates
the project on Framework Cloud without a terminal unless you pass `--cloud`.
`fw new NAME --lang stata` while logged in stops with exit 1 when the server
cannot be reached and no defaults are saved. Pass `--offline` to use fw's
built-in defaults instead. Nothing else prompts when there is no terminal.

## Project skills

`fw skills pull` brings a project's skills up to date with its setup on
Framework Cloud. It adds missing skills and updates unedited ones. It keeps
skills the person edited and lists them; `--overwrite` replaces them, with the
old copies in `.framework/skills-backups/`. It never deletes a skill that is
no longer in the setup. It refreshes the Skills table in AGENTS.md. Without a
project token, or when Framework Cloud can't be reached, it says so and uses
the package skills for the project's type. In R and Python projects, fw runs
the package's `pull_skills()`.

```sh
fw skills pull --dry-run --json
fw skills pull
```

The summary reads `Skills: 2 added, 3 updated, 5 up to date, 1 locally edited (kept)`.

`fw skills push` sends edited skills to Framework Cloud as new versions of the
person's own skills. Before sending, it prints what each new version replaces.
A skill that changed on Framework Cloud since the last pull is refused with
"Pull first", and the exit code is 1; `--force` replaces it, and the Cloud
changes stay in the skill's history. An edited built-in skill is saved as the
person's own copy. Skills that fw doesn't track are listed; add one with
`fw skills sync PATH`. Without a terminal, push needs `--yes`. Show the
person the plan from `--dry-run` first.

```sh
fw skills push --dry-run
fw skills push --yes --json
```

## Project setup

A project's setup on Framework Cloud is its packages and skills. Folders and
structure are not part of it. `fw setup pull` lists the changes, then asks
"Apply these changes?" before applying them. It adds packages to the
project's settings and installs them, then pulls the skills as
`fw skills pull` does. It never removes a local package. It keeps local edits
such as version pins unless you pass `--overwrite`. It saves the setup's
revision in `.framework/setup.lock.json`.

`fw setup push` shows what would change on Framework Cloud, with a warning
that new setups, ZIP downloads, and Update on GitHub will use it. It asks
"Push these changes?", then pushes edited skills as `fw skills push` does.
When the setup changed on Framework Cloud since the last pull, it says "Pull
first". `--force` replaces Cloud's newer changes. `--remove` also removes
packages the project doesn't have.

```sh
fw setup pull --dry-run
fw setup pull --yes
fw setup push --dry-run
fw setup push --yes
```

Without a terminal, both commands need `--yes`, or `--dry-run` to only list
the changes. Show the person the `--dry-run` list first. In R and Python
projects, fw runs the package's `pull_setup()` and `push_setup()`. At a
terminal, fw runs them once as a dry run, asks, and then applies. Packages
aren't managed for Stata projects, so for those, these commands pull and
push skills only.

## Uploads and private paths

fw never uploads anything on its own. Data files and project code go to the
cloud only when you run `fw publish` or `fw project push-spec`, and
`fw publish` sends only the path it is given. `fw publish --to github`
commits and pushes only `published/SLUG/` in the project's repository, and
its JSON is `{"url", "commit", "pushed", "pages_enabled"}`. Tell the person
that a GitHub Pages site is public, even from a private repository. When a file's full path
contains "private", in any letter case, fw stops with exit code 6 unless you
pass `--include-private`. Sensitive projects keep their AI deny rules, which
stop agents from reading private folders and `.env`.
