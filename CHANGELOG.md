# Ankra CLI Changelog

## v0.3.0-rc2 — June 2026

Third release candidate for the v0.3.0 line. It carries everything in
**v0.3.0-rc1** and adds OVH region discovery plus node label/taint management to
the OVH command set. Install it with `ankra upgrade --beta` (beta channel) or
download the `v0.3.0-rc2` release-candidate asset directly.

**New in rc2 (since rc1):**

- **`ankra cluster ovh regions --credential-id <id>`** — list the OVH Cloud
  regions a credential's project can actually deploy in, so you pick a valid
  `--region` for `ankra cluster ovh create` instead of guessing (a region that
  is not enabled on the project fails the reconcile at private-network setup).
- **`ankra cluster ovh node-group labels <cluster_id> <group> --labels k=v,...`**
  and **`ankra cluster ovh node-group taints <cluster_id> <group> --taints
  k=v:Effect,...`** — set Kubernetes labels and taints on every node in an OVH
  node group from the CLI (empty value clears them; taint effect defaults to
  `NoSchedule`).

## v0.3.0-rc1 — June 2026

Second release candidate for the v0.3.0 line. It bundles everything previewed in
**v0.3.0-rc0** and adds a draft/validate workflow plus a more capable
self-updater on top. Install it with `ankra upgrade --beta` (beta channel) or
download the `v0.3.0-rc1` release-candidate asset directly.

**New in rc1 (since rc0):**

- **`ankra cluster draft`** — stage every stack in an `ImportCluster` as a
  reviewable draft instead of applying it.
- **`ankra cluster validate`** — the offline `apply --dry-run` checks plus
  server-side chart-existence, plaintext-secret, and parent-reference
  validation; CI-friendly exit codes and `--strict-secrets`.
- **`ankra upgrade` pinning, downgrade & rollback** — `--version` installs an
  exact release (newer *or* older), with SHA-256 checksum enforcement and
  `--allow-unverified` for releases that predate published checksums.

**Carried over from v0.3.0-rc0:**

- **`ankra upgrade`** self-update (download the latest release, verify SHA-256,
  atomically swap the binary) and the **beta / pre-release update channel**
  (`ankra config beta enable|disable|status`, `ankra upgrade --beta`) with
  semver-aware version comparison.
- **Offline dependency-tree and referenced-file validation** in
  `ankra cluster apply`, and **`--dry-run`** for `apply` / `delete cluster`
  (fully offline, no token).
- **`--watch` and `-o json|yaml`** for `ankra cluster operations`.
- **Credential and organisation fixes**: `ankra credentials get` resolves a
  name to an ID (trying the v2 platform-credential lookup before the legacy
  table); `ankra org members` / `current` honor `--org` and validate the saved
  selection instead of sending a stale value.

### New Features

#### Stage changes as drafts with `ankra cluster draft`

`ankra cluster draft -f cluster.yaml` stages every stack in an ImportCluster YAML as a reviewable draft instead of applying it. The local checks run first (the same as `ankra cluster apply --dry-run`), then each stack is saved as a resource draft you can review, edit, and deploy from the Ankra stack builder — nothing is deployed by the command itself.

If the cluster does not exist yet it is imported first (live), since drafts can only be attached to an existing cluster. Stacks that already match the cluster's desired state are reported as `no changes` rather than creating an empty draft. The command exits non-zero if any stack fails validation.

```bash
ankra cluster draft -f cluster.yaml
```

#### Server-side validation with `ankra cluster validate`

`ankra cluster validate -f cluster.yaml` runs the same offline checks as `ankra cluster apply --dry-run` (structure, referenced-file YAML, parent/dependency tree) and then sends the spec to the Ankra API for the checks that need server-side data — checks the offline path cannot perform:

- **chart existence** in the Helm registries connected to your organisation,
- **plaintext secret detection** for Kubernetes `Secret` manifests and addon values that are not SOPS-encrypted,
- **parent references** resolved against an existing cluster's deployed resources (with `--cluster <id>`).

