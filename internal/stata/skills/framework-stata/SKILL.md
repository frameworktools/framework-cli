---
name: framework-stata
description: Use when writing, editing, or running do-files in a project that uses Framework for Stata. Covers master.do, the path globals in settings.do, the data catalog and publishing destinations in settings.yml, saving tables and figures with fw_table and fw_graph, publishing with fw_publish, logs, and where data and outputs go.
metadata:
  title: "Framework for Stata"
---

# Framework for Stata

Follow these conventions in do-files that `master.do` runs. Leave existing
code as it is unless the user asks you to change it.

## master.do

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
globals, so the project runs unchanged on another machine. Prefer them to
absolute paths and `cd`.

Quote paths with compound quotes, because folder names can contain spaces:

```stata
use `"$inputs_final/analysis.dta"', clear
save `"$inputs_final/analysis_clean.dta"', replace
```

## settings.yml

`settings.yml` lists the same folders and holds the project name, the data
catalog under `data:`, the publishing destinations under `publish:`, and the
security settings. Framework for Stata (`fw_project`) reads both files. When
the two set a folder differently, `settings.yml` wins and `fw_project` prints a
note, so change a folder in both.

Catalog entries are nested by folder and read by their dotted name:

```yaml
data:
  inputs:
    raw:
      survey:
        path: inputs/raw/survey.csv
        locked: true
        stata_names:
          reason_ecmoref_refpicu_any_cardiac: reason_cardiac
```

```stata
fw_use inputs.raw.survey, clear
```

`stata_names` maps a column name in the file to a Stata variable name, for
names longer than 32 characters or not valid in Stata. `fw_save` under the same
catalog name writes the file's own column names back, so R and Python read the
names they expect.

## Tables, figures, and publishing

Prefer the Framework commands to `export delimited` and `graph export`. They
create folders, record the result, and in a sensitive project
save to the private folders unless given `public`:

```stata
fw_table "meeting/table-1" group n pct, headers("Group" "Patients" "Share, %")
fw_graph "meeting/figure-1", format(png pdf)
```

`fw_table` saves the data in memory, or a matrix with `matrix()`, to the tables
folder with free-text headers, as `save_table()` does in R and Python.
`fw_graph` exports the current graph, or `name()`, to the figures folder, as
`save_figure()` does. A name may hold subfolders.

`fw_publish` sends a file, a folder, or a wildcard such as
`"$fw_outputs_figures/*.png"` to a destination in `settings.yml`. A folder or
wildcard becomes one artifact, as `publish_dir()` does:

```stata
fw_publish "$fw_outputs_figures", to(results)
```

## Data

- Raw data stays unchanged. Steps write to the intermediate and final folders.
- Everything under `private/` folders is gitignored, and so are logs, cache,
  scratch, and tmp folders. Data elsewhere, such as a public `.dta`, can be
  committed.
- Logs from `master.do` go to the logs folder. Check the newest log when a run
  fails.

## Style

- Start each do-file with a comment that says what the step does.
- Keep the `version` line in `master.do`.
- Prefer built-in commands to user-written ones. When one is needed, note the
  `ssc install` line in a comment at the top of the do-file.
- Label variables and values in the cleaning step, so later steps and tables
  read well.
