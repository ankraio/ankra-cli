---
name: ankra-managed-kubernetes
description: Provision, import, and operate provider-managed Kubernetes through Ankra - DigitalOcean DOKS, UpCloud UKS, Google GKE, OVHcloud MKS, Azure AKS, Amazon EKS, Scaleway Kapsule and Ankra Cloud Kubernetes - creating a cluster with its first node pool and GitOps wiring as a hub or spoke of the estate, discovering and adopting clusters that already exist, node pools and autoscaling, version upgrades, and deletion. Use when the user wants a managed Kubernetes cluster (the provider runs the control plane), mentions DOKS, UKS, GKE, AKS, EKS, OVH MKS, Kapsule or Ankra Cloud Kubernetes, or wants to import a managed cluster into Ankra.
---

# Ankra managed Kubernetes

The provider runs the control plane; Ankra manages the node pools, upgrades, addons, stacks and
GitOps on top. This is the counterpart to `ankra-cloud-clusters` (self-managed kubeadm/k3s on VMs).

Choose **managed** when you want the provider responsible for control-plane uptime and patching, and
you do not need cluster-admin over it. Choose **cloud clusters** when you need control-plane access,
a provider with no managed offering, or node-level control the managed API does not expose.

## Recommended path

- **Golden paths:** *The estate* (hub-and-spoke) on *GitOps as the source of truth* - see
  `ankra-platform-principles`. Managed clusters are first-class spokes or hubs: create the
  infrastructure cluster first, then one spoke, each **with the `--gitops-*` flags** into the one
  platform repository, then wire central metrics, logs, backups and alerts (`ankra-observability`,
  `ankra-backups`, `ankra-alerts-webhooks`).
- **Already running at the provider?** `discover` then `import` - never hand-install the agent.
- **Avoid:** creating pools or upgrading in the provider console behind Ankra's back, and
  `kubectl`/`helm` against the cluster - use the `ankra` CLI.
- Terraform users declare the same thing with `ankra_managed_cluster` and `ankra_managed_node_pool`
  (see `ankra-terraform`); one source of truth per cluster.

Providers, exactly as `--provider` spells them:

`doks` (DigitalOcean) · `uks` (UpCloud) · `gke` (Google) · `ovh_mks` (OVHcloud) · `aks` (Azure) ·
`eks` (AWS) · `kapsule` (Scaleway) · `ankracloud_k8s` (Ankra Cloud Kubernetes, closed beta enabled
per organisation on request - see below)

**Every `ankra cluster managed` subcommand takes `--provider`.**

## 1. A provider credential

```bash
ankra credentials list                       # find an existing credential ID
ankra credentials digitalocean create --name do-prod     # doks
ankra credentials upcloud create --name upcloud-prod     # uks
ankra credentials ovh create --name ovh-prod             # ovh_mks (needs a Public Cloud project)
ankra credentials azure create --name aks-prod --subscription-id <s> --tenant-id <t> --client-id <c>
ankra credentials scaleway create --name scw-prod --project-id <p> --access-key <k>
ankra credentials aws onboarding --scope provisioning     # eks: role via launch stack, then create-role
ankra credentials ankracloud create --name cloud --token-stdin   # ankracloud_k8s (optional, see below)
```

The Azure client secret and the Scaleway secret key are prompted for, never passed as flags. An
AWS role must be onboarded with scope `provisioning` for EKS (`cost` cannot build clusters).
GCP (service account key with `roles/container.admin`) is added from the portal's **Credentials**
page. Scope each to the project or subscription Ankra manages - see `ankra-security`.

## 2. Create

```bash
ankra cluster managed create \
  --provider aks \
  --name my-cluster \
  --credential-id <id> \
  --location westeurope \
  --node-pool-name workers \
  --node-pool-size Standard_D2s_v5 \
  --node-pool-count 2
```

With autoscaling on the first pool instead of a fixed count:

```bash
ankra cluster managed create --provider doks --name prod --credential-id <id> \
  --location fra1 --node-pool-name workers --node-pool-size s-4vcpu-8gb \
  --autoscaling --autoscaling-min 2 --autoscaling-max 8
```

