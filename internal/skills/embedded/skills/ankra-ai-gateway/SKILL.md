---
name: ankra-ai-gateway
description: Configure Ankra's AI Gateway across Slack, Microsoft Teams, GitHub, GitLab, and Bitbucket - per-connection Ask/Agent safety modes, the AI code review on every pull request (per-repository rules, related repositories, review model, `@ankraai` mentions), the org staging cluster for ephemeral AI workspace pods and PR demos and where demos are published, and the failed-build investigation and fix-pull-request flow. Use when the user connects an AI integration, mentions Ask vs Agent mode, AI code review or `ankra org ai-review`, AI workspaces, PR demos or previews, a staging cluster for AI, or wants the AI to investigate a failing build and open a fix PR.
---

# Ankra AI Gateway

## Recommended path

- Golden paths 4 (day 2: AI review of every pull request, failed-build investigation with fix
  pull requests) and 5 (agent-driven work); see `ankra-platform-principles`.
- Connect the provider, turn the review on deliberately (`ankra org ai-review repos set ...
  --review --mentions`), keep every connection in **Ask**, and promote one connection to **Agent**
  only where you want fix pull requests opened for you.
- A pull request is the review gate: Agent opens one, a person merges it. The pipeline the fix runs
  through is `ankra-cicd`; agent governance and model choice are `ankra-ai-agents`.
- Anti-patterns: setting the organisation default to Agent "to save clicks", and expecting an
  automatic review to change code.

The Ankra AI Gateway lets Ankra AI answer and act from **Slack, Microsoft Teams, GitHub, GitLab,
and Bitbucket Cloud**. Connections are made under **AI** → **Settings** → **Connections**, and
connecting one is an organisation admin action.

## Safety modes

Every connection carries an **AI mode**, separate from its capability switches: the switches decide
*whether* the AI answers, the mode decides *how far* the answer may go.

| Mode | What the AI can do |
|------|--------------------|
| **Ask** (default) | Investigate and explain, plus a curated set of **safe creations** that cannot damage existing infrastructure: an ephemeral **workspace pod** to clone and search an application's repositories, a **throwaway PR demo** on the staging cluster, and a **brand-new stack**. It never modifies existing infrastructure and never opens a pull request. |
| **Agent** (marked **Can act**) | Everything Ask allows, plus acting. It opens fix pull requests on its own - the pull request is the review gate. Other destructive changes still ask for confirmation. |

Ask is the default and the gateway **fails closed** to it: a connection with no explicit mode
inherits the organisation default, which is `ask` until an admin changes it. Mode is enforced at
the tool offer, the dispatch guard and the executor, so a stale offer or a prompt-injected
instruction cannot escalate. Automatic review events stay read-only whatever the mode.

On GitLab and Bitbucket Cloud, Ankra cannot open the pull request itself, so an Agent-mode reply
proposes the fix with a branch name and commit message instead.

`ankra chat --mode ask|agent` picks the same modes for a CLI conversation, and MCP token scopes
mirror them (`mcp:read` is the Ask surface, `mcp:write` the Agent surface; see `ankra-security`).

## AI code review on every pull request

**Nothing runs until it is switched on.** A new source-control connection has every capability
off: no review, no mention replies, no previews. Turn them on per connection (the default row) in
the portal, or per repository from the CLI:

```bash
ankra org ai-review repos list                         # connections, their defaults and repository rules
ankra org ai-review repos list --reachable             # also what each GitHub App installation can see
ankra org ai-review repos set my-org/api --review --mentions --max-reviews-per-pr 5
ankra org ai-review repos set my-org/api --review-drafts=false --model think
ankra org ai-review repos set my-org/web --previews     # PR preview environments (GitHub only)
ankra org ai-review repos unset my-org/api             # follow the connection defaults again

ankra org ai-review related-repos add my-org/web my-org/api   # reviews of either read the other
ankra org ai-review related-repos list
ankra ai lanes set pr_review think                     # organisation-wide review model
ankra application pull-request-reviews <application-id>  # what the review posted
```

