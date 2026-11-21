---
id: publishing
title: Publishing artifacts
description: Publish figures, tables, files, and rendered reports to Cloud, GitHub Pages, folders, or S3
section: guides
position: 2
---

`fw publish` packages and delivers files, rendered HTML reports, and multi-file sites to Framework Cloud, GitHub Pages, local or network folders, and S3-compatible object storage.

## Project token

Publishing to Framework Cloud authenticates with `FW_PROJECT_TOKEN` in `.env`. Projects created with `fw new --from`, `--zip`, or `--github` already include a token. Link an unlinked local project first:

```sh
fw project link --from vaccine-study
```

## Supported inputs

- **Single file**: images, PDFs, CSV, JSON, text, and binary files keep their original filename.
- **Self-contained HTML**: Quarto documents rendered with `embed-resources: true` upload as a single HTML file.
- **HTML with companion assets**: when Quarto writes `<name>_files/` next to `<name>.html`, `fw` bundles the HTML file and every referenced `*_files/` directory together.
- **Rendered directory**: Quarto books and websites upload as a complete directory bundle opening at `--entrypoint` (`index.html` by default).

```sh
fw publish outputs/notebooks/analysis.html
fw publish _book --entrypoint index.html
```

Framework Cloud enforces a 50 MB limit per artifact upload. Symbolic links inside bundles are rejected so uploads cannot include files outside the target directory.

## Metadata and access control

```sh
fw publish outputs/effect.png --slug treatment-effect --title "Treatment effect"
fw publish outputs/estimates.csv --slug estimates --description "Adjusted estimates"
fw publish outputs/model.html --slug adjusted-model --kind model --password reviewer-password
```

On Framework Cloud, `--visibility` accepts `public`, `unlisted`, or `private`. Optional artifact passwords must be at least eight characters. Static destinations (`folder`, `github`, and `s3`) record titles, descriptions, and artifact kinds, and enforce destination-level visibility rules described below.

## Private path guard

