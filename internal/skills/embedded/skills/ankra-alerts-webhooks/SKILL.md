---
name: ankra-alerts-webhooks
description: Route Ankra's platform notifications to Slack, Microsoft Teams, Discord, PagerDuty or a custom webhook - creating destinations (webhook URLs or bot channels), routing rules that filter by kind, severity and cluster with include/exclude modes and priorities, previewing which destinations a notification would reach, and testing delivery before relying on it. Use when the user wants alerting, notifications, incident routing, on-call integration, webhook delivery from Ankra, or asks why a notification did or did not reach a channel.
---

# Ankra alerts and webhooks

Ankra emits **notifications** (a failed execution, a degraded resource, a firing alert trigger, a
GitOps sync failure, an offline agent, new severe CVEs). **Destinations** are where they can be
delivered; **routes** decide which notification reaches which destination. Both are managed with
`ankra alerts`.

```
notification (kind, severity, cluster) → routes (filters, priority) → destinations (Slack/Teams/PagerDuty/webhook)
```

## Destinations

Two shapes: a **webhook URL**, or a **bot channel** for Slack/Teams workspaces connected to the
organisation.

```bash
ankra alerts destinations create --name ops-slack \
  --url https://hooks.slack.com/services/...
ankra alerts destinations create --name oncall --type pagerduty \
  --url https://events.pagerduty.com/...
ankra alerts destinations channels                    # channel ids the Ankra bot can post to
ankra alerts destinations channels --provider teams
ankra alerts destinations create --name ops-teams --type teams \
  --channel-id 19:abc@thread.tacv2 --teams-tenant-id <tenant>

ankra alerts destinations list
ankra alerts destinations get <destination-id>
ankra alerts destinations update <destination-id> ...
ankra alerts destinations delete <destination-id>
```

- `--type` (`slack`, `teams`, `discord`, `pagerduty`, `custom`; default `slack`) records the
  receiver and selects the default payload format; `--template-file` overrides the payload with
  your own template.
- A Teams channel destination needs both `--channel-id` and `--teams-tenant-id`.
- In `channels`, a provider reading **"not connected"** means the workspace/tenant is not linked to
  the organisation, and **"not available"** means the bot service is not configured on the
  platform. Neither is an error — connect the integration first (`ankra-ai-gateway`).
- `--disabled` creates a destination without it receiving anything yet.

**Test before relying on it** — both forms exit non-zero on a failed delivery, so CI can gate on
them:

```bash
ankra alerts destinations test <destination-id>
ankra alerts destinations test-url --url https://example.com/hook --template-file payload.json
```

`test-url` sends to an ad-hoc URL *without* storing a destination — the right way to validate a
webhook before it goes anywhere near configuration.

## Routes

Every filter is optional; **a route with no filters matches every notification.**

```bash
ankra alerts routes create --destination-id <id> --severity critical
ankra alerts routes create --destination-id <id> --kinds execution_failed,gitops_sync_failed
ankra alerts routes create --destination-id <id> --cluster-id <cluster-id> --severity warning
ankra alerts routes create --destination-id <id> --kind agent_offline --mode exclude --stop-on-match

ankra alerts routes list
ankra alerts routes update <route-id> ...
ankra alerts routes delete <route-id>
```

Filters and mechanics:

- **Kinds** include `execution_failed`, `resource_deployment_failed`, `resource_health_degraded`,
  `alert_trigger_fired`, `gitops_sync_failed`, `agent_offline`, `security_new_severe_cves`, and the
  other platform notification kinds. `--kind` (one), `--kinds` (list), or `--exclude-kinds`
  (everything except these).
- **Severities**: `critical`, `warning`, `info`.
- **`--mode include`** (default) delivers matches; **`--mode exclude`** withholds them — the way to
  mute one noisy kind from a destination that otherwise gets everything.
- **`--priority`** orders evaluation (lowest first, default 100), and **`--stop-on-match`** stops
  lower-priority routes once this one matches. Together they express "critical goes to on-call and
  *only* on-call".
- `--source-id` scopes to one emitting source; `--cluster-id` to one cluster.

## Preview and test routing

`preview` is the answer to "where would this go?" — and to "why did that page fire?":

```bash
ankra alerts routes preview --kind alert_trigger_fired --severity critical
ankra alerts routes preview --kind alert_trigger_fired --severity critical --alert-id <alert-id>
ankra alerts routes preview --kind gitops_sync_failed --severity warning --cluster-id <id> -o json
ankra alerts routes test <route-id>          # queue a real sample through the route
```

Nothing is delivered by `preview` and nothing changes — it prints the destinations a hypothetical
notification would reach and the reason each matched. **Pass `--alert-id` whenever previewing an
alert firing**: an alert can carry its own destination list, a destination reached by both that
list and a routing rule is delivered to once, and without the id the preview cannot tell you which
rule that affects.

`routes test` queues a real sample; delivery is asynchronous, so it returns the delivery id once
queued, not once the receiver accepted it — `destinations test` is the synchronous check.

## A sane starting layout

```bash
ankra alerts destinations create --name oncall --type pagerduty --url https://events.pagerduty.com/...
ankra alerts destinations create --name ops-slack --url https://hooks.slack.com/services/...

ankra alerts routes create --destination-id <oncall> --severity critical --priority 10 --stop-on-match
ankra alerts routes create --destination-id <ops-slack> --severity warning
ankra alerts routes create --destination-id <ops-slack> --kinds gitops_sync_failed,agent_offline
```

Critical pages on-call and stops; warnings and platform events land in chat. Ankra attaches AI
incident analysis to alerts, so the summary lands where responders actually look.

## Rules

- **`test-url` before storing, `test` after storing, `preview` before trusting a route.**
- **Severity-route**: critical → on-call, warning → chat. Do not fan everything everywhere — noisy
  alerts get ignored, which is worse than no alerts.
- **A filterless route matches everything.** Make that a deliberate choice, not an accident.
- **Use `--mode exclude` to mute a kind**, not deletion of the broad route.
- **Webhook URLs and integration keys are secrets.** Pass them to the CLI, never commit them to
  Git; a custom endpoint must be HTTPS and validate the payload.
- **`--alert-id` on previews of alert firings**, or the dedup against the alert's own destinations
  is invisible.

## Related skills

- `ankra-observability` — the Prometheus/Grafana stack producing the underlying signals.
- `ankra-troubleshooting` — what to do with the notification once it lands.
- `ankra-ai-gateway` — connecting the Slack workspace / Teams tenant the bot posts into.
- `ankra-security` — scoping who can manage destinations and routes.
