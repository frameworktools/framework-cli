---
id: configuration
title: Servers, tokens, and environment variables
description: Server resolution order, keyring token storage, and environment variables
section: guides
position: 4
---

`fw` shares server configuration and keyring credentials with the R and Python Framework packages. Signing in once with `fw login`, `framework::cloud_login()`, or `framework-py` authenticates all three tools on the machine.

## Server resolution

Account-scoped commands (`fw status`, `fw logout`, `fw projects list`, `fw blueprints list`, `fw new`) select the Framework Cloud server in this order:

1. `--url` flag
2. `FW_CLOUD_URL` environment variable
3. Last server saved by `fw login`, `framework::cloud_login()`, or `framework-py`

Project-scoped commands (`fw publish`, `fw project push-spec`, `fw project status`, `fw pull`, `fw push`, `fw data pull`, `fw data push`, `fw skills pull`) use this order, so each project stays bound to the server that issued its token:

1. `--url` flag
2. `FW_CLOUD_URL` in the project's `.env`
3. `FW_CLOUD_URL` environment variable
4. Saved login server. When saved logins name different servers, the command fails and asks for `FW_CLOUD_URL` in `.env`.

With none of these set, commands exit with an error. They never fall back to `https://framework.pub`. `fw login` without `--url`, `FW_CLOUD_URL`, or a saved login names `https://framework.pub` and asks before connecting.

Plain `http://` URLs are permitted only for `localhost` and loopback addresses.

## Token storage

`fw login` stores personal API tokens in the OS keyring (macOS Keychain, Windows Credential Manager, or Linux Secret Service) under service `framework-cloud:<host>` and account `token`. On headless Linux hosts and HPC clusters without a keyring daemon, `fw` writes a `0600` token file inside its configuration directory instead.

`fw config path` prints both `fw`'s configuration directory and the R package's configuration directory. `fw config sync` copies saved `cloud.yml` and fallback token files from the R directory into `fw`'s directory without overwriting existing files (keyring entries are already shared directly).

## Environment variables

| Variable | Purpose |
|---|---|
| `FW_CLOUD_URL` | Default Framework Cloud server URL when `--url` is omitted |
| `FW_CLOUD_TOKEN` | Personal API token overriding stored keyring or file tokens |
| `FW_TOKEN_FILE` | Path to a file containing the API token (disables keyring lookup when set) |
| `FW_CONFIG_HOME` | Configuration directory for `fw` (default `~/.config/framework`) |
| `FW_RSCRIPT` | Path to the `Rscript` executable |
| `FW_UV` | Path to the `uv` executable |
| `FW_PYTHON` | Path to a Python executable, used instead of `uv` when set |
| `FW_PYTHON_PACKAGE` | Package spec or local path passed to `uv` for `framework-py` |

Unlike keyring entries (which are scoped per server host), `FW_CLOUD_TOKEN` and `FW_TOKEN_FILE` supply a single token for whichever server URL is active. Scope those variables to the matching server.

Project `.env` files store `FW_PROJECT_TOKEN` and `FW_CLOUD_URL`, written by `fw new` or `fw project link`. `fw` refuses to write a token into a `.env` file tracked by Git.

> [!TIP]
> In CI pipelines, pass `FW_CLOUD_TOKEN` (for account commands) from your CI secret store, and write `FW_PROJECT_TOKEN` (for project commands) to the project's `.env`, rather than running `fw login`.
