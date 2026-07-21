---
name: ankra-managed-kubernetes
description: Provision, import, and operate provider-managed Kubernetes through Ankra - DigitalOcean DOKS, UpCloud UKS, Google GKE, OVHcloud MKS, Azure AKS, and Amazon EKS - with live options and pricing, preflight checks, discovery/import of existing clusters, node pool autoscaling, and version upgrades. Use when the user wants a managed Kubernetes cluster (the provider runs the control plane), mentions DOKS, UKS, GKE, AKS, EKS, or OVH MKS, or wants to import a managed cluster into Ankra.
---

# Ankra Managed Kubernetes

Ankra provisions and manages provider-native managed Kubernetes: the cloud provider runs the control plane, Ankra manages node pools, upgrades, addons, stacks, and GitOps on top. This is distinct from `ankra-cloud-clusters` (self-managed k3s/kubeadm on VMs with a bastion).

Providers: `doks` (DigitalOcean), `uks` (UpCloud), `gke` (Google Cloud), `ovh-mks` (OVHcloud), `aks` (Azure), `eks` (AWS). Every `ankra cluster managed` subcommand takes `--provider`.

## Prerequisite: one provider credential

```bash
ankra credentials list                       # find an existing credential ID
ankra credentials digitalocean create ...    # doks
ankra credentials upcloud create ...         # uks
ankra credentials ovh create ...             # ovh-mks (needs a Public Cloud project)
```

GCP (service account key with `roles/container.admin`), Azure (service principal with Contributor), and AWS credentials are added from the Ankra portal **Credentials** page.

## Always check live options first

Locations, Kubernetes versions (with support windows), node sizes, and pricing are fetched live from the provider — never guess sizes or regions:

```bash
ankra cluster managed options --provider gke --credential-id <id>
```

## Provision

```bash
ankra cluster managed create \
  --provider aks \
  --name my-cluster \
  --credential-id <id> \
  --location westeurope \
  --node-pool-name workers \
  --node-pool-size Standard_D2s_v5 \
  --node-pool-count 2 \
  --node-pool-autoscaling-min 2 --node-pool-autoscaling-max 5
```

- Preflight checks (credential, quota, naming) run automatically and abort on errors; only bypass with `--skip-preflight` when the user explicitly accepts the risk.
- `--ha` (doks), `--cluster-plan` (uks), and `--kubernetes-version` are optional.
- Provider naming rules: GKE names are lowercase; AKS node pool names are lowercase alphanumeric, max 12 characters.

## Discover and import existing clusters

```bash
ankra cluster managed discover --provider eks --credential-id <id>
ankra cluster managed import --provider eks --credential-id <id> \
  --cluster-id <provider_cluster_id> --name imported-prod
```

Discovery marks clusters that are already imported. Import adopts the cluster without modifying it at the provider.

## Day-2 operations

```bash
ankra cluster managed node-pool add <cluster_id> --provider doks --name pool-b --size s-4vcpu-8gb --count 2
ankra cluster managed node-pool scale <cluster_id> workers --provider doks --count 5
ankra cluster managed node-pool update <cluster_id> workers --provider doks \
  --autoscaling-enabled --autoscaling-min 2 --autoscaling-max 10
ankra cluster managed node-pool delete <cluster_id> pool-b --provider doks

ankra cluster managed upgrades <cluster_id> --provider aks     # list provider upgrade targets
ankra cluster managed upgrade <cluster_id> --provider aks --version 1.33.0
```

Autoscaling works on every provider except `uks`.

## Delete (destructive)

```bash
ankra cluster managed delete <cluster_id> --provider aks
```

Destroys the cluster at the provider — control plane, node pools, and workloads. Confirm the target with `ankra cluster info` first, and clean up provider-created LoadBalancers/volumes.

## Rules

- **Fetch options live** (`managed options`) before proposing sizes, regions, or versions — pricing and availability change.
- **Never skip preflight** unless the user explicitly asks.
- **Confirm before create, import, upgrade, and delete** — they cost money or are destructive.
- **Respect provider naming rules** (GKE lowercase, AKS pool-name limits) instead of retrying blindly.
- **Prefer autoscaling bounds** over large fixed counts when the workload is variable.

## Related skills

- `ankra-cloud-clusters` for self-managed k3s/kubeadm clusters on provider VMs.
- `ankra-import-cluster` for importing self-managed clusters via an ImportCluster manifest.
- `ankra-stacks-addons` / `ankra-gitops` to deploy workloads once the cluster exists.
