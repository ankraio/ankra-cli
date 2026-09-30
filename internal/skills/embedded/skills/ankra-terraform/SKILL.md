---
name: ankra-terraform
description: What the released Ankra Terraform provider (ankraio/ankra 0.1.6) can and cannot do, and how to keep Ankra cluster definitions as code today with `ankra cluster apply -f` or GitOps instead. Use when the user wants to manage Ankra with Terraform, mentions the Ankra provider or `ankra_cluster`, or wants infrastructure-as-code for their Ankra clusters.
---

# Ankra Terraform Provider

The Ankra Terraform provider is published on the Terraform Registry as `ankraio/ankra`. The latest release is **0.1.6**. Describe it exactly as it is below; do not offer provider arguments, resources or data sources that are not listed here.

## Current status: prefer `ankra cluster apply` or GitOps

**Provider 0.1.6 cannot create or update a cluster against the current Ankra API.** Ankra now registers a cluster in the background and answers without a cluster ID; 0.1.6 treats that as a failure, so `terraform apply` stops with `Failed to create cluster: missing cluster_id` even though Ankra has registered the cluster, which is then left outside Terraform state.

Until a fixed provider release is published, keep cluster definitions as code with:

- **`ankra cluster apply -f cluster.yaml`**: the same cluster definition as an ImportCluster YAML file, runnable from any pipeline. Add `--dry-run` to validate it locally first. See the `ankra-import-cluster` skill.
- **GitOps**: the cluster's Stacks and add-ons live in a Git repository that Ankra syncs. See the `ankra-gitops` skill.
- **Terraform without the provider**, only when the rest of the stack must stay in one Terraform run: call `ankra cluster apply -f` from a `terraform_data` resource with a `local-exec` provisioner.

Check the Registry for a newer release before recommending the provider for a new setup; if the user already runs 0.1.6, point them at the alternatives above rather than working around the failure.

## What 0.1.6 contains

```hcl
terraform {
  required_providers {
    ankra = {
      source  = "ankraio/ankra"
      version = "0.1.6"
    }
  }
}

provider "ankra" {}
```

- **Provider configuration:** none. The `provider` block takes no arguments; the token is set on each resource.
- **Resources:** `ankra_cluster` only.
- **Data sources:** none.
- **`terraform import`:** not supported.
- **Drift detection:** none. Reading a cluster is a no-op, so a plan compares the configuration with Terraform state only.
- **Endpoint:** fixed to `https://platform.ankra.app`.

### `ankra_cluster`

Registers an imported cluster with its cluster GitOps repository on GitHub, plus optional Stacks of raw manifests.

| Argument | Required | Notes |
|---|---|---|
| `cluster_name` | Yes | Changing it replaces the cluster. |
| `ankra_token` | Yes | An Ankra API token (`ankra tokens create`). Sensitive. Changing it **replaces the cluster**: Terraform destroys and recreates it. |
| `github_credential_name` | Yes | Name of the GitHub credential stored in Ankra. |
| `github_repository` | Yes | `my-org/my-repo` |
| `github_branch` | Yes | |
| `stacks` | No | Blocks with `name`, `description` and `manifests`. |
| `stacks.manifests` | No | `name`, `namespace` and `manifest_base64`. Leave `parents` unset: 0.1.6 sends it in a shape the API refuses. |
| `stacks.addons` | No | Do not use: its fields do not match the API, which refuses it. |

| Attribute | Notes |
|---|---|
| `cluster_id` | The Ankra cluster ID. |
| `helm_command` | The command that installs the Ankra agent. It contains a live agent token; treat it as a secret. |

`terraform destroy` deletes the cluster from Ankra by name.

## Rules

- **Token from a variable backed by the secret store** (`var.ankra_token` marked `sensitive`), never a literal in committed HCL. It is stored in state, so state is sensitive too.
- **Never rotate `ankra_token` in place** on 0.1.6: the change plans a destroy and recreate of the cluster.
- **Protect Terraform state** with a remote backend that locks and encrypts; it holds the token and `helm_command`.
- **Read every `terraform plan`** before applying; stop on any destroy or replace of `ankra_cluster`.
- **Pin the provider version** in `required_providers`.
- **One source of truth.** Manage a given cluster from Terraform, `ankra cluster apply` or GitOps YAML, never two of them, so the tools do not overwrite each other's changes.

## Related skills

- `ankra-import-cluster` for the ImportCluster YAML that `ankra cluster apply -f` takes.
- `ankra-gitops` for keeping cluster and stack definitions in Git.
- `ankra-cli` for creating the scoped token (`ankra tokens create`).
- `ankra-platform-principles` for credential and review discipline.
