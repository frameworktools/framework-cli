---
id: publishing
title: Publishing artifacts
description: Publish figures, tables, files, and rendered reports to configured destinations
section: guides
position: 2
---

`fw publish` delivers files and rendered documents to Framework Cloud, GitHub Pages,
a folder, or S3. Named destinations live in the project settings. Framework Cloud
uses the same upload service as R and Python.

## Project token

Publishing uses the project token in `.env`. Projects created with
`fw new --from` already have one. For other projects, link them first:

```sh
fw project link --from vaccine-study
```

## Published files

- **A file.** Images, PDFs, CSV, JSON, text, and other files retain their original filename.
- **One HTML file.** Framework's Quarto settings use `embed-resources`, so a
  rendered notebook is usually a single self-contained file.
- **An HTML file with its resources.** Without `embed-resources`, Quarto
  writes `analysis_files/` next to `analysis.html`. fw sends the HTML and
  every `_files` folder it refers to, as one bundle.
- **A folder.** A rendered website or book is sent whole and opens at
  `--entrypoint`, which defaults to `index.html`.

```sh
fw publish outputs/notebooks/analysis.html
fw publish _book --entrypoint index.html
```

The limit is 50 MB per document. fw refuses symbolic links in a bundle, so a
bundle cannot pull in files from elsewhere.

## Artifact details

```sh
fw publish outputs/effect.png --slug treatment-effect --title "Treatment effect"
fw publish outputs/estimates.csv --slug estimates --description "Adjusted estimates"
fw publish outputs/model.html --slug adjusted-model --kind model --password reviewer-password
```

Cloud `--visibility` accepts `public`, `unlisted`, or `private`. Passwords require
at least eight characters. Static destinations support titles, descriptions, and
types. Their access restrictions are described under Named destinations.

## Private folders

Folders named `private` usually hold restricted data. When the full path of
a file fw would upload contains "private", in any letter case, fw warns and
asks again, defaulting to No. With `--yes`, `--json`, or no terminal, it
stops with exit code 6 instead. Pass `--include-private` to upload them.
A leading `/private/` is not counted, because macOS adds it to `/tmp`,
`/var`, and `/etc`. The R and Python packages
check paths the same way.

Sensitive projects publish like any other project. The sensitive setting
changes only how the project is set up on your machine.

## Links and versions

Every artifact gets an unguessable unlisted link. Anyone with the link can open
it unless you set a password. `--readable` also gives it
a readable address under your handle, and `--readable=false` removes it.
Leaving the flag out keeps the document's current setting.

Publishing to the same `--slug` again adds a version. Older versions keep
their own links.

```sh
fw publish outputs/notebooks/results.html --slug results --readable
```

> [!NOTE]
> On framework.pub, publishing needs paid hosting. Self-hosted servers allow
> it for every account.

## GitHub Pages

A project with a GitHub repository can publish to GitHub Pages for free.
Turn Pages on once, with "Publish with GitHub Pages" under Projects on
Framework Cloud. That adds a workflow to the repository which deploys its
`published/` folder whenever a change to it reaches the default branch.

```sh
fw publish outputs/notebooks/results.html --to github --slug results
```

fw copies the rendered file to `published/results/index.html`, with the
`_files` folders it refers to. It keeps each earlier version in
`published/results/v/N/` and replaces the latest copy. A folder is copied as
it is. fw then commits only `published/results/` and the site index in
`published/`, and pushes, so anything else you have staged stays out of that
commit. R and Python projects ignore `*_files/` in `.gitignore`; fw adds the
files it wrote anyway, and stops before committing if git leaves any out.
When nothing changed, fw says so and makes no commit. The document never
passes through Framework Cloud.

> [!WARNING]
> GitHub Pages sites are public, even when the repository is private.

fw prints the document's address. When the project is linked to Framework
Cloud, fw gets the site's address from there with the project token and
warns if Pages is still off. Otherwise it works the address out from the
GitHub remote, such as
`https://acme.github.io/study/results/`. Pages deploys only from the default
branch, so on another branch fw warns and pushes anyway. The document goes
live once that branch is merged.

## Project spec

`fw project push-spec` sends `.framework/spec.json` to the project page. The
spec describes the project's structure: folders, settings, packages, and
templates. It holds no data, and the server refuses secret values in it.


## Collections

Select an existing collection in the current project by its ID or slug. The
client resolves the collection before uploading and does not create collections.
The collection follows the artifact's latest version.

```sh
fw publish outputs/effect.png --collection main-findings --share-unlisted --yes
```

Public collections require public artifacts. Sharing an unlisted artifact with
readers of an unlisted collection requires explicit consent through
`--share-unlisted`.
If attachment fails after the artifact uploads, publishing returns its URL and
warns that collection attachment failed. Attach the artifact from the project
Outputs page; publishing again creates another version.

## Named destinations

`--to NAME` selects a destination from `settings.yml`. Without `--to`, fw uses
`publish.default`, then Framework Cloud. Explicit `--to cloud` and `--to github`
remain available. Project files generated by R and Python place these settings
under `default.publish`; fw reads that profile and also accepts root-level
`publish`. Stata projects retain the same settings in the resolved spec.

```yaml
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

Folder and GitHub destinations include an offline reader, a `framework.bundle/v1`
manifest with SHA-256 checksums, and a site index. Typed `framework.artifact/v1`
JSON retains its interactive representation. Other JSON remains a downloadable file.
Slugs use lowercase letters, digits, and hyphens. Underscores and periods become
hyphens. Static folders open at `index.html`, or the file selected with `--entrypoint`.

Every changed publish preserves an immutable `<slug>/v/<number>/` archive.
Repeated identical HTML retains its existing version. GitHub commits include the
artifact, archives, and the shared `published/index.html` and `index.json` files. The complete GitHub Pages
site, including retained versions, triggers a warning at 800 MB. Publishing stops
above 1 GB before any commit or push. Local bundle files remain available.

Folder visibility must be declared explicitly. Framework does not configure or
verify the mounted share's permissions. An optional `url` provides reader links;
without it, the receipt contains a local location. Cloud tracking never includes
that local path.

Destination credentials require `env("VARIABLE")` or `env("VARIABLE", "")`
references, with single or double quotes, the rule Framework Cloud applies
to specs. Literal secrets, a non-empty default such as
`env("VARIABLE", "value")`, and embedded URL passwords are rejected. A
variable that isn't set takes its empty default, or stops fw when there is
none. S3 `access_key_id`, `secret_access_key`,
and `session_token` references are passed to the AWS CLI environment. The legacy
`private: true` setting means `visibility: private`; an explicit visibility takes
precedence.

S3 delivery requires the AWS CLI with configured credentials. `endpoint` selects
an S3-compatible service. Framework queries `get-bucket-policy-status` before any
upload and rejects visibility mismatches. Services without that API are currently
unsupported. Bucket policies control access; Framework does not change ACLs.
Private delivery also requires all four S3 public access blocks. Concurrent
publishing to one S3 prefix requires external coordination.

Static destinations reject passwords, expiry, and unlisted visibility. Private
GitHub Pages publishing remains disabled until site access can be confirmed;
a private repository or GitHub Pro account does not establish private Pages access.

After a successful non-cloud delivery, a linked project records metadata in
Framework Cloud. Tracking failures produce a warning. They do not undo delivery
or require publishing the artifact again.
