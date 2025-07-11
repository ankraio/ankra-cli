# Ankra CLI

A command-line interface for the Ankra platform that allows you to manage Kubernetes clusters, operations, stacks, manifests, and addons.

## Features

- **Cluster Management**: Select and manage multiple Kubernetes clusters
- **Operations**: View and track cluster operations and their status
- **Stacks**: List and inspect Kubernetes stacks with their manifests and addons
- **Manifests**: View Kubernetes manifests with decoded YAML content
- **Addons**: Manage Helm charts and addons deployed to clusters
- **Persistent Selection**: Remember your active cluster selection across sessions

## Installation

### Quick Install (GitLab) - Option 1

```bash
# Download and run the installation script
curl -sSL https://cicd.infra.ankra.cloud/ankra/cli/-/raw/minimal/install-gitlab.sh?ref_type=heads -o install-gitlab.sh
chmod +x install-gitlab.sh
./install-gitlab.sh
```

### Quick Install (GitLab) - Option 2

If the above doesn't work, try building from source:

```bash
# Clone and build
git clone https://cicd.infra.ankra.cloud/ankra/cli.git
cd cli
go build -o ankra
sudo mv ankra /usr/local/bin/
```

### Manual Installation

1. **Download the binary** directly:
   ```bash
   curl -sSL https://cicd.infra.ankra.cloud/ankra/cli/-/raw/minimal/ankra?ref_type=heads&inline=false -o ankra
   ```

2. **Make it executable and install**:
   ```bash
   chmod +x ankra
   sudo mv ankra /usr/local/bin/
   ```

### Build from Source

**Prerequisites**: Go 1.19 or later

```bash
git clone https://cicd.infra.ankra.cloud/ankra/cli.git
cd cli
go build -o ankra
```

## Configuration

### Authentication

Set your API token using one of these methods:

1. **Environment variable**:
   ```bash
   export ANKRA_API_TOKEN=your_api_token_here
   ```

2. **Configuration file** (`~/.ankra.yaml`):
   ```yaml
   token: your_api_token_here
   base-url: https://platform.ankra.app
   ```

3. **Command line flag**:
   ```bash
   ankra --token your_api_token_here [command]
   ```

### Base URL Configuration

The CLI defaults to `https://platform.ankra.app`. To use a different instance:

```bash
export ANKRA_BASE_URL=https://your-ankra-instance.com
```

## Usage

### Basic Workflow

1. **Select a cluster**:
   ```bash
   ankra select cluster
   ```

2. **View cluster resources**:
   ```bash
   # List all clusters
   ankra get clusters

   # List stacks in active cluster
   ankra get stacks

   # List manifests in active cluster
   ankra get manifests

   # List addons in active cluster
   ankra get addons
   ```

3. **View detailed information**:
   ```bash
   # View specific stack details
   ankra get stacks "stack-name"

   # View specific manifest with decoded YAML
   ankra get manifests "manifest-name"

   # View specific addon details
   ankra get addons "addon-name"
   ```

### Commands Reference

#### Cluster Management
```bash
# List all available clusters
ankra get clusters

# Select an active cluster (interactive)
ankra select cluster

# Clear cluster selection
ankra get clear-selection
```

#### Stacks
```bash
# List all stacks in the active cluster
ankra get stacks

# View detailed information about a specific stack
ankra get stacks "my-stack"
```

Stack details include:
- Stack metadata (name, description, state)
- Associated manifests with Kubernetes resource kinds
- Associated addons with Helm chart information
- Parent-child relationships
- State indicators (✓ up, ⟳ updating, ✗ failed, ● other)

#### Manifests
```bash
# List all manifests in the active cluster
ankra get manifests

# View detailed information about a specific manifest
ankra get manifests "my-manifest"
```

Manifest details include:
- Manifest metadata (name, kind, namespace, state)
- Parent dependencies
- **Full decoded YAML content** from base64-encoded manifest
- Kubernetes resource kind extraction

#### Addons
```bash
# List all addons in the active cluster
ankra get addons

# View detailed information about a specific addon
ankra get addons "my-addon"
```

Addon details include:
- Addon metadata (name, chart, version, namespace)
- Helm chart information (repository, version)
- Health and state status
- Whether managed through Ankra
- Creation and update timestamps

### Advanced Usage

#### Case-Insensitive Matching
All resource name matching is case-insensitive:
```bash
ankra get stacks "My-Stack"     # Matches "my-stack"
ankra get manifests "NGINX"     # Matches "nginx"
```

#### Output Formatting
The CLI provides rich, formatted output with:
- **Tables** for list views with proper column sizing
- **Colored state indicators** (✓ ✗ ⟳ ●)
- **Hierarchical displays** for stack relationships
- **Syntax highlighting** for YAML content

