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
  - Invite users and remove members
  - Persistent organisation selection across sessions

- **Cluster Management**
  - Switch context between clusters
  - Persistent cluster selection across sessions
  - Trigger cluster reconciliation
  - Provision and deprovision managed clusters
  - Delete clusters
  - Roll back to a specific resource version

- **AI-Powered Chat**
  - Interactive troubleshooting with AI assistance
  - Cluster-aware context for better answers
  - Chat history management
  - AI-analyzed cluster health insights

- **Operations & Insight**
  - View and track all operations (create, update, delete) across clusters
  - Cancel running operations and jobs
  - List jobs within an operation with filtering by kind and timestamp
  - Drill into operation timelines, statuses, and related jobs

- **Stacks & Manifests**
  - List, inspect, and manage Kubernetes stack definitions
  - Create, delete, and rename stacks
  - Clone stacks between clusters
  - View stack change history
  - Decode base64-encoded manifests to view full YAML
  - Show parent-child relationships between stacks, manifests, and addons

- **Addons**
  - List available and installed addons
  - View addon settings
  - Update addon settings from a JSON file
  - Uninstall addons from clusters
  - See chart repository, version history, and health status

- **Agent Management**
  - View agent status and health
  - Get and generate agent tokens
  - Trigger agent upgrades

- **Hetzner Cloud**
  - Create and deprovision Hetzner Kubernetes clusters
  - Scale worker nodes up and down
  - Check and upgrade Kubernetes versions
  - Manage node groups (add, scale, upgrade, delete)
  - Manage Hetzner API credentials and SSH key credentials

- **OVH Cloud**
  - Create and deprovision OVH Kubernetes clusters
  - Scale worker nodes up and down
  - Check and upgrade Kubernetes versions
  - Manage node groups (add, scale, upgrade, delete)
  - Manage OVH API credentials and SSH key credentials

- **UpCloud**
  - Create and deprovision UpCloud Kubernetes clusters
  - Scale worker nodes up and down
  - Check and upgrade Kubernetes versions
  - Manage node groups (add, scale, upgrade, delete)
  - Manage UpCloud API credentials and SSH key credentials

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

- **SOPS Encryption**
  - Encrypt sensitive values in manifest and addon configuration files
  - Decrypt and view encrypted manifest files
  - View SOPS configuration and public keys
  - Automatic `encrypted_paths` tracking in cluster YAML
  - Add new encrypted keys to already-encrypted files

- **Shell Completion**
  - Generate completion scripts for bash, zsh, fish, and powershell
  - One-command install for the current shell

- **Help & Versioning**
  - `--help` on any command
  - `--version` to see CLI release & API compatibility

