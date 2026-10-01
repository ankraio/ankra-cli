---
name: ankra-terraform
description: Manage Ankra clusters with the Ankra Terraform provider (ankraio/ankra 0.2.x) - provider token configuration, creating clusters on Hetzner, DigitalOcean, OVHcloud, UpCloud and Scaleway through Ankra, registering an existing cluster with its Stacks (ankra_cluster), the ankra_clusters data source, import, readiness waits, known limits, and upgrading from 0.1.x without replacing clusters. Use when the user wants to manage Ankra with Terraform, mentions the Ankra provider, `ankra_cluster` or an `ankra_*_cluster` resource, or wants infrastructure-as-code for their Ankra clusters.
---

# Ankra Terraform Provider

The provider is published on the Terraform Registry as `ankraio/ankra`. Recommend **0.2.1 or later**: 0.1.6 cannot create or update a cluster against the current Ankra API, and 0.2.0 refuses a configuration that sets the token only on each resource. Describe it exactly as below; do not offer arguments, resources or data sources that are not listed here, and check the Registry docs for anything this skill does not cover.

## Provider block

```hcl
terraform {
  required_providers {
    ankra = {
      source  = "ankraio/ankra"
      version = "~> 0.2.1"
    }
  }
}

provider "ankra" {}
```

- **`token`** or `ANKRA_TOKEN`: an Ankra API token (`ankra tokens create`). Prefer the environment variable, or a `sensitive` variable fed from the secret store; never a literal in committed HCL.
- **`base_url`** or `ANKRA_BASE_URL`: defaults to `https://platform.ankra.app`.
- Terraform 1.0 or newer (plugin protocol 6).
- If no token reaches a resource, it fails with `Missing API token` and sends no request.

## What exists

| Name | Manages |
|---|---|
| `ankra_hetzner_cluster`, `ankra_digitalocean_cluster`, `ankra_ovh_cluster`, `ankra_upcloud_cluster`, `ankra_scaleway_cluster` | A cluster Ankra creates on that cloud. Scaleway is a closed-beta provider: it only works for organisations that have it turned on. |
| `ankra_cluster` | A cluster the user runs themselves, registered with Ankra with its cluster GitOps repository (GitHub) and Stacks. |
| `ankra_clusters` (data source) | `id` and `name` of every cluster the token can see. |

There is no resource for managed Kubernetes (DOKS, UKS, GKE, AKS, EKS, OVHcloud MKS, Kapsule), AWS EC2, Proxmox, Morpheus, Ankra Cloud, node groups, credentials or tokens. Use the CLI for those (see the `ankra-cloud-clusters` and `ankra-managed-kubernetes` skills).

## Cloud cluster resources

```hcl
resource "ankra_hetzner_cluster" "dev" {
  name          = "my-cluster"
  credential_id = var.hetzner_credential_id
  location      = "fsn1"

  control_plane_count       = 1
  control_plane_server_type = "cpx22"
  worker_count              = 2
  worker_server_type        = "cpx22"

  gitops_repository      = "my-org/my-repo"
  gitops_branch          = "main"
  gitops_credential_name = "my-github-credential"
}
```

- Each cloud keeps its own words for size and place: Hetzner `*_server_type` + `location`; DigitalOcean `*_size` + `region`; OVHcloud `*_flavor_id` + `region` (+ `gateway_flavor_id`, `availability_zones`); UpCloud `*_plan` + `zone`; Scaleway `*_type` + `region` + `zone` (+ `gateway_type`, `retention_policy`).
- `credential_id` is the ID column of `ankra credentials list`. `ssh_key_credential_id` (required except on Hetzner) comes from `ankra credentials hetzner ssh-key list` (or the same command for the other provider).
- `distribution` defaults to `kubeadm` and `cni` to `cilium`. For k3s set `distribution = "k3s"` and `cni = "flannel"` or `"calico"`.
- **Every provisioning argument forces replacement.** Changing a count or size in HCL destroys and recreates the cluster. Scale and resize existing clusters with the CLI (`ankra cluster scale`, `ankra cluster node-group`), and never present a Terraform edit as a resize.
- Create waits for the cluster to reach `running` (default 60m, `timeouts { create = "90m" }`); `wait_for_ready = false` returns as soon as Ankra accepts it. Keep the wait when anything in the same plan uses the cluster.
- `terraform destroy` deprovisions the cluster and what Ankra created in the cloud account. `force_destroy` (default `true`) skips the platform's guards; `false` takes the guarded path.
- Read-only `cluster_id`, `state`, `kind`.

