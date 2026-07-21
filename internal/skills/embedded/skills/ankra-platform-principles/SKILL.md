---
name: ankra-platform-principles
description: Cross-cutting best practices for building and operating Kubernetes with Ankra - Git as source of truth, pinned versions, variables over hardcoding, least-privilege credentials, promotion through environments, idempotent operations, and explicit confirmation for destructive actions. Use whenever designing, reviewing, or changing Ankra clusters, stacks, GitOps repos, or CI/CD that touches Ankra.
---

# Ankra Platform Principles

Apply these whenever you design, review, or change anything in an Ankra environment. They are the defaults Ankra follows; deviate only with a stated reason.

## 1. Git is the source of truth

Express cluster state as committed YAML (ImportCluster, stacks, manifests). Change the repo and let Ankra sync it; avoid out-of-band manual mutations. Every running change should be traceable to a commit.

## 2. Pin everything

- Helm addons: exact `chart_version`, never floating or `latest`.
- Container images: immutable tags or digests, never `latest`.

A commit should fully determine what runs.

## 3. Variables, not hardcoded values

Promote environment-specific values (domains, sizes, replica counts, storage classes) to variables. The same stack definition should work across dev, staging, and prod by changing variables, not by forking the YAML.

## 4. Small, focused, composable stacks

One concern per stack (ingress, monitoring, logging). Small stacks are easier to order, clone, and reason about than a single mega-stack. Use dependency `parents` to express order explicitly; deploy namespace manifests before anything inside them.

## 5. Least-privilege credentials and secrets

- Scope Git, registry, and cloud credentials to the minimum needed.
- Never commit plaintext Secrets; encrypt with SOPS and mark `encrypted_paths`.
- Keep API tokens in the secret store / environment, never in the repo. Use short-lived, scoped tokens for automation.

## 6. Promote through environments

Roll a change out to dev/staging first, verify, then promote the identical definition to production. Production uses the most conservative, pinned configuration.

## 7. Operations are idempotent and retry-safe

Ankra operations can re-run. Design changes so reapplying is safe: declarative manifests, no side effects that break on a second apply, no reliance on one-shot imperative steps.

## 8. Confirm destructive actions

Treat `delete`, `deprovision`, `roll-to`, and force operations as deliberate. Confirm the target cluster/org first (`ankra cluster info`) and prefer a reviewed PR over an ad-hoc command for anything irreversible.

## 9. Review before deploy

Protect synced branches, require pull-request review, and keep diffs small. A merge is a deploy — review it like one.

## Related skills

These principles are applied concretely in `ankra-import-cluster`, `ankra-stacks-addons`, `ankra-gitops`, `ankra-cicd`, and `ankra-sops-secrets`.
