---
name: ankra-security
description: Secure an Ankra organisation end to end - API tokens and their MCP scopes, organisation roles and membership, cluster access grants through the kube gateway, credential scope for Git/registry/cloud, secret handling with SOPS, application code and container scanning findings, and how much autonomy the AI agents and MCP tool servers are given. Use when the user asks about permissions, RBAC, who can access a cluster, token scopes, least privilege, a security review, hardening, or handling secrets safely.
---

# Ankra security

Ankra has five distinct places where access is granted. Confusing them is how a "read-only" token
ends up able to open pull requests, and how a developer who needed to read logs ends up
`cluster-admin`.

| Layer | Grants | Managed with |
|-------|--------|--------------|
| **API tokens** | What a script or an MCP client can call | `ankra tokens` |
| **Organisation roles** | What a member can do in Ankra | `ankra org members`, `ankra org roles` |
| **Cluster access grants** | Kubernetes API access through the Ankra gateway | `ankra cluster access` |
| **Credentials** | What Ankra can reach in Git, registries and clouds | `ankra credentials`, `ankra helm credentials` |
| **Agent autonomy** | What the AI may do without a human | Ask/Agent mode, MCP tool grants |

## 1. API tokens and MCP scopes

```bash
ankra tokens list
ankra tokens create ci-payments --expires 2026-12-31
ankra tokens create cursor-readonly --scopes mcp:read
ankra tokens create agent-writer   --scopes mcp:read,mcp:write
ankra tokens revoke <token-id>
ankra tokens delete <token-id>          # only after it is revoked
```

- **Omitting `--scopes` creates a REST-only token** — it can drive the CLI and API but exposes no
  MCP tool surface. That is the right default for CI.
- **`mcp:read`** is the Ask surface: reads plus the curated safe creations (a workspace pod, a
  throwaway PR demo, a brand-new stack). Give this to an editor integration.
- **`mcp:read,mcp:write`** adds the mutating tools, including opening fix pull requests. Give it
  only where you want the agent to act.
- **Always set `--expires`.** A token with no expiry is a permanent credential nobody will
  remember to rotate.
- Revoke first, delete second — the two-step exists so an accidental delete cannot hide an active
  credential.

Resolution order in the CLI is `--token`, then the saved login from `ankra login`, then
`ANKRA_API_TOKEN`. A saved login **beats** the environment variable, so a script that seems to
ignore `ANKRA_API_TOKEN` usually needs `ankra logout` first.

Store tokens in the CI secret store or the environment — never in the repository, never in a
committed `.ankra.yaml`.

## 2. Organisation roles and membership

```bash
ankra org members
ankra org roles                          # the assignable roles
ankra org invite <email> --role viewer   # owner, admin, operator, member, viewer, read-only
ankra org remove <email>
ankra org current
```

Invite at the lowest role that works, and review membership when people change teams. Role is also
what gates MCP tool grants (below), so a wide role quietly widens agent capability too.

## 3. Cluster access through the kube gateway

```bash
ankra cluster access list --cluster prod
ankra cluster access grant alice@example.com --cluster prod --role view
ankra cluster access grant bob@example.com --cluster prod --role edit --namespace payments
ankra cluster access revoke alice@example.com --cluster prod
```

Roles map to the standard Kubernetes ClusterRoles: `view`, `edit`, `admin`, `cluster-admin`. Grants
are **cluster-wide by default** — pass `--namespace` to scope one.

Two rules that carry most of the value here:

- **`view` is enough for troubleshooting.** `ankra cluster logs --previous`, `describe`, `events`
  and `top` all work through the agent without a grant at all; a grant is only needed for direct
  `kubectl`. Reaching for `edit` to read a crash log is a common and unnecessary escalation.
- **`cluster-admin` needs a named reason and a review date.** Treat every existing one as a finding
  until someone justifies it.

```bash
ankra cluster kubeconfig add --use     # a kubeconfig context using ankra cluster kube-token
ankra cluster kubeconfig list
ankra cluster kubeconfig remove <context>
```

The kubeconfig credential plugin mints short-lived credentials per call, so there is no long-lived
kubeconfig to leak. Read-only kubectl through it is fine; mutations belong in the GitOps repo.

## 4. Credentials: scope, and what they can really reach

```bash
ankra credentials list
ankra credentials get <name>
ankra credentials repositories <name>    # what a Git credential can ACTUALLY reach
ankra credentials validate <name>
ankra helm credentials list
```

`credentials repositories` is the one to run in a review: a Git credential that was meant for one
repository frequently reaches an entire organisation, and nothing in its name says so.