Nothing is applied. Warnings (e.g. plaintext secrets) are printed but do not fail the command; pass `--strict-secrets` to treat plaintext secrets as errors. The command exits non-zero when validation finds errors, so it drops straight into CI.

```bash
ankra cluster validate -f cluster.yaml
ankra cluster validate -f cluster.yaml --strict-secrets
ankra cluster validate -f cluster.yaml --cluster <cluster_id>
```

#### Self-update with `ankra upgrade`

`ankra upgrade` downloads and installs the latest Ankra CLI release, replacing
the running binary in place. It resolves the latest release tag from GitHub
(or installs a pinned `--version v0.2.5`), downloads the matching
`ankra-cli-<os>-<arch>` asset, verifies it against the published SHA-256
checksum, and atomically swaps the executable. The command needs no API token.

Pin an exact release with `--version` (with or without the leading `v`) to
upgrade *or* downgrade — a pinned version installs whether it is newer, older
or the same as the running binary, so it doubles as a rollback. Only an
unpinned `ankra upgrade` keeps the "already up to date" / "installed version is
newer" safety checks; pinning is treated as explicit intent and asks for a
single confirmation (`Upgrade` / `Downgrade` / `Reinstall`).

If a release does not publish a checksum, the upgrade fails closed rather than
installing an unverified binary; pass `--allow-unverified` to override that for
older releases that predate published checksums.

```bash
ankra upgrade                       # upgrade to the latest release
ankra upgrade --check               # report whether a newer release is available
ankra upgrade --version v0.2.5      # install an exact release (upgrade)
ankra upgrade --version 0.1.9 --yes # downgrade/roll back, no confirmation prompt
ankra upgrade --version v0.1.0 --allow-unverified  # release without a checksum
```

If the installed binary lives in a directory the current user cannot write
(for example `/usr/local/bin`), the command prints a clear message pointing to
`sudo ankra upgrade` or the install script.

#### Beta (pre-release) update channel

`ankra config beta enable` opts the CLI into pre-release versions. When the
beta channel is enabled, `ankra upgrade` resolves the newest release
*including* release candidates (for example `v0.3.0-rc.1`); when disabled (the
default) only stable `x.x.x` releases are installed. The preference is stored
in `~/.ankra/settings.json`, separately from credentials.

```bash
ankra config beta enable     # opt into pre-releases
ankra config beta status     # show the current channel
ankra config beta disable    # back to stable only (default)
ankra upgrade --beta         # one-off: include pre-releases for this run
```

Version comparison now follows semantic-versioning precedence, so a stable
release outranks its release candidates (`v0.3.0` > `v0.3.0-rc.2` > `v0.3.0-rc.1`).

#### Offline dependency-tree validation in `ankra cluster apply`

`ankra cluster apply` now validates the parent (`parents:`) graph of the
assembled `ImportCluster` document before it is sent to the API, in addition to
the existing structural and `from_file` checks. The validation enforces that
resource names are unique per kind across the whole document (parents resolve by
`kind`+`name` with no stack qualifier, so a duplicate is ambiguous), that every
parent reference uses a valid `kind` (`manifest` or `addon`), names a resource
declared somewhere in the document (cross-stack references allowed), and that
the resulting graph is acyclic. This catches dependency errors locally that the
backend would otherwise only reject at apply time (HTTP 422).

It runs for both real applies and `--dry-run`, so you can lint a `cluster.yaml`
end-to-end without a token or network:

```bash
ankra cluster apply -f cluster.yaml --dry-run
# Invalid ImportCluster in "cluster.yaml":
#   dependency cycle detected: addon "a" -> addon "b" -> addon "a"
```

#### Referenced-file YAML validation in `ankra cluster apply`

Every file reference in the document is now resolved and validated, regardless
of whether its content is ultimately used. Manifest content (`manifest` inline
or `from_file`, including multi-document files) and addon values
(`configuration.values` inline or `configuration.from_file`) are parsed to
confirm valid YAML; `stack.description_from_file` is resolved and read for
existence even when an inline `description` is also set (previously the file
reference was silently skipped in that case). Errors name the resolved file and
the problem:

```bash
ankra cluster apply -f cluster.yaml --dry-run
# Invalid ImportCluster in "cluster.yaml":
#   stack "logging": manifest "broken": the file referenced by 'from_file' ("/abs/path/broken.yaml") is not valid YAML: ...
```

#### `--dry-run` for `ankra cluster apply` and `ankra delete cluster`

`ankra cluster apply --dry-run` runs the structural, referenced-file, and
dependency-tree validation above and then exits without contacting the API.
`ankra delete cluster --dry-run` reports the cluster it would delete without
calling the API. Both dry-run modes are fully offline and no longer require a
token, so they can run in pre-merge CI without credentials. (Dry-run modes that
still query live cluster state, such as `cluster addons upgrade --dry-run`,
continue to require authentication.)

#### Watch and machine-readable output for `ankra cluster operations`

`ankra cluster operations list` gains `--watch`/`-w` to continuously poll and
refresh until every execution reaches a terminal state, with a configurable
`--interval` (default `5s`, floored at `1s`). Both `operations list` and
`operations steps` gain `-o json|yaml` for machine-readable output in CI.
`--watch` cannot be combined with `-o` (structured output is rendered once).

```bash
ankra cluster operations list --watch --interval 10s
ankra cluster operations steps <execution_id> -o json
```

## v0.2.4 — May 2026

### New Features

#### Variables CRUD at Organisation, Cluster, and Stack Scopes

`ankra org variables` and `ankra cluster variables` are new top-level command
groups for managing template variables that get substituted into stack
manifests and addon values at deploy time. Stack-scoped variables are managed
via `ankra cluster stacks variables`. All three scopes have the same UX:

```bash
# Organisation (available to every cluster)
ankra org variables list
ankra org variables set DB_HOST db.example.com --description "Primary DB"
ankra org variables get DB_HOST
ankra org variables delete DB_HOST

# Cluster (shadows org variables on that cluster)
ankra cluster variables list --cluster prod
ankra cluster variables set DB_HOST db.prod.example.com

# Stack (most specific; shadows cluster + org variables on that stack)
ankra cluster stacks variables list demo-web-app
ankra cluster stacks variables set demo-web-app FEATURE_FLAG enabled
```

`set` is an upsert: it creates the variable, or updates it if a variable with
the same name already exists. The value can also be read from stdin with `-`
(useful for piping secrets from a vault or `pass`). All `list`/`get` commands
support `-o json|yaml` for scripting. `delete` prompts for confirmation
(`--yes` to skip).

Org and cluster variables are exposed on new bearer-token endpoints
(`/api/v1/org/variables` and `/api/v1/org/clusters/imported/{id}/variables`)
that wrap the existing usecases; stack variables travel through the same
partial-stack PATCH used by `manifests upgrade` / `addons upgrade`.

#### Encrypt and Decrypt Live Cluster Resources with SOPS

`ankra cluster encrypt` and `ankra cluster decrypt` can now operate directly on
manifests and addons stored on a live cluster, without needing a local
`cluster.yaml`. They mirror the partial-stack PATCH flow used by
`manifests upgrade` / `addons upgrade`: fetch the current content, call the
SOPS API to encrypt/decrypt, and (for encrypt) push the result back with
`encrypted_paths` updated.

```bash
# Encrypt a key in a live manifest on the selected cluster
ankra cluster encrypt manifest db-secret --key data.password

# Encrypt a key in a live addon's values, with an explicit cluster + stack
ankra cluster encrypt addon --name grafana --key adminPassword \
  --cluster prod --stack monitoring

# Print decrypted content from a live cluster
ankra cluster decrypt manifest db-secret
ankra cluster decrypt addon --name grafana --cluster prod
```

The existing `-f <cluster.yaml>` file mode is unchanged and remains for GitOps
workflows where the source of truth lives on disk. The two modes are mutually
exclusive; cluster mode is the default when no `-f` is given. A new
`decrypt addon` subcommand brings the addon variant to parity with the manifest
variant.

#### Install Ankra Agent Skills

`ankra skills` installs the curated Ankra Agent Skills (for Cursor / Claude / OpenClaw)
into a skills directory. The skills are embedded in the CLI binary, so installation works
offline and is versioned with the release.