`--autoscaling-min` / `--autoscaling-max` require `--autoscaling`. `--kubernetes-version` is
optional; omitted, the provider's default applies.

Scaleway Kapsule additionally requires the private network:

```bash
ankra cluster managed create --provider kapsule --name prod --credential-id <id> \
  --location fr-par-1 --private-network-id <network_id> \
  --node-pool-name workers --node-pool-size DEV1-M
```

AKS-only create options: `--resource-group` (adopt one; default Ankra creates `ankra-<name>`),
`--network-plugin azure|kubenet`, `--sku-tier Free|Standard|Premium` (Standard for an uptime SLA),
`--private-cluster`, `--vnet-subnet-id`, `--maintenance-window Saturday:22:4`, and for the first
pool `--zone 1,2,3`, `--os-disk-type`, `--os-disk-size-gb`. Extra AKS pools may run on spot
capacity (`node-pool add --spot [--spot-max-price]`), never the first pool.

### Ankra Cloud Kubernetes

The Ankra team runs the control plane. `--credential-id` is optional (the organisation's built-in
Ankra Cloud credential is used), and no SSH key is needed. The control-plane shape is chosen at
create and cannot change:

```bash
ankra cluster managed create --provider ankracloud_k8s --name prod \
  --location <zone> --node-pool-size <plan> --node-pool-count 2          # production: 3 replicas, HA, fee
ankra cluster managed create --provider ankracloud_k8s --name dev \
  --location <zone> --node-pool-size <plan> --control-plane development  # 1 replica, no fee, not for production
ankra cluster managed create --provider ankracloud_k8s --name try \
  --location <zone> --node-pool-size <plan> --dev-cluster                # one k3s server, no HA
```

Optional network flags: `--private-network-id`, `--network-cidr`, `--public-ipv4=false`. The hub
and production take the production control plane; development may take the development one.

### Wire GitOps in the same command

```bash
ankra cluster managed create ... \
  --gitops-repository my-org/platform-gitops \
  --gitops-credential-name github-acme \
  --gitops-branch main
```

Doing this at create time means the cluster's definition is reviewable in a pull request from the
start, rather than living only in the platform. Always pass `--gitops-branch`: the default is
`master`. `ankra cluster gitops status` shows what a cluster syncs from. See `ankra-gitops`.

### Sizes, regions and versions

There is no `managed options` command. Get the valid values from the provider side before you
create - the provider's own console or CLI, or (for the providers that overlap with the VM lane)
the Ankra discovery commands:

```bash
ankra cluster digitalocean regions --credential-id <id>
ankra cluster digitalocean sizes   --credential-id <id> --region fra1 --available-only
ankra cluster ovh regions          --credential-id <id>
ankra cluster ankracloud zones|plans --credential-id <id>    # ankracloud_k8s locations and sizes
```

Naming rules bite here: GKE cluster names must be lowercase; AKS node pool names are lowercase
alphanumeric, max 12 characters. A rejected name is a provider error, not an Ankra one — fix it
rather than retrying.

## 3. Adopt a cluster that already exists

```bash
ankra cluster managed discover --provider eks --credential-id <id>
ankra cluster managed import  --provider eks --credential-id <id> \
  --provider-cluster-id <id-from-discover> --name imported-prod --description "..."
```

`discover` lists what the credential can see at the provider and marks the ones already imported.
`import` fetches the kubeconfig through the provider API and installs the agent itself, so **there
is nothing to run against the cluster** and nothing at the provider is modified.

`--name` defaults to the provider-side name. Use `--provider-cluster-id`, not `--cluster-id`.

## 4. Node pools

