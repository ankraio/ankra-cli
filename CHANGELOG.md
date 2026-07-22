# Ankra CLI Changelog

## Unreleased

### Added

- **Secrets can be set and encrypted in a single commit.** `ankra cluster
  encrypt manifest` (cluster mode) now accepts repeatable `--set` edits that
  are applied in-memory before encryption, so the new value and its SOPS
  encryption land in one partial-stack PATCH — the plaintext value never
  reaches git history. Previously the documented flow (`manifests upgrade
  --set` followed by `encrypt manifest`) committed the plaintext secret
  first, leaving it recoverable from the repository history.
- **`ankra cluster manifests upgrade --from-file` accepts SOPS-encrypted
  files.** When the file carries SOPS metadata, the keys holding `ENC[...]`
  ciphertext are detected and recorded as `encrypted_paths` automatically
  (merged with the manifest's existing paths), and the new repeatable
  `--encrypted-path` flag declares keys explicitly. Previously such uploads
  dropped the encryption metadata and the backend rejected them with a
  generic 500.
- **`ankra helm registries list` supports pagination, search, and sorting.**
  The command used to fetch only the server's first page (20 registries) and
  gave no hint that more existed. It now accepts `--page` and `--page-size`
  (up to 100 per page), `--search` for a case-insensitive name filter, and
  `--sort-by` (`name`, `url`, `created_at`, `updated_at`, `chart_count`,
  `last_indexed_at`, `is_global`) with `--sort-order asc|desc`, and every
  listing ends with a `Page X of Y (total N)` footer so truncation is always
  visible.

## v0.9.0-rc4 — 2026-07-21

### Added

- **Hetzner clusters can be stopped and started.** `ankra cluster hetzner
  stop <cluster_id>` releases the cluster's compute while preserving its
  saved topology, and `ankra cluster hetzner start <cluster_id>` re-provisions
  it (optionally `--scope control_plane`), matching the other self-managed
  providers.
- **The managed Kubernetes family reaches parity.** `ankra cluster managed
  stop|start` drives provider-native stop/start where the provider supports
  it (AKS today), the new `ankra cluster managed node-pool update` command
  changes node counts and autoscaling settings in place, node pools take
  autoscaling bounds at create and add (`--autoscaling`,
  `--autoscaling-min`, `--autoscaling-max`), and Scaleway Kapsule joins the
  provider list (`--provider kapsule`, with `--private-network-id`).
- **Proxmox VE clusters are managed from the CLI.** The new `ankra cluster
  proxmox` family covers create, deprovision, stop/start, worker and
  node-group scaling, labels and taints, autoscaling, control-plane changes,
  node inspection and restart, SSH keys, Kubernetes upgrades, and discovery of Proxmox
  nodes, storages, bridges, and templates, plus `ankra credentials proxmox`
  for credential management.
- **HPE Morpheus clusters are managed from the CLI.** The new `ankra
  cluster morpheus` family mirrors the Proxmox surface (node restart excepted — the
  platform has no Morpheus restart lane) — full lifecycle,
  node groups, control plane, SSH keys, and upgrades — plus discovery of
  Morpheus groups, clouds, plans, layouts, and networks, and `ankra
  credentials morpheus` for credential management.
- **The lifecycle systemtest covers managed clusters.** `systemtest/
  lifecycle_systemtest.sh` now exercises both cluster families: the
  self-managed provider lifecycle and, per managed provider, the managed
  lifecycle (create, node-pool add/scale/update, stop/start where
  supported, upgrade, delete).

## v0.9.0-rc3 — 2026-07-21

### Added

- **See and stop what your AI agents are doing with `ankra agents`.** The
  new command family lists the organisation's dispatched AI agent runs
  (`ankra agents runs`, filterable by agent and status), shows one run in
  full (`ankra agents run <run_id>`), reads the run's session transcript —
  what the agent actually said and did — (`ankra agents transcript
  <run_id>`), and cancels a live run (`ankra agents cancel <run_id>`,
  organisation admins only): the platform interrupts the in-flight turn
  within seconds without pausing the agent itself. All four support
  `-o json|yaml` for scripting.
## v0.9.0-rc2 — 2026-07-20

### Added

- **Scaleway clusters now support lifecycle commands.** Use
  `ankra cluster scaleway stop <cluster_id>` to release compute while
  preserving the cluster definition, then `ankra cluster scaleway start
  <cluster_id>` to re-provision it (optionally `--scope control_plane`).
- **Application management is available from the CLI.** `ankra application
  add .` detects a local GitHub checkout and starts application setup, while
  the application subcommands expose lifecycle, deployment, workflow,
  repository, security, publishing, and demo operations through the bearer
  API. `-o json|yaml` provides scriptable output.

## v0.9.0-rc1 — 2026-07-17

### Added

- **Read-only API calls now retry transient platform errors.** Bodyless
  `GET`/`HEAD` requests that fail with a transport-level timeout (for
  example `http2: timeout awaiting response headers`), a connection
  setup/reset error, a mid-exchange disconnect, an HTTP/2 GOAWAY, or a
  502/503/504 gateway status are retried up to two more times with a
  short backoff (1s, then 2s), with a warning on stderr per retry. A
  seconds-long platform blip no longer hard-fails scripts and CI
  pipelines on their first read (2026-07-14: a brief platform stall
  failed a production rollout on `listing clusters`). Writes are never
  retried.
- **`ankra cluster <provider> nodes restart` restarts a single node.** For
  Hetzner, OVH, UpCloud, and DigitalOcean clusters you can now restart any
  provisioned node - a control plane node, a worker, or the bastion/gateway -
  as a tracked operation. The platform schedules a native reboot (falling
  back to a power cycle); the node must be in the `up` state with no restart
  already in flight. Find the node ID with `nodes list`.
- **`ankra cluster <provider> bastion resize` changes the bastion instance
  type.** A new `bastion` command family (Hetzner, OVH, UpCloud,
  DigitalOcean) resizes the cluster's bastion/gateway node, following the
  same async accept/wait contract as node-group instance-type upgrades:
  submit-and-return by default, or block with `--wait`.
- **`ankra cluster <provider> nodes list` now shows provider status.** The
  node table gained a `PROVIDER_STATUS` column carrying the cloud provider's
  live status/power state (for example OVH `ACTIVE`/`SHUTOFF`) as last
  recorded by the provider read job, so a crashed or externally-stopped VM is
  visible before you act on it. Structured output (`-o json|yaml`) carries
  `provider_status` and `provider_power_state`.

### Fixed

- **`ankra tokens create` now gives MCP-specific guidance for scoped tokens.**
  The previous examples named permission scopes the platform rejects.
  The help text now shows `mcp:read` and `mcp:write`, and successful scoped
  token creation prints the MCP endpoint instead of suggesting a REST
  `ANKRA_API_TOKEN` configuration that would be refused.

## v0.8.0 — 2026-07-14

The stable v0.8.0 release promotes v0.8.0-rc0: agent-token output and
agent-status accuracy fixes, plus drift field-path visibility in
`ankra cluster operations`.

### Added

- **`ankra cluster operations` now shows which fields drifted.** Single
  execution views (`operations list <id>`, `operations steps <id>`) fetch
  the step results and render each drifting resource with its drift type
  and the exact field paths the agent compared (for example
  `/spec/template/spec/hostNetwork`), instead of only step metadata and
  timings. Structured output (`-o json|yaml`) carries the same data as
  `drift_resources` on each step. Enrichment is best-effort: on platforms
  without the execution result endpoint the commands work as before and
  print a note to stderr.

### Fixed

- **`ankra cluster agent token` no longer prints an empty token.** The
  platform's token endpoints return the agent install command (and, on newer
  platforms, `token` and `cluster_id` fields), while the CLI decoded a
  `token`/`expires_at` shape that no longer existed and silently rendered an
  empty string. The CLI now decodes all returned fields, extracts the
  `ank_cai_…` token from the helm command when the platform only returns
  `command`, and prints the token together with the full install/update
  command. Structured output (`-o json|yaml`) now carries `token`,
  `cluster_id`, and `command`; the never-populated `expires_at` field is
  gone.