```bash
ankra skills list                  # list available skills, marking installed ones
ankra skills install               # install all into ~/.cursor/skills (personal)
ankra skills install --project .   # install into ./.cursor/skills (project)
ankra skills install ankra-gitops  # install only named skills
ankra skills uninstall             # remove all Ankra skills
```

Use `--force` to overwrite existing skills and `--source <dir>` to install from a local
skills directory instead of the embedded copy. This is separate from `ankra openclaw skill`,
which generates a per-cluster SKILL.md.

#### Manage Dependency Parents from the CLI

`ankra cluster addons upgrade` and `ankra cluster manifests upgrade` now accept
`--add-parent`, `--remove-parent`, and `--set-parent` flags to edit a resource's
dependency parents (which control deployment ordering inside a stack) without
re-applying the whole `cluster.yaml`. Parents are given as
`name=<name>,kind=<manifest|addon>` (kind defaults to `manifest`).

```bash
# Make an addon wait for a namespace manifest
ankra cluster addons upgrade infisical \
  --add-parent name=infisical-ns,kind=manifest \
  --cluster website-demo

# Replace all parents at once
ankra cluster manifests upgrade web \
  --set-parent name=infisical-ns,kind=manifest \
  --set-parent name=infisical,kind=addon \
  --cluster website-demo

# Remove a parent (removing the last one clears the link)
ankra cluster manifests upgrade web \
  --remove-parent name=infisical-ns,kind=manifest \
  --cluster website-demo
```

`--set-parent` replaces the list wholesale and is mutually exclusive with
`--add-parent` / `--remove-parent`.

#### Read and Delete Manifests and Addon Values

Two new read commands print the current stored content, ready to pipe to a file
or edit and re-apply:

```bash
ankra cluster addons values website > values.yaml
ankra cluster manifests get web > web.yaml
```

Both support `-o raw` to emit the base64-encoded form. A new
`ankra cluster manifests delete <name>` command disconnects a manifest from its
stack (removing its resources from the cluster); the owning stack is resolved
automatically and a confirmation prompt protects the operation (skip with
`--yes`, preview with `--dry-run`).

#### Patch a Manifest In-Place with `--set`

`ankra cluster manifests upgrade` now accepts helm-style `--set`, `--set-string`,
and `--set-file` flags to mutate a single path inside a manifest's Kubernetes
YAML, instead of only replacing the whole file. This makes it easy to bump, for
example, a Deployment image tag from CI.

```bash
# Bump a Deployment's image tag in place
ankra cluster manifests upgrade web \
  --set 'spec.template.spec.containers[name=app].image=nginx:1.27' \
  --cluster website-demo

# Pick a document when the manifest holds several
ankra cluster manifests upgrade web \
  --target-kind Deployment --target-name web \
  --set 'spec.replicas=3' \
  --cluster website-demo
```

`--set*` MUTATE the existing manifest and are mutually exclusive with
`--from-file` / `--manifest -`, which REPLACE it. When a manifest contains more
than one document, use `--target-kind` / `--target-name` to choose which one to
edit.

#### Address List Items by Field with `--set` Selectors

Both `manifests upgrade` and `addons upgrade` `--set` paths can now address a
list item by a stable field instead of a fragile numeric index. For example,
`containers[name=app].image` targets the container named `app`, and
`env[name=LOG_LEVEL].value` targets that environment entry. A selector that
matches nothing fails with a clear error rather than silently creating an entry.
Numeric indexes (`containers[0]`) continue to work.

#### Run Commands Against a Specific Organisation (`--org`)

A new global `--org` flag (or the `ANKRA_ORG` environment variable) runs a
single command against any organisation you belong to, without changing your
selected organisation. The value accepts an organisation name or ID.

```bash
# Run against another organisation by name, just for this command
ankra --org "Acme Corp" cluster list

# Or by ID
ankra --org 22222222-2222-2222-2222-222222222222 get pods my-cluster

# Scope a whole shell session via the environment
export ANKRA_ORG="Acme Corp"
ankra cluster list
```

