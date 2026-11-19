# Skills follow-ups

Problems found while reviewing `fw skills copy` that also happen on origin/main. They are left for a later change.

- An ordinary pull overwrites a local revert of a personal skill: a copy put back to a version the setup knows matches `w.Known` and is updated as unedited. `internal/skillsync/pull.go:459`
- An offline pull (package fallback) leaves personal Cloud skills out of its report. Only the package skills and diverged tracked skills are listed, so a healthy personal skill isn't mentioned. `internal/skillsync/pull.go:246`, with the fallback list at `internal/cli/skillspull.go:192`
- Revisit the UX: with `--overwrite`, adding an assistant folder replaces an edited package skill, so both folders get the shipped copy. This is the agreed behavior. `internal/skillsync/pull.go:569`
- Pre-existing on origin/main: after an assistant switch, pull overwrites an independent same-named copy. It picks the entry that records the new folder's path, whatever skill that is, and installs the setup's copy over it (`internal/skillsync/pull.go:214` on origin/main). This branch already preserves that copy: pull finds the setup skill's entry by identity and never plans a folder another entry records.
- Pre-existing on origin/main: after a rename such as `.claude` to `.agents`, push lists the moved copies as not tracked with the bare `fw skills sync PATH` hint. Run as printed, that registers each one as a new skill with a new Cloud ID instead of moving the existing entry; the missing-skill line above it gives the right `fw skills sync PATH --id ID` command. `internal/cli/skillspull.go:564` and `internal/cli/sync.go:783`
