---
name: ankra-cloud-clusters
description: Provision and operate clusters Ankra builds on cloud infrastructure - Hetzner, OVHcloud, UpCloud, DigitalOcean, Proxmox VE, HPE Morpheus and Scaleway - choosing the region and the right instance family, picking kubeadm (the default) or k3s and the etcd topology, wiring the generated stack straight into a GitOps repository, taking the ingress/DNS/TLS batteries at create time, then scaling, node groups, availability zones, upgrades and teardown. Use when the user wants Ankra to build a cluster rather than import one, asks which server type or region to pick, or mentions Hetzner, OVH, UpCloud, DigitalOcean, Proxmox, Morpheus or Scaleway clusters.
---

# Ankra cloud clusters

Ankra can build a cluster on infrastructure you own an account with, install the agent, and bring
up ingress, DNS and TLS in the same pass. `ankra cluster <provider> create` is the entry point;
after creation almost everything is a **provider-agnostic** verb (`ankra cluster scale`,
`ankra cluster upgrade`, `ankra cluster node-group ...`) because Ankra detects the provider from
the cluster.

Providers: `hetzner`, `ovh`, `upcloud`, `digitalocean`, `proxmox`, `morpheus`. (`scaleway` exists
for lifecycle verbs but has no `create` — build Scaleway Kubernetes as managed Kapsule instead,
see `ankra-managed-kubernetes`.)

For a control plane the *provider* runs — DOKS, UKS, GKE, OVH MKS, AKS, EKS, Kapsule — use
`ankra cluster managed ...` and the `ankra-managed-kubernetes` skill instead. Rule of thumb: this
skill when you want cluster-admin over the control plane, node-level control, or a provider with no
managed offering; `managed` when you want the provider to own the control plane's uptime.

## 1. Credentials first

`create` takes credential **IDs**, not names, so store them and read the IDs back.

```bash
ankra credentials hetzner create --name hetzner-prod
ankra credentials hetzner ssh-key create --name ops-key
ankra credentials ovh create --name ovh-prod
ankra credentials upcloud create --name upcloud-prod
ankra credentials digitalocean create --name do-prod
ankra credentials proxmox create --name pve-lab
ankra credentials morpheus create --name morpheus-prod

ankra credentials list                 # the IDs to pass to create
ankra credentials get <name>
ankra credentials validate <name>
```

Every provider group has `create`, `list` and `ssh-key`. Scope the token at the provider to the
project Ankra manages — see `ankra-security`.

## 2. Choose the region and the instance family *before* you create

This is the step people skip and then rebuild. Each provider names its compute differently, and
each has a discovery command that lists what the credential can actually deploy:

| Provider | Region/location | Instance family | Create flags name it |
|----------|-----------------|-----------------|----------------------|
| Hetzner | `ankra cluster hetzner locations` | `ankra cluster hetzner server-types --location <loc> --available-only` | `--worker-server-type`, `--control-plane-server-type` |
| DigitalOcean | `ankra cluster digitalocean regions` | `ankra cluster digitalocean sizes --region <r> --available-only` | `--worker-size`, `--control-plane-size` |
| OVH | `ankra cluster ovh regions [--with-zones]` | flavors (`b2-*`, `c2-*`, `r2-*`, `t1/t2-*` GPU) | `--worker-flavor-id`, `--control-plane-flavor-id` |
| UpCloud | zones (`--zone`) | plans (`2xCPU-4GB`, …) | `--worker-plan`, `--control-plane-plan` |
| Proxmox VE | `ankra cluster proxmox hosts` | `ankra cluster proxmox sizes` (presets) | see `proxmox create --help` |
| Morpheus | `ankra cluster morpheus clouds` / `groups` | `ankra cluster morpheus plans`, `layouts` | see `morpheus create --help` |

All discovery commands take `--credential-id`. **A region not enabled on the account fails the
reconcile late, at private-network setup** — list first rather than guessing from the provider's
public docs.

Kubernetes versions:

```bash
ankra cluster kubeadm-versions      # vanilla Kubernetes targets (the default)
ankra cluster k3s-versions          # k3s targets
```

[reference.md](reference.md) has the per-provider family cheat-sheet — what to pick for a control
plane, a general worker, a memory-heavy or CPU-heavy workload, and GPU.

## 3. Create

The flag shape is the same everywhere; only the compute flag names change.

```bash
ankra cluster hetzner create \
  --name prod \
  --credential-id <hetzner_cred_id> \
  --ssh-key-credential-id <ssh_key_id> \
  --location nbg1 \
  --control-plane-count 3 --control-plane-server-type cx33 \
  --worker-count 3 --worker-server-type cx43 \
  --distribution kubeadm \
  --kubernetes-version <from kubeadm-versions>
```

