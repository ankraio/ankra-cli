---
name: ankra-getting-started
description: Onboard from an empty Ankra organisation to a running application - installing and authenticating the CLI, storing cloud/Git/registry credentials, deciding between importing a cluster and letting Ankra build one, wiring the GitOps repository, choosing the domain and DNS, laying down the base platform stack, shipping the first app on Ankra Pipelines, and wiring day 2 (alerts, backups, AI review) - walking onto Ankra's golden paths in the order that avoids rework. Use when the user is new to Ankra, is setting up an organisation or their first cluster, asks "how do I start" or "what do I do first", or needs the end-to-end onboarding path rather than one subsystem.
---

# Getting started with Ankra

## Recommended path

This skill walks a new organisation onto the golden paths in `ankra-platform-principles`, in an
order where nothing has to be redone: **GitOps as the source of truth** (or **infrastructure as
code** with `ankra-terraform`, not both for one cluster) before the first cluster, **the estate**
starting small, **ship code** with **fast CI** on Ankra Pipelines for the first application, then
**day 2**. Hand off to the owning skill at each step. Avoid the two classic dead ends: a cluster
built by hand that exists only in a dashboard, and a first app deployed with a hand-written GitHub
Actions workflow or `kubectl`.

Three decisions are cheap now and expensive later - the GitOps repository, the batteries on the
first cluster, and the domain - so they come before anything is deployed.

```
CLI + login → credentials → GitOps repo (path 2) → first cluster (path 3) → domain/DNS
  → base platform stack (path 2) → first application (paths 1 + 6) → day 2 (path 4) → access
```

An assistant doing this end to end follows **agent-driven work** (path 5): development first, proven
in a real browser on a public https URL, and "done" reported with evidence.

## 0. The decisions, up front

| Decision | Ask | Default if unsure |
|----------|-----|-------------------|
| **Import or build?** | Do you already run Kubernetes? | Import what exists; build new clusters with Ankra |
| **Managed or self-managed control plane?** | Do you need cluster-admin over the control plane? | Provider-managed (`ankra cluster managed`) |
| **GitOps repository** | Where should cluster state be reviewable? | One repo, `platform-gitops`, wired at cluster create |
| **GitOps YAML or Terraform?** | Does the organisation already run Terraform? | GitOps YAML; Terraform (`ankra-terraform`) if it is already your IaC - one source of truth per object |
| **Domain** | Whose DNS holds it? | Start on the generated `ankra.cc` subdomain; move later |
| **Registry** | Do you already run one? | Ankra's own registry until you do |

Getting the first three right is what separates a cluster you can review and rebuild from one that
only exists in a dashboard.

## 1. CLI and login

```bash
bash <(curl -sL https://github.com/ankraio/ankra-cli/releases/latest/download/install.sh)
ankra --version
ankra completion install

ankra login                      # browser SSO
ankra org list
ankra org switch <slug>          # not "select" - the verb is switch
ankra org current
```

Install the skills into whatever assistant you use, so the rest of this is applied automatically:

```bash
ankra skills install             # every assistant configured on this machine, plus the /ankra-* workflow commands
ankra skills install --with-hooks   # also ask before an assistant runs a cluster-mutating kubectl or helm
ankra skills clients
```

## 2. Payment method and credentials

**Building a cluster on Hetzner (or any self-managed provider) needs a payment method on the
organisation.** Without one the create is refused with "The cluster was not created ... Add a
payment method"; that is intentional. Have the user save a card under **Billing** first. The
playground (below) is the only cluster that works without one.

Store these before creating anything; several later steps take credential **IDs**. A Hetzner
cluster needs a Hetzner credential (a **Read & Write** project token; the CLI prompts for it) and
at least one SSH key credential. In the new portal (`/next`) both are added at **Integrations &
alerts → Credentials → Add credential**.

```bash
ankra credentials list
ankra credentials hetzner create --name hetzner-prod        # or ovh / upcloud / digitalocean / proxmox / morpheus
ankra credentials ssh-key create --name ops-key --public-key-file ~/.ssh/id_ed25519.pub   # org-wide, works for every provider
ankra credentials repositories <git-credential>             # what a Git credential can ACTUALLY reach
ankra helm credentials list                                 # private chart registries
```

GitHub, GCP, Azure, AWS and Scaleway credentials are added from the portal's **Credentials** page.
Scope each one to the project, subscription or repositories Ankra manages - a Git credential meant
for one repository often reaches a whole organisation, and nothing in its name says so.

## 3. Decide the GitOps repository now

Cluster state should live in Git and be reviewable in a pull request. Create the repository (empty
is fine) before the first cluster, because both `create` paths can wire it in the same command:

```bash
ankra cluster hetzner create ... \
  --gitops-repository my-org/platform-gitops \
  --gitops-credential-name github-acme \
  --gitops-branch main

ankra cluster managed create ... --gitops-repository my-org/platform-gitops \
  --gitops-credential-name github-acme
```

Retrofitting GitOps onto a running cluster is strictly more work. See `ankra-gitops` for the layout.

## 4. The first cluster

