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

### Upgrading

Once installed, the CLI can update itself:

```bash
ankra upgrade                       # upgrade to the latest release
ankra upgrade --check               # report whether a newer release is available
ankra upgrade --version v0.2.5      # install an exact release (upgrade)
ankra upgrade --version 0.1.9 --yes # downgrade / roll back to an older release
```

Pin an exact release with `--version` (the leading `v` is optional) to upgrade
or downgrade: a pinned version is installed whether it is newer, older or the
same as the running binary, so the same command rolls back a bad release.
Unpinned `ankra upgrade` still refuses to reinstall when you are already up to
date or running a newer build (use `--force` to override those).

`ankra upgrade` downloads the matching `ankra-cli-<os>-<arch>` asset, verifies
it against the published SHA-256 checksum, and atomically replaces the running
binary. If a release publishes no checksum the upgrade aborts rather than
installing an unverified binary — pass `--allow-unverified` to override for
older releases. If the binary lives in a directory you cannot write (such as
`/usr/local/bin`), re-run with `sudo ankra upgrade`.

#### Beta (pre-release) channel

By default `ankra upgrade` installs stable `x.x.x` releases. Opt into
pre-release versions (release candidates) with the beta channel:

```bash
ankra config beta enable     # install pre-releases (e.g. v0.3.0-rc.1)
ankra config beta status     # show the current channel
ankra config beta disable    # back to stable only (default)
ankra upgrade --beta         # one-off override for a single run
```

The preference is stored in `~/.ankra/settings.json`.

#### Installing a beta / release-candidate version

The quick-install one-liner tracks the latest **stable** release, so it will not
pick up a pre-release. To install a beta (RC) build, either pin the version or
opt into the beta channel:

```bash
# Option 1: install a specific release candidate directly (pin its tag)
bash <(curl -sL https://github.com/ankraio/ankra-cli/releases/latest/download/install.sh) --version <pre-release-tag>

# Option 2: install stable, then switch to the beta channel
bash <(curl -sL https://github.com/ankraio/ankra-cli/releases/latest/download/install.sh)
ankra config beta enable
ankra upgrade
```

Switch back to stable any time with `ankra config beta disable`.

#### Deprecations

Commands scheduled for removal are tracked in [`DEPRECATIONS.md`](DEPRECATIONS.md),
including the version they are removed in and the replacement to use. Running a
deprecated command also prints a warning at runtime.


## Features

- **Organisation Management**
  - List, switch, and create organisations
  - View organisation members and roles
  - Invite users and remove members
  - Persistent organisation selection across sessions
  - Manage organisation-scoped template variables (`ankra org variables ...`)

- **Variables (org / cluster / stack scopes)**
  - CRUD for variables that get substituted into manifests and addon values
    at deploy time (`ankra org variables`, `ankra cluster variables`,
    `ankra cluster stacks variables`)
  - Stack > cluster > org resolution order — a more specific scope shadows
    less specific ones
  - Upsert semantics (`set` creates or updates), stdin value support,
    `-o json|yaml` for scripting

- **Cluster Management**
  - Switch context between clusters
  - Persistent cluster selection across sessions
  - Trigger cluster reconciliation
  - Provision and deprovision managed clusters
  - Delete clusters
  - Roll back to a specific resource version

- **Cluster Access (kube gateway)**
  - Grant organisation members scoped access to a cluster's Kubernetes API
    (`ankra cluster access grant <email> --role view|edit|admin|cluster-admin`)
  - Cluster-wide or single-namespace grants (`--namespace`)
  - List grants with their RBAC reconcile status (`ankra cluster access list`)
  - Revoke by grant ID, or by email to clear every grant a member has
    (`ankra cluster access revoke`)

- **AI-Powered Chat**
  - Interactive troubleshooting with AI assistance
  - Cluster-aware context for better answers
  - Chat history management
  - AI-analyzed cluster health insights