```bash
ankra cluster digitalocean create \
  --name prod --credential-id <do_cred_id> --ssh-key-credential-id <ssh_key_id> \
  --region nyc3 --control-plane-size s-2vcpu-4gb --worker-size s-2vcpu-4gb --worker-count 3

ankra cluster upcloud create \
  --name prod --credential-id <uc_cred_id> --ssh-key-credential-id <ssh_key_id> \
  --zone fi-hel1 --control-plane-plan 2xCPU-4GB --worker-plan 4xCPU-8GB --cni cilium

ankra cluster ovh create \
  --name prod --credential-id <ovh_cred_id> --ssh-key-credential-id <ssh_key_id> \
  --region GRA9 --control-plane-flavor-id b2-15 --worker-flavor-id b2-30
```

`create` submits and returns; it does not take `--wait`. Track it with
`ankra cluster operations list` and `ankra cluster info`.

### Distribution and etcd topology

`--distribution kubeadm` (default: vanilla upstream Kubernetes with containerd and Cilium) or `k3s`
(lightweight; never a default). For kubeadm, `--etcd-topology stacked` (default) puts
etcd on the control planes; `external` gives it dedicated VMs (`--etcd-node-count 3|5`,
`--etcd-server-type` / `--etcd-flavor-id` / `--etcd-plan` / `--etcd-size`).

**`--control-plane-count 3` is what makes a cluster survive losing a node** — one control plane is
fine for dev and is a single point of failure everywhere else. Count is 1 or 3.

### The batteries — take them at create time

Three flags decide whether the cluster is usable the moment it comes up. All default **on**:

- **`--external-cloud-provider`** — installs the provider's CCM and CSI, so `LoadBalancer`
  Services get a real load balancer and `PersistentVolumeClaim`s get real disks. Turning it off
  also disables `--include-networking`.
- **`--include-networking`** — Traefik + cert-manager for ingress. Requires the CCM (the ingress
  LoadBalancer is provisioned by it).
