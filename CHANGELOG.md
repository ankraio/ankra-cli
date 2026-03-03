# Ankra CLI Changelog

## v0.1.125

### New Features

#### Kubernetes Version Query

Check the current Kubernetes version running on a Hetzner cluster:

```bash
ankra cluster hetzner k8s-version <cluster_id>
```

Example output:

```
Kubernetes Version: v1.29.1+k3s1
  Distribution: k3s
```

#### Kubernetes Version Upgrade

Upgrade the Kubernetes (k3s) version across all nodes in a Hetzner cluster:

```bash
ankra cluster hetzner upgrade <cluster_id> <target_version>
```

Example:

```bash
ankra cluster hetzner upgrade abc123 v1.30.0+k3s1
```

Example output:

```
Kubernetes version upgrade initiated.
  Previous version: v1.29.1+k3s1
  New version:      v1.30.0+k3s1
  Nodes affected:   4
```

### API Endpoints

- `GET /api/v1/clusters/hetzner/{id}/k8s-version` — fetch current k8s version
- `POST /api/v1/clusters/hetzner/{id}/upgrade-k8s-version` — trigger k8s version upgrade

---

## v0.1.124

### New Features

#### Hetzner Cluster Management

Full lifecycle management for Hetzner clusters, including provisioning, deprovisioning, and scaling.

##### Create a Cluster

```bash
ankra cluster hetzner create \
  --name my-cluster \
  --credential-id <cred_id> \
  --ssh-key-credential-id <ssh_key_id> \
  --location fsn1 \
  --worker-count 3 \
  --worker-server-type cx33 \
  --control-plane-count 1 \
  --distribution k3s
```

##### Deprovision a Cluster

```bash
ankra cluster hetzner deprovision <cluster_id>
```

Example output:

```
Hetzner cluster deprovisioned successfully!
  Cluster ID: abc123
  Deleted servers: 4
  Deleted networks: 1
  Deleted SSH keys: 1
```

##### Check Worker Count

```bash
ankra cluster hetzner workers <cluster_id>
```

Example output:

```
Worker Count: 3
  Min: 1
  Max: 10
```

##### Scale Workers

```bash
ankra cluster hetzner scale <cluster_id> <worker_count>
```

Example:

```bash
ankra cluster hetzner scale abc123 5
```

Example output:

```
Scaling up from 3 to 5 workers.
```

#### Hetzner Credentials Management

Manage Hetzner API credentials and SSH keys.

##### List Hetzner Credentials

```bash
ankra credentials hetzner list
```

##### Create a Hetzner Credential

```bash
ankra credentials hetzner create --name my-hetzner-key
```

You will be prompted securely for the API token.

##### List SSH Key Credentials

```bash
ankra credentials hetzner ssh-key list
```

##### Create an SSH Key Credential

```bash
# Generate a new keypair
ankra credentials hetzner ssh-key create --name my-key --generate

# Or provide an existing public key
ankra credentials hetzner ssh-key create --name my-key --public-key "ssh-ed25519 AAAA..."
```

#### Stack Cloning Between Clusters

Clone stacks from one cluster to another as a draft for review before deployment.

```bash
# Clone a stack to another cluster
ankra cluster stacks clone my-stack --to target-cluster

# Clone with a new name
ankra cluster stacks clone my-stack --to target-cluster --name new-stack-name

# Clone without addon configurations
ankra cluster stacks clone my-stack --to target-cluster --include-config=false
```

Example output:

```
Cloning stack 'my-stack' to cluster 'target-cluster'...

Stack cloned successfully!
  Draft ID:    draft-456
  Stack Name:  my-stack
  Addons:      3
  Manifests:   2

The stack has been created as a draft. Open the Ankra dashboard to review and deploy.
```

---

## v0.1.123

### SOPS Encryption Commands

New commands for encrypting and decrypting manifest and addon configuration files using SOPS.

#### Breaking Change

- **Removed**: `ankra cluster sops <secret>` command has been removed

#### New Commands

##### Encrypt Manifest

Encrypt a specific key in a manifest file referenced by the cluster configuration.

```bash
ankra cluster encrypt manifest <manifest_name> --key <key_name> -f <cluster.yaml>
```

Example:
```bash
ankra cluster encrypt manifest trinity-database-secret --key TRINITY_DB_PASSWORD -f cluster.yaml
```

