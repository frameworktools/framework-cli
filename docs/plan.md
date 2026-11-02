# fw: plan

Status: approved, 2026-10-30.

`fw` is the command-line helper for Framework. It works with framework.pub or a
self-hosted server, creates projects by calling each language's own package,
and scaffolds Stata projects natively. The R and Python packages never need it.

## Command tree

```
fw login      [--url URL] [--with-token]    store a user token for a server
fw logout     [--url URL]                   remove the stored token for a server
fw status     [--url URL]                   server in use and its source, signed-in user,
                                            and the current project's link when inside one
fw new        [NAME] [--type T] [--lang r|python|stata] [--dir PATH]
              [--from SLUG|layers.json] [--pull-token] [--headless] [--print]
fw projects   list                          the user's projects on the server
fw project    show SLUG                     one project's details on the server
              link --from SLUG | --pull-token
                                            write FW_PROJECT_TOKEN and FW_CLOUD_URL to .env
              status                        this project's server, link, and runtime
              bootstrap                     run the language's bootstrap() in the project's env
              push-spec                     send .framework/spec.json to the project page
fw blueprints list                          blueprints the user can start a project from
fw publish    PATH [--slug S] [--readable]  publish rendered HTML with the project token
fw config     sync | path                   pull settings from R's folder; print config paths
fw doctor                                   runtimes found, keyring backend, config paths
fw version | fw completion SHELL
```

- `fw login` reads the token from a hidden prompt, or from stdin with
  `--with-token`, as `gh` does. It verifies the token with `GET /api/v1/me`,
  stores it, and saves the server as the default in `cloud.yml`. A browser
  sign-in flow can come later (see the endpoints section).
- `fw new --from SLUG` fetches the signed-in user's project layers, creates
  the project, and then requests a pull token for its `.env`.
  `fw new --from FILE.json` reads a layers file.
- `fw new --pull-token` creates a project from a project token, read from a
  hidden prompt or from stdin. This is the path for people who are not signed
  in. A token is never accepted as an argument, because arguments are saved
  in shell history and are visible in the process list.
- `fw new --print` prints the command it would run and runs nothing.
- `fw publish` (added in 0.2.0) uploads rendered HTML through the same
  `POST /api/v1/documents` endpoint as R's `publish()`: one self-contained
  file, an HTML file with the `_files` folders it refers to, or a rendered
  folder with an entry point. It asks before uploading; without a terminal it
  needs `--yes`. Erik chose this on 2026-10-31. The ledger commands stay in R and Python.
- `fw project push-spec` (0.2.0) sends `.framework/spec.json` to
  `PUT /api/v2/project/spec` with the project token. Sensitive projects are
  allowed, because the spec holds structure and template text and no data;
  fw says what it sends.