- **`--include-dns`** — gives the cluster its own subdomain under `ankra.cc` (or the
  organisation's own root domain) and installs external-dns, so an Ingress hostname under that
  subdomain gets its DNS record **and its TLS certificate** with no manual setup.

Scope worth knowing: the generated external-dns manages **only** the generated subdomain. Ingress
hosts on your own domains are ignored and their records stay yours to create — declare those with
`ankra org custom-dns-zones` / `ankra cluster custom-dns-zones`. See `ankra-domains-dns`.

Pass `--include-dns=false` / `--include-networking=false` only when you are bringing your own
ingress and DNS, and say so in the stack.

### Commit the generated stack to Git at create time

Ankra generates a cloud-provider stack (CCM, CSI, ingress, external-dns) for the new cluster. Point
`create` at a repository and it lands there as a commit instead of existing only in the platform:

```bash
ankra cluster hetzner create ... \
  --gitops-repository my-org/platform-gitops \
  --gitops-credential-name github-acme \
  --gitops-branch main
```

Do this on the first create rather than wiring GitOps afterwards: it is the difference between a
cluster whose base layer is reviewable in a pull request and one whose base layer only exists in
the platform. `ankra cluster gitops status` shows what a cluster syncs from. See `ankra-gitops`.

### OVH availability zones

```bash
ankra cluster ovh regions --credential-id <id> --with-zones      # only region-3-az regions take zones
ankra cluster ovh create --name prod --region EU-WEST-PAR \
  --availability-zones eu-west-par-a,eu-west-par-b,eu-west-par-c \
  --control-plane-count 3
ankra cluster node-group add <cluster_id> --name db-par-a \
  --instance-type b2-15 --availability-zone eu-west-par-b
```

Zone names are region-scoped — read them from `--with-zones`, never guess. Spreading needs
`--control-plane-count 3` for etcd quorum. An instance's zone is fixed at creation: a worker can be
re-placed by adding a pinned group and draining the old one, a control plane cannot.

**Node spread alone is not zone fault tolerance.** OVH block storage is zonal and cannot attach
across zones, so a stateful workload also needs one replica and one volume per zone. And on a
zone-spread cluster an *unpinned* group's nodes each take the thinnest zone cluster-wide, so a
one-node group lands wherever the cluster is thinnest — pin the group when it runs zonal storage.

### UpCloud multi-zone clusters

UpCloud has no multi-zone region - every private network and load balancer is zone-local - so
Ankra stretches a cluster across zones itself: one private network per zone and a platform-managed
WireGuard mesh between the nodes (kubeadm only). Needs the organisation's `network_overlay`
feature; without it the flags are refused as "not supported for upcloud clusters".

```bash
ankra cluster upcloud create --name prod --zone fi-hel1 \
  --zones fi-hel1,fi-hel2,se-sto1 --control-plane-count 3 \
  --credential-id <id> --ssh-key-credential-id <id>
ankra cluster upcloud zones <cluster_id> --zones fi-hel1,fi-hel2,se-sto1,de-fra1   # grow the pool
ankra cluster upcloud node-group add <cluster_id> --name db-sto --instance-type 4xCPU-8GB --count 2 --zone se-sto1
```

`--zone` is the primary (bastion, first control plane, default network) and must be in `--zones`. A
pool needs **at least 3 zones and `--control-plane-count 3`** so etcd keeps quorum when any one
zone is lost; two-zone pools are refused. Control planes spread one per zone and node groups
spread across the pool unless pinned with `--zone`. A single-zone cluster that may grow later is
created with `--network-mode wireguard_mesh`; the mesh cannot be retrofitted, and pools only ever
grow.

**What stays in the primary zone:** LoadBalancer Services (the CCM is scoped to primary-zone
nodes) and PersistentVolumes (the UpCloud CSI provisions in one zone). Pin stateful groups to the
primary zone, or run replicated storage (Longhorn) for workloads in other zones. `ankra cluster
node-group list` shows `zones=` per group.

## 4. Operate — mostly provider-agnostic

The provider is detected from the cluster, so these work everywhere:

```bash
ankra cluster scale <cluster_id> 5                    # default worker pool
ankra cluster upgrade <cluster_id> <target_version>   # kubeadm or k3s; --force to override a blocked drain
ankra cluster ssh-keys get|set|resync <cluster_id>

ankra cluster node-group list <cluster_id>
ankra cluster node-group add <cluster_id> --name gpu --instance-type <type> --count 2 --wait
ankra cluster node-group scale <cluster_id> <group> <count>
ankra cluster node-group labels <cluster_id> <group> --labels k=v,...
ankra cluster node-group taints <cluster_id> <group> --taints k=v:NoSchedule,...
ankra cluster node-group autoscaling get|set <cluster_id> <group>
ankra cluster node-group upgrade <cluster_id> <group>   # instance type change, IRREVERSIBLE
ankra cluster node-group delete <cluster_id> <group>
```

Upgrades roll one node at a time, control plane first: each node is cordoned, drained respecting
PodDisruptionBudgets, upgraded, and gated on being Ready at the target version. An etcd snapshot is
taken before the control plane upgrade. A drain blocked by a PDB aborts the rollout. Downgrades and
skipping a minor version are not supported.

Per-provider reads and power:

```bash
ankra cluster <provider> k8s-version <cluster_id>
ankra cluster <provider> workers <cluster_id>
ankra cluster <provider> nodes list|get <cluster_id> [node]
ankra cluster <provider> control-plane get|set-count|set-instance-type <cluster_id>
ankra cluster <provider> bastion ...
ankra cluster <provider> stop <cluster_id>            # power off, keep state
ankra cluster <provider> start <cluster_id>
ankra cluster ovh access-info <cluster_id>            # bastion/control-plane IPs, ssh -J commands
ankra cluster power-schedules ...                     # scheduled stop/start
```

## 5. Teardown — three different things

Do not confuse these; the words are similar and the outcomes are not.

| Command | What it does |
|---------|--------------|
| `ankra cluster <provider> stop` / `start` | **Power.** Compute off and on, state kept. Also `ankra cluster power-schedules`. |
| `ankra cluster deprovision <name>` | **Teardown.** Releases every cloud resource (servers, networks, SSH keys) and uninstalls every stack resource. The cluster *record* is kept. |
| `ankra cluster provision <name>` | Rebuilds a deprovisioned cluster's infrastructure and redeploys its stacks from the stored definition. Not a power-on. |
| `ankra delete cluster <name>` | Removes the cluster record itself (`--dry-run` first). |

```bash
ankra cluster info                                     # confirm the target, every time
ankra cluster deprovision prod --yes
ankra cluster deprovision prod --force                 # also clears leftover CSI volumes and LBs
```

`--force` on UpCloud, Hetzner, OVH and DigitalOcean also deletes leftover CSI storage volumes and
load balancers — without it, a teardown can leave paid-for orphans behind. After a `provision`,
verify anything that was patched in place: `ankra cluster addons list`, `ankra cluster manifests list`.

## Rules

- **Discover, then create.** Region and instance family come from the provider's own listing for
  that credential, not from memory.
- **Credential IDs, not names**, on `create`.
- **`--control-plane-count 3` for anything that must survive a node loss.** 1 or 3, nothing else.
- **Take the batteries on the first create.** Retrofitting CCM, ingress and external-dns onto a
  running cluster is strictly more work than asking for them.
- **Wire GitOps at create time** so the base layer is reviewable from day one.
- **`create` has no `--wait`.** Follow it with `ankra cluster operations list`; do not re-submit.
- **Pin the group when it runs zonal storage**, and remember node spread is not fault tolerance.
- **Node-group `upgrade` is irreversible** — it replaces instances.
- **Confirm before teardown**, and prefer `--force` on cloud providers so orphaned volumes and load
  balancers are not left billing.
- **Upgrade non-production first**, one minor at a time.

## Related skills

- `ankra-getting-started` — the whole path from an empty organisation to a first app.
- `ankra-managed-kubernetes` — DOKS, UKS, GKE, OVH MKS, AKS, EKS, Kapsule.
- `ankra-import-cluster` — adopting a cluster that already exists.
- `ankra-domains-dns` — the generated domain, your own zones, and where TLS comes from.
- `ankra-gitops` — the repository `--gitops-repository` commits into.
- `ankra-stacks-addons` — deploying onto the cluster once it is up.
- `ankra-troubleshooting` — when a create or upgrade does not converge.
