---
name: ankra-observability
description: Deploy and wire observability on Ankra clusters - central metrics on an infrastructure (hub) cluster with spokes remote-writing to it (kube-prometheus-stack, Grafana, Tempo), logs (OpenSearch with Fluent Bit, or Loki), the cluster's metrics source (`spec.prometheus_metrics`) and log source (`spec.log_source`) that power Ankra's metrics pages, log history and AI, Ankra hosted logs (`ankra cluster logs-ship`), and reading it all with `ankra cluster metrics` and `ankra cluster top`. Use when the user wants monitoring, metrics, dashboards, logging, central observability across clusters, or to connect Prometheus, Grafana Cloud, VictoriaMetrics, Thanos, Loki, Elasticsearch or OpenSearch to Ankra.
---

# Ankra Observability

Observability on Ankra is ordinary stacks of Helm addons, plus a metrics source and a log source
on each cluster that Ankra's own metrics pages, log history, insights and AI read from.

## Recommended path

- Golden path **3, the estate**: one infrastructure (hub) cluster holds central Prometheus,
  Alertmanager and Grafana (and a log store); development and production clusters are thin senders
  that ship metrics and logs to it. Start with the hub, then add one spoke.
- Every cluster gets a metrics source in its cluster YAML (`spec.prometheus_metrics`) so Ankra and
  its AI can read it; apply through GitOps (`ankra-gitops`, `ankra-stacks-addons`).
- Alertmanager and Ankra's own notifications go to the same on-call channel - `ankra-alerts-webhooks`.
- Just want logs for one app with nothing to run? `ankra cluster logs-ship enable` (hosted logs).
- Avoid: a full Prometheus + Grafana + Alertmanager on every cluster, plaintext Grafana or webhook
  secrets in Git, and `kubectl port-forward` to reach Grafana - publish it behind an Ingress with TLS.

## Hub: the central metrics stack

Pin versions; reference catalogue registries (`prometheus-community`, `grafana`) by
`registry_name` + `registry_url`; let the CRDs addon own the CRDs:

```yaml
stacks:
  - name: monitoring
    manifests:
      - name: monitoring-ns
        parents: []
        from_file: stacks/monitoring/manifests/namespace.yaml
    addons:
      - name: prometheus-operator-crds
        chart_name: prometheus-operator-crds
        chart_version: 30.0.1
        registry_name: prometheus-community
        registry_url: https://prometheus-community.github.io/helm-charts
        namespace: prometheus-stack
        parents:
          - kind: manifest
            name: monitoring-ns
      - name: kube-prometheus-stack          # Prometheus + Alertmanager (receiver for the spokes)
        chart_name: kube-prometheus-stack
        chart_version: 83.4.0
        registry_name: prometheus-community
        registry_url: https://prometheus-community.github.io/helm-charts
        namespace: prometheus-stack
        parents:
          - kind: addon
            name: prometheus-operator-crds
        configuration:
          from_file: stacks/monitoring/add-ons/kube-prometheus-stack/values.yaml
          encrypted_paths: [webhook_url]   # Alertmanager receiver, sealed with SOPS
```

The values that make it a hub: `crds.enabled: false`,
`prometheus.prometheusSpec.enableRemoteWriteReceiver: true`, a default `scrapeClass` stamping
`cluster=<hub name>` on its own scrapes, and Alertmanager `group_by` including `cluster`. Every rule
then evaluates the spokes' series too, so spokes need no Alertmanager. Run Grafana (and Tempo for
traces) as their own addons. Expose the receiver as one Ingress path (`/api/v1/write`) with TLS and
an IP allow-list of the spokes' egress addresses - write-only, never readable from outside.

## Spokes: senders only

On development and production, the same kube-prometheus-stack chart in agent mode, nothing else:

```yaml
prometheus:
  agentMode: true
  prometheusSpec:
    externalLabels:
      cluster: production          # equal to the Ankra cluster name
    remoteWrite:
      - url: https://metrics-ingest.example.com/api/v1/write
grafana: {enabled: false}
alertmanager: {enabled: false}
defaultRules: {create: false}
crds: {enabled: false}
```

