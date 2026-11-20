# Skills follow-ups

Open items after skills lock v2 (`docs/architecture/skills-lock-v2.md` in the R package). The other items in this file were resolved by v2.

- Revisit the UX: with `--overwrite`, adding an assistant folder replaces an edited package skill, so both folders get the shipped copy. This is the agreed behavior (spec open question 2). Per-copy rows now show it in a dry run.
- The skill scenarios in `internal/spec/testdata/conformance/skills/` are pinned to R commit `c42e771` on the R `skills-lock-v2` branch. Move the pin in `assets.lock.json` to the R release tag that ships v2 (2.1.0) with `go run ./tools/syncassets --from ../framework --ref v2.1.0`.
- fw has no skill upgrade of its own: `fw project upgrade` doesn't touch a Stata project's skills. `skillsync.Pull` takes `Command: "upgrade"` and passes the upgrade scenarios, ready for when it does.
- fw reads `sync.known_hashes` from the setup and `known_hashes` from a skill snapshot. Until Cloud sends them, a project-token pull recognizes an older unedited Cloud copy without a record only by the setup's own hash.
