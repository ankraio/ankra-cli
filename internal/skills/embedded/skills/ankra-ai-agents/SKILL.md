---
name: ankra-ai-agents
description: Run and govern Ankra's AI agents - choosing the model provider and catalogue (Ankra, Anthropic, OpenRouter, or an OpenAI-compatible endpoint such as LiteLLM or vLLM), registering MCP tool servers and granting individual tools to organisation roles, dispatching and supervising agent runs and their transcripts, working the AI board of tickets, and confirming or rejecting the writes an agent proposes. Use when the user mentions Ankra AI, AI agents, sub-agents, agent runs, MCP servers or tools, the AI board or tickets, the model catalogue, or configuring which model Ankra uses.
---

# Ankra AI agents

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
ankra ai anthropic set                # your own Anthropic key (sk-ant-...)
ankra ai openrouter set
ankra ai endpoints create --name litellm --base-url https://llm.example.com/v1 --api-key <key>
ankra ai endpoints discover litellm   # the model ids the endpoint advertises
ankra ai endpoints list
ankra ai endpoints update litellm --base-url ...
ankra ai endpoints delete litellm
```

`endpoints` is the multi-endpoint surface (LiteLLM, vLLM, TGI, Ollama, OpenRouter, Together — any
OpenAI v1 API); `ankra ai openai` is the older single-endpoint form. Ankra probes
`GET <base-url>/models` on save, so a bad URL fails immediately — but a **wrong model id only
surfaces at the first AI call**, which is what `endpoints discover` is for.

The catalogue is what the chat model picker offers:

```bash
ankra ai models list
ankra ai models create --name <model-id> ...
ankra ai models update <model-id> ...
ankra ai models delete <model-id>
ankra ai models reset                 # back to the built-in defaults
```

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

## 5. Chat, and approving what it proposes

```bash
ankra chat --mode ask "why is the payments pod crashlooping in prod?"
ankra chat --mode agent "open a PR fixing the failing lint job"
ankra chat health                                  # AI-analysed cluster health
ankra chat history ; ankra chat show <conversation-id> ; ankra chat delete <id>

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

Workspace pods and PR demos run on one **staging cluster** nominated per organisation, configured
in **Organisation settings → AI → Environment** along with the workspace and demo TTLs and the
default gateway mode. Without it, the safe-creation tools answer "configure a staging cluster
first". `ankra org ai-environment` shows and changes where PR demos are published; see
`ankra-ai-gateway`.

Per-application AI behaviour:

```bash
ankra application ai-config <application-id>       # read, set, or reset the app's AI lane config
```

## Rules

- **Set the credential, then switch the provider.** A switch with no valid credential is refused.
- **`endpoints discover` before trusting a model id.** A wrong id fails at the first real call.
- **Secret headers go in secret slots** (`--secret-header Key` alone), never inline, never
  `--header`.
- **`read_only` tier and a trimmed allow-list by default.** Widen deliberately, per tool, per role.
- **Read the transcript before approving a write.** `agents transcript` is the evidence.
- **Watch `awaiting_user` and `--needs-human`.** An unanswered decision is a stalled agent.
- **Cancel rather than let a wrong run finish** — `agents cancel` interrupts within seconds.
- **Never paste a provider API key or a secret header value** into a ticket, a transcript, a PR or
  chat.

## Related skills

- `ankra-ai-gateway` — Ask/Agent modes per Slack/Teams/SCM binding, workspaces and PR demos.
- `ankra-security` — token scopes, roles, and how tool grants widen with role.
- `ankra-app-integrations` — pointing an application (rather than Ankra) at an LLM gateway.
- `ankra-troubleshooting` — verifying what an agent claims it found.
- `ankra-alerts-webhooks` — routing agent findings to Slack, Teams or PagerDuty.
