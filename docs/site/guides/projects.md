---
id: projects
title: Project creation and setup
description: Project creation, adoption, and bootstrap for R, Python, and Stata
section: guides
position: 3
---

fw creates projects through each language's own Framework package, so a
project made with fw is the same as one made in R or Python.

## Project sources

`fw new` takes a project from one of four places:

- `--from SLUG`: one of your projects on Framework Cloud.
- `--from FILE`: a layers file, such as one downloaded from Framework Cloud.
- A name and pieces on Framework Cloud: a blueprint, a project structure,
  tools, package sets, skills, and settings.
- A name, `--lang`, and `--type`: a new project from the language's own
  defaults, without the cloud. Without `--type`, the type is `project`, the
  Standard Project. Pass `--type bare` for a bare project.

```sh
fw new --from vaccine-study
fw new --from layers.json --dir vaccine-study
fw new survey --blueprint framework/project --tool renv --sensitive
fw new survey-analysis --lang r --type project_sensitive
```

At a terminal, `fw new` with no name walks you through it. It asks for the
project's name and language and, when you are logged in, a blueprint. Flags
you give answer their questions, and without a terminal fw never asks.

## Project location

`--dir` names the project folder. Without it, fw asks at a terminal:

```text
Create the project here (/home/ada/survey)? [y/n]
```

With a Projects folder set in Framework Cloud Settings, fw offers a folder
there instead:

```text
Create the project in /home/ada/research/survey? [y/n]
```

Logged in, fw asks the server for it; otherwise, or offline, it reads the
copy of your settings that R's and Python's `cloud_sync()` keep. With
`--pull-token`, the project owner's Projects folder, sent with the project,
comes first. A missing Projects folder is created with the project.

Enter or `y` takes the offered folder. `n` asks for another folder, which
holds the project folder. A path that already ends in the project's folder
name is used as is, and `~` is your home folder. Without a terminal, or with
`--headless`, fw stops and asks for `--dir`. This covers `--pull-token`,
`--zip`, and a GitHub project cloned here too.

## Build location

When you are logged in and the server offers more than one build location,
fw asks which to use:

- **Build it here.** fw runs the language's package on this computer. This
  is the default and needs R, Python, or nothing for Stata.
- **Download it ready-made.** Framework Cloud builds the project, and fw
  unpacks it into the folder. Nothing needs to be installed, and there is no
  Git repository.
- **Create it on GitHub.** Framework Cloud creates a repository and builds
  the project there. fw waits for it and clones it into the folder.

Each of these is also a flag, for scripts and agents:

| Flag | Effect |
|---|---|
| `--local` | built here, without asking |
| `--zip` | built on Framework Cloud and downloaded here |
| `--github OWNER` or `--github OWNER/REPO` | a new repository, private unless `--public`; `--no-clone` leaves it on GitHub |

`--zip` and `--github` create the project on Framework Cloud and link the
folder to it, so they need `fw login`. GitHub also has to be connected on
the server, with the Framework app installed on the owner.

```sh
fw new
fw new survey --lang python --zip
fw new survey --blueprint framework/project --github my-lab/survey
```

## Cloud pieces

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

Some flags can be repeated, as in `--tool renv --tool
targets`. List what you can choose from with `fw blueprints list`,
`fw structures list`, `fw tools list`, `fw package-sets list`, and
`fw skills list`. Stata projects have no tools.

These flags need `fw login`. At a terminal, fw then asks whether to also
create the project on Framework Cloud, defaulting to No. A project created
there is linked, as with `--from`. Without a terminal, fw creates it on the
cloud only with `--cloud`. `--no-cloud` keeps it on this machine without
asking.

## Stata projects and saved defaults

When you are logged in, `fw new NAME --lang stata` builds the project from
your defaults on Framework Cloud and saves them on this machine, in
`cache/defaults/` in the Framework configuration folder. If the server
cannot be reached later, fw uses the saved defaults and prints the date they
were saved. With nothing saved, fw asks before using its built-in defaults
at a terminal. Without a terminal it stops, unless you add `--offline`.
`--offline` never contacts the server: fw uses the saved defaults, or its
built-in ones when none are saved. A rejected login is an error and never
falls back to saved defaults.

Without a login, and with `--type`, fw uses its built-in setups.

