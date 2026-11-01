---
id: projects
title: Creating and preparing projects
description: How fw creates R, Python, and Stata projects and runs them in their own environment
section: guides
position: 3
---

fw creates projects through each language's own Framework package, so a
project made with fw is the same as one made in R or Python.

## Creating a project

`fw new` takes a project from one of four places:

- `--from SLUG`: one of your projects on Framework Cloud.
- `--from FILE`: a layers file, such as one downloaded from Framework Cloud.
- A name and pieces on Framework Cloud: a blueprint, a project structure,
  tools, package sets, skills, and settings.
- A name, `--lang`, and `--type`: a new project from the language's own
  defaults, without the cloud.

```sh
fw new --from vaccine-study
fw new --from layers.json --dir vaccine-study
fw new survey --blueprint framework/project --tool renv --sensitive
fw new survey-analysis --lang r --type project_sensitive
```

## Building from cloud pieces

These flags build a project from pieces on Framework Cloud. Your defaults
there (Settings > Defaults) fill whatever you leave out, and `--defaults`
uses only your defaults.

| Flag | Chooses |
|------|---------|
| `--blueprint KEY` | a saved set of choices |
| `--structure KEY` | the folders and Framework's core files |
| `--tool ID`, `--no-tool ID` | tools such as `quarto`, `targets`, `renv`, `uv`, and `jupyter` |
| `--package-set KEY` | a named list of packages |
| `--skill-set KEY`, `--skill KEY` | skills for AI assistants |
| `--sensitive`, `--ai`, `--git` | settings; add `=false` to turn one off |

The flags can be repeated where it makes sense, as in `--tool renv --tool
targets`. List what you can choose from with `fw blueprints list`,
`fw structures list`, `fw tools list`, `fw package-sets list`, and
`fw skills list`. Stata projects have no tools.

These flags need `fw login`. At a terminal, fw then asks whether to also
create the project on Framework Cloud, defaulting to No. A project created
there is linked, as with `--from`. Without a terminal, fw creates it on the
cloud only with `--cloud`. `--no-cloud` keeps it on this machine without
asking.

## Languages

- **R** projects are created with `framework::new()` or
  `framework::project_create_from_layers()` through `Rscript`.
- **Python** projects are created with `python -m framework create`, run
  through `uv` when it is installed.
- **Stata** projects are built by fw itself: a master do-file, `settings.do`
  with path globals, do-file templates, a log folder, and a `.gitignore` that
  keeps `.dta` files out of git.

When R, Python, or uv is missing, fw prints the exact command to run once it
is installed, and keeps the layers file for it. `--print` shows that command
without running anything.

## Inside a project

fw finds the project from the current folder and runs code in the project's
own environment, so its pinned package versions apply:

- R runs from the project folder, where `.Rprofile` activates renv.
- Python runs with `uv run` when `uv.lock` exists, or with the project's
  `.venv`.

`fw project status` shows what fw found and why.

## Bootstrap after a clone

```sh
fw project bootstrap
```

This runs `framework::bootstrap()` for R or `python -m framework bootstrap`
for Python. It restores packages, creates `.env` from `.env.example`, creates
the folders git does not keep, and installs the git hooks. It exits 0 when
the project is ready, even if it lists to-dos such as empty `.env` values.

Stata projects need no bootstrap: `settings.do` creates the missing folders
when it runs.

## Sensitive projects

Projects of type `project_sensitive` keep private and public data in
separate folders. Their AI assistant settings deny reads of private folders
and `.env`. The R and Python packages write those rules, and fw writes the
same ones for Stata projects.
