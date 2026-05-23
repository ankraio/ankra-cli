# Ankra CLI Installation Guide

This guide provides step-by-step instructions for installing the Ankra CLI on your system.

## Installation

### Quick install (recommended)

```sh
bash <(curl -sL https://github.com/ankraio/ankra-cli/releases/latest/download/install.sh)
```

The installer downloads the binary to a private temp directory, verifies
its SHA256 checksum, strips the macOS quarantine attribute when needed,
and installs to `/usr/local/bin/ankra`. The temp directory is removed on
exit.

### Pin to a specific release

```sh
bash <(curl -sL https://github.com/ankraio/ankra-cli/releases/latest/download/install.sh) --version v1.0.0
```

### Manual install with verification

See [SECURITY.md](./SECURITY.md) for instructions on downloading a
specific release asset and checking its `.sha256` sibling.

## Authentication

The recommended path is the browser-based login:

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

## Troubleshooting

- Ensure that `/usr/local/bin` is on your `$PATH`. You may need to
  restart your terminal after installation.
- If `ankra` exits with `invalid Ankra base URL: ...`, double-check the
  value of `ANKRA_BASE_URL`.
- For installation or authentication issues that look like bugs, please
  email hello@ankra.io with the output of `ankra --version` and the
  failing command.
