---
name: ankra-cli
description: Drive the Ankra CLI to log in, select an organisation and cluster, apply cluster/stack YAML, inspect Kubernetes resources and logs, triage operations, and chat with Ankra AI. Use when the user mentions the `ankra` CLI, `ankra login`, `ankra cluster`, applying an ImportCluster, or managing an Ankra-managed cluster from the terminal.
---

# Ankra CLI

The `ankra` CLI is the terminal client for the Ankra Platform (an AI-powered Kubernetes platform). Use it to manage organisations, clusters, stacks, addons, manifests, credentials, secrets, and to inspect live cluster state.

## Install & keep current

```bash
bash <(curl -sL https://github.com/ankraio/ankra-cli/releases/latest/download/install.sh)
ankra upgrade            # self-update in place (SHA-256 verified, atomic swap)
ankra upgrade --check    # report whether a newer release exists, install nothing
```

Verify: `ankra --version`. Docs: https://docs.ankra.ai

`ankra upgrade --version v0.2.5` pins an exact release (also works as a downgrade/rollback). Pre-release access: `ankra config beta enable` (then `ankra upgrade` includes release candidates), or `ankra upgrade --beta` for a single run.

## Core workflow (do this in order)

```bash
ankra login                 # browser SSO; stores creds in ~/.ankra.yaml
ankra org list              # see organisations
ankra org switch            # pick the active organisation (org current shows it)
ankra cluster list          # see clusters in the org
ankra cluster select        # pick the active cluster (persisted); or: select <name>
ankra cluster info          # confirm the selected cluster
```

Most `ankra cluster ...` subcommands act on the selected cluster. Override per command with `--cluster <name>`. Override the organisation per command with the global `--org <name|id>`.

## Common commands

| Goal | Command |
|------|---------|
| Apply cluster/stack YAML | `ankra cluster apply -f cluster.yaml` |
| Validate locally before applying | `ankra cluster apply -f cluster.yaml --dry-run` |
| Server-side validate (charts/secrets/parents) | `ankra cluster validate -f cluster.yaml [--strict-secrets]` |
| Stage stacks as reviewable drafts | `ankra cluster draft -f cluster.yaml` |
| Trigger reconciliation | `ankra cluster reconcile` |
| List stacks / addons / manifests | `ankra cluster stacks list` / `addons list` / `manifests list` |
| Upgrade an addon / manifest in place | `ankra cluster addons upgrade <addon>` / `manifests upgrade <name>` |
| Inspect workloads | `ankra cluster get pods` (also `deployments`, `services`, `nodes`, `events`, `resources <kind>`, ...) |
| Stream logs | `ankra cluster logs <pod>` |
| Live Helm releases | `ankra cluster helm releases` / `helm uninstall <release>` |
| PromQL against the cluster | `ankra cluster metrics query 'up'` / `metrics query-range ...` |
| Triage automation | `ankra cluster operations list [--watch] [-o json]`, `... retry <id>`, `... cancel <id>` |
| Variables (org/cluster/stack scope) | `ankra org variables ...` / `ankra cluster variables ...` / `ankra cluster stacks variables ...` |
| Ask the AI | `ankra chat "why is my ingress pod crashlooping?"` |

`apply` and the cloud `node-group` mutations **submit asynchronously by default** (return on `202 Accepted`). Add `--wait` to block until the platform finishes (bounded by `--timeout`, default 10m). Avoid re-running the same change while it may still be running.

## Machine-readable output

Every data command supports `-o json` (and `-o yaml`). **Always pass `-o json` when parsing output programmatically** - never scrape tables or prose:

```bash
ankra cluster list -o json
ankra cluster operations list -o json
ankra cluster stacks list <stack> -o json
ankra cluster get pods -o json
```

Write commands (reconcile, provision, deprovision, scaling, node groups, ...) accept `-o json` too and emit the API result including operation IDs, so you can poll `ankra cluster operations list <id> -o json` for completion. Asynchronous writes submitted without `--wait` emit `{"submitted": true, ...}`.

## Connect kubectl to an Ankra cluster

```bash
ankra cluster kubeconfig add --use         # write an exec-based ankra-* context (atomic 0600)
ankra cluster kubeconfig add --embed-token # embed a short-lived token instead of the exec plugin
ankra cluster kubeconfig list / remove     # manage Ankra-managed contexts
ankra cluster kube-token                   # print a Kubernetes ExecCredential (credential plugin)
```