- **Agent Skills**
  - Install the curated Ankra Agent Skills into Cursor/Claude
  - Embedded in the binary, so installation works offline
  - Personal (`~/.cursor/skills/`) or project (`.cursor/skills/`) scope

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
  - Upgrade a manifest in-place from a file or stdin, or patch a single path with helm-style `--set` (e.g. a Deployment image tag), with `--target-kind`/`--target-name` to pick a document in multi-doc manifests (`ankra cluster manifests upgrade <name>`)
  - Edit dependency parents with `--add-parent`/`--remove-parent`/`--set-parent` (`ankra cluster manifests upgrade <name>`)
  - Print the current manifest YAML (`ankra cluster manifests get <name>`) or disconnect it from its stack (`ankra cluster manifests delete <name>`)

- **Addons**
  - List available and installed addons
  - View addon settings, and print current Helm values (`ankra cluster addons values <name>`)
  - Update addon settings from a JSON file
  - Upgrade an addon in-place: bump chart version, patch values with helm-style `--set` (including field selectors like `env[name=LOG_LEVEL]`), edit dependency parents, swap registry credentials, or change namespace (`ankra cluster addons upgrade <name>`)
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
  - Create, deprovision, stop, and start OVH Kubernetes clusters
  - Scale worker nodes up and down
  - Check and upgrade Kubernetes versions
  - Manage node groups (add with labels/taints, scale, upgrade, label, taint, delete)
  - Manage the control plane (count and instance type) and inspect nodes
  - View SSH access info and manage cluster SSH keys
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
  - Encrypt and decrypt values for live cluster manifests/addons directly
    (`ankra cluster encrypt manifest <name> --key <key> --cluster <name>`),
    or against a local `cluster.yaml` with `-f` for GitOps workflows
  - View SOPS configuration and public keys (`ankra cluster sops-config`)
  - Automatic `encrypted_paths` tracking in the PATCH or in `cluster.yaml`
  - Add new encrypted keys to already-encrypted manifests/addons

- **Shell Completion**
  - Generate completion scripts for bash, zsh, fish, and powershell
  - One-command install for the current shell

- **Help & Versioning**
  - `--help` on any command
  - `--version` to see CLI release & API compatibility