The override is per-request: it does not call `ankra org switch` and leaves your
persistently selected organisation untouched. You must be an active member of the
requested organisation, otherwise the API returns a permission error.

#### Control Plane Management

Inspect and change the control plane of a stopped cluster, without going through
the dashboard.

```bash
# Show the current configuration
ankra cluster hetzner control-plane get <cluster_id>

# Switch between 1 and 3 controllers (etcd quorum: only 1 or 3 is allowed)
ankra cluster hetzner control-plane set-count <cluster_id> 3

# Change the controller instance type
ankra cluster hetzner control-plane set-instance-type <cluster_id> cx33
```

The same commands are available for OVH (`ankra cluster ovh control-plane …`)
and UpCloud (`ankra cluster upcloud control-plane …`). The cluster must be
stopped; changes apply the next time you start the cluster.

#### Cluster Nodes Listing

List every server Ankra manages for the cluster (control plane, workers, and
bastion or gateway), or drill into one for full spec and metadata. Soft-deleted
entries from a stopped cluster are listed too, so the saved topology is visible
before re-provisioning.

```bash
ankra cluster hetzner nodes list <cluster_id>
ankra cluster hetzner nodes list <cluster_id> --json
ankra cluster hetzner nodes get <cluster_id> <node_id>
ankra cluster hetzner nodes get <cluster_id> <node_id> --json
```

Available for all providers (`hetzner`, `ovh`, `upcloud`).

#### Surgical Addon and Manifest Upgrades

Two new subcommands for in-place updates against the existing partial-stack endpoint — no more hand-editing the full `ImportCluster.yaml`.

##### Bump an addon's chart version

```bash
ankra cluster addons upgrade ankra-website \
  --chart-version 1.0.146 \
  --cluster website-demo
```

##### Tweak a single Helm values field with `--set` (helm-style)

```bash
ankra cluster addons upgrade website \
  --set image.tag=1.0.146 \
  --cluster website-demo
```

`--set` accepts comma-separated dotted paths with array indexing (`ingress.hosts[0].host=demo.ankra.io`).

> `--set` vs `--set-string`: `--set image.tag=1.0.146` keeps the value a string because `1.0.146` is not a valid number. `--set image.tag=2.0` would coerce to the float `2.0`, which Helm renders as `2`. When the value is a valid number/bool but you want it to stay a string, use `--set-string image.tag=2.0`. `--set-file key=path` reads file contents as the value (useful for certs or configmap blobs).

##### Replace the whole values document

```bash
ankra cluster addons upgrade website \
  --values-from-file ./values.yaml \
  --cluster website-demo
```

`--set*` and `--values-from-file` are mutually exclusive: `--set*` mutates the existing document while `--values-from-file` replaces it.

##### Update a manifest

```bash
ankra cluster manifests upgrade demo-namespace \
  --from-file manifests/demo-namespace.yaml \
  --cluster website-demo
```

##### Common options

- `--cluster <name|id>` — defaults to the selected cluster.
- `--stack <name>` — addons only, required when the same addon name exists in multiple stacks. Manifest names are globally unique on a cluster, so `manifests upgrade` has no `--stack` flag.
- `--registry-name`, `--registry-url`, `--registry-credential-name` — atomically retag the addon's registry.
- `--namespace` — destructive for addons (Helm reinstall); requires `--yes` or interactive confirmation.
- `--dry-run` — print the before/after YAML; no API write.
- `-o json|yaml` — machine-readable output for CI scripts.

All upgrades go through the same partial-stack endpoint as the UI, so they are atomic, locked, and produce a single git commit per invocation when gitops is enabled.

### API Endpoints

- `GET /api/v1/clusters/{provider}/{id}/control-plane` — read controller count, instance type and editability
- `PUT /api/v1/clusters/{provider}/{id}/control-plane` — change controller count (1 or 3)
- `PUT /api/v1/clusters/{provider}/{id}/control-plane/instance-type` — change controller instance type
- `GET /api/v1/clusters/{provider}/{id}/nodes` — list all managed servers for the cluster
- `GET /api/v1/clusters/{provider}/{id}/nodes/{node_id}` — full spec and metadata for a node

