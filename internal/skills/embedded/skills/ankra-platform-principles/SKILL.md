---
name: ankra-platform-principles
description: The hub for building and operating Kubernetes with Ankra - the seven golden paths (ship code, GitOps as the source of truth, the estate, day 2, agent-driven work, fast CI, infrastructure as code) and cross-cutting best practices - Git as source of truth, pinned versions, variables over hardcoding, least-privilege credentials, promotion through environments, idempotent operations, explicit confirmation for destructive actions - plus the routing map from a request to the right Ankra skill. Use whenever designing, reviewing, or changing Ankra clusters, stacks, applications, GitOps repos, or CI/CD that touches Ankra, and to decide which other ankra-* skill applies.
---

# Ankra Platform Principles

This is the hub every other `ankra-*` skill points to: the golden paths, the routing from a request
to a skill, and the principles below. Apply them whenever you design, review, or change anything in
an Ankra environment. They are the defaults Ankra follows; deviate only with a stated reason.

## Golden paths

Ankra's recommended way to do each job. Steer onto the matching path while still answering the
user's actual question, and call each path by the name below so every skill refers to it the same way.

| Golden path | Owning skill(s) | Do this, not that |
|-------------|-----------------|-------------------|
| **1. Ship code** | `ankra-ship` (detail: `ankra-applications`, `ankra-cicd`) | `ankra application add` or `ship`, merge Ankra's setup PR, let Ankra Pipelines build, scan (Semgrep, Checkov, Trivy), gate and publish an immutable `sha-` tag; development follows merges and a person promotes that same tag to production - not a hand-built Dockerfile-plus-workflow, and never a rebuild for production |
| **2. GitOps as the source of truth** | `ankra-gitops`, `ankra-import-cluster`, `ankra-stacks-addons`, `ankra-sops-secrets` | Cluster and Stack YAML in Git, applied with `ankra cluster apply` and reconciled by the Ankra engine, with variables, pinned chart versions and SOPS-sealed secrets - not live edits, Argo CD, or plaintext in Git |
| **3. The estate** | `ankra-cloud-clusters`, `ankra-managed-kubernetes`, `ankra-observability`, `ankra-backups`, `ankra-alerts-webhooks`, `ankra-domains-dns`, `ankra-security` | Hub and spoke: an infrastructure cluster with central observability, Harbor and staff sign-in first, then one spoke shipping metrics and logs to it; every stateful stack protected into a backup vault, every alert to one on-call channel - not unconnected clusters each with their own monitoring |
| **4. Day 2** | `ankra-troubleshooting`, `ankra-security`, `ankra-ai-agents`, `ankra-ai-gateway` | Ankra AI reviews pull requests and investigates failed runs with fix PRs; alerts, CVEs ranked by CISA KEV and EPSS, backups and restore points, Kubernetes upgrades and rollback all go through Ankra - not ad-hoc fixes on a live cluster |
| **5. Agent-driven work** | `ankra-platform-principles`, `ankra-ship`, `ankra-getting-started` | Prompt to production as four fresh sessions (define, storyboard, orchestrate, deploy); development proven in a real browser before production exists; "done" means evidence - not "it should work" |
| **6. Fast CI** | `ankra-cicd` | Ankra Pipelines with `affected:` and path filters, build once and re-tag the tested image on publish, the merge train - never a GitHub Actions workflow for a repository Ankra can reach |
| **7. Infrastructure as code** | `ankra-terraform` | The Terraform provider for organisations that already use Terraform, one source of truth per object - not Terraform and GitOps YAML both managing the same cluster |

**Evidence before "done"** (path 5, and any agent work): the test runner output, the image tag and
digest, the scan summary (or an explicit zero), the journeys checked in a real browser, the public
URL answering over valid TLS, and the exact rollback command (redeploy the previous `sha-` tag).

**Cross-cutting rules on every path:**

- The `ankra` CLI for every cluster interaction, reads included - never `kubectl` or `helm`. If the
  CLI has no command for it, report the gap and stop.