- `fw publish --to github` (framework-cloud-f1's design, 2026-10-31) copies
  the rendered document to `published/<slug>/` at the top of the git
  repository, commits only that path, and pushes. The workflow the cloud adds
  when Pages is turned on deploys `published/`. The URL comes from
  `pages_url` in `GET /api/v1/me` with the project token (cloud 49bd585).
  Older servers need `github.pages_url` from the project's details with the
  user's login. Without either, fw builds it from the remote. It asks before publishing, like the cloud upload, and runs the same
  private-path check. Documents never pass through the cloud.
- Upload policy (Erik, 2026-10-31): an explicit upload is the user's choice,
  whether or not the project is sensitive, and fw never uploads on its own.
  Sensitive only shapes the local setup. When any full path fw would upload
  contains "private" in any case, `fw publish` and `fw project push-spec`
  warn and ask, defaulting to No. With `--yes`, `--json`, or no terminal they
  exit 6 (`reason: private_path`) unless `--include-private` is given. A
  leading `/private/`, which macOS adds to `/tmp`, `/var`, and `/etc`, is
  ignored, as in R and Python. 0.2.0's
  `--allow-private` still works as a hidden alias.

### Scripts and AI agents

Added in 0.2.0 for the Framework Cloud landing page, which presents fw as the
way agents work with the platform.

- `--json` on `login`, `status`, `projects list`, `project show`,
  `blueprints list`, `project status`, `publish`, and `project push-spec`.
  Output is one JSON document on stdout and never includes token values.
  With `--json`, errors are one JSON line on stderr:
  `{"error": ..., "exit_code": N}`.
- Exit codes: 0 success, 1 failure, 2 usage, 3 not logged in or token
  rejected (401, 403), 4 not found (404), 5 browser login denied or expired.
- Without a terminal, `fw login --yes` runs the device flow and prints the
  code and link on stderr (as JSON with `--json`). A person approves it and
  the agent never handles the token.

### Docs site

`docs/site` holds the site for cli.framework.pub in the common format shared
with R and Python (`inst/schema/docs-site.md` and `docs-reference.v1.json` in
the R repository). `reference.json` is generated from the command tree by
`internal/cli/reference.go`. Each release attaches `docs-site.tar.gz`, built
by `tools/docsite`, and framework-cloud imports it.

## Contracts shared with R and Python

- **Server resolution.** User calls use `--url`, then `FW_CLOUD_URL`, then the
  saved default in `cloud.yml`, then `https://framework.pub`. Project calls use
  `--url`, then `FW_CLOUD_URL` in the project's `.env`, then the environment
  variable, then framework.pub. URL normalization copies
  `.fw_normalize_cloud_url()`. A bare host gets `https://`, and plain `http` is
  accepted only for `localhost`, `127.0.0.1`, and `[::1]`. `fw status` names
  the source of the server, as `cloud_status()` does.
- **Tokens.** Lookup order:
  1. `FW_CLOUD_TOKEN`.
  2. `FW_TOKEN_FILE`. When it is set, it is the only place fw looks, and fw
     never falls through to the real keyring.
  3. The OS keyring under service `framework-cloud:<host[:port]>`, account
     `token`, with the legacy `framework.pub` service as a fallback when
     reading. On Windows, fw reads and writes the Credential Manager target
     `:framework-cloud:<host[:port]>:token` directly, because that is the name
     R's keyring package uses and Go libraries default to a different one.
  4. A token file in the config directory: `cloud-token` for framework.pub, and
     `cloud-token-<host>` for other servers. `<host>` is host[:port] lowercased,
     with each character outside `[a-z0-9.-]` replaced by `_`, so
     `localhost:8080` becomes `cloud-token-localhost_8080`. Files have mode
     0600, and the token is the first line. HPC login nodes often have no
     Secret Service, so fw writes here when the keyring fails.
  5. For framework.pub only, the legacy `~/.secrets/framework/token`, read-only.
- **Project `.env`.** `FW_PROJECT_TOKEN` and `FW_CLOUD_URL`, parsed the way
  `.fw_project_env_value()` does.
- **Config directory.** fw and Python share one folder. R keeps its own folder
  and stays CRAN-safe, and its `config_sync()` pulls from the shared folder.
  1. `FW_CONFIG_HOME`.
  2. `$XDG_CONFIG_HOME/framework`, else `~/.config/framework`
     (`%USERPROFILE%\.config\framework` on Windows). fw writes only here.
  3. Read-only fallback for login: R's folder,
     `tools::R_user_dir("framework", "config")`. That is `R_USER_CONFIG_DIR`,
     else `XDG_CONFIG_HOME`, else `%APPDATA%\R\config` on Windows,
     `~/Library/Preferences/org.R-project.R` on macOS, or `~/.config` on Linux,
     each followed by `/R/framework`. fw reads `cloud.yml` and the token files
     there when its own folder does not have them.

  `fw config sync` copies `cloud.yml` and the token files from R's folder into
  fw's folder. It never overwrites a file, and it never writes to R's folder.
  Keyring entries need no sync because all three tools use the same entries.
- **Specs.** fw embeds `layer.v1`, `layers.v1`, and `spec.v1` copied from the
  framework R package, along with R's merge conformance fixtures. It validates
  layers files before handing them to R or Python, and before scaffolding
  Stata. Copying is a manual step: `go run ./tools/syncassets --from
  ../framework --ref <tag or commit>` copies the files and writes
  `assets.lock.json`, which records each file's source path, the R commit, and
  its SHA-256. `go test` fails when a copy no longer matches the lock, or when
  the lock points at an uncommitted R working tree. This follows
  framework-py's `scripts/sync_assets.py`. The copies are pinned to v2 commit
  `c74051f` until R has a release tag.
- **Secrets.** Tokens are never printed, logged, or passed on a command line.
  `fw status` shows only a token's source and its last four characters.

## Runtime detection and routing

`fw new` for R or Python writes the inputs to a temporary JSON file and passes
its path as an argument. Names and paths never pass through shell quoting.

- **R:** with layers, `Rscript -e 'a <- commandArgs(TRUE); framework::project_create_from_layers(a[1], a[2], interactive = TRUE)' LAYERS PATH`.
  Without `--from`, fw calls `framework::new(name, location, type)` instead,
  so the user's R defaults (author, directories, and packages) apply. Rscript
  passes everything after `-e EXPR` to `commandArgs(TRUE)`, and an explicit
  `--args` would arrive as the first argument. New projects run from the home
  folder, so another project's `.Rprofile` or renv in the current folder does
  not take over. If `Rscript` is missing, or the installed framework has no
  `project_create_from_layers()`, fw prints the install command and the
  Rscript command, keeps the layers file, and exits with status 1.
- **Python:** with layers, `python -m framework create --from-layers FILE --path DIR --interactive`.
  Without `--from`, `python -m framework create --name NAME --path DIR [--type T]`,
  which runs `framework.new()` with the user's Python defaults, as R's
  `new()` does. framework-py builds this in its phase 3 (agreed 2026-10-30).
  The distribution is `framework-py` and the import name is `framework`. There
  is no console script. For a new project, fw uses
  `uv run --no-project --with framework-py python -m framework create ...` when uv
  is installed,
  and otherwise a `python3` that can import `framework`. When neither works, fw
  prints the command. Exit code 0 means success, 1 means failure with a message
  on stderr, and 2 means a usage error. The package refuses layers whose
  `language` is not `python`.
- **Stata:** native Go scaffolding (next section).

Inside a project, fw walks up from the working directory to the project root
(the folder with `.framework/` or `settings.yml`) and picks the runtime from
what it finds.

| Found | Runs |
|---|---|
| `renv.lock` or `.Rprofile` | `Rscript` from the project root, so `.Rprofile` activates renv |
| `uv.lock` | `uv run python ...` |
| `.venv/` | `.venv/bin/python` (`.venv\Scripts\python.exe` on Windows) |
| `pyproject.toml` alone | `uv run` when uv is installed, otherwise `python3` |

`fw project status` prints this decision, along with the language, where the
language came from (`.framework/spec.json`, or the files present), and the
cloud link, which it checks with the project token.

`fw project bootstrap` runs the language package's bootstrap in that
environment from the project root: `framework::bootstrap(interactive = FALSE)`
for R and `python -m framework bootstrap` for Python (contracts agreed with
framework-d4 and framework-py-3b on 2026-10-30). R's is built on v2 and
verified with fw on a real project; Python's is not built yet. fw first
checks that the installed package has it, and otherwise says so and exits 0.
The exit status passes through: 0 includes runs that only list to-dos, and 1
means the project could not be prepared. Stata needs no bootstrap, because
`settings.do` creates the folders git does not keep. `--print` shows the
command.

After a project is created from the cloud, fw writes `FW_PROJECT_TOKEN` and
`FW_CLOUD_URL` to its `.env`. It refuses when git tracks `.env`, because the
token would be committed, and it warns when no `.gitignore` line ignores
`.env`. A failed or `--print` run mints no token.

Schema patterns are ECMAScript regular expressions with lookaheads, which Go's
`regexp` lacks, so fw validates with `github.com/dlclark/regexp2` in ECMAScript
mode.

## Stata scaffolding

fw creates the project with Go templates embedded through `embed` and
`text/template`. It reads the same layers input. The spec's `language` field
(`r`, `python`, or `stata`) selects the engine. A layer that omits it means
`r`, and the last layer that sets it wins. `fw new --from` reads `language`
and routes R and Python layers to their packages. For `stata`, fw resolves the
layers itself. Its layer 0 is the Stata defaults, and the merge follows the
spec's rules. The Go merge passes the shared fixtures in the R repo's
`inst/conformance/merge/`, which fw copies from a release tag with the schemas.
Results are compared as parsed JSON, with key order ignored, `{}` equal to
`[]`, and conflicts compared as a set. Stata uses only part of the resolved
spec: `name`, `author`, `directories`, `extra_directories`, `files`, the
gitignore blocks, `ai`, and `security`.

```
my-study/
  master.do              runs settings.do, opens a log, then each do-file you
                         list in it
  settings.do            one global per directory key, and mkdir for folders git
                         does not keep (private data, logs, and outputs)
  do/                    empty; starter do-files come only from a blueprint
  inputs/ outputs/ docs/ logs/ scratch/   from the spec, with .gitkeep files
  .gitignore             *.dta, *.log, *.smcl, private folders, .env, and logs
  AGENTS.md, CLAUDE.md, and skills (framework-stata, plus
  framework-sensitive-data for sensitive projects) for the selected assistants
  .claude/settings.json  deny rules and a fail-closed sandbox for private
                         folders and .env, when security.sensitive is true
  .framework/spec.json   the resolved spec
```

Decisions made while building it (2026-10-30):

- No `profile.do`. Stata runs the first `profile.do` it finds, so a project
  copy would shadow the user's own.
- `settings.do` sets `$root` from the current folder every time and checks
  that `settings.do` is there. Globals survive `clear all`, so a root left
  over from another project in the same session would otherwise be reused.
- The Stata defaults (layer 0) use the R keys where they mean the same thing
  (`inputs_raw`, `outputs_tables`, `logs`, and the private and public pairs
  for sensitive projects), plus `do` for do-files.
- Types: `project`, `project_sensitive`, and `bare`. Course and presentation
  are refused. `bare` gets only `settings.do`, `.gitignore`, and the spec.
- Directory keys must be valid Stata global names (at most 32 characters, and
  not `root`), and paths cannot contain quotes, backticks, or `$`.
- Skills with `package: true` come from fw's own copies. R-only package skills
  are skipped with a note.
- Before the first commit, fw checks with `git check-ignore` that `.env` is
  ignored, and appends it to `.gitignore` when a layer un-ignored it.
- A Stata spec has no `render_dirs`, `packages`, `scaffold`, or
  `connections`. spec.v1 requires those only for R and Python. fw records its
  version in `source.fw_version`.
- The cloud's top layer for a Stata project sets R-only directories from the
  layers beneath it to null, and the merge deletes them.
- Stata is not installed on the development Mac, so the do-files are checked
  by reading only. They use base Stata and `version 16`.

## Libraries and tooling

- Current stable Go, `cobra`, and the standard library for HTTP, JSON, and
  templates.
- Keyring access needs no cgo.
  - macOS: go-keyring reads and deletes entries. fw writes the plain token
    itself through `security -i`, because go-keyring's `Set` stores
    `go-keyring-base64:<...>`, which R would read as the token. The plain value
    was checked with `security find-generic-password -w` on 2026-10-30.
  - Linux: go-keyring over Secret Service. libsecret may match on the schema
    name, so the `keyring-interop` CI job runs gnome-keyring and checks that
    fw, R's keyring package, and Python's keyring read each other's entries
    (`go test -tags interop ./internal/keyring`). It first runs on the first
    push.
  - Windows: `github.com/danieljoos/wincred` with R's target name. fw writes
    UTF-8 as R does. On read, it accepts UTF-16LE too, which is what Python's
    WinVaultKeyring writes.
- `gopkg.in/yaml.v3` for `cloud.yml` and `github.com/santhosh-tekuri/jsonschema`
  for draft-07 validation.
- `golangci-lint`, `go test` with an `httptest` fake server, and GitHub Actions
  on macOS, Linux, and Windows.

## Distribution

- GoReleaser (`.goreleaser.yaml`) builds with `CGO_ENABLED=0` for darwin,
  linux, and windows on amd64 and arm64, and uploads archives and checksums to
  GitHub Releases. `.github/workflows/release.yml` runs it when a `v*` tag is
  pushed. Archive names are fixed (`fw_<version>_<os>_<arch>.tar.gz`, `.zip`
  on Windows, and `fw_<version>_checksums.txt`) because framework-cloud's
  scaffold workflow downloads them.
- A Homebrew tap, `frameworktools/homebrew-tap`, gets a cask from GoReleaser
  (GoReleaser deprecated formulas for prebuilt binaries). The binary is not
  notarized, so the cask clears macOS quarantine for `fw` after install.
  Notarizing would remove that step. The tap repository and a
  `HOMEBREW_TAP_TOKEN` secret that can push to it must exist before the first
  release.
- The license is MIT, as in the R, Python, and cloud repositories.
- On HPC and Windows, users download the archive from Releases into a folder on
  their PATH. Scoop and code signing can come later.
- The cloud's GitHub scaffold workflow downloads a pinned fw release for Stata
  projects and runs `fw new --from .framework/layers.json --dir . --headless`.
  Headless mode follows the R rules: no prompts, no git commands, and refusal
  to write under `.github/workflows/`.

## Blueprints, structures, and settings (built for 0.3.0)

Framework Cloud's plan of 2026-10-31 (framework-cloud c5c9f4a) replaces
project types and facets with blueprints, project structures, package sets,
skills and skill sets, tools, and settings. fw's side:

- The cloud keeps emitting only the existing layer kinds. A structure is a
  `base` layer; tools, package sets, and skill sets are `facet` layers with
  keys such as `framework/tool-quarto`; settings are in the `blueprint`
  layer. The cloud applies the sensitive private/public split itself, so
  clients see final directories. fw 0.2.0 keeps working.
- R adds optional `core_files` (base layer, default true), `tools` (id to
  boolean), and a looser `type` (`^[a-z][a-z0-9_]*$`) at spec_version 1. fw
  syncs them. Stata scaffolding then keys bare on `core_files` and sensitive
  on `security.sensitive`, accepts any type label when the layers carry
  directories, and treats a true tool in a Stata spec as an error. Without
  layers, `--type project|project_sensitive|bare` maps to fw's built-in
  Stata structures.
- Listing commands with `--json` and `--lang`: `fw structures list`, `fw package-sets
  list`, `fw skills list` (`--sets`), and `fw tools list`. `fw blueprints
  list` drops facets.
- `fw new` gains `--blueprint`, `--structure`, `--package-set`, `--skill`,
  `--skill-set`, `--tool`/`--no-tool`, `--sensitive`, `--ai`, and `--git`.
  With any of them, fw posts the choices to `POST /api/v2/resolve`, which
  fills the rest from the user's defaults and returns a layers envelope.
  Without them, or logged out, fw keeps the offline path.
- Creating the cloud project too (Erik, 2026-10-31): at a terminal fw asks
  "Also create this project on <server>? [y/N]". With `--yes`, `--json`, or
  no terminal it creates it only with `--cloud`; `--no-cloud` skips the
  question. Logged out, fw does not ask. Creation uses the same endpoint as
  the website.
- `fw new --from SLUG` and `fw project push-spec` are unchanged.

Endpoints (framework-cloud 0381706, live on framework.pub; user token only,
a project token gets 403):

- Lists, each with `?language=r|python|stata`: `GET /api/v2/structures`
  `{structures: [{key, name, description, type, version, system,
  visibility}]}`; `/tools` `{tools: [{key, name, description, language[],
  requires[], ...}]}`; `/package-sets` `{package_sets: [{..., language,
  packages[]}]}`; `/skill-sets` `{skill_sets: [{..., language, skills[],
  packages[]}]}`; `/skills` `{skills: [...]}`. `GET /api/v2/blueprints` adds
  `structure`, `tools`, `package_sets`, and `skill_sets`, and keeps `facets`
  for fw 0.2 and 0.3.
- Keys are handles: `framework/<key>` or `<user-handle>/<key>`. Bare tool ids
  mean `framework/<id>`.
- `POST /api/v2/resolve` `{name?, language?, blueprint?, structure?, tools
  {id: bool}, package_sets[], skill_sets[], skills[], settings {sensitive,
  git, ai (bool or {enabled, assistants})}}` returns the layers envelope of
  `GET /api/v2/projects/{slug}/layers`. It needs a blueprint or a structure.
  422 `{errors: {field: [...]}}` for unknown handles, wrong-language or
  missing required tools, and any tool on Stata. AI off drops all skills;
  sensitive with AI on adds framework-sensitive-data.
- `POST /api/v2/projects`: the same body plus `name` (required) and
  `description`. 201 `{slug, name, language, url, token}`; the token is a
  project token for linking. 422 `errors.name` when the name is taken.
- User defaults (framework-cloud 2196b0d): `GET /api/v2/defaults` returns
  `{blueprint, package_sets[], skill_sets[], tools {id: bool}, settings
  {sensitive, git, ai}}`. Resolve and create apply them: with no blueprint
  or structure, the default blueprint and the preferred sets and tools are
  used, and default settings fill any the blueprint leaves unset. An empty
  body resolves to the user's defaults. `source.blueprint` appears whenever
  the result equals the blueprint. A `fw defaults show` command can read
  them.

### Offline creation from saved defaults (framework-cloud plan 2026-11-01)

The shared contract is in framework-cloud's
`docs/plans/2026-11-01-workflows-defaults-and-recipes.md`.

- Logged in, `fw new NAME --lang stata` posts `{"language": "stata"}` to
  `POST /api/v2/resolve` and saves `{fetched_at, defaults_revision,
  envelope}` to `cache/defaults/<host_port>/<language>.json` in fw's config
  folder. Every `:` in `host[:port]` becomes `_` on every platform, because
  Windows folder names cannot contain `:`. `defaults_revision` comes from
  the `Framework-Defaults-Revision` header and is compared only for
  equality.
- The server names a nameless envelope "Untitled project". Clients replace
  the name with the project's before validating, so a cached envelope is
  valid as stored.
- Each `Framework-Notice` response header is one plain-text notice, printed
  as is. The body stays exactly a layers envelope.
- Offline means a network error, a timeout, or a 5xx. Offline, fw uses the
  newest cache from its own folder or R's and prints the date it was saved.
  With no cache it asks at a terminal, and without one it stops (exit 1)
  unless `--offline` is given. `--offline` skips the server and uses the
  cache, or fw's built-ins. 401 and 403 are exit 3 and never fall back.
  Logged out, or with `--type`, fw uses its built-ins without asking.
- R and Python run the same policy inside their own `new()`. fw will pass
  the choices and `--offline` through to them once released versions accept
  those arguments. framework-py 0.1.0.dev4 does: fw passes `--offline` and
  `--workflow` to `python -m framework create`, runs `python -m framework
  adopt` for `fw adopt`, and asks uv for `framework-py>=0.1.0.dev4`. R's arrive in
  framework 2.0.0 (`workflow`, `offline`, and the choices), so fw checks
  `packageVersion("framework") >= "2.0.0"` first. framework-py will name its
  minimum version when it ships them.

### Adopt and structure import

- The scanner and mapping are shared: `inst/adopt/rules.json` and the
  `inst/conformance/adopt/` cases come from the R package, and
  `internal/adopt` follows the algorithm in the conformance README.
- `fw adopt` detects the language from top-level files (.do, pyproject.toml,
  renv.lock, DESCRIPTION, or .Rproj); `--lang` overrides it. For Stata it
  writes `settings.do` and `.framework/spec.json` (a bare spec with exactly
  the mapped folders, no AI, no git) and refuses when the folder is already a
  project. It stays local. R and Python will be handed to `adopt()` once
  framework 2.0.0 and framework-py release it.
- `fw structures import` (alias `fw structure import`) posts `{name,
  description?, directories, extra_directories?}` to `POST
  /api/v2/structures` and gets 201 `{key, name, url}`; the structure is
  private.
- Before writing FW_PROJECT_TOKEN to .env, fw makes sure git ignores .env,
  adding "# Secrets (added by Framework)" and ".env" to .gitignore when
  needed, as R and Python do.

### Workflows and create/ fixtures

- Stata supports only the Scripts-only workflow. The Stata defaults record
  `scaffold.workflow: scripts` and `notebook_format: script`, and any other
  workflow or format in the layers is an error.
- As in R, a project without core files (`core_files: false`) gets no AI
  files.
- R generates `inst/conformance/create/r/`, Python `create/python/`, and fw
  `create/stata/` with `go run ./tools/createfixtures`. Each owner sends its
  files to the R repository, and everyone pins one copy. A case is
  `{description, language, layers, spec, expected: {paths}}`. Creating
  `spec` in an empty folder must write exactly `expected.paths`: folders end
  in `/`, `.git/` is left out, and the list is sorted by byte value. fw pins
  `create/stata/` and `resolve/`, and its tests check the pinned Stata cases.

## Endpoints needed from framework-cloud

Existing endpoints that fw uses: `GET /api/v1/me`, `GET /api/v1/projects`, and
`GET /api/v1/project` with a project token.

Agreed with framework-cloud-f1 on 2026-10-30 and built. Added on 2026-10-31:
`GET /api/v2/projects/{slug}`, `GET /api/v2/blueprints`, and
`PUT /api/v2/project/spec`. Publishing uses the existing
`POST /api/v1/documents`.

1. `language` stored for projects and blueprints and emitted in the project
   layer. Every project layer sets it, `r` included.
2. `GET /api/v2/projects/pull` with a project token that has the
   `project:pull` ability. It returns the `layers.v1` envelope. `source` is
   left out for sensitive projects.
3. `GET /api/v2/projects/{slug}/layers` with a user token returns the same
   envelope. `POST /api/v2/projects/{slug}/pull-tokens` with
   `{"name": "fw on <host>"}` returns `{token, url}`, which fw writes to `.env`
   as `FW_PROJECT_TOKEN` and `FW_CLOUD_URL`. The read and the token are two
   calls so that a read never creates a credential. fw requests the token only
   after the project is created.
4. Later: `POST /api/v1/cli/device` and `POST /api/v1/cli/device/token` for
   browser sign-in. The token call returns 428 until the user approves.

Errors are Laravel JSON, `{"message": ...}`, with 401, 403, 404, or 422. The
GitHub scaffold workflow pins a tagged fw release for Stata projects. It needs
the release URL pattern and checksum file name once the first release exists.

## Decisions

Erik approved this plan on 2026-10-30. The Stata templates live in this
repository. Each project has one `language`. R keeps its own config folder and
syncs from fw's folder. The endpoint requests went to framework-cloud-f1 on the
same day.

## First milestones

1. Skeleton: cobra root, `version`, `doctor`, config directory, server
   resolution, and CI.
2. `login`, `logout`, `status`, and `projects list`, tested against a fake
   server and then a local framework-cloud.
3. `fw new` for R, then Python once its entry point exists.
4. Stata scaffolding with the shared conformance fixtures.
5. GoReleaser, the Homebrew tap, and the first tagged release.