```bash
ankra cluster managed node-pool add <cluster_id> --provider doks \
  --name pool-b --size s-4vcpu-8gb --count 2
ankra cluster managed node-pool add <cluster_id> --provider doks \
  --name burst --size s-4vcpu-8gb --autoscaling --autoscaling-min 0 --autoscaling-max 10

ankra cluster managed node-pool scale  <cluster_id> workers --provider doks --count 5
ankra cluster managed node-pool update <cluster_id> workers --provider doks \
  --autoscaling --autoscaling-min 2 --autoscaling-max 10
ankra cluster managed node-pool update <cluster_id> workers --provider doks --autoscaling=false
ankra cluster managed node-pool delete <cluster_id> pool-b --provider doks --yes

# hand a pool's node count to an autoscaler you run yourself, and take it back
ankra cluster managed node-pool update <cluster_id> autoscaled --provider uks --externally-managed
ankra cluster managed node-pool update <cluster_id> autoscaled --provider uks --externally-managed=false
```

`update` takes at least one of `--count`, `--autoscaling`, `--autoscaling-min`,
`--autoscaling-max`, or `--externally-managed` by itself; anything unspecified is left unchanged. `--autoscaling` is a boolean —
`--autoscaling=false` turns it off.

`--externally-managed` is passed on its own. It marks the pool's node count as owned by
something outside Ankra (UpCloud's Cluster Autoscaler on UKS, where Ankra does not manage
autoscaling). While it is set, Ankra refuses to scale the pool, change any other pool setting, replace it or
delete it, from the CLI, the portal, the API and Ankra AI alike; the pool listing shows `externally_managed`
and the provider's live `observed_count`. `--externally-managed=false` hands the pool back and
adopts the live count. Ankra never changes a managed pool's count on its own, so the flag only
guards explicit writes.

To see the pools, read the cluster: `ankra cluster info` and `ankra cluster get nodes`.

## 5. Upgrade

```bash
ankra cluster managed upgrade <cluster_id> --provider aks --version 1.33.0 --yes
```

One command, singular. Get the valid target versions from the provider — a managed control plane
only accepts the versions its own support window offers, and skipping a minor is refused by the
provider, not by Ankra. Upgrade non-production first.

## 6. Power and deletion

```bash
ankra cluster managed stop  <cluster_id> --provider aks    # AKS only
ankra cluster managed start <cluster_id> --provider aks    # AKS only

ankra cluster managed delete <cluster_id> --provider aks --yes
ankra cluster managed delete <cluster_id> --provider aks --force   # cluster in a non-idle state
```

Stop/start currently works on **AKS only**; every other provider bills a running control plane
until the cluster is deleted.

`delete` destroys the cluster at the provider — control plane, node pools and workloads. Confirm
with `ankra cluster info` first, and afterwards check the provider for LoadBalancers and volumes the
cluster created: those are frequently left behind and keep billing.

## Rules

- **Get sizes, regions and versions from the provider before proposing them.** There is no
  `managed options`; availability and pricing change.
- **`--provider` on every subcommand**, spelled `ovh_mks` (not `mks` or `ovh-mks`).
- **`--provider-cluster-id` on import**, from `discover`.
- **Autoscaling flags are `--autoscaling` / `--autoscaling-min` / `--autoscaling-max`** on
  `create`, `node-pool add` and `node-pool update`.
- **UKS has no Ankra-managed autoscaling.** Run UpCloud's Cluster Autoscaler yourself and mark
  the pool `--externally-managed` so nothing in Ankra overrides the count it reaches.
- **Wire GitOps at create time** rather than retrofitting it.
- **Confirm before create, import, upgrade and delete** — each costs money or is destructive.
- **Prefer autoscaling bounds** to a large fixed count when the workload varies.
- **After deleting, check the provider for orphaned LoadBalancers and volumes.**

## Related skills

- `ankra-cloud-clusters` — self-managed kubeadm/k3s on provider VMs, and the instance-family guide.
- `ankra-getting-started` — the path from an empty organisation to a first running app.
- `ankra-import-cluster` — adopting a self-managed cluster via an ImportCluster manifest.
- `ankra-domains-dns` — ingress hostnames, DNS and TLS once the cluster is up.
- `ankra-stacks-addons` / `ankra-gitops` — deploying onto it.
- `ankra-observability`, `ankra-backups`, `ankra-alerts-webhooks` — hub services and spoke wiring.
- `ankra-terraform` — `ankra_managed_cluster` and `ankra_managed_node_pool`.