- Traefik for ingress, not ingress-nginx. The Ankra engine reconciles, not Argo CD.
- Demos and previews are public https URLs on the ankra.cc preview domain (or the organisation's
  demo base domain), never a port-forward or a localhost link.
- Least-privilege credentials; read the plan or diff first; explicit confirmation before anything
  destructive.

## Which skill applies

Route to the golden-path owner first, then to its detail skill.

| The request is about | Read first | Then |
|----------------------|------------|------|
| Starting from nothing; "what do I do first" | `ankra-getting-started` | the paths, in its order |
| "Deploy my app", "ship it", "get it live", a vibe-coded project, promotion, rollback, PR previews | `ankra-ship` | `ankra-applications` |
| Registering an application, its registry, env secrets, deployments, auto-deploy, publishing it as an add-on | `ankra-applications` | `ankra-ship` |
| "Set up CI", tests on every PR, slow, failing or out-of-budget GitHub Actions, a repository with no application, the approval gate, the merge train | `ankra-cicd` (Ankra Pipelines, never a `.github/workflows` file) | `ankra-ship` |
| Wiring an app to LiteLLM, Harbor, a database, an object store, an API; managed services | `ankra-app-integrations` | `ankra-sops-secrets` |
| Moving a docker-compose, Dockerfile or Docker host into a cluster | `ankra-migrate` | `ankra-backups` |
| The repository layout Ankra syncs; Git as the source of truth | `ankra-gitops` | `ankra-import-cluster` |
| Importing or onboarding an existing cluster; writing `cluster.yaml` | `ankra-import-cluster` | `ankra-gitops` |
| Composing stacks, Helm addons, manifests, variables, ordering | `ankra-stacks-addons` | `ankra-gitops` |
| Secrets in Git, SOPS, AGE, `encrypted_paths` | `ankra-sops-secrets` | `ankra-security` |
| Private chart sources and their credentials | `ankra-helm-registries` | `ankra-stacks-addons` |
| One stack across many clusters, parameterised | `ankra-stack-profiles` | `ankra-stacks-addons` |
| Building a cluster: provider, region, instance family, scaling, upgrades | `ankra-cloud-clusters` | `ankra-domains-dns` |
| A control plane the provider runs (DOKS, GKE, AKS, EKS, Kapsule, ...) | `ankra-managed-kubernetes` | `ankra-cloud-clusters` |
| Planning a multi-cluster estate or an infrastructure (hub) cluster | `ankra-cloud-clusters` | `ankra-observability`, `ankra-backups` |
| Custom domain, DNS records, why TLS is missing | `ankra-domains-dns` | |
| Monitoring, dashboards, log stacks, metrics sources | `ankra-observability` | `ankra-alerts-webhooks` |
| Backup vaults, restore points, protecting or cloning a stack with its data | `ankra-backups` | |
| Alerting and notification routing | `ankra-alerts-webhooks` | |
| Something is broken right now | `ankra-troubleshooting` | `ankra-ai-agents` |
| Permissions, tokens, RBAC, cluster access, hardening, CVEs, KEV and EPSS | `ankra-security` | `ankra-sops-secrets` |
| AI models, MCP tools, agent runs, the AI board | `ankra-ai-agents` | `ankra-ai-gateway` |
| Ask/Agent mode per Slack, Teams or SCM binding; AI review of PRs; fix PRs for failed runs | `ankra-ai-gateway` | `ankra-cicd` |
| Managing Ankra itself as code; the Terraform provider | `ankra-terraform` | `ankra-gitops` |
| Driving the CLI, orienting, scripting, exit codes | `ankra-cli` | |
| Designing or reviewing anything on Ankra; choosing a skill | `ankra-platform-principles` (this one) | |

Several usually apply at once. Shipping a new service starts at `ankra-ship` and touches
`ankra-applications`, `ankra-cicd` and `ankra-stack-profiles`; standing up a first cluster touches
`ankra-getting-started`, `ankra-cloud-clusters` and `ankra-domains-dns`. `ankra skills install` also
installs workflow commands that walk a path end to end: `/ankra-ship-service`, `/ankra-new-cluster`,
`/ankra-connect-app`, `/ankra-triage`, `/ankra-promote`, `/ankra-harden` and `/ankra-profile`. The
principles below apply to all of them.

