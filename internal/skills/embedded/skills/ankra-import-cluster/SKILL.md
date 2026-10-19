---
name: ankra-import-cluster
description: Author and apply an Ankra ImportCluster YAML manifest that connects an existing Kubernetes cluster, wires a GitOps repository, and declares stacks of manifests, Helm addons and applications with dependency ordering, deploy waves, stack variables and backup policy. Use when the user wants to import or onboard a cluster into Ankra, write a cluster.yaml / ImportCluster file, or validate, draft or apply one with `ankra cluster apply`.
---

# Ankra ImportCluster

An `ImportCluster` manifest is the declarative entry point for onboarding an existing Kubernetes cluster (EKS, GKE, AKS, on-prem, k3s, kind, minikube) into Ankra, and the same schema Ankra reads from a cluster's GitOps repository. Apply it with `ankra cluster apply -f cluster.yaml`.

## Recommended path

- **Golden path:** *GitOps as the source of truth* - see `ankra-platform-principles`. The file lives in the platform repository with `spec.git_repository` set, is checked with `validate`, staged with `draft` for review on anything that matters, and only then applied; the Ankra engine reconciles it.
- Pin every `chart_version`, put per-environment values in `variables`, and seal secrets with SOPS (`ankra-sops-secrets`) - never plaintext in Git.
- A cluster Ankra should **build** is `ankra-cloud-clusters` / `ankra-managed-kubernetes` (managed ones import with `ankra cluster managed import`, no YAML needed). Repository layout is `ankra-gitops`; stack design is `ankra-stacks-addons`.
- **Avoid:** applying straight to production without a reviewed draft, and `kubectl apply`/`helm install` beside the file - the next apply or sync removes what the file does not declare.

## Anatomy

```yaml
apiVersion: v1
kind: ImportCluster
metadata:
  name: my-cluster
  description: Importing my Kubernetes cluster
spec:
  git_repository:                 # optional: enable GitOps for this cluster
    provider: github              # github (default) or bitbucket_cloud
    credential_name: my-git-credential
    repository: my-org/my-gitops-repo
    branch: main
  stacks:
    - name: logging
      description: Stack for logging
      deploy_wave: 1              # optional: order whole stacks
      variables:                  # optional: stack-scoped, highest precedence
        LOG_LEVEL: info
      manifests:
        - name: namespace-fluent-bit
          parents: []
          from_file: "manifests/fluent-bit-namespace.yaml"
        - name: configmap-fluent-bit
          parents:
            - kind: manifest
              name: namespace-fluent-bit
          from_file: "manifests/fluent-bit-configmap.yaml"
      addons:
        - name: fluent-bit
          chart_name: fluent-bit
          chart_version: 0.49.1          # pin exact version, especially in prod
          registry_name: fluent
          registry_url: https://fluent.github.io/helm-charts
          namespace: fluent-bit
          parents:
            - kind: manifest
              name: configmap-fluent-bit
          configuration:
            values: |-
              logLevel: ${{ ankra.LOG_LEVEL }}
```

## Field reference

