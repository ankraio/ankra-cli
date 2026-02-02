# Ankra CLI Changelog

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
