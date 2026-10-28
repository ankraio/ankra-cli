---
name: ankra-helm-registries
description: Connect HTTP and OCI Helm chart registries to Ankra, declare them in the GitOps cluster YAML (`spec.helm_registries`), and store least-privilege registry credentials (Harbor, Nexus, JFrog Artifactory, ChartMuseum, GHCR, Amazon ECR, Google Artifact Registry, Azure Container Registry, Docker Hub), then find and pin chart versions for add-ons. Use when the user needs private charts, mentions Helm registries, OCI registries, chart sync, or registry credentials in Ankra.
---

# Ankra Helm Registries

## Recommended path

- Golden path 2 (GitOps as the source of truth; see `ankra-platform-principles`): declare the
  registry in the cluster YAML under `spec.helm_registries`, reference it from each add-on by
  `registry_name` + `registry_url`, and pin an exact `chart_version`.
- First store a read-only pull credential (`ankra helm credentials create`), then declare or
  create the registry, then confirm the chart and version with `ankra charts`.
- Add-on wiring is `ankra-stacks-addons`; container images (rather than charts) are
  `ankra-app-integrations`.
- Anti-patterns: an unpinned or floating chart version, a push or admin credential used for pulls,
  and a credential value written into Git.

Add-ons pull charts from a registry connected to Ankra. Every organisation also sees a curated set
of global registries that power the add-on catalogue; a private or extra chart source needs a
registry of your own, with a credential when it is private.

## Registry types

The type is inferred from the URL scheme.

| Type | URL | Examples |
|------|-----|----------|
| **OCI** | `oci://...` | `oci://ghcr.io/ORG/charts`, `oci://REGION-docker.pkg.dev/PROJECT/REPO`, `oci://ACCOUNT.dkr.ecr.REGION.amazonaws.com/charts`, `oci://NAME.azurecr.io/charts`, `oci://registry-1.docker.io/ORG`, Harbor 2.8+ `oci://harbor.example.com/PROJECT` |
| **HTTP** | `https://...` | ChartMuseum `https://charts.example.com`, Harbor before 2.8 `https://harbor.example.com/chartrepo/PROJECT`, Nexus `https://nexus.example.com/repository/helm-hosted/`, Artifactory `https://artifactory.example.com/artifactory/helm-local` |

## Declare it in Git (preferred)

The cluster YAML can declare the registries its add-ons depend on, so the repository is
self-contained: `ankra cluster apply` connects any registry not already present before add-ons
deploy. A declaration matching an existing registry (same name and URL, organisation or global) is
a no-op; a name already taken by a different URL fails the apply before anything is written.
Credentials are referenced by name and must already exist, so secrets stay out of Git.

```yaml
spec:
  helm_registries:
    - name: private-hub
      url: oci://harbor.example.com/charts
      credential_name: harbor-pull          # an existing Helm registry credential
      exclude_charts: [legacy-chart]        # optional: keep charts out of the index
  stacks:
    - name: apps
      addons:
        - name: my-app
          chart_name: my-app
          chart_version: 2.3.1              # exact version, always
          registry_name: private-hub
          registry_url: oci://harbor.example.com/charts
          registry_credential_name: harbor-pull
          namespace: app
```

## Manage registries with the CLI

```bash
ankra helm credentials create --name harbor-pull --username robot-reader   # password is prompted, never a flag
ankra helm credentials list
ankra helm credentials get harbor-pull            # the password is never returned
ankra helm credentials update harbor-pull --username <user>   # re-prompts the password: rotation
ankra helm credentials delete harbor-pull

ankra helm registries create --name private-hub --url oci://harbor.example.com/charts \
  --credential-name harbor-pull --exclude-charts legacy-chart
ankra helm registries create -f registry-spec.yaml
ankra helm registries list --search hub          # --all for every page; -o json
ankra helm registries get private-hub            # the registry and its indexed charts
ankra helm registries update private-hub --read-job-interval 600   # seconds between syncs; 0 disables
ankra helm registries sync private-hub           # re-index now
ankra helm registries sync-jobs private-hub      # why a chart is missing from the catalogue
ankra helm registries delete private-hub         # deployed add-ons keep running
```

A spec file nests the registry under exactly one of `helm_oci_registry` or `helm_http_registry`
(a flat `{name, url}` file is accepted and nested automatically).

Ankra syncs every registry on an interval (every 2 minutes by default) and retries failed
versions. When a chart you expect is not offered, `sync` forces a re-index and `sync-jobs` shows
what the last runs did.

## Credentials per provider

Use a read-only token, scoped to the repositories Ankra needs:

| Registry | Username | Password |
|----------|----------|----------|
| GHCR | GitHub user | token with `read:packages` |
| Google Artifact Registry | `_json_key` | key of a service account with Artifact Registry Reader |
| Amazon ECR | `AWS` | `aws ecr get-login-password` output - it expires after 12 hours, so refresh it |
| Docker Hub | user | an access token |
| Harbor | a robot account | its secret, pull-only on the project |

## Find the chart before writing the add-on

```bash
ankra charts list
ankra charts search <term>
ankra charts info <chart>                      # versions, home, description
ankra charts values <chart>                    # default values (helm show values)
ankra charts template <chart>                  # rendered manifests (helm template)
```

`charts values` is where an add-on's `configuration.values` starts: read the defaults, override
only what you mean to. `charts template` shows what a chart would create before anything touches a
cluster.

## Rules

- **Pin `chart_version`** to an exact version; never resolve "latest" in production.
- **Least privilege.** Pull-only credentials, scoped to the needed repositories; one credential per
  purpose.
- **Credentials live in Ankra**, referenced by name from Git - never inline in stack YAML.
- **Declare registries in the cluster YAML** so a fresh cluster applies from the repository alone.
- **Prefer OCI** for new private registries where the backend supports it.
- **Global registries** are proposed through the public `ankraio/ankra-global-resources`
  repository, not by connecting them per organisation.

## Related skills

- `ankra-stacks-addons` for the add-on fields that consume a registry.
- `ankra-gitops` / `ankra-import-cluster` for the cluster YAML the declaration lives in.
- `ankra-app-integrations` for the container-image side of Harbor and friends.
- `ankra-security` for credential scope and review.