### Deprecations

- `ankra chat` currently uses the bearer-token streaming endpoints
  `/api/v1/chat/general` and `/api/v1/org/clusters/{cluster_id}/kubernetes/chat`.
  These are now deprecated and will be removed in a future release; the platform
  now responds with `Deprecation: true` and a `Sunset` header on these routes.
  When the warning prints, upgrade `ankra-cli` to the next release once a
  resumable session-based replacement has shipped on the platform.

## v0.1.129 — April 2026

### New Features

#### Node Group Management

Full CRUD for node groups on Hetzner, OVH, and UpCloud clusters. Each node group has its own instance type, node count, Kubernetes labels, and taints.

##### List Node Groups

```bash
ankra cluster hetzner node-group list <cluster_id>
```

Example output:

```
default              type=cx33     count=2  labels=0  taints=0
gpu-workers          type=ccx33    count=3  labels=1  taints=1
```

##### Add a Node Group

```bash
ankra cluster hetzner node-group add <cluster_id> \
  --name gpu-workers \
  --instance-type ccx33 \
  --count 3
```

##### Scale a Node Group

```bash
ankra cluster hetzner node-group scale <cluster_id> default 4
```

Node groups can be scaled to 0 (removes all servers but keeps the group definition).

##### Upgrade Instance Type

```bash
ankra cluster hetzner node-group upgrade <cluster_id> default cx43
```

Instance type upgrades are irreversible — Hetzner disk enlargement cannot be undone. To use a smaller type, create a new node group and delete the old one.

##### Delete a Node Group

```bash
ankra cluster hetzner node-group delete <cluster_id> gpu-workers
```

##### OVH and UpCloud

The same commands are available for OVH and UpCloud clusters:

```bash
# OVH
ankra cluster ovh node-group list <cluster_id>
ankra cluster ovh node-group add <cluster_id> --name workers --instance-type b2-15 --count 2
ankra cluster ovh node-group scale <cluster_id> workers 4
ankra cluster ovh node-group upgrade <cluster_id> workers b2-30
ankra cluster ovh node-group delete <cluster_id> workers

# UpCloud
ankra cluster upcloud node-group list <cluster_id>
ankra cluster upcloud node-group add <cluster_id> --name workers --instance-type 4xCPU-8GB --count 2
ankra cluster upcloud node-group scale <cluster_id> workers 4
ankra cluster upcloud node-group upgrade <cluster_id> workers 8xCPU-16GB
ankra cluster upcloud node-group delete <cluster_id> workers
```

#### Node Groups at Cluster Creation

The `node_groups` field is now supported in the cluster create API for all providers. When provided, it replaces `worker_count` and `worker_server_type`:

```json
{
  "node_groups": [
    {"name": "default", "instance_type": "cx33", "count": 2},
    {"name": "gpu", "instance_type": "ccx33", "count": 1, "labels": {"gpu": "true"}, "taints": [{"key": "gpu", "value": "true", "effect": "NoSchedule"}]}
  ]
}
```

### Improvements

- **Server naming**: Servers are now named `{cluster}-{group_name}-{index}` instead of `{cluster}-worker-{index}` for better identification.
- **No online requirement**: Node group operations no longer require the cluster to be online.
- **Safe instance type changes**: Servers are powered off, verified off, resized, then powered back on. If the resize fails, the server is powered back on automatically.
- **Graceful K8s cleanup**: K8s uninstall during node deletion is now best-effort — unreachable nodes (powered off, deleted) no longer block the delete operation.

### API Endpoints

- `GET /api/v1/clusters/hetzner/{id}/node-groups` — list node groups
- `POST /api/v1/clusters/hetzner/{id}/node-groups` — add a node group
- `PUT /api/v1/clusters/hetzner/{id}/node-groups/{name}/scale` — scale a node group
- `PUT /api/v1/clusters/hetzner/{id}/node-groups/{name}/instance-type` — upgrade instance type
- `PUT /api/v1/clusters/hetzner/{id}/node-groups/{name}/labels` — update labels
- `PUT /api/v1/clusters/hetzner/{id}/node-groups/{name}/taints` — update taints
- `DELETE /api/v1/clusters/hetzner/{id}/node-groups/{name}` — delete a node group

