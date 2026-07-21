---
name: ankra-cli
description: Drive the Ankra CLI to log in, select an organisation and cluster, apply cluster/stack YAML, inspect Kubernetes resources and logs, triage operations, and chat with Ankra AI. Use when the user mentions the `ankra` CLI, `ankra login`, `ankra cluster`, applying an ImportCluster, or managing an Ankra-managed cluster from the terminal.
---

# Ankra CLI

The `ankra` CLI is the terminal client for the Ankra Platform (an AI-powered Kubernetes platform). Use it to manage organisations, clusters, stacks, addons, manifests, credentials, and to inspect live cluster state.

## Install

```bash
bash <(curl -sL https://github.com/ankraio/ankra-cli/releases/latest/download/install.sh)
```

Verify: `ankra --version`. Docs: https://docs.ankra.ai

## Core workflow (do this in order)

```bash
ankra login                 # browser SSO; stores creds in ~/.ankra.yaml
ankra org list              # see organisations
ankra org select            # pick the active organisation
ankra cluster list          # see clusters in the org
ankra cluster select        # pick the active cluster (persisted)
ankra cluster info          # confirm the selected cluster
```

Most `ankra cluster ...` subcommands act on the selected cluster. Override per command with `--cluster <name>`. Override the organisation per command with the global `--org <name|id>`.

## Common commands

| Goal | Command |
|------|---------|
| Apply cluster/stack YAML | `ankra cluster apply -f cluster.yaml` |
| Trigger reconciliation | `ankra cluster reconcile` |
| List stacks / addons / manifests | `ankra cluster stacks list` / `addons list` / `manifests list` |
| Inspect workloads | `ankra cluster get pods` (also `deployments`, `services`, `nodes`, `events`, ...) |
| Stream logs | `ankra cluster logs <pod>` |
| Triage automation | `ankra cluster operations list`, `... retry`, `... cancel` |
| Ask the AI (read-only) | `ankra chat --mode ask "why is my ingress pod crashlooping?"` |
| Let the AI act | `ankra chat --mode agent "open a PR fixing the failing lint job"` |

## AI safety modes from the terminal

`ankra chat` takes `--mode ask|agent` (omit it to use the server default):

- `--mode ask` — read-only, plus the curated safe creations (spin up a workspace pod to search an Application's repos, deploy a throwaway PR demo, create a brand-new stack). Never changes existing infrastructure and never opens a PR.
- `--mode agent` — can act; opening a pull request is automatic, other destructive changes stay confirmation-gated.

This is the same Ask/Agent contract the AI Gateway integrations use (see `ankra-ai-gateway`). Workspace pods and PR demos run on the org's staging cluster configured in **Organisation settings → AI → Environment**.

MCP token scopes mirror the modes: create tokens with `ankra tokens create --scope mcp:read` for the Ask surface (read + safe creations, e.g. for Cursor) or `--scope mcp:write` for the Agent surface (adds mutating tools, including opening fix PRs).

## Authentication for automation

For CI or scripts, skip `ankra login` and pass a token created with `ankra tokens create`:

```bash
export ANKRA_API_TOKEN=<token>     # never hardcode in the repo
ankra cluster info --cluster prod
```

Resolution order: explicit `--token`, then the saved login from `ankra login`, then `ANKRA_API_TOKEN`. A saved login token takes precedence over `ANKRA_API_TOKEN` - run `ankra logout` first if you want the env var to win. Use least-privilege tokens and store them in the CI secret store, not in Git.

## Conventions to follow

- Select the org and cluster first, or pass `--org` / `--cluster` explicitly. Never assume the active selection in a shared script.
- Treat mutating commands (`apply`, `reconcile`, `delete`, `provision`, `deprovision`) as deliberate; confirm the target cluster with `ankra cluster info` first.
- Prefer applying versioned YAML (`ankra cluster apply -f`) over ad-hoc edits so the change is reproducible and reviewable.
- Enable completions once per machine: `ankra completion install`.

## Related skills

- Authoring the YAML you apply: see `ankra-import-cluster` and `ankra-stacks-addons`.
- Encrypting secrets before commit: see `ankra-sops-secrets`.
