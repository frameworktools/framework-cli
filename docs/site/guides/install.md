---
id: install
title: Install fw
description: Install fw with Homebrew or from a release archive
section: getting_started
position: 1
---

fw is one program with no dependencies. The R and Python packages work fully
without it. fw is a helper for logging in, creating and linking projects,
and publishing.

## macOS and Linux with Homebrew

```sh
brew install frameworktools/tap/fw
```

Upgrade with `brew upgrade fw`.

## Linux, HPC clusters, and Windows

Download the archive for your platform from
[Releases](https://github.com/frameworktools/framework-cli/releases) and put
`fw` (or `fw.exe`) in a folder on your PATH. On a cluster without root
access, `~/bin` or `~/.local/bin` works:

```sh
tar -xzf fw_0.2.0_linux_amd64.tar.gz fw
mkdir -p ~/.local/bin && mv fw ~/.local/bin/
```

Each release lists SHA-256 checksums in `fw_<version>_checksums.txt`.

## Check the installation

```sh
fw version
fw doctor
```

`fw doctor` shows the R, Python, and uv installations fw found, its
configuration folders, and the server it would use.

> [!NOTE]
> fw runs R and Python only to create and bootstrap projects in those
> languages. Stata projects need neither.
