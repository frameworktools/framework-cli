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
- Rare states stop before writing anything, with an error code: `lock_migrate_conflict` (two v1 entries for one skill at different versions), `paths_alias` (two setup names that are one folder on a case-insensitive volume), `copy_source_missing`, `copy_source_unreadable`, `copy_target_unreadable`, and `move_target_owned` (a move onto any path the lock tracks).
- A push of a skill whose first push stopped before Framework Cloud replied is refused with `first_push_pending` until `--force`.
- A rollback never deletes a copy it wrote. It moves the copy to `.framework/skills-backups/` and names it in the error.
- New: `fw skills move FROM [TO]` moves tracked copies to another folder, or records a move made by hand, and `--id`, `--builtin`, and `--variant` switch which skill a copy tracks.
- `fw skills push` sends nothing for a skill whose edited bytes Cloud already has: the row is `current`, and the lock records Cloud's version.
- Cloud skills' known hashes include the setup's `sync.known_hashes` and a snapshot's `known_hashes`, when Cloud sends them.
- `--json` rows add `reason_code`, `hint_key`, and `hint_args`. `fw skills push --json` rows list `paths` instead of `path`.
- Built-in Stata skills ship `internal/stata/skill-history.json`, the hash of every version fw has shipped, so an older unedited copy is recognized.
- Commands that write the lock (`fw skills copy`, `fw skills move`, `fw skills sync`, and `fw skills push`) first check that the project's language package reads lock v2: framework 2.1.0 or later in R projects, framework-py 0.1.0.dev31 or later in Python projects, found by their `skills_copy()` and `skills_move()`. Dry runs aren't checked.
- `fw skills push` fails a Cloud skill tracked on another server, before asking or writing anything, and stops with `lock_changed` when the lock changed while it asked to continue.
- A setup skill whose bundle doesn't hash to its `sync.hash` stops the pull before anything is installed.
- A move that fails partway moves back the folders it already moved, and names any it couldn't.
- Case-only name differences are one folder only on a case-insensitive volume, so Linux projects never see a false `paths_alias`.
