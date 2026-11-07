---
name: framework-stata
description: Use when writing, editing, or running do-files in this Framework Stata project. Covers master.do, the path globals in settings.do, the data catalog and publishing destinations in settings.yml, logs, and where data and outputs go.
---

# Framework Stata projects

## How the project runs

`master.do` runs the whole analysis: it runs `settings.do`, loads the project
with `fw_project` (from framework-stata), opens a log, and then runs each
step's do-file in order. Run it from the project folder with
`do master.do`. Each step reads the previous step's output, so the steps can be
rerun from scratch at any time.

To add a step, write a new numbered do-file next to the others and add a `do`
line for it in `master.do`. A do-file that no step runs is not part of the
analysis.

## Paths

`settings.do` defines one global per project folder, such as `$inputs_raw`,
`$inputs_final`, and `$outputs_tables`. Refer to folders only through these
globals, so the project runs unchanged on another machine. Do not write
absolute paths or use `cd` in do-files.

Quote paths with compound quotes, because folder names can contain spaces:

```stata
use `"$inputs_final/analysis.dta"', clear
graph export `"$outputs_figures/figure1.png"', replace
```

## settings.yml

`settings.yml` lists the same folders and holds what the framework-stata
commands read: the data catalog under `data:`, which `fw_use` and `fw_save`
take names from, and the publishing destinations under `publish:`, which
`fw_publish, to(NAME)` uses. `fw_project` reads both files. When a folder
moves, change it in `settings.do` and `settings.yml`.

## Data

- Raw data is read-only. Steps write to the intermediate and final folders.
- Everything under `private/` folders is gitignored, and so are logs, cache,
  scratch, and tmp folders. Data elsewhere, such as a public `.dta`, can be
  committed. Data that must stay on the machines that may hold it goes under
  `private/`; git holds the code that rebuilds it.
- Logs from `master.do` go to the logs folder. Check the newest log when a run
  fails.

## Style

- Start each do-file with a comment that says what the step does.
- Use `version 14` (set in `master.do`) and avoid user-written commands unless
  the project already uses them. When one is needed, note the `ssc install`
  line in a comment at the top of the do-file.
- Label variables and values in the cleaning step, so later steps and tables
  read well.
