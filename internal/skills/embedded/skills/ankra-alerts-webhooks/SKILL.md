---
name: ankra-alerts-webhooks
description: Configure alerting and notifications for Kubernetes clusters through Ankra - alert rules and outbound webhooks that notify Slack, Microsoft Teams, Discord, PagerDuty, Opsgenie, Datadog, or custom endpoints, with automatic AI incident analysis attached to alerts. Use when the user wants alerting, notifications, incident routing, or on-call integration for a Kubernetes cluster in an Ankra environment.
---

# Ankra Alerts & Webhooks

Ankra raises alerts on cluster conditions and delivers them to external systems via webhooks. Alerts can carry AI incident analysis so responders get context, not just a firing signal.

## Webhook targets

- **ChatOps:** Slack, Microsoft Teams, Discord.
- **On-call / incident:** PagerDuty, Opsgenie.
- **Observability:** Datadog.
- **Custom:** any HTTPS endpoint that accepts a JSON POST.

## Configure

1. Create a webhook pointing at the destination (channel webhook URL, PagerDuty/Opsgenie integration key, or custom endpoint). Store the URL/key as a credential, not in plaintext config.
2. Configure which alerts route to which webhook (severity, cluster, namespace scope).
3. Send a test event and confirm delivery in the destination.

## AI incident analysis

When an alert fires, Ankra can attach an AI analysis of the likely cause and affected resources. Route high-severity alerts to your on-call tool (PagerDuty/Opsgenie) and informational ones to a ChatOps channel so the AI summary lands where responders look.

## Rules

- **Secrets as credentials.** Webhook URLs and integration keys are secrets - store them in Ankra credentials / the secret store, never commit them.
- **Scope and severity-route.** Don't fan every alert to every channel; map severity to destination (critical → on-call, warning → chat).
- **Test delivery** before relying on a route in production.
- **Avoid alert noise.** Tune thresholds so alerts are actionable; noisy alerts get ignored.
- **Custom endpoints must be HTTPS** and validate the payload.

## Related skills

- `ankra-observability` produces the signals that drive these alerts.
- `ankra-platform-principles` for the secret-handling stance.
