---
name: ankra-cloud-clusters
description: Provision and operate clusters Ankra builds on cloud infrastructure - Hetzner, OVHcloud, UpCloud, DigitalOcean, Scaleway, AWS (self-managed EC2), Ankra Cloud servers, Proxmox VE and HPE Morpheus - laying out a hub-and-spoke estate (infrastructure cluster first, then production and development), choosing the region and the right instance family, picking kubeadm (the default) or k3s and the etcd topology, wiring the generated stack straight into a GitOps repository, taking the ingress/DNS/TLS batteries at create time, then scaling, node groups, availability zones, upgrades and teardown. Use when the user wants Ankra to build a cluster rather than import one, plans a multi-cluster platform, asks which server type or region to pick, or mentions Hetzner, OVH, UpCloud, DigitalOcean, Scaleway, AWS EC2 (not EKS), Ankra Cloud, Proxmox or Morpheus clusters, or the playground.
---

# Ankra cloud clusters

Ankra can build a cluster on infrastructure you own an account with, install the agent, and bring
up ingress, DNS and TLS in the same pass. `ankra cluster <provider> create` is the entry point;
after creation almost everything is a **provider-agnostic** verb (`ankra cluster scale`,
`ankra cluster upgrade`, `ankra cluster node-group ...`) because Ankra detects the provider from
the cluster.

## Recommended path

- **Golden paths:** *The estate* (hub-and-spoke) on top of *GitOps as the source of truth* - see
  `ankra-platform-principles`. Build the **infrastructure (hub) cluster first** (three control
  planes), then one spoke (production, three control planes; development may run one), and grow
  from there. The full worked example is the docs use case *Production Multi-Cluster Platform*.
- **First:** credentials, then discover region and instance family, then `create` **with the
  `--gitops-*` flags** so the generated stacks land in the one platform repository from minute one.
- **Then hand over:** stacks to `ankra-gitops` / `ankra-stacks-addons`, central metrics and logs
  to `ankra-observability`, vaults to `ankra-backups`, the on-call channel to `ankra-alerts-webhooks`.
- **Avoid:** clicking clusters into existence with no repository, ingress-nginx instead of the
  Traefik Ankra installs, and `kubectl`/`helm` against the result - use the `ankra` CLI.
- Organisations already on Terraform can declare the same clusters with `ankra_<provider>_cluster`
  and `ankra_node_group` - see `ankra-terraform`; keep one source of truth per cluster.