- `set` changes only the switches you pass (`--review`, `--mentions`, `--previews`, `--model`,
  `--review-drafts`, `--max-reviews-per-pr`; `=false` turns one off); pass
  `--binding <provider>/<id>` when the connection is ambiguous. `set` and `unset` need organisation
  admin.
- Related repositories belong to one GitHub App installation and both must be reachable by it;
  pick it with `--credential <github-credential>` when the organisation has more than one.
  `related-repos remove` takes the pair or the relationship id. Applications on the same cluster
  are related with no configuration.
- The review posts **one comment per pull request**, updated in place, with a verdict (`looks
  good`, `no findings`, `comments`, `changes requested`) and at most eight findings pinned to their
  lines. It also reads callers in the same repository and related repositories for code the change
  can break.
- **Talk to it with `@ankraai`** in a pull request comment (needs **Mention replies**). A mention
  always runs, even on a draft or past the per-pull-request cap.
- A skipped review says why on the **Ankra AI review** check run (switched off, draft, out-of-scope
  branch, budget reached). Two budgets apply: **Auto reviews / PR** (default 5) on the repository
  rule, and **Max runs per day** under the application's AI settings.
- Per-application lanes (branch patterns, ignore paths, budgets, instructions) are
  `ankra application ai-config`, keyed by repository.

## Staging cluster, workspaces and PR demos

Ephemeral AI work runs on one **staging cluster** the organisation nominates (portal: **AI** →
**Settings** → **Workspaces**), with a workspace lifetime (default 8h), a demo lifetime (default
24h) and the default gateway mode. It must belong to the organisation and have an online agent.
Without it, the safe-creation tools answer "configure a staging cluster first". Expired workspaces
and demos are reaped automatically, and the portal lists what is running so you can stop it early.

- **Workspace pod** - a locked-down sandbox (non-root, read-only root filesystem, time-limited)
  that shallow-clones an application's repository to search and read it.
- **PR demo** - a throwaway copy of a pull request's build in its own namespace on the staging
  cluster, with a public https URL for reviewers.

Where demos are published:

```bash
ankra org ai-environment get
ankra org ai-environment set --demo-base-domain previews.example.com   # a wildcard zone you point at the staging cluster
ankra org ai-environment set --demo-cert-issuer letsencrypt-prod       # only alongside a base domain
ankra org ai-environment set --demo-base-domain ""                     # back to the staging cluster's Ankra subzone
```

The preview domain is not the organisation's root domain (`ankra org domain`). A write that leaves
previews on plain http says so. In Terraform, `ankra_ai_environment` manages the staging cluster,
lifetimes, default gateway mode and preview domain together.

## Failed builds and fix pull requests

On Ankra Pipelines, a failed build on an application's tracked branch dispatches an agent. The
application's **CI/CD** AI lane decides how far it goes:

| Lane | What happens |
|------|--------------|
| **Ask** (default) | It investigates and proposes a fix, without committing |
| **Agent** | It commits the fix on a new branch and opens a pull request for you to review |

A failed run on someone's own pull request is never committed to: the pull request gets the
explanation and a **Fix with Ankra** link on the run page instead.

From an **Agent**-mode Slack workspace or Teams tenant, *"fix the failing build in
`owner/repo`"* works the same way: Ankra investigates, opens the pull request, and replies in the
thread with the link. It never pushes to the default branch, never force-pushes and never merges.
From an Ask-mode connection it stops at the proposed patch and says to switch to Agent.

## Rules

- **Start in Ask.** Promote one connection to Agent deliberately; leave the organisation default
  at `ask`.
- **Switch the review on per connection or repository**; nothing runs by default.
- **Set the staging cluster before using workspaces or PR demos**, and publish demos on a public
  https preview domain, never a port-forward.
- **A pull request is the review gate.** Agent opens pull requests without a prompt on purpose; a
  person reviews and merges them.

## Related skills

- `ankra-ship` - where the AI review, run watch and fix pull requests sit in the shipping path.
- `ankra-cicd` - Ankra Pipelines, which the fix pull requests run through.
- `ankra-ai-agents` - models, lanes, autonomy, the board and agent runs.
- `ankra-security` - token scopes, roles and the AI review of who can do what.
- `ankra-domains-dns` - the preview domain and DNS for demos.
