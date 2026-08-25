---
name: ankra-cli
description: Drive the Ankra CLI end to end - log in, switch organisation and cluster, apply cluster/stack YAML, manage applications and stack profiles, inspect Kubernetes resources, logs, events and metrics, triage operations, work the AI board, and script it all with `-o json` and stable exit codes. Use when the user mentions the `ankra` CLI, `ankra login`, `ankra cluster`, `ankra application`, `ankra stack-profiles`, `ankra tickets`, applying an ImportCluster, or managing an Ankra-managed cluster from the terminal.
---

# Ankra CLI

`ankra` is the terminal client for the Ankra Platform. Everything the portal does, it does — and
unlike the portal it is scriptable, which is why agent work should go through it.

## Install and verify

```bash
bash <(curl -sL https://github.com/ankraio/ankra-cli/releases/latest/download/install.sh)
ankra --version
ankra completion install            # once per machine
ankra upgrade                       # later, to update
ankra config beta enable            # opt in to pre-release builds (disable to leave)
```

Docs: https://docs.ankra.ai

## Orient before you act

```bash
ankra login                 # browser SSO; stores credentials in ~/.ankra.yaml
ankra org list
ankra org switch <slug|name|id>
ankra org current
ankra cluster list
ankra cluster select <name>         # persisted; omit the name for a picker
ankra cluster info                  # confirm what you are about to change
```

Most `ankra cluster ...` subcommands act on the **selected** cluster. Override per command with
`--cluster <name|id>`, and the organisation with the global `--org <name|id>`. In a shared script,
never rely on the selection — pass both explicitly.

## The command map

| Area | Entry point | Skill |
|------|-------------|-------|
| Clusters, stacks, addons, manifests | `ankra cluster ...` | `ankra-import-cluster`, `ankra-stacks-addons` |
| Your own code, deployed | `ankra application ...` | `ankra-applications` |
| Reusable parameterised stacks | `ankra stack-profiles ...` | `ankra-stack-profiles` |
| Cloud clusters Ankra provisions | `ankra cluster hetzner\|ovh\|upcloud\|digitalocean\|proxmox\|scaleway\|morpheus ...` | `ankra-cloud-clusters` |
| Provider-managed Kubernetes | `ankra cluster managed ...` | `ankra-managed-kubernetes` |
| Helm chart sources | `ankra helm registries\|credentials ...`, `ankra charts` | `ankra-helm-registries` |
| Secrets in Git | `ankra cluster encrypt\|decrypt\|sops-config` | `ankra-sops-secrets` |
| Alerts and routing | `ankra alerts destinations\|routes ...` | `ankra-alerts-webhooks` |
| Metrics and logs | `ankra cluster metrics\|top\|logs\|events` | `ankra-observability`, `ankra-troubleshooting` |
| AI provider, tools, runs, board | `ankra ai ...`, `ankra org mcp-servers ...`, `ankra agents ...`, `ankra tickets ...` | `ankra-ai-agents` |
| Tokens, roles, cluster access | `ankra tokens`, `ankra org members\|roles`, `ankra cluster access` | `ankra-security` |
| Credentials | `ankra credentials ...` | `ankra-security` |
| Support requests | `ankra support create\|list\|get\|comment\|attach\|close` | below |

## Applying configuration

```bash
ankra cluster validate -f cluster.yaml      # server-side validation, changes nothing
ankra cluster apply -f cluster.yaml --dry-run
ankra cluster apply -f cluster.yaml
ankra cluster draft -f cluster.yaml         # stage every stack as a reviewable draft instead
ankra cluster reconcile                     # ask Ankra to re-converge
ankra cluster clone existing.yaml new.yaml --stack monitoring
```

`ankra cluster draft` is the safe default on an environment you are not ready to change: the local
checks run first, then each stack is saved as a resource draft to review, edit and deploy from the
stack builder. **`apply` replaces a stack's contents declaratively** — what is not in the file is
removed from that stack.

## Reading a cluster

