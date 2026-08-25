---
name: ankra-platform-principles
description: Cross-cutting best practices for building and operating Kubernetes with Ankra - Git as source of truth, pinned versions, variables over hardcoding, least-privilege credentials, promotion through environments, idempotent operations, explicit confirmation for destructive actions - plus the routing map from a request to the right Ankra skill. Use whenever designing, reviewing, or changing Ankra clusters, stacks, applications, GitOps repos, or CI/CD that touches Ankra, and to decide which other ankra-* skill applies.
---

# Ankra Platform Principles

Apply these whenever you design, review, or change anything in an Ankra environment. They are the
defaults Ankra follows; deviate only with a stated reason.

## Which skill applies

| The request is about | Read |
|----------------------|------|
| Driving the CLI, orienting, scripting | `ankra-cli` |
| Importing or onboarding an existing cluster | `ankra-import-cluster` |
| Provisioning a cluster Ankra manages | `ankra-cloud-clusters`, `ankra-managed-kubernetes` |
| Composing stacks, Helm addons, manifests, ordering | `ankra-stacks-addons` |
| One stack across many clusters, parameterised | `ankra-stack-profiles` |
| Deploying your own source code | `ankra-applications` |
| Wiring an app to LiteLLM, Harbor, a database, an API | `ankra-app-integrations` |
| Pipelines that build and roll out | `ankra-cicd` |
| The repository layout Ankra syncs | `ankra-gitops` |
| Secrets in Git | `ankra-sops-secrets` |
| Private chart sources | `ankra-helm-registries` |
| Monitoring, dashboards, log stacks, metrics sources | `ankra-observability` |
| Something is broken right now | `ankra-troubleshooting` |
| Alerting and notification routing | `ankra-alerts-webhooks` |
| Permissions, tokens, RBAC, hardening | `ankra-security` |
| AI models, MCP tools, agent runs, the board | `ankra-ai-agents` |
| Ask/Agent mode per Slack/Teams/SCM binding | `ankra-ai-gateway` |
| Managing Ankra itself as code | `ankra-terraform` |

Several usually apply at once. Shipping a new service touches `ankra-applications`,
`ankra-cicd` and `ankra-stack-profiles`; the principles below apply to all of them.

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

## 6. Promote through environments

Roll a change out to dev/staging first, verify, then promote the **identical artefact** to
production. If the image tag differs between staging and production, you did not promote — you
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

Protect synced branches, require pull-request review, keep diffs small. A merge is a deploy —
review it like one. Where Ankra offers a draft (`ankra cluster draft`, `stack-profiles apply`
without `--deploy`), take it: a reviewable draft costs nothing and an unreviewed apply can cost a
lot.

## 10. Verify, then claim

A green command is not a working service. Verify at the layer that would actually fail: the
platform execution, the pod, the log, and — for an integration — the *provider's* log showing the
call arrive. Report what you checked.

## 11. Read-only by default

Investigate with reads (`get`, `describe`, `events`, `logs`, `top`, `metrics`, `operations`), and
propose the change before making it. Do not reconcile or re-apply as a probe: it changes the state
you are diagnosing.

## Related skills

These principles are applied concretely in `ankra-import-cluster`, `ankra-stacks-addons`,
`ankra-stack-profiles`, `ankra-applications`, `ankra-gitops`, `ankra-cicd`, `ankra-sops-secrets`,
`ankra-security` and `ankra-troubleshooting`.