> **New to Ankra?** Start with our [platform overview](https://ankra.io) and [getting started guide](https://docs.ankra.ai/getting-started).


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

### Machine-readable output (`-o json|yaml`)

Every command that reads or returns data supports structured output via the
shared `-o/--output` flag, so scripts and AI agents never have to parse
tables or prose:

- `-o json` — print the result as indented JSON
- `-o yaml` — same data as YAML
- The kubectl-style commands default to `-o table` and accept `json`/`yaml`
  as alternatives (for example `ankra cluster get pods -o json`)

```bash
ankra cluster list -o json
ankra cluster operations list -o json
ankra cluster stacks list my-stack -o json
ankra cluster agent status -o json
ankra org list -o json
ankra cluster get pods -o json
```

Write commands (reconcile, provision, deprovision, scaling, node groups,
token creation, ...) also accept `-o json` and emit the API result — including
operation IDs — so automation can poll `ankra cluster operations list <id> -o json`
for completion. Asynchronous writes submitted without `--wait` emit
`{"submitted": true, ...}`.

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
ankra org variables list                                       # List org variables
ankra org variables get <name> [-o json|yaml]                  # Show one variable
ankra org variables set <name> <value> [--description <text>]  # Upsert (value "-" reads stdin)
ankra org variables delete <name> [--yes]                      # Delete one variable
```

Run a single command against a different organisation without switching, using
the global `--org` flag (organisation name or ID) or the `ANKRA_ORG`
environment variable:
```bash
ankra --org "Acme Corp" cluster list                 # By name, just for this command
ankra --org 2222...-2222 get pods my-cluster          # By ID
export ANKRA_ORG="Acme Corp"; ankra cluster list      # For the whole session
```
The override applies per request and never changes your selected organisation
(`ankra org switch`). You must be an active member of the target organisation.

#### Cluster Management
```bash
ankra cluster list                    # List all clusters
ankra cluster info [name]             # Show cluster details (defaults to selected cluster)
ankra cluster select [name]           # Select a cluster (interactive when no name)
ankra cluster clear                   # Clear active cluster selection
ankra cluster reconcile [name]        # Trigger cluster reconciliation
ankra cluster apply -f <file>         # Apply an ImportCluster YAML
  [--dry-run]                         #   Validate locally (structure, referenced-file YAML,
                                      #   and the parent/dependency tree) without calling the API
ankra cluster validate -f <file>      # Server-side validation (chart existence, plaintext
  [--strict-secrets]                  #   secrets, parent refs); treat secrets as errors
  [--cluster <cluster_id>]            #   validate against an existing cluster's resources
ankra cluster draft -f <file>         # Stage every stack as a reviewable draft (nothing
                                      #   deployed); imports the cluster first if it's new
ankra cluster clone <src> <dst>       # Clone cluster configuration
  [--clean] [--force]                 #   Replace all stacks / force merge on conflicts
  [--copy-missing]                    #   Copy missing files for skipped stacks
  [--stack <name>]                    #   Clone only specific stacks (repeatable)
ankra cluster provision [name]        # Provision (start) a managed cluster
ankra cluster deprovision [name]      # Deprovision (stop) a managed cluster
  [--auto-delete] [--force]
ankra cluster roll-to --version <id>  # Roll to a specific resource version
  [--cluster <name|id>]
ankra cluster k3s-versions            # List Kubernetes (k3s) versions available for upgrades
ankra cluster upgrade <id> <version>  # Upgrade the Kubernetes version of a cloud cluster
                                      #   (Hetzner/OVH/UpCloud detected automatically)
```

`ankra cluster ...` subcommands act on the selected cluster, but every one of
them also accepts a global `--cluster <name|id>` flag so you can target a
cluster for a single command **instead of** running `ankra cluster select`
first:
```bash
ankra cluster stacks list --cluster prod          # By name
ankra cluster operations list --cluster 2222...    # By ID
ankra cluster agent status --cluster staging
```
The flag takes precedence over the saved selection and accepts either a cluster
name or ID. For commands that also take a positional cluster name (for example
`ankra cluster info [name]` and `ankra cluster reconcile [name]`), an explicit
argument wins over `--cluster`, which in turn wins over the saved selection.
`ankra chat health` and `ankra openclaw skill | handoff` accept `--cluster` too.
(`ankra cluster scale | node-group | upgrade` always take the cluster id as a
required positional argument and do not use the selection.)

#### Cluster Access
```bash
ankra cluster access list             # List access grants and their RBAC reconcile status
ankra cluster access grant <email>    # Grant a member access through the kube gateway
  [--role <role>]                     #   view (default), edit, admin, or cluster-admin
  [--namespace <ns>]                  #   Limit the grant to one namespace (default: cluster-wide)
ankra cluster access revoke <target>  # Revoke by grant ID, or by email (every grant for that member)
                                      # All subcommands accept --cluster (defaults to selected cluster)
```

#### Delete Resources
```bash
ankra delete cluster <name>           # Delete a cluster by name
ankra delete cluster <name> -f        # Delete without confirmation
ankra delete cluster <name> --dry-run # Show what would be deleted without calling the API
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
ankra cluster stacks variables list <stack>             # List variables on a stack
ankra cluster stacks variables get <stack> <name>       # Print one variable value
ankra cluster stacks variables set <stack> <name> <value> [--dry-run]   # Upsert
ankra cluster stacks variables delete <stack> <name> [--yes]            # Delete one
```

#### Stack Profiles
```bash
ankra stack-profiles list                       # List org-level reusable stack profiles
  [--search <text>] [--page N] [--page-size N]
ankra stack-profiles get <profile-id>           # Show a profile's versions and parameters
  [--version N]                                  #   Describe a specific version (default: current)
ankra stack-profiles apply <profile-id>         # Apply a profile to a cluster as a draft
  [--cluster <name|id>]                          #   Target cluster (defaults to the selected cluster)
  [--version N]                                  #   Profile version (default: the profile's current version)
  [--stack-name <name>]                          #   Name for the new stack
  [--set <name=value>]                           #   Bind a parameter (repeatable; not for secrets)
  [--set-file <name=path>]                       #   Bind a parameter from a file (repeatable; secret-safe)
  [--set-env <name=ENV_VAR>]                     #   Bind a parameter from an env var (repeatable; secret-safe)
  [--deploy]                                     #   Deploy immediately instead of leaving a draft
ankra stack-profiles export-iac <profile-id>    # Export a version as ClusterInfrastructureAsCode YAML
  [--version N] [-o <file>]
ankra stack-profiles import <file>              # Create a profile from an IaC YAML file
  [--name <name>] [--category <category>]
```

By default `apply` creates a reviewable **draft** on the target cluster — nothing is deployed until you pass `--deploy` or deploy it from the dashboard. Run `ankra stack-profiles get <profile-id>` first to discover which parameters a profile expects. For **secret** parameters prefer `--set-file` or `--set-env` so values never land in your shell history or process list.

#### Cluster Variables
```bash
ankra cluster variables list [--cluster <name|id>]
ankra cluster variables get <name> [--cluster <name|id>] [-o json|yaml]
ankra cluster variables set <name> <value> [--description <text>] [--cluster <name|id>]   # Upsert (value "-" reads stdin)
ankra cluster variables delete <name> [--cluster <name|id>] [--yes]
```

Resolution at deploy time is **stack > cluster > organisation** — a more specific scope shadows less specific ones for the same variable name. Stack variables travel through the same partial-stack PATCH used by `manifests upgrade` / `addons upgrade`; org and cluster variables use dedicated bearer-token endpoints.

#### Cluster Addons
```bash
ankra cluster addons list [name]      # List addons or show details
ankra cluster addons available        # List addons available for installation
ankra cluster addons settings <name>  # Get addon settings
ankra cluster addons values <name>    # Print the addon's current Helm values (decoded)
  [-o raw]                            #   Emit the base64-encoded form instead
  [--cluster <name|id>]
ankra cluster addons update <name> -f <file>  # Update addon settings from JSON
ankra cluster addons upgrade <name>   # In-place upgrade against the partial-stack endpoint
  [--chart-version <version>]         #   Bump the chart version
  [--values-from-file <path>]         #   REPLACE the values document from a file
  [--values -]                        #   REPLACE the values document from stdin
  [--set key=value]                   #   MUTATE one Helm value (repeatable, helm-style)
  [--set-string key=value]            #   --set but always coerce to string
  [--set-file key=path]               #   --set with the value read from a file
                                      #   Paths address list items by index (foo[0])
                                      #   or by a field selector (env[name=LOG_LEVEL])
  [--add-parent name=<n>,kind=<k>]    #   Add a dependency parent (kind: manifest|addon)
  [--remove-parent name=<n>,kind=<k>] #   Remove a dependency parent
  [--set-parent name=<n>,kind=<k>]    #   Replace ALL parents (repeatable)
  [--registry-name <name>]            #   Atomically retag the addon's registry
  [--registry-url <url>]
  [--registry-credential-name <name>]
  [--namespace <ns>]                  #   Destructive: Helm reinstall in the new namespace
  [--yes]                             #   Skip the namespace-change confirmation prompt
  [--cluster <name|id>]               #   Target cluster (defaults to active selection)
  [--stack <stack>]                   #   Required when the addon exists in multiple stacks
  [--dry-run]                         #   Print before/after spec without writing
  [-o json|yaml]                      #   Machine-readable output for CI
ankra cluster addons uninstall <name> # Uninstall an addon
  [--delete]                          #   Also delete the addon permanently
```

Examples:

```bash
# Bump the chart version
ankra cluster addons upgrade ankra-website \
  --chart-version 1.0.146 \
  --cluster website-demo

# Mutate one Helm values field (CLI fetches existing values and patches them)
ankra cluster addons upgrade website \
  --set image.tag=1.0.146 \
  --cluster website-demo

# Address a list item by a field instead of a fragile numeric index
ankra cluster addons upgrade website \
  --set 'env[name=LOG_LEVEL].value=debug' \
  --cluster website-demo

# Make the addon depend on a namespace manifest (deployment ordering)
ankra cluster addons upgrade infisical \
  --add-parent name=infisical-ns,kind=manifest \
  --cluster website-demo

# Read the current values, edit, and re-apply
ankra cluster addons values website > values.yaml
ankra cluster addons upgrade website --values-from-file values.yaml --cluster website-demo
```

> Changing `--namespace` on an addon is **destructive**: Helm reinstalls the chart in the new namespace and leaves the old release orphaned. Use `--yes` to skip the interactive confirmation in CI.

#### Cluster Operations (Executions)
```bash
ankra cluster operations list [id]            # List executions or show details
  [--status failed --status critical]         #   Filter by status (repeatable)
  [--failed]                                  #   Shortcut for failed + critical
  [--limit 50]                                #   Page size (max 100)
  [--watch | -w]                              #   Poll until all executions reach a terminal state
  [--interval 5s]                             #   Poll interval used with --watch
  [-o json|yaml]                              #   Machine-readable output for CI
ankra cluster operations cancel <id> [<id>...]    # Cancel one or more executions
ankra cluster operations cancel-step <exec_id> <step_id>  # Cancel a single step
ankra cluster operations retry <exec_id>      # Retry a terminal execution
ankra cluster operations steps <exec_id>      # List steps for an execution
  [-o json|yaml]                              #   Machine-readable output for CI
```

`ankra cluster executions ...` is an alias for `ankra cluster operations ...` and uses the canonical `/api/v1/org/executions` endpoints.

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
ankra cluster manifests get <name>    # Print the manifest's current YAML (decoded)
  [-o raw]                            #   Emit the base64-encoded form instead
  [--cluster <name|id>]
ankra cluster manifests upgrade <name>  # In-place upgrade against the partial-stack endpoint
  [--from-file <path>]                #   REPLACE manifest content from a file
  [--manifest -]                      #   REPLACE manifest content from stdin
  [--set key=value]                   #   MUTATE one path in the manifest YAML (repeatable)
  [--set-string key=value]            #   --set but always coerce to string
  [--set-file key=path]               #   --set with the value read from a file
                                      #   Paths address list items by index (containers[0])
                                      #   or by a field selector (containers[name=app])
  [--target-kind <kind>]              #   With --set: pick the document by kind (multi-doc)
  [--target-name <name>]              #   With --set: pick the document by metadata.name
  [--add-parent name=<n>,kind=<k>]    #   Add a dependency parent (kind: manifest|addon)
  [--remove-parent name=<n>,kind=<k>] #   Remove a dependency parent
  [--set-parent name=<n>,kind=<k>]    #   Replace ALL parents (repeatable)
  [--namespace <ns>]                  #   Change the manifest's namespace
  [--cluster <name|id>]               #   Target cluster (defaults to active selection)
  [--dry-run]                         #   Print before/after spec without writing
  [-o json|yaml]                      #   Machine-readable output for CI
ankra cluster manifests delete <name> # Disconnect a manifest from its stack
  [--yes]                             #   Skip the confirmation prompt
  [--dry-run]                         #   Print the target without disconnecting
  [--cluster <name|id>]
```

Examples:

```bash
# Bump a Deployment's image tag in place (CLI fetches the manifest and patches it)
ankra cluster manifests upgrade web \
  --set 'spec.template.spec.containers[name=app].image=nginx:1.27' \
  --cluster website-demo

# Select which document to edit when the manifest holds several
ankra cluster manifests upgrade web \
  --target-kind Deployment --target-name web \
  --set 'spec.replicas=3' \
  --cluster website-demo

# Edit dependency parents (deployment ordering)
ankra cluster manifests upgrade web \
  --add-parent name=infisical-ns,kind=manifest \
  --cluster website-demo

# Export, delete (disconnect) a manifest
ankra cluster manifests get web > web.yaml
ankra cluster manifests delete web --cluster website-demo
```

Manifest names are unique across a cluster (a manifest belongs to exactly one stack), so unlike addons there is no `--stack` flag here.

`--set*` MUTATE the existing manifest YAML and are mutually exclusive with `--from-file` / `--manifest -`, which REPLACE the whole manifest. List items can be addressed by numeric index (`containers[0]`) or, more robustly, by a field selector (`containers[name=app]`); a selector that matches nothing fails loudly rather than guessing. `--target-kind` / `--target-name` are only needed when the manifest contains more than one Kubernetes document.

When neither `--from-file`, `--manifest`, nor `--set*` is supplied, the CLI fetches the existing content from the cluster and re-sends it (the backend requires `manifest_base64` on every patch).

Parent flags (`--add-parent` / `--remove-parent` / `--set-parent`) edit a resource's dependency parents, which control deployment ordering inside a stack. Each value is `name=<name>,kind=<manifest|addon>` (kind defaults to `manifest`). `--set-parent` replaces the list wholesale and cannot be combined with `--add-parent`/`--remove-parent`; removing the last parent clears the link. `ankra cluster manifests delete <name>` disconnects a manifest from its stack, removing its resources from the cluster (the owning stack is resolved automatically).

#### SOPS Encryption
```bash
ankra cluster sops-config                                                  # Show SOPS configuration and public key

# Cluster mode (operate directly on a live cluster; defaults to the selected cluster)
ankra cluster encrypt manifest <name> --key <key> [--cluster <name|id>]    # Encrypt a key in a live manifest
ankra cluster encrypt addon --name <addon> --key <key> [--cluster <name|id>] [--stack <stack>]  # Encrypt a key in a live addon's values
ankra cluster decrypt manifest <name> [--cluster <name|id>]                # Print a decrypted manifest
ankra cluster decrypt addon --name <addon> [--cluster <name|id>] [--stack <stack>]              # Print decrypted addon values

# File mode (GitOps workflows — rewrites the local cluster.yaml in place)
ankra cluster encrypt manifest <name> --key <key> -f <file>
ankra cluster encrypt addon --name <addon> --key <key> -f <file>
ankra cluster decrypt manifest <name> -f <file>
ankra cluster decrypt addon --name <addon> -f <file>
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
  --external-cloud-provider            #   Install Hetzner CCM + CSI (default: true; --external-cloud-provider=false also disables --include-networking)
  --include-networking                 #   Install Traefik + cert-manager (default: true; requires --external-cloud-provider; use --include-networking=false to skip)
  --gitops-credential-name <name>      #   GitHub credential for GitOps (optional; requires --gitops-repository)
  --gitops-repository <org/repo>       #   Git repo to commit the generated hcloud stack to (optional)
  --gitops-branch <branch>             #   GitOps branch (default: master)
ankra cluster hetzner deprovision <id>    # Deprovision a Hetzner cluster (deprecated: use `ankra cluster deprovision`)
ankra cluster hetzner workers <id>       # Get current worker count
ankra cluster hetzner scale <id> <n>     # Scale workers to n (deprecated: use `ankra cluster scale`)
ankra cluster hetzner k8s-version <id>   # Get current Kubernetes version
ankra cluster hetzner upgrade <id> <ver> # Upgrade Kubernetes version (deprecated: use `ankra cluster upgrade`)

# node-group commands are deprecated: use `ankra cluster node-group <list|add|scale|upgrade|delete>` (provider auto-detected)
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
  --external-cloud-provider               #   Install OpenStack CCM + Cinder CSI (default: true; --external-cloud-provider=false also disables --include-networking)
  --include-networking                    #   Install Traefik + cert-manager (default: true; requires --external-cloud-provider; use --include-networking=false to skip)
  --gitops-credential-name <name>         #   GitHub credential for GitOps (optional; requires --gitops-repository)
  --gitops-repository <org/repo>          #   Git repo to commit the generated ovh-cloud stack to (optional)
  --gitops-branch <branch>                #   GitOps branch (default: master)
ankra cluster ovh deprovision <id>        # Deprovision an OVH cluster (deprecated: use `ankra cluster deprovision`)
ankra cluster ovh stop <id>               # Stop an OVH cluster (keeps configuration)
ankra cluster ovh start <id> [--scope all|control_plane]  # Start a stopped OVH cluster
ankra cluster ovh workers <id>            # Get current worker count
ankra cluster ovh scale <id> <n>          # Scale workers to n (deprecated: use `ankra cluster scale`)
ankra cluster ovh k8s-version <id>        # Get current Kubernetes version
ankra cluster ovh upgrade <id> <version>  # Upgrade Kubernetes version (deprecated: use `ankra cluster upgrade`)
ankra cluster ovh regions --credential-id <id>  # List regions the credential can deploy in
ankra cluster ovh access-info <id>        # Show gateway/control-plane IPs and SSH commands
ankra cluster ovh ssh-keys get <id>       # Show SSH keys attached to the cluster
ankra cluster ovh ssh-keys set <id> --ssh-key-credential-ids <id>,...  # Replace attached SSH keys

ankra cluster ovh control-plane get <id>                  # Show control plane configuration
ankra cluster ovh control-plane set-count <id> <count>    # Change control plane count (1 or 3)
ankra cluster ovh control-plane set-instance-type <id> <type>  # Change control plane instance type
ankra cluster ovh nodes list <id>                         # List cluster nodes
ankra cluster ovh nodes get <id> <node_id>                # Show node details

# node-group list/add/scale/upgrade/delete are deprecated: use `ankra cluster node-group ...` (provider auto-detected); labels/taints remain OVH-specific
ankra cluster ovh node-group list <id>                    # List node groups
ankra cluster ovh node-group add <id>                     # Add a node group
  --name <name> [--instance-type <type>] [--count <n>] [--labels k=v,...] [--taints k=v:Effect,...]
ankra cluster ovh node-group scale <id> <group> <n>       # Scale a node group
ankra cluster ovh node-group upgrade <id> <group> <type>  # Upgrade instance type
ankra cluster ovh node-group labels <id> <group> --labels k=v,...      # Set node labels
ankra cluster ovh node-group taints <id> <group> --taints k=v:Effect,...  # Set node taints
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
  --external-cloud-provider               #   Install UpCloud CCM + CSI (default: true; --external-cloud-provider=false also disables --include-networking)
  --include-networking                    #   Install Traefik + cert-manager (default: true; requires --external-cloud-provider; use --include-networking=false to skip)
  --gitops-credential-name <name>         #   GitHub credential for GitOps (optional; requires --gitops-repository)
  --gitops-repository <org/repo>          #   Git repo to commit the generated upcloud-cloud-provider stack to (optional)
  --gitops-branch <branch>                #   GitOps branch (default: master)
ankra cluster upcloud deprovision <id>    # Deprovision an UpCloud cluster (deprecated: use `ankra cluster deprovision`)
ankra cluster upcloud stop <id>           # Stop an UpCloud cluster (keeps configuration)
ankra cluster upcloud start <id> [--scope all|control_plane]  # Start a stopped UpCloud cluster
ankra cluster upcloud workers <id>        # Get current worker count
ankra cluster upcloud scale <id> <n>      # Scale workers to n (deprecated: use `ankra cluster scale`)
ankra cluster upcloud k8s-version <id>    # Get current Kubernetes version
ankra cluster upcloud upgrade <id> <ver>  # Upgrade Kubernetes version (deprecated: use `ankra cluster upgrade`)

# node-group commands are deprecated: use `ankra cluster node-group <list|add|scale|upgrade|delete>` (provider auto-detected)
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

#### Self-Update
```bash
ankra upgrade                         # Upgrade to the latest release
ankra upgrade --check                 # Report whether a newer release exists
ankra upgrade --version <tag> [--yes] # Install a specific release tag
ankra upgrade --force                 # Reinstall even if already current
ankra upgrade --beta                  # Include pre-releases for this run
```

#### Settings
```bash
ankra config beta enable              # Opt into pre-release (beta) versions
ankra config beta disable             # Use stable releases only (default)
ankra config beta status              # Show the current update channel
```

#### Shell Completion
```bash
ankra completion bash                 # Print bash completion script
ankra completion zsh                  # Print zsh completion script
ankra completion fish                 # Print fish completion script
ankra completion powershell           # Print powershell completion script
ankra completion install [--shell]    # Install completions for current shell
```

#### Agent Skills

Install the curated [Ankra Agent Skills](https://github.com/ankraio/ankra-skills) so your
Cursor/Claude agent follows Ankra's recommended practices. The skills are embedded in the
CLI, so installation works offline.

```bash
ankra skills list                     # List available skills (marks installed ones)
ankra skills install                  # Install all skills into ~/.cursor/skills (personal)
ankra skills install --project .      # Install into ./.cursor/skills (project)
ankra skills install ankra-cli ankra-gitops   # Install only named skills
ankra skills install --force          # Overwrite existing skills
ankra skills uninstall                # Remove all Ankra skills
ankra skills uninstall ankra-cli      # Remove a named skill
```

This is distinct from `ankra openclaw skill`, which generates a SKILL.md describing one of
your clusters. `ankra skills` installs the static, curated skill set.

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

# Investigate executions and failures
ankra cluster operations list --failed
ankra cluster operations steps <execution_id>
ankra cluster operations retry <execution_id>

# Update addon settings from a file
ankra cluster addons update my-addon -f settings.json

# View SOPS encryption config
ankra cluster sops-config

# Encrypt sensitive values on a live cluster (no cluster.yaml needed)
# --key takes the YAML key name (SOPS matches key names, not dotted paths)
ankra cluster encrypt manifest my-secret --key password
ankra cluster encrypt addon --name grafana --key adminPassword --cluster prod

# Encrypt sensitive values in a local cluster.yaml (GitOps workflow)
ankra cluster encrypt manifest my-secret --key DB_PASSWORD -f cluster.yaml
ankra cluster encrypt addon --name grafana --key adminPassword -f cluster.yaml

# View decrypted content (cluster or file mode)
ankra cluster decrypt manifest my-secret
ankra cluster decrypt addon --name grafana --cluster prod
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
- Visit our [documentation](https://docs.ankra.ai) for detailed guides
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
- **Documentation**: [docs.ankra.ai](https://docs.ankra.ai)
- **Blog & Tutorials**: [blog.ankra.io](https://blog.ankra.io)
- **Community**: [community.ankra.io](https://community.ankra.io)

## Support

- Issues: https://github.com/ankraio/ankra-cli/issues
- Documentation: [docs.ankra.ai](https://docs.ankra.ai)
- Community Slack: [community.ankra.io](https://community.ankra.io)
- Email: hello@ankra.io