```bash
ankra cluster get pods -n <ns>              # also deployments, services, nodes, ingresses,
                                            # configmaps, secrets, statefulsets, daemonsets,
                                            # cronjobs, k8s-jobs, namespaces, storageclasses
ankra cluster get resources <kind> --group <api-group>
ankra cluster describe <kind> <name> -n <ns>
ankra cluster events --for pod/<name> -n <ns> --type Warning
ankra cluster logs <pod> -n <ns> --previous --follow=false
ankra cluster top pods -n <ns>
ankra cluster metrics query '<promql>'
```

`--follow` defaults to true on `logs`; pass `--follow=false` for anything scripted or it will hang.

## Triage

```bash
ankra cluster operations list
ankra cluster operations steps <execution-id>
ankra cluster operations retry <execution-id>
ankra cluster stacks history <stack>
ankra cluster agent status
```

A failed platform execution explains more deploy failures than pod logs do — start there. See
`ankra-troubleshooting`.

## AI from the terminal

```bash
ankra chat --mode ask "why is my ingress pod crashlooping?"
ankra chat --mode agent "open a PR fixing the failing lint job"
ankra chat health
ankra chat actions list <conversation-id>   # writes awaiting confirmation
ankra tickets list --needs-human
ankra agents runs --status running
```

- `--mode ask` — read-only, plus curated safe creations (a workspace pod to search an
  application's repositories, a throwaway PR demo, a brand-new stack). It never changes existing
  infrastructure and never opens a pull request.
- `--mode agent` — can act. Opening a pull request is automatic (the PR is the review gate); other
  destructive changes stay confirmation-gated via `ankra chat actions`.

Omit `--mode` to use the server default. MCP token scopes mirror the modes:
`ankra tokens create <name> --scopes mcp:read` for the Ask surface, `--scopes mcp:read,mcp:write`
for the Agent surface. See `ankra-ai-agents` and `ankra-ai-gateway`.

## Support requests

```bash
ankra support create --subject "Nodes NotReady" --description "..." --cluster prod
ankra support list
ankra support get <ticket-id>
ankra support comment <ticket-id> --message "Any update?"
ankra support attach <ticket-id> ./screenshot.png
ankra support close <ticket-id>
```

Every request is reviewed by Ankra AI before it reaches the team; `--force` on `create` submits a
flagged request anyway. Attach the evidence (operations output, logs) rather than describing it.

## Scripting

```bash
export ANKRA_API_TOKEN=<token>              # never hardcode in a repository
ankra cluster info --cluster prod -o json
ankra cluster logs -l app=web -n prod --follow=false -o json | jq '.[].message'
```

- **Structured output**: most read commands take `-o json|yaml`. stdout stays parseable; hints and
  errors go to stderr.
- **Token resolution order**: explicit `--token`, then the saved login from `ankra login`, then
  `ANKRA_API_TOKEN`. A saved login **beats** the environment variable — run `ankra logout` first if
  you want the variable to win.
- **Exit codes are a contract**: `0` success, `1` API/runtime, `2` usage, `3` not found,
  `4` confirmation declined, `5` `--wait`/`--timeout` expiry, `6` auth, `7` RBAC permission denied
  (the role lacks the permission; re-logging in will not help).
- **Destructive commands confirm first** (`delete`, `uninstall`, `deprovision`); declining exits 4.
  In automation, decide deliberately whether to pass the command's non-interactive flag.

## Conventions to follow

- **Confirm the target before mutating.** `ankra org current` and `ankra cluster info`, every time.
- **Prefer versioned YAML** (`ankra cluster apply -f`) over ad-hoc edits, so the change is
  reproducible and reviewable.
- **Validate, then draft, then apply.** Each step is cheap; a wrong apply is not.
- **Do not reach for `kubectl` to change things.** Read-only kubectl through
  `ankra cluster kubeconfig add --use` is fine; mutations belong in the GitOps repo or
  `ankra cluster apply`.
- **Never paste secrets** into a command line — use stdin, a prompt, `--set-file` or `--set-env`.

## Related skills

- Authoring the YAML you apply: `ankra-import-cluster`, `ankra-stacks-addons`.
- Deploying your own code: `ankra-applications`, `ankra-cicd`.
- Reusing a stack across clusters: `ankra-stack-profiles`.
- When it breaks: `ankra-troubleshooting`.
- Everything above, safely: `ankra-security`, `ankra-sops-secrets`, `ankra-platform-principles`.
