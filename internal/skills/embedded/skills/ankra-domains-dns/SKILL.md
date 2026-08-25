---
name: ankra-domains-dns
description: Give Ankra clusters public hostnames with DNS and TLS - the generated per-cluster subdomain under ankra.cc, registering your own root domain delegated to Ankra, publishing records in the organisation's zone, serving zones you hold in your own DNS account with your own credential (org-wide or per cluster), and the separate preview domain PR demos publish under. Use when the user wants a custom domain, asks why an ingress hostname does not resolve or has no certificate, mentions external-dns, DNS records, delegation, or where preview and demo URLs come from.
---

# Domains, DNS and TLS on Ankra

An Ankra cluster can publish an Ingress hostname, create its DNS record and get a TLS certificate
with no manual step — but only inside the zone whose credential it holds. Almost every "it does not
resolve" or "there is no certificate" comes down to a hostname outside that zone.

## The four things people conflate

| Thing | What it is | Set with |
|-------|-----------|----------|
| **Organisation root domain** | The root every Ankra-generated hostname nests under. `ankra.cc` by default. | `ankra org domain set` |
| **Cluster generated domain** | `<cluster_short_id>.<root>` — one subzone per cluster, served by the external-dns Ankra installs on it. | `ankra cluster domain --enable` |
| **Custom DNS zone** | A zone you hold in **your own** DNS account, served by an extra external-dns using **your** credential. | `ankra org custom-dns-zones add` / `ankra cluster custom-dns-zones add` |
| **Preview domain** | Where PR demos and on-demand previews publish. Changes nothing else. | `ankra org ai-environment set --demo-base-domain` |

The first two are Ankra's own DNS account. The third is yours. The fourth is a separate setting that
people reach for the root domain to fix, and should not.

## The default path: nothing to configure

Create a cluster with `--include-dns` (the default) and it gets its own subdomain plus an
external-dns. An Ingress hostname under that subdomain gets its record and its certificate
automatically:

```bash
ankra cluster domain <cluster>                 # the generated domain, and its state
ankra cluster domain <cluster> --enable        # give a cluster one it does not have (idempotent)
ankra org dns zones                            # every cluster domain in the organisation
```

A fresh zone reads `pending` until it is published to the authoritative nameservers, then `active`;
external-dns is wired to it on the next cloud-provider pass. A cluster with none reports `none`.

**Scope, and this is the whole trap:** that external-dns manages **only** the generated subdomain.
Its credential is scoped to that zone by the DNS provider, so an Ingress on any other hostname is
dropped **silently** — no record, no certificate, no error.

## Your own root domain, delegated to Ankra

```bash
ankra org domain get
ankra org domain set example.com
ankra org domain set --default                 # back to ankra.cc
```

The domain must be **delegated to the Ankra nameservers first**; the write is refused otherwise and
names the nameservers to point at.

What this actually does: Ankra adopts the domain **as the organisation's zone apex** — it does not
carve a subzone out of it. Each cluster then gets `<cluster_short_id>.example.com`. The external-dns
on every cluster holds a token pinned to the whole domain, so an Ingress on **any** hostname under
it — `app.example.com` included — is published by the cluster serving it, with no credential of
your own. Records you already publish that no Ingress claims are left alone: each cluster's
external-dns owns only the records it created.

Two things to know before switching:

- **A switch is refused while cluster zones or DNS records still live under the old root.** The
  refusal lists exactly what to remove. Inventory with `ankra org dns zones` and `ankra org dns list`;
  clear with `ankra cluster domain <cluster> --remove` and `ankra org dns delete <record>`. Ankra
  re-creates the organisation zone under the new root once the switch is accepted.
- **A switch does not re-stamp anything.** Zone labels derive from the organisation and cluster ids,
  so a cluster keeps its label, its external-dns `--txt-owner-id`, and any GitOps path built from it.

Reading requires membership; changing requires organisation admin.

## Records in the organisation's zone

```bash
ankra org dns zone                                          # the delegated zone
ankra org dns list
ankra org dns add chat CNAME lb-1234.example-lb.com --ttl 300
ankra org dns update chat target.example.com
ankra org dns delete chat
```

Records reconcile asynchronously: a new or edited record starts `pending` and turns `active` once
published. Creating, editing and deleting requires organisation admin.

## A zone you hold in your own DNS account

When the domain lives in your provider account and cannot be delegated to Ankra, serve it with your
own credential instead:

```bash
ankra org dns credentials create ...                        # the external-dns webhook credential
ankra org dns credentials list

ankra org custom-dns-zones add --zone example.com --credential <name>
ankra org custom-dns-zones list
ankra org custom-dns-zones remove --zone example.com

ankra cluster custom-dns-zones add --zone example.com --credential <name>   # one cluster only
ankra cluster custom-dns-zones list
```

Ankra renders and reconciles a **separate** external-dns for each declared zone, pinned to exactly
that zone with its own record ownership, so it can never fight Ankra's own controller, another
cluster's, or records you publish yourself.

- **`org custom-dns-zones` covers every cluster** — the ones that exist and the ones created later.
  That "and later" is the reason to declare org-wide: a cluster created after a per-cluster
  declaration comes up with your hostnames silently unserved.
- **A cluster's own declaration wins** over the organisation's on that cluster, which is how one
  cluster serves the zone with a different credential. `cluster custom-dns-zones list` shows
  inherited zones with source `organisation`.
