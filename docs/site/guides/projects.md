---
id: projects
title: Project creation and setup
description: Project creation, adoption, upgrade, and bootstrap for R, Python, and Stata
section: guides
position: 3
---

`fw` builds R and Python projects through each language's Framework package and builds Stata projects natively, producing identical project layouts across interfaces.

## Project sources

`fw new` builds a project from one of four sources:

- `--from SLUG`: an existing project on your Framework Cloud account.
- `--from FILE`: a saved layers JSON file.
- A project name combined with Framework Cloud flags (`--blueprint`, `--structure`, `--tool`, `--package-set`, `--skill-set`, `--skill`, and settings flags).
- A project name with `--lang` and `--type`: a project built from the language's own defaults (`project` by default, or `bare` for a minimal layout).

```sh
fw new --from vaccine-study
fw new --from layers.json --dir vaccine-study
fw new survey --blueprint framework/project --tool renv --sensitive
fw new survey-analysis --lang r --type project_sensitive
```

At an interactive terminal, running `fw new` with no arguments prompts for the project name, language, and (when signed in) blueprint. Explicit flags skip the corresponding prompts. Without a terminal, `fw` never prompts.

## Target directory

Pass `--dir` to specify the target directory. Without `--dir`, an interactive terminal prompts for confirmation:

```text
Create the project here (/home/ada/survey)? [y/n]
```

When a **Projects folder** is configured in Framework Cloud (**Settings > Project defaults**), `fw` offers a subdirectory inside that folder instead:

```text
Create the project in /home/ada/research/survey? [y/n]
```

When signed in, `fw` reads the **Projects folder** setting from the server; offline or signed out, it reads the local cache written by `cloud_sync()` in R or Python. With `--pull-token`, the project owner's **Projects folder** takes precedence. Missing parent directories are created automatically.

Press Enter or `y` to accept the suggested path, or `n` to enter a different parent directory. If the entered path already ends with the project slug, `fw` uses it directly. `~` expands to your home directory. Without a terminal, or with `--headless`, `--dir` is required across local, `--pull-token`, `--zip`, and cloned `--github` builds.

## Build destination

When signed in to a server that supports remote builds or GitHub scaffolding, interactive `fw new` asks how to provision the project:

- **Local setup (`--local`)**: runs the language scaffolder on this machine (the default; requires R or Python, or no extra runtime for Stata).
- **ZIP download (`--zip`)**: builds the project on Framework Cloud and extracts the archive locally without initializing a Git repository or requiring local language runtimes.
- **GitHub repository (`--github OWNER` or `--github OWNER/REPO`)**: creates a GitHub repository (private unless `--public` is passed), scaffolds the project on GitHub, and clones it locally unless `--no-clone` is passed.

| Flag | Behavior |
|---|---|
| `--local` | Scaffold locally without prompting |
| `--zip` | Build on Framework Cloud and extract locally |
| `--github OWNER[/REPO]` | Scaffold in a new GitHub repository (`--public` makes it public; `--no-clone` skips cloning) |

Both `--zip` and `--github` register the project on Framework Cloud, write `FW_PROJECT_TOKEN` to `.env`, and require `fw login`. `--github` also requires a connected GitHub account and the Framework GitHub App installed on the target owner.

```sh
fw new
fw new survey --lang python --zip
fw new survey --blueprint framework/project --github my-lab/survey
```

## Cloud components

The flags below assemble a project from components on Framework Cloud. Unspecified fields inherit your **Settings > Project defaults** on Framework Cloud, and `--defaults` uses those defaults directly.

| Flag | Component |
|---|---|
| `--blueprint KEY` | Blueprint preset |
| `--structure KEY` | Directory layout and core Framework files |
| `--tool ID`, `--no-tool ID` | Optional integrations (`quarto`, `targets`, `renv`, `uv`, `jupyter`) |
| `--package-set KEY` | Named package list |
| `--skill-set KEY`, `--skill KEY` | AI assistant skills |
| `--sensitive`, `--ai`, `--git` | Boolean project flags (pass `=false` to disable) |

Repeatable flags can be combined, such as `--tool renv --tool targets`. Inspect available keys with `fw blueprints list`, `fw structures list`, `fw tools list`, `fw package-sets list`, and `fw skills list`. Stata projects do not use tool layers.

Component flags require `fw login`. At a terminal, `fw` asks whether to register the project on Framework Cloud as well (defaulting to `No`). Without a terminal, pass `--cloud` to register and link the project on Framework Cloud, or `--no-cloud` to keep it local without prompting.

## Offline defaults

