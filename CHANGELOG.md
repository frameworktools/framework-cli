# Changelog

## Unreleased

- Skills lock v2. `.framework/skills.lock.json` tracks each skill by its identity (a built-in, a Cloud skill, setup text, or a folder not yet pushed) and records the last synced hash of each copy. fw reads a schema 1 lock and writes schema 2 only when a command changes the lock. A lock from a newer client stops skill commands with `lock_schema_unsupported` and the advice to update fw.
- `fw skills pull`, `fw skills push`, `fw skills copy`, and the skill parts of `fw pull` and `fw push` run one planner, shared with the R and Python packages and checked against 63 shared scenarios. Changes in behavior:
  - A copy reverted to an older version counts as edited and is kept.
  - A copy missing next to unedited copies is restored. Next to an edited copy, the skill is diverged.
  - A new assistant folder gets the setup's copy when the other copies are unedited.
  - Offline, every tracked skill the package doesn't ship, your own Cloud skills included, is reported unavailable.
  - Setup text (inline skills) is recorded in the lock.
  - A skill whose copies are all gone is restored from Framework Cloud at its newest version when you are signed in.
  - After a folder rename, pull and push name `fw skills move` instead of `fw skills sync --id`. `fw skills sync --id` no longer moves an entry.
  - A copy tracked as another skill, or another variant, is kept with the `fw skills move --id` command that switches it.
- New: `fw skills move FROM [TO]` moves tracked copies to another folder, or records a move made by hand, and `--id`, `--builtin`, and `--variant` switch which skill a copy tracks.
- `--json` rows add `reason_code`, `hint_key`, and `hint_args`. `fw skills push --json` rows list `paths` instead of `path`.
- Built-in Stata skills ship `internal/stata/skill-history.json`, the hash of every version fw has shipped, so an older unedited copy is recognized.
- R projects need framework 2.1.0 or later for skill commands: fw checks for `skills_copy()` and `skills_move()`.