Same endpoints available for OVH (`/clusters/ovh/...`) and UpCloud (`/clusters/upcloud/...`).

---

## v0.1.128 — April 2026

### New Features

#### Hetzner: Multiple SSH Key Support

Hetzner cluster creation now supports attaching multiple SSH key credentials with the `--ssh-key-credential-ids` flag. Pass a comma-separated list of credential IDs to deploy multiple keys to all servers.

```bash
ankra cluster hetzner create \
  --name my-cluster \
  --credential-id <hetzner_credential_id> \
  --ssh-key-credential-ids <key_id_1>,<key_id_2>,<key_id_3> \
  --location fsn1 \
  --control-plane-count 1 \
  --worker-count 2
```

The existing `--ssh-key-credential-id` flag continues to work for single-key usage.

#### UpCloud Cloud Cluster Management

Full lifecycle management for UpCloud clusters, including provisioning, deprovisioning, scaling, and Kubernetes version upgrades. UpCloud clusters use managed SDN Routers and NAT Gateways for private networking.

##### Create a Cluster

```bash
ankra cluster upcloud create \
  --name my-cluster \
  --credential-id <upcloud_credential_id> \
  --ssh-key-credential-id <ssh_key_credential_id> \
  --zone fi-hel1 \
  --control-plane-count 1 \
  --control-plane-plan 2xCPU-4GB \
  --worker-count 2 \
  --worker-plan 2xCPU-4GB
```

##### Deprovision a Cluster

Deprovision now uses the DAG-based operation system. Resources are deleted in the correct dependency order via the scheduler, and the cluster is only removed once all resources are cleaned up.

```bash
ankra cluster upcloud deprovision <cluster_id>
```

Example output:

```
UpCloud cluster deprovision initiated!
  Cluster ID: abc123
  Operation ID: op-456
  Resources queued for deletion: 11
```

##### Check Worker Count

```bash
ankra cluster upcloud workers <cluster_id>
```

##### Scale Workers

```bash
ankra cluster upcloud scale <cluster_id> 4
```

##### Check Kubernetes Version

```bash
ankra cluster upcloud k8s-version <cluster_id>
```

##### Upgrade Kubernetes Version

```bash
ankra cluster upcloud upgrade <cluster_id> v1.31.2+k3s1
```

#### UpCloud API Credentials

Manage UpCloud API credentials for cluster provisioning. UpCloud uses a single API token for authentication.

##### List UpCloud Credentials

```bash
ankra credentials upcloud list
```

##### Create an UpCloud Credential

```bash
ankra credentials upcloud create --name my-upcloud-cred --api-token <token>
```

##### List SSH Key Credentials

```bash
ankra credentials upcloud ssh-key list
```

##### Create an SSH Key Credential

```bash
ankra credentials upcloud ssh-key create --name my-key --generate
ankra credentials upcloud ssh-key create --name my-key --public-key "ssh-ed25519 AAAA..."
```

### Improvements

- **DAG-based deprovision**: Cluster deletion now creates a tracked operation with individual delete jobs, visible in the Operations UI. The cluster is only marked as deleted once all resources are successfully destroyed.
- **Parallel server deletion**: Multiple server delete jobs run concurrently in the DAG, reducing deprovision time.
- **Best-effort agent uninstall**: The Ankra agent uninstall step no longer blocks deprovision if SSH or Helm is unavailable.

### API Endpoints

- `POST /api/v1/clusters/upcloud` — create an UpCloud cluster
- `DELETE /api/v1/clusters/upcloud/{id}` — deprovision a cluster (returns operation ID)
- `GET /api/v1/clusters/upcloud/{id}/worker-count` — get worker count
- `POST /api/v1/clusters/upcloud/{id}/scale-workers` — scale workers
- `GET /api/v1/clusters/upcloud/{id}/k8s-version` — get Kubernetes version
- `POST /api/v1/clusters/upcloud/{id}/upgrade-k8s-version` — upgrade Kubernetes version
- `GET /api/v1/credentials/upcloud` — list UpCloud credentials
- `POST /api/v1/credentials/upcloud` — create an UpCloud credential
- `GET /api/v1/credentials/upcloud/ssh-keys` — list SSH key credentials
- `POST /api/v1/credentials/upcloud/ssh-key` — create an SSH key credential