- **`ankra cluster agent status` no longer reports a stale agent as
  `connected`.** The status was derived from `checked_in_at` merely being
  present, so an agent that had been rejected or offline for hours (even
  days) still displayed `Status: connected`. The CLI now uses the platform's
  `is_online` verdict when present (30-second check-in threshold, the same
  one that flips clusters offline) and otherwise falls back to a two-minute
  check-in recency test; a stale check-in renders as
  `not connected (stale check-in)`.

## v0.7.0 — 2026-07-10

The stable v0.7.0 release consolidates the v0.7.0 release candidates: the
new ticket-relay browser login (required by the platform, which now answers
the old localhost-callback flow with 426 Upgrade Required), managed
Kubernetes support for all six providers, and Homebrew installation.

### Changed

- **`ankra login` no longer opens a local network port.** The old flow
  started a localhost callback server and had the browser redirect the OAuth
  code to it. The CLI now starts a platform login ticket, drives the whole
  flow in the browser (including sign-in approval and any MFA challenge),
  and polls `/api/v1/cli/login/poll` with the PKCE code verifier — which
  never leaves the machine — to collect the parked token. The platform has
  dropped support for the localhost-callback flow and refuses pre-v0.7.0
  CLIs with `426 Upgrade Required`, so this release is required to log in.

### Added

