---
id: agents
title: Using fw from AI agents and scripts
description: JSON output, exit codes, and a login a person approves
section: guides
position: 1
---

fw is built to be driven by AI coding agents and scripts as well as by
people. Commands that report information take `--json`, failures have
distinct exit codes, and the login keeps the token away from the agent.

## A typical session

```sh
fw login --yes --json                 # a person approves the link
fw projects list --json
fw structures list --lang r --json    # also tools, package-sets, and skills
fw new --from vaccine-study && cd vaccine-study
fw project bootstrap
fw project push-spec
fw publish outputs/notebooks/results.html --slug results --yes --json
fw publish outputs/notebooks/results.html --to github --yes --json
```

## Logging in without a terminal

When fw has no terminal, `fw login` needs `--yes`, given after reading the
notice it prints. It then prints a code and a link on stderr and waits. A
person opens the link in a browser where they are signed in and approves
it. fw stores the token in the keyring; the agent never sees it.

```sh
fw login --yes --json
```

With `--json`, the code arrives on stderr as one line, ready to show the
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
`project show`, `blueprints list`, `project status`, `publish`, and
`project push-spec`. The output goes to stdout as one JSON document, and
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

## Questions fw asks

fw asks before it connects a machine (`fw login`) and before it uploads
(`fw publish`). Without a terminal it cannot ask, so these need `--yes`.
fw prints what it is about to do first, so an agent can show it to the
person. `fw project push-spec` asks only when the project's path contains
"private". `fw new` with cloud choices such as `--blueprint` never creates
the project on Framework Cloud without a terminal unless you pass `--cloud`.
Nothing else prompts when there is no terminal.

## What never leaves the machine

fw never uploads anything on its own. Data files and project code go to the
cloud only when you run `fw publish` or `fw project push-spec`, and
`fw publish` sends only the path it is given. `fw publish --to github`
commits and pushes only `published/SLUG/` in the project's repository, and
its JSON is `{"url", "commit", "pushed", "pages_enabled"}`. Tell the person
that a GitHub Pages site is public, even from a private repository. When a file's full path
contains "private", in any letter case, fw stops with exit code 6 unless you
pass `--include-private`. Sensitive projects keep their AI deny rules, which
stop agents from reading private folders and `.env`.
