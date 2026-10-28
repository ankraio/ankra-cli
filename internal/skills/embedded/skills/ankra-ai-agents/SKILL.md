---
name: ankra-ai-agents
description: Run and govern Ankra's AI agents - choosing the model provider and catalogue (Ankra, Anthropic, OpenRouter, or an OpenAI-compatible endpoint such as LiteLLM or vLLM), choosing the model per AI function (lanes), registering MCP tool servers and granting individual tools to organisation roles, the AI kill switch and pause, the auto-remediation policy, daily AI spend caps, the board identity, dispatching and supervising agent runs and their transcripts, working the AI board of tickets, and confirming or rejecting the writes an agent proposes. Use when the user mentions Ankra AI, AI agents, sub-agents, agent runs, MCP servers or tools, the AI board or tickets, the model catalogue, or configuring which model Ankra uses.
---

# Ankra AI agents

## Recommended path

- Golden paths 4 (day 2) and 5 (agent-driven work); see `ankra-platform-principles`. Agents
  investigate and propose; a person approves writes, merges pull requests and promotes to
  production.
- Before widening anything, read the current envelope: `ankra ai status`, `ankra ai autonomy
  status`, `ankra ai remediation policy`, `ankra org mcp-servers list`.
- Keep agents that can reach production at **Propose**, MCP servers `read_only` with a trimmed
  allow-list, and caps set (`ankra org ai-spend-cap`). Ask/Agent per integration is
  `ankra-ai-gateway`; token scopes and roles are `ankra-security`.
- Anti-patterns: approving a write without reading the transcript, a per-tool `auto` override
  nobody reviewed, and a secret pasted into a ticket or chat.

Ankra runs AI agents against your organisation: they investigate incidents, answer questions about
clusters, propose changes, and open pull requests. This skill is the **operator's** view — which
model they use, which tools they may call, what they are allowed to do without asking, and how to
supervise a run in flight.

Four surfaces, and they are independent:

| Surface | Question it answers | Commands |
|---------|---------------------|----------|
| **Provider & catalogue** | Which model, whose key | `ankra ai ...` |
| **Tools (MCP)** | What the agent can call | `ankra org mcp-servers ...` |
| **Runs** | What an agent is doing right now | `ankra agents ...` |
| **Board** | What the agents are working on, and what needs a human | `ankra tickets ...` |

Safety mode (Ask vs Agent) is the fifth, and it lives in `ankra-ai-gateway`.

## 1. Provider and model catalogue

```bash
ankra ai status                       # active provider and which credentials are configured
ankra ai provider ankra               # Ankra-managed Claude models (the default)
ankra ai provider anthropic
ankra ai provider openrouter
ankra ai provider openai_compatible
```

Set the credential **before** switching — switching is blocked without a valid one:

```bash
ankra ai anthropic set                # your own Anthropic key (sk-ant-...); `anthropic delete` removes it
ankra ai openrouter set               # `openrouter remove` removes it
ankra ai endpoints create --name litellm --base-url https://llm.example.com/v1 --api-key <key>
ankra ai endpoints list               # prints each endpoint's id
ankra ai endpoints discover <endpoint-id>   # the model ids the endpoint advertises
ankra ai endpoints update <endpoint-id> --base-url ...   # --api-key rotates the key
ankra ai endpoints delete <endpoint-id>     # also removes its catalogue models
```

`endpoints` is the multi-endpoint surface (LiteLLM, vLLM, TGI, Ollama, OpenRouter, Together — any
OpenAI v1 API); `ankra ai openai` is the older single-endpoint form. Ankra probes
`GET <base-url>/models` on save, so a bad URL fails immediately — but a **wrong model id only
surfaces at the first AI call**, which is what `endpoints discover` is for.

The catalogue is what the chat model picker offers:

```bash
ankra ai models list
ankra ai models create --key local-fast --name "Local fast" --model-id <id-from-discover> \
  --endpoint <endpoint-id> --tier quick
ankra ai models update <id|key> ...
ankra ai models delete <id|key>
ankra ai models reset                 # back to the built-in defaults

ankra ai lanes list                   # the model each non-chat function runs on
ankra ai lanes set pr_review think    # a catalogue key, or an exact OpenRouter model id
ankra ai lanes clear pr_review        # back to the lane's default tier
```

A lane is one AI function outside chat: deploy analysis, troubleshooting, CI/CD generation, stack
README generation and AI code review. A lane with no selection follows its default tier, so it
moves when the catalogue does.

In OpenAI-compatible mode, tool-using paths (the agentic assistant, AI insights, CI/CD file
generation, deploy analysis) fall back to the Ankra platform provider; stack descriptions and
summaries run natively against your endpoint. The **What runs where** matrix on the settings page
is authoritative — check it before promising an air-gapped setup.

## 2. Tools: MCP servers

An agent can only call tools it has been given. MCP servers are how you extend that set beyond
Ankra's own — a ticket tracker, an error tracker, an internal API.

