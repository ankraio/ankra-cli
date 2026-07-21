---
name: ankra-import-cluster
description: Author and apply an Ankra ImportCluster YAML manifest that connects an existing Kubernetes cluster, wires a GitOps repository, and declares stacks of manifests and Helm addons with dependency ordering. Use when the user wants to import or onboard a cluster into Ankra, write a cluster.yaml / ImportCluster file, or apply one with `ankra cluster apply`.
---

# Ankra ImportCluster

An `ImportCluster` manifest is the declarative entry point for onboarding an existing Kubernetes cluster (EKS, GKE, AKS, on-prem, k3s, kind, minikube) into Ankra. Apply it with `ankra cluster apply -f cluster.yaml`.

## Anatomy

```yaml
apiVersion: v1
kind: ImportCluster
metadata:
  name: my-cluster
  description: Importing my Kubernetes cluster
spec:
  git_repository:                 # optional: enable GitOps for this cluster
    provider: github
    credential_name: my-git-credential
    repository: my-org/my-gitops-repo
    branch: main
  stacks:
    - name: logging
      description: Stack for logging
      manifests:
        - name: namespace-fluent-bit
          parents: []
          from_file: "manifests/fluent-bit-namespace.yaml"
        - name: configmap-fluent-bit
          parents:
            - manifest: namespace-fluent-bit
          from_file: "manifests/fluent-bit-configmap.yaml"
      addons:
        - name: fluent-bit
          chart_name: fluent-bit
          chart_version: 0.49.1          # pin exact version, especially in prod
          repository_url: https://fluent.github.io/helm-charts
          namespace: fluent-bit
          parents:
            - manifest: configmap-fluent-bit
          configuration:
            values: |-
              service:
                enabled: true
```

## Field reference

- `spec.git_repository` — connect a Git repo so stacks are stored and synced from Git (see `ankra-gitops`). Omit for a non-GitOps import.
- `spec.stacks[]` — each stack groups related `manifests` and `addons`.
- `manifests[]` — raw Kubernetes YAML. Use `from_file: "path.yaml"` to reference a file, or `manifest: |-` for inline content.
- `addons[]` — Helm releases. Required: `chart_name`, `chart_version`, `repository_url`, `namespace`. Configure via `configuration.values: |-` (inline) or `configuration.from_file:`.
- `parents` — the dependency edges that control deployment order. A resource only deploys after its parents succeed. Reference a parent as `- manifest: <name>` or `- addon: <name>`.

## Workflow

1. Create namespaces as manifests first; make dependent addons/configmaps list that namespace manifest in `parents`.
2. Pin every addon's `chart_version` to an exact version.
3. Keep secret material out of plaintext — encrypt with SOPS and mark `encrypted_paths` (see `ankra-sops-secrets`).
4. Validate and apply:

```bash
ankra cluster apply -f cluster.yaml --cluster my-cluster
ankra cluster operations list      # watch the resulting operation
```

## Rules

- Namespace manifests must come before anything deployed into that namespace, expressed via `parents`.
- Pin chart versions; never use floating/`latest` charts in production.
- Reference files with forward-slash relative paths (`manifests/foo.yaml`), resolved from the manifest's location.
- Keep stacks small and focused (one concern each) rather than one giant stack.

## Related skills

- `ankra-stacks-addons` for composing stacks in depth.
- `ankra-gitops` for the repository layout that backs `spec.git_repository`.
- `ankra-cli` for applying and watching operations.
