---
name: ankra-cloud-clusters
description: Provision and manage Ankra-managed K3s clusters on Hetzner Cloud, OVHcloud, and UpCloud - creating clusters, storing provider credentials, and managing node groups, scaling, Kubernetes versions, and upgrades. Use when the user wants Ankra to provision a cluster (rather than import one), or mentions Hetzner, OVH, or UpCloud clusters.
---

# Ankra Cloud Clusters

Besides importing existing clusters, Ankra can provision managed K3s clusters on Hetzner Cloud, OVHcloud, and UpCloud. This skill covers the provision-and-manage lifecycle. Provider subcommands share a common shape: `ankra cluster hetzner|ovh|upcloud <verb>`.

## 1. Store provider credentials

Provider credentials (and SSH keys) are scoped credentials held by Ankra. Create them first and note the returned credential ID - the create commands take the ID, not the name.

```bash
ankra credentials hetzner create --name hetzner-prod
ankra credentials hetzner ssh-key create --name ops-key      # SSH key credential
ankra credentials ovh create --name ovh-prod
ankra credentials upcloud create --name upcloud-prod
ankra credentials list                                       # find the IDs to pass below
ankra credentials get <name>                                 # resolve a name to its ID
```

## 2. Create a cluster (per provider)

`create` provisions a brand-new cloud cluster. It submits asynchronously by default; add `--wait` (bounded by `--timeout`, default 10m) to block and print the agent install command on first import.

```bash
ankra cluster ovh regions --credential-id <id>               # pick a region the project can deploy in
ankra cluster ovh create \
  --name prod \
  --credential-id <ovh_cred_id> \
  --region GRA9 \
  --ssh-key-credential-ids <id>,<id> \
  --wait
```

Hetzner/UpCloud `create` take equivalent flags (`--name`, `--credential-id`, location/region, control-plane and worker sizes/counts, `--ssh-key-credential-id[s]`, optional `--kubernetes-version`). Run `ankra cluster <provider> create --help` for the provider-specific server-type/location flags.

> `ankra cluster provision` and `ankra cluster deprovision` **start and stop** an already-created managed cluster - they are not how you create one. Creation is always `ankra cluster <provider> create`.

## 3. Manage the lifecycle

Same verbs exist under `hetzner`, `ovh`, and `upcloud`:

```bash
ankra cluster ovh k8s-version <cluster_id>                   # current Kubernetes version
ankra cluster ovh upgrade <cluster_id> ...                   # upgrade Kubernetes
ankra cluster ovh workers <cluster_id>                       # current worker count
ankra cluster ovh scale <cluster_id> ...                     # scale workers

ankra cluster ovh control-plane get <cluster_id>
ankra cluster ovh control-plane set-count <cluster_id> 3     # 1 or 3 controllers
ankra cluster ovh control-plane set-instance-type <cluster_id> ...

ankra cluster ovh nodes list <cluster_id>
ankra cluster ovh nodes get <cluster_id> <node>

ankra cluster ovh node-group list <cluster_id>
ankra cluster ovh node-group add <cluster_id> ...            # async by default; --wait to block
ankra cluster ovh node-group scale <cluster_id> <group> ...
ankra cluster ovh node-group upgrade <cluster_id> <group>    # instance/plan change, irreversible
ankra cluster ovh node-group delete <cluster_id> <group>
```

### OVH-only extras (parity with the web UI)

```bash
ankra cluster ovh node-group add <cluster_id> --labels k=v,... --taints k=v:Effect,...
ankra cluster ovh node-group labels <cluster_id> <group> --labels k=v,...   # empty value clears
ankra cluster ovh node-group taints <cluster_id> <group> --taints k=v:Effect,...  # effect defaults NoSchedule
ankra cluster ovh stop <cluster_id>                          # stop compute, keep config
ankra cluster ovh start <cluster_id> [--scope all|control_plane]
ankra cluster ovh access-info <cluster_id>                   # bastion/control-plane IPs + ssh -J / port-forward commands
ankra cluster ovh ssh-keys get <cluster_id>
ankra cluster ovh ssh-keys set <cluster_id> --ssh-key-credential-ids <id>,...  # applies on next reconcile
```

(Hetzner and UpCloud node groups support `add/list/scale/upgrade/delete`; labels/taints, start/stop, access-info, ssh-keys, and `regions` are OVH-specific today.)

## 4. Deprovision / delete (destructive)

```bash
ankra cluster deprovision <cluster_id>                  # stop; for cloud clusters releases cloud resources
ankra cluster deprovision <cluster_id> --auto-delete    # stop then delete the cluster record
ankra cluster <provider> deprovision <cluster_id>       # provider-specific teardown
ankra delete cluster <name>                             # delete the cluster (supports --dry-run)
```

Confirm the target with `ankra cluster info` first; deprovisioning releases infrastructure.

## Rules

- **Least-privilege provider credentials**, stored in Ankra and validated (`ankra credentials get`) before provisioning. Pass credential IDs, not names, to `create`.
- **Pick a valid region first** (`ankra cluster ovh regions`); a region not enabled on the project fails the reconcile at private-network setup.
- **Confirm the target** before any `deprovision`, `delete`, scale-down, or version/instance upgrade. Node-group `upgrade` is irreversible.
- **Use `--wait`** when a follow-up step depends on the result; otherwise treat creates and node-group mutations as in-flight and don't re-submit.
- **Plan node groups** for workload isolation rather than one undifferentiated pool, and right-size them to avoid inflated cost.
- **Upgrade deliberately** - review the target Kubernetes version and upgrade non-prod first.

## Related skills

- `ankra-cli` for the broader command surface, auth, and async/`--wait` conventions.
- `ankra-stacks-addons` / `ankra-gitops` to deploy workloads once the cluster exists.