---

## v0.1.127

### New Features

#### OVH Cloud Cluster Management

Full lifecycle management for OVH Cloud clusters, including provisioning, deprovisioning, scaling, and Kubernetes version upgrades.

##### Create a Cluster

```bash
ankra cluster ovh create \
  --name my-cluster \
  --credential-id <ovh_credential_id> \
  --ssh-key-credential-id <ssh_key_credential_id> \
  --region GRA7 \
  --control-plane-count 1 \
  --control-plane-flavor-id b2-15 \
  --worker-count 2 \
  --worker-flavor-id b2-15
```

##### Deprovision a Cluster

```bash
ankra cluster ovh deprovision <cluster_id>
```

##### Check Worker Count

```bash
ankra cluster ovh workers <cluster_id>
```

Example output:

```
Worker Count: 2
```

##### Scale Workers

```bash
ankra cluster ovh scale <cluster_id> 4
```

Example output:

```
Scaling workers.
  Previous count: 2
  New count:      4
```

##### Check Kubernetes Version

```bash
ankra cluster ovh k8s-version <cluster_id>
```

Example output:

```
Kubernetes Version: v1.31.2+k3s1
  Distribution: k3s
```

##### Upgrade Kubernetes Version

```bash
ankra cluster ovh upgrade <cluster_id> v1.35.1+k3s1
```

Example output:

```
Kubernetes version upgrade initiated.
  Previous version: v1.31.2+k3s1
  New version:      v1.35.1+k3s1
  Nodes affected:   3
```

#### OVH API Credentials

Manage OVH Cloud API credentials for cluster provisioning.

##### List OVH Credentials

```bash
ankra credentials ovh list
```

##### Create an OVH Credential

```bash
ankra credentials ovh create --name my-ovh-cred --project-id <project_id>
```

Prompts securely for application key, application secret, and consumer key. Credentials are validated against the OVH API on creation.

##### List SSH Key Credentials

```bash
ankra credentials ovh ssh-key list
```

##### Create an SSH Key Credential

```bash
ankra credentials ovh ssh-key create --name my-key --generate
```

Use `--generate` to create a new keypair, or omit it to provide your own public key.

### API Endpoints

- `POST /api/v1/clusters/ovh` — create an OVH cluster
- `DELETE /api/v1/clusters/ovh/{id}` — deprovision a cluster
- `GET /api/v1/clusters/ovh/{id}/worker-count` — get worker count
- `POST /api/v1/clusters/ovh/{id}/scale-workers` — scale workers
- `GET /api/v1/clusters/ovh/{id}/k8s-version` — get Kubernetes version
- `POST /api/v1/clusters/ovh/{id}/upgrade-k8s-version` — upgrade Kubernetes version
- `GET /api/v1/credentials/ovh` — list OVH credentials
- `POST /api/v1/credentials/ovh` — create an OVH credential
- `GET /api/v1/credentials/ovh/ssh-keys` — list SSH key credentials
- `POST /api/v1/credentials/ovh/ssh-key` — create an SSH key credential

---

## v0.1.126

### New Features

#### Hetzner Worker Scaling

Scale worker nodes on a Hetzner cluster up or down (1–10 nodes):

```bash
ankra cluster hetzner scale <cluster_id> <count>
```

Example:

```bash
ankra cluster hetzner scale abc123 5
```

Example output:

```
Scaling workers.
  Previous count: 3
  New count:      5
```

#### Hetzner Kubernetes Version Upgrade

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

- `POST /api/v1/clusters/hetzner/{id}/scale-workers` — scale workers
- `GET /api/v1/clusters/hetzner/{id}/k8s-version` — fetch current k8s version
- `POST /api/v1/clusters/hetzner/{id}/upgrade-k8s-version` — trigger k8s version upgrade

---

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