Providers: `hetzner`, `ovh`, `upcloud`, `digitalocean`, `scaleway`, `aws`, `proxmox`, `morpheus`,
`ankracloud`. Every one has `create`; `scaleway`, `aws` and `ankracloud` also have `preflight`,
which checks the same flags without building anything. Scaleway builds on Instances behind a Public
Gateway, and `aws` builds k3s/kubeadm on plain EC2 inside a VPC you already own (never EKS - for
EKS use `ankra cluster managed`). The AWS create adopts your networking, so it takes `--vpc-id`,
`--node-subnet-ids`, `--bastion-subnet-id` and `--bastion-allowed-ips` on top of the usual flags;
list them with `ankra cluster aws vpcs|subnets|availability-zones --credential-id <id> --region
<r>` (`subnets` shows each subnet's egress kind, which is what decides `--egress-mode`). The AWS
credential is the organisation's existing AWS credential - an assumable role connected with
`ankra credentials aws onboarding --scope self_managed` then `create-role`, or an access key pair
with `create-keys`; `ankra credentials aws list` shows the ids. A role onboarded with scope `cost`
cannot build clusters. The SSH key credential is organisation-wide: `ankra credentials ssh-key create`.

`ankracloud` (alias `ankra-cloud`; closed beta, enabled per organisation on request) builds kubeadm
(default) or k3s (`--distribution k3s`) on Ankra Cloud servers. Servers are sized by **plan**, not
instance type: `--bastion-plan`, `--control-plane-plan`, `--worker-plan`, `--etcd-plan`. Browse
with `ankra cluster ankracloud zones|plans|templates|networks|pricing --credential-id <id>`
(`plans --cluster <id>` for day-2 resizes). The credential is an Ankra Cloud API token:
`ankra credentials ankracloud create --name <n> --token-stdin` (or `ANKRA_CLOUD_API_TOKEN`).
`--network-mode ipv4_nat|ipv6_only` picks the network layout (omit it for the server default,
`ipv4_nat`): a zone with no public IPv4 left needs `ipv6_only`, and the `preflight` says so — rerun
`ankra cluster ankracloud preflight ... --network-mode ipv6_only`, then `create` with the same flag.

For a control plane the *provider* runs - DOKS, UKS, GKE, OVH MKS, AKS, EKS, Kapsule, Ankra Cloud
Kubernetes - use `ankra cluster managed ...` and the `ankra-managed-kubernetes` skill instead. Rule
of thumb: this skill when you want cluster-admin over the control plane, node-level control, or a
provider with no managed offering; `managed` when you want the provider to own the control plane's
uptime. To just try something, `ankra cluster playground create` gives the organisation one
short-lived virtual cluster with the agent installed (`plans`, `status`, `resize`, `destroy`).

## 1. Credentials first

`create` takes credential **IDs**, not names, so store them and read the IDs back.

```bash
ankra credentials hetzner create --name hetzner-prod
ankra credentials ssh-key create --name ops-key --public-key-file ~/.ssh/id_ed25519.pub   # org-wide, any provider
ankra credentials ovh create --name ovh-prod
ankra credentials upcloud create --name upcloud-prod
ankra credentials digitalocean create --name do-prod
ankra credentials proxmox create --name pve-lab
ankra credentials morpheus create --name morpheus-prod
ankra credentials aws onboarding --scope self_managed          # external id + launch-stack URL
ankra credentials aws create-role --name aws-prod --role-arn <arn> --external-id <id> --scope self_managed
ankra credentials aws create-keys --name aws-prod --access-key-id AKIA...   # prompts for the secret

ankra credentials list                 # the IDs to pass to create
ankra credentials get <name>           # an ssh-key credential shows its public key and SHA256 fingerprint
ankra credentials validate <name>
ankra credentials ssh-key list
```

Every provider group has `create` and `list`. An SSH key credential belongs to the organisation, not a provider: `ankra credentials ssh-key create --name <n> --public-key-file <path.pub>` (or `--public-key "<key>"`, or `--generate --private-key-file <path>`), and the platform refuses a key the organisation already holds, naming the credential that has it. Scope the token at the provider to the
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
| Scaleway | `ankra cluster scaleway locations` | `scaleway instance-types`, `gateway-types` | `--worker-type`, `--control-plane-type`, `--gateway-type` |
| AWS (EC2) | `ankra cluster aws regions`, `availability-zones` | `aws instance-types`, `images`, `pricing` | `--worker-type`, `--control-plane-type` |
| Ankra Cloud | `ankra cluster ankracloud zones` | `ankracloud plans`, `pricing` | `--worker-plan`, `--control-plane-plan` |
| Proxmox VE | `ankra cluster proxmox hosts`, `storages`, `bridges`, `templates` | `ankra cluster proxmox sizes` (presets) | `--worker-instance-type`, `--control-plane-instance-type` |
| Morpheus | `ankra cluster morpheus clouds` / `groups` / `networks` | `ankra cluster morpheus plans`, `layouts` | `--worker-plan-id`, `--control-plane-plan-id` |

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
`ankra cluster operations list` and `ankra cluster list` until the cluster reads online.

### Distribution and etcd topology

`--distribution kubeadm` (default: vanilla upstream Kubernetes with containerd and Cilium) or `k3s`
(lightweight; never a default). For kubeadm, `--etcd-topology stacked` (default) puts
etcd on the control planes; `external` gives it dedicated VMs (`--etcd-node-count 3|5`,
`--etcd-server-type` / `--etcd-flavor-id` / `--etcd-plan` / `--etcd-size`).

**`--control-plane-count 3` is what makes a cluster survive losing a node** - one control plane is
fine for dev and is a single point of failure everywhere else. Count is 1 or 3;
`ankra cluster <provider> control-plane set-count <cluster> 1|3` can change it later, but choose
3 at create for the hub and production. The distribution, CNI (`--cni`), etcd topology and network
mode cannot be changed after create.

### The batteries - take them at create time

These flags decide whether the cluster is usable the moment it comes up. All default **on**
(`--external-cloud-provider` exists on Hetzner, DigitalOcean, OVH and UpCloud; the others install
their cloud integration as part of the generated stack):

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
the platform. Ankra writes each cluster to `clusters/<name>-<short-id>/` in the repository, so
every cluster of the estate can share one repository. Always pass `--gitops-branch`: the server
default is `master`. The flags exist on Hetzner, DigitalOcean, OVH, UpCloud, AWS and Ankra Cloud;
for Scaleway, Proxmox and Morpheus connect the repository after create (see `ankra-gitops`).
`ankra cluster gitops status` shows what a cluster syncs from.

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
ankra cluster node-group add <cluster_id> --name db-sto --instance-type 4xCPU-8GB --count 2 --zone se-sto1
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
ankra cluster ssh-keys get|set|resync <cluster_id>   # get names each attached key; set REPLACES the attached set

ankra cluster node-group list <cluster_id>
ankra cluster node-group add <cluster_id> --name gpu --instance-type <type> --count 2 --wait
ankra cluster node-group scale <cluster_id> <group> <count>
ankra cluster node-group labels <cluster_id> <group> --labels k=v,...
ankra cluster node-group taints <cluster_id> <group> --taints k=v:NoSchedule,...
ankra cluster node-group autoscaling get|set <cluster_id> <group> [--enabled --min 2 --max 6]
ankra cluster node-group upgrade <cluster_id> <group> <instance_type>   # IRREVERSIBLE
ankra cluster node-group delete <cluster_id> <group>
```

The per-provider `ankra cluster hetzner|ovh|upcloud node-group ...` forms are deprecated; use the
generic verbs above. Node-group writes submit and return unless `--wait` is passed; a refusal that
arrives later (no stock in the location, for example) is recorded as a failed operation - read it
with `ankra cluster operations list --failed --cluster <cluster>`. On UpCloud a plan from another
family replaces each node (drained, deleted, re-provisioned) instead of resizing it in place.

Removing workers (`cluster scale` or `node-group scale` down, `node-group delete`) drains each
node through its pods' PodDisruptionBudgets first. A node whose drain is refused (a budget allows
no eviction, or the node is dead) is **kept in service, not removed**, and a notice on the cluster
says why, so a command that "succeeded" can leave the group at its old size. Fix the cause (more
replicas, a looser budget) and re-run. `--force-drain` on `cluster scale`, `node-group scale`,
`node-group delete` and `node-group upgrade` bypasses PodDisruptionBudgets for that one request:
the node is removed (for `upgrade`, power-cycled into the new type) even if its pods' disruption
budget refuses the drain. Use it only for a node the user has decided to lose, and say so before
running it; never add it just to make a stuck scale-down go through.

```bash
ankra cluster node-group scale <cluster_id> <group> 2 --force-drain
ankra cluster node-group delete <cluster_id> <group> --force-drain --yes
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
ankra cluster <provider> stop <cluster_id>            # terminate the VMs after capturing state (--mode pause keeps them)
ankra cluster <provider> start <cluster_id>
ankra cluster ovh access-info <cluster_id>            # bastion/control-plane IPs, ssh -J commands
ankra cluster power-schedules ...                     # scheduled stop/start
```

`control-plane set-instance-type` on a running cluster with three controllers is a rolling resize:
each controller is drained, honouring its pods' PodDisruptionBudgets, and resized one at a time.
`--force-drain` bypasses PodDisruptionBudgets for that drain, so pods whose budget refuses eviction
are evicted anyway. It applies to that one request and only matters for the live rolling resize: a
stopped cluster's offline resize drains nothing and ignores it, and so does a running cluster with
fewer than three controllers (the rolling resize refuses it) or one already on that type. Same rule
as for workers: say so before using it, never add it by reflex.

## 5. Teardown - power is not deletion

Do not confuse these; the words are similar and the outcomes are not.

| Command | What it does |
|---------|--------------|
| `ankra cluster <provider> stop` / `start` | **Power.** Compute off and on, state kept. Also `ankra cluster power-schedules`. |
| `ankra cluster deprovision <name>` | **Teardown and deletion** for a cloud cluster: releases every cloud resource (servers, networks, SSH keys), uninstalls every stack resource, and **deletes the cluster record** - its id and stacks are gone and `provision` cannot bring it back. Rebuilding means a new `create` (which is why the stacks belong in Git). |
| `ankra cluster provision <name>` | Only for *imported* clusters, whose record survives a deprovision: redeploys their stacks from the stored definition. |
| `ankra delete cluster <name>` | Removes a cluster record (`--dry-run` first). |

```bash
ankra cluster info                                     # confirm the target, every time
ankra cluster deprovision prod                         # names the volumes it will delete, then asks
ankra cluster deprovision prod --force --accept-volume-data-loss --yes
```

On Hetzner, OVH, UpCloud, DigitalOcean, Scaleway, Ankra Cloud and AWS the cluster's CSI volumes are
deleted with it, and only after the prompt is answered or `--accept-volume-data-loss` is passed
(`--yes` does not imply it). A cluster created with `--retention-policy retain` (AWS, Scaleway,
Ankra Cloud; their default) keeps its volumes, which keep billing. `--force` tolerates an
unexpected state and also deletes leftover load balancers on UpCloud, Hetzner, OVH, DigitalOcean
and AWS; on Proxmox it finishes when the host is unreachable and leaves the VMs behind.

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
- **Confirm before teardown** - a cloud `deprovision` deletes the cluster and, once accepted, its
  volumes. Back stateful stacks up into a vault first (`ankra-backups`).
- **Upgrade non-production first**, one minor at a time: development, then production, then the hub.

## Related skills

- `ankra-getting-started` — the whole path from an empty organisation to a first app.
- `ankra-managed-kubernetes` — DOKS, UKS, GKE, OVH MKS, AKS, EKS, Kapsule, Ankra Cloud Kubernetes.
- `ankra-observability`, `ankra-backups`, `ankra-alerts-webhooks` — the hub's services and the spokes' wiring.
- `ankra-terraform` — the same clusters as Terraform resources.
- `ankra-import-cluster` — adopting a cluster that already exists.
- `ankra-domains-dns` — the generated domain, your own zones, and where TLS comes from.
- `ankra-gitops` — the repository `--gitops-repository` commits into.
- `ankra-stacks-addons` — deploying onto the cluster once it is up.
- `ankra-troubleshooting` — when a create or upgrade does not converge.