Python and R projects follow the same rules inside framework-py 0.1.0.dev6
or later and the framework R package. fw passes `--offline` and
`--workflow` (`quarto`, `jupyter`, or `scripts`) through to them. fw checks
that the installed R package takes them, and says to update it when it does
not. Rscript cannot ask questions, so when you are logged in and the server
cannot be reached, fw asks at a terminal before building an R project
offline. Without a terminal, add `--offline`.

Logged in, framework-py and the R package also register a project built
from your defaults on Framework Cloud and write `FW_PROJECT_TOKEN` to its
`.env`. `--offline` keeps it on this machine.

## Git repositories

A project built here gets a git repository and a first commit, unless your
defaults turn git off or the folder is already inside a repository.
`--headless`, for automation, never prompts, and makes no repository in
these cases:

- with `--from` and a layers file, in every language
- for Stata projects, which fw builds itself

`--offline --headless` without `--from` still runs `git init` for R and
Python projects. fw has no `--no-git` to pass on: neither the framework R
package's `new()` (2.0.0) nor framework-py's `create` (0.1.0.dev9) has a
setting that skips git. To build one without git, turn git off in your
defaults, create it from a layers file with `--from` and `--headless`, or
delete `.git` afterwards.

## Existing folders

`fw adopt [DIR]` makes a folder you already work in a Framework project
without moving anything. fw looks at folder names only, never file names or
contents, and at most three levels deep. It skips version-control folders,
environments, caches, and hidden folders. It proposes a Framework directory
for each folder it recognizes, such as `data/raw` for `inputs_raw`, and
lists the rest as other folders. At a terminal you can change a folder's
role or leave it out. Without one, read the proposal and pass `--yes`.

fw detects the language from the files at the top of the folder and says
which file decided it: `.do` files, `pyproject.toml`, or `renv.lock`,
`DESCRIPTION`, or an `.Rproj` file first, then weaker signs such as
`setup.py`, `requirements*.txt`, notebooks, and `.R`, `.Rmd`, or `.qmd`
files. `--lang` overrides it. A folder of loose files with no subfolders is
adopted with no directories.

A folder whose path inside the project contains "private" gets a warning.
Keep such folders out of git and away from AI assistants. `--sensitive`
turns on Framework's guards for private data. fw infers the type from the
folders, as R and Python do: `course` when two or more course folders
(such as `slides/`, `modules/`, and `assignments/`) are at the top,
`project_sensitive` when private and public data folders map, `project`
when any folder maps, and `bare` otherwise. `--type` sets it: `project`,
`project_sensitive`, `course`, `presentation`, or `bare` (Stata takes
`project`, `project_sensitive`, and `bare`). Framework's `settings/` folder
and Quarto's render folders (`*_files`, `_site`) are never project folders,
and rendered-notebook folders such as `outputs/notebooks` become
`render_dirs`. fw passes both to the R and
Python packages, and says when the installed package is too old to take
them.

For a Stata project fw writes `settings.do`, `settings.yml`, and
`.framework/spec.json`, and appends the `.gitignore` lines the project
lacks for secrets and private folders. It never changes a line already in
`.gitignore`. For Python projects fw runs framework-py's `adopt`, and for R
projects it shows the proposal and runs `framework::adopt()` to accept it.
To change a folder's role in an R project, run `framework::adopt()` in R.
`--json` without `--yes` prints the proposal and changes nothing.

In a sensitive project, fw lists the folders that may hold restricted data
(inputs and outputs folders, `data`, `cache`, `scratch`, and folders named
like data, exports, reports, results, raw, tables, figures, or cohorts)
and, at a terminal, asks about each. `--private FOLDER`, repeatable, marks
a folder without asking: it keeps its path and gets a private key
(`inputs_raw` becomes `inputs_private_raw`; another folder becomes
`outputs_private_<name>` or `inputs_private_<name>`), and `.gitignore`
keeps it out of git. R needs a framework package newer than 2.0.1 for it,
and Python a framework-py newer than 0.1.0.dev13.

fw adopt stops without changing anything if the folder is already a
Framework project, and names the file it found. A `config.yml` counts only
when it is Framework's own. Upgrade an existing project instead:
`fw project upgrade` prints what would change, and `--apply` makes the
changes. R projects run `framework::upgrade()`, Python projects
`python -m framework upgrade`, and fw upgrades Stata projects itself. It
never removes a line, and backs up each file it changes to
`.framework/backups/`.

