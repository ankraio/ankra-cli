---
name: ankra-cloud-clusters
description: Provision and manage Ankra-managed K3s clusters on Hetzner Cloud, OVHcloud, and UpCloud - creating clusters, storing provider credentials, and managing node groups, scaling, Kubernetes versions, and upgrades. Use when the user wants Ankra to provision a cluster (rather than import one), or mentions Hetzner, OVH, or UpCloud clusters.
---

# Ankra Cloud Clusters

Besides importing existing clusters, Ankra can provision managed K3s clusters on Hetzner Cloud, OVHcloud, and UpCloud. This skill covers the provision-and-manage lifecycle.

## Prerequisite: provider credentials

Store provider credentials in Ankra first (scoped API tokens from the provider):

```bash
ankra credentials hetzner      # add/validate Hetzner credentials
ankra credentials ovh
ankra credentials upcloud
ankra credentials list
```

## Provision

```bash
ankra cluster provision        # create a managed cluster (provider, region, sizes)
ankra cluster list
ankra cluster info
```

## Manage the cluster lifecycle

Per-provider subcommands manage control plane and workers:

```bash
ankra cluster hetzner control-plane ...   # control-plane management
ankra cluster hetzner nodes ...           # node management
ankra cluster hetzner workers ...         # worker pools
ankra cluster hetzner scale ...           # scale a node group
ankra cluster hetzner node-group ...      # add/edit node groups
ankra cluster hetzner k8s-version ...     # view target k8s version
ankra cluster hetzner upgrade ...         # upgrade Kubernetes
```

The same shape exists under `ankra cluster ovh ...` and `ankra cluster upcloud ...`.

## Deprovision (destructive)

```bash
ankra cluster deprovision      # tears down the managed cluster
```

Confirm the target with `ankra cluster info` first; deprovisioning destroys infrastructure.

## Rules

- **Least-privilege provider credentials**, stored in Ankra and validated before provisioning.
- **Confirm the target** before any `deprovision`, scale-down, or version upgrade.
- **Plan node groups** for workload isolation rather than one undifferentiated pool.
- **Upgrade deliberately** — review the target Kubernetes version and upgrade non-prod first.
- **Right-size** node groups; avoid oversized defaults that inflate cost.

## Related skills

- `ankra-cli` for credential and cluster commands.
- `ankra-stacks-addons` / `ankra-gitops` to deploy workloads once the cluster exists.