When signed in, `fw new NAME --lang stata` resolves your Stata defaults from Framework Cloud and caches them under `cache/defaults/` in the Framework configuration directory. If the server is unreachable on a later run, `fw` uses the cached defaults and prints the cache date. When no cached defaults exist, `fw` prompts before falling back to built-in defaults at a terminal, or exits without a terminal unless `--offline` is set. Passing `--offline` skips network requests and uses cached defaults (or built-in defaults when no cache exists). Rejected credentials always fail rather than falling back to cached defaults.

When signed out, or when `--type` is passed, `fw` uses its built-in templates.

R (`framework` 2.0.0+) and Python (`framework-py` 0.1.0.dev27+) apply the same offline caching rules. `fw` forwards `--offline` and `--workflow` (`quarto`, `jupyter`, or `scripts`) to both packages and reports if an older installed R package lacks support. Because `Rscript` runs non-interactively, `fw` prompts at the terminal when the Cloud server is unreachable before invoking an offline R build; without a terminal, pass `--offline`.

When signed in online, R and Python builds from your Cloud defaults also register the project on Framework Cloud and write `FW_PROJECT_TOKEN` to `.env`. Pass `--offline` to keep the build local-only.

## Git initialization

Local builds initialize a Git repository and create an initial commit unless Git is disabled in your defaults or the target directory already sits inside a Git repository. Passing `--headless` disables prompts and skips Git initialization in two cases:

- Builds from a local layers file (`--from FILE`) in any language.
- Stata builds scaffolded directly by `fw`.

`--offline --headless` without `--from` still runs `git init` for R and Python projects because `framework::new()` and `python -m framework create` do not expose a flag to skip Git on built-in defaults. To omit `.git`, disable Git in your Cloud defaults, pass a layers file with `--from` and `--headless`, or remove `.git` after creation.

## Existing directories and upgrades

`fw adopt [DIR]` converts an existing project directory into a Framework project in place without moving or overwriting files. `fw` scans directory names up to three levels deep (never filenames or file contents), skipping version-control metadata, virtual environments, caches, and hidden directories. It maps recognized names (such as `inputs_raw` to `data/raw`) and records remaining directories under `extra_directories`. Interactive runs let you reassign or omit any directory; non-interactive runs require `--yes` after reviewing the proposal.

`fw` detects the project language from top-level files and reports the deciding file: `.do` files for Stata; `pyproject.toml` for Python; `renv.lock`, `DESCRIPTION`, or `*.Rproj` for R; followed by `setup.py`, `requirements*.txt`, `.ipynb`, `.R`, `.Rmd`, or `.qmd`. Pass `--lang` to override detection. Directories containing only top-level files adopt cleanly with an empty directory map.

Paths containing `private` trigger a warning. Pass `--sensitive` to enable privacy guards. `fw` infers the project type from mapped directories (`course` when two or more course directories appear at the root, `project_sensitive` when both private and public data directories map, `project` when any directory maps, and `bare` otherwise), or accepts an explicit `--type` (`project`, `project_sensitive`, `course`, `presentation`, or `bare`; Stata supports `project`, `project_sensitive`, and `bare`). Framework's `settings/` directory and Quarto output directories (`*_files`, `_site`) are excluded from directory roles, while rendered-output directories such as `outputs/notebooks` become `render_dirs`.

For Stata projects, `fw adopt` writes `settings.do`, `settings.yml`, and `.framework/spec.json`, and appends missing secret and private-directory rules to `.gitignore` without altering existing lines. Python projects run `framework-py`'s `adopt`, and R projects preview the mapping before running `framework::adopt()` (to customize individual directory roles in R, run `framework::adopt()` inside an R session). Running `fw adopt --json` without `--yes` prints the proposed mapping as JSON without modifying disk.

In sensitive projects, `fw` identifies directories that may hold restricted data (`data`, `cache`, `scratch`, input/output directories, and directories named after exports, reports, results, raw files, tables, figures, or cohorts) and asks whether to mark each one private. Pass `--private FOLDER` (repeatable) to mark paths non-interactively: each directory keeps its current path on disk, receives a `*_private_*` directory key, and is added to `.gitignore` (requires `framework` > 2.0.1 in R and `framework-py` > 0.1.0.dev13 in Python).

If the target directory is already a Framework project, `fw adopt` exits without making changes and names the detected configuration file. Run `fw project upgrade` instead to inspect layout updates, and pass `--apply` to write them. R runs `framework::upgrade()`, Python runs `python -m framework upgrade`, and `fw` upgrades Stata projects directly (updating `master.do` to run `fw_scaffold` when `framework-stata` 0.3.0+ is installed, adding missing `settings.yml` and `.gitignore` entries, and backing up modified files under `.framework/backups/`).

