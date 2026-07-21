---
name: ankra-ai-gateway
description: Configure Ankra's AI Gateway across Slack, Microsoft Teams, GitHub, GitLab, and Bitbucket, set per-binding Ask/Agent safety modes, pick the org staging cluster for ephemeral AI workspace pods and PR demos, and run the pipeline-failure investigation and auto-fix-PR flow. Use when the user connects an AI integration, mentions Ask vs Agent mode, AI workspaces, PR demos, a staging cluster for AI, or wants the AI to investigate a failing pipeline and open a fix PR.
---

# Ankra AI Gateway

The Ankra AI Gateway lets Ankra AI answer and act from your chat and source-control tools. It supports **Slack, Microsoft Teams, GitHub, GitLab, and Bitbucket**. Every integration runs under a **safety mode** that decides whether the AI can only read and explain, or can also change things.

## Safety modes (the core concept)

Two modes, set per integration (and an org-wide default):

| Mode | What the AI can do |
|------|--------------------|
| **Ask** (read-only) | Investigate and explain. Plus a curated set of **safe creations** that cannot damage existing infrastructure: spin up an ephemeral **workspace pod** to clone and search an Application's repos, deploy a **throwaway PR demo** to the staging cluster, and create a **brand-new stack**. It cannot modify, apply to, or delete anything that already exists, and it never opens a pull request. |
| **Agent** | Everything Ask allows, plus mutating actions. **Opening a pull request is automatic** (the PR is itself the human review gate). Ask it to fix a bug from Slack, Teams, or the portal and it fixes it and opens the PR for you, replying with the link. Commits to existing branches and other destructive changes still require confirmation. |

Ask is the safe default and the gateway **fails closed** to it: a binding with no explicit mode inherits the org default, and the org default is itself `ask` until an admin changes it. A Slack workspace or Teams tenant must be **explicitly set to Agent** (or the org default flipped to Agent) before it will auto-open pull requests — an unconfigured, misrouted, or lookup-failing binding stays read-only.

Mode is enforced in three places, so a stale offer or a prompt-injected instruction cannot escalate: the tool **offer filter**, the tool **dispatch guard**, and the tool **executor**. An automatic webhook review (e.g. an AI review on every PR) always stays read-only regardless of mode; only an explicit `@ankra` mention in an Agent-mode binding can propose a concrete, ready-to-apply fix.

## Configure a binding's mode

In the portal: **Organisation settings → Integrations**. Each Slack workspace, Teams tenant, and SCM (GitHub/GitLab/Bitbucket) binding has an **AI mode** selector (Ask / Agent). Unset bindings inherit the org default (`default_gateway_mode`, itself `ask` out of the box), so a workspace only gains Agent autonomy once an admin sets it explicitly.

Over the API:

```
GET  /api/v1/org/ai-gateway/bindings                                  # list bindings + effective mode
PUT  /api/v1/org/ai-gateway/bindings/{provider}/{binding_external_id} # {"mode":"ask"|"agent"}
```

## Staging cluster for AI workloads

Ephemeral AI work — workspace pods and PR demos — runs on one **staging cluster** you nominate per organisation. Configure it in **Organisation settings → AI → Environment**:

- **Staging cluster** — must belong to the org and have an online agent. Used for all small throwaway AI workloads.
- **Workspace TTL** — how long a workspace pod lives (default 8h).
- **Demo TTL** — how long a PR demo lives (default 24h).
- **Default gateway mode** — Ask or Agent for bindings that don't override it.

A reaper tears down expired workspaces and demos automatically. The **Active AI workspaces & demos** panel lists what is running and lets you terminate early.

```
GET    /api/v1/org/ai-environment                 # settings
PUT    /api/v1/org/ai-environment                 # staging cluster, TTLs, default mode
GET    /api/v1/org/ai-workspaces                  # active workspaces + PR demos
DELETE /api/v1/org/ai-workspaces/{workspace_id}   # terminate now
```

## Workspace pods and PR demos (Ask-allowed)

- **Workspace pod** — ask Ankra to search an Application's code and it spins up a locked-down sandbox on the staging cluster, shallow-clones the linked repo, and searches/reads files. Non-root, read-only root filesystem, time-limited.
- **PR demo** — deploy a throwaway copy of an Application's PR build to a TTL namespace (`ankra-demo-pr-<n>`) on the staging cluster, so reviewers can click through the change. Stopped with `demo_stop` or reaped at TTL.

These are available in Ask mode because they only create isolated, ephemeral state.

## Pipeline-failure investigation and auto-fix PRs

When a pipeline fails, Ankra can:

1. **Investigate** — list pipeline runs and read the failing job logs (GitHub Actions today; GitLab/Bitbucket via minted short-lived tokens).
2. **Reproduce** — clone the repo into a workspace pod and inspect the offending files.
3. **Propose a fix** — explain the root cause and show the exact file changes.
4. **Open the PR** — in **Agent** mode this happens automatically (`scm_create_pull_request`); in **Ask** mode Ankra stops at the proposed patch and tells you to switch to Agent to open it.

### From Slack or Teams

In an **Agent**-mode Slack workspace or Teams tenant, message Ankra something like *"fix the failing build in `owner/repo`"* (or "fix this bug…"). Ankra investigates, commits the fix to a new branch, opens the pull request, and replies **in the thread with the PR link** — you never leave chat. It commits to a new branch and opens a PR; it never pushes to your default branch and never merges.

On an SCM PR itself, `@ankra`-mention a failing pull request from an Agent-mode binding and Ankra replies with a concrete fix and opens it as a pull request the same way.

## Rules

- **Start in Ask.** Only promote a binding to Agent when you want it to act; Agent auto-opens PRs.
- **Set the staging cluster before using workspaces or PR demos.** Without it, the safe-creation tools answer "configure a staging cluster first".
- **Auto reviews stay read-only.** Agent mode changes only what a human explicitly mentions the agent to do; it never mutates on an automatic webhook review.
- **A pull request is the review gate.** Agent opens PRs without a confirmation prompt on purpose, but never merges and never force-pushes.

## Related skills

- `ankra-cli` — `ankra chat --mode ask|agent`, and MCP token scopes (`mcp:read` = Ask surface, `mcp:write` = Agent surface).
- `ankra-cicd` — the GitOps deploy pattern the fix PRs plug into.
- `ankra-stacks-addons` / `ankra-import-cluster` — the Applications and stacks the AI reasons about.
