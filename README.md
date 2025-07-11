# Ankra CLI

A command-line interface for the [Ankra Platform](https://ankra.io) that allows you to manage Kubernetes clusters, operations, stacks, manifests, addons—and tap into platform-wide insights, interactive builders, and multi-cluster & multi-organization workflows.

## Features

- **Cluster Management**
  - Select and manage multiple Kubernetes clusters across different organizations
  - Switch context between clusters in one or many organizations
  - Persistent cluster selection across sessions

- **Multi-Organization & Multi-Cluster**
  - Authenticate once and access clusters in any organization you’re a member of
  - List and filter clusters by organization, region, or labels
  - Perform bulk operations—deploy, delete, inspect—across many clusters at once

- **Operations & Insight**
  - View and track all operations (create, update, delete) across clusters
  - Stream real-time logs and events for any operation
  - Drill into operation timelines, statuses, and related jobs
  - Query platform metrics (CPU, memory, networking) for clusters and namespaces

- **Interactive Builder**
  - Guided, interactive Helm-style chart & manifest generator
  - Preview rendered YAML before you apply
  - Save templates to your account for reuse across teams

- **Stacks & Manifests**
  - List, inspect, and manage Kubernetes stack definitions
  - Decode base64-encoded manifests to view full YAML
  - Show parent-child relationships between stacks, manifests, and addons

- **Addons**
  - Install, upgrade, and remove Helm charts (e.g., `fluent-bit`, `cert-manager`)
  - See chart repository, version history, and health status

- **Platform Hooks & Automation**
  - Secure authentication via API token or OIDC
  - Automatically generate GitOps manifests from platform resources
  - Trigger CI/CD pipelines and webhooks when operations complete

- **Help & Versioning**
  - `--help` on any command
  - `--version` to see CLI release & API compatibility

> **New to Ankra?** Start with our [platform overview](https://ankra.io) and [getting started guide](https://docs.ankra.io/getting-started).

## Installation

### Quick Install (Recommended)

For **macOS** and **Linux**, use the universal installer:

```bash
bash <(curl -sL https://github.com/ankraio/ankra-cli/releases/latest/download/install.sh)
```

This script will:
- Auto-detect OS & architecture
- Download the correct binary
- Handle macOS security attributes
- Install to `/usr/local/bin`

### Manual Installation

1. **Download the binary** for your platform from the [latest release](https://github.com/ankraio/ankra-cli/releases/latest):
   - `ankra-cli-darwin-amd64` (macOS Intel)
   - `ankra-cli-darwin-arm64` (macOS Apple Silicon)
   - `ankra-cli-linux-amd64` (Linux x86_64)
   - `ankra-cli-linux-arm64` (Linux ARM64)
   - `ankra-cli-windows-amd64.exe` (Windows x86_64)
   - `ankra-cli-windows-arm64.exe` (Windows ARM64)

2. **Make it executable and install**:
   ```bash
   chmod +x ankra-cli-*
   sudo mv ankra-cli-* /usr/local/bin/ankra
   ```

3. **For macOS**: Remove quarantine attribute:
   ```bash
   xattr -d com.apple.quarantine /usr/local/bin/ankra
   ```

## Build from Source

**Prerequisites**: Go 1.19+

```bash
git clone https://github.com/ankraio/ankra-cli.git
cd ankra-cli
go build -o ankra
```

## Configuration

### Authentication

Set your API token (or use OIDC login):

1. **Environment variable**:
   ```bash
   export ANKRA_API_TOKEN=your_api_token_here
   ```

2. **Config file** (`~/.ankra.yaml`):
   ```yaml
   token: your_api_token_here
   ```

> **Get your API token**: Sign up or log in at [ankra.io](https://ankra.io) to generate your API token from the dashboard.

3. **CLI flag**:
   ```bash
   ankra --token your_api_token_here [command]
   ```

## Usage

### Basic Workflow

1. **Authenticate / login** (if using OIDC):
   ```bash
   ankra login
   ```

2. **Select a cluster** (interactive across all orgs):
   ```bash
   ankra select cluster
   ```

3. **Browse resources**:
   ```bash
   ankra get clusters         # list all clusters by org
   ankra get operations       # track operations platform-wide
   ankra get stacks           # list stacks in active cluster
   ankra get addons           # list addons in active cluster
   ankra get metrics --cpu    # fetch CPU metrics for active cluster
   ```

4. **Interactive builder**:
   ```bash
   ankra builder start        # launch guided manifest/chart builder
   ankra builder preview      # preview rendered output
   ankra builder apply        # apply to selected cluster
   ```

5. **Multi-cluster operations**:
   ```bash
   ankra multi run --clusters cluster-a,cluster-b -- command-to-run
   ```

### Command Reference

#### Cluster Management
```bash
# List clusters across all organizations
ankra get clusters

# Select an active cluster
ankra select cluster

# Switch organization context
ankra select organization
```

#### Operations & Insight
```bash
# List all operations (filter by org or cluster)
ankra get operations --organization AcmeCorp

# Show operation details and logs
ankra get operations 1234abcd

# Stream live logs for an operation
ankra logs operation 1234abcd --follow

# Fetch CPU & memory metrics
ankra get metrics --cpu --memory
```

#### Interactive Builder
```bash
# Start builder wizard
ankra builder start

# Preview generated manifest
ankra builder preview --output yaml

# Apply built resources
ankra builder apply
```

#### Multi-Cluster & Multi-Org
```bash
# Run a command across multiple clusters
ankra multi run --clusters cluster1,cluster2 -- kubectl get pods

# Export stack definitions from all clusters in an org
ankra multi export stacks --organization DevTeam
```

## Examples

```bash
# Log in with OIDC
ankra login

# Select cluster in AcmeCorp
ankra select organization   # choose AcmeCorp
ankra select cluster        # choose production-cluster

# Deploy an addon to multiple clusters
ankra multi run   --clusters prod,staging   -- ankra apply addon cert-manager

# Build & apply a new NGINX stack interactively
ankra builder start   --name nginx-stack   --namespace web   --image nginx:1.24   && ankra builder apply
```

## Troubleshooting

- Ensure `ankra` is in your `PATH`
- Verify `ANKRA_API_TOKEN` is set or run `ankra login`
- Check connectivity: `ankra get clusters`
- Consult platform logs: `ankra get operations --failed`
- Visit our [documentation](https://docs.ankra.io) for detailed guides
- Check the [Ankra Platform status](https://status.ankra.io) for any service outages

## Project Structure

```
cli/
├── cmd/                    # Command implementations
├── internal/client/        # API clients
├── install.sh
├── README.md               # This file
```

## Contributing

1. Fork the repo
2. Create a feature branch
3. Implement & test
4. Open a pull request


## Learn More

- **Platform Overview**: [ankra.io](https://ankra.io)
- **Documentation**: [docs.ankra.io](https://docs.ankra.io)
- **Blog & Tutorials**: [blog.ankra.io](https://blog.ankra.io)
- **Community**: [community.ankra.io](https://community.ankra.io)

## Support

- Issues: https://github.com/ankraio/ankra-cli/issues
- Documentation: [docs.ankra.io](https://docs.ankra.io)
- Community Slack: [community.ankra.io](https://community.ankra.io)
- Email: hello@ankra.io
