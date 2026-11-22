# Ankra CLI Installation Guide

This guide provides step-by-step instructions for installing the Ankra CLI on your system.

## Installation

### Install script (recommended)

macOS and Linux:

```sh
curl -fsSL https://ankra.ai/install | sh
```

Windows, in PowerShell:

```powershell
irm https://ankra.ai/install.ps1 | iex
```

The script installs `ankra` and then runs `ankra onboard`, which signs you
up or in through your browser and sets up only what your account still
needs (see [First steps](#first-steps)). To install without onboarding:

```sh
curl -fsSL https://ankra.ai/install | sh -s -- --no-onboard
```

On macOS and Linux the script downloads the binary into a private temporary
directory and verifies its SHA-256 checksum. It refuses to install when the
checksum is missing or does not match. It then installs without `sudo` and
without asking anything:

- next to an `ankra` you already have, when it can write there
- else into `/usr/local/bin`, when that is writable
- else into `~/.ankra/bin`, which it adds to your `PATH` in your shell's
  startup files (`~/.zshrc` and `~/.zshenv`, `~/.bashrc` and your bash login
  file, or `~/.profile`) inside a block marked `ankra installer`, and links
  from `~/.local/bin/ankra`

It prints the `export PATH=...` line that makes `ankra` work in the terminal
you ran it from. An `ankra` installed by Homebrew is left alone: update it with
`brew upgrade ankra`.

On Windows the script installs `ankra.exe` into
`%LOCALAPPDATA%\Programs\ankra` and adds that directory to your user `PATH`.

`https://ankra.ai/install` redirects to
`https://github.com/ankraio/ankra-cli/releases/latest/download/install.sh`
(and `install.ps1` likewise), so the GitHub URL works too.

### Install script options

| Flag | Environment variable | Effect |
| --- | --- | --- |
| `--version vX.Y.Z` | `ANKRA_VERSION` | install this release instead of the latest |
| `--install-dir DIR` | `ANKRA_INSTALL_DIR` | install into `DIR` |
| `--no-onboard` | `ANKRA_NO_ONBOARD=1` | install only, do not run `ankra onboard` |
| `--no-modify-path` | `ANKRA_NO_MODIFY_PATH=1` | do not edit shell startup files (macOS and Linux) |

Flags go after `sh -s --`:

```sh
curl -fsSL https://ankra.ai/install | sh -s -- --version v1.0.0 --no-onboard
```

On Windows, set the environment variables before running the script, for
example `$env:ANKRA_NO_ONBOARD = '1'`.

### Homebrew

```sh
brew install ankraio/tap/ankra
```

Upgrade later with `brew update && brew upgrade ankra`. Homebrew-managed
binaries refuse `ankra upgrade` (self-update) and defer to brew, so the
two upgrade paths never fight over the same file.

### Manual install with verification

See [SECURITY.md](./SECURITY.md) for instructions on downloading a
specific release asset and checking its `.sha256` sibling.

## Authentication

`ankra onboard` signs you up or in, and the install script runs it for you.
On its own, `ankra login` is the browser-based sign-in:

```sh
ankra login
```

That writes a token to `~/.ankra.yaml` with `0600` permissions. To log
out and clear the token:

```sh
ankra logout
```

Alternatively, set the token in the environment:

```sh
export ANKRA_API_TOKEN=your-api-token
```

To target a non-default platform (for example, internal environments):

```sh
export ANKRA_BASE_URL=https://platform.example.internal
```

`ANKRA_BASE_URL` must be HTTPS unless it points at loopback
(`localhost`/`127.0.0.1`) or `ANKRA_ALLOW_INSECURE_HTTP=1` is set
explicitly.

## First steps

`ankra onboard` does the first steps for you: it signs you in, picks your
organisation, offers a free playground cluster when the organisation has
none, and installs the Ankra skills for the coding agents on your machine.
Rerun it at any time; it skips what is already done.

A coding agent runs the same flow with `--json`: one JSON event per line,
each with an `instruction` for the agent and the `next_command` to run.
Without a terminal, `ankra onboard --json` prints a sign-in link for the
user to open, and `ankra onboard --wait --json` waits for the approval and
finishes. Rerunning hands out the same link until it expires.

To do the same steps by hand:

1. **Pick an organisation** (only required if you belong to more than one):
   ```sh
   ankra org list
   ankra org switch <organisation_id>
   ```
2. **Select an active cluster** for subsequent commands:
   ```sh
   ankra cluster list
   ankra cluster select <cluster_name>
   ```
3. **Inspect cluster state**:
   ```sh
   ankra cluster info
   ankra cluster get pods
   ```
4. **Apply a cluster YAML**:
   ```sh
   ankra cluster apply -f cluster.yaml
   ```
5. **Get help on any command**:
   ```sh
   ankra --help
   ankra cluster --help
   ```

The full command reference — every command, flag, and default — lives at
[docs.ankra.ai/reference/cli](https://docs.ankra.ai/reference/cli).

## Troubleshooting

- If `ankra` is not found after installing, run the `export PATH=...` line
  the install script printed, or open a new terminal. The script names the
  directory it installed into.
- If `ankra` exits with `invalid Ankra base URL: ...`, double-check the
  value of `ANKRA_BASE_URL`.
- For installation or authentication issues that look like bugs, please
  email hello@ankra.io with the output of `ankra --version` and the
  failing command.