- Scope Git credentials to the repositories Ankra needs, and no more.
- Registry credentials come in three flavours and should not be shared: a **push** credential for
  CI, a **read** credential for Ankra, and a **pull secret** for the cluster. Where the registry
  supports it, hand Ankra an **admin** credential so it mints a narrow per-application robot
  instead — see `ankra-app-integrations`.
- Cloud provider credentials should be scoped to the project/subscription Ankra manages.

## 5. Secrets

```bash
ankra cluster sops-config                          # the public key in use
ankra cluster encrypt manifest <name> --key password --key username
ankra cluster encrypt addon <name> --key apiKey
ankra cluster decrypt manifest <name>              # verify shape, never to copy values
ankra application env-secrets list <application-id>
```

- **Nothing sensitive is committed in plaintext.** Encrypt with SOPS and declare `encrypted_paths`
  so Ankra decrypts the right fields — an encrypted value with no declared path deploys as
  ciphertext.
- **Never print a decrypted value** into a terminal transcript, a pull request, a commit message,
  an issue, or chat. Report the key name and its state.
- **Rotate and re-encrypt when access changes**, and scope decryption to the clusters that need it.
- Opening a stack profile builder draft on a published profile **drops `encrypted_paths`** —
  re-declare them before publishing (see `ankra-stack-profiles`).

## 6. Supply chain

```bash
ankra application upgrade-workflow <application-id>    # add scanning to the build workflow
ankra application code-security <application-id>
ankra application container-security <application-id>
ankra application pull-request-reviews <application-id>
```

An application whose build workflow has no scanning step is a finding in itself. So is a floating
image tag or an unpinned chart version: a mutable tag means the artefact you reviewed is not
necessarily the artefact that runs, which defeats every other control here.

## 7. Agent autonomy

```bash
ankra org mcp-servers list
ankra org mcp-servers get <name>
ankra org mcp-servers tools <name>
ankra org mcp-servers grants <name>                    # per-tool role grants
ankra org mcp-servers grant <name> <tool> --role admin
ankra org mcp-servers revoke-grant <name> <tool> --role admin
ankra org mcp-servers disable <name>
ankra agents runs --status failed
```

- **Ask is the safe default and the gateway fails closed to it.** A binding with no explicit mode
  inherits the organisation default, which is `ask` until an admin changes it.
- **Agent mode auto-opens pull requests** — that is the design; the PR is the review gate. It never
  merges and never force-pushes.
- **MCP servers**: prefer `--tier read_only`, keep `--allowed-tools` to what is actually needed,
  restrict with `--cluster` where a server should only serve some clusters, and store credential
  headers with `--secret-header` (a secret slot) rather than `--header` (plaintext).
- Grant mutating tools to the narrowest role that needs them, not to `member`.

See `ankra-ai-agents` for the full surface and `ankra-ai-gateway` for the Ask/Agent contract.

## A review pass, in order

```bash
ankra tokens list                        # no-expiry tokens, over-scoped mcp:write
ankra org members ; ankra org roles      # who holds what
ankra cluster access list --cluster <c>  # cluster-admin, cluster-wide grants
ankra credentials list                   # then `credentials repositories` on each Git credential
ankra cluster sops-config                # secrets encrypted, paths declared
ankra application container-security <a> # per application; and code-security
ankra org mcp-servers list               # read_write tiers, wide tool grants
```

Report findings ranked by exposure: what is reachable, by whom, and the one command or change that
closes it. Do not change anything in a review pass without asking, and never print a secret value.

## Rules

- **Least privilege at every layer**, and the layers are independent — a narrow role does not
  narrow a token.
- **Expiry on every token.** Revoke, then delete.
- **`view` before `edit`, `edit` before `admin`**, namespace-scoped before cluster-wide.
- **Plaintext secrets never reach Git**, and decrypted values never reach a transcript.
- **Pin every artefact.** Unpinned images and charts make review meaningless.
- **Ask mode by default**; promote a binding to Agent deliberately, per binding.
- **Destructive commands confirm first** (`delete`, `uninstall`, `deprovision`, `revoke`) — check
  the organisation and cluster with `ankra org current` and `ankra cluster info` before running one.

## Related skills

- `ankra-sops-secrets` — the encryption workflow in detail.
- `ankra-app-integrations` — credential reuse and the registry credential split.
- `ankra-ai-agents` — MCP servers, tool grants, and agent runs.
- `ankra-ai-gateway` — Ask/Agent modes per integration.
- `ankra-platform-principles` — the least-privilege and confirm-destructive defaults.
