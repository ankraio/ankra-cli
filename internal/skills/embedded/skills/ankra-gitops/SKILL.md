---
name: ankra-gitops
description: Make Git the source of truth for Ankra clusters - one GitOps repository holding every cluster's ImportCluster and Stack YAML, wired at create time or connected later, synced two-way by the Ankra engine, with the directory layout Ankra writes, include paths for hand-written layouts, the `.ankra-owned.json` ownership manifest, repointing, and resolving Git-versus-platform conflicts. Use when the user connects a Git repository to a cluster, organizes a GitOps repo layout, asks why a change did not sync or was reverted, or wants Git to be the source of truth for their Kubernetes configuration.
---

# Ankra GitOps

With GitOps enabled, a cluster's stacks are stored in a Git repository and synced to the cluster by the Ankra engine (not Argo CD). Git becomes the single source of truth; changes flow through commits and pull requests, not manual edits.

## Recommended path

- **Golden path:** *GitOps as the source of truth* - see `ankra-platform-principles`. One platform repository for the whole estate (hub and spokes), each cluster in its own directory.
- **First:** wire the repository when the cluster is created (`--gitops-*` flags, `ankra-cloud-clusters` / `ankra-managed-kubernetes`); for an existing cluster, `spec.git_repository` in its ImportCluster (`ankra-import-cluster`).
- **Then:** every change is a pull request - `ankra cluster validate`, `ankra cluster draft` for review, merge, let the engine reconcile. Variables over hard-coding and pinned chart versions (`ankra-stacks-addons`), secrets sealed with SOPS (`ankra-sops-secrets`).
- **Avoid:** plaintext secrets in Git, editing the same resource in the portal and in Git at once (that is a conflict that pauses sync), and `kubectl`/`helm` changes the next sync will undo.

## Connect a repo

In the `ImportCluster` spec:

```yaml
spec:
  git_repository:
    provider: github            # github (default) or bitbucket_cloud
    credential_name: my-git-credential
    repository: my-org/my-gitops-repo   # Bitbucket Cloud: workspace/repo-slug
    branch: main                # must already exist; Ankra does not create branches
```

The `credential_name` references a Git credential stored in Ankra (least-privilege deploy token /
app install); `ankra credentials repositories <name>` shows what it can actually reach.

When Ankra *builds* the cluster, skip the retrofit entirely: provider `create` commands (Hetzner,
DigitalOcean, OVH, UpCloud, AWS, Ankra Cloud) and `ankra cluster managed create` take
`--gitops-repository`, `--gitops-credential-name` and `--gitops-branch` (pass the branch - the
server default is `master`), and the generated stacks land in the repo as a commit from day one.

Connecting a repository to a cluster that already has state pushes that state first, as a commit
named `Connect GitOps repository`, then starts syncing. Connect an empty repository or one Ankra
has written to before.

Changing `git_repository` on a connected cluster **repoints** it: Ankra writes the current state to
the new source, then syncs from it, and anything that later leaves that source is pruned (PVCs
included). `ankra cluster apply` refuses it without `--allow-repoint` (and
`--allow-repoint-destroying-data` when the cluster holds PersistentVolumeClaims).

```bash
ankra cluster gitops status [cluster]   # repository, branch, last synced commit, observed_source, sync errors
```

`source_mismatch: true` means the last sync read a different repository than the one configured.

## Layout

**What Ankra writes** - one directory per cluster, so the whole estate shares one repository:

```
platform-gitops/
  clusters/
    infrastructure-<short-id>/
      infrastructure.yaml          # the ImportCluster definition
      .ankra-owned.json            # what Ankra last wrote - never edit or delete
      .sops.yaml                   # the AGE rule for this directory
      stacks/
        monitoring/
          add-ons/kube-prometheus-stack/values.yaml
          manifests/monitoring-namespace.yaml
    production-<short-id>/
    development-<short-id>/
```

Edit these files and push; Ankra applies the change. Sync is two-way: portal, CLI, API and Ankra AI
changes are committed back (authored by `ankra-platform[bot]` or `Ankra Reconciler`).
`.ankra-owned.json` is the overwrite and deletion boundary - Ankra keeps a file you edited instead
of overwriting it, and prunes only files it rendered. Deleting it switches that boundary off; if
it is gone, restore it from history.

**A layout you write by hand** points at files with `include` paths, which the sync loads:

```yaml
stacks:
  - name: platform
    manifests:
      - include: manifests/            # every YAML file in the folder
    addons:
      - include: addons/ingress.yaml   # or one file
```

`include` is read when Ankra syncs from Git; `ankra cluster apply -f` does not resolve it, so a
file applied from the terminal uses `from_file` entries instead.

## Workflow

1. Branch from the synced branch, edit the relevant cluster YAML / values / manifests.
2. `ankra cluster validate -f <file>` (and `ankra cluster draft -f <file>` for a reviewable draft on a cluster you are not ready to change).
3. Open a pull request; review the diff as "what will deploy".
4. Merge - the Ankra engine reconciles the change onto the cluster.
5. Confirm with `ankra cluster gitops status` and `ankra cluster operations list`.

## When Git and the platform both change

A resource changed in Git and on the platform since they last agreed is a **conflict**, and it
pauses the whole cluster's sync until decided. Nothing picks a side on its own:

```bash
ankra cluster gitops conflicts list [cluster]
ankra cluster gitops conflicts resolve <resource-key> --keep git|cluster
ankra cluster gitops conflicts resolve --all --keep git --yes
```

`--keep git` overwrites the platform version; `--keep cluster` pushes the platform version over
Git. If Git is the authority for a cluster, change it only through Git.

## Rules

- **Git is the source of truth.** Do not hand-edit cluster state out of band; change the repo and let it sync.
- **Protect the synced branch.** Require PR review; treat merges as deploys.
- **Encrypt secrets before commit.** Never commit plaintext Secrets — use SOPS (`ankra-sops-secrets`).
- **Pin versions.** Exact chart versions and immutable image tags so a commit fully determines what runs.
- **Never rename a manifest that owns a namespace or volumes** - a rename is a delete plus a create.
- **Small, reviewable commits.** One logical change per PR; the diff should read as "what will deploy".

Terraform can manage the same clusters (`ankra-terraform`), but pick one owner per object - Terraform or the GitOps repository, not both.

## Related skills

- `ankra-import-cluster` for the `cluster.yaml` schema.
- `ankra-stacks-addons` for stack design, variables and parents.
- `ankra-sops-secrets` for encrypting Git-stored secrets.
- `ankra-cicd` for pipelines that commit image-tag bumps into this repo.
- `ankra-stack-profiles` when the same stack must run on many clusters.
