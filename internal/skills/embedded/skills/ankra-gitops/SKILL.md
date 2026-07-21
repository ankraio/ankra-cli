---
name: ankra-gitops
description: Structure an Ankra GitOps repository with modular include paths so cluster and stack definitions are stored in Git and synced by Ankra/ArgoCD. Use when the user connects a Git repository to a cluster, organizes a GitOps repo layout, or wants Git to be the source of truth for their Kubernetes configuration.
---

# Ankra GitOps

With GitOps enabled, a cluster's stacks are stored in a Git repository and synced to the cluster (ArgoCD-backed). Git becomes the single source of truth; changes flow through commits and pull requests, not manual edits.

## Connect a repo

In the `ImportCluster` spec:

```yaml
spec:
  git_repository:
    provider: github            # github, gitlab, ...
    credential_name: my-git-credential
    repository: my-org/my-gitops-repo
    branch: main
```

The `credential_name` references a Git credential stored in Ankra (least-privilege deploy token / app install). See `ankra-helm-registries` and the credentials docs for storing credentials.

## Modular layout with include paths

Keep the repo modular instead of one enormous file. A typical layout:

```
gitops-repo/
├── cluster.yaml                 # ImportCluster: references the stacks below
├── stacks/
│   ├── ingress/
│   │   ├── stack.yaml
│   │   └── manifests/
│   ├── monitoring/
│   │   ├── stack.yaml
│   │   └── values/
│   └── logging/
│       └── stack.yaml
└── manifests/
    └── shared/
```

- One directory per stack; reference manifest and values files by relative path from where they are declared.
- Split large stacks into their own files/folders rather than inlining everything.
- Shared raw manifests live in a common folder and are referenced from multiple stacks.

## Workflow

1. Branch from `main`, edit the relevant `stack.yaml` / manifests.
2. Open a pull request; review the diff.
3. Merge to the synced branch — Ankra/ArgoCD reconciles the change onto the cluster.
4. Confirm with `ankra cluster operations list` and `ankra cluster info`.

## Rules

- **Git is the source of truth.** Do not hand-edit cluster state out of band; change the repo and let it sync.
- **Protect the synced branch.** Require PR review; treat merges as deploys.
- **Encrypt secrets before commit.** Never commit plaintext Secrets — use SOPS (`ankra-sops-secrets`).
- **Pin versions.** Exact chart versions and immutable image tags so a commit fully determines what runs.
- **Small, reviewable commits.** One logical change per PR; the diff should read as "what will deploy".

## Related skills

- `ankra-cicd` for pipelines that commit image-tag bumps into this repo.
- `ankra-sops-secrets` for encrypting Git-stored secrets.
- `ankra-import-cluster` for the `cluster.yaml` shape.
