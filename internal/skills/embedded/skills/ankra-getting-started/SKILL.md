---
name: ankra-getting-started
description: Onboard from an empty Ankra organisation to a running application - installing and authenticating the CLI, storing cloud/Git/registry credentials, deciding between importing a cluster and letting Ankra build one, wiring the GitOps repository, choosing the domain and DNS, laying down the base platform stack, and deploying the first app - in the order that avoids rework. Use when the user is new to Ankra, is setting up an organisation or their first cluster, asks "how do I start" or "what do I do first", or needs the end-to-end onboarding path rather than one subsystem.
---

# Getting started with Ankra

The order below is chosen so nothing has to be redone. Three decisions are cheap now and expensive
later — the GitOps repository, the batteries on the first cluster, and the domain — so they come
before anything is deployed.

```
CLI + login → organisation → credentials → GitOps repo → cluster → domain/DNS
           → base platform stack → first application → access + hardening
```

## 0. The five decisions, up front

| Decision | Ask | Default if unsure |
|----------|-----|-------------------|
| **Import or build?** | Do you already run Kubernetes? | Import what exists; build new clusters with Ankra |
| **Managed or self-managed control plane?** | Do you need cluster-admin over the control plane? | Provider-managed (`ankra cluster managed`) |
| **GitOps repository** | Where should cluster state be reviewable? | One repo, `platform-gitops`, wired at cluster create |
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
ankra skills install             # every assistant configured on this machine
ankra skills clients
```

## 2. Credentials

Store these before creating anything; several later steps take credential **IDs**.

```bash
ankra credentials list
ankra credentials hetzner create --name hetzner-prod        # or ovh / upcloud / digitalocean / proxmox / morpheus
ankra credentials hetzner ssh-key create --name ops-key
ankra credentials repositories <git-credential>             # what a Git credential can ACTUALLY reach
ankra helm credentials list                                 # private chart registries
```

GitHub, GCP, Azure, AWS and Scaleway credentials are added from the portal's **Credentials** page.
Scope each one to the project, subscription or repositories Ankra manages — a Git credential meant
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

**No cloud account yet? Use the playground.** Every organisation may hold one: a real, writable
virtual cluster on Ankra's own infrastructure, agent already installed, expiring after inactivity.
It is the fastest way to try stacks, addons and deploys before any credential exists:

```bash
ankra cluster playground create
ankra cluster playground status
ankra cluster playground destroy
```

Everything below works against it, except the provider-specific lifecycle commands.

**Adopting one that already exists** — write an ImportCluster YAML and apply it
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

**Letting Ankra build one** — discover the region and instance family first, then create
(`ankra-cloud-clusters`, or `ankra-managed-kubernetes` for a provider-run control plane):

```bash
ankra cluster hetzner locations --credential-id <id>
ankra cluster hetzner server-types --credential-id <id> --location nbg1 --available-only
ankra cluster k3s-versions

ankra cluster hetzner create --name prod --credential-id <id> \
  --ssh-key-credential-id <ssh_id> --location nbg1 \
  --control-plane-count 3 --worker-count 3 --worker-server-type cx43 \
  --gitops-repository my-org/platform-gitops --gitops-credential-name github-acme
```

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

The generated external-dns serves **only** the generated subdomain — an Ingress on your own domain
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

## 7. The first application

```bash
ankra application add .                              # from the repo checkout
ankra application get <id>                           # review the setup PR Ankra opened
ankra application env-secrets list <id>
printf '%s' "$DATABASE_URL" | ankra application env-secrets set <id> DATABASE_URL
ankra application env-secrets apply <id>
ankra application deploy <id> --cluster <cluster-id> --namespace app
```

Verify before calling it done:

```bash
ankra cluster operations list
ankra cluster get pods -n app
ankra cluster logs -l app=<name> -n app --follow=false --tail 100
```

If it does not come up, switch to `ankra-troubleshooting` rather than changing things. See
`ankra-applications` for registries, auto-deploy, PR demos and reaching more clusters.

## 8. Access and hardening

```bash
ankra org invite <email> --role viewer
ankra cluster access grant <email> --cluster prod --role view
ankra tokens create ci-deploy --expires 2026-12-31        # REST-only; add --scopes for MCP
ankra cluster kubeconfig add --use                        # short-lived credentials, no kubeconfig to leak
```

`view` is enough to troubleshoot — `ankra cluster logs --previous`, `describe`, `events` and `top`
all work through the agent with no grant at all. Run `/ankra-harden` or read `ankra-security` once
more than one person has access.

## 9. Where to go next

| Next | Skill |
|------|-------|
| Continuous delivery on push | `ankra-cicd`, `ankra-applications` |
| Secrets in Git | `ankra-sops-secrets` |
| Wiring an app to LiteLLM, Harbor, a database | `ankra-app-integrations` |
| The same stack on many clusters | `ankra-stack-profiles` |
| Alerting | `ankra-alerts-webhooks` |
| Ankra's own AI agents | `ankra-ai-agents`, `ankra-ai-gateway` |
| Managing Ankra itself as code | `ankra-terraform` |

## Rules

- **Credentials and the GitOps repo before the first cluster.** Both are wired at create time.
- **Discover regions and instance families**; never guess them from provider docs.
- **Take the batteries on the first create** — CCM, ingress and DNS are painful to retrofit.
- **`ankra org switch`**, not `select`. **`create` has no `--wait`** — follow with
  `operations list`.
- **The agent must be online** before anything else is worth debugging.
- **Validate → draft → apply**, and prefer a reviewable draft on anything you care about.
- **Verify at the layer that fails**: operations, then pods, then logs.
- **Start on the generated domain.** Moving to your own domain later is supported; a half-configured
  custom domain on day one is the most common way to get stuck.

## Related skills

- `ankra-platform-principles` — the practices behind all of the above.
- `ankra-cli` — the full command surface and scripting conventions.
- `ankra-import-cluster` / `ankra-cloud-clusters` / `ankra-managed-kubernetes` — the three cluster paths.
- `ankra-domains-dns` — domains, DNS and TLS in depth.
- `ankra-gitops` — the repository layout.
