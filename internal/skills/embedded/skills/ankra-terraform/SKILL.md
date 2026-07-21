---
name: ankra-terraform
description: Manage Ankra resources as infrastructure-as-code with the Ankra Terraform provider - clusters, stacks, addons, credentials, and tokens declared in HCL and reconciled by `terraform apply`. Use when the user wants to manage Ankra with Terraform, mentions the Ankra provider, or wants IaC for their Ankra platform setup.
---

# Ankra Terraform Provider

The Ankra Terraform provider lets you declare Ankra resources in HCL and manage them through the standard Terraform workflow, alongside the rest of your infrastructure.

## Provider setup

```hcl
terraform {
  required_providers {
    ankra = {
      source = "ankraio/ankra"
    }
  }
}

provider "ankra" {
  # Prefer an env var: ANKRA_API_TOKEN. Do not hardcode tokens in HCL.
  # token = var.ankra_token
}
```

Authenticate with a scoped API token created via `ankra tokens create`, supplied through `ANKRA_API_TOKEN` or a Terraform variable backed by the secret store — never a literal in committed HCL.

## Workflow

```bash
export ANKRA_API_TOKEN=<scoped-token>
terraform init
terraform plan      # review the diff before applying
terraform apply
```

Always read the `plan` before `apply`; treat resource deletion/replacement as deliberate.

## Rules

- **Token from the environment / secret store**, never committed to HCL or state in plaintext.
- **Protect Terraform state** (remote backend with locking and encryption); state may contain sensitive values.
- **Review `terraform plan`** before every apply; watch for destroy/replace on clusters.
- **Pin the provider version** in `required_providers` for reproducible plans.
- **One source of truth.** Manage a given resource in Terraform OR in GitOps YAML, not both, to avoid fighting reconcilers.

## Related skills

- `ankra-cli` for creating the scoped token (`ankra tokens create`).
- `ankra-gitops` as the alternative declarative path; pick one per resource.
- `ankra-platform-principles` for credential and review discipline.