- `spec.git_repository` — connect a Git repo so stacks are stored and synced from Git (see `ankra-gitops`). `provider` is `github` (default) or `bitbucket_cloud` (`workspace` + `repo_slug` in place of `repository`); the branch must already exist. Changing this block on a cluster that has one **repoints** it and is refused without `--allow-repoint` (plus `--allow-repoint-destroying-data` when the cluster holds PersistentVolumeClaims). Omit for a non-GitOps import.
- `spec.prometheus_metrics` — the cluster's metrics source (`endpoint`, optional `credential_name`, `flavor`). Credentials are referenced by name, never inlined.
- `spec.stacks[]` — each stack groups related `manifests`, `addons` and `applications` (deployments of Ankra applications by `platform_application_id`).
- `variables` — stack-scoped map, referenced as `${{ ankra.NAME }}` in manifests and values; shadows cluster (`ankra cluster variables`) and organisation (`ankra org variables`) values. Quote anything that is not a plain string or integer.
- `deploy_wave` — integer `>= 0`; a stack in wave N deploys only after every lower wave succeeded. Stacks without one are unordered.
- `backup` — the stack's backup policy (`enabled`, `vault`, `schedule`, `retention`, `selection`); see `ankra-backups`. An omitted block never removes protection.
- `manifests[]` — raw Kubernetes YAML: `from_file: "path.yaml"` or inline `manifest: |-`. Optional `namespace`, `encrypted_paths`, `force` (replace on conflict) and `auto_remediate` (re-apply on drift). **The `name` is the identity: renaming a manifest deletes everything it owned** (a Namespace and its PVCs included) and creates it anew.
- `addons[]` — Helm releases. Required: `chart_name`, `chart_version`, `namespace`. Name the chart source with `registry_name` + `registry_url` (with `registry_credential_name` for a private registry — see `ankra-helm-registries`); the older `repository_url` is still accepted. Configure via `configuration.values: |-` (inline) or `configuration.from_file:`, `configuration.encrypted_paths` for SOPS-sealed keys, and `settings` for sync behaviour.
- `agents_md` / `agents_md_from_file` — optional per-manifest and per-addon AGENTS.md: operational learnings in plain markdown, stored as a sibling file in the GitOps repo (e.g. `agents_md_from_file: "manifests/fluent-bit.AGENTS.md"`). Omitting the field preserves the stored content, an explicit empty string clears it, and editing it never triggers a redeploy. Plaintext only — never put secrets in it. After any non-obvious operation (a values quirk, an upgrade pitfall, a dependency gotcha), record what you learned here so future agents working on the same addon or manifest benefit.
- `group` — optional per-manifest and per-addon organizational label within the stack (a quoted string, e.g. `group: "platform services"`). Purely organizational: it never triggers a redeploy. Keep whatever the exported file carries — apply prunes what the file no longer declares, so removing the key removes the resource from its group.
- `parents` — the dependency edges that control deployment order. A resource only deploys after its parents succeed. Every parent is a `kind` + `name` pair — write both keys (`- kind: manifest` / `name: <name>`, or `kind: addon`). **The shorthand `- manifest: <name>` does not work**: it parses to an empty parent and the edge is silently dropped, while both local and server-side validation still pass. Verify what was stored with `ankra cluster stacks list <stack> -o json`.

`spec.helm_registries`, `spec.log_source` and `- include: <path>` entries are read by the GitOps sync when Ankra reads the file from the repository; `ankra cluster apply -f` does not send them (an `include` entry is refused for having no `name`). Use them only in a Git-synced file.

## Workflow

1. Create namespaces as manifests first; make dependent addons/configmaps list that namespace manifest in `parents`.
2. Pin every addon's `chart_version` to an exact version.
3. Keep secret material out of plaintext — encrypt with SOPS and mark `encrypted_paths` (see `ankra-sops-secrets`).
4. Validate, stage, apply:

```bash
ankra cluster apply -f cluster.yaml --dry-run            # local checks only, no API call
ankra cluster validate -f cluster.yaml [--cluster prod] [--strict-secrets]   # server-side, changes nothing
ankra cluster draft -f cluster.yaml                      # stage every stack as a reviewable draft
ankra cluster stacks deploy-draft <stack>                # deploy a reviewed draft
ankra cluster apply -f cluster.yaml [--cluster prod]     # or apply directly
ankra cluster operations list                            # watch the resulting add-on deploys
```

`apply` is a declarative **replace** per stack: what the file no longer declares is removed from
that stack. `--wait` waits only for the configuration write, not for the add-on deploys - follow
those with `ankra cluster operations list`. `draft` is the safe route on a cluster you are not
ready to change; `--cluster` targets a cluster other than `metadata.name`.

Terraform users can declare the same cluster and stacks with `ankra_cluster` (see `ankra-terraform`); keep one source of truth per cluster.

## Rules

- Namespace manifests must come before anything deployed into that namespace, expressed via `parents`.
- Pin chart versions; never use floating/`latest` charts in production.
- Reference files with forward-slash relative paths (`manifests/foo.yaml`), resolved from the manifest's location.
- Keep stacks small and focused (one concern each) rather than one giant stack.
- Never rename a manifest that owns a namespace or stateful resources without moving the data first.

## Related skills

- `ankra-stacks-addons` for composing stacks in depth.
- `ankra-gitops` for the repository layout that backs `spec.git_repository`.
- `ankra-backups` for the `backup` block, `ankra-sops-secrets` for `encrypted_paths`.
- `ankra-cli` for applying and watching operations.