`fw project upgrade --type TYPE` changes a project's type, as R's
`upgrade(type =)` does. To `project_sensitive` it turns on
`security: sensitive` and adds the Privacy Sensitive layout's private and
public folders the project lacks, keeping its own folders; the `.gitignore`
rules, the private folders, and the guard that keeps Claude Code out of
them follow in the same pass. A sensitive project changed to another type
stays sensitive. `--move-settings` moves settings keys outside the
environment sections into `settings/<key>.yml` (R and Python). fw passes
both to R's `upgrade()` (a framework package newer than 2.0.1) and to
framework-py (newer than 0.1.0.dev13), and says when the installed package
lacks them. In Stata, fw makes the change itself, adding the new folders to
`settings.yml` and `settings.do`, and takes `project`, `project_sensitive`,
or `bare`. fw also creates a Stata project's missing private folders, which
git doesn't keep, in `fw project upgrade` and `fw project bootstrap`.

The project stays on this machine until you create it on Framework Cloud.
`fw project link --create` sends `.framework/spec.json` to Framework Cloud,
which makes the project from exactly that spec (its folders, type,
language, and tools, without your defaults) at the folder's slug or
`--slug`, and marks an adopted project as adopted. fw then links the folder
with a project token in `.env`. `fw adopt --cloud` does the same right
after adopting. Running it again on a linked project changes nothing; send
a changed spec with `fw project push-spec`. If you already have a Cloud
project with that slug, fw offers `fw project link --from SLUG` instead. A
slug held by a project in your Trash can't be reused; restore that
project and link to it, or choose another slug. An older self-hosted server builds the project from your
defaults, and fw sends the spec separately.

Both commands check the spec against spec.v1 before sending it, as Framework
Cloud does. A spec that an older `upgrade()` wrote can hold
`scaffold.positron` and `source.upgraded_from`, which spec.v1 doesn't
allow; fw leaves them out of what it sends, with a warning, and
`fw project upgrade --apply` (in R, with a framework package newer than
2.0.1) drops them from the file. Any other problem stops fw before it
sends anything, with each key named; fix it with
`fw project upgrade --apply`.

`fw structures import --type` records the project type a saved layout is
for, such as `bare` or `project_sensitive`.

`fw structures import [DIR]` maps a folder the same way and saves the layout
as a structure on Framework Cloud, private to you, so new projects can use
it with `fw new --structure`. Only folder paths and their roles are sent.
`--json` without `--yes` shows the proposal and saves nothing.

## Languages

- **R** projects are created with `framework::new()` or
  `framework::project_create_from_layers()` through `Rscript`.
- **Python** projects are created with `python -m framework create`, run
  through `uv` when it is installed. fw asks uv for framework-py 0.1.0.dev9
  or later and has it check for new releases first
  (`--refresh-package framework-py`), unless `UV_OFFLINE` is set.
- **Stata** projects are built by fw itself: a master do-file, `settings.do`
  with path globals, `settings.yml`, a log folder, and a `.gitignore` that,
  as in R and Python, keeps `private/` folders and logs out of git. Starter
  do-files come only from a blueprint.

`settings.do` and `settings.yml` divide a Stata project's settings.
`settings.do` sets the path globals that do-files use, such as
`$inputs_raw`. `settings.yml` lists the same folders and holds the project
name, the data catalog for `fw_use` and `fw_save`, the publishing
destinations for `fw_publish`, and, in `project_sensitive` projects,
`security: sensitive: true`. It starts with an empty catalog and commented
examples. Framework for Stata 0.2.0 or later reads both files; when they set
a folder differently, `settings.yml` wins and `fw_project` notes it.
`master.do` runs `fw_project` and stops with the install command when
framework-stata 0.2.0 or later is missing.

Types and workflows differ by language:

| Language | `--type` | `--workflow` |
|---|---|---|
| R, Python | `project` (the default), `project_sensitive`, `course`, `presentation`, `bare` | `quarto`, `jupyter`, `scripts` |
| Stata | `project` (the default), `project_sensitive`, `bare` | `scripts` |

fw stops with a message naming these when a Stata project asks for another
type or workflow.

There is no Stata presentation type. Stata's own `dyndoc` turns Markdown
with Stata output into an HTML page, not slides, and a deck would need
Quarto or pandoc on top, which Stata projects do not use. For a talk, create
a Stata project with `--type project`, export the figures to
`outputs/figures`, and build the deck in an R or Python presentation project
from those files.

When R, Python, or uv is missing, fw prints the exact command to run once it
is installed, and keeps the layers file for it. `--print` shows that command
without running anything.

## Project environments

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
