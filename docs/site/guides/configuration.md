---
id: configuration
title: Servers, tokens, and environment variables
description: Where fw finds its server and token, and the variables that change it
section: guides
position: 4
---

fw shares its login with the R and Python packages. All three find the same
server and token, so logging in with any one of them works for the others.

## Which server fw uses

For account commands such as `fw login` and `fw projects list`, fw uses the
first of these:

1. the `--url` flag
2. the `FW_CLOUD_URL` environment variable
3. the server you last logged in to
4. `https://framework.pub`

Inside a project, commands that use the project token (`fw publish`,
`fw project push-spec`, and `fw project status`) look at the project's `.env`
before the environment, so a project stays linked to the server it came
from.

Plain `http://` is allowed only for `localhost`.

## Where the token is kept

`fw login` stores the token in the system keyring: Keychain on macOS,
Credential Manager on Windows, and the Secret Service on Linux. The entry is
`framework-cloud:<host>` with account `token`, which R and Python also read.
On machines without a keyring, such as many HPC clusters, fw falls back to a
token file in its configuration folder, readable only by you.

`fw config path` shows fw's folder and the R package's folder.
`fw config sync` copies a saved server and token file from R's folder
without overwriting anything.

## Environment variables

| Variable | Effect |
|----------|--------|
| `FW_CLOUD_URL` | The server, unless `--url` is given |
| `FW_CLOUD_TOKEN` | A token to use instead of the stored one |
| `FW_TOKEN_FILE` | A file holding the token; when set, fw reads only this |
| `FW_CONFIG_HOME` | fw's configuration folder (default `~/.config/framework`) |
| `FW_RSCRIPT` | The `Rscript` to run |
| `FW_UV` | The `uv` to run |
| `FW_PYTHON` | The Python to run when uv is not used |
| `FW_PYTHON_PACKAGE` | Where uv installs framework-py from, such as a local checkout |

Project `.env` files hold `FW_PROJECT_TOKEN` and `FW_CLOUD_URL`, written by
`fw project link`. fw refuses to write a token into a `.env` that git tracks.

> [!TIP]
> In CI, set `FW_CLOUD_TOKEN` from a secret instead of running `fw login`.