- **Homebrew installation.** `brew install ankraio/tap/ankra` installs the CLI
  from the new [ankraio/homebrew-tap](https://github.com/ankraio/homebrew-tap)
  vendor tap. The release workflow now renders the formula from
  `packaging/homebrew/ankra.rb.tmpl` and pushes version and checksum bumps to
  the tap on every stable tag; pre-release tags never reach brew. A
  Homebrew-managed binary refuses `ankra upgrade` (self-update) and defers to
  `brew upgrade ankra`, so brew stays the single owner of the file.
- **`ankra cluster managed` now supports all six managed Kubernetes
  providers.** The `--provider` flag previously accepted only `doks` and
  `uks`, even though the backend and portal already managed GKE, OVHcloud
  MKS, AKS, and EKS at the same endpoints. `create`, `delete`, `node-pool
  add|scale|delete`, and `upgrade` now accept `doks`, `uks`, `gke`,
  `ovh_mks`, `aks`, and `eks` (the `ovh-mks` and `mks` aliases normalise to
  `ovh_mks`; input is lower-cased and trimmed). Provider-specific
  control-plane options, node-pool autoscaling bounds, and cluster
  discovery/import remain portal/API only.

## v0.7.0-rc3 — 2026-07-10

### Added

- **`ankra cluster managed` now supports all six managed Kubernetes
  providers.** The `--provider` flag previously accepted only `doks` and
  `uks`, even though the backend and portal already managed GKE, OVHcloud
  MKS, AKS, and EKS at the same endpoints. `create`, `delete`, `node-pool
  add|scale|delete`, and `upgrade` now accept `doks`, `uks`, `gke`,
  `ovh_mks`, `aks`, and `eks` (the `ovh-mks` and `mks` aliases normalise to
  `ovh_mks`; input is lower-cased and trimmed). Provider-specific
  control-plane options, node-pool autoscaling bounds, and cluster
  discovery/import remain portal/API only.

## v0.6.0 — 2026-07-10

The stable v0.6.0 release consolidates v0.6.0-rc0 and adds organisation
cluster groups and scoped role assignments on top: agent rules and hooks that
make Ankra the default Kubernetes workflow, stack deploy waves, node-group
autoscaling, and the first slice of platform RBAC.

### Security

- **Builds now use Go 1.26.5**, picking up the fix for
  [GO-2026-5856](https://pkg.go.dev/vuln/GO-2026-5856) (`crypto/tls`:
  Encrypted Client Hello privacy leak), which govulncheck flagged as reachable
  from the CLI's TLS paths — the login callback server, streaming log/chat
  reads and writes, and every API round-trip. `golang.org/x/sys` is bumped to
  v0.44.0 for [GO-2026-5024](https://pkg.go.dev/vuln/GO-2026-5024)
  (Windows-only; not called by the CLI). `govulncheck ./...` is clean again.

### Added

- **Organisation cluster groups.** `ankra org cluster-groups
  list|create|add-cluster|set-selector|preview` manages named sets of
  clusters, either static (pinned members) or dynamic (a label selector
  evaluated against cluster labels), for use as role-assignment scopes.
  `preview` shows the clusters a group currently resolves to.
- **Scoped role assignments.** `ankra org assign <member-email>` grants a
  member a role at organisation, cluster, or cluster-group scope;
  `ankra org assignments <member-email>` lists what a member holds and
  `ankra org unassign <assignment-id>` revokes it. `ankra org roles create`
  defines custom roles that may bundle Kubernetes access levels provisioned
  across the assignment's scope (ADR 0007 extension).
- **`ankra cluster list` gains an Environment column.**
- **`ankra skills install` makes Ankra the agent's default for Kubernetes
  work.** Skills alone only load when the conversation happens to match their
  description, so install now also writes an always-applied rule telling
  Cursor/Claude Code that clusters here are Ankra-managed: route changes
  through the GitOps repo or `ankra cluster apply`, inspect freely, never
  mutate with raw kubectl/helm. Cursor gets a local plugin rule
  (`~/.cursor/plugins/local/ankra`) or a project `.cursor/rules/ankra.mdc`;
  Claude Code gets a marker-managed block in `CLAUDE.md` that reinstalls
  refresh and uninstalls remove without touching your own content. Skip with
  `--no-rules`; a full `ankra skills uninstall` cleans everything up.
- **`ankra skills install --with-hooks` enforces the workflow.** Installs an
  agent hook (Cursor `beforeShellExecution`, Claude Code `PreToolUse`) that
  runs the new `ankra skills guard` plumbing command on every shell command:
  cluster-mutating kubectl/helm invocations (`apply`, `delete`, `helm
  upgrade`, ...) pause for user confirmation with a redirect to the Ankra
  workflow, while read-only inspection, `--dry-run`, and everything else pass
  through untouched. The guard fails open and merges into existing
  hooks.json/settings.json without disturbing other entries.
- **Skill descriptions now trigger on plain Kubernetes intent.** The embedded
  skills previously activated only when the user said "Ankra"; their
  descriptions now match what users actually ask ("deploy an app", "install a
  Helm chart", "set up monitoring", "store secrets in Git"), so agents reach
  for the Ankra workflow without being told. The `ankra-platform-principles`
  skill doubles as the gateway: it applies to any Kubernetes task in an
  environment with the `ankra` CLI or an Ankra GitOps repo, with an explicit
  escape hatch for clusters that are not Ankra-managed.
- **Stack deploy waves.** Stacks in a cluster YAML accept an optional
  `deploy_wave` (integer >= 0): stacks in wave N deploy only after every
  stack in a lower wave finished, and teardown unwinds in reverse order.
  Stacks without a wave keep the current unordered behaviour. The wave is
  validated by `ankra cluster apply`, preserved by partial patches
  (`ankra cluster addons upgrade`, `ankra cluster manifests upgrade`), and
  shown as a new "Wave" column in `ankra cluster stacks list`.
- **`ankra cluster node-group autoscaling get|set`.** Read and write a node
  group's Cluster Autoscaler settings on Hetzner, OVH, and UpCloud clusters:
  `set --enabled --min <n> --max <n>` keeps the group's node count within
  [min, max] based on pod demand (first enable installs the autoscaler),
  `--enabled=false` turns it off. Both honour `-o json|yaml`, and `set`
  supports the standard `--wait`/`--timeout` async-write flags.
- **Wider organisation roles ahead of platform RBAC.** `ankra org invite
  --role` now accepts `owner`, `admin`, `operator`, `member`, `viewer`, and
  `read-only`, validated client-side with a clear usage error; the new
  `ankra org roles` lists them with descriptions. Until the RBAC assignments
  API ships, `owner`/`operator` alias onto `admin`/`member` on the wire.
- **`ankra tokens create --scopes`.** Optionally pin an API token to a
  permission allowlist (e.g. `--scopes clusters.read,stacks.deploy`);
  omitting it keeps today's behaviour of the user's full authority.
- **Exit code 7 for RBAC permission denials.** When the platform denies a
  request because the caller's role lacks a permission (403
  `permission_denied`), the CLI now names the missing permission, points at
  an organisation admin, and exits 7 — distinct from exit 6, which means
  re-authenticate. Reads, async writes, and stack patches all classify;
  other 403s keep their existing handling.

## v0.5.1 — 2026-07-07

### Added

- **`ankra cluster digitalocean`** — create, deprovision, stop/start, scale, upgrade, node groups,
  regions/sizes discovery, and credential management (alias: `ankra cluster do`,
  `ankra credentials digitalocean`).
- **`ankra cluster managed`** — create, deprovision, upgrade, and node-pool operations for
  DigitalOcean Kubernetes (`doks`) and UpCloud Managed Kubernetes (`uks`).
- Provider-agnostic cluster commands (`scale`, `upgrade`, `node-group`, `ssh-keys`, `deprovision`)
  now detect `digitalocean` clusters automatically.
- `systemtest/lifecycle_systemtest.sh` now exercises Kubernetes distribution as
  an independent axis (`ANKRA_SYSTEMTEST_DISTRIBUTIONS="k3s kubeadm"`), running
  one cluster per provider/distribution pair, and generates a unique `/16` per
  DigitalOcean cluster to avoid VPC range collisions across parallel workers.

### Fixed

- **`ankra cluster addons upgrade` / `manifests upgrade` / `encrypt ... --cluster` /
  `stacks variables set|delete` timing out on large clusters.** These commands
  end in a partial-stack PATCH that the backend serves synchronously (DB
  transaction plus a full GitOps commit/push when the cluster has a linked
  repo), which can legitimately take longer than the previous 60-second
  command context. The context deadline is now 5 minutes, matching the HTTP
  client's existing slow-write timeout.

## v0.6.0-rc0 — 2026-07-07

### Added

- **`ankra skills install` makes Ankra the agent's default for Kubernetes
  work.** Skills alone only load when the conversation happens to match their
  description, so install now also writes an always-applied rule telling
  Cursor/Claude Code that clusters here are Ankra-managed: route changes
  through the GitOps repo or `ankra cluster apply`, inspect freely, never
  mutate with raw kubectl/helm. Cursor gets a local plugin rule
  (`~/.cursor/plugins/local/ankra`) or a project `.cursor/rules/ankra.mdc`;
  Claude Code gets a marker-managed block in `CLAUDE.md` that reinstalls
  refresh and uninstalls remove without touching your own content. Skip with
  `--no-rules`; a full `ankra skills uninstall` cleans everything up.
- **`ankra skills install --with-hooks` enforces the workflow.** Installs an
  agent hook (Cursor `beforeShellExecution`, Claude Code `PreToolUse`) that
  runs the new `ankra skills guard` plumbing command on every shell command:
  cluster-mutating kubectl/helm invocations (`apply`, `delete`, `helm
  upgrade`, ...) pause for user confirmation with a redirect to the Ankra
  workflow, while read-only inspection, `--dry-run`, and everything else pass
  through untouched. The guard fails open and merges into existing
  hooks.json/settings.json without disturbing other entries.
- **Skill descriptions now trigger on plain Kubernetes intent.** The embedded
  skills previously activated only when the user said "Ankra"; their
  descriptions now match what users actually ask ("deploy an app", "install a
  Helm chart", "set up monitoring", "store secrets in Git"), so agents reach
  for the Ankra workflow without being told. The `ankra-platform-principles`
  skill doubles as the gateway: it applies to any Kubernetes task in an
  environment with the `ankra` CLI or an Ankra GitOps repo, with an explicit
  escape hatch for clusters that are not Ankra-managed.
- **Stack deploy waves.** Stacks in a cluster YAML accept an optional
  `deploy_wave` (integer >= 0): stacks in wave N deploy only after every
  stack in a lower wave finished, and teardown unwinds in reverse order.
  Stacks without a wave keep the current unordered behaviour. The wave is
  validated by `ankra cluster apply`, preserved by partial patches
  (`ankra cluster addons upgrade`, `ankra cluster manifests upgrade`), and
  shown as a new "Wave" column in `ankra cluster stacks list`.
- **`ankra cluster node-group autoscaling get|set`.** Read and write a node
  group's Cluster Autoscaler settings on Hetzner, OVH, and UpCloud clusters:
  `set --enabled --min <n> --max <n>` keeps the group's node count within
  [min, max] based on pod demand (first enable installs the autoscaler),
  `--enabled=false` turns it off. Both honour `-o json|yaml`, and `set`
  supports the standard `--wait`/`--timeout` async-write flags.
- **Wider organisation roles ahead of platform RBAC.** `ankra org invite
  --role` now accepts `owner`, `admin`, `operator`, `member`, `viewer`, and
  `read-only`, validated client-side with a clear usage error; the new
  `ankra org roles` lists them with descriptions. Until the RBAC assignments
  API ships, `owner`/`operator` alias onto `admin`/`member` on the wire.
- **`ankra tokens create --scopes`.** Optionally pin an API token to a
  permission allowlist (e.g. `--scopes clusters.read,stacks.deploy`);
  omitting it keeps today's behaviour of the user's full authority.
- **Exit code 7 for RBAC permission denials.** When the platform denies a
  request because the caller's role lacks a permission (403
  `permission_denied`), the CLI now names the missing permission, points at
  an organisation admin, and exits 7 — distinct from exit 6, which means
  re-authenticate. Reads, async writes, and stack patches all classify;
  other 403s keep their existing handling.

## v0.5.0 — 2026-07-05

### Added

- **kubeadm cluster support in `ankra cluster upgrade`.** The provider-agnostic
  upgrade now covers kubeadm-distribution clusters alongside k3s. Nodes
  upgrade one at a time (control plane first): each node is cordoned, drained
  respecting PodDisruptionBudgets, upgraded, and gated on being Ready at the
  target version, with an etcd snapshot taken before the control plane. A new
  `--force` flag proceeds when a drain is blocked by a PodDisruptionBudget
  (the default aborts safely), and the upgrade now prints the operation ID
  with a hint to follow progress via `ankra cluster operations list`.
- **`ankra cluster kubeadm-versions`** lists the upstream Kubernetes versions
  the platform can provision or upgrade kubeadm clusters to, as a sibling of
  `ankra cluster k3s-versions`.
- **etcd topology flags for kubeadm creates.** `ankra cluster
  hetzner|ovh|upcloud create` gain `--etcd-topology` (`stacked` on the control
  planes, or `external` on dedicated VMs), `--etcd-node-count` (3 or 5), and
  `--etcd-server-type` for sizing the dedicated etcd nodes.
- **`ankra stack-profiles list --category`** filters profiles server-side by
  category (e.g. `monitoring`).
- **Generated CLI reference.** `tools/gendocs` renders the full command tree
  as Mintlify MDX pages, and a release-tag workflow opens a sync PR against
  the public docs so the reference never drifts from the shipped CLI.

### Fixed

- **Kubeconfig exec entries pin the cluster's owning organisation.** Entries
  written by `ankra cluster kubeconfig add` now embed `--org
  <organisation-id>` in the `kube-token` exec args, so `kubectl` keeps
  working after you switch your selected organisation — previously the token
  mint failed with "Cluster not found" whenever the selection differed from
  the cluster's owner. Cluster IDs are resolved to their owning organisation
  (and real cluster name) via the backend; entries written before this
  release need a one-time re-add to pick up the pin.
- **`ankra stack-profiles export-iac` exports the current version by
  default.** The `--version` default was a hard-coded `1`, silently exporting
  a stale first version once a profile advanced; it now resolves the
  profile's current published version and errors clearly when a profile has
  no published versions.
- **Deprecated `ankra cluster <provider> upgrade` help no longer overstates
  parity.** These forms always run the safe non-forced rollout; the help now
  says so and points at `ankra cluster upgrade` for `--force` and operation
  tracking.

## v0.4.2 — 2026-07-03

### Fixed

- **`ankra login` now declares two-factor capability to the platform.** The
  token exchange sends `supports_mfa: true`, letting the platform tell CLIs
  that can complete the Ankra-native two-factor step-up apart from legacy
  releases (v0.3.0 and older) that silently saved an empty token and reported
  "Login successful!". Once the platform enforces the check, outdated CLIs
  receive an explicit upgrade error instead of a broken login.

### Changed

- **Differentiated exit codes** - the CLI now exits with a stable, documented
  code instead of always `1`: `0` success, `1` API/runtime error, `2`
  usage/flag error (unknown command/flag, bad arguments, missing required
  flags), `3` targeted resource not found, `4` confirmation declined, `5`
  `--wait`/`--timeout` expiry on asynchronous writes (internal request
  deadlines still exit `1`), `6` authentication failure (missing/expired/
  rejected credentials, 401/403). Scripts can now branch on the failure class
  (re-authenticate on 6, treat 3 as idempotent success) without parsing error
  text.
- **Declined confirmations exit `4` everywhere** - including `helm registries
  delete` and `helm credentials delete`, which previously printed
  "Cancelled." to stdout and exited `0`, indistinguishable from a successful
  delete.
- **Errors always reach stderr and set a non-zero exit code.** Every command
  handler was converted from cobra's `Run` to `RunE`. This fixes a class of
  bugs where failures printed an error to *stdout* and exited `0`, invisible
  to scripts and CI: all of `charts` and `chat`, `cluster manifests list`,
  `cluster select`/`clear`, credential list commands, and others. Error text
  no longer pollutes stdout for `-o json|yaml` consumers; it is printed by
  cobra to stderr as `Error: ...`.
- **`ankra delete cluster`** - declining the confirmation prompt now exits `4`
  (previously printed "Aborted." and exited `0`); deleting a cluster that does
  not exist now exits `3` (previously exited `0`); the refusal hint for cloud
  clusters now points at `ankra cluster deprovision` instead of the
  provider-namespaced form deprecated in v0.4.0; and the underlying API error
  is included in the failure message instead of being swallowed.

### Added

- **Deprecation forwarding machinery** (internal) - `deprecateAndForward`
  registers a hidden forwarder at an old command path that re-dispatches to
  the replacement with argument rewriting, emitting cobra's human-facing
  notice plus a machine-readable `ANKRA_DEPRECATED=<old>=><new>
  removal=<version>` stderr marker for scripts and agents. No forwarders are
  wired yet; this lands the mechanism for upcoming command-tree work.

## v0.4.1 — 2026-07-03

### Fixed

- **`ankra login` no longer reports "Login successful!" while saving an empty
  token.** When the platform withholds the API token — for example when the
  account requires a two-factor step-up that the running CLI version does not
  understand — older CLIs silently wrote an empty `token:` to `~/.ankra.yaml`
  and declared success, leaving every subsequent command failing with
  "not logged in". The login flow now refuses to persist credentials without a
  token and explains what happened: an incomplete two-factor step-up says to
  run `ankra login` again, and a token-less exchange points at
  `ankra upgrade`. Existing saved credentials are left untouched either way.

## v0.4.0 - 2026-06-30

The stable v0.4.0 release consolidates the v0.4.0 release candidates into a
larger CLI control-plane update: provider-agnostic cloud cluster management,
cluster access administration, stack-profile apply/get, global per-command
cluster targeting, self-service MFA tooling, and more resilient login and
GitOps write paths.

### Added

- **`ankra profile auth ...`** - manage your own two-factor authentication from
  the CLI. `status` shows enrolled authenticators, passkeys/security keys and
  remaining recovery codes; `totp start|confirm|remove` sets up or removes an
  authenticator app; `recovery-codes regenerate` creates a fresh one-time code
  set; and `passkeys list|remove|open` lists/removes passkeys or opens Profile
  Authentication in the browser for WebAuthn setup.
- **`ankra skills --editor claude-code`** - install the curated Ankra Agent
  Skills into Claude Code's `~/.claude/skills` directory, or
  `<project>/.claude/skills` when combined with `--project`.
- **`ankra cluster access list | grant | revoke`** - manage per-user access to
  a cluster's Kubernetes API through the Ankra kube gateway, including
  namespace-scoped grants and RBAC reconcile status.
- **Provider-agnostic cloud cluster lifecycle commands** - `ankra cluster
  upgrade`, `scale`, `node-group`, `k3s-versions`, and `deprovision` now detect
  Hetzner, OVH, or UpCloud automatically, so users no longer need to pick a
  provider namespace for common lifecycle work.
- **Cloud create parity across Hetzner, OVH, and UpCloud** - cloud-provider and
  networking stacks can be installed directly by default and committed to GitOps
  when repository flags are supplied.
- **OVH operational commands** - stop/start clusters, print access info, manage
  SSH keys, set node-group labels/taints, and inspect control-plane or node
  details through the public API.
- **`ankra stack-profiles get` and `ankra stack-profiles apply`** - inspect
  published stack-profile versions and instantiate a profile as a draft or
  deploy it directly, with `--set`, `--set-file`, and `--set-env` parameter
  binding.
- **Organisation slug resolution** - organisation slugs are shown in org output,
  and `ankra org switch` plus global `--org` resolve by ID, slug, or name.
- **Global `--cluster <name|id>` for cluster-scoped commands** - target a
  cluster for a single command without changing the saved selection.
- **`ankra cluster ssh-keys get | set | resync <cluster_id>`** - manage SSH keys
  across Hetzner, OVH, and UpCloud from one command group, including provider
  reference repair with `resync`.

### Changed

- **`ankra login` now completes Ankra-native two-factor authentication.** Second
  factors are managed by Ankra (not Auth0): when your account has a passkey,
  security key, or authenticator app enrolled, the token exchange withholds the
  API token and returns a one-time challenge URL. The CLI opens that URL in your
  browser, you complete the second step (passkey, authenticator code, or recovery
  code), and the CLI polls until the step-up succeeds and the token is released.
  No flags change; accounts without a second factor log in exactly as before.
- **`ankra login` is more reliable on dual-stack IPv4/IPv6 machines.** The
  browser redirect now uses the same `127.0.0.1` loopback address the callback
  server listens on, and the callback wait matches the backend's 10-minute
  login-state expiry.
- **`--config <file>` now fully isolates per-invocation state.** Extensionless
  config files are parsed as YAML, and active-cluster selection is keyed to the
  explicit config path so parallel workers do not clobber each other.
- **`ankra support create` now shows the AI review before submitting.** Flagged
  requests and possible duplicates are shown before confirmation; `--force`
  still skips the prompt.

### Fixed

- **Partial-stack writes tolerate slow synchronous Git commits.** Commands that
  PATCH a stack (`manifests upgrade`, `addons update`, `cluster encrypt`, and
  `stack-variables set`) are bounded by an overall 5-minute deadline instead of
  the shared client's 30-second response-header timeout.
- **`ankra cluster encrypt` preserves leading-dot keys such as
  `.dockerconfigjson`.** Dotted-path normalisation no longer corrupts literal
  Kubernetes secret keys that begin with a dot.

### Deprecated

- The provider-specific `ankra cluster {hetzner,ovh,upcloud} upgrade`, `scale`,
  `node-group`, and `deprovision` commands are deprecated in favour of the
  provider-agnostic verbs above and are scheduled for removal in v0.5.0.
- `ankra cluster ovh ssh-keys get | set <cluster_id>` is deprecated in favour
  of `ankra cluster ssh-keys get | set <cluster_id>` and is scheduled for
  removal in v0.6.0.

## v0.4.0-rc4 - 2026-06-23

### Fixed

- **Partial-stack writes no longer fail with `http2: timeout awaiting response
  headers` when the server commits to git synchronously.** `ankra cluster
  manifests upgrade`, `ankra cluster addons update`, `ankra cluster encrypt`,
  and `ankra cluster stack-variables set` all issue
  `PATCH /stacks/{stack_name}`, which performs a synchronous git commit+push on
  the request path and can legitimately take longer than the shared HTTP
  client's 30s response-header timeout to start responding. These partial-stack
  writes now use a dedicated client that drops the response-header timeout and
  is bounded by an overall 5-minute deadline, so a slow-but-progressing server
  completes the write instead of erroring out while still making progress.

## v0.4.0-rc3 - 2026-06-23

### Added

- **Global `--cluster <name|id>` on every `ankra cluster ...` subcommand** -
  target a cluster per command without first running `ankra cluster select`.
  The flag is inherited by all cluster subcommands (`stacks`, `operations`,
  `addons`, `manifests`, `get`/`logs`/`resources`, `helm`, `agent`,
  `reconcile`, `provision`, `deprovision`, `roll-to`, `info`, ...) and takes
  precedence over the persisted selection; it also accepts either a cluster
  name or ID. `ankra chat health` and `ankra openclaw skill | handoff` gained
  the same `--cluster` override. Commands that already accepted a positional
  cluster name still do - an explicit argument wins over `--cluster`, which in
  turn wins over the saved selection.

- **`ankra cluster ssh-keys get | set | resync <cluster_id>`** - cloud-agnostic
  SSH key management that detects the provider (Hetzner, OVH, UpCloud)
  automatically from the cluster. `get` lists attached and available SSH key
  credentials, `set` replaces the attached set (use `--clear` to remove all user
  keys; the Ankra-managed key is always retained) and applies the change to
  running nodes, and `resync` repairs a stale provider-side SSH key reference
  (for example when the key was deleted and re-created in the provider console)
  that blocks new node creation, re-applying the authorised keys to running
  nodes.

### Fixed

- **`ankra login` now completes reliably on dual-stack (IPv4/IPv6) machines.**
  The browser callback server binds the IPv4 loopback (`127.0.0.1`) but the
  redirect URI advertised to the backend used `localhost`, which resolves to
  both `127.0.0.1` and `::1`. A browser that connected to the IPv6 address
  reached nothing, so after authenticating (including MFA) the final
  `http://localhost:<port>/callback` redirect failed and login never finished.
  The redirect URI now uses the `127.0.0.1` literal (RFC 8252 §8.3), matching
  the listener. The CLI also waits up to 10 minutes for the callback (was 5) to
  align with the backend's login-state expiry, so a slow MFA round-trip no
  longer tears the callback server down early.

### Deprecated

- **`ankra cluster ovh ssh-keys get | set <cluster_id>`** - replaced by the
  cloud-agnostic `ankra cluster ssh-keys get | set <cluster_id>`. The provider is
  detected automatically from the cluster.

## v0.4.0-rc2 - 2026-06-19

Builds on v0.4.0-rc1 (all of its provider-parity work is included) and adds
stack-profile inspection and one-step apply, plus organisation slug resolution.

### Added

- **`ankra stack-profiles get <profile-id>`** - show a stack profile's metadata,
  its published versions, and the parameters a version exposes. Pick a specific
  version with `--version` (defaults to the profile's current version) and use
  `-o json|yaml` for structured output.
- **`ankra stack-profiles apply <profile-id>`** - instantiate a stack profile
  onto a cluster. By default it creates a reviewable **draft** (nothing is
  deployed until you pass `--deploy` or deploy it from the dashboard); it targets
  the selected cluster unless `--cluster <name|id>` is given. Choose the profile
  `--version`, name the new stack with `--stack-name`, and bind parameters with
  `--set name=value` - or, for secrets, `--set-file name=path` / `--set-env
  name=ENV_VAR` so values never reach your shell history or process list.
- **Organisation slugs** - the organisation `slug` is now shown in
  `ankra org list`, `ankra org current`, and `ankra org create`, and both
  `ankra org switch <organisation>` and the global `--org` flag resolve a
  reference by ID, slug, or name (case-insensitive), with actionable errors on
  ambiguous or unknown references.

See the **v0.4.0-rc1** notes below for the cloud-agnostic `cluster
upgrade | scale | node-group` verbs, the cloud-provider/ingress parity across
OVH, UpCloud and Hetzner, and the deprecation of the provider-specific
`cluster {hetzner,ovh,upcloud}` commands.

## v0.4.0-rc1 - 2026-06-18

### Added

- **`ankra cluster access list | grant | revoke`** - manage who can reach a
  cluster's Kubernetes API through the Ankra kube gateway (the access used by
  `ankra cluster kubeconfig` and `ankra cluster kube-token`). A grant maps an
  organisation member (by email) to a Kubernetes role (`view`, `edit`, `admin`,
  `cluster-admin`), cluster-wide or limited to one namespace with
  `--namespace`. `list` shows each grant's RBAC reconcile status (pending,
  applied, failed, cluster offline); `revoke` accepts a grant ID or an email
  (revoking every grant that member has on the cluster). Managing access
  requires organisation admin rights.

- **`ankra cluster upgrade <cluster_id> <target_version>`**, **`ankra cluster
  scale <cluster_id> <worker_count>`**, and **`ankra cluster node-group
  <list|add|scale|upgrade|delete>`** - cloud-agnostic verbs that detect the
  provider (Hetzner, OVH, UpCloud) automatically from the cluster, so you no
  longer pick a provider namespace. They replace the provider-specific
  `ankra cluster {hetzner,ovh,upcloud} ...` forms (see Deprecated).
- **`ankra cluster k3s-versions`** - list the k3s (Kubernetes) versions
  available for `ankra cluster upgrade`, with the stable channel highlighted.
- **`ankra cluster deprovision <cluster_id>`** now accepts a cluster ID or a
  name (previously name-only) and routes cloud clusters to the provider-specific
  teardown so cloud resources are released.

- **`ankra cluster ovh create`** now accepts **`--external-cloud-provider`**
  (OpenStack CCM + Cinder CSI), **`--include-networking`** (Traefik +
  cert-manager), and **`--gitops-credential-name`** / **`--gitops-repository`** /
  **`--gitops-branch`**. The cloud provider and networking install by default
  (reconciled directly, no GitOps required) and are committed to Git when the
  GitOps flags are set. `--include-networking` requires `--external-cloud-provider`
  (the ingress LoadBalancer is provisioned by the cloud controller manager), so
  `--external-cloud-provider=false` also disables networking; pass
  `--include-networking=false` to keep the cloud provider without ingress.
- **`ankra cluster upcloud create`** now matches OVH: **`--external-cloud-provider`**
  (UpCloud CCM + CSI) and the new **`--include-networking`** flag (Traefik +
  cert-manager) both default to **on** and no longer require GitOps - the
  cloud-provider/networking stacks are reconciled directly, and are additionally
  committed to Git when **`--gitops-credential-name`** and **`--gitops-repository`**
  are set. `--include-networking` requires `--external-cloud-provider` (the ingress
  LoadBalancer is provisioned by the cloud controller manager), so
  `--external-cloud-provider=false` also disables networking; pass
  `--include-networking=false` to keep the cloud provider without ingress.
- **`ankra cluster hetzner create`** reaches the same parity: new
  **`--external-cloud-provider`** (Hetzner CCM + CSI), **`--include-networking`**
  (Traefik + cert-manager), and **`--gitops-credential-name`** /
  **`--gitops-repository`** / **`--gitops-branch`** flags. The cloud-provider and
  networking stacks now install by default without GitOps (reconciled directly),
  and are committed to Git when the GitOps flags are set. `--include-networking`
  requires `--external-cloud-provider`, so `--external-cloud-provider=false` also
  disables networking; pass `--include-networking=false` to keep the cloud provider
  without ingress.
- **`ankra cluster ovh stop <cluster_id>`** and **`ankra cluster ovh start
  <cluster_id> [--scope all|control_plane]`** - stop an OVH cluster's compute
  while keeping its configuration, then start it again later (optionally bringing
  up only the control plane first).
- **`ankra cluster ovh access-info <cluster_id>`** - print the gateway (bastion)
  and control plane IPs along with ready-to-use `ssh -J` jump and Kubernetes API
  port-forward commands.
- **`ankra cluster ovh ssh-keys get <cluster_id>`** and **`ankra cluster ovh
  ssh-keys set <cluster_id> --ssh-key-credential-ids <id>,...`** - view and
  replace the SSH key credentials attached to an OVH cluster (changes apply on
  the next reconciliation).
- **`ankra cluster ovh node-group add`** now accepts **`--labels k=v,...`** and
  **`--taints k=v:Effect,...`** so a new node group can be created with its
  Kubernetes labels and taints in one step.
- **`ankra cluster ovh control-plane ...`** and **`ankra cluster ovh nodes
  ...`** now reach the public API: the control-plane and node-inspection
  endpoints are exposed on `/api/v1/clusters/ovh/...` (previously only
  available to the web UI), so these commands work against a token-authenticated
  CLI session.

### Changed

- **`--config <file>` now fully isolates per-invocation state.** A config file
  with an unfamiliar or missing extension (for example `--config /run/ankra/worker1`)
  is now parsed as YAML - the only format the CLI writes - instead of reading as
  empty and silently dropping the saved token and base URL. The active-cluster
  selection (`ankra cluster select`) is also keyed to the explicit `--config`
  path (stored alongside it as `<config>.selected.json`) rather than `$HOME`, so
  parallel runs against different config files no longer clobber each other's
  selection. **Migration:** if you previously ran with `--config` and relied on
  the `$HOME`-keyed selection, re-run `ankra cluster select` once to re-establish it.
- **`ankra support create` now shows the AI review before submitting.** Instead
  of a one-shot create that returned a terse "ticket flagged in review; retry
  with --force" on rejection, the command first calls the review endpoint and
  prints what it found: the specific reasons a request was flagged, clarifying
  questions that would speed up triage, and any existing ticket that may already
  track the same problem. When the review flags the request or finds a possible
  duplicate, you're asked to confirm interactively (`Submit this request anyway?
  [y/N]`); `--force` still skips the prompt and submits, and `-o json|yaml`
  callers get a `--force`-guidance error instead of a prompt. A clean request is
  submitted with no extra step.

### Deprecated

- The provider-specific **`ankra cluster {hetzner,ovh,upcloud} upgrade`**,
  **`scale`**, **`node-group <list|add|scale|upgrade|delete>`**, and
  **`deprovision`** commands are deprecated in favour of the cloud-agnostic
  `ankra cluster upgrade` / `scale` / `node-group` / `deprovision` verbs, which
  detect the provider automatically. The old commands still work and now print a
  runtime warning pointing at the replacement; they are scheduled for removal in
  v0.5.0. See `DEPRECATIONS.md`.

## v0.3.0 - 2026-06-11

First stable release of the v0.3.0 line. It consolidates everything shipped in
the **v0.3.0-rc0 → rc3** release candidates and adds direct kubeconfig, metrics,
support and stack-profile tooling on top, so you can drive an Ankra cluster
end-to-end from the terminal. Install it with the standard one-liner or
`ankra upgrade`; the beta channel is no longer required for the v0.3.0 features.

### Security

- **`ankra cluster encrypt manifest | addon` no longer produces files that only
  look encrypted.** SOPS' `encrypted_regex` matches YAML key names during tree
  traversal, not dotted paths, so `--key data.password` previously matched
  nothing: the file gained full `sops:` metadata (age recipient, mac) while the
  secret value stayed plaintext base64, and `encrypted_paths` was still updated.
  A dotted `--key` is now normalised to its last segment (`data.password` →
  `password`) with a notice, and after every encryption the CLI verifies the
  target key's value is real `ENC[...]` ciphertext - hard-failing before any
  file write or stack PATCH when SOPS encrypted nothing. The `--help` examples
  and the `ankra-sops-secrets` skill no longer steer users into the dotted-path
  form.

### Added

- **`ankra cluster kubeconfig add | remove | list`** and **`ankra cluster
  kube-token`** - wire `kubectl` straight to an Ankra cluster. `kube-token`
  prints a short-lived Kubernetes `ExecCredential` for use as a credential
  plugin, and `kubeconfig add` writes an `ankra-*` context (exec-based, or
  `--embed-token`) into your kubeconfig with atomic `0600` writes that preserve
  any foreign entries and use collision-safe context naming.
- **`ankra cluster metrics query | query-range`** - proxy a PromQL query (instant
  or range) to the cluster's Prometheus metrics source, with `table | json |
  yaml` output for ad-hoc inspection and CI.
- **`ankra support create | list | get | comment | attach | close`** - open and
  track Ankra support requests from the CLI, including image/screenshot
  attachments. Each request goes through a mandatory AI review; use `--force` to
  submit a request the reviewer flags.
- **`ankra stack-profiles list | export-iac | import`** - manage reusable,
  organisation-level stack profiles as `ClusterInfrastructureAsCode` YAML
  (export a profile version, import one from a file).
- **`ankra cluster draft`** - stage every stack in an `ImportCluster` as a
  reviewable draft instead of applying it; nothing is deployed by the command
  itself.
- **`ankra cluster validate`** - the offline `apply --dry-run` checks plus
  server-side chart-existence, plaintext-secret, and parent-reference
  validation; CI-friendly exit codes and `--strict-secrets`.
- **Self-update & beta channel** - `ankra upgrade` downloads, SHA-256-verifies
  and atomically swaps the binary, with `--version` pinning for upgrade,
  downgrade and rollback (`--allow-unverified` for releases that predate
  published checksums), and an `ankra config beta enable|disable|status`
  pre-release channel with semver-aware precedence (a stable release outranks
  its release candidates).
- **Offline dependency-tree and referenced-file validation** in
  `ankra cluster apply`, and **`--dry-run`** for `apply` / `delete cluster`
  (fully offline, no token, CI-friendly).
- **`--watch` and `-o json|yaml`** for `ankra cluster operations` list and
  steps.
- **Shared `-o json|yaml` output across commands**, and unexpected platform
  errors now print a hint to file the bug with `ankra support create`.
- **OVH command parity with the web UI**:
  - `ovh regions --credential-id <id>` - list the OVH Cloud regions a
    credential's project can actually deploy in.
  - `ovh stop <cluster_id>` and `ovh start <cluster_id>
    [--scope all|control_plane]` - stop a cluster's compute while keeping its
    configuration, then start it again later.
  - `ovh access-info <cluster_id>` - gateway (bastion) and control plane IPs
    with ready-to-use `ssh -J` jump and Kubernetes API port-forward commands.
  - `ovh ssh-keys get|set` - view and replace the SSH key credentials attached
    to a cluster (changes apply on the next reconciliation).
  - `ovh node-group add --labels k=v,... --taints k=v:Effect,...`, plus
    `node-group labels` / `node-group taints` to update existing groups (an
    empty value clears them; taint effect defaults to `NoSchedule`).
  - `ovh control-plane ...` and `ovh nodes ...` now reach the public API
    (`/api/v1/clusters/ovh/...`), so they work against a token-authenticated
    CLI session.

### Changed

- **`cluster apply` and the cloud `node-group` mutations (Hetzner, OVH,
  UpCloud)** submit async by default (`202 Accepted`); `--wait` blocks until
  the platform finishes and prints the full result (including the agent install
  command on first import), bounded by `--timeout` (default 10m).
- **`ankra cluster apply`** understands the `prometheus_metrics` spec field.

### Fixed

- **`ankra credentials get`** resolves a name to an ID (trying the v2
  platform-credential lookup before the legacy table).
- **`ankra org members` / `org current`** honour `--org` and validate the saved
  selection instead of sending a stale value.
- An unknown `--cluster` name fails clearly instead of forwarding a non-UUID
  value and producing an opaque server-side error.

### Details and examples

#### Stage changes as drafts with `ankra cluster draft`

`ankra cluster draft -f cluster.yaml` stages every stack in an ImportCluster YAML as a reviewable draft instead of applying it. The local checks run first (the same as `ankra cluster apply --dry-run`), then each stack is saved as a resource draft you can review, edit, and deploy from the Ankra stack builder - nothing is deployed by the command itself.

If the cluster does not exist yet it is imported first (live), since drafts can only be attached to an existing cluster. Stacks that already match the cluster's desired state are reported as `no changes` rather than creating an empty draft. The command exits non-zero if any stack fails validation.

```bash
ankra cluster draft -f cluster.yaml
```

#### Server-side validation with `ankra cluster validate`

`ankra cluster validate -f cluster.yaml` runs the same offline checks as `ankra cluster apply --dry-run` (structure, referenced-file YAML, parent/dependency tree) and then sends the spec to the Ankra API for the checks that need server-side data - checks the offline path cannot perform:

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
upgrade *or* downgrade - a pinned version installs whether it is newer, older
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

## v0.2.4 - May 2026

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

`ankra skills` installs the curated Ankra Agent Skills (for Cursor, Claude Code, and OpenClaw)
into a skills directory. The skills are embedded in the CLI binary, so installation works
offline and is versioned with the release.

```bash
ankra skills list                  # list available skills, marking installed ones
ankra skills install               # install all into ~/.cursor/skills (personal)
ankra skills install --editor claude-code  # install all into ~/.claude/skills
ankra skills install --project .   # install into ./.cursor/skills (project)
ankra skills install --editor claude-code --project .  # install into ./.claude/skills
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

Two new subcommands for in-place updates against the existing partial-stack endpoint - no more hand-editing the full `ImportCluster.yaml`.

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

- `--cluster <name|id>` - defaults to the selected cluster.
- `--stack <name>` - addons only, required when the same addon name exists in multiple stacks. Manifest names are globally unique on a cluster, so `manifests upgrade` has no `--stack` flag.
- `--registry-name`, `--registry-url`, `--registry-credential-name` - atomically retag the addon's registry.
- `--namespace` - destructive for addons (Helm reinstall); requires `--yes` or interactive confirmation.
- `--dry-run` - print the before/after YAML; no API write.
- `-o json|yaml` - machine-readable output for CI scripts.

All upgrades go through the same partial-stack endpoint as the UI, so they are atomic, locked, and produce a single git commit per invocation when gitops is enabled.

### API Endpoints

- `GET /api/v1/clusters/{provider}/{id}/control-plane` - read controller count, instance type and editability
- `PUT /api/v1/clusters/{provider}/{id}/control-plane` - change controller count (1 or 3)
- `PUT /api/v1/clusters/{provider}/{id}/control-plane/instance-type` - change controller instance type
- `GET /api/v1/clusters/{provider}/{id}/nodes` - list all managed servers for the cluster
- `GET /api/v1/clusters/{provider}/{id}/nodes/{node_id}` - full spec and metadata for a node

### Deprecations

- `ankra chat` currently uses the bearer-token streaming endpoints
  `/api/v1/chat/general` and `/api/v1/org/clusters/{cluster_id}/kubernetes/chat`.
  These are now deprecated and will be removed in a future release; the platform
  now responds with `Deprecation: true` and a `Sunset` header on these routes.
  When the warning prints, upgrade `ankra-cli` to the next release once a
  resumable session-based replacement has shipped on the platform.

## v0.1.129 - April 2026

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

Instance type upgrades are irreversible - Hetzner disk enlargement cannot be undone. To use a smaller type, create a new node group and delete the old one.

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
- **Graceful K8s cleanup**: K8s uninstall during node deletion is now best-effort - unreachable nodes (powered off, deleted) no longer block the delete operation.

### API Endpoints

- `GET /api/v1/clusters/hetzner/{id}/node-groups` - list node groups
- `POST /api/v1/clusters/hetzner/{id}/node-groups` - add a node group
- `PUT /api/v1/clusters/hetzner/{id}/node-groups/{name}/scale` - scale a node group
- `PUT /api/v1/clusters/hetzner/{id}/node-groups/{name}/instance-type` - upgrade instance type
- `PUT /api/v1/clusters/hetzner/{id}/node-groups/{name}/labels` - update labels
- `PUT /api/v1/clusters/hetzner/{id}/node-groups/{name}/taints` - update taints
- `DELETE /api/v1/clusters/hetzner/{id}/node-groups/{name}` - delete a node group

Same endpoints available for OVH (`/clusters/ovh/...`) and UpCloud (`/clusters/upcloud/...`).

---

## v0.1.128 - April 2026

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

- `POST /api/v1/clusters/upcloud` - create an UpCloud cluster
- `DELETE /api/v1/clusters/upcloud/{id}` - deprovision a cluster (returns operation ID)
- `GET /api/v1/clusters/upcloud/{id}/worker-count` - get worker count
- `POST /api/v1/clusters/upcloud/{id}/scale-workers` - scale workers
- `GET /api/v1/clusters/upcloud/{id}/k8s-version` - get Kubernetes version
- `POST /api/v1/clusters/upcloud/{id}/upgrade-k8s-version` - upgrade Kubernetes version
- `GET /api/v1/credentials/upcloud` - list UpCloud credentials
- `POST /api/v1/credentials/upcloud` - create an UpCloud credential
- `GET /api/v1/credentials/upcloud/ssh-keys` - list SSH key credentials
- `POST /api/v1/credentials/upcloud/ssh-key` - create an SSH key credential

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

- `POST /api/v1/clusters/ovh` - create an OVH cluster
- `DELETE /api/v1/clusters/ovh/{id}` - deprovision a cluster
- `GET /api/v1/clusters/ovh/{id}/worker-count` - get worker count
- `POST /api/v1/clusters/ovh/{id}/scale-workers` - scale workers
- `GET /api/v1/clusters/ovh/{id}/k8s-version` - get Kubernetes version
- `POST /api/v1/clusters/ovh/{id}/upgrade-k8s-version` - upgrade Kubernetes version
- `GET /api/v1/credentials/ovh` - list OVH credentials
- `POST /api/v1/credentials/ovh` - create an OVH credential
- `GET /api/v1/credentials/ovh/ssh-keys` - list SSH key credentials
- `POST /api/v1/credentials/ovh/ssh-key` - create an SSH key credential

---

## v0.1.126

### New Features

#### Hetzner Worker Scaling

Scale worker nodes on a Hetzner cluster up or down (1-10 nodes):

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

- `POST /api/v1/clusters/hetzner/{id}/scale-workers` - scale workers
- `GET /api/v1/clusters/hetzner/{id}/k8s-version` - fetch current k8s version
- `POST /api/v1/clusters/hetzner/{id}/upgrade-k8s-version` - trigger k8s version upgrade

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

- `GET /api/v1/clusters/hetzner/{id}/k8s-version` - fetch current k8s version
- `POST /api/v1/clusters/hetzner/{id}/upgrade-k8s-version` - trigger k8s version upgrade

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

## v0.1.122 and earlier - initial releases

Originally published as "Ankra CLI v1.0.0"; the tags actually shipped for this
initial line were `v0.1.115` through `v0.1.122`.

### Highlights

This release introduces the **Ankra CLI** - a powerful command-line interface for managing your Kubernetes infrastructure. Authenticate with SSO, chat with AI about your clusters, browse Helm charts, manage credentials, and control stacks - all from your terminal.

---

### New Features

#### SSO Authentication

Securely authenticate with the Ankra platform using browser-based SSO login with PKCE.

```bash
# Login to Ankra (opens browser for SSO)
ankra login

# Logout and clear credentials
ankra logout
```

Your credentials are securely stored in `~/.ankra.yaml` and automatically used for all subsequent commands.

---

#### AI-Powered Chat

Get instant help troubleshooting your infrastructure with AI-powered chat. Ask questions about your clusters, get recommendations, and analyze health issues.

##### Interactive Chat Mode

```bash
# Start an interactive chat session
ankra chat

# Chat with cluster context for better answers
ankra chat --cluster my-production-cluster
```

##### One-Shot Questions

```bash
# Ask a single question
ankra chat "Why are my pods in CrashLoopBackOff?"

# Ask with cluster context
ankra chat --cluster staging "How do I scale my deployment?"
```

##### Cluster Health Analysis

```bash
# Get AI-analyzed cluster health for the selected cluster
ankra chat health

# Include detailed AI analysis
ankra chat health --ai
```

##### Chat History Management

```bash
# List previous conversations
ankra chat history

# Show a specific conversation
ankra chat show <conversation_id>

# Delete a conversation
ankra chat delete <conversation_id>
```

---

#### Helm Charts

Browse and search the Helm chart catalog directly from your terminal.

##### List Available Charts

```bash
# List all available charts
ankra charts list

# Paginate through charts
ankra charts list --page 2 --page-size 50

# Show only subscribed charts
ankra charts list --subscribed
```

##### Search Charts

```bash
# Search for charts by name
ankra charts search nginx

# Search for monitoring solutions
ankra charts search prometheus
```

##### Chart Information

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

#### Credentials Management

Manage cloud provider and Git credentials for your clusters.

##### List Credentials

```bash
# List all credentials
ankra credentials list

# Filter by provider
ankra credentials list --provider github
```

##### View Credential Details

```bash
# Get details of a specific credential
ankra credentials get <credential_id>
```

##### Validate & Delete

```bash
# Check if a credential name is available
ankra credentials validate my-new-credential

# Delete a credential
ankra credentials delete <credential_id>
```

**Aliases:** `ankra creds`, `ankra cred`, `ankra credential`

---

#### Stack Management

Create, manage, and track infrastructure stacks on your clusters.

##### List & View Stacks

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

##### Create & Delete Stacks

```bash
# Create a new stack
ankra cluster stacks create my-new-stack --description "Application stack"

# Delete a stack
ankra cluster stacks delete old-stack
```

##### Rename & History

```bash
# Rename a stack
ankra cluster stacks rename old-name new-name

# View change history for a stack
ankra cluster stacks history my-stack
```

---

#### Cluster Clone

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

#### API Tokens

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

#### Cluster Operations

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

### Bug Fixes

#### `ankra cluster clone` - Registry Linkage Fix

Fixed an issue where `ankra cluster clone` did not correctly format the linkage to existing registries when cloning stacks or entire clusters. Addon configurations that reference container registries (`registry_name`, `registry_url`, `registry_credential_name`) are now properly preserved and formatted in the cloned configuration.

**Before:** Registry references in cloned addons could be malformed or missing, causing deployment failures when the cloned cluster tried to pull images from private registries.

**After:** All registry linkage fields are correctly preserved and formatted, ensuring seamless deployments with private container registries.

---

#### `ankra chat` - API Request & Response Format Fix

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

### Getting Started

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

### Configuration

The CLI stores configuration in `~/.ankra.yaml`:

- **token**: Your API authentication token
- **base-url**: The Ankra platform URL (defaults to https://platform.ankra.app)

You can also use environment variables:

- `ANKRA_API_TOKEN`: Override the stored token
- `ANKRA_BASE_URL`: Override the base URL

---

**Full documentation:** https://docs.ankra.app/cli