Directories named `private` typically hold restricted data. Whenever any file scheduled for upload has `private` in its resolved path (case-insensitive, ignoring macOS's leading `/private/` system prefix on `/tmp`, `/var`, and `/etc`), `fw` warns and asks for confirmation, defaulting to `No`. With `--yes`, `--json`, or no terminal, `fw` aborts with exit code `6` unless `--include-private` is passed. R and Python enforce the same check.

Projects with `security.sensitive: true` can still publish shareable outputs; the path guard blocks accidental uploads from private directories.

## URLs, versions, and storage

After a Framework Cloud publish, `fw` prints two links:

```text
Published analysis, version 3.
Share:  https://framework.pub/go/az7h
Manage: https://framework.pub/artifacts/123
```

`Share` is the link people open, and the one to paste into a blog or site that takes Framework embeds: a public artifact's short link, or an unlisted artifact's own link. A private or password-protected artifact has none. `Manage` is the artifact's page on Framework Cloud, where you change who can open it, what embeds show, and its title and description. `--json` includes them as `embed_link` and `artifact_url`.

Every Cloud artifact receives an unguessable unlisted URL (`private_url`). Pass `--readable` to assign a readable URL under your account handle, or `--readable=false` to remove it; omitting `--readable` preserves the artifact's current setting.

Publishing again to the same `--slug` creates a new immutable version while preserving links to earlier versions.

```sh
fw publish outputs/notebooks/results.html --slug results --readable
```

> [!NOTE]
> Hosted accounts on `framework.pub` include free publishing with 1 GB of artifact storage per account. Self-hosted servers use your own configured storage backend.

## Code snippets

`fw snippet publish` publishes one cell of an executed Jupyter notebook as a `code_snippet` artifact. The snippet holds the cell's code and the text outputs saved with it. Blog posts and reports embed it beside the results it produced. `fw` does not run the notebook, so run it and save it first.

```sh
fw snippet publish notebooks/analysis.ipynb --cell fit-ols
fw snippet publish notebooks/analysis.ipynb --cell fit-ols --folded --link notebook=https://example.org/analysis
```

`--cell` names the cell by a tag, a Quarto `#| label:` line, or the label on its `%%fw_snippet` line. Quarto option lines and the `%%fw_snippet` line are left out of the code. Outputs keep their order. Standard output becomes output, standard error becomes a message, results and displays send their `text/plain` form, and errors send their traceback. An output that carries an image is left out whole.

- `--folded` and `--unfolded` set the fold state of output blocks. Without them, Framework Cloud folds outputs longer than 12 lines.
- `--no-output` publishes the code only.
- `--link notebook=URL` links the full notebook. `--link model=SLUG` links a model artifact in the project, or an `https` URL.
- `--repository URL` names the repository. Without it, `fw` uses `publish.repository` in `settings.yml` and otherwise sends none.

The snippet records the notebook's path in its git repository and the commit checked out. The slug defaults to the label. Publishing an unchanged snippet keeps its newest version, and `fw` prints `Unchanged, still version N`.

`fw publish code_snippet.json --kind code_snippet` publishes a snippet document written by another client.

## Embed display

Framework Cloud keeps, for each artifact, what sites that embed it show. Set it on the artifact's **Settings** tab, or with `--display NAME=on|off` when you publish. The flag repeats, names only the settings it changes, and works with `fw publish` and `fw snippet publish`:

```sh
fw publish model.dagitty --display analysis=off --display legend=off
```

Settings: `title`, `description`, `author`, `credit` (the link back), `details` (a model's or test's details), `variable_tips`, `column_summaries`, `output` and `links` (code snippets), `legend`, `analysis`, `dagitty`, and `code` (DAGs), and `interactive` (charts with a static image). Settings are on until turned off, except that a DAG embeds as its drawing alone: turn on `description`, `analysis`, `dagitty`, or `code` to show them. The artifact's own page shows everything.

## DAGs

`fw publish model.dagitty` publishes a causal diagram written in DAGitty's model syntax. Framework Cloud reads the graph, lays it out, and draws it on its page and in embeds, with the DAGitty code beside it. `fw publish dag.json --kind dag` publishes a DAG document written by another client, such as one from the R, Python, or Stata package with labels, the code that built the graph, and adjustment sets.

## GitHub Pages

Projects backed by a GitHub repository can publish static artifacts directly to GitHub Pages. Enable Pages on the project's **GitHub & Pages** tab on Framework Cloud to add the deployment workflow that publishes `published/` whenever changes land on the default branch.

```sh
fw publish outputs/notebooks/results.html --to github --slug results
```

`fw` writes the artifact bundle to `published/<slug>/`, archives earlier versions under `published/<slug>/v/<N>/`, updates `published/index.html` and `published/index.json`, commits only those `published/` paths (leaving other staged work untouched), and pushes. Force-add rules ensure Quarto `*_files/` assets are included even when `*_files/` appears in `.gitignore`. If the rendered output has not changed, `fw` skips creating a duplicate version or commit. Artifact files go straight to GitHub without passing through Framework Cloud.

> [!WARNING]
> GitHub Pages sites are publicly accessible on the internet even when the underlying GitHub repository is private.

`fw` prints the published URL (resolved from Framework Cloud when linked, or derived from the `origin` remote URL such as `https://acme.github.io/study/results/`). Because GitHub Pages deploys from the default branch, publishing on a feature branch pushes the commit and prints a reminder that the page goes live after merge. Total `published/` size warns at 800 MB and stops before committing above 1 GB.

## Project specification

`fw project push-spec` uploads `.framework/spec.json` to Framework Cloud for display on the project **Overview** page. The spec records directory roles, settings, package lists, and templates; it contains no data files, and both `fw` and the server reject literal secrets.

## Collections

Attach an artifact to an existing project collection by passing `--collection` with the collection slug or ID. Collections always point to the artifact's latest version.

```sh
fw publish outputs/effect.png --collection main-findings --share-unlisted --yes
```

Public collections require `--visibility public` (or `--readable`). Attaching an unlisted artifact to an unlisted collection requires `--share-unlisted` to confirm that collection readers may open the artifact. If uploading succeeds but collection attachment fails, `fw` prints the published URL and warns so you can attach the artifact from the project **Artifacts** page without re-uploading a duplicate version.

## Named destinations

Pass `--to NAME` to publish to a destination configured in `settings.yml`. Without `--to`, `fw` uses `publish.default` when set, falling back to Framework Cloud (`cloud`). Built-in `--to cloud` and `--to github` targets are always available. `fw` reads `default.publish` (used by R, Python, and Stata `settings.yml` files) as well as root-level `publish`.

```yaml
default:
  publish:
    default: lab
    destinations:
      lab:
        driver: folder
        path: /Volumes/lab/published
        visibility: private
      pages:
        driver: github
        visibility: public
      bucket:
        driver: s3
        bucket: research-results
        prefix: published
        visibility: public
        url: https://results.example.org
```

```sh
fw publish outputs/figure.svg --to lab --yes
fw publish outputs/model.json --to pages --yes
```

Folder, GitHub, and S3 destinations write a self-contained offline reader, a `framework.bundle/v1` manifest with SHA-256 checksums, immutable `<slug>/v/<N>/` version archives, and a root index (`index.html` and `index.json`). Structured `framework.artifact/v1` JSON files render interactively in the offline reader, while arbitrary JSON files remain downloadable files. Slugs normalize underscores and periods to lowercase hyphens.

Folder destinations require an explicit `visibility` field (`public` or `private`); Framework does not modify filesystem or network-share permissions. Set an optional `url` when the folder is served over HTTP; otherwise receipts record the local path without sending local filesystem paths to Framework Cloud.

Destination credentials in `settings.yml` must use `env("VARIABLE")` or `env("VARIABLE", "")` references. Literal credentials, non-empty defaults such as `env("VARIABLE", "secret")`, and URLs with embedded passwords are rejected. S3 `access_key_id`, `secret_access_key`, and `session_token` values pass through to the AWS CLI environment. Legacy `private: true` maps to `visibility: private` when `visibility` is omitted.

S3 destinations require the `aws` CLI (`endpoint` selects an S3-compatible provider). Before uploading, `fw` checks `get-bucket-policy-status` to verify that bucket public/private status matches the declared `visibility` (and for `visibility: private`, verifies all four S3 Public Access Block flags). Bucket policies govern reader access; `fw` never sets object ACLs.

Static destinations reject passwords, expiration dates, and `unlisted` visibility. After a non-Cloud publish completes in a linked project, `fw` records artifact metadata on Framework Cloud; if that tracking request fails, `fw` prints a warning and keeps the published files intact.
