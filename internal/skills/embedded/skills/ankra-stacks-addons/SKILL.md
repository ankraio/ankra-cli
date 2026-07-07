---
name: ankra-stacks-addons
description: Compose Ankra Stacks from Helm addons, raw Kubernetes manifests, variables, and dependency edges that control deployment order. Use when deploying anything onto an Ankra-managed Kubernetes cluster - installing a Helm chart or addon (ingress-nginx, cert-manager, Prometheus, databases, ...), applying manifests, ordering resources with parents or deploy waves - instead of running helm install or kubectl apply directly.
---

# Ankra Stacks & Addons

A Stack is Ankra's reusable unit of deployment: it bundles Helm addons, raw manifests, variables, and the dependency edges between them. Stacks live inside a cluster (and, with GitOps, in a Git repo).

## Building blocks

- **Addon** - a Helm release: `chart_name`, `chart_version`, `repository_url`, `namespace`, and `configuration.values`.
- **Manifest** - raw Kubernetes YAML (namespaces, ConfigMaps, CRDs, RBAC, anything Helm does not own).
- **Variable** - a named value substituted into manifests/addon values, so the same stack works across environments.
- **Parents** - dependency edges. A resource deploys only after every parent has succeeded.

## Deployment order across stacks via `deploy_wave`

Stacks accept an optional `deploy_wave` (integer >= 0): a stack in wave N deploys only after every stack in a lower wave finished, teardown unwinds in reverse, and stacks sharing a wave run in parallel. Stacks without a wave stay independent.

```yaml
stacks:
  - name: infrastructure
    deploy_wave: 1
  - name: platform
    deploy_wave: 2
  - name: applications
    deploy_wave: 3
```

## Deployment order within a stack via `parents`

Order is a graph, not a list. Declare what each resource needs:

```yaml
manifests:
  - name: monitoring-ns
    parents: []
  - name: grafana-dashboards
    parents:
      - manifest: monitoring-ns
addons:
  - name: kube-prometheus-stack
    chart_name: kube-prometheus-stack
    chart_version: 65.1.1
    repository_url: https://prometheus-community.github.io/helm-charts
    namespace: monitoring
    parents:
      - manifest: monitoring-ns
      - manifest: grafana-dashboards
```

Edit parents after the fact with the CLI instead of re-applying everything:

```bash
ankra cluster addons upgrade kube-prometheus-stack \
  --add-parent name=monitoring-ns,kind=manifest --cluster prod
```

## Variables over hardcoding

Promote anything environment-specific (domains, replica counts, storage classes, sizes) to a variable and reference it, rather than baking literals into addon values. This keeps a stack reusable across dev/staging/prod and avoids copy-paste drift.

## Design rules

- **Small, focused stacks.** One concern per stack (logging, monitoring, ingress) beats a single mega-stack - easier to reason about, reorder, and clone.
- **Namespace first.** The namespace manifest is a parent of everything deployed into it.
- **Pin chart versions.** Exact `chart_version` everywhere; never floating in production.
- **Test before prod.** Roll a stack out to dev/staging, then promote the same definition to production.
- **Values are a contract.** Keep `configuration.values` minimal and intentional; rely on chart defaults otherwise.

## Inspecting and changing

```bash
ankra cluster stacks list
ankra cluster addons list
ankra cluster addons settings <addon>     # show stored values
ankra cluster addons upgrade <addon>       # change version/values/parents
ankra cluster manifests upgrade <name>
```

## Related skills

- `ankra-import-cluster` for the surrounding ImportCluster document.
- `ankra-helm-registries` for private chart sources.
- `ankra-gitops` for storing stacks in Git.
