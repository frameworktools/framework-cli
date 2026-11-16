---
id: install
title: Installation
description: Install fw with Homebrew or from a release archive
section: getting_started
position: 1
---

`fw` is a standalone executable with zero runtime dependencies. It handles Framework Cloud authentication, project creation and adoption, Stata scaffolding, and artifact publishing. The R and Python packages work independently when `fw` is absent.

## Homebrew on macOS and Linux

```sh
brew install frameworktools/tap/fw
```

Upgrade to the latest release with `brew upgrade fw`.

## Prebuilt archives for Linux, HPC clusters, and Windows

Download the archive for your platform from [Releases](https://github.com/frameworktools/framework-cli/releases) and place `fw` (or `fw.exe` on Windows) in a directory on `PATH`. On shared Linux hosts and HPC clusters without root access, install into `~/.local/bin` or `~/bin`:

```sh
tar -xzf fw_0.6.3_linux_amd64.tar.gz fw
mkdir -p ~/.local/bin && mv fw ~/.local/bin/
```

Each release includes SHA-256 checksums in `fw_<version>_checksums.txt`.

## Installation check

```sh
fw version
fw doctor
```

`fw doctor` reports detected `Rscript`, `uv`, `python3`, and `git` binaries, configuration directory paths, and the active Framework Cloud server.

> [!NOTE]
> `fw` invokes R and Python only when creating, adopting, bootstrapping, upgrading, or syncing R and Python projects. Stata projects require neither runtime.
