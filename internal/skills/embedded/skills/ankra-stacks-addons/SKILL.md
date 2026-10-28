---
name: ankra-stacks-addons
description: Compose Ankra Stacks from Helm addons, raw Kubernetes manifests, variables, and dependency edges that control deployment order, plus deploy waves between stacks and in-place addon changes. Use when the user is designing or editing an Ankra stack, adding a Helm addon, ordering resources with parents or stacks with deploy waves, changing an addon's version or values, or factoring configuration into variables.
---

# Ankra Stacks & Addons

A Stack is Ankra's reusable unit of deployment: it bundles Helm addons, raw manifests, variables, and the dependency edges between them. Stacks live inside a cluster (and, with GitOps, in a Git repo).

## Recommended path

- **Golden path:** *GitOps as the source of truth* - see `ankra-platform-principles`. Write the stack into the cluster's YAML in the GitOps repository, `ankra cluster validate`, `ankra cluster draft` to review, merge, and let the Ankra engine reconcile.
- Pin every chart version, put per-environment values in variables, seal secrets with SOPS (`ankra-sops-secrets`), and protect any stack holding data into a backup vault (`ankra-backups`).
- The same stack on several clusters becomes a stack profile (`ankra-stack-profiles`); the surrounding file is `ankra-import-cluster`, the repository `ankra-gitops`.
- **Avoid:** `helm install` / `kubectl apply` beside a stack, floating chart versions, and copy-pasted values per cluster. Ingress is Traefik (installed by Ankra), not ingress-nginx.

## Building blocks

- **Addon** — a Helm release: `chart_name`, `chart_version`, `registry_name` + `registry_url` (the older `repository_url` is a legacy alias), `namespace`, and `configuration.values`. Optional `agents_md` (inline markdown) or `agents_md_from_file` (path) attaches an AGENTS.md of operational learnings, stored next to the addon in the GitOps repo.
- **Manifest** — raw Kubernetes YAML (namespaces, ConfigMaps, CRDs, RBAC, anything Helm does not own). Also takes optional `agents_md` / `agents_md_from_file`. Its `name` is its identity: renaming one deletes what it owned.
- **Variable** — a named value substituted into manifests/addon values as `${{ ankra.NAME }}`, so the same stack works across environments.
- **Parents** — dependency edges. A resource deploys only after every parent has succeeded.

AGENTS.md semantics: omit the field to preserve what is stored, set `""` to clear it, and editing it never redeploys anything. Content is plain markdown (never encrypted — no secrets). Use it to record durable operational learnings after non-obvious operations so future agents benefit.

## Deployment order via `parents`

Order is a graph, not a list. Declare what each resource needs; every parent is a `kind` + `name` pair:

```yaml
manifests:
  - name: monitoring-ns
    parents: []
    from_file: manifests/monitoring-ns.yaml
  - name: grafana-dashboards
    parents:
      - kind: manifest
        name: monitoring-ns
    from_file: manifests/grafana-dashboards.yaml
addons:
  - name: kube-prometheus-stack
    chart_name: kube-prometheus-stack
    chart_version: 65.1.1
    registry_name: prometheus-community
    registry_url: https://prometheus-community.github.io/helm-charts
    namespace: monitoring
    agents_md_from_file: agents/kube-prometheus-stack.AGENTS.md
    parents:
      - kind: manifest
        name: monitoring-ns
      - kind: manifest
        name: grafana-dashboards
```

**The shorthand `- manifest: <name>` does not work** - it parses to an empty parent and the edge is silently dropped while validation still passes. Check what was stored with `ankra cluster stacks list <stack> -o json`.

Edit parents after the fact with the CLI instead of re-applying everything:

```bash
ankra cluster addons upgrade kube-prometheus-stack \
  --add-parent name=monitoring-ns,kind=manifest --cluster prod
```

Between stacks, use `deploy_wave: <n>` on the stack: wave N starts only after every lower wave succeeded (for example networking 1, platform 2, applications 3). Within a stack, parents still decide.

## Variables over hardcoding

Promote anything environment-specific (domains, replica counts, storage classes, sizes) to a variable and reference it as `${{ ankra.NAME }}`, rather than baking literals into addon values. Scopes, most specific wins: stack (`variables:` in the stack YAML, or `ankra cluster stacks variables set <stack> <name> <value>`), cluster (`ankra cluster variables set`), organisation (`ankra org variables set`). Built-ins include `${{ ankra.cluster_domain }}` and `${{ ankra.stack_name }}`. An unresolved variable reaches the cluster as a literal, so check it is set on every target.

## Design rules

- **Small, focused stacks.** One concern per stack (logging, monitoring, ingress) beats a single mega-stack — easier to reason about, reorder, and clone.
- **Namespace first.** The namespace manifest is a parent of everything deployed into it.
- **Pin chart versions.** Exact `chart_version` everywhere; never floating in production.
- **Test before prod.** Roll a stack out to development, then promote the same definition to production.
- **Values are a contract.** Keep `configuration.values` minimal and intentional; rely on chart defaults otherwise.
- **Stable manifest names.** Never rename a manifest that owns a namespace or volumes.

## Inspecting and changing

```bash
ankra cluster stacks list [stack] [-o json]
ankra cluster stacks history <stack>
ankra cluster addons list
ankra cluster addons values <addon>            # current Helm values (YAML)
ankra cluster addons settings get <addon>      # sync settings (automated, prune, self-heal, retry)
ankra cluster addons upgrade <addon> --chart-version 65.2.0 --dry-run
ankra cluster addons upgrade <addon> --set image.tag=1.2.3       # mutate values
ankra cluster addons upgrade <addon> --values-from-file v.yaml   # REPLACES all values
ankra cluster manifests upgrade <name>
```

`addons upgrade --namespace` is a destructive Helm reinstall and asks for confirmation. On a GitOps
cluster these CLI changes are committed back to the repository; prefer editing the file in a pull
request so the change is reviewed.

## One stack, many clusters

The moment a stack would be copy-pasted to a second cluster and edited, stop and capture it as a
stack profile instead: every per-cluster difference becomes a parameter, and the two clusters
cannot drift apart silently. `ankra cluster stacks clone <stack> --to <cluster>` is the one-off
route (it lands as a draft on the target and strips encrypted values, which must be reconfigured
there; `ankra cluster stacks deploy-draft <stack>` deploys it); a profile is the durable one. See
`ankra-stack-profiles`. A clone copies configuration only unless `--with-data` is passed, which
carries the stack's data through a backup vault - see `ankra-backups`.

## Related skills

- `ankra-import-cluster` for the surrounding ImportCluster document and every field.
- `ankra-stack-profiles` for reusing a stack across a fleet, with parameters.
- `ankra-helm-registries` for private chart sources.
- `ankra-gitops` for storing stacks in Git.
- `ankra-troubleshooting` when a resource deploys before its dependency.
