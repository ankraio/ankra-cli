---
name: ankra-terraform
description: Manage Ankra with the Ankra Terraform provider (ankraio/ankra) - provider configuration (token, organisation_id, project_id), clusters on every lane (Hetzner, DigitalOcean, OVHcloud, UpCloud, Scaleway, AWS, Proxmox, Morpheus, Ankra Cloud), managed Kubernetes and node pools, node groups, imported clusters with Stacks (ankra_cluster), credentials, Helm registries, variables, cluster settings, alerts and DNS, applications and pipelines, stack profiles, backups and the image registry, organisation members and tokens, AI, cost and security settings, plus import, waits, destroy behaviour and known limits. Use when the user wants infrastructure-as-code for Ankra, mentions the Ankra provider or any ankra_* resource or data source, or asks whether something can be managed with Terraform.
---

# Ankra Terraform Provider

The provider is published on the Terraform Registry as `ankraio/ankra`. Since **0.3.0** it covers what the `ankra` CLI can create, change or delete: 97 resources and 42 data sources. On **0.2.x** only the six cluster resources and `ankra_clusters` exist; everything else on this page needs 0.3.0 or later. Check which version the user can install (`terraform providers` or the Registry) before writing HCL for a newer resource. Describe the provider exactly as here: do not invent arguments, and check the Registry docs for each resource's full schema.

Operational commands stay in the CLI: logs, exec, metrics, runs and reruns, `cluster scale`, `cluster upgrade`, chat, the AI board. Never wrap them in `local-exec`.

## Recommended path

This is the **infrastructure as code** golden path in `ankra-platform-principles`: use the provider when the organisation already runs Terraform; otherwise **GitOps as the source of truth** (`ankra-gitops`, `ankra cluster apply`) is the default. Keep one source of truth per object: a given cluster's Stacks live in GitOps YAML or in `ankra_cluster`, never both, and nothing Terraform manages is also changed in the portal or with the CLI. Applications still ship through `ankra-ship` and Ankra Pipelines; Terraform declares them, it does not replace the pipeline.

## Provider block

```hcl
terraform {
  required_providers {
    ankra = {
      source  = "ankraio/ankra"
      version = "~> 0.3.0"
    }
  }
}

provider "ankra" {
  project_id = var.ankra_project_id # required in organisations with more than one project
}

variable "ankra_project_id" {
  type = string
}
```

