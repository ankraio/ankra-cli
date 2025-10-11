# Ankra CLI

A command-line interface for the [Ankra Platform](https://ankra.io) that allows you to manage Kubernetes clusters, operations, stacks, manifests, addons—and tap into platform-wide insights.

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


## Features

- **Cluster Management**
  - Switch context between clusters in one
  - Persistent cluster selection across sessions

- **Operations & Insight**
  - View and track all operations (create, update, delete) across clusters
  - Stream real-time logs and events for any operation
  - Drill into operation timelines, statuses, and related jobs
  - Query platform metrics (CPU, memory, networking) for clusters and namespaces


- **Stacks & Manifests**
  - List, inspect, and manage Kubernetes stack definitions
  - Decode base64-encoded manifests to view full YAML
  - Show parent-child relationships between stacks, manifests, and addons

- **Addons**
  - Install, upgrade, and remove Helm charts (e.g., `fluent-bit`, `cert-manager`)
  - See chart repository, version history, and health status

- **Cluster Cloning & Templates**
  - Clone stack configurations from existing clusters or remote repositories
  - Support for local files and HTTP/HTTPS URLs (including GitHub raw URLs)
  - Smart conflict resolution with merge, clean, and force options
  - Automatic file downloading and directory structure creation

- **Platform Hooks & Automation**
  - Secure authentication via API token or OIDC
  - Automatically generate GitOps manifests from platform resources
  - Trigger CI/CD pipelines and webhooks when operations complete

- **Help & Versioning**
  - `--help` on any command
  - `--version` to see CLI release & API compatibility

> **New to Ankra?** Start with our [platform overview](https://ankra.io) and [getting started guide](https://docs.ankra.io/getting-started).


## Build from Source

**Prerequisites**: Go 1.19+

```bash
git clone https://github.com/ankraio/ankra-cli.git
cd ankra-cli
go build -o ankra
```

## Configuration

### Authentication

Set your API token:

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


1. **Select a cluster** (interactive across all orgs):
   ```bash
   ankra select cluster
   ```

2. **Browse resources**:
   ```bash
   ankra get clusters         # list all clusters by org
   ankra get operations       # track operations platform-wide
   ankra get stacks           # list stacks in active cluster
   ankra get addons           # list addons in active cluster
   ```

4. **Apply Clusters**:
   ```bash
   ankra apply -f cluster.yaml
   ```

5. **Clone existing configurations**:
   ```bash
   ankra clone existing.yaml new-cluster.yaml    # copy stacks to new cluster
   ankra clone https://github.com/user/repo/raw/main/cluster.yaml local.yaml
   ```

### Command Reference

#### Cluster Management
```bash
# List clusters across all organizations
ankra get clusters

# Select an active cluster
ankra select cluster
```

#### Operations & Insight
```bash
ankra get operations

# Show operation details and logs
ankra get operations <uuid>
```


#### Cluster Cloning
```bash
# Clone stacks from a local cluster file
ankra clone existing-cluster.yaml new-cluster.yaml

# Clone from a GitHub repository (or any URL)
ankra clone https://github.com/user/repo/raw/main/cluster.yaml new-cluster.yaml

# Clean copy (replace all stacks in target)
ankra clone source.yaml target.yaml --clean

# Force merge (override conflicts)
ankra clone source.yaml target.yaml --force

# Copy missing files even from skipped stacks
ankra clone source.yaml target.yaml --copy-missing

# Combine flags for complete replacement
ankra clone source.yaml target.yaml --clean --force
```

## Examples

```bash
ankra select cluster        # choose production-cluster

# Clone a cluster configuration from GitHub
ankra clone https://github.com/ankraio/ankra-gitops-examples/raw/main/clusters/monitoring-stack/cluster.yaml ./my-cluster.yaml

# Clone and merge with existing cluster (skip conflicts)
ankra clone production.yaml staging.yaml

# Clone with force override
ankra clone production.yaml staging.yaml --force --copy-missing
```

## Troubleshooting

- Ensure `ankra` is in your `PATH`
- Verify `ANKRA_API_TOKEN` is set
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