## `ankra_cluster`

```hcl
resource "ankra_cluster" "prod" {
  cluster_name           = "my-cluster"
  github_credential_name = "my-github-credential"
  github_repository      = "my-org/my-repo"
  github_branch          = "main"

  stacks {
    name = "ingress"

    manifests {
      name            = "traefik-namespace"
      manifest_base64 = base64encode(file("${path.module}/traefik-namespace.yaml"))
    }

    addons {
      name              = "traefik"
      chart_name        = "traefik"
      chart_version     = "37.1.1"
      registry_name     = "traefik"
      registry_url      = "https://traefik.github.io/charts"
      namespace         = "traefik"
      parents           = ["manifest:traefik-namespace"]
      configuration     = file("${path.module}/traefik-values.yaml")
      job_configuration = jsonencode({ create_job_timeout = 600 })
    }
  }
}
```

- **`parents`**: `"manifest:<name>"` or `"addon:<name>"`; a bare name only when exactly one member of the resource has it. Unset keeps the order stored in Ankra; `[]` removes it. Declare an order for every member that needs another first (namespaces and secrets before the add-ons that use them).
- **Add-ons**: `registry_url` (`https://` or `oci://`) is required; `registry_name` is the Helm registry's name in the organisation (derived from the URL when omitted, accepted only if a registry with that URL is connected); `registry_credential_name` for a private registry. `repository_url` is a deprecated alias of `registry_url`; `configuration_type` is ignored.
- **`configuration`**: Helm values as YAML or base64. **`job_configuration`**: `jsonencode` of `create_job_timeout`, `read_job_timeout`, `update_job_timeout`, `delete_job_timeout` (seconds).
- `cluster_name` forces replacement. Create with a name that already exists **adopts** that cluster (Ankra updates it and Terraform manages it).
- `helm_command` (sensitive) installs the agent; it is issued only on first registration, so it is empty for an adopted cluster or a queued import. Terraform cannot run it; the user runs it against the cluster. `wait_for_online = true` waits for the agent to check in.
- Timeouts default to 60m create, 20m update. `terraform destroy` deletes the cluster from Ankra.
- `ankra_token` on the resource still works but is deprecated; changing it is an in-place update.

## Import

Every resource imports by Ankra cluster ID: `terraform import ankra_cluster.prod <cluster-id>`. On cloud resources, write the configuration to match the cluster before applying: any provisioning mismatch plans a replacement.

## Known limits

- Changing `github_repository` or `github_branch` on `ankra_cluster` fails with a 409: Ankra refuses a GitOps repoint until it is acknowledged, which the provider cannot do. Do the repoint with `ankra cluster apply -f cluster.yaml --allow-repoint` (see `ankra-import-cluster`), then update the HCL to match.
- Resizes made outside Terraform are not detected (the cluster listing carries no sizing). `state` and `kind` refresh on every read, and a cluster deleted outside Terraform leaves state.

## Upgrading from 0.1.x

Before `terraform init -upgrade`:

1. On cloud resources created with the old defaults, set `distribution = "k3s"` and `cni = "flannel"` explicitly, or the plan replaces the cluster.
2. Move the token to the provider (`ANKRA_TOKEN`); per-resource `ankra_token` keeps working on 0.2.1.
3. Add-ons: `registry_url` + `registry_name` instead of `repository_url`; `job_configuration = jsonencode({...})`.
4. `parents`: use `manifest:` / `addon:` prefixes.
5. `helm_command` was not sensitive in 0.1.x: rotate any agent token a plan or CI log printed.

Then read the plan and stop on any replace the user did not ask for.

## Rules

- **Read every `terraform plan`** before applying; stop on any destroy or replace of a cluster resource and show it to the user.
- **Pin the provider version** in `required_providers`.
- **Protect Terraform state** with a locking, encrypted remote backend: it holds the token and `helm_command`.
- **One source of truth.** Manage a given cluster from Terraform, `ankra cluster apply` or GitOps YAML, never two of them.
- Without a provider resource for what the user needs, use `ankra cluster apply -f` or the CLI rather than wrapping commands in `local-exec`.

## Related skills

- `ankra-cloud-clusters` for choosing regions and machine types, and day-2 operations on provisioned clusters.
- `ankra-import-cluster` for the ImportCluster YAML that `ankra cluster apply -f` takes.
- `ankra-gitops` for keeping cluster and Stack definitions in Git.
- `ankra-cli` for creating the scoped token (`ankra tokens create`).
- `ankra-platform-principles` for credential and review discipline.