- **`token`** or `ANKRA_TOKEN`: an Ankra API token (`ankra tokens create`). Prefer the environment variable, or a `sensitive` variable fed from the secret store; never a literal in committed HCL. If no token reaches a resource, it fails with `Missing API token` and sends no request.
- **`base_url`** or `ANKRA_BASE_URL`: defaults to `https://platform.ankra.app`.
- **`organisation_id`** or `ANKRA_ORG` (the CLI's variable for `--org`): act in another organisation the token's user belongs to. An ID, not a slug. The provider ignores `ANKRA_ORGANISATION_ID`, which Ankra Pipelines sets in every step to the run's organisation.
- **`project_id`** or `ANKRA_PROJECT_ID`: the project to act in. **Set it in any organisation that uses projects.** Without it the platform scopes every request to the project the token's user last picked in the portal, so the resources added in 0.3.0 refuse to run in an organisation with more than one project until it is set. An ID, not a slug.
- Terraform 1.0 or newer. `ankra_application_env_secret.value_wo` (write-only) needs Terraform 1.11.

## What exists (0.3.0)

| Area | Resources | Data sources |
|---|---|---|
| Clusters Ankra builds | `ankra_hetzner_cluster`, `ankra_digitalocean_cluster`, `ankra_ovh_cluster`, `ankra_upcloud_cluster`, `ankra_scaleway_cluster`, `ankra_aws_cluster`, `ankra_proxmox_cluster`, `ankra_morpheus_cluster`, `ankra_ankracloud_cluster`, `ankra_playground` | `ankra_clusters`, `ankra_cluster`, `ankra_kubernetes_versions`, `ankra_playground_plans` |
| Managed Kubernetes (DOKS, UKS, GKE, AKS, EKS, OVHcloud MKS, Kapsule, Ankra Cloud Kubernetes) | `ankra_managed_cluster`, `ankra_managed_node_pool` | |
| Node groups | `ankra_node_group` | `ankra_node_groups` |
| Imported clusters and Stacks | `ankra_cluster` | |
| Credentials | `ankra_{hetzner,digitalocean,upcloud,ovh,scaleway,azure,ankracloud,morpheus,proxmox}_credential`, `ankra_aws_role_credential`, `ankra_aws_keys_credential`, `ankra_ssh_key_credential` | `ankra_credentials`, `ankra_credential` |
| Helm and variables | `ankra_helm_registry`, `ankra_helm_registry_credential`, `ankra_organisation_variable`, `ankra_cluster_variable` | `ankra_helm_registries`, `ankra_helm_registry_credentials`, `ankra_helm_chart`, `ankra_organisation_variables`, `ankra_cluster_variables` |
| Cluster settings | `ankra_cluster_access_grant`, `ankra_cluster_ssh_keys`, `ankra_cluster_custom_dns_zone`, `ankra_cluster_power_schedule`, `ankra_cluster_agent_auto_upgrade`, `ankra_cluster_agent_ci_settings`, `ankra_cluster_log_shipping`, `ankra_cluster_mesh`, `ankra_cluster_mesh_member` | `ankra_cluster_access_grants`, `ankra_cluster_custom_dns_zones`, `ankra_cluster_power_schedules`, `ankra_cluster_meshes` |
| Alerts and DNS | `ankra_alert_destination`, `ankra_alert_route`, `ankra_organisation_dns_record`, `ankra_organisation_custom_dns_zone`, `ankra_organisation_domain`, `ankra_dns_credential`, `ankra_cloudflare_credential`, `ankra_cloudflare_dns_record` | `ankra_alert_destinations`, `ankra_alert_routes`, `ankra_organisation_dns_zone`, `ankra_organisation_dns_records`, `ankra_organisation_custom_dns_zones`, `ankra_dns_credentials`, `ankra_cloudflare_credentials`, `ankra_cloudflare_domains` |
| Applications and pipelines | `ankra_application`, `ankra_application_settings`, `ankra_application_auto_deploy`, `ankra_application_ai_config`, `ankra_application_demo_config`, `ankra_application_env_secret`, `ankra_application_deployment`, `ankra_pipeline_repository`, `ankra_pipeline_definition`, `ankra_pipeline_schedule`, `ankra_pipeline_merge_train` | `ankra_applications`, `ankra_application`, `ankra_pipeline_repositories` |
| Stack profiles, backups, image registry | `ankra_stack_profile`, `ankra_stack_profile_version`, `ankra_stack_profile_share`, `ankra_stack_profile_logo`, `ankra_backup_vault`, `ankra_object_storage_bucket`, `ankra_registry_project`, `ankra_registry_robot`, `ankra_registry_retention` | `ankra_stack_profiles`, `ankra_backup_vaults`, `ankra_object_storage_buckets`, `ankra_registry_projects`, `ankra_registry_robots` |
| Organisation and access | `ankra_organisation`, `ankra_organisation_member`, `ankra_api_token`, `ankra_service_token`, `ankra_mcp_server`, `ankra_mcp_tool_grant`, `ankra_ci_settings`, `ankra_ci_pool_member`, `ankra_ai_review_repository`, `ankra_ai_review_related_repository`, `ankra_ai_spend_cap`, `ankra_ai_environment` | `ankra_organisation`, `ankra_organisations`, `ankra_mcp_servers` |
| AI, cost, security, services, targets | `ankra_ai_provider`, `ankra_ai_anthropic_key`, `ankra_ai_openrouter_key`, `ankra_ai_openai_compatible`, `ankra_ai_endpoint`, `ankra_ai_model`, `ankra_ai_lane_model`, `ankra_ai_board_identity`, `ankra_cost_budget`, `ankra_cost_autopilot`, `ankra_cost_autopilot_override`, `ankra_cost_settings`, `ankra_security_disposition`, `ankra_security_compliance_framework`, `ankra_security_report_schedule`, `ankra_service_placement_policy`, `ankra_service_consumer`, `ankra_host_join_token` | `ankra_ai_models`, `ankra_ai_endpoints`, `ankra_ai_lanes`, `ankra_cost_budgets`, `ankra_security_compliance_frameworks`, `ankra_host_targets`, `ankra_deploy_environments` |

Not available as Terraform: stack variables (stack-scoped `ankra cluster stacks variables`; use cluster or organisation variables, or the stack's own YAML), managed services setup, stack profile builder drafts, restore points and restores, migrations, and every operational command above.

## A cluster from scratch

```hcl
resource "ankra_hetzner_credential" "main" {
  name      = "hetzner-main"
  api_token = var.hcloud_token
}

resource "ankra_hetzner_cluster" "prod" {
  name          = "prod"
  credential_id = ankra_hetzner_credential.main.id
  location      = "fsn1"

  control_plane_count       = 3
  control_plane_server_type = "cpx32"
  worker_count              = 2
  worker_server_type        = "cpx32"

  gitops_repository      = "my-org/platform-gitops"
  gitops_branch          = "main"
  gitops_credential_name = "my-github-credential"
}

resource "ankra_node_group" "gpu" {
  cluster_id    = ankra_hetzner_cluster.prod.cluster_id
  name          = "gpu"
  instance_type = "cx42"
  node_count    = 2
  labels        = { accelerator = "gpu" }
}

resource "ankra_cluster_variable" "domain" {
  cluster_id = ankra_hetzner_cluster.prod.cluster_id
  name       = "ingress_domain"
  value      = "apps.example.com"
}

variable "hcloud_token" {
  type      = string
  sensitive = true
}
```

## Clusters

- **Lane arguments** keep each cloud's own words: Hetzner `*_server_type` + `location`; DigitalOcean `*_size` + `region`; OVHcloud `*_flavor_id` + `region`; UpCloud `*_plan` + `zone`; Scaleway `*_type` + `region` + `zone`; AWS, Proxmox, Morpheus and Ankra Cloud likewise. `ssh_key_credential_id` (required except on Hetzner) is an `ankra_ssh_key_credential.id`.
- **A cluster's own provisioning arguments force replacement** (counts and sizes on the cluster resource included). Resize with `ankra_node_group` (`node_count`, `instance_type`, `labels`, `taints`, `autoscaling` update in place; an instance-type change drains and power-cycles each node and the apply waits for the roll) or with `ankra cluster scale`. Never present a replacement as a resize; read the plan.
- `distribution` defaults to `kubeadm`, `cni` to `cilium`. Create waits for `running` (default 60m); `wait_for_ready = false` returns at once.
- **Destroy waits** until the cluster has left Ankra on the newer lanes. `force_destroy` defaults to `false` on AWS, Proxmox, Morpheus and Ankra Cloud and to `true` on the original five; `accept_volume_data_loss` (AWS, Ankra Cloud) must be set before the platform deletes a cluster that still holds persistent volumes.
- **`ankra_managed_cluster`**: `provider_name` + `credential_id` + `location`, provider-specific options as `provider_options = jsonencode({...})`, and `node_pools` as a **map keyed by pool name**. `node_count` and `autoscaling` change in place; adding a key adds a pool, removing one deletes it. To change a pool's size, rename its key: the new pool is added and the old one is deleted only after the new pool's nodes are Ready (UKS and OVHcloud MKS cannot be verified, so both pools are kept and the apply errors). `destroy_mode` is `delete_provider_cluster` for clusters this resource created and `disconnect` for imported ones. `wait_for_agent = true` waits until the agent is `online`.
- **`ankra_playground`**: moving from a paid plan to `trial` is refused unless `allow_trial_downgrade = true`.

## `ankra_cluster` (an existing cluster and its Stacks)

```hcl
resource "ankra_helm_registry" "traefik" {
  name = "traefik"
  url  = "https://traefik.github.io/charts"
}

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
      registry_name     = ankra_helm_registry.traefik.name
      registry_url      = ankra_helm_registry.traefik.url
      namespace         = "traefik"
      parents           = ["manifest:traefik-namespace"]
      configuration     = file("${path.module}/traefik-values.yaml")
      job_configuration = jsonencode({ create_job_timeout = 600 })
    }
  }
}
```

- **`parents`**: `"manifest:<name>"` or `"addon:<name>"`; a bare name only when exactly one member has it. Unset keeps the stored order; `[]` removes it.
- **Add-ons**: `registry_url` (`https://` or `oci://`) is required; `registry_name` / `registry_credential_name` name an `ankra_helm_registry` / `ankra_helm_registry_credential`. Pin `chart_version`, for example from `data.ankra_helm_chart.<name>.latest_version`.
- `cluster_name` forces replacement; creating with an existing name adopts that cluster. `helm_command` (sensitive) installs the agent and is issued only on first registration; the user runs it. Stack variables are kept when the stack spec does not mention them.
- Changing `github_repository` or `github_branch` fails with a 409: repoint with `ankra cluster apply -f cluster.yaml --allow-repoint` (see `ankra-import-cluster`), then update the HCL.

## Rules that hold across resources

- **Secrets are write-only.** API tokens, keys, passwords and webhook URLs are `Sensitive`; the platform never returns them, so a secret changed outside Terraform is not detected. State still holds what the configuration set (prefer `value_wo` on `ankra_application_env_secret` with Terraform 1.11+). One-time values (`ankra_api_token.token`, `ankra_service_token.token`, `ankra_registry_robot` secret, a generated SSH private key, `ankra_host_join_token`) are captured once into state.
- **A scope change is not a deletion.** When `organisation_id` or `project_id` changes, objects from the old scope are reported "not visible in the provider's current scope": they are neither recreated nor reported destroyed. Point the provider back, or `terraform state rm` to stop managing them.
- **Settings resources** (`ankra_ci_settings`, `ankra_ai_environment`, `ankra_ai_spend_cap`, `ankra_cluster_agent_ci_settings`, `ankra_cost_settings`, `ankra_cost_autopilot`) write only the attributes the configuration sets and reset only those on destroy. Some refuse to create over settings already customised in the portal; import them instead.
- **Tokens.** Changing a token's expiry, scopes, label or project mints a new token value in place (the new one first, then the old one is retired), so every holder must get the new value; `scopes = []` is refused because an empty list means full authority. A service account's role and a member's role are changed in the portal, not Terraform; the plan stops when they differ.
- **Destructive replacements are refused at plan** where they would lose data: a backup vault or bucket replacement while `destroy_provider_resources = true`, a key change on a bring-your-own vault, a node pool shape change, a lane's `retention_policy` change.
- **Applications.** Creating `ankra_application` opens a setup pull request on its repository (merged unattended if `ankra_application_settings.auto_merge_setup_pull_requests` is on); replacing one tears down every deployment. Recommend `lifecycle { prevent_destroy = true }`. `ankra_application_deployment` waits (create timeout, default 30m) until setup is complete. `ankra_pipeline_definition` is for repositories without a committed `.ankra/pipeline.yaml`, and protected sections still need an admin's `definitions approve`.
- **Plan-time validation** refuses what the platform would refuse at apply: reserved names, names with `/`, values with surrounding whitespace the platform trims, enum values, past timestamps.

## Import

Every resource imports, usually by its ID; cluster-scoped objects by `<cluster_id>/<key>` and managed clusters by `<provider_name>/<cluster_id>` (the Registry docs list each format). Cluster resources and credentials adopt the configured build arguments and secrets on the first apply after an import instead of planning a replacement, because the platform does not return them: write the configuration to match the real object before applying. `ankra_host_join_token` cannot be imported.

## Known limits

- Stack variables cannot be managed: the token API offers no safe per-variable write yet.
- Resizes made outside Terraform to a cluster's own arguments are not detected; node groups and node pools do refresh.
- A credential or other secret changed outside Terraform is not detected (see above).
- On 0.2.x: only the six cluster resources and `ankra_clusters`; the cloud lanes' provisioning arguments always force replacement, including after an import.

## Upgrading

- **0.2.x to 0.3.0**: set `project_id` if the organisation uses projects. Imports of the original five cloud clusters now adopt the configuration instead of planning a rebuild. Read the first plan: validation is stricter.
- **0.1.x to 0.2.x**: set `distribution = "k3s"` and `cni = "flannel"` on clusters created with the old defaults before upgrading, move the token to the provider, use `registry_url` + `registry_name` and `job_configuration = jsonencode({...})` on add-ons, prefix `parents` with `manifest:` / `addon:`, and rotate any agent token a 0.1.x plan printed (`helm_command` was not sensitive).

## Rules

- **Read every `terraform plan`** before applying; stop on any destroy or replace of a cluster, credential, token, vault, bucket or application and show it to the user.
- **Pin the provider version** in `required_providers`.
- **Protect Terraform state** with a locking, encrypted remote backend: it holds tokens, secrets and `helm_command`.
- **One source of truth.** Manage a given object from Terraform, the CLI, the portal or GitOps YAML, never two of them.
- Without a provider resource for what the user needs, use the CLI rather than `local-exec`.

## Related skills

- `ankra-cloud-clusters` and `ankra-managed-kubernetes` for choosing regions, machine types and providers.
- `ankra-import-cluster` for the ImportCluster YAML that `ankra cluster apply -f` takes.
- `ankra-gitops` for keeping cluster and Stack definitions in Git.
- `ankra-security` for token scopes, roles and access grants.
- `ankra-cli` for creating the token (`ankra tokens create`).
- `ankra-platform-principles` for credential and review discipline.