```bash
ankra org mcp-servers catalog                 # curated adapters, their headers and tool lists
ankra org mcp-servers add sentry --adapter sentry \
  --url https://mcp.sentry.dev/mcp --secret-header Authorization
ankra org mcp-servers add internal-tools --url https://mcp.example.internal/sse \
  --transport sse --tier read_write --allowed-tools search_docs,create_ticket
ankra org mcp-servers add staging-only --url https://mcp.example.com/mcp \
  --cluster <cluster-uuid> --disabled
ankra org mcp-servers list
ankra org mcp-servers get <name>
ankra org mcp-servers tools <name>            # what it actually exposes
ankra org mcp-servers health <name>           # is it reachable
ankra org mcp-servers update <name> ...
ankra org mcp-servers enable <name> / disable <name> / remove <name>
```

Credentials, and why the flag choice matters:

- **`--secret-header Authorization`** (name only) prompts with hidden input, or reads one header
  per line from piped stdin, and stores the value in an organisation secret slot. The server record
  keeps only a `${SECRET_SLOT:<id>}` sentinel, so the secret never lands in it.
- **`--secret-header Key=Value`** works but leaves the secret in shell history and in process
  listings. Avoid it.
- **`--header`** is plaintext, for non-secret values only; the backend refuses plaintext under
  sensitive-looking names.
- If a later registration step fails, slots already created are removed, so no secret material is
  orphaned.
- `--url` is required for **every** transport. With `--transport stdio` it is stored as the
  server's identifier and never dialled — use a placeholder such as `cmd://<binary-name>`.
- With `--adapter` and no `--allowed-tools`, the allow-list is seeded from the adapter's
  recommendation. Trim it: an allow-list is a capability grant.

### Granting tools to roles

```bash
ankra org mcp-servers grants <name>                     # current per-tool role grants
ankra org mcp-servers grant sentry get_issue            # defaults to the member role
ankra org mcp-servers grant sentry create_ticket --role admin
ankra org mcp-servers revoke-grant sentry create_ticket --role admin
```

A tool is callable from agent runs started by members holding the granted role. Grants are
additive. Read tools can sit at `member`; **anything that writes belongs to the narrowest role that
needs it**, not to `member`.

Two-layer defence: `--tier read_only` on the server, and a tight `--allowed-tools`. Use `--cluster`
(repeatable) when a server should only serve some clusters' runs.

## 3. Supervising runs

Every dispatch — a board ticket, a schedule, an alert, or run-now — records a run with a linked
session.

```bash
ankra agents runs                                  # newest first
ankra agents runs --status running --status awaiting_user
ankra agents runs --status failed --limit 50
ankra agents runs --task <agent-task-uuid>
ankra agents run <run-id>                          # one run in full
ankra agents transcript <run-id> --limit 500       # the session transcript
ankra agents transcript <run-id> --since <sequence-number>
ankra agents cancel <run-id>                       # interrupts the in-flight turn in seconds
```

Statuses: `pending`, `running`, `awaiting_user`, `completed`, `failed`, `cancelled`, `expired`,
`skipped`. `awaiting_user` means it is blocked on a person — that is the queue to watch.

`agents transcript` is the audit trail: it shows what the agent read, which tools it called, and
what it concluded. Read it before accepting a conclusion, and always before approving a write.

### Autonomy, remediation and cost

```bash
ankra ai remediation policy                   # autonomy level, per-tool overrides, cluster scope (read-only)
ankra ai autonomy status
ankra ai autonomy pause --reason "..."        # nothing changes without approval; chat and diagnosis keep working
ankra ai autonomy resume                      # restores exactly what the pause switched off
ankra ai autonomy stop-all --reason "..."     # incident stop: cancels every AI session and run, blocks chat
ankra ai autonomy start-all
ankra org ai-spend-cap show
ankra org ai-spend-cap set --daily-usd 25 --token-soft-cap 2000000 --token-hard-cap 5000000
```

- Autonomy is opt-in: a new organisation has auto-remediation off and no active agents. When an
  admin enables the policy (in the portal, **AI** → **Settings** → **Autonomy**), its levels are
  **Observe only**, **Propose** (the default: every action goes for approval) and **Autonomous**
  (low-risk reversible actions run, the rest ask). Per-agent autonomy uses the same three levels.
- In `remediation policy`, a per-tool `auto` executes with no approval card, and a cluster allow
  list that was never set covers every cluster. Read both before trusting the policy.
- Prefer `pause` to tighten; `stop-all` is for incidents.
- Spend caps: the daily USD cap bounds chat; past the token soft cap unattended runs degrade to the
  quick tier, past the hard cap new runs are refused until midnight UTC.

## 4. The AI board

Tickets are how agents track incidents, insights and requests, and how they ask a human for a
decision.