- **Withdrawing removes the controller Ankra rendered.** The zone's records are yours and are left
  untouched. Clusters that declared the zone themselves keep theirs.

Once a custom zone covers the organisation's preview domain, that becomes the cluster's **public**
domain — what hostnames publish under and what `${{ ankra.cluster_domain }}` resolves to in stacks
and stack profiles. `ankra cluster domain <cluster>` reports both the generated and the public one.

## Preview and demo URLs

```bash
ankra org ai-environment get
ankra org ai-environment set --demo-base-domain previews.example.com
ankra org ai-environment set --demo-base-domain ""        # back to the Ankra subzone
ankra org ai-environment set --demo-cert-issuer letsencrypt-prod
ankra org ai-environment set --demo-tls-secret <secret>
```

This decides only where PR demos and on-demand previews publish — demos land at
`<namespace>.<preview-domain>`. It is **not** `ankra org domain`, and reaching for the root domain
to change preview URLs is the common mistake. Both fields sit on the same portal screen
(**AI → Settings → Workspaces**), which is why they get confused.

**On your own preview domain, publishing the DNS is yours.** Ankra mints the hostname but publishes
records only inside the subzones it delegates, and a cluster's external-dns credential is scoped to
that cluster's own Ankra subzone and nothing else. In practice you create **one wildcard** pointing
at the staging cluster's ingress. Without it the demo still deploys and still reports ready, and its
URL does not load — and the certificate goes the same way, because an HTTP-01 challenge is answered
over the hostname being certified, so a name that does not resolve never gets one. An unpublished
preview domain costs you the URL and the TLS together.

`ankra org ai-environment get` relays the platform's verdict, naming the wildcard to create and the
address to point it at when the domain answers nothing — and says so explicitly when the platform
reports no verdict at all, rather than letting silence read as an all-clear.

TLS on your own preview domain: each demo hostname is concrete when the ingress is written, so an
HTTP-01 challenge answers for it and no wildcard certificate is involved. `--demo-tls-secret` serves
a certificate you already hold and requests nothing; `--demo-cert-issuer` asks a different
cert-manager `ClusterIssuer`. A staging cluster carrying no ACME HTTP-01 issuer cannot be asked for
a certificate at all, so its previews stay on plain HTTP — `get` says so when that is the case.

On an Ankra subzone none of this applies: the platform provisions the zone and the in-cluster
external-dns publishes each preview record itself.

## Where TLS comes from

`--include-networking` (default on) installs Traefik **and cert-manager**. A certificate is issued
when the ACME challenge can be answered, which needs the DNS record to exist and resolve. So TLS
failures are usually DNS failures one step earlier:

1. Does the hostname's zone have a controller? (generated subdomain, or a declared custom zone)
2. Did the record get created? `ankra org dns list`, or query the authoritative nameservers.
3. Is the record `active` rather than `pending`?
4. Only then look at cert-manager: `ankra cluster get pods -n cert-manager`,
   `ankra cluster describe certificate <name> -n <ns>`, `ankra cluster events -n <ns> --type Warning`.

## Diagnosing "it does not resolve"

| Symptom | Cause |
|---------|-------|
| Ingress host on your own domain gets no record at all | The generated external-dns only serves the generated subdomain. Declare the zone with `custom-dns-zones`. |
| Worked on one cluster, not on a newly created one | The zone was declared per cluster. Declare it with `ankra org custom-dns-zones` so later clusters inherit it. |
| Record exists but does not resolve | Still `pending` — not yet published to the authoritative nameservers. |
| `ankra org domain set` refused | The domain is not delegated to the Ankra nameservers yet, or zones/records still live under the old root. |
| Certificate never issues | The DNS record is missing or unresolvable; fix DNS first. |
| Preview URLs on the wrong domain | `ankra org ai-environment set --demo-base-domain`, not `ankra org domain`. |
| Demo reports ready, its URL does not load | Your preview domain has no wildcard pointing at the staging cluster's ingress. Ankra publishes nothing there. |
| Preview stays on plain HTTP | The staging cluster carries no ACME HTTP-01 issuer; `ankra org ai-environment get` reports it. |
| Cluster reports domain `opted out` | Someone ran `ankra cluster domain --remove`; the removal is held until `--enable`. |
| Nothing has a domain | The cluster was created with `--include-dns=false`. |

## Rules

- **Know which zone a hostname is in** before debugging anything else. Generated subdomain, your own
  declared zone, or neither — the third case explains most failures.
- **Delegate before registering** a root domain; the write is refused otherwise.
- **Declare org-wide** unless one cluster genuinely needs a different credential — per-cluster
  declarations leave later clusters unserved.
- **The preview domain is a separate setting** from the root domain — and on a domain you own,
  publishing its wildcard record is yours, not Ankra's.
- **Removing a cluster domain is held** until you `--enable` it again; nothing re-creates it.
- **Withdrawing a custom zone never touches your records.**
- **DNS before TLS** when diagnosing a missing certificate.

## Related skills

- `ankra-cloud-clusters` — `--include-dns` / `--include-networking` at create time.
- `ankra-getting-started` — where domains fit in the onboarding order.
- `ankra-stacks-addons` — the Ingress manifests that carry these hostnames.
- `ankra-ai-gateway` — PR demos and the staging cluster they publish from.
- `ankra-troubleshooting` — reading events and certificate state.
