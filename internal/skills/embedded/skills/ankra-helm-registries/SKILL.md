---
name: ankra-helm-registries
description: Connect HTTP and OCI Helm chart registries to Ankra and store least-privilege registry credentials (Harbor, Nexus, JFrog Artifactory, ChartMuseum, GHCR, Amazon ECR, Google Artifact Registry, Azure Container Registry, Docker Hub). Use when the user needs charts from a private registry on a Kubernetes cluster, or mentions Helm registries, OCI registries, or registry credentials.
---

# Ankra Helm Registries

Addons pull charts from a `repository_url`. Public charts work as-is; private charts need a registry connected to Ankra with credentials.

## Registry types

- **HTTP Helm repositories** - classic chart servers: ChartMuseum, Harbor, Nexus, JFrog Artifactory, an S3-backed index. `repository_url: https://charts.example.com`.
- **OCI registries** - charts stored as OCI artifacts: GHCR, Amazon ECR, Google Artifact Registry, Azure Container Registry, Docker Hub. `repository_url: oci://registry.example.com/charts`.

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

Manage registries and their credentials with the CLI:

```bash
ankra helm                      # manage Helm registries and registry credentials
ankra charts search <term>      # browse the chart catalog
ankra charts info <chart>       # inspect a chart and its versions
```

## Rules

- **Least privilege.** Registry credentials should be read-only pull tokens, scoped to the needed repositories.
- **Pin `chart_version`.** Always pin; never resolve `latest` from a registry in production.
- **Prefer OCI** for new private registries where the backend supports it.
- **Credentials live in Ankra**, referenced by name from addons - never inline secrets in stack YAML.
- **Verify reachability** from the cluster's network before relying on a private registry in a deploy.

## Related skills

- `ankra-stacks-addons` for the addon fields that consume a registry.
- `ankra-sops-secrets` if a credential must be expressed as an encrypted manifest.
