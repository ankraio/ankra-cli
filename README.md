# Ankra CLI

[![Latest release](https://img.shields.io/github/v/release/ankraio/ankra-cli)](https://github.com/ankraio/ankra-cli/releases/latest)
[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)

`ankra` is the command-line interface for the [Ankra platform](https://ankra.io).
Manage Kubernetes clusters, deploy stacks and addons, provision managed clusters
on Hetzner, OVHcloud, UpCloud, DigitalOcean, or Scaleway, and chat with AI about your
infrastructure — from your terminal or your CI scripts.

## Installation

**Homebrew** (macOS and Linux):

```bash
brew install ankraio/tap/ankra
```

**Quick install script** (macOS and Linux without Homebrew):

```bash
bash <(curl -sL https://github.com/ankraio/ankra-cli/releases/latest/download/install.sh)
```

Binaries for macOS, Linux, and Windows are on the
[releases page](https://github.com/ankraio/ankra-cli/releases/latest).
See [INSTALL.md](INSTALL.md) for manual installation and pinning a specific version.

Script and manual installs update themselves with `ankra upgrade`
(opt into pre-releases with `ankra config beta enable`).
Homebrew installs update with `brew update && brew upgrade ankra`.

## Getting started

Authenticate via your browser, then pick a cluster to work with:

```bash
ankra login            # browser-based login; saves a token to ~/.ankra.yaml
ankra cluster select   # interactive cluster picker (persists across sessions)

ankra cluster list     # all clusters in your organisation
ankra cluster stacks list
ankra cluster addons list
```

For CI and scripts, set `ANKRA_API_TOKEN` instead of logging in, and add
`-o json` (or `-o yaml`) to any command for machine-readable output.

## Examples

```bash
# Apply a cluster definition and follow the rollout
ankra cluster apply -f cluster.yaml
ankra cluster operations list --watch

# Bump a Deployment image tag in place
ankra cluster manifests upgrade web \
  --set 'spec.template.spec.containers[name=app].image=nginx:1.27'

# Patch one Helm value on an installed addon
ankra cluster addons upgrade grafana --set image.tag=11.2.0

# Investigate and retry failures
ankra cluster operations list --failed
ankra cluster operations retry <execution_id>

# Provision a managed cluster on Hetzner
ankra cluster hetzner create --name prod --credential-id <cred> \
  --location fsn1 --worker-count 3

# Connect and manage an application
ankra application add .
ankra application list
ankra application deployments <application-id>

# Store Scaleway credentials (keys are masked when prompted)
ankra credentials scaleway create --name scw-prod --project-id <project-id>

# Inspect live Scaleway catalogs before provisioning
ankra cluster scaleway locations --credential-id <scaleway-credential-id>
ankra cluster scaleway instance-types --credential-id <scaleway-credential-id> --zone fr-par-1

# Preflight/create Scaleway Kapsule from one strict YAML/JSON request
ankra cluster managed kapsule preflight --file kapsule.yaml
ankra cluster managed kapsule create --file kapsule.yaml

# Discover and import an existing Kapsule cluster
ankra cluster managed kapsule discover --credential-id <scaleway-credential-id> -o json
ankra cluster managed kapsule import --credential-id <scaleway-credential-id> \
  --provider-cluster-id regions/fr-par/clusters/<provider-id>

# Ask AI about your infrastructure
ankra chat "why is my nginx pod crash-looping?" --cluster prod

# Install the Ankra Agent Skills into Cursor or Claude Code
ankra skills install --editor claude-code

# Machine-readable output for scripts and agents
ankra cluster list -o json
```

Every command acts on your selected cluster and organisation by default;
target a different one for a single command with the global `--cluster <name|id>`
and `--org <name|id>` flags.

## Documentation

The full CLI documentation lives at [docs.ankra.ai](https://docs.ankra.ai):

- **[CLI guide](https://docs.ankra.ai/integrations/ankra-cli)** — installation, authentication, configuration, and troubleshooting
- **[Command reference](https://docs.ankra.ai/reference/cli)** — every command, flag, and default, generated from the CLI source
- **[CLI changelog](https://docs.ankra.ai/integrations/ankra-cli-changelog)** — release history
- **[Scaleway provider guide][scaleway-provider-guide]** — Instances and
  Kapsule IAM, networking, lifecycle, retention, and troubleshooting
- **[Scaleway operations runbook][scaleway-operations-runbook]** — rotation,
  recovery, orphan sweeps, acceptance, metrics, and alerts

[scaleway-provider-guide]: https://github.com/ankraio/cluster/blob/main/docs/providers/scaleway.md
[scaleway-operations-runbook]: https://github.com/ankraio/cluster/blob/main/docs/runbooks/scaleway-operations.md

`ankra --help` and `ankra <command> --help` document every command offline.
Commands scheduled for removal are tracked in [DEPRECATIONS.md](DEPRECATIONS.md).

## Development

Requires Go 1.25+.

```bash
git clone https://github.com/ankraio/ankra-cli.git
cd ankra-cli
go build -o ankra
go test -race ./...
```

Contributions are welcome: fork the repo, create a feature branch, make sure
`go test -race ./...` passes, and open a pull request. To report a security
issue, see [SECURITY.md](SECURITY.md).

## Support

- Issues: [github.com/ankraio/ankra-cli/issues](https://github.com/ankraio/ankra-cli/issues)
- Community: [community.ankra.io](https://community.ankra.io)
- Email: hello@ankra.io

## License

[Apache 2.0](LICENSE)
