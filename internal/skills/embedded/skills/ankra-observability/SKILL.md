---
name: ankra-observability
description: Deploy a monitoring and logging stack on an Ankra cluster (Prometheus, Grafana, Loki, Promtail) and wire external Prometheus-compatible metrics sources (Grafana Cloud, Amazon Managed Prometheus, Google Cloud Managed Prometheus, Thanos, VictoriaMetrics). Use when the user wants monitoring, metrics, dashboards, logging, or to connect a metrics source to Ankra.
---

# Ankra Observability

Ankra deploys observability as ordinary stacks/addons and can also read from external Prometheus-compatible metrics sources to power dashboards and AI insights.

## Monitoring + logging stack

Deploy as a focused stack. Pin versions, create the namespace first:

```yaml
stacks:
  - name: monitoring
    manifests:
      - name: monitoring-ns
        parents: []
        from_file: "manifests/monitoring-namespace.yaml"
    addons:
      - name: kube-prometheus-stack       # Prometheus + Grafana + Alertmanager
        chart_name: kube-prometheus-stack
        chart_version: 65.1.1
        repository_url: https://prometheus-community.github.io/helm-charts
        namespace: monitoring
        parents:
          - kind: manifest
            name: monitoring-ns
        configuration:
          values: |-
            grafana:
              adminPassword: ""           # set via SOPS-encrypted value, not plaintext
      - name: loki                          # logs
        chart_name: loki
        chart_version: 6.6.4
        repository_url: https://grafana.github.io/helm-charts
        namespace: monitoring
        parents:
          - kind: manifest
            name: monitoring-ns
      - name: promtail                      # log shipping
        chart_name: promtail
        chart_version: 6.16.4
        repository_url: https://grafana.github.io/helm-charts
        namespace: monitoring
        parents:
          - kind: addon
            name: loki
```

## External metrics sources

Instead of (or in addition to) in-cluster Prometheus, connect a Prometheus-compatible source so Ankra reads metrics for dashboards/insights: Grafana Cloud, Amazon Managed Prometheus, Google Cloud Managed Prometheus, Thanos, or VictoriaMetrics. Provide the query endpoint URL and scoped, read-only credentials stored in Ankra.

The source a cluster queries is configured in **Cluster Settings → Metrics**; that is the endpoint
`ankra cluster metrics` proxies to.

## Reading what you deployed

```bash
ankra cluster metrics query 'sum by (pod) (rate(container_cpu_usage_seconds_total[5m]))'
ankra cluster metrics query-range 'up' --range 6h --step 5m
ankra cluster top nodes                       # metrics-server, works without Prometheus
ankra cluster top pods -n monitoring
ankra cluster logs -l app.kubernetes.io/name=grafana -n monitoring --follow=false --tail 50
```

`top` reads the aggregated metrics API directly and works on clusters where Prometheus was never
installed; `metrics` is the PromQL surface for trends. Use these to verify the stack itself came up
before wiring dashboards to it.

## Rules

- **Namespace first**, then the stack via `parents`.
- **Pin chart versions.**
- **No plaintext credentials** (Grafana admin password, remote-write tokens) — use SOPS-encrypted values (`ankra-sops-secrets`).
- **Read-only credentials** for external metrics sources.
- **Right-size retention and resources** in values; avoid unbounded storage defaults in production.

## Related skills

- `ankra-stacks-addons` for stack composition.
- `ankra-alerts-webhooks` to route alerts from this stack to Slack/Teams/PagerDuty with AI analysis.
- `ankra-troubleshooting` for the diagnosis workflow these metrics feed.
