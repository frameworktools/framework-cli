---
id: quickstart
title: Quickstart
description: Log in, create a project from Framework Cloud, and publish a notebook
section: getting_started
position: 2
---

This walks through the most common path: log in, create a project you
started on Framework Cloud, prepare it, and publish a rendered notebook.

## Log in

```sh
fw login
```

fw explains what connecting the machine means, opens a browser to approve the
login, and stores the token in the system keyring. The R and Python packages
read the same keyring entry, so logging in once covers all three. To use a
self-hosted server, add `--url https://framework.example.edu`.

Check the login:

```sh
fw status
```

## Create a project

List your projects and create one locally:

```sh
fw projects list
fw new --from vaccine-study
```

fw fetches the project's layers and hands them to the project's language
package: R, framework-py, or fw's own Stata scaffolding. It then links the
project to the cloud by adding `FW_PROJECT_TOKEN` and `FW_CLOUD_URL` to its
`.env`.

To start a project without the cloud, give a name and a language:

```sh
fw new survey-analysis --lang python --type project
```

## Prepare a cloned project

After cloning a project someone else created, run:

```sh
cd vaccine-study
fw project bootstrap
```

It restores packages, creates `.env` from `.env.example`, and creates the
folders git does not keep. See [Creating and preparing projects](guide:projects) for what happens in each
language.

## Publish a notebook

Render the notebook, then publish the HTML:

```sh
quarto render notebooks/results.qmd
fw publish outputs/notebooks/results.html --slug results
```

fw shows what it will upload and asks first. The document gets a private
link, and `--readable` also gives it a readable address under your handle.
See [Publishing documents](guide:publishing).
