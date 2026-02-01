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

- **Organisation Management**
  - List, switch, and create organisations
  - View organisation members and roles
  - Persistent organisation selection across sessions

- **Cluster Management**
  - Switch context between clusters
  - Persistent cluster selection across sessions
  - Trigger cluster reconciliation

- **AI-Powered Chat**
  - Interactive troubleshooting with AI assistance
  - Cluster-aware context for better answers
  - Chat history management
  - AI-analyzed cluster health insights

- **Operations & Insight**
  - View and track all operations (create, update, delete) across clusters
  - Cancel running operations and jobs
  - Stream real-time logs and events for any operation
  - Drill into operation timelines, statuses, and related jobs

- **Stacks & Manifests**
  - List, inspect, and manage Kubernetes stack definitions
  - Create, delete, and rename stacks
  - View stack change history
  - Decode base64-encoded manifests to view full YAML
  - Show parent-child relationships between stacks, manifests, and addons

- **Addons**
  - List available and installed addons
  - View and update addon settings
  - Uninstall addons from clusters
  - See chart repository, version history, and health status

- **Agent Management**
  - View agent status and health
  - Get and generate agent tokens
  - Trigger agent upgrades

- **Credentials & Tokens**
  - List and manage platform credentials
  - Create and revoke API tokens
  - Validate credential names

- **Chart Browser**
  - Browse available Helm charts
  - Search charts by name
  - View chart details, versions, and profiles

- **Cluster Cloning & Templates**
  - Clone stack configurations from existing clusters or remote repositories
  - Support for local files and HTTP/HTTPS URLs (including GitHub raw URLs)
  - Smart conflict resolution with merge, clean, and force options
  - Automatic file downloading and directory structure creation

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

1. **Select a cluster** (interactive):
   ```bash
   ankra cluster select
   ```

2. **Browse cluster resources**:
   ```bash
   ankra cluster list              # list all clusters
   ankra cluster stacks list       # list stacks in active cluster
   ankra cluster addons list       # list addons in active cluster
   ankra cluster operations list   # list operations for active cluster
   ```

3. **Apply Clusters**:
   ```bash
   ankra cluster apply -f cluster.yaml
   ```

4. **Clone existing configurations**:
   ```bash
   ankra cluster clone existing.yaml new-cluster.yaml
   ankra cluster clone https://github.com/user/repo/raw/main/cluster.yaml local.yaml
   ```

### Command Reference

#### Organisation Management
```bash
ankra org list                        # List all organisations
ankra org switch <org_id>             # Switch to a different organisation
ankra org current                     # Show current organisation
ankra org create <name> [--country]   # Create a new organisation
ankra org members [org_id]            # List organisation members
```

#### Cluster Management
```bash
ankra cluster list                    # List all clusters
ankra cluster get <name>              # Get cluster details
ankra cluster select                  # Interactively select a cluster
ankra cluster clear                   # Clear active cluster selection
ankra cluster reconcile [name]        # Trigger cluster reconciliation
ankra cluster apply -f <file>         # Apply an ImportCluster YAML
ankra cluster clone <src> <dst>       # Clone cluster configuration
```

#### Cluster Stacks
```bash
ankra cluster stacks list [name]      # List stacks or show details
ankra cluster stacks create <name>    # Create a new stack
ankra cluster stacks delete <name>    # Delete a stack
ankra cluster stacks rename <old> <new>  # Rename a stack
ankra cluster stacks history <name>   # View stack change history
```

#### Cluster Addons
```bash
ankra cluster addons list [name]      # List addons or show details
ankra cluster addons available        # List addons available for installation
ankra cluster addons settings <name>  # Get addon settings
ankra cluster addons uninstall <name> # Uninstall an addon
```

#### Cluster Operations
```bash
ankra cluster operations list [id]    # List operations or show details
ankra cluster operations cancel <id>  # Cancel a running operation
ankra cluster operations cancel-job <op_id> <job_id>  # Cancel a job
```

#### Cluster Agent
```bash
ankra cluster agent status            # Get agent status
ankra cluster agent token             # Get agent token
ankra cluster agent token --generate  # Generate new agent token
ankra cluster agent upgrade           # Upgrade the agent
```

#### Cluster Manifests
```bash
ankra cluster manifests list [name]   # List manifests or show details
```

#### AI Chat
```bash
ankra chat                            # Interactive chat mode
ankra chat "question"                 # One-shot question
ankra chat --cluster <name>           # Chat with cluster context
ankra chat history [--cluster]        # View chat history
ankra chat show <conversation_id>     # Show a conversation
ankra chat delete <conversation_id>   # Delete a conversation
ankra chat health                     # Get AI-analyzed cluster health
```

#### Credentials
```bash
ankra credentials list [--provider]   # List all credentials
ankra credentials get <id>            # Get credential details
ankra credentials validate <name>     # Validate credential name
ankra credentials delete <id>         # Delete a credential
```

#### API Tokens
```bash
ankra tokens list                     # List API tokens
ankra tokens create <name> [--expires]  # Create a new token
ankra tokens revoke <token_id>        # Revoke a token
ankra tokens delete <token_id>        # Delete a revoked token
```

#### Charts
```bash
ankra charts list [--page] [--subscribed]  # List Helm charts
ankra charts search <query>           # Search charts
ankra charts info <name> [--repository]  # Get chart details
```

## Examples

```bash
# Select and work with a cluster
ankra cluster select
ankra cluster stacks list
ankra cluster addons list

# Clone a cluster configuration from GitHub
ankra cluster clone https://github.com/ankraio/examples/raw/main/cluster.yaml ./my-cluster.yaml

# Apply and reconcile
ankra cluster apply -f my-cluster.yaml
ankra cluster reconcile
```

## Troubleshooting

- Ensure `ankra` is in your `PATH`
- Verify `ANKRA_API_TOKEN` is set
- Check connectivity: `ankra cluster list`
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