**The estate starts small.** The target shape is hub and spoke: an infrastructure cluster holding
central observability, Harbor and staff sign-in, with development and production clusters shipping
metrics and logs to it. Do not build it all at once: one cluster first (the hub, or a development
cluster for a single team), then one spoke. Production is created only once development is proven.
See `ankra-cloud-clusters` and `ankra-observability`.

**No cloud account yet? Use the playground.** Every organisation may hold one: a real, writable
virtual cluster on Ankra's own infrastructure, agent already installed, expiring after inactivity.
It is the fastest way to try stacks, addons and deploys before any credential exists:

```bash
ankra cluster playground create
ankra cluster playground status
ankra cluster playground destroy
```

Everything below works against it, except the provider-specific lifecycle commands.

**Adopting one that already exists** - write an ImportCluster YAML and apply it
(`ankra-import-cluster`):

```bash
ankra cluster validate -f cluster.yaml
ankra cluster draft    -f cluster.yaml      # stage as reviewable drafts
ankra cluster apply    -f cluster.yaml
```

For a *managed* cluster at a provider, Ankra can adopt it without you running anything against it:

```bash
ankra cluster managed discover --provider eks --credential-id <id>
ankra cluster managed import   --provider eks --credential-id <id> \
  --provider-cluster-id <id> --name prod
```

**Letting Ankra build one** - discover the region and instance family first, then create
(`ankra-cloud-clusters`, or `ankra-managed-kubernetes` for a provider-run control plane):

```bash
ankra cluster hetzner locations --credential-id <id>
ankra cluster hetzner server-types --credential-id <id> --location nbg1 --available-only
ankra cluster kubeadm-versions

# server types from the AVAILABLE=yes rows; the cx line is often unavailable
ankra cluster hetzner create --name prod --credential-id <id> \
  --ssh-key-credential-id <ssh_id> --location nbg1 \
  --bastion-server-type cpx12 --control-plane-server-type cpx22 \
  --control-plane-count 3 --worker-count 3 --worker-server-type cpx22 \
  --gitops-repository my-org/platform-gitops --gitops-credential-name github-acme
```

In the portal the same flow is the new portal's wizard: **Infrastructure → Clusters → Add a
cluster → Hetzner** (`/next/clusters/new?provider=hetzner`) - name and purpose, credential, SSH
key, location, 1 or 3 control plane nodes, management server type, node groups, kubeadm (default,
Cilium) or k3s, version, an optional Git repository, then **Review** (with a cost estimate) and
**Create**. Point users there, not at Classic.

**Take the batteries.** `--external-cloud-provider`, `--include-networking` and `--include-dns`
default on and give you load balancers, persistent volumes, ingress, DNS and TLS from the first
minute. Turn one off only when you are bringing your own, and say so in the stack.

Then:

```bash
ankra cluster select prod
ankra cluster info
ankra cluster agent status              # the agent must be online before anything else works
ankra cluster operations list           # watch the build converge
```

## 5. Domain and DNS

Out of the box each cluster gets `<cluster_short_id>.ankra.cc` and publishes Ingress hostnames under
it with certificates, no configuration. That is enough to get an app on a real HTTPS URL today.

When you want your own domain:

```bash
ankra cluster domain <cluster>                       # what it has now
ankra org domain set example.com                     # delegate to Ankra's nameservers FIRST
# or, for a zone you keep in your own DNS account:
ankra org dns credentials create ...
ankra org custom-dns-zones add --zone example.com --credential <name>
```

The generated external-dns serves **only** the generated subdomain - an Ingress on your own domain
is dropped silently until the zone is declared. Declare org-wide so clusters created later inherit
it. See `ankra-domains-dns`.

## 6. The base platform stack

Before any application, lay down the shared layer as a stack (`ankra-stacks-addons`), pinned and
namespace-ordered:

```bash
ankra cluster stacks list
ankra cluster addons available
ankra cluster addons list
```

Typical first stack: ingress (if you skipped `--include-networking`), monitoring and logging
(`ankra-observability`), and any operator your apps need. One concern per stack, exact
`chart_version`, namespace manifest as the parent of everything in it.

Once it works, capture it so the next cluster is not hand-built:

```bash
ankra stack-profiles drafts create --name base-platform \
  --source-cluster prod --source-stack platform
```

See `ankra-stack-profiles`.

## 7. The first application (ship code + fast CI)

Follow `ankra-ship`. Ankra opens a setup pull request with the Dockerfile, manifests,
`.ankra/ankra.yaml` and `.ankra/pipeline.yaml`; once merged, Ankra Pipelines tests, builds rootless
in your own cluster, scans with Semgrep, Checkov and Trivy, gates on findings and publishes an
immutable `sha-` tag. Development follows every merge.

```bash
ankra application ship . --cluster <dev-cluster>     # from the repo checkout: register, setup PR, first build, deploy; prints Live:
ankra pipeline get --application <id> --latest --wait --exit-code
printf '%s' "$DATABASE_URL" | ankra application env-secrets set <id> DATABASE_URL
ankra application env-secrets apply <id>
ankra application deployments <id>                   # where it runs
```