> **New to Ankra?** Start with our [platform overview](https://ankra.io) and [getting started guide](https://docs.ankra.io/getting-started).


## Build from Source

**Prerequisites**: Go 1.23+

```bash
git clone https://github.com/ankraio/ankra-cli.git
cd ankra-cli
go test ./...
go build -o ankra
```

## Configuration

### Authentication

The recommended way to authenticate is the browser-based login:

```bash
ankra login
```

This opens your browser, authenticates via the Ankra platform, and saves the token to `~/.ankra.yaml`.

Alternatively, you can provide a token directly:

1. **Environment variable**:
   ```bash
   export ANKRA_API_TOKEN=your_api_token_here
   ```

2. **Config file** (`~/.ankra.yaml`):
   ```yaml
   token: your_api_token_here
   ```

3. **CLI flag**:
   ```bash
   ankra --token your_api_token_here [command]
   ```

To log out and clear saved credentials:

```bash
ankra logout
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

#### Authentication
```bash
ankra login                           # Authenticate via browser
ankra logout                          # Remove saved credentials
```

#### Organisation Management
Aliases: `org`, `organisation`, `organization`
```bash
ankra org list                        # List all organisations
ankra org switch <org_id>             # Switch to a different organisation
ankra org current                     # Show current organisation
ankra org create <name> [--country]   # Create a new organisation
ankra org members [org_id]            # List organisation members
ankra org invite <email> [--role]     # Invite a user (role: member, admin, read-only)
ankra org remove <user_id> [-f]       # Remove a user from the organisation
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
  [--clean] [--force]                 #   Replace all stacks / force merge on conflicts
  [--copy-missing]                    #   Copy missing files for skipped stacks
  [--stack <name>]                    #   Clone only specific stacks (repeatable)
ankra cluster provision [name]        # Provision (start) a managed cluster
ankra cluster deprovision [name]      # Deprovision (stop) a managed cluster
  [--auto-delete] [--force]
ankra cluster roll-to --version <id>  # Roll to a specific resource version
  [--cluster <cluster_id>]
```

#### Delete Resources
```bash
ankra delete cluster <name>           # Delete a cluster by name
ankra delete cluster <name> -f        # Delete without confirmation
```

#### Cluster Stacks
```bash
ankra cluster stacks list [name]      # List stacks or show details
ankra cluster stacks create <name>    # Create a new stack
  [--description <desc>]              #   Stack description
ankra cluster stacks delete <name>    # Delete a stack
ankra cluster stacks rename <old> <new>  # Rename a stack
ankra cluster stacks history <name>   # View stack change history
ankra cluster stacks clone <name>     # Clone a stack to another cluster
  --to <cluster>                      #   Target cluster name or ID (required)
  [--name <new_name>]                 #   New stack name (defaults to original)
  [--include-config]                  #   Include addon configurations (default: true)
```

#### Cluster Addons
```bash
ankra cluster addons list [name]      # List addons or show details
ankra cluster addons available        # List addons available for installation
ankra cluster addons settings <name>  # Get addon settings
ankra cluster addons update <name> -f <file>  # Update addon settings from JSON
ankra cluster addons uninstall <name> # Uninstall an addon
  [--delete]                          #   Also delete the addon permanently
```

#### Cluster Operations
```bash
ankra cluster operations list [id]    # List operations or show details
ankra cluster operations cancel <id>  # Cancel a running operation
ankra cluster operations cancel-job <op_id> <job_id>  # Cancel a job
ankra cluster operations jobs <op_id> # List jobs for an operation
  [--kind <kind>] [--since <ts>]
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

#### SOPS Encryption
```bash
ankra cluster sops-config                          # Show SOPS configuration and public key
ankra cluster encrypt manifest <name> --key <key> -f <file>  # Encrypt a key in a manifest
ankra cluster encrypt addon --name <addon> --key <key> -f <file>  # Encrypt a key in addon config
ankra cluster decrypt manifest <name> -f <file>    # Decrypt and display manifest
```

#### AI Chat
```bash
ankra chat                            # Interactive chat mode
ankra chat "question"                 # One-shot question
ankra chat --cluster <name>           # Chat with cluster context
ankra chat history [--cluster]        # View chat history
  [--limit <n>]                       #   Max conversations to show (default: 20)
ankra chat show <conversation_id>     # Show a conversation
ankra chat delete <conversation_id>   # Delete a conversation
ankra chat health [--ai]              # Get AI-analyzed cluster health (default: true)
```

#### Hetzner Clusters
```bash
ankra cluster hetzner create           # Create a Hetzner cluster
  --name <name>                        #   Cluster name (required)
  --credential-id <id>                 #   Hetzner API credential (required)
  --ssh-key-credential-ids <id,id>     #   SSH key credentials (comma-separated)
  --ssh-key-credential-id <id>         #   SSH key credential (single, backward compat)
  --location <loc>                     #   Datacenter location (required)
  --worker-count <n>                   #   Number of workers (default: 1)
  --worker-server-type <type>          #   Worker server type (default: cx33)
  --control-plane-count <n>            #   Control planes (default: 1)
  --control-plane-server-type <type>   #   CP server type (default: cx33)
  --network-ip-range <cidr>            #   Network IP range (default: 10.0.0.0/16)
  --subnet-range <cidr>                #   Subnet range (default: 10.0.1.0/24)
  --bastion-server-type <type>         #   Bastion server type (default: cx23)
  --distribution <dist>                #   Kubernetes distribution (default: k3s)
  --kubernetes-version <ver>           #   Kubernetes version (optional)
ankra cluster hetzner deprovision <id>    # Deprovision a Hetzner cluster
ankra cluster hetzner workers <id>       # Get current worker count
ankra cluster hetzner scale <id> <n>     # Scale workers to n
ankra cluster hetzner k8s-version <id>   # Get current Kubernetes version
ankra cluster hetzner upgrade <id> <ver> # Upgrade Kubernetes version

ankra cluster hetzner node-group list <id>                  # List node groups
ankra cluster hetzner node-group add <id>                   # Add a node group
  --name <name> [--instance-type <type>] [--count <n>]
ankra cluster hetzner node-group scale <id> <group> <n>     # Scale a node group
ankra cluster hetzner node-group upgrade <id> <group> <type>  # Upgrade instance type
ankra cluster hetzner node-group delete <id> <group>        # Delete a node group
```

#### Hetzner Credentials
Alias: `credentials hetzner` or `creds hz`
```bash
ankra credentials hetzner list                              # List Hetzner API credentials
ankra credentials hetzner create --name <n>                 # Create Hetzner credential (prompts for token)

ankra credentials hetzner ssh-key list                      # List SSH key credentials
ankra credentials hetzner ssh-key create --name <n> --generate          # Generate SSH keypair
ankra credentials hetzner ssh-key create --name <n> --public-key "..."  # Import SSH public key
```

#### OVH Clusters
```bash
ankra cluster ovh create                  # Create an OVH cluster
  --name <name>                           #   Cluster name (required)
  --credential-id <id>                    #   OVH API credential (required)
  --ssh-key-credential-id <id>            #   SSH key credential (required)
  --region <region>                       #   OVH region (required, e.g. GRA7)
  --worker-count <n>                      #   Number of workers (default: 1)
  --worker-flavor-id <flavor>             #   Worker flavor (default: b2-15)
  --control-plane-count <n>               #   Control planes (default: 1)
  --control-plane-flavor-id <flavor>      #   CP flavor (default: b2-15)
  --gateway-flavor-id <flavor>            #   Gateway instance flavor (default: b2-7)
  --network-vlan-id <id>                  #   Network VLAN ID (default: 0)
  --subnet-cidr <cidr>                    #   Subnet CIDR (default: 10.0.1.0/24)
  --dhcp-start <ip>                       #   DHCP range start (default: 10.0.1.100)
  --dhcp-end <ip>                         #   DHCP range end (default: 10.0.1.200)
  --distribution <dist>                   #   Kubernetes distribution (default: k3s)
  --kubernetes-version <ver>              #   Kubernetes version (optional)
ankra cluster ovh deprovision <id>        # Deprovision an OVH cluster
ankra cluster ovh workers <id>            # Get current worker count
ankra cluster ovh scale <id> <n>          # Scale workers to n
ankra cluster ovh k8s-version <id>        # Get current Kubernetes version
ankra cluster ovh upgrade <id> <version>  # Upgrade Kubernetes version

ankra cluster ovh node-group list <id>                    # List node groups
ankra cluster ovh node-group add <id>                     # Add a node group
  --name <name> [--instance-type <type>] [--count <n>]
ankra cluster ovh node-group scale <id> <group> <n>       # Scale a node group
ankra cluster ovh node-group upgrade <id> <group> <type>  # Upgrade instance type
ankra cluster ovh node-group delete <id> <group>          # Delete a node group
```

#### OVH Credentials
```bash
ankra credentials ovh list                                  # List OVH API credentials
ankra credentials ovh create --name <n> --project-id <id>   # Create OVH credential (prompts for secrets)

ankra credentials ovh ssh-key list                          # List SSH key credentials
ankra credentials ovh ssh-key create --name <n> --generate          # Generate SSH keypair
ankra credentials ovh ssh-key create --name <n> --public-key "..."  # Import SSH public key
```

#### UpCloud Clusters
```bash
ankra cluster upcloud create              # Create an UpCloud cluster
  --name <name>                           #   Cluster name (required)
  --credential-id <id>                    #   UpCloud API credential (required)
  --ssh-key-credential-id <id>            #   SSH key credential (required)
  --zone <zone>                           #   Datacenter zone (required, e.g. fi-hel1)
  --worker-count <n>                      #   Number of workers (default: 1)
  --worker-plan <plan>                    #   Worker plan (default: 2xCPU-4GB)
  --control-plane-count <n>               #   Control planes (default: 1)
  --control-plane-plan <plan>             #   CP plan (default: 2xCPU-4GB)
  --network-ip-range <cidr>               #   Network IP range (default: 10.0.0.0/16)
  --bastion-plan <plan>                   #   Bastion plan (default: 1xCPU-2GB)
  --distribution <dist>                   #   Kubernetes distribution (default: k3s)
  --kubernetes-version <ver>              #   Kubernetes version (optional)
ankra cluster upcloud deprovision <id>    # Deprovision an UpCloud cluster
ankra cluster upcloud workers <id>        # Get current worker count
ankra cluster upcloud scale <id> <n>      # Scale workers to n
ankra cluster upcloud k8s-version <id>    # Get current Kubernetes version
ankra cluster upcloud upgrade <id> <ver>  # Upgrade Kubernetes version

ankra cluster upcloud node-group list <id>                  # List node groups
ankra cluster upcloud node-group add <id>                   # Add a node group
  --name <name> [--instance-type <plan>] [--count <n>]
ankra cluster upcloud node-group scale <id> <group> <n>     # Scale a node group
ankra cluster upcloud node-group upgrade <id> <group> <plan>  # Upgrade server plan
ankra cluster upcloud node-group delete <id> <group>        # Delete a node group
```

#### UpCloud Credentials
Alias: `credentials upcloud` or `creds uc`
```bash
ankra credentials upcloud list                              # List UpCloud API credentials
ankra credentials upcloud create --name <n>                 # Create UpCloud credential (prompts for token)

ankra credentials upcloud ssh-key list                      # List SSH key credentials
ankra credentials upcloud ssh-key create --name <n> --generate          # Generate SSH keypair
ankra credentials upcloud ssh-key create --name <n> --public-key "..."  # Import SSH public key
```

#### Credentials
Aliases: `credentials`, `credential`, `cred`, `creds`
```bash
ankra credentials list [--provider]   # List all credentials
ankra credentials get <id>            # Get credential details
ankra credentials validate <name>     # Validate credential name
ankra credentials delete <id>         # Delete a credential
```

#### API Tokens
Aliases: `tokens`, `token`
```bash
ankra tokens list                     # List API tokens
ankra tokens create <name> [--expires]  # Create a new token
ankra tokens revoke <token_id>        # Revoke a token
ankra tokens delete <token_id>        # Delete a revoked token
```

#### Charts
```bash
ankra charts list [--page] [--page-size] [--subscribed]  # List Helm charts
ankra charts search <query>           # Search charts
ankra charts info <name> [--repository]  # Get chart details
```

#### Shell Completion
```bash
ankra completion bash                 # Print bash completion script
ankra completion zsh                  # Print zsh completion script
ankra completion fish                 # Print fish completion script
ankra completion powershell           # Print powershell completion script
ankra completion install [--shell]    # Install completions for current shell
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

# Provision and deprovision managed clusters
ankra cluster provision my-cluster
ankra cluster deprovision my-cluster --auto-delete

# Roll back a cluster to a previous version
ankra cluster roll-to --version <version_id>

# Invite a team member
ankra org invite colleague@company.com --role admin

# Inspect operation jobs
ankra cluster operations list
ankra cluster operations jobs <operation_id> --kind reconcile

# Update addon settings from a file
ankra cluster addons update my-addon -f settings.json

# View SOPS encryption config
ankra cluster sops-config

# Encrypt sensitive values in manifests
ankra cluster encrypt manifest my-secret --key DB_PASSWORD -f cluster.yaml
ankra cluster encrypt addon --name grafana --key adminPassword -f cluster.yaml

# View decrypted manifest content
ankra cluster decrypt manifest my-secret -f cluster.yaml

# Provision an OVH cluster with node groups
ankra credentials ovh create --name my-ovh --project-id abc123
ankra credentials ovh ssh-key create --name my-key --generate
ankra cluster ovh create --name prod --credential-id <cred> --ssh-key-credential-id <key> --region GRA7 --worker-count 3
ankra cluster ovh node-group add <cluster_id> --name gpu-pool --instance-type b2-30 --count 2
ankra cluster ovh node-group scale <cluster_id> gpu-pool 4

# Provision an UpCloud cluster
ankra credentials upcloud create --name my-upcloud
ankra credentials upcloud ssh-key create --name my-key --generate
ankra cluster upcloud create --name prod --credential-id <cred> --ssh-key-credential-id <key> --zone fi-hel1 --worker-count 2
ankra cluster upcloud node-group add <cluster_id> --name workers --instance-type 4xCPU-8GB --count 3
```

## Troubleshooting

- Ensure `ankra` is in your `PATH`
- Verify `ANKRA_API_TOKEN` is set
- Check connectivity: `ankra cluster list`
- Visit our [documentation](https://docs.ankra.io) for detailed guides
- Check the [Ankra Platform status](https://status.ankra.io) for any service outages

## Project Structure

```
ankra-cli/
├── cmd/                    # Cobra command implementations
│   ├── services.go         # APIClient interface definition
│   └── root.go             # Root command, config, auth
├── internal/client/        # HTTP API client
│   ├── client.go           # Client struct and constructor
│   ├── helpers.go          # Shared HTTP helpers (getJSON, parseJSON)
│   ├── clusters.go         # Cluster operations
│   ├── organisations.go    # Organisation management
│   └── ...                 # Addons, stacks, tokens, credentials, chat, etc.
├── testing/stack_test/     # YAML fixtures for testing
├── main.go                 # Entry point
├── go.mod
├── install.sh
└── README.md               # This file
```

## Testing

The project uses Go's standard `testing` package with table-driven tests, `net/http/httptest` for API client tests, and `t.TempDir()` for filesystem tests. No external test dependencies.

### Run all tests

```bash
go test ./...
```

### Run with race detection and coverage

```bash
go test -race -count=1 -coverprofile=coverage.out ./...
go tool cover -func=coverage.out
```

### Test architecture

| Layer | Location | Strategy |
|-------|----------|----------|
| Pure functions | `cmd/*_test.go` | Table-driven unit tests for YAML parsing, URL detection, conflict resolution |
| API client | `internal/client/*_test.go` | httptest-based tests with canned JSON responses for every endpoint |
| Command E2E | `cmd/e2e_test.go` | Mock-based tests via the `APIClient` interface, verifying command output |
| Clone/encrypt | `cmd/clone_integration_test.go` | Filesystem tests with `t.TempDir()` for stack cloning and YAML round-trips |

CI runs `go test -race` on every push via GitHub Actions.

## Contributing

1. Fork the repo
2. Create a feature branch
3. Run `go test -race ./...` and ensure all tests pass
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