Logs follow the same shape: a log store on the hub (OpenSearch operator with Dashboards, or Loki)
and a shipper (Fluent Bit, or Promtail for Loki) on each spoke, each spoke with its own write-only
user. Retention policies live in Git, not in a UI.

## Metrics source and log source

Ankra's metrics pages, insights and AI read the cluster's configured sources. Declare them in the
cluster YAML; the next sync connects them:

```yaml
spec:
  prometheus_metrics:
    endpoint: http://kube-prometheus-stack-prometheus.prometheus-stack.svc.cluster.local:9090
    flavor: self_hosted            # self_hosted, grafana_cloud or victoria_metrics
  log_source:
    provider: opensearch           # loki, elasticsearch or opensearch
    endpoint: https://opensearch.logging.svc:9200
    credential_name: opensearch-read
    index_pattern: production-*
```

- A spoke in agent mode holds no queryable data: point its `prometheus_metrics` at a hub query
  endpoint (an Ingress with basic auth) with `credential_name` and `verify_ssl: true`.
- **Credentials never go in Git.** `username`, `password`, `token` or `auth_scheme` inside either
  block is refused by name. Create the credential in the portal (**Settings → Integrations**) and
  reference it with `credential_name`. Use read-only, index-scoped credentials.
- Removing a block does **not** disconnect the source; disconnect in **Settings → Integrations**.
- External Prometheus-compatible endpoints: self-hosted Prometheus, Grafana Cloud, Thanos Query,
  VictoriaMetrics. Amazon Managed Prometheus, Google Cloud Managed Prometheus and Azure Monitor
  are not selectable yet.

## Ankra hosted logs

No log store of your own: the cluster's agent ships every container's log lines to Ankra, kept
7 days, searchable from each application's log history. Off by default, per cluster, needs
`clusters.write`. A connected `log_source` takes precedence for history.

```bash
ankra cluster logs-ship status --cluster prod   # on/off, platform availability, agent new enough
ankra cluster logs-ship enable --cluster prod   # asks first; --yes in scripts
ankra cluster logs-ship disable --cluster prod
```

An agent too old to follow the switch sends nothing - `ankra cluster agent upgrade`.

## Verify

```bash
ankra cluster metrics query 'count by (cluster) (up)' --cluster infrastructure   # one value per cluster
ankra cluster metrics query-range 'up' --range 6h --step 5m
ankra cluster top nodes                       # metrics-server, works without Prometheus
ankra cluster top pods -n prometheus-stack
ankra cluster logs -l app.kubernetes.io/name=grafana -n grafana --follow=false --tail 50
```

A spoke missing from `count by (cluster)` is an allow-list or `cluster` label problem; a `403` on
remote write means the spoke's egress address changed. `ankra cluster exec <pod> -n <ns> -- curl -s
https://ifconfig.me` reads it from inside the spoke.

## Rules

- **Hub receives, spokes send.** One Prometheus and one Grafana for the estate.
- **Namespace first**, then the stack via `parents` (`kind` + `name`, never shorthand).
- **Pin chart versions**, and use a `reclaimPolicy: Retain` storage class for Prometheus and log
  data; protect the hub's stateful stacks with `ankra-backups`.
- **No plaintext secrets** (Grafana admin password, Alertmanager webhook, shipper passwords) -
  seal with `ankra-sops-secrets` and declare `encrypted_paths`.
- **Right-size retention and resources**; avoid unbounded storage defaults.

## Related skills

- `ankra-stacks-addons` / `ankra-gitops` - stack composition and the cluster YAML.
- `ankra-alerts-webhooks` - routing Ankra notifications to the same on-call channel.
- `ankra-troubleshooting` - the diagnosis workflow these metrics and logs feed.
- `ankra-domains-dns` - hostnames and TLS for Grafana and the ingest endpoints.