`ship` is `add` plus the waits and the first deploy; `ankra application add .` alone registers the
repository and opens the setup pull request.

Production comes later and moves only when a person promotes the tag development proved, never a
rebuild (`/ankra-promote`, or `ankra application deploy <id> --cluster <prod-cluster-id> --set
image_tag=sha-<tag>`). Verify before calling it done - operations, pods, logs, and the public URL:

```bash
ankra cluster operations list
ankra cluster get pods -n <namespace>
ankra cluster logs -l app=<name> -n <namespace> --follow=false --tail 100
```

If it does not come up, switch to `ankra-troubleshooting` rather than changing things. See
`ankra-applications` for registries, auto-deploy, PR previews and reaching more clusters. CI for a
repository that is *not* an application (a library, docs, platform code) is Ankra Pipelines too:
`ankra pipeline repositories connect` plus a committed `.ankra/pipeline.yaml` - `ankra-cicd`. Never
write a `.github/workflows` CI file for a repository Ankra can reach.

## 8. Day 2 from the start

Wire these before the first incident, not after it (`ankra-alerts-webhooks`, `ankra-backups`,
`ankra-ai-gateway`, `ankra-security`):

```bash
ankra alerts destinations create --name on-call --type slack --url <webhook>   # one on-call channel
ankra alerts routes create --destination-id <id> --severity critical
ankra backup vaults provision                                     # a vault on a provider credential
ankra cluster stacks protect <stack> --vault backups --schedule daily
ankra org ai-review repos set my-org/app --review                 # Ankra AI reviews every pull request
ankra security findings --known-exploited                         # CVEs ranked by CISA KEV and EPSS
```

Every stateful stack is protected into a vault, and every alert reaches one on-call channel.

## 9. Access and hardening

```bash
ankra org invite <email> --role viewer
ankra cluster access grant <email> --cluster prod --role view
ankra tokens create ci-deploy --expires 2026-12-31        # REST-only; add --scopes for MCP
ankra tokens create ci-bot --service --role <role>        # a service account's token, for automation
```

A person who needs their own kubectl gets short-lived credentials with `ankra cluster kubeconfig add`;
an assistant stays on the `ankra` CLI.

`view` is enough to troubleshoot - `ankra cluster logs --previous`, `describe`, `events` and `top`
all work through the agent with no grant at all. Run `/ankra-harden` or read `ankra-security` once
more than one person has access.

The person who creates a cluster gets `cluster-admin` on it by default, unless the organisation
access policy sets another creator role (`ankra org access-policy get` shows it). If yours gives
creators `view` and you need `edit` for a first debugging session, ask an admin for a time-boxed
grant, or run `elevate` yourself if you hold the break-glass permission `kube_access.elevate`:

```bash
ankra cluster access grant <email> --cluster prod --role edit --expires 4h --reason "first debugging session"
ankra cluster access elevate --cluster prod --role edit --expires 4h --reason "first debugging session"
```

## 10. Where to go next

| Next | Golden path | Skill |
|------|-------------|-------|
| Promotion, previews, rollback by tag | Ship code | `ankra-ship` |
| CI for any repository, `affected:` stages, the merge train | Fast CI | `ankra-cicd` |
| Secrets in Git | GitOps as the source of truth | `ankra-sops-secrets` |
| Wiring an app to LiteLLM, Harbor, a database | Ship code | `ankra-app-integrations` |
| The same stack on many clusters | The estate | `ankra-stack-profiles` |
| Central observability, backups, alerting | The estate | `ankra-observability`, `ankra-backups`, `ankra-alerts-webhooks` |
| AI review, fix PRs, Ankra's own agents | Day 2 | `ankra-ai-gateway`, `ankra-ai-agents` |
| Moving an existing Docker deployment in | Ship code | `ankra-migrate` |
| Managing Ankra itself as code | Infrastructure as code | `ankra-terraform` |

## Rules

- **Credentials and the GitOps repo before the first cluster.** Both are wired at create time.
- **Discover regions and instance families**; never guess them from provider docs.
- **Take the batteries on the first create** - CCM, ingress and DNS are painful to retrofit.
- **`ankra org switch`**, not `select`. **`create` has no `--wait`** - follow with
  `operations list`.
- **The agent must be online** before anything else is worth debugging.
- **Validate → draft → apply**, and prefer a reviewable draft on anything you care about.
- **Verify at the layer that fails**: operations, then pods, then logs.
- **Development before production.** Production is created and promoted to only after development
  is proven on its public URL; "done" means evidence (tests, `sha-` tag and digest, scan summary,
  URL, rollback command).
- **Start on the generated domain.** Moving to your own domain later is supported; a half-configured
  custom domain on day one is the most common way to get stuck.

## Related skills

- `ankra-platform-principles` - the golden paths and the practices behind all of the above.
- `ankra-ship` - the first application and every one after it.
- `ankra-cli` - the full command surface and scripting conventions.
- `ankra-import-cluster` / `ankra-cloud-clusters` / `ankra-managed-kubernetes` - the three cluster paths.
- `ankra-domains-dns` - domains, DNS and TLS in depth.
- `ankra-gitops` - the repository layout.