`fw project upgrade --type TYPE` converts a project's type. Switching to `project_sensitive` enables `security.sensitive`, adds missing private and public directories alongside existing ones, updates `.gitignore`, and writes AI assistant read-deny rules. Sensitive projects retain `security.sensitive: true` even if switched to another layout type. In R and Python, `--move-settings` splits top-level settings sections into `settings/<key>.yml`.

Adopted projects remain local until registered on Framework Cloud. Run `fw project link --create` (or pass `--cloud` during `fw adopt`) to create the Cloud project directly from `.framework/spec.json` at the folder's slug (or `--slug`), link `.env` with `FW_PROJECT_TOKEN`, and preserve the adopted layout without mixing in account defaults. Subsequent spec updates use `fw project push-spec`. If the slug already exists on your account, `fw` suggests `fw project link --from SLUG`; slugs held by trashed projects must be restored or replaced with another `--slug`.

Both `fw project link --create` and `fw project push-spec` validate `.framework/spec.json` against `spec.v1` before uploading. Legacy keys written by older `upgrade()` versions (`scaffold.positron` and `source.upgraded_from`) are stripped from the payload with a warning and removed permanently by `fw project upgrade --apply`. Any other schema violation stops the upload and lists the offending keys.

`fw structures import [DIR]` scans a directory with the same mapping rules and saves its directory roles as a private reusable **Project structure** on Framework Cloud (use `--type` to tag the target project type). Only directory paths and role keys are uploaded.

## Language engines

- **R**: runs `framework::new()` or `framework::project_create_from_layers()` via `Rscript`.
- **Python**: runs `python -m framework create` via `uv` when installed (requesting `framework-py` 0.1.0.dev27+ and refreshing package metadata unless `UV_OFFLINE` is set).
- **Stata**: runs `fw`'s built-in scaffolder, generating `master.do`, `settings.do`, `settings.yml`, a `logs/` directory, and `.gitignore`. Starter analysis `.do` files come from blueprints when included.

In Stata projects, `settings.do` defines path globals such as `$inputs_raw`, while `settings.yml` stores the project name, directory map, `data` catalog (`fw_use` / `fw_save`), `packages` list (`fw_packages`), `scaffold` settings (`seed`, `stata_scheme`, `source_all_functions`), `publish` destinations (`fw_publish`), and `security.sensitive`. `framework-stata` 0.2.0+ reads both files (`settings.yml` wins if a directory path differs), and `master.do` runs `fw_scaffold` on `framework-stata` 0.3.0+ (falling back to `fw_project` on 0.2.x).

Supported project types and workflows vary by language:

| Language | `--type` | `--workflow` |
|---|---|---|
| R, Python | `project` (default), `project_sensitive`, `course`, `presentation`, `bare` | `quarto`, `jupyter`, `scripts` |
| Stata | `project` (default), `project_sensitive`, `bare` | `scripts` |

Stata does not have a standalone `presentation` project type because `dyndoc` produces HTML documents rather than slide decks. For slide decks backed by Stata analyses, scaffold a Stata `project`, export figures to `outputs/figures`, and render slides from an R or Python `presentation` project.

When R, Python, or `uv` is not installed, `fw` saves the resolved layers file and prints the exact command to run once the runtime is available. Pass `--print` to display that command without executing it.

## Runtime environments

`fw` locates the enclosing project root and executes commands inside the project's isolated environment:

- **R**: runs from the project root so `.Rprofile` activates `renv`.
- **Python**: runs through `uv run` when `uv.lock` is present, or uses `.venv`.

Run `fw project status` to inspect the detected project root, language, runner command, and Cloud link state.

## Bootstrap after cloning

```sh
fw project bootstrap
```

`fw project bootstrap` runs `framework::bootstrap()` in R or `python -m framework bootstrap` in Python. It restores locked packages, copies `.env.example` to `.env`, creates gitignored directories, and installs Git hooks. It exits `0` once setup succeeds, printing any remaining manual steps such as filling empty `.env` variables.

In Stata projects, `fw project bootstrap` creates any missing gitignored private directories; `settings.do` creates remaining standard directories on first run, and `fw_scaffold` installs packages declared in `settings.yml`.

## Sensitive projects

Projects created or upgraded as `project_sensitive` separate restricted files (`inputs/private/`, `outputs/private/`) from shareable outputs (`outputs/public/`), ignore private paths in `.gitignore`, and write AI assistant configuration rules that block reading `.env` and private directories across R, Python, and Stata.