```bash
ankra tickets list
ankra tickets list --needs-human                   # only tickets waiting on a person
ankra tickets list --status blocked --status awaiting_approval
ankra tickets list --search ingress --include-closed --limit 100
ankra tickets get T-8                              # includes the decision it is waiting on
ankra tickets events T-8                           # the timeline, oldest first
ankra tickets comment T-8 --body "Checked the ingress; it is the cert, not the route."
ankra tickets transition T-8 --status investigating --note "Reassigned to the platform lane"
```

A ticket is referenced by number (`8`, `T-8`, `#8`) or UUID. Statuses: `triage`, `investigating`,
`planning`, `awaiting_review`, `awaiting_approval`, `executing`, `verifying`, `blocked`, `done`,
`cancelled`.

### Answering a decision

A `blocked` ticket is waiting on a choice the agent offered:

```bash
ankra tickets get T-8                              # read the offered options and their keys
ankra tickets decide T-8 --option rollback
ankra tickets decide T-8 --answer "Neither - drain the node first, then retry."
ankra tickets decide T-8 --option rollback --answer "Roll back, then open a follow-up ticket."
```

`--option` picks one of the offered keys; `--answer` is the "something else" path in your own
words; both together record the option with your note beside it. The answer lands on the timeline
as a `Decision:` comment and the agent resumes from it. `decide` only applies to a ticket that is
actually `blocked` on a decision — otherwise use `tickets comment` to talk to the agent.

**Commenting on a ticket re-admits it** and re-dispatches the agent, but only once any running turn
has terminated. That is the supported way to steer an agent that is already working.

Board tickets are worked only by an agent an admin designates (**AI** → **Settings** →
**Autonomy**); until then every ticket escalates to a person. Platform-created agents first need
the board identity, a principal with its own role that cannot sign in and has no token:

```bash
ankra ai board-identity status
ankra ai board-identity provision --role operator   # operator (default), member or viewer
ankra ai board-identity revoke                      # agents escalate to a person again
```

## 5. Chat, and approving what it proposes

```bash
ankra chat --mode ask "why is the payments pod crashlooping in prod?"
ankra chat --mode agent "open a PR fixing the failing lint job"
ankra chat health                                  # AI-analysed cluster health
ankra chat history ; ankra chat show <conversation-id> ; ankra chat delete <id>
ankra ai memories list ; ankra ai memories clear   # what the assistant remembers about you

ankra chat actions list <conversation-id>          # writes awaiting confirmation
ankra chat actions confirm <action-id>
ankra chat actions reject <action-id>
```

Agent-mode chat **halts before every write** and proposes it as a pending action. Pending actions
expire, so confirm promptly. If the cluster changed since the action was proposed, confirming
answers a drift conflict — `--force` applies it against the changed state anyway, which you should
only do after re-reading what changed.

Opening a pull request is the exception: in Agent mode it happens automatically, because the PR is
itself the review gate. Nothing is merged and nothing is force-pushed.

## 6. Where ephemeral AI work runs

Workspace pods and PR demos run on one **staging cluster** nominated per organisation (portal:
**AI** → **Settings** → **Workspaces**, with the workspace and demo lifetimes and the default
gateway mode). Without it, the safe-creation tools answer "configure a staging cluster first".
`ankra org ai-environment get|set` covers where demos are published; see `ankra-ai-gateway`.

Per-application AI lanes (review, mentions, CI/CD investigation, demo URL) for the application's
repository:

```bash
ankra application ai-config get <application-id> -o json > lanes.json
ankra application ai-config set <application-id> --file lanes.json
ankra application ai-config clear <application-id>   # follow the organisation defaults again
```

## Rules

- **Set the credential, then switch the provider.** A switch with no valid credential is refused.
- **`endpoints discover` before trusting a model id.** A wrong id fails at the first real call.
- **Secret headers go in secret slots** (`--secret-header Key` alone), never inline, never
  `--header`.
- **`read_only` tier and a trimmed allow-list by default.** Widen deliberately, per tool, per role.
- **Read the transcript before approving a write.** `agents transcript` is the evidence.
- **Watch `awaiting_user` and `--needs-human`.** An unanswered decision is a stalled agent.
- **Cancel rather than let a wrong run finish** — `agents cancel` interrupts within seconds;
  `ai autonomy pause` or `stop-all` when it is more than one run.
- **Production stays a human decision.** Agents that can reach production stay at Propose.
- **Never paste a provider API key or a secret header value** into a ticket, a transcript, a PR or
  chat.

## Related skills

- `ankra-ai-gateway` — Ask/Agent modes per Slack/Teams/SCM binding, workspaces and PR demos.
- `ankra-security` — token scopes, roles, and how tool grants widen with role.
- `ankra-app-integrations` — pointing an application (rather than Ankra) at an LLM gateway.
- `ankra-troubleshooting` — verifying what an agent claims it found.
- `ankra-alerts-webhooks` — routing agent findings to Slack, Teams or PagerDuty.