Gateway access is gated by per-member grants. Organisation admins manage them with `ankra cluster access`:

```bash
ankra cluster access list                                # grants + RBAC reconcile status
ankra cluster access grant user@example.com --role view  # roles: view, edit, admin, cluster-admin
ankra cluster access grant user@example.com --role edit --namespace staging
ankra cluster access revoke user@example.com             # by email (all grants) or by grant ID
```

## Cloud clusters, credentials & secrets

- Provision/manage Hetzner, OVH, UpCloud clusters: `ankra cluster hetzner|ovh|upcloud ...` and `ankra credentials ...`. See the **`ankra-cloud-clusters`** skill.
- Helm registries and chart catalog: `ankra helm registries ...`, `ankra helm credentials ...`, `ankra charts search|info`. See **`ankra-helm-registries`**.
- SOPS encrypt/decrypt: `ankra cluster encrypt|decrypt addon|manifest ...`, `ankra cluster sops-config`. See **`ankra-sops-secrets`**.
- Reusable org-level stack profiles: `ankra stack-profiles list|export-iac|import`.

## Report bugs & get help (`ankra support`)

Users **and agents** file bugs and support requests straight from the terminal. When any `ankra` command fails with an unexpected platform response, the CLI prints this exact suggestion - follow it:

```bash
ankra support create \
  --category bug \
  --severity high \
  --cluster prod \
  --subject "cluster apply returns 500" \
  --description "Ran: ankra cluster apply -f cluster.yaml
Got: apply failed: status 500, body: ...
Expected: the apply to be accepted."
```

- Include the **exact command, full error output, and what you expected** in `--description`. `--cluster` links the ticket to a cluster so the team sees its context.
- Every ticket passes a **mandatory AI review**. If it is flagged (low detail, missing reproduction), the create returns guidance - improve the description, or re-submit with `--force` to file it anyway.
- Attach screenshots or images: `ankra support attach <ticket-id> <file>...`
- Track it: `ankra support list`, `ankra support get <ticket-id>` (shows replies and whether the Ankra team is tracking it), `ankra support comment <ticket-id> -m "..."`, `ankra support close <ticket-id>`.
- **Agents** filing on a user's behalf: pass `--source agent` and `-o json` for machine-readable output; never invent reproduction details - quote the real command and output.
- Categories: `technical` (default), `bug`, `account`, `billing`, `feature_request`, `other`. Severities: `low`, `medium`, `high`, `critical`.

## Authentication for automation

For CI or scripts, skip `ankra login` and pass a token created with `ankra tokens create`:

```bash
export ANKRA_API_TOKEN=<token>     # never hardcode in the repo
export ANKRA_ORG=<org>             # optional; or pass --org
ankra cluster info --cluster prod
```

Resolution order: explicit `--token` (paired with `--base-url`), then the saved login from `ankra login`, then `ANKRA_API_TOKEN` (+ optional `ANKRA_BASE_URL`). A saved login token takes precedence over `ANKRA_API_TOKEN` - run `ankra logout` first if you want the env var to win. Use least-privilege tokens and store them in the CI secret store, not in Git.

## Conventions to follow

- Select the org and cluster first, or pass `--org` / `--cluster` explicitly. Never assume the active selection in a shared script.
- Treat mutating commands (`apply`, `reconcile`, `delete`, `provision`, `deprovision`, `roll-to`) as deliberate; confirm the target cluster with `ankra cluster info` first. `apply` / `delete cluster` accept `--dry-run` for a no-token, CI-friendly check.
- Prefer applying versioned YAML (`ankra cluster apply -f`) over ad-hoc edits so the change is reproducible and reviewable. Stage risky changes with `ankra cluster draft` and review them in the stack builder.
- Enable completions once per machine: `ankra completion install`.
- Install these skills into your editor with the CLI itself: `ankra skills list` / `ankra skills install [--project .]`.

## Related skills

- Authoring the YAML you apply: see `ankra-import-cluster` and `ankra-stacks-addons`.
- Provisioning managed cloud clusters: see `ankra-cloud-clusters`.
- Encrypting secrets before commit: see `ankra-sops-secrets`.
