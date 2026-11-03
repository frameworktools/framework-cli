---
id: publishing
title: Publishing artifacts
description: Publish figures, tables, files, and rendered reports to Framework Cloud
section: guides
position: 2
---

`fw publish` uploads a file or rendered document to the current project on Framework
Cloud. It uses the same service as R's `publish()`, so documents published
either way appear together on the project page.

## Before you publish

Publishing uses the project token in `.env`. Projects created with
`fw new --from` already have one. For other projects, link them first:

```sh
fw project link --from vaccine-study
```

## What fw sends

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

`--visibility` accepts `public` or `unlisted`. Passwords require at least eight
characters. Artifact details apply to Framework Cloud publishing. `--to cloud`
is the default; `--to github` publishes rendered HTML to GitHub Pages.

## Private folders

Folders named `private` usually hold restricted data. When the full path of
a file fw would upload contains "private", in any letter case, fw warns and
asks again, defaulting to No. With `--yes`, `--json`, or no terminal, it
stops with exit code 6 instead. If you are sure the files are safe to share,
pass `--include-private`. A leading `/private/` is not counted, because
macOS adds it to `/tmp`, `/var`, and `/etc`. The R and Python packages
check paths the same way.

Publishing is always your choice. A sensitive project can publish like any
other; the sensitive setting only changes how the project is set up on your
machine.

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

## Publishing to GitHub Pages

A project with a GitHub repository can publish to GitHub Pages for free.
Turn Pages on once, with "Publish with GitHub Pages" under Projects on
Framework Cloud. That adds a workflow to the repository which deploys its
`published/` folder whenever a change to it reaches the default branch.

```sh
fw publish outputs/notebooks/results.html --to github --slug results
```

fw copies the rendered file to `published/results/index.html`, with the
`_files` folders it refers to, and replaces anything already in
`published/results/`. A folder is copied as it is and must have an
`index.html`. fw then commits only `published/results/` and pushes, so
anything else you have staged stays out of that commit. When nothing
changed, fw says so and makes no commit. The document never passes through
Framework Cloud.

> [!WARNING]
> GitHub Pages sites are public, even when the repository is private.

fw prints the document's address. When the project is linked to Framework
Cloud, fw gets the site's address from there with the project token and
warns if Pages is still off. Otherwise it works the address out from the
GitHub remote, such as
`https://acme.github.io/study/results/`. Pages deploys only from the default
branch, so on another branch fw warns and pushes anyway. The document goes
live once that branch is merged.

## Sending the project spec

`fw project push-spec` sends `.framework/spec.json` to the project page. The
spec describes the project's structure: folders, settings, packages, and
templates. It holds no data, and the server refuses secret values in it.


## Adding an artifact to a collection

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
