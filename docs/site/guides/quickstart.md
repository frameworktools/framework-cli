---
id: quickstart
title: Quickstart
description: Sign in, create a project from Framework Cloud, and publish an artifact
section: getting_started
position: 2
---

This guide covers signing in to Framework Cloud, creating a linked local project, bootstrapping a cloned repository, and publishing a rendered report.

## Sign-in

```sh
fw login
```

`fw` prints a data-sharing notice, opens your browser to approve the device code, and stores the resulting token in the system keyring. The R and Python packages read the same keyring entry. Pass `--url https://framework.example.edu` to connect to a self-hosted server.

Verify the active account and server:

```sh
fw status
```

## Project creation

List your Cloud projects and scaffold one locally:

```sh
fw projects list
fw new --from vaccine-study
```

At an interactive terminal, `fw` offers a directory named after the project slug (inside your configured **Projects folder** when set, or the current working directory). Pass `--dir` to set the target path directly. `fw` downloads the project layers, runs the language scaffolder (`framework` in R, `framework-py` in Python, or `fw`'s built-in Stata engine), and writes `FW_PROJECT_TOKEN` and `FW_CLOUD_URL` to `.env`.

To create a local project without Framework Cloud, pass a project name and language:

```sh
fw new survey-analysis --lang python --type project --offline
```

## Cloned repository setup

After cloning an existing Framework repository, restore its local environment:

```sh
cd vaccine-study
fw project bootstrap
```

`fw project bootstrap` restores locked packages, copies `.env.example` to `.env` when missing, creates gitignored directories, and installs Git hooks. See [Project creation and setup](guide:projects) for language-specific behavior.

## Notebook publishing

Render the Quarto document, then publish the resulting HTML file:

```sh
quarto render notebooks/results.qmd
fw publish outputs/notebooks/results.html --slug results
```

`fw` prints the upload summary and asks for confirmation. Published Cloud artifacts receive an unguessable unlisted URL by default; pass `--readable` to add a handle-based URL under your account. See [Publishing artifacts](guide:publishing) for collections, visibility controls, and static destinations.