This will:
1. Find the manifest in the cluster YAML
2. Read the referenced manifest file
3. Encrypt the specified key using your organisation's SOPS key
4. Update the manifest file with encrypted values
5. Add the key to `encrypted_paths` in the cluster YAML

##### Encrypt Addon

Encrypt a specific key in an addon's values file.

```bash
ankra cluster encrypt addon --name <addon_name> --key <key_name> -f <cluster.yaml>
```

Example:
```bash
ankra cluster encrypt addon --name grafana --key adminPassword -f cluster.yaml
```

##### Decrypt Manifest

Decrypt and display the contents of a manifest file.

```bash
ankra cluster decrypt manifest <manifest_name> -f <cluster.yaml>
```

Example:
```bash
ankra cluster decrypt manifest trinity-database-secret -f cluster.yaml
```

#### Features

- **Add keys to existing encrypted files**: You can add new encrypted keys to files that are already SOPS-encrypted (as long as they were encrypted with your organisation's key)
- **Clear error messages**: If you try to encrypt a file that was encrypted by a different organisation, you'll get a helpful error message explaining the issue

---

# Ankra CLI v1.0.0

## Highlights

This release introduces the **Ankra CLI** - a powerful command-line interface for managing your Kubernetes infrastructure. Authenticate with SSO, chat with AI about your clusters, browse Helm charts, manage credentials, and control stacks - all from your terminal.

---

## New Features

### SSO Authentication

Securely authenticate with the Ankra platform using browser-based SSO login with PKCE.

```bash
# Login to Ankra (opens browser for SSO)
ankra login

# Logout and clear credentials
ankra logout
```

Your credentials are securely stored in `~/.ankra.yaml` and automatically used for all subsequent commands.

---

### AI-Powered Chat

Get instant help troubleshooting your infrastructure with AI-powered chat. Ask questions about your clusters, get recommendations, and analyze health issues.

#### Interactive Chat Mode

```bash
# Start an interactive chat session
ankra chat

# Chat with cluster context for better answers
ankra chat --cluster my-production-cluster
```

#### One-Shot Questions

```bash
# Ask a single question
ankra chat "Why are my pods in CrashLoopBackOff?"

# Ask with cluster context
ankra chat --cluster staging "How do I scale my deployment?"
```

#### Cluster Health Analysis

```bash
# Get AI-analyzed cluster health for the selected cluster
ankra chat health

# Include detailed AI analysis
ankra chat health --ai
```

#### Chat History Management

```bash
# List previous conversations
ankra chat history

# Show a specific conversation
ankra chat show <conversation_id>

# Delete a conversation
ankra chat delete <conversation_id>
```

---

### Helm Charts

Browse and search the Helm chart catalog directly from your terminal.

#### List Available Charts

```bash
# List all available charts
ankra charts list

# Paginate through charts
ankra charts list --page 2 --page-size 50

# Show only subscribed charts
ankra charts list --subscribed
```

#### Search Charts

```bash
# Search for charts by name
ankra charts search nginx

# Search for monitoring solutions
ankra charts search prometheus
```

#### Chart Information

```bash
# Get detailed info about a chart
ankra charts info nginx

# Specify a repository
ankra charts info grafana --repository https://grafana.github.io/helm-charts
```

**Example Output:**

```
Chart: nginx

  Repository: bitnami (https://charts.bitnami.com/bitnami)

  Available Versions (10):
    - 15.1.2
    - 15.1.1
    - 15.1.0
    ...

  Available Profiles:
    - default: Standard nginx deployment
    - high-availability: Multi-replica HA setup
```

---

### Credentials Management

Manage cloud provider and Git credentials for your clusters.

#### List Credentials

```bash
# List all credentials
ankra credentials list

# Filter by provider
ankra credentials list --provider github
```

#### View Credential Details

```bash
# Get details of a specific credential
ankra credentials get <credential_id>
```

#### Validate & Delete

```bash
# Check if a credential name is available
ankra credentials validate my-new-credential

# Delete a credential
ankra credentials delete <credential_id>
```

**Aliases:** `ankra creds`, `ankra cred`, `ankra credential`

---

### Stack Management

Create, manage, and track infrastructure stacks on your clusters.

#### List & View Stacks

```bash
# First, select a cluster
ankra cluster select

# List all stacks on the active cluster
ankra cluster stacks list

# View details of a specific stack
ankra cluster stacks list my-monitoring-stack
```

**Example Output:**

```
Stack Details:
  Name:        my-monitoring-stack
  Description: Production monitoring
  State:       up
  Manifests:   3
  Addons:      2

  Manifests:
    ✓ prometheus-config
      ├─ kind: ConfigMap
      ├─ namespace: monitoring
      ├─ state: up
      └─ parents: none

  Addons:
    ✓ grafana
      ├─ chart: grafana:6.50.7
      ├─ namespace: monitoring
      ├─ state: up
      └─ parents: none
```

#### Create & Delete Stacks

```bash
# Create a new stack
ankra cluster stacks create my-new-stack --description "Application stack"

# Delete a stack
ankra cluster stacks delete old-stack
```

#### Rename & History

```bash
# Rename a stack
ankra cluster stacks rename old-name new-name

# View change history for a stack
ankra cluster stacks history my-stack
```

---

### Cluster Clone

Clone stacks from an existing cluster to a new cluster configuration. Supports both local files and remote URLs.

```bash
# Clone all stacks from one cluster to another
ankra cluster clone source-cluster.yaml new-cluster.yaml

# Clone from a remote URL
ankra cluster clone https://github.com/org/repo/raw/main/cluster.yaml new-cluster.yaml

# Clone only specific stacks
ankra cluster clone cluster.yaml new-cluster.yaml --stack "monitoring" --stack "networking"

# Replace all stacks in the target cluster
ankra cluster clone cluster.yaml new-cluster.yaml --clean

# Force merge even with naming conflicts
ankra cluster clone cluster.yaml new-cluster.yaml --force

# Copy missing files from skipped stacks
ankra cluster clone cluster.yaml new-cluster.yaml --copy-missing
```

---

### API Tokens

Manage API tokens for programmatic access.

```bash
# List all API tokens
ankra tokens list

# Create a new token
ankra tokens create my-ci-token

# Create token with expiration
ankra tokens create my-temp-token --expires "2024-12-31T00:00:00Z"

# Revoke a token
ankra tokens revoke <token_id>

# Delete a revoked token
ankra tokens delete <token_id>
```

---

### Cluster Operations

```bash
# List all clusters
ankra cluster list

# Get cluster details
ankra cluster get my-cluster

# Select a cluster for subsequent commands
ankra cluster select

# Trigger reconciliation
ankra cluster reconcile my-cluster
```

---

## Bug Fixes

### `ankra cluster clone` - Registry Linkage Fix

Fixed an issue where `ankra cluster clone` did not correctly format the linkage to existing registries when cloning stacks or entire clusters. Addon configurations that reference container registries (`registry_name`, `registry_url`, `registry_credential_name`) are now properly preserved and formatted in the cloned configuration.

**Before:** Registry references in cloned addons could be malformed or missing, causing deployment failures when the cloned cluster tried to pull images from private registries.

**After:** All registry linkage fields are correctly preserved and formatted, ensuring seamless deployments with private container registries.

---

### `ankra chat` - API Request & Response Format Fix

Fixed issues where the chat command had incompatible field names with the backend API:

1. **Request fields:** The CLI was sending `message` and `history` fields, but the backend expects `query` and `conversation_history`.

2. **Response parsing:** The CLI was looking for `content` field in streaming events, but the backend sends content in the `data` field.

3. **Status message formatting:** Status messages (like "Processing...") were being concatenated inline with content, making output hard to read.

**Before:** Chat would fail with 422 validation errors, show empty responses, or display status messages inline with content:
```
Assistant: Processing...I'll generate a report...
```

**After:** The CLI now correctly sends `query` and `conversation_history` fields, properly parses the `data` field from streaming events, and formats status messages on separate lines:
```
Assistant: [Processing...]
I'll generate a report...
```

---

## Getting Started

```bash
# 1. Install the CLI (download from releases)

# 2. Login with SSO
ankra login

# 3. List your clusters
ankra cluster list

# 4. Select a cluster to work with
ankra cluster select

# 5. Start chatting with AI about your infrastructure
ankra chat "What's the status of my deployments?"
```

---

## Configuration

The CLI stores configuration in `~/.ankra.yaml`:

- **token**: Your API authentication token
- **base-url**: The Ankra platform URL (defaults to https://platform.ankra.app)

You can also use environment variables:

- `ANKRA_API_TOKEN`: Override the stored token
- `ANKRA_BASE_URL`: Override the base URL

---

**Full documentation:** https://docs.ankra.app/cli