#### Parent-Child Relationships
View resource dependencies:
```bash
# Stack view shows manifest and addon parents
ankra get stacks "web-app"

# Manifest view shows parent stacks/resources
ankra get manifests "nginx-deployment"
```

## Examples

### Complete Workflow Example

```bash
# 1. Set up authentication
export ANKRA_API_TOKEN=your_token_here

# 2. Select a cluster to work with
ankra select cluster
# → Interactive selection from available clusters

# 3. Explore cluster resources
ankra get stacks
# ┌─────────────────┬─────────────────┬─────────────┬───────────────┐
# │ Name            │ Description     │ State       │ Resources     │
# │ web-application │ Main web stack  │ ✓ up       │ 3M, 2A        │
# └─────────────────┴─────────────────┴─────────────┴───────────────┘

# 4. View detailed stack information
ankra get stacks "web-application"
# Stack Details:
#   Name:        web-application
#   Description: Main web stack
#   State:       ✓ up
#
#   Manifests:
#   ├── nginx-deployment (Deployment) - ✓ up
#   │   └── Parents: web-application (stack)
#   └── nginx-service (Service) - ✓ up
#       └── Parents: web-application (stack)
#
#   Addons:
#   ├── cert-manager (cert-manager/v1.12.0) - ✓ healthy
#   │   └── Parents: web-application (stack)

# 5. View manifest details with decoded YAML
ankra get manifests "nginx-deployment"
# Manifest Details:
#   Name:        nginx-deployment
#   Kind:        Deployment
#   Namespace:   default
#   State:       ✓ up
#   Parents:     web-application (stack)
#
#   Manifest Content:
#     apiVersion: apps/v1
#     kind: Deployment
#     metadata:
#       name: nginx-deployment
#       namespace: default
#     spec:
#       replicas: 3
#       selector:
#         matchLabels:
#           app: nginx
#       template:
#         metadata:
#           labels:
#             app: nginx
#         spec:
#           containers:
#           - name: nginx
#             image: nginx:1.14.2
#             ports:
#             - containerPort: 80
```

### Troubleshooting Common Issues

```bash
# Check if cluster is selected
ankra get clusters
# Look for "SELECTED" indicator

# Re-select cluster if needed
ankra select cluster

# Verify API token is set
echo $ANKRA_API_TOKEN

# Check connectivity
ankra get clusters
```

## Project Structure

```
cli/
├── cmd/                    # CLI command implementations
│   ├── root.go            # Root command and global flags
│   ├── cluster.go         # Cluster management commands
│   ├── cluster_addon.go   # Addon listing and details
│   ├── cluster_stacks.go  # Stack listing and details
│   ├── cluster_manifests.go # Manifest listing and details
│   ├── cluster_select.go  # Interactive cluster selection
│   └── helpers.go         # Shared utility functions
├── internal/client/       # API client implementations
│   ├── clusters.go        # Cluster API calls
│   ├── addons.go          # Addon API calls
│   ├── stacks.go          # Stack API calls
│   ├── manifests.go       # Manifest API calls
│   └── helpers.go         # HTTP client utilities
├── install.sh             # GitHub installation script
├── install-gitlab.sh      # GitLab installation script
└── README.md             # This file
```

## Development

### Prerequisites
- Go 1.19 or later
- Access to Ankra API

### Building
```bash
go build -o ankra
```

### Testing
```bash
go test ./...
```

### Dependencies
- `github.com/spf13/cobra` - CLI framework
- `github.com/spf13/viper` - Configuration management
- `github.com/jedib0t/go-pretty/v6` - Table formatting
- `gopkg.in/yaml.v3` - YAML parsing

## Contributing

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Add tests if applicable
5. Submit a pull request

## License

[Add your license information here]

## Support

For issues and questions:
- Create an issue in the [GitLab repository](https://cicd.infra.ankra.cloud/ankra/cli/-/issues)
- Contact the Ankra team

## Security Notice

⚠️ **macOS Users**: You may encounter a security warning when running the downloaded binary:

> "Apple cannot verify "ankra" is free of malware..."

**Quick Fix**: Use our installation script that automatically handles the security bypass:
```bash
# For Intel Macs
curl -sSL https://github.com/your-org/ankra-cli/releases/latest/download/install-macos-amd64.sh | bash

# For Apple Silicon Macs
curl -sSL https://github.com/your-org/ankra-cli/releases/latest/download/install-macos-arm64.sh | bash
```

**Alternative**: Remove the quarantine attribute manually:
```bash
xattr -d com.apple.quarantine /path/to/ankra-cli
```

See [SECURITY.md](SECURITY.md) for more details and alternative methods.
