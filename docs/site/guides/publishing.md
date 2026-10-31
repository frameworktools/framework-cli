---
id: publishing
title: Publishing documents
description: Publish rendered Quarto notebooks and other HTML to Framework Cloud
section: guides
position: 2
---

`fw publish` uploads a rendered document to the current project on Framework
Cloud. It uses the same service as R's `publish()`, so documents published
either way appear together on the project page.

## Before you publish

Publishing uses the project token in `.env`. Projects created with
`fw new --from` already have one. For other projects, link them first:

```sh
fw project link --from vaccine-study
```

## What fw sends

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

## Private folders

fw refuses to publish anything inside a folder named `private` in the
project, as R does. Private folders hold restricted data. If you are sure a
document in one is safe to share, pass `--allow-private`.

## Links and versions

Every document gets an unguessable private link. `--readable` also gives it
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

## Sending the project spec

`fw project push-spec` sends `.framework/spec.json` to the project page. The
spec describes the project's structure: folders, settings, packages, and
templates. It holds no data, and the server refuses secret values in it.
