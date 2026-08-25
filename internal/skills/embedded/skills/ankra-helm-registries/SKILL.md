---
name: ankra-helm-registries
description: Connect HTTP and OCI Helm chart registries to Ankra and store least-privilege registry credentials (Harbor, Nexus, JFrog Artifactory, ChartMuseum, GHCR, Amazon ECR, Google Artifact Registry, Azure Container Registry, Docker Hub). Use when the user needs private charts, mentions Helm registries, OCI registries, or registry credentials in Ankra.
---

# Ankra Helm Registries

Addons pull charts from a `repository_url`. Public charts work as-is; private charts need a registry connected to Ankra with credentials.

## Registry types

- **HTTP Helm repositories** — classic chart servers: ChartMuseum, Harbor, Nexus, JFrog Artifactory, an S3-backed index. `repository_url: https://charts.example.com`.
- **OCI registries** — charts stored as OCI artifacts: GHCR, Amazon ECR, Google Artifact Registry, Azure Container Registry, Docker Hub. `repository_url: oci://registry.example.com/charts`.

## Connect and use

1. Store a registry credential in Ankra (scoped, read-only where possible).
2. Reference the registry from the addon. Example with an OCI private chart:

```yaml
addons:
  - name: my-app
    chart_name: my-app
    chart_version: 2.3.1
    repository_url: oci://ghcr.io/my-org/charts
    namespace: app
    # link the stored registry credential by name
    registry_credential_name: my-ghcr-credential
```

## Manage registries with the CLI

```bash
ankra helm credentials create ...              # store the pull credential first
ankra helm credentials list

ankra helm registries create --name my-charts --url oci://ghcr.io/acme/charts \
  --credential-name my-cred
ankra helm registries create --name my-charts --url https://charts.example.com
ankra helm registries create -f registry-spec.yaml
ankra helm registries list
ankra helm registries get <name>
ankra helm registries update <name> ...
ankra helm registries sync <name>              # trigger a manual index sync
ankra helm registries sync-jobs <name>         # why a chart is missing from the catalog
ankra helm registries delete <name>
```

The registry type is inferred from the URL scheme — `oci://` creates an OCI registry, `http(s)://`
an HTTP chart repository. A spec file nests the registry under exactly one of `helm_oci_registry`
or `helm_http_registry` (a flat `{name, url}` file is accepted and nested automatically).
`--exclude-charts` (repeatable) keeps named charts out of the index.

When a chart you expect is not offered, the sync is the usual reason: `registries sync` forces a
re-index and `sync-jobs` shows what the last runs did.

## Find the chart before writing the addon

```bash
ankra charts list
ankra charts search <term>
ankra charts info <chart>                      # versions, home, description
ankra charts values <chart>                    # default values (helm show values)
ankra charts template <chart>                  # rendered manifests (helm template)
```

`charts values` is where an addon's `configuration.values` starts: read the defaults, override only
what you mean to. `charts template` shows what a chart would actually create before anything
touches a cluster.

## Rules

- **Least privilege.** Registry credentials should be read-only pull tokens, scoped to the needed repositories.
- **Pin `chart_version`.** Always pin; never resolve `latest` from a registry in production.
- **Prefer OCI** for new private registries where the backend supports it.
- **Credentials live in Ankra**, referenced by name from addons — never inline secrets in stack YAML.
- **Verify reachability** from the cluster's network before relying on a private registry in a deploy.

## Related skills

- `ankra-stacks-addons` for the addon fields that consume a registry.
- `ankra-app-integrations` for the container-image side of Harbor and friends.
- `ankra-sops-secrets` if a credential must be expressed as an encrypted manifest.
- `ankra-security` for credential scope and review.