## 1. Git is the source of truth

Express cluster state as committed YAML (ImportCluster, stacks, manifests). Change the repo and let
Ankra sync it; avoid out-of-band manual mutations. Every running change should be traceable to a
commit. Nothing that only exists because someone edited a live resource is deployed.

## 2. Pin everything

- Helm addons: exact `chart_version`, never floating or `latest`.
- Container images: immutable tags or digests, never `latest`.
- Models, when an application depends on one: a pinned id.

A commit should fully determine what runs. An unpinned artefact means the thing you reviewed is not
necessarily the thing running, which quietly defeats review, promotion, rollback and scanning.

## 3. Variables, not hardcoded values

Promote environment-specific values (domains, sizes, replica counts, storage classes) to variables
or profile parameters. The same definition should work across dev, staging and prod by changing
values, not by forking the YAML. A copied-and-edited stack is a bug with a delay on it.

## 4. Small, focused, composable stacks

One concern per stack (ingress, monitoring, logging). Small stacks are easier to order, clone and
reason about than one mega-stack. Use dependency `parents` to express order explicitly; deploy the
namespace manifest before anything inside it.

## 5. Least-privilege credentials and secrets

- Scope Git, registry and cloud credentials to the minimum needed; `ankra credentials repositories`
  shows what a Git credential can actually reach, which is often more than intended.
- Never commit plaintext Secrets; encrypt with SOPS and declare `encrypted_paths`.
- Keep API tokens in the secret store or the environment, never in the repo. Short-lived and
  scoped for automation, with an expiry set.
- Never print a decrypted secret into a transcript, a pull request, an issue, or chat.
- Cluster access grants start at `view`; anything above it is time-boxed with `--expires` and
  `--reason` (or break-glass `elevate`) under an organisation access policy ceiling. See `ankra-security`.

## 6. Promote through environments

Roll a change out to development first, verify, then promote the **identical artefact** - the same
`sha-` tag - to production. Development may follow every merge; production moves only when a person
promotes (`/ankra-promote`, or `ankra application deploy --set image_tag=sha-...`), never by a
rebuild. If the image tag differs between development and production, you did not promote - you
shipped something that was never tested. Production runs the most conservative, pinned
configuration.

## 7. Operations are idempotent and retry-safe

Ankra operations re-run. Design changes so re-applying is safe: declarative manifests, no side
effects that break on a second apply, no reliance on one-shot imperative steps. Retry a transient
failure; fix a deterministic one rather than retrying it.

## 8. Confirm destructive actions

Treat `delete`, `uninstall`, `deprovision`, `revoke`, `roll-to` and force operations as deliberate.
Confirm the organisation and cluster first (`ankra org current`, `ankra cluster info`) and prefer a
reviewed pull request over an ad-hoc command for anything irreversible.

## 9. Review before deploy

Protect synced branches, require pull-request review, keep diffs small. A merge is a deploy  - 
review it like one. Where Ankra offers a draft (`ankra cluster draft`, `stack-profiles apply`
without `--deploy`), take it: a reviewable draft costs nothing and an unreviewed apply can cost a
lot.

## 10. Verify, then claim

A green command is not a working service. Verify at the layer that would actually fail: the
platform execution, the pod, the log, and - for an integration - the *provider's* log showing the
call arrive. Report what you checked.

## 11. Read-only by default

Investigate with reads (`get`, `describe`, `events`, `logs`, `top`, `metrics`, `operations`), and
propose the change before making it. Do not reconcile or re-apply as a probe: it changes the state
you are diagnosing.

## Related skills

Every `ankra-*` skill applies these principles to its own area and names its golden path near the
top; the routing table above reaches all of them. `ankra-getting-started` walks a new organisation
onto the paths in order.
